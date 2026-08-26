package api

import (
	"context"
	"errors"
	"net/http"
	"time"

	"github.com/Mag1cFall/AIStudio2API/internal/aistudio"
)

// AdminService 定义管理端需要的权威状态能力
type AdminService interface {
	Status(context.Context) (AdminStatus, error)
	Accounts(context.Context) ([]AdminAccount, error)
	CreateAccount(context.Context, AccountInput) (AdminAccount, error)
	UpdateAccount(context.Context, string, AccountInput) (AdminAccount, error)
	DeleteAccount(context.Context, string) error
	LoginAccount(context.Context, string) (AdminAccount, error)
	VerifyAccount(context.Context, string) (AdminAccount, error)
	StartService(context.Context) (AdminStatus, error)
	StopService(context.Context) (AdminStatus, error)
	ClearLogs(context.Context) error
	RuntimeConfig(context.Context) (RuntimeConfig, error)
	UpdateRuntimeConfig(context.Context, RuntimeConfig) (RuntimeConfig, error)
	Cooldowns(context.Context) ([]AdminCooldown, error)
	Requests(context.Context) ([]AdminRequest, error)
	CancelRequest(context.Context, string) error
	Events(context.Context) (<-chan AdminEvent, error)
	RecordAccessStart(AccessLog)
	RecordAccessLog(AccessLog)
}

// AdminStatus 表示管理端运行状态
type AdminStatus struct {
	State          string             `json:"state"`
	Running        bool               `json:"running"`
	Ready          bool               `json:"ready"`
	Version        string             `json:"version"`
	ActiveRequests int                `json:"active_requests"`
	Accounts       AdminAccountCounts `json:"accounts"`
}

// AdminLog 表示管理页面展示的一条运行日志
type AdminLog struct {
	Time    time.Time `json:"time"`
	Level   string    `json:"level"`
	Source  string    `json:"source"`
	Message string    `json:"message"`
}

// AccessLog 表示一次公开 API 请求的最终访问记录
type AccessLog struct {
	Status          int
	Latency         time.Duration
	FirstEvent      time.Duration
	FirstContent    time.Duration
	ContentChars    int
	OutputTokens    int64
	ReasoningTokens int64
	Temperature     string
	TopP            string
	Thinking        string
	MaxOutputTokens string
	Method          string
	Path            string
	Model           string
	Account         string
	FinishReason    string
	Error           string
	Canceled        bool
	Generation      bool
}

// AdminAccountCounts 表示账户状态计数
type AdminAccountCounts struct {
	Total        int `json:"total"`
	Ready        int `json:"ready"`
	Busy         int `json:"busy"`
	Cooldown     int `json:"cooldown"`
	AuthRequired int `json:"auth_required"`
}

// AdminAccount 表示管理端账户摘要
type AdminAccount struct {
	ID          string   `json:"id"`
	Label       string   `json:"label"`
	Enabled     bool     `json:"enabled"`
	State       string   `json:"state"`
	Proxy       string   `json:"proxy"`
	Locale      string   `json:"locale"`
	Timezone    string   `json:"timezone"`
	Models      []string `json:"models"`
	BenefitTier string   `json:"benefit_tier"`
	Message     string   `json:"message"`
}

// AccountInput 表示新增账户配置
type AccountInput struct {
	Label    string `json:"label"`
	Enabled  bool   `json:"enabled"`
	Proxy    string `json:"proxy"`
	Locale   string `json:"locale"`
	Timezone string `json:"timezone"`
}

// RuntimeConfig 表示全局运行配置
type RuntimeConfig struct {
	AuthStates             string `json:"auth_states"`
	ListenAddr             string `json:"listen_addr"`
	APIKey                 string `json:"proxy_api_key"`
	Proxy                  string `json:"proxy"`
	InitTimeout            string `json:"init_timeout"`
	RequestTimeout         string `json:"request_timeout"`
	WarmWorkerLimit        int    `json:"warm_worker_limit"`
	WarmStartupConcurrency int    `json:"warm_startup_concurrency"`
	PerAccountConcurrency  int    `json:"per_account_concurrency"`
	TemporaryChat          bool   `json:"temporary_chat"`
}

// AdminCooldown 表示账户模型冷却
type AdminCooldown struct {
	AccountID    string    `json:"account_id"`
	AccountLabel string    `json:"account_label"`
	ModelID      string    `json:"model_id"`
	Until        time.Time `json:"until"`
	Reason       string    `json:"reason,omitempty"`
}

// AdminRequest 表示活动请求摘要
type AdminRequest struct {
	ID           string    `json:"id"`
	Model        string    `json:"model"`
	AccountID    string    `json:"account_id"`
	AccountLabel string    `json:"account_label"`
	State        string    `json:"state"`
	StartedAt    time.Time `json:"started_at"`
}

// AdminEvent 表示管理端增量事件
type AdminEvent struct {
	Type string `json:"type"`
	Data any    `json:"data"`
}

func (s *server) registerAdmin(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/accounts", s.handleAccounts)
	mux.HandleFunc("POST /api/accounts", s.handleCreateAccount)
	mux.HandleFunc("PUT /api/accounts/{id}", s.handleUpdateAccount)
	mux.HandleFunc("DELETE /api/accounts/{id}", s.handleDeleteAccount)
	mux.HandleFunc("POST /api/accounts/{id}/login", s.handleLoginAccount)
	mux.HandleFunc("POST /api/accounts/{id}/verify", s.handleVerifyAccount)
	mux.HandleFunc("POST /api/control/start", s.handleStartService)
	mux.HandleFunc("POST /api/control/stop", s.handleStopService)
	mux.HandleFunc("DELETE /api/logs", s.handleClearLogs)
	mux.HandleFunc("GET /api/config", s.handleRuntimeConfig)
	mux.HandleFunc("PUT /api/config", s.handleUpdateRuntimeConfig)
	mux.HandleFunc("GET /api/cooldowns", s.handleCooldowns)
	mux.HandleFunc("GET /api/requests", s.handleRequests)
	mux.HandleFunc("POST /api/requests/{id}/cancel", s.handleCancelRequest)
	mux.HandleFunc("GET /api/events", s.handleAdminEvents)
}

