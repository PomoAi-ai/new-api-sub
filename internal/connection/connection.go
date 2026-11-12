// Package connection opens new-api from the configuration stored in the app
// database. Nothing it returns or logs comes from an underlying error: those
// quote DSNs, URLs and new-api's answers, so errors are sorted into fixed texts.
package connection

import (
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net"
	"net/http"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/go-sql-driver/mysql"
	"github.com/jackc/pgx/v5/pgconn"

	"newapisub/internal/config"
	"newapisub/internal/httpx"
	"newapisub/internal/keyprobe"
	"newapisub/internal/newapi"
	"newapisub/internal/secret"
	"newapisub/internal/store"
)

const (
	// openTimeout leaves the caller's startup context some time for what follows.
	openTimeout  = 20 * time.Second
	checkTimeout = 30 * time.Second
)

// The sealed columns of a pool. Each ciphertext is bound to pool:<id>:<column>,
// so that it cannot stand in for another column or another pool's.
const (
	colPAT = "pat"
	colDB  = "db_dsn"
	colLog = "log_dsn"
	colKey = "key_dsn"
)

func field(row store.Pool, column string) string {
	if row.AADLegacy { // copied from newapi_connection, sealed under its names
		return "newapi." + column
	}
	return "pool:" + strconv.FormatInt(row.ID, 10) + ":" + column
}

// ErrUnavailable means no pool is stored. Its text is safe to show: the admin
// runs restricted on it, the portal exits with it.
var ErrUnavailable = errors.New("new-api 连接不可用")

func unavailable(reason string) error { return fmt.Errorf("%w：%s", ErrUnavailable, reason) }

// reasonDecrypt is the fixed text of a pool that cannot be used for what is stored.
const reasonDecrypt = "配置无法解密，主密钥可能已更换，请重新填写全部连接信息"

// Pool is one new-api as the processes use it. Reason is a fixed text saying
// why it cannot be used, and then only the fields up to QuotaPerUnit (as last
// stored, 0 if never read) are set. DB is nil for a pool without a database,
// Probe without a key connection. Logs is the DB, or for a pool that syncs its
// logs the sync store.
type Pool struct {
	ID           int64
	Name         string
	LogMode      string
	AcceptNew    bool
	QuotaPerUnit float64
	Reason       string
	API          *newapi.Client
	DB           *newapi.DB
	Probe        *keyprobe.Prober
	Logs         newapi.LogSource
}

func (p *Pool) close() {
	if p.DB != nil {
		p.DB.Close()
	}
	if p.Probe != nil {
		p.Probe.Close()
	}
}

// slot holds a pool as it is now: Retry replaces an unavailable one in place.
type slot struct {
	id, version int64 // of the row Load read
	p           atomic.Pointer[Pool]
}

// Pools is every stored pool, by id. Take List once per request: a pool can
// come back between two calls.
type Pools struct {
	slots []*slot
	mongo func(poolID int64) newapi.LogSource // the synced logs of a pool
}

func (p *Pools) List() []*Pool {
	out := make([]*Pool, len(p.slots))
	for i, s := range p.slots {
		out[i] = s.p.Load()
	}
	return out
}

// Get is nil for an id that is not a pool.
func (p *Pools) Get(id int64) *Pool {
	for _, s := range p.slots {
		if s.id == id {
			return s.p.Load()
		}
	}
	return nil
}

// Home is the pool with the smallest id, whose quota unit the pages show.
func (p *Pools) Home() *Pool { return p.slots[0].p.Load() }

func (p *Pools) Close() {
	for _, s := range p.slots {
		s.p.Load().close()
	}
}

// openPool opens row; how new-api's side fails is the pool's Reason. A legacy
// row is sealed again under its own field names. The error is the app database.
// mongo is not nil for a row that syncs its logs: Load sees to it.
func openPool(ctx context.Context, st *store.Store, box *secret.Box, row store.Pool, mongo func(int64) newapi.LogSource) (*Pool, error) {
	p := &Pool{ID: row.ID, Name: row.Name, LogMode: row.LogMode, AcceptNew: row.AcceptNew, QuotaPerUnit: row.QuotaPerUnit}
	c, err := Decrypt(box, row)
	if err != nil {
		p.Reason = reasonDecrypt
		return p, nil
	}
	if row.AADLegacy {
		if err := st.ResealPool(ctx, seal(box, row.ID, c)); err != nil {
			return nil, err
		}
	}
	if err := openSet(ctx, c, p); err != nil {
		p.Reason = reason(err)
		return p, nil
	}
	if row.LogMode == "sync" {
		p.Logs = mongo(row.ID)
	}
	if err := st.SetPoolQPU(ctx, row.ID, p.QuotaPerUnit); err != nil {
		p.close()
		return nil, err
	}
	return p, nil
}

