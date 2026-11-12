package channels_test

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"newapisub/internal/admin"
	"newapisub/internal/audit"
	"newapisub/internal/auth"
	"newapisub/internal/channels"
	"newapisub/internal/config"
	"newapisub/internal/connection"
	"newapisub/internal/logstore"
	"newapisub/internal/newapi"
	"newapisub/internal/portal"
	"newapisub/internal/secret"
	"newapisub/internal/security"
	"newapisub/internal/store"
)

// The columns of new-api's channels and logs tables that this system touches,
// typed as new-api's PostgreSQL migration creates them.
const newapiDDL = `
CREATE TABLE channels (
  id BIGSERIAL PRIMARY KEY, type BIGINT DEFAULT 0, key TEXT NOT NULL, test_model TEXT, status BIGINT DEFAULT 1,
  name TEXT, weight BIGINT DEFAULT 0, created_time BIGINT, test_time BIGINT, response_time BIGINT,
  base_url TEXT DEFAULT '', balance NUMERIC, balance_updated_time BIGINT, models TEXT,
  "group" VARCHAR(64) DEFAULT 'default', used_quota BIGINT DEFAULT 0, priority BIGINT DEFAULT 0,
  auto_ban BIGINT DEFAULT 1, other_info TEXT, tag TEXT, remark VARCHAR(255), channel_info JSON,
  model_mapping TEXT, param_override TEXT);
CREATE TABLE logs (
  id BIGSERIAL PRIMARY KEY, user_id BIGINT, created_at BIGINT, type BIGINT, content TEXT, username TEXT,
  token_name TEXT, model_name TEXT, quota BIGINT, prompt_tokens BIGINT, completion_tokens BIGINT, use_time BIGINT,
  is_stream BOOLEAN, channel_id BIGINT, token_id BIGINT, "group" TEXT, ip TEXT, request_id VARCHAR(64), upstream_request_id VARCHAR(64), other TEXT);`

const (
	testQuotaPerUnit = 500000
	testPortalOrigin = "https://portal.example.test"
	testAdminOrigin  = "https://admin.example.test"
)

// call is one request new-api received.
type call struct {
	Method, Path, Query, Body string
	Create                    struct {
		Mode         string `json:"mode"`
		MultiKeyMode string `json:"multi_key_mode"`
		KeyPrefix    bool   `json:"batch_add_set_key_prefix_2_name"`
		Channel      struct {
			Type      int    `json:"type"`
			Key       string `json:"key"`
			Name      string `json:"name"`
			BaseURL   string `json:"base_url"`
			Models    string `json:"models"`
			Group     string `json:"group"`
			Priority  int64  `json:"priority"`
			Weight    int    `json:"weight"`
			TestModel string `json:"test_model"`
			Status    int    `json:"status"`
			Remark    string `json:"remark"`
			newapi.Extra
		} `json:"channel"`
	}
}

// fake is one new-api: the tables of its database and its management API.
type fake struct {
	t            *testing.T
	raw          *sql.DB
	dsn          string
	newAPI       *httptest.Server
	qpu          int // its quota per unit
	mu           sync.Mutex
	calls        []call
	idsInCreate  bool                // answer like new-api HEAD (data.ids) rather than rc.47 (no data)
	extraIDs     []int               // reported on top of the real ids
	breakCreate  bool                // new-api creates the channel, then the connection breaks
	rejectCreate bool                // new-api refuses to create a channel
	onCreate     func()              // runs as new-api creates a channel
	denied       string              // a read the PAT has no scope for
	notInCache   bool                // new-api's memory cache has not learned new channels yet
	rejectUpdate bool                // new-api refuses to edit a channel
	dropDelete   bool                // new-api breaks the connection on a hard delete
	rawBalance   string              // what update_balance answers for an advanced custom channel
	onStatus     func()              // runs as new-api answers /api/status
	keyStatus    map[int]map[int]int // channel -> key index -> status new-api's fake keeps
	refuseKeys   map[int]bool        // key indexes new-api refuses to switch
	chats        []string            // bodies of the model calls OpenRouter's fake got
}

