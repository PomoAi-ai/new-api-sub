package channels

import (
	"errors"
	"net/http"
	"slices"

	"newapisub/internal/audit"
	"newapisub/internal/httpx"
	"newapisub/internal/store"
)

// placement resolves {pool_id} to a placement of the channel {id} in the
// caller's scope.
func (s *Service) placement(r *http.Request, ev *audit.Event) (target, int64, error) {
	t, err := s.lookup(r, ev)
	if err != nil {
		return t, 0, err
	}
	poolID, err := httpx.PathID(r, "pool_id")
	if err != nil {
		return t, 0, err
	}
	ev.Detail["pool_id"] = poolID
	if !slices.ContainsFunc(t.m.Placements, func(p store.Placement) bool { return p.PoolID == poolID }) {
		return t, 0, httpx.Errorf(http.StatusNotFound, "该渠道没有推送到此号池")
	}
	return t, poolID, nil
}

// retryPlacement queues a failed push again, from its first attempt.
func (s *Service) retryPlacement(w http.ResponseWriter, r *http.Request, ev *audit.Event) error {
	t, poolID, err := s.placement(r, ev)
	if err != nil {
		return err
	}
	if err := s.Store.RetryPlacement(r.Context(), t.m.ChannelID, poolID); errors.Is(err, store.ErrNotFound) {
		return httpx.Errorf(http.StatusConflict, "只有推送失败的号池可以重试")
	} else if err != nil {
		return err
	}
	w.WriteHeader(http.StatusAccepted)
	return nil
}

// cancelPlacement drops a push that created nothing, for a pool that should not
// get the channel after all. Only a super may.
func (s *Service) cancelPlacement(w http.ResponseWriter, r *http.Request, ev *audit.Event) error {
	if !s.isSuper(r) {
		return httpx.Errorf(http.StatusForbidden, "仅超级管理员可用")
	}
	t, poolID, err := s.placement(r, ev)
	if err != nil {
		return err
	}
	if err := s.Store.CancelPlacement(r.Context(), t.m.ChannelID, poolID); errors.Is(err, store.ErrNotFound) {
		return httpx.Errorf(http.StatusConflict, "已创建或正在推送的号池不能取消")
	} else if err != nil {
		return err
	}
	w.WriteHeader(http.StatusNoContent)
	return nil
}
