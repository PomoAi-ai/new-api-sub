package admin

import (
	"context"
	"errors"
	"net/http"
	"slices"
	"strconv"

	"newapisub/internal/audit"
	"newapisub/internal/auth"
	"newapisub/internal/config"
	"newapisub/internal/connection"
	"newapisub/internal/httpx"
	"newapisub/internal/security"
	"newapisub/internal/store"
)

type dbView struct {
	Set  bool   `json:"set"`
	Host string `json:"host"`
}

// poolView shows what is set and where, never a secret, and how this process
// sees the pool. A row that cannot be decrypted still says which fields it holds.
type poolView struct {
	ID           int64     `json:"id"`
	Name         string    `json:"name"`
	BaseURL      string    `json:"base_url"`
	LogMode      string    `json:"log_mode"`
	AcceptNew    bool      `json:"accept_new"`
	PATSet       bool      `json:"pat_set"`
	DB           dbView    `json:"db"`
	LogDB        dbView    `json:"log_db"`
	KeyDB        dbView    `json:"key_db"`
	Available    bool      `json:"available"`
	Reason       string    `json:"reason"`
	QuotaPerUnit *float64  `json:"quota_per_unit"`
	Placements   int       `json:"placements"`
	Templates    int       `json:"templates"`
	Version      int64     `json:"version"`
	UpdatedBy    string    `json:"updated_by"`
	UpdatedAt    int64     `json:"updated_at"`
	Sync         *syncView `json:"sync"` // the log sync; nil until it is set up
}

func (s *Server) poolViews(ctx context.Context) ([]poolView, error) {
	rows, err := s.Store.Pools(ctx)
	if err != nil {
		return nil, err
	}
	use, err := s.Store.PoolUse(ctx)
	if err != nil {
		return nil, err
	}
	syncs, err := s.Store.PoolSyncs(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]poolView, len(rows))
	for i, row := range rows {
		c, err := connection.Decrypt(s.Box, row)
		if err != nil {
			c = config.NewAPI{}
		}
		v := poolView{ID: row.ID, Name: row.Name, BaseURL: row.BaseURL, LogMode: row.LogMode, AcceptNew: row.AcceptNew,
			PATSet: len(row.PAT) > 0, DB: dbView{len(row.DBDSN) > 0, connection.Host(c.DBDSN)},
			LogDB: dbView{len(row.LogDSN) > 0, connection.Host(c.LogDBDSN)}, KeyDB: dbView{len(row.KeyDSN) > 0, connection.Host(c.KeyDBDSN)},
			Placements: use[row.ID][0], Templates: use[row.ID][1], Version: row.Version, UpdatedBy: row.UpdatedBy, UpdatedAt: row.UpdatedAt}
		qpu := row.QuotaPerUnit
		switch p := s.pool(row.ID); {
		case p == nil:
			v.Reason = "保存后两个应用重启时载入"
		case p.Reason != "":
			v.Reason = p.Reason
		default:
			v.Available, qpu = true, p.QuotaPerUnit
		}
		if qpu > 0 {
			v.QuotaPerUnit = &qpu
		}
		if j := slices.IndexFunc(syncs, func(ps store.PoolSync) bool { return ps.PoolID == row.ID }); j >= 0 {
			v.Sync = newSyncView(syncs[j])
		}
		out[i] = v
	}
	return out, nil
}

// pool is this process's pool id, nil for one it did not load.
func (s *Server) pool(id int64) *connection.Pool {
	if s.Pools == nil {
		return nil
	}
	return s.Pools.Get(id)
}

func (s *Server) listPools(w http.ResponseWriter, r *http.Request) error {
	out, err := s.poolViews(r.Context())
	if err != nil {
		return err
	}
	httpx.JSON(w, http.StatusOK, out)
	return nil
}

// answerPool answers with the view of pool id.
func (s *Server) answerPool(w http.ResponseWriter, r *http.Request, status int, id int64) error {
	views, err := s.poolViews(r.Context())
	if err != nil {
		return err
	}
	for _, v := range views {
		if v.ID == id {
			httpx.JSON(w, status, v)
			return nil
		}
	}
	return httpx.Errorf(http.StatusNotFound, "号池不存在")
}