// env is both systems against two pools. Pool 1 lives in the app's own schema,
// so e.raw reads its channels and logs next to the app's tables; pool 2 has a
// schema of its own, whose ids start again at 1: the two pools' ids collide.
type env struct {
	*fake
	p2           *fake
	t            *testing.T
	st           *store.Store
	pools        *connection.Pools
	portalMux    *http.ServeMux
	adminMux     *http.ServeMux
	portalAuth   *auth.Manager
	adminAuth    *auth.Manager
	portalSec    *security.Service
	adminSec     *security.Service
	box          *secret.Box
	mongo        *logstore.Lazy // the store of synced logs, nil unless useMongo
	dsn          string
	super, a, b  int64
	alice, bobby int64 // portal users of admins a and b
}

// schemaDSN is base in a new schema of its own, dropped with the test.
func schemaDSN(t *testing.T, admin0 *sql.DB, base string) string {
	t.Helper()
	nonce := make([]byte, 6)
	rand.Read(nonce)
	schema := "t_" + hex.EncodeToString(nonce)
	if _, err := admin0.Exec("CREATE SCHEMA " + schema); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { admin0.Exec("DROP SCHEMA " + schema + " CASCADE") })
	u, err := url.Parse(base)
	if err != nil {
		t.Fatal(err)
	}
	q := u.Query()
	q.Set("search_path", schema)
	u.RawQuery = q.Encode()
	return u.String()
}

func newFake(t *testing.T, dsn string, qpu int) *fake {
	t.Helper()
	f := &fake{t: t, dsn: dsn, qpu: qpu, keyStatus: map[int]map[int]int{}}
	var err error
	if f.raw, err = sql.Open("pgx", dsn); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { f.raw.Close() })
	if _, err := f.raw.Exec(newapiDDL); err != nil {
		t.Fatal(err)
	}
	f.newAPI = httptest.NewServer(f.fakeNewAPI())
	t.Cleanup(f.newAPI.Close)
	return f
}

// newEnv is bareEnv with both pools stored and loaded. Templates send channels
// to pool 1.
func newEnv(t *testing.T) *env {
	e := bareEnv(t)
	e.addPool("号池 1", e.fake, e.dsn) // the test database stands in for the key role: it can read channels.key
	e.addPool("号池 2", e.p2, "")
	e.serve()
	return e
}

// bareEnv has no pool stored: the admin backend runs restricted.
func bareEnv(t *testing.T) *env {
	t.Helper()
	base := os.Getenv("TEST_PG_DSN")
	if base == "" {
		t.Skip("TEST_PG_DSN not set")
	}
	ctx := context.Background()
	admin0, err := sql.Open("pgx", base)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { admin0.Close() })
	e := &env{t: t, dsn: schemaDSN(t, admin0, base)}
	e.fake = newFake(t, e.dsn, testQuotaPerUnit)
	e.p2 = newFake(t, schemaDSN(t, admin0, base), 2*testQuotaPerUnit)
	if e.st, err = store.Open(ctx, e.dsn); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { e.st.Close() })
	e.portalAuth = &auth.Manager{Cookie: "p", Secret: []byte("portal-secret-portal-secret-portal"), Lookup: e.st.PortalUsers().Session}
	e.adminAuth = &auth.Manager{Cookie: "a", Secret: []byte("admin-secret-admin-secret-admin-xx"), Lookup: e.st.AdminUsers().Session}
	if e.box, err = secret.New([]byte(rand.Text() + rand.Text())); err != nil {
		t.Fatal(err)
	}
	if e.portalSec, err = security.New(e.st.PortalUsers(), e.portalAuth, audit.Recorder{Store: e.st, ActorType: "portal"}, e.box, testPortalOrigin, "门户"); err != nil {
		t.Fatal(err)
	}
	if e.adminSec, err = security.New(e.st.AdminUsers(), e.adminAuth, audit.Recorder{Store: e.st, ActorType: "admin"}, e.box, testAdminOrigin, "后台"); err != nil {
		t.Fatal(err)
	}
	e.super = e.mustAdmin("root", store.RoleSuper)
	e.a = e.mustAdmin("admin-a", store.RoleAdmin)
	e.b = e.mustAdmin("admin-b", store.RoleAdmin)
	e.alice = e.mustUser("alice", e.a)
	e.bobby = e.mustUser("bobby", e.b)
	e.template(20, "https://openrouter.example.com", false)
	e.serve()
	return e
}

