package camoufoxnative

import (
	"io"
	"net/http"
	"time"
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
	ReadyTimeout     time.Duration
	Log              io.Writer
}

// State 返回原生 runtime 的当前页面与 bootstrap 结果
type State struct {
	PID                    int
	PageURL                string
	UserAgent              string
	Platform               string
	Timezone               string
	SnapshotKey            string
	OfficialGenerateStatus int
	Headers                http.Header
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