func openSet(ctx context.Context, c config.NewAPI, p *Pool) error {
	ctx, cancel := context.WithTimeout(ctx, openTimeout)
	defer cancel()
	api := newapi.NewClient(c.BaseURL, c.PAT)
	if err := api.Ping(ctx); err != nil {
		return err
	}
	qpu, err := api.Status(ctx)
	if err != nil {
		return err
	}
	p.API, p.QuotaPerUnit = api, qpu
	if c.DBDSN == "" { // a pool that syncs its logs, read through the API alone
		return nil
	}
	db, err := newapi.OpenDB(ctx, c.DBDSN, cmp.Or(c.LogDBDSN, c.DBDSN))
	if err != nil {
		return err
	}
	var probe *keyprobe.Prober
	if c.KeyDBDSN != "" {
		if probe, err = keyprobe.Open(ctx, c.KeyDBDSN); err != nil {
			db.Close()
			return err
		}
	}
	p.DB, p.Probe, p.Logs = db, probe, db
	return nil
}

// Load imports the NEWAPI_* variables as pool 1 when no pool was ever stored,
// then opens every pool at once: one that fails only has its Reason. actor is
// the process, for the audit log. fingerprint is of the rows it opened, what
// Watch must compare to: one read apart could already be newer. mongo is the
// synced logs of a pool, nil without MONGO_URI: then a pool that syncs its logs
// fails Load. Without pools it is ErrUnavailable; any other error is the app
// database or that.
func Load(ctx context.Context, st *store.Store, box *secret.Box, actor string, mongo func(poolID int64) newapi.LogSource) (pools *Pools, fingerprint string, err error) {
	rows, err := st.Pools(ctx)
	if err != nil {
		return nil, "", err
	}
	env, hasEnv := config.NewAPIFromEnv()
	switch {
	case len(rows) == 0 && hasEnv:
		if err := importEnv(ctx, st, box, env, actor); err != nil {
			return nil, "", err
		}
		if rows, err = st.Pools(ctx); err != nil {
			return nil, "", err
		}
	case hasEnv:
		log.Print("NEWAPI_* environment variables ignored: the pools are stored in the database")
	}
	versions := make([]string, len(rows))
	for i, row := range rows {
		versions[i] = fmt.Sprintf("%d:%d", row.ID, row.Version)
	}
	fingerprint = strings.Join(versions, ",") // as Store.PoolsVersion writes it
	if len(rows) == 0 {
		return nil, fingerprint, unavailable("尚未配置")
	}
	for _, row := range rows {
		if row.LogMode == "sync" && mongo == nil {
			return nil, "", fmt.Errorf("pool %d reads its logs from MongoDB: MONGO_URI is required", row.ID)
		}
	}
	pools = &Pools{slots: make([]*slot, len(rows)), mongo: mongo}
	errs := make([]error, len(rows))
	var wg sync.WaitGroup
	for i, row := range rows {
		s := &slot{id: row.ID, version: row.Version}
		pools.slots[i] = s
		wg.Go(func() {
			p, err := openPool(ctx, st, box, row, mongo)
			if err != nil {
				p = &Pool{ID: row.ID}
			}
			s.p.Store(p)
			errs[i] = err
		})
	}
	wg.Wait()
	if err := errors.Join(errs...); err != nil {
		pools.Close()
		return nil, "", err
	}
	return pools, fingerprint, nil
}

func importEnv(ctx context.Context, st *store.Store, box *secret.Box, c config.NewAPI, actor string) error {
	if err := c.Validate(false); err != nil {
		return fmt.Errorf("NEWAPI_* environment variables: %w", err)
	}
	row := seal(box, 1, c)
	row.Name, row.UpdatedBy = "号池 1", "import"
	inserted, err := st.ImportPool(ctx, row)
	if err != nil || !inserted {
		return err
	}
	detail, err := json.Marshal(map[string]any{"fields": setFields(c)})
	if err != nil {
		return err
	}
	if err := st.InsertAudit(ctx, store.AuditLog{ActorType: actor, ActorName: "system", Action: "settings.newapi_import",
		TargetType: "settings", Success: true, Detail: detail}); err != nil {
		log.Printf("audit settings.newapi_import: %v", err)
	}
	return nil
}

