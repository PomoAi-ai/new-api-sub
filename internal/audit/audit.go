// Package audit records write operations of portal users and admins.
// Recording is a side channel: failures are logged and never change the
// business response.
package audit

import (
	"cmp"
	"context"
	"encoding/json"
	"log"
	"net"
	"net/http"
	"net/netip"
	"strings"
	"time"

	"newapisub/internal/auth"
	"newapisub/internal/httpx"
	"newapisub/internal/store"
)

// Event is filled in by a handler while it runs. Detail must never contain
// passwords, channel keys, the PAT, DSNs or anything of a second factor.
type Event struct {
	ActorID    int64 // 0 = unknown (e.g. login with a nonexistent user)
	ActorName  string
	TargetType string
	TargetID   string
	Detail     map[string]any
	Failed     bool   // the handler answered normally but the operation did not succeed (a failed channel test)
	Action     string // overrides the action given to Wrap, for a handler with more than one outcome
	Quiet      bool   // Wrap records nothing: a refusal repeated in a flood, recorded once already
	status     int    // what Wrap answered; 0 for a record made along the way
}

type Recorder struct {
	Store     *store.Store
	ActorType string // "portal" or "admin"
	// TrustedProxies may state the client address in X-Forwarded-For.
	TrustedProxies []netip.Prefix
}

// Handler runs an auditable write; ev is pre-filled with the session user.
type Handler func(w http.ResponseWriter, r *http.Request, ev *Event) error

// statusWriter remembers the status a handler sent. Unwrap keeps
// http.ResponseController working behind it.
type statusWriter struct {
	http.ResponseWriter
	status int
}

func (w *statusWriter) WriteHeader(code int) {
	w.status = cmp.Or(w.status, code)
	w.ResponseWriter.WriteHeader(code)
}

func (w *statusWriter) Write(b []byte) (int, error) {
	w.status = cmp.Or(w.status, http.StatusOK)
	return w.ResponseWriter.Write(b)
}

func (w *statusWriter) Unwrap() http.ResponseWriter { return w.ResponseWriter }

// Wrap records action with the handler's outcome. An error is sent to the
// client and stored in detail.error as the client sees it: portal users read
// this log, the whole error is in the server log. Once the handler has started the response
// (a download) no error can be sent, so the connection is broken instead: the
// client must not take a cut-off body for a complete one.
func (rec Recorder) Wrap(action string, h Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ev := &Event{ActorID: auth.UserID(r.Context()), ActorName: auth.Username(r.Context()), Detail: map[string]any{}}
		sw := &statusWriter{ResponseWriter: w}
		err := h(sw, r, ev)
		cutOff := err != nil && sw.status != 0
		if err != nil {
			ev.Detail["error"] = httpx.Public(err)
			if !cutOff {
				httpx.WriteError(sw, r, err)
			}
		}
		ev.status = cmp.Or(sw.status, http.StatusOK)
		if !ev.Quiet {
			rec.Record(r, cmp.Or(ev.Action, action), err == nil && !ev.Failed, ev)
		}
		if cutOff {
			log.Printf("%s %s: cut off: %v", r.Method, r.URL.Path, err)
			panic(http.ErrAbortHandler)
		}
	})
}

// View records a logged-in read of data as view.<name>, so the log answers what
// an account or address looked at and not only what it changed.
func (rec Recorder) View(name string, h httpx.Handler) http.Handler {
	return rec.Wrap("view."+name, func(w http.ResponseWriter, r *http.Request, _ *Event) error {
		return h(w, r)
	})
}

// queryFields are the parameters of the audited reads. Anything else a client
// sends stays out, so the log cannot end up holding a stray secret.
var queryFields = []string{
	"start", "end", "type", "channel_id", "user_id", "owner", "model_name", "username", "token_name", "group",
	"request_id", "upstream_request_id", "p", "page_size", "hours", "tz_offset", "lang", "view", "by", "split", "pool_id",
	"action", "category", "success", "actor_type", "actor_id", "actor_name", "ip",
}