// addPool stores f as the next pool, as the pools page would.
func (e *env) addPool(name string, f *fake, keyDSN string) {
	e.t.Helper()
	m := connection.Merged{Name: name, LogMode: "direct", AcceptNew: true,
		Config: config.NewAPI{BaseURL: f.newAPI.URL, PAT: "nap_test", DBDSN: f.dsn, KeyDBDSN: keyDSN}}
	if _, err := connection.Save(context.Background(), e.st, e.box, m, "test"); err != nil {
		e.t.Fatal(err)
	}
}

// serve loads the stored pools and mounts both systems on them, as a start of
// the processes does.
func (e *env) serve() {
	e.t.Helper()
	var synced func(int64) newapi.LogSource
	if e.mongo != nil {
		synced = e.mongo.Reader
	}
	pools, _, err := connection.Load(context.Background(), e.st, e.box, "admin", synced)
	srv := &admin.Server{Store: e.st, Auth: e.adminAuth, Limiter: auth.NewLimiter(), Audit: audit.Recorder{Store: e.st, ActorType: "admin"},
		Security: e.adminSec, Box: e.box, Mongo: e.mongo}
	e.portalMux, e.adminMux = http.NewServeMux(), http.NewServeMux()
	switch {
	case errors.Is(err, connection.ErrUnavailable):
		srv.Restricted, srv.RestrictedReason = true, err.Error()
	case err != nil:
		e.t.Fatal(err)
	default:
		e.t.Cleanup(pools.Close)
		e.pools, srv.Pools = pools, pools
		(&portal.Server{Store: e.st, Pools: pools, Box: e.box, Auth: e.portalAuth, Limiter: auth.NewLimiter(),
			Audit: audit.Recorder{Store: e.st, ActorType: "portal"}, Security: e.portalSec}).Register(e.portalMux)
	}
	srv.Register(e.adminMux)
	for _, f := range []*fake{e.fake, e.p2} { // opening a pool calls it
		f.mu.Lock()
		f.calls = nil
		f.mu.Unlock()
	}
}

// push runs the push worker until nothing is due, as its next round would.
func (e *env) push() {
	e.t.Helper()
	channels.ResetPushRate()
	p := &channels.Pusher{Store: e.st, Pools: e.pools, Box: e.box}
	p.Drain(context.Background())
	p.Refresh(context.Background())
}

// placed is the new-api id of a local channel in a pool, 0 while it has none.
func (e *env) placed(local int, pool int64) int {
	e.t.Helper()
	var id sql.NullInt64
	err := e.raw.QueryRow(`SELECT newapi_channel_id FROM channel_placements WHERE channel_id = $1 AND pool_id = $2`, local, pool).Scan(&id)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		e.t.Fatal(err)
	}
	return int(id.Int64)
}

// nid is the new-api id of a local channel in pool 1.
func (e *env) nid(local int) int {
	e.t.Helper()
	id := e.placed(local, 1)
	if id == 0 {
		e.t.Fatalf("channel %d is not in pool 1", local)
	}
	return id
}

func (e *env) mustAdmin(name, role string) int64 {
	e.t.Helper()
	id, err := e.st.CreateAdmin(context.Background(), name, "not-a-hash", role)
	if err != nil {
		e.t.Fatal(err)
	}
	return id
}

func (e *env) mustUser(name string, adminID int64) int64 {
	e.t.Helper()
	id, err := e.st.CreatePortalUser(context.Background(), name, "not-a-hash", adminID)
	if err != nil {
		e.t.Fatal(err)
	}
	return id
}

func (e *env) template(typ int, baseURL string, allowCustom bool, pools ...int64) {
	e.t.Helper()
	if pools == nil {
		pools = []int64{1}
	}
	_, err := e.st.CreateTemplate(context.Background(), store.Template{Name: "tpl-" + strconv.Itoa(typ), ChannelType: typ,
		BaseURL: baseURL, AllowCustomBaseURL: allowCustom, Models: "tpl-a,tpl-b", Group: "tpl-group", Priority: 7, Weight: 3,
		TestModel: "tpl-a", Extra: json.RawMessage("{}"), Enabled: true, PoolIDs: pools})
	if err != nil {
		e.t.Fatal(err)
	}
}