// setFields names what c holds, for the audit log: only ever the names.
func setFields(c config.NewAPI) []string {
	fields := []string{"base_url", "pat", "db_dsn"}
	if c.LogDBDSN != "" {
		fields = append(fields, "log_dsn")
	}
	if c.KeyDBDSN != "" {
		fields = append(fields, "key_dsn")
	}
	return fields
}

// seal is c as the row of pool id; the other columns are the caller's.
func seal(box *secret.Box, id int64, c config.NewAPI) store.Pool {
	row := store.Pool{ID: id, BaseURL: c.BaseURL}
	for _, f := range []struct {
		column, plain string
		dst           *[]byte
	}{{colPAT, c.PAT, &row.PAT}, {colDB, c.DBDSN, &row.DBDSN}, {colLog, c.LogDBDSN, &row.LogDSN}, {colKey, c.KeyDBDSN, &row.KeyDSN}} {
		if f.plain != "" {
			*f.dst = box.NewAPI.Seal(field(row, f.column), []byte(f.plain))
		}
	}
	return row
}

// Decrypt opens a stored row.
func Decrypt(box *secret.Box, row store.Pool) (config.NewAPI, error) {
	c := config.NewAPI{BaseURL: row.BaseURL}
	for _, f := range []struct {
		column string
		sealed []byte
		dst    *string
	}{{colPAT, row.PAT, &c.PAT}, {colDB, row.DBDSN, &c.DBDSN}, {colLog, row.LogDSN, &c.LogDBDSN}, {colKey, row.KeyDSN, &c.KeyDBDSN}} {
		if len(f.sealed) == 0 {
			continue
		}
		plain, err := box.NewAPI.Open(field(row, f.column), f.sealed)
		if err != nil {
			return config.NewAPI{}, err
		}
		*f.dst = string(plain)
	}
	return c, nil
}

// Input is what the pool form sends. An empty value keeps the stored one, and
// so does a nil AcceptNew.
type Input struct {
	Name                 string `json:"name"`
	LogMode              string `json:"log_mode"`
	AcceptNew            *bool  `json:"accept_new"`
	BaseURL              string `json:"base_url"`
	PAT                  string `json:"pat"`
	DBDSN                string `json:"db_dsn"`
	LogDBDSN             string `json:"log_dsn"`
	KeyDBDSN             string `json:"key_dsn"`
	ClearLogDSN          bool   `json:"clear_log_dsn"`
	ClearKeyDSN          bool   `json:"clear_key_dsn"`
	ConfirmAddressChange bool   `json:"confirm_address_change"`
}

// Merged is an Input applied to a stored pool, or to none for a new one.
type Merged struct {
	ID        int64 // 0 for a new pool
	Name      string
	LogMode   string
	AcceptNew bool
	Config    config.NewAPI
	Version   int64    // of the row it was merged onto
	Fields    []string // names of what changes
}

