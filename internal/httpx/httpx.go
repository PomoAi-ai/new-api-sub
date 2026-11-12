// Package httpx holds the small HTTP helpers shared by both servers: JSON
// I/O, typed handler errors, security headers, SPA fallback and serving.
package httpx

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log"
	"maps"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"time"
)

const maxBody = 1 << 20

// Error is a handler error with the status and message sent to the client.
type Error struct {
	Status int
	Msg    string
	Extra  map[string]any // more fields of the answer, next to error
}

func (e *Error) Error() string { return e.Msg }

func Errorf(status int, format string, args ...any) error {
	return &Error{Status: status, Msg: fmt.Sprintf(format, args...)}
}

func JSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(v); err != nil {
		log.Printf("write json response: %v", err)
	}
}

// Public is what a client may read of err: an *Error was written for one, and
// anything else can name internal hosts.
func Public(err error) string {
	var he *Error
	if errors.As(err, &he) {
		return he.Msg
	}
	return "服务器内部错误"
}

// WriteError sends err to the client and returns the message sent. Errors
// other than *Error are logged and answered with a generic 500.
func WriteError(w http.ResponseWriter, r *http.Request, err error) string {
	var he *Error
	if errors.As(err, &he) {
		body := map[string]any{"error": he.Msg}
		maps.Copy(body, he.Extra)
		JSON(w, he.Status, body)
		return he.Msg
	}
	log.Printf("%s %s: %v", r.Method, r.URL.Path, err)
	msg := Public(err)
	JSON(w, http.StatusInternalServerError, map[string]string{"error": msg})
	return msg
}

// Handler adapts an error-returning handler.
type Handler func(w http.ResponseWriter, r *http.Request) error

func (h Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if err := h(w, r); err != nil {
		WriteError(w, r, err)
	}
}

// Decode reads a JSON body of at most 1MB into v.
func Decode(w http.ResponseWriter, r *http.Request, v any) error {
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxBody))
	if err := dec.Decode(v); err != nil {
		return Errorf(http.StatusBadRequest, "请求体格式错误")
	}
	if dec.Decode(&struct{}{}) != io.EOF {
		return Errorf(http.StatusBadRequest, "请求体格式错误")
	}
	return nil
}

// DecodeStatus reads {status}: 1 (enabled) or 2 (disabled).
func DecodeStatus(w http.ResponseWriter, r *http.Request) (int, error) {
	var req struct {
		Status int `json:"status"`
	}
	if err := Decode(w, r, &req); err != nil {
		return 0, err
	}
	if req.Status != 1 && req.Status != 2 {
		return 0, Errorf(http.StatusBadRequest, "status 只能为 1 或 2")
	}
	return req.Status, nil
}

// PathID parses a positive integer path value.
func PathID(r *http.Request, name string) (int64, error) {
	id, err := strconv.ParseInt(r.PathValue(name), 10, 64)
	if err != nil || id <= 0 {
		return 0, Errorf(http.StatusBadRequest, "无效的 %s", name)
	}
	return id, nil
}

// Wrap applies security headers, no-store for /api/ and cross-origin
// (CSRF) protection around the whole mux.
func Wrap(h http.Handler) http.Handler {
	csrf := http.NewCrossOriginProtection().Handler(h)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hd := w.Header()
		hd.Set("X-Content-Type-Options", "nosniff")
		hd.Set("X-Frame-Options", "DENY")
		if strings.HasPrefix(r.URL.Path, "/api/") {
			hd.Set("Cache-Control", "no-store")
		}
		csrf.ServeHTTP(w, r)
	})
}

// APINotFound answers unknown /api/ routes with JSON instead of the SPA.
var APINotFound = Handler(func(w http.ResponseWriter, r *http.Request) error {
	return Errorf(http.StatusNotFound, "接口不存在")
})

// SPA serves static files from dist, falling back to index.html for
// client-side routes. It fails if dist has no index.html.
func SPA(dist fs.FS) (http.Handler, error) {
	index, err := fs.ReadFile(dist, "index.html")
	if err != nil {
		return nil, fmt.Errorf("frontend dist has no index.html (run make web): %w", err)
	}
	files := http.FileServerFS(dist)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		name := strings.TrimPrefix(r.URL.Path, "/")
		if name != "" && name != "index.html" {
			if st, err := fs.Stat(dist, name); err == nil && !st.IsDir() {
				if strings.HasPrefix(name, "static/") { // every name in there carries a content hash
					w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
				}
				files.ServeHTTP(w, r)
				return
			}
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Header().Set("Cache-Control", "no-cache")
		_, _ = w.Write(index)
	}), nil
}

// Serve runs the server until ctx ends or SIGINT/SIGTERM, then shuts down gracefully.
func Serve(ctx context.Context, addr string, h http.Handler) error {
	srv := &http.Server{
		Addr:              addr,
		Handler:           h,
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       30 * time.Second,
		WriteTimeout:      120 * time.Second, // channel tests may take up to 90s
		IdleTimeout:       120 * time.Second,
	}
	ctx, stop := signal.NotifyContext(ctx, os.Interrupt, syscall.SIGTERM)
	defer stop()
	errc := make(chan error, 1)
	go func() { errc <- srv.ListenAndServe() }()
	log.Printf("listening on %s", addr)
	select {
	case err := <-errc:
		return err
	case <-ctx.Done():
	}
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	return srv.Shutdown(shutdownCtx)
}