// auditedQuery returns the known parameters of r, nil if it has none.
func auditedQuery(r *http.Request) map[string]string {
	v := r.URL.Query()
	var out map[string]string
	for _, name := range queryFields {
		if vals := v[name]; len(vals) > 0 {
			if out == nil {
				out = map[string]string{}
			}
			out[name] = truncate(strings.Join(vals, ","), 256)
		}
	}
	return out
}

// parseIP returns the form of an address that compares and prints the same
// however it arrived: IPv4 mapped into IPv6, or with a zone.
func parseIP(s string) (netip.Addr, bool) {
	addr, err := netip.ParseAddr(strings.TrimSpace(s))
	return addr.Unmap().WithZone(""), err == nil
}

func (rec Recorder) trusted(addr netip.Addr) bool {
	for _, p := range rec.TrustedProxies {
		if p.Contains(addr) {
			return true
		}
	}
	return false
}

// ClientIP is the address the request came from. X-Forwarded-For is anyone's to
// write, so it only counts when a trusted proxy delivered the request, and only
// from the right, past the proxies, to the first address no trusted proxy can
// vouch for: everything left of that was written by the client.
func (rec Recorder) ClientIP(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		host = r.RemoteAddr
	}
	peer, ok := parseIP(host)
	if !ok {
		return host
	}
	if !rec.trusted(peer) {
		return peer.String()
	}
	hops := strings.Split(strings.Join(r.Header.Values("X-Forwarded-For"), ","), ",")
	for i := len(hops) - 1; i >= 0; i-- {
		hop, ok := parseIP(hops[i])
		if !ok {
			break // no proxy of ours wrote this
		}
		if !rec.trusted(hop) {
			return hop.String()
		}
	}
	return peer.String()
}

// Record writes one audit row synchronously, detached from request
// cancellation. The request's method, route, status and, for a read, query go
// into detail, as they do into new-api's audit_info.
func (rec Recorder) Record(r *http.Request, action string, success bool, ev *Event) {
	ev.Detail["http_method"] = r.Method
	ev.Detail["http_route"] = r.Pattern[strings.IndexByte(r.Pattern, ' ')+1:] // "GET /api/x" -> "/api/x"
	if ev.status != 0 {
		ev.Detail["http_status"] = ev.status
	}
	if q := auditedQuery(r); q != nil && r.Method == http.MethodGet {
		ev.Detail["query"] = q
	}
	if id := auth.TokenID(r.Context()); id != 0 {
		ev.Detail["token_id"] = id
	}
	detail, err := json.Marshal(ev.Detail)
	if err != nil {
		log.Printf("audit %s: marshal detail: %v", action, err)
		detail = []byte("{}")
	}
	var actorID *int64
	if ev.ActorID > 0 {
		actorID = &ev.ActorID
	}
	a := store.AuditLog{
		ActorType:    rec.ActorType,
		ActorID:      actorID,
		ActorName:    truncate(ev.ActorName, 64),
		Action:       action,
		TargetType:   ev.TargetType,
		TargetID:     ev.TargetID,
		Success:      success,
		Detail:       detail,
		IP:           rec.ClientIP(r),
		ForwardedFor: truncate(strings.Join(r.Header.Values("X-Forwarded-For"), ", "), 512),
		UserAgent:    truncate(r.UserAgent(), 512),
	}
	ctx, cancel := context.WithTimeout(context.WithoutCancel(r.Context()), 5*time.Second)
	defer cancel()
	if err := rec.Store.InsertAudit(ctx, a); err != nil {
		log.Printf("audit %s actor=%s/%d target=%s/%s: insert failed: %v",
			action, rec.ActorType, ev.ActorID, ev.TargetType, ev.TargetID, err)
	}
}

// truncate also makes s valid NUL-free UTF-8, which PostgreSQL TEXT requires.
func truncate(s string, n int) string {
	r := []rune(strings.ReplaceAll(strings.ToValidUTF8(s, "\uFFFD"), "\x00", ""))
	if len(r) > n {
		r = r[:n]
	}
	return string(r)
}
