package api

import (
	"context"
	"errors"
	"net/http"
	"time"

	"github.com/Mag1cFall/AIStudio2API/internal/aistudio"
	"github.com/Mag1cFall/AIStudio2API/internal/camoufoxnative"
)

// AdminService 定义管理端需要的权威状态能力
type AdminService interface {
	Status(context.Context) (AdminStatus, error)
	CatalogModels(context.Context) ([]aistudio.Model, error)
	Accounts(context.Context) ([]AdminAccount, error)
	CreateAccount(context.Context, AccountCreateInput, *camoufoxnative.LiveView) (AdminAccount, error)
	ChromeImportProfiles(context.Context) ([]ChromeImportProfile, error)
	CookieBrowsers(ctx context.Context, proxy string) ([]CookieBrowser, error)
	DiscoverCookieAccounts(context.Context, CookieImportInput) ([]CookieBrowser, error)
	ImportChromeAccounts(context.Context, ChromeImportInput) ([]AdminAccount, error)
	ImportAccountState(context.Context, AccountStateInput) (AdminAccount, error)
	RenewAccountCookies(ctx context.Context, accountID string, token string, cookies []aistudio.StateCookie) (AdminAccount, error)
	UpdateAccount(context.Context, string, AccountInput) (AdminAccount, error)
	DeleteAccount(context.Context, string) error
	LoginAccount(context.Context, string, *camoufoxnative.LiveView) (AdminAccount, error)
	RefreshAccount(context.Context, string) (AdminAccountRefresh, error)
	StartService(context.Context) (AdminStatus, error)
	StopService(context.Context) (AdminStatus, error)
	DownloadCamoufox(context.Context) (AdminStatus, error)
	ClearLogs(context.Context) error
	RuntimeConfig(context.Context) (RuntimeConfig, error)
	UpdateRuntimeConfig(context.Context, RuntimeConfig) (RuntimeConfig, error)
	Cooldowns(context.Context) ([]AdminCooldown, error)
	ClearCooldown(ctx context.Context, accountID string, scope string) error
	Requests(context.Context) ([]AdminRequest, error)
	CancelRequest(context.Context, string) error
	Events(context.Context) (<-chan AdminEvent, error)
	FirstEventLatencies() []FirstEventLatency
	RecordAccessStart(AccessLog)
	RecordAccessLog(AccessLog)
}

// AdminStatus 表示管理端运行状态
type AdminStatus struct {
	State          string                     `json:"state"`
	Running        bool                       `json:"running"`
	Ready          bool                       `json:"ready"`
	Version        string                     `json:"version"`
	ActiveRequests int                        `json:"active_requests"`
	Accounts       AdminAccountCounts         `json:"accounts"`
	Camoufox       camoufoxnative.Preparation `json:"camoufox"`
	ChromeImport   bool                       `json:"chrome_import"`
}

// InvalidConfigValue 表示加载时取值无效、按默认值生效的一个配置项
type InvalidConfigValue struct {
	Key    string `json:"key"`
	Value  string `json:"value"`
	Reason string `json:"reason"`
}

// AdminLog 表示管理页面展示的一条运行日志
type AdminLog struct {
	Time    time.Time   `json:"time"`
	Level   string      `json:"level"`
	Source  string      `json:"source"`
	Message string      `json:"message"`
	Event   string      `json:"event"`
	Request *RequestLog `json:"request,omitempty"`
}

