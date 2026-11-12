// Command admin runs the management backend (API + embedded frontend).
package main

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"log"
	"net/http"
	"os"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"newapisub/internal/admin"
	"newapisub/internal/audit"
	"newapisub/internal/auth"
	"newapisub/internal/channels"
	"newapisub/internal/config"
	"newapisub/internal/connection"
	"newapisub/internal/httpx"
	"newapisub/internal/logstore"
	"newapisub/internal/newapi"
	"newapisub/internal/secret"
	"newapisub/internal/security"
	"newapisub/internal/store"
	adminweb "newapisub/web/admin"
)

const (
	watchEvery      = 10 * time.Second
	retryEvery      = 30 * time.Second // a pool that is down, or MongoDB, is opened again
	pushEvery       = 5 * time.Second
	workerLockEvery = 30 * time.Second // how often the worker lock is checked
)

func main() {
	var err error
	switch {
	case len(os.Args) == 1:
		err = run()
	case len(os.Args) == 3 && os.Args[1] == "reset-2fa":
		err = reset2FA(os.Args[2])
	default:
		err = errors.New("usage: admin [reset-2fa <username>]")
	}
	if err != nil {
		log.Fatalf("admin: %v", err)
	}
}

// reset2FA is the way back in for a super who lost every factor. It needs only
// the database, so it works while the server cannot start or the master key is lost.
func reset2FA(username string) error {
	dsn, err := config.LoadAppDB()
	if err != nil {
		return fmt.Errorf("config: %w", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	st, err := store.Open(ctx, dsn)
	if err != nil {
		return err
	}
	defer st.Close()
	users := st.AdminUsers()
	u, err := users.ByName(ctx, username)
	if err != nil {
		return fmt.Errorf("admin %q: %w", username, err)
	}
	if err := users.ResetFactors(ctx, u.ID); err != nil {
		return err
	}
	detail, err := json.Marshal(map[string]any{"username": u.Username})
	if err != nil {
		return err
	}
	if err := st.InsertAudit(ctx, store.AuditLog{ActorType: "admin", ActorName: "cli", Action: "admin.2fa_reset", TargetType: "admin",
		TargetID: strconv.FormatInt(u.ID, 10), Success: true, Detail: detail}); err != nil {
		return err
	}
	log.Printf("reset the second factors of admin %q; their sessions are logged out", u.Username)
	return nil
}

// bootstrap creates the first admin, a super, when admin_users is empty. Unless
// env names the account it is random, and this log is the only place showing it.
func bootstrap(ctx context.Context, st *store.Store, cfg config.Admin) error {
	n, err := st.AdminUsers().Count(ctx)
	if err != nil || n > 0 {
		return err
	}
	username, password := cfg.BootstrapUsername, cfg.BootstrapPassword
	random := username == "" && password == ""
	if random {
		username = "admin_" + strings.ToLower(rand.Text()[:8])
		password = rand.Text()
	}
	if username == "" || password == "" {
		return errors.New("ADMIN_BOOTSTRAP_USERNAME and ADMIN_BOOTSTRAP_PASSWORD must be set together")
	}
	if !regexp.MustCompile(`^[A-Za-z0-9_.-]{3,32}$`).MatchString(username) {
		return errors.New("ADMIN_BOOTSTRAP_USERNAME must match ^[A-Za-z0-9_.-]{3,32}$")
	}
	if err := auth.ValidatePassword(password); err != nil {
		return fmt.Errorf("ADMIN_BOOTSTRAP_PASSWORD: %w", err)
	}
	hash, err := auth.HashPassword(password)
	if err != nil {
		return err
	}
	if _, err := st.CreateAdmin(ctx, username, hash, store.RoleSuper); err != nil {
		if errors.Is(err, store.ErrConflict) {
			return nil // another instance got there first
		}
		return fmt.Errorf("create bootstrap admin: %w", err)
	}
	if random {
		log.Printf("首次启动，已随机生成超级管理员（只显示这一次，登录后请在头像菜单修改）：用户名 %s 密码 %s", username, password)
		return nil
	}
	log.Printf("created bootstrap admin %q", username)
	return nil
}

func run() error {
	cfg, err := config.LoadAdmin()
	if err != nil {
		return fmt.Errorf("config: %w", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	st, err := store.Open(ctx, cfg.AppDBDSN)
	if err != nil {
		return err
	}
	defer st.Close()
	box, err := secret.New(cfg.MasterKey)
	if err != nil {
		return err
	}
	am := &auth.Manager{Cookie: "admin_session", Secret: cfg.SessionSecret, Secure: cfg.CookieSecure, Lookup: st.AdminUsers().Session}
	rec := audit.Recorder{Store: st, ActorType: "admin", TrustedProxies: cfg.TrustedProxies}
	// Ends the process with exit code 0 when the pools change; restart: unless-stopped brings it back.
	root, restart := context.WithCancel(context.Background())
	defer restart()
	// MongoDB being down only leaves out the logs of the pools that sync them.
	var logs *logstore.Lazy
	var synced func(poolID int64) newapi.LogSource
	if cfg.Mongo.URI != "" {
		logs = logstore.Dial(root, cfg.Mongo.URI, cfg.Mongo.DB, retryEvery)
		defer logs.Close(context.Background())
		synced = logs.Reader
	} else if err := noSync(ctx, st); err != nil {
		return err
	}
	srv := &admin.Server{Store: st, Auth: am, Limiter: auth.NewLimiter(), Audit: rec, Box: box, Mongo: logs}
	// Without any pool only a super's way to add one is served; one that is down only leaves its part out.
	pools, fingerprint, err := connection.Load(ctx, st, box, "admin", synced)
	switch {
	case errors.Is(err, connection.ErrUnavailable):
		srv.Restricted, srv.RestrictedReason = true, err.Error()
		log.Printf("restricted mode: %v", err)
	case err != nil:
		return err
	default:
		defer pools.Close()
		srv.Pools = pools
	}
	name, err := st.Setting(ctx, store.SettingAdminName)
	if err != nil {
		return err
	}
	if srv.Security, err = security.New(st.AdminUsers(), am, rec, box, cfg.PublicOrigin, name); err != nil {
		return err
	}
	// Last, so a start that fails on its configuration does not use up the one
	// time a random account is shown.
	if err := bootstrap(ctx, st, cfg); err != nil {
		return err
	}

	dist, err := fs.Sub(adminweb.Dist, "dist")
	if err != nil {
		return err
	}
	spa, err := httpx.SPA(dist)
	if err != nil {
		return err
	}
	mux := http.NewServeMux()
	srv.Register(mux)
	mux.Handle("/", spa)

	go connection.Watch(root, watchEvery, st.PoolsVersion, fingerprint, restart)
	if pools != nil {
		go pools.Retry(root, retryEvery, st, box)
		pusher := &channels.Pusher{Store: st, Pools: pools, Box: box}
		jobs := []func(context.Context){func(ctx context.Context) { pusher.Run(ctx, pushEvery) }}
		if logs != nil {
			jobs = append(jobs, (&logstore.Syncer{St: st, Logs: logs, Pool: exporter(pools)}).Run)
		}
		go workers(root, st, jobs...)
	}
	return httpx.Serve(root, cfg.ListenAddr, httpx.Wrap(mux))
}

// noSync fails a start without MONGO_URI while a pool's sync is on: it would
// silently stop.
func noSync(ctx context.Context, st *store.Store) error {
	syncs, err := st.PoolSyncs(ctx)
	if err != nil {
		return err
	}
	for _, ps := range syncs {
		if ps.Enabled {
			return fmt.Errorf("pool %d syncs its logs into MongoDB: MONGO_URI is required", ps.PoolID)
		}
	}
	return nil
}

// exporter is the new-api of a pool to sync from, nil while it is down.
func exporter(pools *connection.Pools) func(int64) logstore.Exporter {
	return func(id int64) logstore.Exporter {
		if p := pools.Get(id); p != nil && p.Reason == "" {
			return p.API
		}
		return nil
	}
}

// workers runs jobs while this process holds the worker lock, stops them as
// soon as it loses it, and asks for the lock again while another one does.
func workers(ctx context.Context, st *store.Store, jobs ...func(context.Context)) {
	for {
		lock, err := st.TryLock(ctx, store.WorkerLock)
		if err != nil && ctx.Err() == nil {
			log.Printf("workers: take the lock: %v", err)
		}
		if lock != nil {
			running, stop := context.WithCancel(ctx)
			var wg sync.WaitGroup
			for _, job := range jobs {
				wg.Go(func() { job(running) })
			}
			for wait(ctx, workerLockEvery) && lock.Check(ctx) == nil {
			}
			if ctx.Err() == nil {
				log.Print("workers: lost the worker lock, stopped until it is taken again")
			}
			stop()
			wg.Wait()
			lock.Release()
		}
		if !wait(ctx, workerLockEvery) {
			return
		}
	}
}

// wait sleeps for d and says whether ctx is still on.
func wait(ctx context.Context, d time.Duration) bool {
	select {
	case <-ctx.Done():
		return false
	case <-time.After(d):
		return true
	}
}
