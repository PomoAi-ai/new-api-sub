// Command portal runs the supplier portal (API + embedded frontend).
package main

import (
	"context"
	"fmt"
	"io/fs"
	"log"
	"net/http"
	"time"

	"newapisub/internal/audit"
	"newapisub/internal/auth"
	"newapisub/internal/config"
	"newapisub/internal/connection"
	"newapisub/internal/httpx"
	"newapisub/internal/logstore"
	"newapisub/internal/newapi"
	"newapisub/internal/portal"
	"newapisub/internal/secret"
	"newapisub/internal/security"
	"newapisub/internal/store"
	portalweb "newapisub/web/portal"
)

const (
	watchEvery = 10 * time.Second
	retryEvery = 30 * time.Second // a pool that is down is opened again
)

func main() {
	if err := run(); err != nil {
		log.Fatalf("portal: %v", err)
	}
}

func run() error {
	cfg, err := config.LoadPortal()
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
	// Ends the process with exit code 0 when the pools change; restart: unless-stopped brings it back.
	root, restart := context.WithCancel(context.Background())
	defer restart()
	// MongoDB being down only leaves out the logs of the pools that sync them.
	var synced func(poolID int64) newapi.LogSource
	if cfg.Mongo.URI != "" {
		logs := logstore.Dial(root, cfg.Mongo.URI, cfg.Mongo.DB, retryEvery)
		defer logs.Close(context.Background())
		synced = logs.Reader
	}
	// Without any pool there is nothing to serve; one that is down only leaves its part out.
	pools, fingerprint, err := connection.Load(ctx, st, box, "portal", synced)
	if err != nil {
		return err
	}
	defer pools.Close()
	name, err := st.Setting(ctx, store.SettingPortalName)
	if err != nil {
		return err
	}

	dist, err := fs.Sub(portalweb.Dist, "dist")
	if err != nil {
		return err
	}
	spa, err := httpx.SPA(dist)
	if err != nil {
		return err
	}
	users := st.PortalUsers()
	am := &auth.Manager{Cookie: "portal_session", Secret: cfg.SessionSecret, Secure: cfg.CookieSecure, Lookup: users.Session}
	rec := audit.Recorder{Store: st, ActorType: "portal", TrustedProxies: cfg.TrustedProxies}
	sec, err := security.New(users, am, rec, box, cfg.PublicOrigin, name)
	if err != nil {
		return err
	}
	srv := &portal.Server{Store: st, Pools: pools, Box: box, Auth: am, Limiter: auth.NewLimiter(), Audit: rec, Security: sec}
	mux := http.NewServeMux()
	srv.Register(mux)
	mux.Handle("/", spa)

	go connection.Watch(root, watchEvery, st.PoolsVersion, fingerprint, restart)
	go pools.Retry(root, retryEvery, st, box)
	return httpx.Serve(root, cfg.ListenAddr, httpx.Wrap(mux))
}