// RequestLog 保存可关联的请求状态、用量与诊断字段
type RequestLog struct {
	ID              string            `json:"id"`
	State           string            `json:"state"`
	StartedAt       time.Time         `json:"started_at,omitzero"`
	Model           string            `json:"model,omitempty"`
	Method          string            `json:"method,omitempty"`
	Path            string            `json:"path,omitempty"`
	Status          int               `json:"status,omitempty"`
	DurationMS      float64           `json:"duration_ms,omitempty"`
	Usage           *RequestLogUsage  `json:"usage,omitempty"`
	ToolCalls       int               `json:"tool_calls,omitempty"`
	FinishReason    string            `json:"finish_reason,omitempty"`
	Error           string            `json:"error,omitempty"`
	InputMessages   int               `json:"input_messages,omitempty"`
	InputTextChars  int               `json:"input_text_chars,omitempty"`
	InputMedia      int               `json:"input_media,omitempty"`
	InputMediaBytes int64             `json:"input_media_bytes,omitempty"`
	InputFiles      int               `json:"input_files,omitempty"`
	Parameters      map[string]string `json:"parameters,omitempty"`
	FirstEventMS    float64           `json:"first_event_ms,omitempty"`
	QueueMS         float64           `json:"queue_ms,omitempty"`
	ProofMS         float64           `json:"proof_ms,omitempty"`
	UpstreamBytes   int64             `json:"upstream_bytes,omitempty"`
	Channel         string            `json:"channel,omitempty"`
}

// RequestLogUsage 区分输入、缓存命中、思考、回复与首字之后的输出速率
type RequestLogUsage struct {
	InputTokens            int64   `json:"input_tokens"`
	CachedTokens           int64   `json:"cached_tokens"`
	ReasoningTokens        int64   `json:"reasoning_tokens"`
	ReplyTokens            int64   `json:"reply_tokens"`
	OutputTokens           int64   `json:"output_tokens"`
	TotalTokens            int64   `json:"total_tokens"`
	AverageTokensPerSecond float64 `json:"average_tokens_per_second,omitempty"`
}

// AccessLog 表示一次公开 API 请求的最终访问记录
type AccessLog struct {
	Status          int
	Latency         time.Duration
	FirstEvent      time.Duration
	FirstOutput     int64
	TrailingError   string
	QueueWait       time.Duration
	Proof           time.Duration
	UpstreamBytes   int64
	Usage           *aistudio.Usage
	ToolCalls       int
	InputMessages   int
	InputTextChars  int
	InputMedia      int
	InputMediaBytes int64
	InputFiles      int
	Temperature     string
	TopP            string
	Thinking        string
	MaxOutputTokens string
	RequestID       string
	Method          string
	Path            string
	Model           string
	Account         string
	Channel         string
	FinishReason    string
	Error           string
	Canceled        bool
	Generation      bool
	Attempts        []RequestAttempt
	Authorized      bool
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
	Egress      string   `json:"egress"`
	Locale      string   `json:"locale"`
	Timezone    string   `json:"timezone"`
	Models      []string `json:"models"`
	BenefitTier string   `json:"benefit_tier"`
	Message     string   `json:"message"`

	// PairingRenewedAt 是远程配对账户最近一次由原电脑续签的时间，没有经配对续签时省略
	PairingRenewedAt *time.Time `json:"pairing_renewed_at,omitempty"`

	// Transition 是账户启停的后台收尾阶段，draining 为等待在途请求结束，syncing 为同步模型目录，没有收尾时省略
	Transition string `json:"transition,omitempty"`
	// ActiveRequests 是账户上进行中的请求数
	ActiveRequests int `json:"active_requests"`
}

// AdminProxy 表示代理池中一个代理的可用性与绑定账户数
type AdminProxy struct {
	Proxy     string     `json:"proxy"`
	Available bool       `json:"available"`
	Accounts  int        `json:"accounts"`
	CheckedAt *time.Time `json:"checked_at,omitempty"`
	Error     string     `json:"error,omitempty"`
}

// AdminAccountRefresh 表示一次手动刷新后的账户与刷新前后的权益和模型数
type AdminAccountRefresh struct {
	Account        AdminAccount `json:"account"`
	PreviousTier   string       `json:"previous_tier"`
	Tier           string       `json:"tier"`
	PreviousModels int          `json:"previous_models"`
	Models         int          `json:"models"`
}

// AccountInput 表示已有账户配置
type AccountInput struct {
	Label    string `json:"label"`
	Enabled  bool   `json:"enabled"`
	Proxy    string `json:"proxy"`
	Locale   string `json:"locale"`
	Timezone string `json:"timezone"`
}