// fakeNewAPI behaves like new-api on the routes this system calls, writing
// through to the channels table the read-only queries see.
func (e *fake) fakeNewAPI() http.Handler {
	mux := http.NewServeMux()
	ok := func(w http.ResponseWriter, body string) { fmt.Fprint(w, body) }
	record := func(r *http.Request) {
		e.mu.Lock()
		defer e.mu.Unlock()
		e.calls = append(e.calls, call{Method: r.Method, Path: r.URL.Path, Query: r.URL.RawQuery})
	}
	mux.HandleFunc("POST /api/channel/", func(w http.ResponseWriter, r *http.Request) {
		var c call
		c.Method, c.Path = r.Method, r.URL.Path
		if err := json.NewDecoder(r.Body).Decode(&c.Create); err != nil {
			http.Error(w, err.Error(), 400)
			return
		}
		e.mu.Lock()
		e.calls = append(e.calls, c)
		onCreate, breakCreate, reject := e.onCreate, e.breakCreate, e.rejectCreate
		e.mu.Unlock()
		if reject {
			ok(w, `{"success":false,"message":"refused by new-api"}`)
			return
		}
		ch := c.Create.Channel
		keys, multi := []string{ch.Key}, false
		switch c.Create.Mode {
		case "batch":
			keys = strings.Split(ch.Key, "\n")
		case "multi_to_single":
			multi = true
		}
		var ids []int
		for _, key := range keys {
			info := fmt.Sprintf(`{"is_multi_key":%v,"multi_key_size":%d,"multi_key_disabled_reason":{"0":"upstream said sk-partial"},"multi_key_mode":%q}`,
				multi, strings.Count(ch.Key, "\n")+1, c.Create.MultiKeyMode)
			var id int
			err := e.raw.QueryRow(`INSERT INTO channels (type, key, name, status, base_url, models, "group", priority, weight,
				test_model, remark, channel_info, created_time, other_info) VALUES ($1,$2,$3,$12,$4,$5,$6,$7,$8,NULLIF($9,''),$10,$11,100,'{"status_reason":"r"}') RETURNING id`,
				ch.Type, key, ch.Name, ch.BaseURL, ch.Models, ch.Group, ch.Priority, ch.Weight, ch.TestModel, ch.Remark, info, ch.Status).Scan(&id)
			if err != nil {
				http.Error(w, err.Error(), 500)
				return
			}
			ids = append(ids, id)
		}
		if onCreate != nil {
			onCreate()
		}
		if breakCreate {
			panic(http.ErrAbortHandler)
		}
		if !e.idsInCreate {
			ok(w, `{"success":true,"message":""}`)
			return
		}
		b, _ := json.Marshal(map[string]any{"success": true, "data": map[string]any{"ids": append(ids, e.extraIDs...)}})
		w.Write(b)
	})
	mux.HandleFunc("GET /api/channel/{$}", func(w http.ResponseWriter, r *http.Request) {
		record(r)
		page, _ := strconv.Atoi(r.URL.Query().Get("p"))
		size, _ := strconv.Atoi(r.URL.Query().Get("page_size"))
		rows, err := e.raw.Query(`SELECT id, type, COALESCE(name, ''), status, COALESCE("group", ''), COALESCE(models, ''),
			COALESCE(used_quota, 0), COALESCE(created_time, 0), COALESCE(remark, ''), COALESCE(channel_info::text, '{}')
			FROM channels ORDER BY id DESC LIMIT $1 OFFSET $2`, size, (max(page, 1)-1)*size)
		if err != nil {
			http.Error(w, err.Error(), 500)
			return
		}
		defer rows.Close()
		items := []map[string]any{}
		for rows.Next() {
			var id, typ, status, used, created int64
			var name, group, models, remark, info string
			if err := rows.Scan(&id, &typ, &name, &status, &group, &models, &used, &created, &remark, &info); err != nil {
				http.Error(w, err.Error(), 500)
				return
			}
			items = append(items, map[string]any{"id": id, "type": typ, "name": name, "status": status, "group": group, "models": models,
				"used_quota": used, "created_time": created, "remark": remark, "channel_info": json.RawMessage(info)})
		}
		var total int
		e.raw.QueryRow(`SELECT count(*) FROM channels`).Scan(&total)
		json.NewEncoder(w).Encode(map[string]any{"success": true, "data": map[string]any{"items": items, "total": total}})
	})
	mux.HandleFunc("GET /api/channel/{id}", func(w http.ResponseWriter, r *http.Request) {
		record(r)
		var id, status, used int64
		var info string
		err := e.raw.QueryRow(`SELECT id, status, COALESCE(used_quota, 0), COALESCE(channel_info::text, '{}') FROM channels WHERE id = $1`,
			r.PathValue("id")).Scan(&id, &status, &used, &info)
		if errors.Is(err, sql.ErrNoRows) {
			ok(w, `{"success":false,"message":"record not found"}`) // gorm's, as new-api passes it on
			return
		}
		if err != nil {
			http.Error(w, err.Error(), 500)
			return
		}
		json.NewEncoder(w).Encode(map[string]any{"success": true, "data": map[string]any{"id": id, "status": status, "used_quota": used,
			"channel_info": json.RawMessage(info)}})
	})
	mux.HandleFunc("GET /api/status", func(w http.ResponseWriter, r *http.Request) {
		if e.onStatus != nil {
			e.onStatus()
		}
		ok(w, fmt.Sprintf(`{"success":true,"data":{"quota_per_unit":%d}}`, e.qpu))
	})
	for path, data := range map[string]string{
		"/api/channel/models":            `[{"id":"gpt-x"}]`,
		"/api/channel/default_base_urls": `{"1":"https://api.example.com"}`,
		"/api/group/":                    `["default","vip"]`,
		"/api/prefill_group/":            `[{"id":1,"type":"model"}]`,
	} {
		mux.HandleFunc("GET "+path, func(w http.ResponseWriter, r *http.Request) {
			record(r)
			if path == e.denied {
				w.WriteHeader(http.StatusForbidden)
				ok(w, `{"success":false,"message":"The access token lacks the required permission: group:read"}`)
				return
			}
			ok(w, `{"success":true,"message":"","data":`+data+`}`)
		})
	}
	mux.HandleFunc("PUT /api/channel/", func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		e.mu.Lock()
		e.calls = append(e.calls, call{Method: r.Method, Path: r.URL.Path, Body: string(body)})
		reject := e.rejectUpdate
		e.mu.Unlock()
		if reject {
			ok(w, `{"success":false,"message":"refused by new-api"}`)
			return
		}
		var c struct {
			ID           int
			Name, Models string
			TestModel    string `json:"test_model"`
		}
		json.Unmarshal(body, &c)
		// Like new-api: a field that was not sent keeps its value.
		if _, err := e.raw.Exec(`UPDATE channels SET name = $1, models = $2, test_model = COALESCE(NULLIF($3, ''), test_model) WHERE id = $4`,
			c.Name, c.Models, c.TestModel, c.ID); err != nil {
			http.Error(w, err.Error(), 500)
			return
		}
		ok(w, `{"success":true,"message":""}`)
	})
	mux.HandleFunc("POST /api/channel/fetch_models", func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		e.mu.Lock()
		e.calls = append(e.calls, call{Method: r.Method, Path: r.URL.Path, Body: string(body)})
		e.mu.Unlock()
		if strings.Contains(string(body), "sk-refused") {
			ok(w, `{"success":false,"message":"Failed to fetch models: upstream refused sk-refused"}`)
			return
		}
		ok(w, `{"success":true,"message":"","data":["m1","m2"]}`)
	})
	mux.HandleFunc("POST /api/channel/{id}/status", func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Status int `json:"status"`
		}
		json.NewDecoder(r.Body).Decode(&body)
		record(r)
		e.mu.Lock()
		notInCache := e.notInCache
		e.mu.Unlock()
		if notInCache {
			ok(w, `{"success":true,"data":false}`)
			return
		}
		if _, err := e.raw.Exec(`UPDATE channels SET status = $1 WHERE id = $2`, body.Status, r.PathValue("id")); err != nil {
			http.Error(w, err.Error(), 500)
			return
		}
		ok(w, `{"success":true,"data":true}`)
	})
	mux.HandleFunc("DELETE /api/channel/{id}", func(w http.ResponseWriter, r *http.Request) {
		record(r)
		e.mu.Lock()
		drop := e.dropDelete
		e.mu.Unlock()
		if drop {
			panic(http.ErrAbortHandler)
		}
		e.raw.Exec(`DELETE FROM channels WHERE id = $1`, r.PathValue("id"))
		ok(w, `{"success":true}`)
	})
	mux.HandleFunc("GET /api/channel/test/{id}", func(w http.ResponseWriter, r *http.Request) {
		record(r)
		switch r.URL.Query().Get("model") {
		case "pass":
			ok(w, `{"success":true,"message":"","time":0.5}`)
			return
		case "quote": // an upstream's error repeats the key
			var key string
			e.raw.QueryRow(`SELECT key FROM channels WHERE id = $1`, r.PathValue("id")).Scan(&key)
			fmt.Fprintf(w, `{"success":false,"message":"upstream refused %s","time":1.5}`, key)
			return
		}
		ok(w, `{"success":false,"message":"upstream 401","time":1.5}`)
	})
	mux.HandleFunc("POST /api/channel/multi_key/manage", e.fakeMultiKey)
	mux.HandleFunc("GET /api/channel/update_balance/{id}", func(w http.ResponseWriter, r *http.Request) {
		record(r)
		var info struct {
			IsMultiKey bool `json:"is_multi_key"`
		}
		var raw string
		e.raw.QueryRow(`SELECT channel_info::text FROM channels WHERE id = $1`, r.PathValue("id")).Scan(&raw)
		json.Unmarshal([]byte(raw), &info)
		if info.IsMultiKey {
			ok(w, `{"success":false,"message":"Multi-key channels do not support balance queries"}`)
			return
		}
		e.mu.Lock()
		rawBalance := e.rawBalance
		e.mu.Unlock()
		if rawBalance != "" {
			json.NewEncoder(w).Encode(map[string]any{"success": true, "message": "", "raw_response": rawBalance})
			return
		}
		ok(w, `{"success":true,"message":"","balance":12.5}`)
	})
	return mux
}

