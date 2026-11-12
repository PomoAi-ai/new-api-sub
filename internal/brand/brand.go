// Package brand serves the system name and logo to both processes, before
// anyone has logged in.
package brand

import (
	"bytes"
	"errors"
	"io"
	"net/http"
	"slices"
	"strconv"
	"time"

	"newapisub/internal/httpx"
	"newapisub/internal/store"
)

// defaultLogo is the frontend's own file, in use until a super uploads one.
const defaultLogo = "/logo.png"

// logoTypes are what a logo may be, by sniffing. SVG stays out: it can carry script.
var logoTypes = []string{"image/png", "image/jpeg", "image/webp", "image/x-icon"}

// Register mounts the public routes. nameKey is the setting that holds this
// process's system name.
func Register(mux *http.ServeMux, st *store.Store, nameKey string) {
	mux.Handle("GET /api/status", httpx.Handler(func(w http.ResponseWriter, r *http.Request) error {
		name, err := st.Setting(r.Context(), nameKey)
		if err != nil {
			return err
		}
		logo := defaultLogo
		v, err := st.LogoVersion(r.Context())
		if err == nil {
			logo = "/api/brand/logo?v=" + strconv.FormatInt(v, 10)
		} else if !errors.Is(err, store.ErrNotFound) {
			return err
		}
		httpx.JSON(w, http.StatusOK, map[string]string{"system_name": name, "logo": logo})
		return nil
	}))
	mux.Handle("GET /api/brand/logo", httpx.Handler(func(w http.ResponseWriter, r *http.Request) error {
		l, err := st.Logo(r.Context())
		if errors.Is(err, store.ErrNotFound) {
			return httpx.Errorf(http.StatusNotFound, "未设置 logo")
		}
		if err != nil {
			return err
		}
		hd := w.Header()
		hd.Set("Content-Type", l.ContentType)
		hd.Set("X-Content-Type-Options", "nosniff")
		hd.Set("Content-Security-Policy", "default-src 'none'")
		// A new upload gets a new v, so a URL with one never changes.
		if r.URL.Query().Has("v") {
			hd.Set("Cache-Control", "public, max-age=31536000, immutable")
		} else {
			hd.Set("Cache-Control", "no-cache")
		}
		http.ServeContent(w, r, "", time.UnixMilli(l.Version), bytes.NewReader(l.Data))
		return nil
	}))
}

// ReadLogo reads an upload from the raw request body and returns its sniffed
// type, whatever the client claims it is.
func ReadLogo(w http.ResponseWriter, r *http.Request) (string, []byte, error) {
	data, err := io.ReadAll(http.MaxBytesReader(w, r.Body, store.MaxLogoBytes))
	var tooBig *http.MaxBytesError
	if errors.As(err, &tooBig) {
		return "", nil, httpx.Errorf(http.StatusRequestEntityTooLarge, "logo 不能超过 256KB")
	}
	if err != nil {
		return "", nil, httpx.Errorf(http.StatusBadRequest, "读取 logo 失败")
	}
	contentType := http.DetectContentType(data)
	if !slices.Contains(logoTypes, contentType) {
		return "", nil, httpx.Errorf(http.StatusBadRequest, "logo 只能是 PNG、JPEG、WebP 或 ICO")
	}
	return contentType, data, nil
}