// AccountCreateInput 表示浏览器登录的账户环境
type AccountCreateInput struct {
	Proxy    string `json:"proxy"`
	Locale   string `json:"locale"`
	Timezone string `json:"timezone"`
}

// CookieImportInput 表示识别 Cookie 中账户时提交的 Cookie 文本与出口代理
type CookieImportInput struct {
	Cookies string `json:"cookies"`
	Proxy   string `json:"proxy"`
}

// ChromeImportProfile 表示可从本机 Chrome 导入的账号
type ChromeImportProfile struct {
	ID          string `json:"id"`
	Profile     string `json:"profile"`
	DisplayName string `json:"display_name"`
	Email       string `json:"email"`
	Locale      string `json:"locale"`
	// Restricted 是该账户服务受限的原因，受限的账户不能导入
	Restricted string `json:"restricted"`
}

// CookieBrowser 表示本机开启远程调试的浏览器与其中登录 AI Studio 的账户
type CookieBrowser struct {
	Address   string `json:"address"`
	Browser   string `json:"browser"`
	Email     string `json:"email"`
	AuthUser  int    `json:"auth_user"`
	Locale    string `json:"locale"`
	Timezone  string `json:"timezone"`
	Imported  bool   `json:"imported"`
	SignedOut bool   `json:"signed_out"`
	Error     string `json:"error"`
	// Restricted 是该账户服务受限的原因，受限的账户不能导入
	Restricted string `json:"restricted"`

	// DeviceBound 表示 Cookie 带 __Secure-1PSIDRTS，会话绑定原浏览器所在的设备，导入的副本约 2 小时内失效
	DeviceBound bool `json:"device_bound"`
}

// ChromeImportInput 表示批量导入的 Chrome 账号与账户环境
type ChromeImportInput struct {
	AccountIDs []string `json:"account_ids"`
	Proxy      string   `json:"proxy"`
	Locale     string   `json:"locale"`
	Timezone   string   `json:"timezone"`
}

// RuntimeConfig 表示全局运行配置
type RuntimeConfig struct {
	AutoStart                 bool                 `json:"auto_start"`
	HideDockIcon              bool                 `json:"hide_dock_icon"`
	RequestBodyLog            bool                 `json:"request_body_log"`
	AdminAuthEnabled          bool                 `json:"admin_auth_enabled"`
	AdminUsername             string               `json:"admin_username"`
	AdminPassword             *string              `json:"admin_password,omitempty"`
	AdminPasswordSet          bool                 `json:"admin_password_set"`
	SavedAdminPassword        string               `json:"-"`
	BuildNativeNonstream      bool                 `json:"build_native_nonstream"`
	NonstreamKeepalive        bool                 `json:"nonstream_keepalive"`
	FakeStream                bool                 `json:"fake_stream"`
	YouTubeLinks              bool                 `json:"youtube_links"`
	StripThoughtSignatures    bool                 `json:"strip_thought_signatures"`
	SearchModelAliases        bool                 `json:"search_model_aliases"`
	DiscoverHiddenModels      bool                 `json:"discover_hidden_models"`
	GoogleSearch              bool                 `json:"google_search"`
	FirstEventHedge           string               `json:"first_event_hedge"`
	FirstEventHedgeModels     string               `json:"first_event_hedge_models"`
	ContinuationRewrite       bool                 `json:"continuation_rewrite"`
	ContinuationPrompt        string               `json:"continuation_prompt"`
	LogFile                   bool                 `json:"log_file"`
	LogMaxTotalMB             int                  `json:"log_max_total_mb"`
	AuthStates                string               `json:"auth_states"`
	ListenAddr                string               `json:"listen_addr"`
	APIKey                    string               `json:"proxy_api_key"`
	ActiveListenAddr          string               `json:"active_listen_addr"`
	ReservedListenAddr        string               `json:"reserved_listen_addr"`
	PreviewListenAddr         string               `json:"preview_listen_addr"`
	ActivePreviewListenAddr   string               `json:"active_preview_listen_addr"`
	ActiveAPIKey              string               `json:"active_proxy_api_key"`
	ManagementRestartRequired bool                 `json:"management_restart_required"`
	ServiceRestartRequired    bool                 `json:"service_restart_required"`
	Proxy                     string               `json:"proxy"`
	ActiveProxy               string               `json:"active_proxy"`
	ProxyPool                 []AdminProxy         `json:"proxy_pool"`
	InitTimeout               string               `json:"init_timeout"`
	RequestTimeout            string               `json:"request_timeout"`
	WarmWorkerLimit           int                  `json:"warm_worker_limit"`
	MaxActiveWorkers          int                  `json:"max_active_workers"`
	WarmStartupConcurrency    int                  `json:"warm_startup_concurrency"`
	PerAccountConcurrency     int                  `json:"per_account_concurrency"`
	RoutingStrategy           string               `json:"routing_strategy"`
	AccountOrder              string               `json:"account_order"`
	MaxAccountSwitches        int                  `json:"max_account_switches"`
	UpstreamChannels          []string             `json:"upstream_channels"`
	ModelMapping              string               `json:"model_mapping"`
	ModelChannels             string               `json:"model_channels"`
	ModelAccounts             string               `json:"model_accounts"`
	APIKeyAccounts            string               `json:"api_key_accounts"`
	WAABackend                string               `json:"waa_backend"`
	MediaOutput               string               `json:"media_output"`
	MediaBaseURL              string               `json:"media_base_url"`
	Environment               []string             `json:"environment"`
	Invalid                   []InvalidConfigValue `json:"invalid"`
}