// callsTo returns the recorded calls with the method and path prefix.
func (e *fake) callsTo(method, prefix string) []call {
	e.mu.Lock()
	defer e.mu.Unlock()
	var out []call
	for _, c := range e.calls {
		if c.Method == method && strings.HasPrefix(c.Path, prefix) {
			out = append(out, c)
		}
	}
	return out
}

func (e *fake) lastCreate() call {
	e.t.Helper()
	cs := e.callsTo("POST", "/api/channel/")
	for i := len(cs) - 1; i >= 0; i-- {
		if cs[i].Path == "/api/channel/" {
			return cs[i]
		}
	}
	e.t.Fatal("new-api received no create")
	return call{}
}

// caller is a session in one of the two systems, or a program with a token.
type caller struct {
	e      *env
	mux    *http.ServeMux
	cookie *http.Cookie
	token  string
	remote string // the address the request comes from
	id     int64
	users  store.Users
	proof  string // a re-verification, for the one request that uses it
}

func session(m *auth.Manager, id int64) *http.Cookie {
	rec := httptest.NewRecorder()
	m.Issue(rec, id, 1) // new accounts start at session_version 1
	return rec.Result().Cookies()[0]
}

func (e *env) asUser(id int64) caller {
	return caller{e: e, mux: e.portalMux, cookie: session(e.portalAuth, id), id: id, users: e.st.PortalUsers()}
}
func (e *env) asAdmin(id int64) caller {
	return caller{e: e, mux: e.adminMux, cookie: session(e.adminAuth, id), id: id, users: e.st.AdminUsers()}
}