// Merge applies in to pool id, or to a new pool for id 0. Moving the address
// needs the PAT again and an explicit confirmation: the stored PAT must never go
// to a new address, and channel ids mean nothing on another new-api. A row that
// cannot be decrypted has nothing to keep, so everything it held must be sent
// again. Two pools on one new-api would count its logs twice. Every error is
// fit for the client.
func Merge(ctx context.Context, st *store.Store, box *secret.Box, id int64, in Input) (Merged, error) {
	in.Name, in.LogMode = strings.TrimSpace(in.Name), strings.TrimSpace(in.LogMode)
	in.PAT, in.DBDSN = strings.TrimSpace(in.PAT), strings.TrimSpace(in.DBDSN)
	in.LogDBDSN, in.KeyDBDSN = strings.TrimSpace(in.LogDBDSN), strings.TrimSpace(in.KeyDBDSN)
	rows, err := st.Pools(ctx)
	if err != nil {
		return Merged{}, err
	}
	row := store.Pool{LogMode: "direct", AcceptNew: true}
	exists := id != 0
	if exists {
		i := slices.IndexFunc(rows, func(r store.Pool) bool { return r.ID == id })
		if i < 0 {
			return Merged{}, httpx.Errorf(http.StatusNotFound, "号池不存在")
		}
		row = rows[i]
	}
	old, err := Decrypt(box, row)
	if exists && err != nil {
		old = config.NewAPI{BaseURL: row.BaseURL}
		if in.PAT == "" || (len(row.DBDSN) > 0 && in.DBDSN == "") || (len(row.LogDSN) > 0 && in.LogDBDSN == "" && !in.ClearLogDSN) ||
			(len(row.KeyDSN) > 0 && in.KeyDBDSN == "" && !in.ClearKeyDSN) {
			return Merged{}, httpx.Errorf(http.StatusBadRequest, "已保存的配置无法解密，请重新填写全部连接信息")
		}
	}
	next := old
	if v := config.NormalizeBaseURL(in.BaseURL); v != "" {
		next.BaseURL = v
	}
	if exists && next.BaseURL != old.BaseURL {
		if in.PAT == "" {
			return Merged{}, httpx.Errorf(http.StatusBadRequest, "修改地址时必须同时填写新的 PAT")
		}
		if !in.ConfirmAddressChange {
			return Merged{}, httpx.Errorf(http.StatusBadRequest, "修改地址须确认：换到另一个 new-api 后已有渠道会对不上")
		}
	}
	next.PAT, next.DBDSN = cmp.Or(in.PAT, next.PAT), cmp.Or(in.DBDSN, next.DBDSN)
	next.LogDBDSN, next.KeyDBDSN = cmp.Or(in.LogDBDSN, next.LogDBDSN), cmp.Or(in.KeyDBDSN, next.KeyDBDSN)
	if in.ClearLogDSN {
		next.LogDBDSN = ""
	}
	if in.ClearKeyDSN {
		next.KeyDBDSN = ""
	}
	m := Merged{ID: id, Name: cmp.Or(in.Name, row.Name), LogMode: cmp.Or(in.LogMode, row.LogMode), AcceptNew: row.AcceptNew,
		Config: next, Version: row.Version}
	if err := next.Validate(m.LogMode == "sync"); err != nil {
		return Merged{}, httpx.Errorf(http.StatusBadRequest, "%s", strings.ReplaceAll(err.Error(), "\n", "；"))
	}
	if in.AcceptNew != nil {
		m.AcceptNew = *in.AcceptNew
	}
	if n := utf8.RuneCountInString(m.Name); n < 1 || n > 40 || strings.ContainsFunc(m.Name, unicode.IsControl) {
		return Merged{}, httpx.Errorf(http.StatusBadRequest, "号池名称须为 1–40 个字符且不含控制字符")
	}
	if m.LogMode != "direct" && m.LogMode != "sync" {
		return Merged{}, httpx.Errorf(http.StatusBadRequest, "日志模式只能为 direct 或 sync")
	}
	for _, r := range rows {
		switch {
		case r.ID == id:
		case r.Name == m.Name:
			return Merged{}, httpx.Errorf(http.StatusBadRequest, "号池名称已被使用")
		case strings.EqualFold(r.BaseURL, next.BaseURL):
			return Merged{}, httpx.Errorf(http.StatusBadRequest, "该 new-api 已配置为号池「%s」，同一个 new-api 不能配置两次", r.Name)
		}
	}
	for _, f := range []struct {
		name     string
		was, now string
	}{{"name", row.Name, m.Name}, {"log_mode", row.LogMode, m.LogMode}, {"accept_new", strconv.FormatBool(row.AcceptNew), strconv.FormatBool(m.AcceptNew)},
		{"base_url", old.BaseURL, next.BaseURL}, {"pat", old.PAT, next.PAT}, {"db_dsn", old.DBDSN, next.DBDSN},
		{"log_dsn", old.LogDBDSN, next.LogDBDSN}, {"key_dsn", old.KeyDBDSN, next.KeyDBDSN}} {
		if f.was != f.now {
			m.Fields = append(m.Fields, f.name)
		}
	}
	return m, nil
}