// AdminCooldown 表示账户模型冷却
type AdminCooldown struct {
	AccountID    string    `json:"account_id"`
	AccountLabel string    `json:"account_label"`
	Channel      string    `json:"channel"`
	ModelID      string    `json:"model_id"`
	Scope        string    `json:"scope"`
	Until        time.Time `json:"until"`
	Reason       string    `json:"reason,omitempty"`
}

// AdminRequest 表示活动请求摘要
type AdminRequest struct {
	ID           string    `json:"id"`
	Model        string    `json:"model"`
	AccountID    string    `json:"account_id"`
	AccountLabel string    `json:"account_label"`
	Channel      string    `json:"channel,omitempty"`
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
	mux.HandleFunc("GET /api/accounts/import/chrome", s.handleChromeImportProfiles)
	mux.HandleFunc("POST /api/accounts/import/chrome", s.handleImportChromeAccounts)
	mux.HandleFunc("GET /api/accounts/import/browsers", s.handleCookieBrowsers)
	mux.HandleFunc("POST /api/accounts/import/cookies/discover", s.handleDiscoverCookieAccounts)
	mux.HandleFunc("POST /api/accounts/import/cookies", s.handleImportCookieAccounts)
	mux.HandleFunc("GET /api/accounts/tasks", s.handleAccountTasks)
	mux.HandleFunc("DELETE /api/accounts/tasks/{id}", s.handleDeleteAccountTask)
	mux.HandleFunc("GET /api/pairing", s.handleActivePairing)
	mux.HandleFunc("POST /api/pairing", s.handleCreatePairing)
	mux.HandleFunc("GET /api/paths", s.handlePaths)
	mux.HandleFunc("PUT /api/accounts/{id}", s.handleUpdateAccount)
	mux.HandleFunc("DELETE /api/accounts/{id}", s.handleDeleteAccount)
	mux.HandleFunc("POST /api/accounts/{id}/login", s.handleLoginAccount)
	mux.HandleFunc("GET /api/login-view/{view}", s.handleLoginView)
	mux.HandleFunc("POST /api/accounts/{id}/refresh", s.handleRefreshAccount)
	mux.HandleFunc("POST /api/control/start", s.handleStartService)
	mux.HandleFunc("POST /api/control/stop", s.handleStopService)
	mux.HandleFunc("POST /api/camoufox/download", s.handleDownloadCamoufox)
	mux.HandleFunc("DELETE /api/logs", s.handleClearLogs)
	mux.HandleFunc("GET /api/config", s.handleRuntimeConfig)
	mux.HandleFunc("PUT /api/config", s.handleUpdateRuntimeConfig)
	mux.HandleFunc("GET /api/first-event-latency", s.handleFirstEventLatencies)
	mux.HandleFunc("GET /api/cooldowns", s.handleCooldowns)
	mux.HandleFunc("DELETE /api/accounts/{id}/cooldowns/{scope}", s.handleClearCooldown)
	mux.HandleFunc("GET /api/requests", s.handleRequests)
	mux.HandleFunc("POST /api/video-tokens", s.handleVideoTokens)
	mux.HandleFunc("POST /api/requests/{id}/cancel", s.handleCancelRequest)
	mux.HandleFunc("GET /api/events", s.handleAdminEvents)
}