func (s *server) handleHealth(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func (s *server) handleStatus(w http.ResponseWriter, r *http.Request) {
	if s.config.Admin != nil {
		status, err := s.config.Admin.Status(r.Context())
		if err != nil {
			writeAdminUpstreamError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, status)
		return
	}
	if _, err := s.service.Models(r.Context()); err != nil {
		writeAdminUpstreamError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ready": true})
}

func (s *server) handleAdminModels(w http.ResponseWriter, r *http.Request) {
	models, err := s.service.Models(r.Context())
	if err != nil {
		writeAdminUpstreamError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string][]aistudio.Model{"models": models})
}

func (s *server) handleAccounts(w http.ResponseWriter, r *http.Request) {
	accounts, err := s.config.Admin.Accounts(r.Context())
	if err != nil {
		writeAdminUpstreamError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string][]AdminAccount{"accounts": accounts})
}

func (s *server) handleCreateAccount(w http.ResponseWriter, r *http.Request) {
	var input AccountInput
	if err := decodeJSON(r, &input); err != nil {
		writeAdminError(w, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}
	account, err := s.config.Admin.CreateAccount(r.Context(), input)
	if err != nil {
		writeAdminUpstreamError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, map[string]AdminAccount{"account": account})
}

func (s *server) handleUpdateAccount(w http.ResponseWriter, r *http.Request) {
	var input AccountInput
	if err := decodeJSON(r, &input); err != nil {
		writeAdminError(w, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}
	account, err := s.config.Admin.UpdateAccount(r.Context(), r.PathValue("id"), input)
	if err != nil {
		writeAdminUpstreamError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]AdminAccount{"account": account})
}

func (s *server) handleDeleteAccount(w http.ResponseWriter, r *http.Request) {
	if err := s.config.Admin.DeleteAccount(r.Context(), r.PathValue("id")); err != nil {
		writeAdminUpstreamError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *server) handleLoginAccount(w http.ResponseWriter, r *http.Request) {
	account, err := s.config.Admin.LoginAccount(r.Context(), r.PathValue("id"))
	if err != nil {
		writeAdminUpstreamError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]AdminAccount{"account": account})
}

func (s *server) handleVerifyAccount(w http.ResponseWriter, r *http.Request) {
	account, err := s.config.Admin.VerifyAccount(r.Context(), r.PathValue("id"))
	if err != nil {
		writeAdminUpstreamError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]AdminAccount{"account": account})
}

func (s *server) handleStartService(w http.ResponseWriter, r *http.Request) {
	status, err := s.config.Admin.StartService(r.Context())
	if err != nil {
		writeAdminUpstreamError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, status)
}

func (s *server) handleStopService(w http.ResponseWriter, r *http.Request) {
	status, err := s.config.Admin.StopService(r.Context())
	if err != nil {
		writeAdminUpstreamError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, status)
}

func (s *server) handleClearLogs(w http.ResponseWriter, r *http.Request) {
	if err := s.config.Admin.ClearLogs(r.Context()); err != nil {
		writeAdminUpstreamError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *server) handleRuntimeConfig(w http.ResponseWriter, r *http.Request) {
	config, err := s.config.Admin.RuntimeConfig(r.Context())
	if err != nil {
		writeAdminUpstreamError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, config)
}

func (s *server) handleUpdateRuntimeConfig(w http.ResponseWriter, r *http.Request) {
	var config RuntimeConfig
	if err := decodeJSON(r, &config); err != nil {
		writeAdminError(w, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}
	updated, err := s.config.Admin.UpdateRuntimeConfig(r.Context(), config)
	if err != nil {
		writeAdminUpstreamError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, updated)
}

func (s *server) handleCooldowns(w http.ResponseWriter, r *http.Request) {
	cooldowns, err := s.config.Admin.Cooldowns(r.Context())
	if err != nil {
		writeAdminUpstreamError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string][]AdminCooldown{"cooldowns": cooldowns})
}

func (s *server) handleRequests(w http.ResponseWriter, r *http.Request) {
	requests, err := s.config.Admin.Requests(r.Context())
	if err != nil {
		writeAdminUpstreamError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string][]AdminRequest{"requests": requests})
}

func (s *server) handleCancelRequest(w http.ResponseWriter, r *http.Request) {
	if err := s.config.Admin.CancelRequest(r.Context(), r.PathValue("id")); err != nil {
		writeAdminUpstreamError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *server) handleAdminEvents(w http.ResponseWriter, r *http.Request) {
	events, err := s.config.Admin.Events(r.Context())
	if err != nil {
		writeAdminUpstreamError(w, err)
		return
	}
	streamHeaders(w)
	for {
		select {
		case <-r.Context().Done():
			return
		case event, ok := <-events:
			if !ok {
				return
			}
			if err := writeSSE(w, "", event); err != nil {
				return
			}
		}
	}
}

func writeAdminUpstreamError(w http.ResponseWriter, err error) {
	if errors.Is(err, context.Canceled) {
		return
	}
	writeAdminError(w, statusFromError(err), codeFromError(err, "upstream_error"), err.Error())
}