// Save stores m, a new pool for ID 0, and returns its id; both processes
// restart on it. ErrConflict: the pool changed meanwhile, or another took its
// name or address.
func Save(ctx context.Context, st *store.Store, box *secret.Box, m Merged, by string) (int64, error) {
	id := m.ID
	if id == 0 {
		var err error
		if id, err = st.NextPoolID(ctx); err != nil {
			return 0, err
		}
	}
	row := seal(box, id, m.Config)
	row.Name, row.LogMode, row.AcceptNew, row.Version, row.UpdatedBy = m.Name, m.LogMode, m.AcceptNew, m.Version, by
	if m.ID == 0 {
		return id, st.CreatePool(ctx, row)
	}
	return id, st.SavePool(ctx, row)
}

// Step is one stage of Check. Message is a fixed text.
type Step struct {
	Name    string `json:"name"`
	OK      bool   `json:"ok"`
	Message string `json:"message,omitempty"`
}

// Check tries m stage by stage and stops at the first failure. It runs next to
// the processes' own pools, so each of its pools holds one connection: README's
// role limits count those. A pool that syncs its logs must be allowed to export
// them (log:read). Last, the newest log of m is looked for among others, the
// pools of this process: found, m is one of them again.
func Check(ctx context.Context, m Merged, others []*Pool) (steps []Step, ok bool) {
	ctx, cancel := context.WithTimeout(ctx, checkTimeout)
	defer cancel()
	step := func(name string, err error) bool {
		s := Step{Name: name, OK: err == nil}
		if err != nil {
			s.Message = reason(err)
		}
		steps = append(steps, s)
		return s.OK
	}
	c, since := m.Config, time.Now().Unix()-86400
	api := newapi.NewClient(c.BaseURL, c.PAT)
	if !step("管理接口", api.Ping(ctx)) {
		return steps, false
	}
	if _, err := api.Status(ctx); !step("状态接口", err) {
		return steps, false
	}
	var latest *newapi.Log
	if c.DBDSN != "" {
		db, err := newapi.OpenDB(ctx, c.DBDSN, cmp.Or(c.LogDBDSN, c.DBDSN))
		if !step("主库和日志库", err) {
			return steps, false
		}
		defer db.Close()
		db.SetMaxOpenConns(1)
		if c.KeyDBDSN != "" && !checkKeys(ctx, c, db, step, &steps) {
			return steps, false
		}
		if m.LogMode == "direct" {
			latest, err = newest(func(fn func(newapi.Log) error) error {
				return db.ExportLogs(ctx, newapi.Filter{AllChannels: true, Start: since, End: since + 86401}, false, fn)
			})
			if !step("读取最新日志", err) {
				return steps, false
			}
		}
	}
	if m.LogMode == "sync" {
		var err error
		latest, err = newest(func(fn func(newapi.Log) error) error {
			_, _, err := api.ExportLogs(ctx, since, since+86400, fn)
			return err
		})
		if !step("日志导出权限", err) {
			return steps, false
		}
	}
	s := duplicate(ctx, m.ID, latest, others)
	return append(steps, s), s.OK
}

func checkKeys(ctx context.Context, c config.NewAPI, db *newapi.DB, step func(string, error) bool, steps *[]Step) bool {
	probe, err := keyprobe.Open(ctx, c.KeyDBDSN)
	if !step("key 库", err) {
		return false
	}
	defer probe.Close()
	probe.SetMaxOpenConns(1)
	if !step("key 读取", probe.Check(ctx)) {
		return false
	}
	reads, err := db.ReadsKeys(ctx)
	if err != nil {
		return step("key 隔离", err)
	}
	if reads {
		*steps = append(*steps, Step{Name: "key 隔离", Message: "主库或日志库的角色也能读取 key，须改用不能读 key 的角色"})
		return false
	}
	return step("key 隔离", nil)
}

var errStop = errors.New("stop")

// newest is the first log read gives, which is the newest, nil for none.
func newest(read func(fn func(newapi.Log) error) error) (*newapi.Log, error) {
	var got *newapi.Log
	err := read(func(l newapi.Log) error { got = &l; return errStop })
	if errors.Is(err, errStop) {
		err = nil
	}
	return got, err
}

