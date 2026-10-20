package api

import (
	"context"
	"net/http"
	"time"
)

// UpdateService 检查并安装 GitHub 上的新发行版本
type UpdateService interface {
	UpdateStatus(ctx context.Context, refresh bool) (UpdateStatus, error)
	ApplyUpdate(ctx context.Context) (UpdateStatus, error)
}

// UpdateStatus 表示当前版本与 GitHub 最新发行版本
type UpdateStatus struct {
	Current     string    `json:"current"`
	Latest      string    `json:"latest,omitempty"`
	Available   bool      `json:"available"`
	Mode        string    `json:"mode"`
	ManagedBy   string    `json:"managed_by,omitempty"`
	Notes       string    `json:"notes,omitempty"`
	URL         string    `json:"url,omitempty"`
	PublishedAt time.Time `json:"published_at,omitzero"`
	CheckedAt   time.Time `json:"checked_at,omitzero"`
	Error       string    `json:"error,omitempty"`
	Updating    bool      `json:"updating"`
}

func (s *server) registerUpdates(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/update", s.handleUpdateStatus)
	mux.HandleFunc("POST /api/update", s.handleApplyUpdate)
}

func (s *server) handleUpdateStatus(w http.ResponseWriter, r *http.Request) {
	status, err := s.config.Updates.UpdateStatus(r.Context(), r.URL.Query().Get("refresh") == "1")
	if err != nil {
		writeAdminUpstreamError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, status)
}

func (s *server) handleApplyUpdate(w http.ResponseWriter, r *http.Request) {
	status, err := s.config.Updates.ApplyUpdate(r.Context())
	if err != nil {
		writeAdminUpstreamError(w, err)
		return
	}
	writeJSON(w, http.StatusAccepted, status)
}