// asToken signs a token in the user's browser session and calls with it alone.
func (e *env) asToken(id int64) caller {
	e.t.Helper()
	t := decode[struct {
		Token string `json:"token"`
	}](e.t, e.asUser(id).withProof(security.PurposeTokenCreate).want(201, "POST", "/api/tokens", ch{"name": "script"}))
	return caller{e: e, mux: e.portalMux, token: t.Token}
}

// withProof gives c a proof without the verification, for tests about what it
// guards. A proof must be used up within its minute, by the next request.
func (c caller) withProof(purpose string) caller {
	c.e.t.Helper()
	ctx := context.Background()
	u, err := c.users.Get(ctx, c.id)
	if err != nil {
		c.e.t.Fatal(err)
	}
	c.proof = rand.Text()
	sum := sha256.Sum256([]byte(c.proof))
	f := store.Flow{Kind: store.FlowProof, AccountID: c.id, Version: u.SessionVersion, Purpose: purpose}
	if err := c.users.CreateFlow(ctx, hex.EncodeToString(sum[:]), f, time.Minute); err != nil {
		c.e.t.Fatal(err)
	}
	return c
}

func (c caller) do(method, path string, body any) *httptest.ResponseRecorder {
	c.e.t.Helper()
	var rd *strings.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			c.e.t.Fatal(err)
		}
		rd = strings.NewReader(string(b))
	} else {
		rd = strings.NewReader("")
	}
	req := httptest.NewRequest(method, path, rd)
	if c.cookie != nil {
		req.AddCookie(c.cookie)
	}
	if c.token != "" {
		req.Header.Set("Authorization", "Bearer "+c.token)
	}
	if c.proof != "" {
		req.Header.Set(security.ProofHeader, c.proof)
	}
	if c.remote != "" {
		req.RemoteAddr = net.JoinHostPort(c.remote, "1234")
	}
	rec := httptest.NewRecorder()
	c.mux.ServeHTTP(rec, req)
	return rec
}

