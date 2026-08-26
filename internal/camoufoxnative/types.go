package camoufoxnative

import (
	"io"
	"net/http"
	"time"
)

// StartupStage 表示 Camoufox runtime 的启动阶段
type StartupStage string

const (
	// StartupPreparingBrowser 表示正在准备浏览器配置
	StartupPreparingBrowser StartupStage = "preparing_browser"
	// StartupLaunchingBrowser 表示正在启动浏览器进程
	StartupLaunchingBrowser StartupStage = "launching_browser"
	// StartupConnectingBiDi 表示正在连接 WebDriver BiDi
	StartupConnectingBiDi StartupStage = "connecting_bidi"
	// StartupLoadingAIStudio 表示正在载入 AI Studio 页面
	StartupLoadingAIStudio StartupStage = "loading_ai_studio"
	// StartupLocatingWAA 表示正在定位 WAA 服务
	StartupLocatingWAA StartupStage = "locating_waa"
	// StartupBootstrappingWAA 表示正在执行 WAA Bootstrap
	StartupBootstrappingWAA StartupStage = "bootstrapping_waa"
)

// Options 定义单个 AI Studio 账户的原生 Camoufox runtime
type Options struct {
	ExecutablePath   string
	StorageStatePath string
	Model            string
	BootstrapPrompt  string
	Locale           string
	Timezone         string
	Proxy            string
	ProxyBypass      string
	Headless         bool
	TemporaryChat    bool
	ReadyTimeout     time.Duration
	Log              io.Writer
	StartupProgress  func(StartupStage)
}

func (options Options) reportStartup(stage StartupStage) {
	if options.StartupProgress != nil {
		options.StartupProgress(stage)
	}
}

// State 返回原生 runtime 的当前页面与 bootstrap 结果
type State struct {
	PID         int
	PageURL     string
	UserAgent   string
	Platform    string
	Timezone    string
	SnapshotKey string
	Headers     http.Header
}

type storageState struct {
	Cookies []storageCookie `json:"cookies"`
	Origins []storageOrigin `json:"origins"`
}

type storageCookie struct {
	Name         string  `json:"name"`
	Value        string  `json:"value"`
	Domain       string  `json:"domain"`
	Path         string  `json:"path"`
	Expires      float64 `json:"expires"`
	HTTPOnly     bool    `json:"httpOnly"`
	Secure       bool    `json:"secure"`
	SameSite     string  `json:"sameSite"`
	PartitionKey string  `json:"partitionKey,omitempty"`
}

type storageOrigin struct {
	Origin       string             `json:"origin"`
	LocalStorage []localStorageItem `json:"localStorage"`
}

type localStorageItem struct {
	Name  string `json:"name"`
	Value string `json:"value"`
}