// duplicate looks for l, the newest log of pool id, among the logs of others:
// the same new-api already configured as another pool. A pool that cannot be
// asked is left out.
func duplicate(ctx context.Context, id int64, l *newapi.Log, others []*Pool) Step {
	s := Step{Name: "重复检测", OK: true}
	if l == nil {
		s.Message = "最近 24 小时没有日志，未能比对"
		return s
	}
	f := newapi.Filter{AllChannels: true, Start: l.CreatedAt, End: l.CreatedAt + 1, RequestID: l.RequestID, Page: 1, PageSize: 20}
	for _, p := range others {
		if p.ID == id || p.Reason != "" || p.Logs == nil {
			continue
		}
		logs, _, err := p.Logs.Logs(ctx, f, false)
		if err != nil {
			log.Printf("duplicate check against pool %d: %v", p.ID, err)
			s.Message = "部分号池无法比对"
			continue
		}
		if slices.ContainsFunc(logs, func(o newapi.Log) bool { return o.ID == l.ID }) {
			return Step{Name: s.Name, Message: fmt.Sprintf("该 new-api 的最新日志也在号池「%s」中：同一个 new-api 不能配置两次", p.Name)}
		}
	}
	return s
}

// reason turns an error of the connection into a fixed text. It must not grow a
// %v or a %w: see the package comment.
func reason(err error) string {
	var rejected *newapi.Rejected
	var pg *pgconn.PgError
	var my *mysql.MySQLError
	var op *net.OpError
	switch {
	case errors.As(err, &rejected):
		if rejected.Status == http.StatusUnauthorized || rejected.Status == http.StatusForbidden {
			return "new-api 拒绝了 PAT，请检查令牌和权限"
		}
		return "new-api 拒绝了请求"
	case errors.As(err, &pg):
		switch pg.Code {
		case "28P01", "28000":
			return "数据库用户名或密码错误"
		case "3D000":
			return "数据库不存在"
		case "42501":
			return "数据库角色权限不足"
		case "53300":
			return "数据库连接数已满"
		}
		return "数据库返回错误"
	case errors.As(err, &my):
		switch my.Number {
		case 1045:
			return "数据库用户名或密码错误"
		case 1049:
			return "数据库不存在"
		case 1142, 1143:
			return "数据库角色权限不足"
		case 1040, 1203:
			return "数据库连接数已满"
		}
		return "数据库返回错误"
	case errors.Is(err, context.DeadlineExceeded):
		return "连接超时"
	case errors.As(err, &op):
		if op.Timeout() {
			return "连接超时"
		}
		return "无法连接到服务器"
	}
	return "连接失败"
}

// Host is the part of a DSN the settings page may show: host:port/database,
// without the user and password. It is empty for what it cannot read.
func Host(dsn string) string {
	if dsn == "" {
		return ""
	}
	if strings.HasPrefix(dsn, "postgres://") || strings.HasPrefix(dsn, "postgresql://") {
		u, err := url.Parse(dsn)
		if err != nil {
			return ""
		}
		return u.Host + u.Path
	}
	cfg, err := mysql.ParseDSN(dsn)
	if err != nil {
		return ""
	}
	return cfg.Addr + "/" + cfg.DBName
}

// Watch calls stop when the stored pools leave fingerprint: one is added,
// removed or saved. A failed read is logged and tried again: the app database
// going away must not restart anything.
func Watch(ctx context.Context, every time.Duration, read func(context.Context) (string, error), fingerprint string, stop func()) {
	tick := time.NewTicker(every)
	defer tick.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
		}
		v, err := read(ctx)
		if err != nil {
			if ctx.Err() == nil {
				log.Printf("read the new-api pool versions: %v", err)
			}
			continue
		}
		if v != fingerprint {
			log.Printf("new-api pools changed (%q -> %q), restarting", fingerprint, v)
			stop()
			return
		}
	}
}

// Retry opens again, every every, each pool that is unavailable, and puts it in
// place once it opens. Only the row Load read: a changed one is Watch's.
func (p *Pools) Retry(ctx context.Context, every time.Duration, st *store.Store, box *secret.Box) {
	tick := time.NewTicker(every)
	defer tick.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
		}
		for _, s := range p.slots {
			if s.p.Load().Reason == "" {
				continue
			}
			row, err := st.Pool(ctx, s.id)
			if err != nil || row.Version != s.version {
				continue
			}
			next, err := openPool(ctx, st, box, row, p.mongo)
			if err != nil {
				if ctx.Err() == nil {
					log.Printf("reopen new-api pool %d: %v", s.id, err)
				}
				continue
			}
			if next.Reason == "" {
				s.p.Store(next)
				log.Printf("new-api pool %d is back", s.id)
			}
		}
	}
}