func (s *server) handleHealth(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func (s *server) handleStatus(w http.ResponseWriter, r *http.Request) {
	status, err := s.config.Admin.Status(r.Context())
	if err != nil {
		writeAdminUpstreamError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, status)
}

func (s *server) handleAdminModels(w http.ResponseWriter, r *http.Request) {
	models, err := s.config.Admin.CatalogModels(r.Context())
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

func (s *server) handleChromeImportProfiles(w http.ResponseWriter, r *http.Request) {
	profiles, err := s.config.Admin.ChromeImportProfiles(r.Context())
	if err != nil {
		writeAdminUpstreamError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string][]ChromeImportProfile{"profiles": profiles})
}

func (s *server) handleCookieBrowsers(w http.ResponseWriter, r *http.Request) {
	browsers, err := s.config.Admin.CookieBrowsers(r.Context(), r.URL.Query().Get("proxy"))
	if err != nil {
		writeAdminUpstreamError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string][]CookieBrowser{"browsers": browsers})
}

func (s *server) handleDiscoverCookieAccounts(w http.ResponseWriter, r *http.Request) {
	var input CookieImportInput
	if err := decodeJSON(r, &input); err != nil {
		writeAdminError(w, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}
	accounts, err := s.config.Admin.DiscoverCookieAccounts(r.Context(), input)
	if err != nil {
		writeAdminUpstreamError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string][]CookieBrowser{"accounts": accounts})
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

func (s *server) handleRefreshAccount(w http.ResponseWriter, r *http.Request) {
	refresh, err := s.config.Admin.RefreshAccount(r.Context(), r.PathValue("id"))
	if err != nil {
		writeAdminUpstreamError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, refresh)
}

func (s *server) handleStartService(w http.ResponseWriter, r *http.Request) {
	status, err := s.config.Admin.StartService(r.Context())
	if err != nil {
		writeAdminUpstreamError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, status)
}

// handleDownloadCamoufox 在后台重新开始下载 Camoufox 并返回服务状态
func (s *server) handleDownloadCamoufox(w http.ResponseWriter, r *http.Request) {
	status, err := s.config.Admin.DownloadCamoufox(r.Context())
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

// handleFirstEventLatencies 返回各模型与通道近期原请求首事件等待的 p95
func (s *server) handleFirstEventLatencies(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string][]FirstEventLatency{"latencies": s.config.Admin.FirstEventLatencies()})
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

func (s *server) handleClearCooldown(w http.ResponseWriter, r *http.Request) {
	if err := s.config.Admin.ClearCooldown(r.Context(), r.PathValue("id"), r.PathValue("scope")); err != nil {
		writeAdminUpstreamError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
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
	if err := streamHeaders(w); err != nil {
		return
	}
	tasks := s.accountTasks.subscribe(r.Context())
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
		case list := <-tasks:
			if err := writeSSE(w, "", AdminEvent{Type: "account_tasks", Data: map[string][]AccountTask{"tasks": list}}); err != nil {
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