// want asserts the status code and returns the body.
func (c caller) want(status int, method, path string, body any) string {
	c.e.t.Helper()
	rec := c.do(method, path, body)
	if rec.Code != status {
		c.e.t.Fatalf("%s %s: got %d want %d: %s", method, path, rec.Code, status, rec.Body)
	}
	return rec.Body.String()
}

type ch = map[string]any

// create adds a channel of the templated type 20, has the push worker push it,
// and returns its local ids.
func (c caller) create(extra ch) []int {
	c.e.t.Helper()
	ids := c.submit(extra)
	c.e.push()
	return ids
}

// submit is create without the push.
func (c caller) submit(extra ch) []int {
	c.e.t.Helper()
	body := ch{"type": 20, "name": "n", "key": "sk-secret-key", "mode": "single"}
	for k, v := range extra {
		body[k] = v
	}
	var res struct {
		IDs []int `json:"ids"`
	}
	if err := json.Unmarshal([]byte(c.want(201, "POST", "/api/channels", body)), &res); err != nil {
		c.e.t.Fatal(err)
	}
	return res.IDs
}

type item struct {
	ID          int             `json:"id"`
	Name        string          `json:"name"`
	Status      int             `json:"status"`
	Exists      bool            `json:"exists"`
	DeletedAt   *int64          `json:"deleted_at"`
	ChannelInfo json.RawMessage `json:"channel_info"`
	OtherInfo   string          `json:"other_info"`
	Owner       *struct {
		UserID        int64  `json:"user_id"`
		Username      string `json:"username"`
		AdminID       int64  `json:"admin_id"`
		AdminUsername string `json:"admin_username"`
	} `json:"owner"`
}

func (c caller) list(query string) []item {
	c.e.t.Helper()
	var out []item
	if err := json.Unmarshal([]byte(c.want(200, "GET", "/api/channels"+query, nil)), &out); err != nil {
		c.e.t.Fatal(err)
	}
	return out
}

func ids(items []item) []int {
	out := make([]int, len(items))
	for i, x := range items {
		out[i] = x.ID
	}
	slices.Sort(out)
	return out
}

func sorted(xs ...int) []int {
	slices.Sort(xs)
	return xs
}

func (e *env) channelPath(id int, suffix string) string {
	return "/api/channels/" + strconv.Itoa(id) + suffix
}

const logRange = "start=1&end=7948801"

type logRow struct {
	channel, typ                 int
	at                           int64
	model, token, group          string // no group means vip-group
	quota, prompt, output, spent int
	other                        string
}

// addLog inserts what new-api writes for a request of an end user, in pool 1:
// l.channel is a local id, or the new-api id of a channel that is not ours.
func (e *env) addLog(l logRow) {
	e.t.Helper()
	if id := e.placed(l.channel, 1); id != 0 {
		l.channel = id
	}
	e.fake.insertLog(l)
}

// insertLog writes l as it is, l.channel being this new-api's own id.
func (e *fake) insertLog(l logRow) {
	e.t.Helper()
	if l.group == "" {
		l.group = "vip-group"
	}
	if _, err := e.raw.Exec(`INSERT INTO logs (user_id, created_at, type, content, username, token_name, model_name, quota,
		prompt_tokens, completion_tokens, use_time, is_stream, channel_id, token_id, "group", ip, request_id, upstream_request_id, other)
		VALUES (9, $1, $2, 'c', 'end-user', $3, $4, $5, $6, $7, $8, true, $9, 7, $10, '203.0.113.9', 'req-id', 'up-req', $11)`,
		l.at, l.typ, l.token, l.model, l.quota, l.prompt, l.output, l.spent, l.channel, l.group, l.other); err != nil {
		e.t.Fatal(err)
	}
}
