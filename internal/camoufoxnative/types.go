package camoufoxnative

import (
	"io"
	"net/http"
	"time"
)

// StartupStage represents the startup stage of the Camoufox runtime.
type StartupStage string

const (
	// StartupPreparingBrowser indicates that the browser configuration is being prepared.
	StartupPreparingBrowser StartupStage = "preparing_browser"
	// StartupLaunchingBrowser indicates that the browser process is being launched.
	StartupLaunchingBrowser StartupStage = "launching_browser"
	// StartupConnectingBiDi indicates that WebDriver BiDi is connecting.
	StartupConnectingBiDi StartupStage = "connecting_bidi"
	// StartupLoadingAIStudio indicates that the AI Studio page is loading.
	StartupLoadingAIStudio StartupStage = "loading_ai_studio"
	// StartupLocatingWAA indicates that the WAA service is being located.
	StartupLocatingWAA StartupStage = "locating_waa"
	// StartupBootstrappingWAA indicates that WAA bootstrap is in progress.
	StartupBootstrappingWAA StartupStage = "bootstrapping_waa"
)

// Options defines the native Camoufox runtime for a single AI Studio account.
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

// State returns the current page and bootstrap results of the native runtime.
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