// mergePool applies the form to pool id, or to a new pool for 0. Merge refuses
// a new address that would take the stored PAT with it, before anything is sent.
func (s *Server) mergePool(r *http.Request, ev *audit.Event, id int64, in connection.Input) (connection.Merged, error) {
	ev.TargetType = "pool"
	if id != 0 {
		ev.TargetID = strconv.FormatInt(id, 10)
	}
	m, err := connection.Merge(r.Context(), s.Store, s.Box, id, in)
	if err != nil {
		return m, err
	}
	ev.Detail["name"], ev.Detail["fields"] = m.Name, m.Fields
	if m.LogMode == "sync" && s.Mongo == nil { // both processes would refuse to start on it
		return m, errNoMongo
	}
	return m, nil
}

// others are this process's pools, among which a pool's new-api is looked for.
func (s *Server) others() []*connection.Pool {
	if s.Pools == nil {
		return nil
	}
	return s.Pools.List()
}

// testPool tries the form without saving it; id is the saved pool whose
// secrets fill what the form leaves empty, 0 for a new one. Failed steps are an
// answer, not an error.
func (s *Server) testPool(w http.ResponseWriter, r *http.Request, ev *audit.Event) error {
	var req struct {
		connection.Input
		ID int64 `json:"id"`
	}
	if err := httpx.Decode(w, r, &req); err != nil {
		return err
	}
	m, err := s.mergePool(r, ev, req.ID, req.Input)
	if err != nil {
		return err
	}
	steps, ok := connection.Check(r.Context(), m, s.others())
	ev.Failed = !ok
	httpx.JSON(w, http.StatusOK, map[string]any{"ok": ok, "steps": steps})
	return nil
}

// savePool checks the form and saves it; both processes restart on it. The
// proof lasts 60 seconds and the check up to 30, so it is used up first.
func (s *Server) savePool(w http.ResponseWriter, r *http.Request, ev *audit.Event, id int64) error {
	if err := s.Security.Require(r, security.PurposeNewAPIConfig); err != nil {
		return err
	}
	var in connection.Input
	if err := httpx.Decode(w, r, &in); err != nil {
		return err
	}
	m, err := s.mergePool(r, ev, id, in)
	if err != nil {
		return err
	}
	steps, ok := connection.Check(r.Context(), m, s.others())
	if !ok {
		failed := steps[len(steps)-1]
		return httpx.Errorf(http.StatusBadRequest, "校验未通过（%s）：%s", failed.Name, failed.Message)
	}
	id, err = connection.Save(r.Context(), s.Store, s.Box, m, auth.Username(r.Context()))
	if errors.Is(err, store.ErrConflict) {
		return httpx.Errorf(http.StatusConflict, "号池已被其他人修改，或名称、地址已被其他号池使用，请刷新后重试")
	}
	if err != nil {
		return err
	}
	ev.TargetID = strconv.FormatInt(id, 10)
	status := http.StatusOK
	if m.ID == 0 {
		status = http.StatusCreated
	}
	return s.answerPool(w, r, status, id)
}

func (s *Server) createPool(w http.ResponseWriter, r *http.Request, ev *audit.Event) error {
	return s.savePool(w, r, ev, 0)
}

func (s *Server) updatePool(w http.ResponseWriter, r *http.Request, ev *audit.Event) error {
	id, err := target(r, ev, "pool")
	if err != nil {
		return err
	}
	return s.savePool(w, r, ev, id)
}

// deletePool removes a pool nothing uses; both processes restart on it. Its
// id is never taken again, so nothing can mistake a later pool for it.
func (s *Server) deletePool(w http.ResponseWriter, r *http.Request, ev *audit.Event) error {
	id, err := target(r, ev, "pool")
	if err != nil {
		return err
	}
	if err := s.Security.Require(r, security.PurposeNewAPIConfig); err != nil {
		return err
	}
	if row, err := s.Store.Pool(r.Context(), id); err == nil {
		ev.Detail["name"] = row.Name
	}
	switch err := s.Store.DeletePool(r.Context(), id); {
	case errors.Is(err, store.ErrNotFound):
		return httpx.Errorf(http.StatusNotFound, "号池不存在")
	case errors.Is(err, store.ErrConflict):
		return httpx.Errorf(http.StatusConflict, "仍有渠道或模板使用该号池，不能删除")
	case err != nil:
		return err
	}
	w.WriteHeader(http.StatusNoContent)
	return nil
}
