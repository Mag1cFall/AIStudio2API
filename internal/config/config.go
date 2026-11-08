package config

import (
	"bufio"
	"bytes"
	"fmt"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"
)

const (
	defaultAuthStates         = "auth"
	defaultListenAddr         = "127.0.0.1:2048"
	defaultPreviewListenAddr  = "127.0.0.1:2050"
	defaultInitTimeout        = 2 * time.Minute
	defaultRequestTimeout     = 30 * time.Minute
	defaultWarmWorkerLimit    = 10
	defaultMaxActiveWorkers   = 20
	camoufoxWarmWorkerLimit   = 5
	camoufoxMaxActiveWorkers  = 10
	defaultWarmConcurrency    = 5
	defaultAccountConcurrency = 2
)

var configKeys = [...]string{
	"AISTUDIO_AUTH_STATES",
	"LISTEN_ADDR",
	"PREVIEW_LISTEN_ADDR",
	"PROXY_API_KEY",
	"API_KEY_ACCOUNTS",
	"PROXY",
	"INIT_TIMEOUT",
	"REQUEST_TIMEOUT",
	"WARM_WORKER_LIMIT",
	"MAX_ACTIVE_WORKERS",
	"WARM_STARTUP_CONCURRENCY",
	"PER_ACCOUNT_CONCURRENCY",
	"ROUTING_STRATEGY",
	"ACCOUNT_ORDER",
	"UPSTREAM_CHANNELS",
	"MODEL_MAPPING",
	"MODEL_CHANNELS",
	"MODEL_ACCOUNTS",
	"WAA_BACKEND",
	"MEDIA_OUTPUT",
	"MEDIA_BASE_URL",
	"AUTO_START",
	"HIDE_DOCK_ICON",
	"REQUEST_BODY_LOG",
	"LOG_FILE",
	"LOG_MAX_TOTAL_MB",
	"ADMIN_AUTH_ENABLED",
	"ADMIN_USERNAME",
	"ADMIN_PASSWORD",
	"BUILD_NATIVE_NONSTREAM",
	"NONSTREAM_KEEPALIVE",
	"FAKE_STREAM",
	"YOUTUBE_LINKS",
	"STRIP_THOUGHT_SIGNATURES",
	"SEARCH_MODEL_ALIASES",
	"GOOGLE_SEARCH",
	"FIRST_EVENT_HEDGE",
	"FIRST_EVENT_HEDGE_MODELS",
	"CONTINUATION_REWRITE",
	"CONTINUATION_PROMPT",
}

// LogFileMB 是单个日志文件的轮转大小
const LogFileMB = 10

// upstreamChannels 表示生成请求可启用的上游通道
var upstreamChannels = []string{"playground", "build"}

// WAABackendCamoufox 表示由 Camoufox 页面承担 WAA 生命周期
const WAABackendCamoufox = "camoufox"

// WAABackendSpiderMonkey 表示由进程内的 SpiderMonkey 承担 WAA 生命周期
const WAABackendSpiderMonkey = "spidermonkey"

// WAAInterpreterCacheName 是账户状态目录中 SpiderMonkey 后端缓存 WAA 解释器的子目录名
const WAAInterpreterCacheName = ".waa-interpreters"

// FirstEventHedgeOff 表示首事件超时不发起备用请求
const FirstEventHedgeOff = "off"

// FirstEventHedgeAuto 表示按模型与通道近期原请求首事件等待的 p95 发起备用请求
const FirstEventHedgeAuto = "auto"

// DefaultFirstEventHedgeModels 是 FIRST_EVENT_HEDGE_MODELS 的默认值
const DefaultFirstEventHedgeModels = "gemini-4-argon"

// MediaOutputInline 表示 Chat 与 Messages 正文以 data URI 内联生成的媒体
const MediaOutputInline = "inline"

// MediaOutputURL 表示生成的媒体保存到本地，Chat 与 Messages 正文以 URL 引用
const MediaOutputURL = "url"

// DefaultContinuationPrompt 是 CONTINUATION_PROMPT 未设置时改写末尾 assistant 轮所用的续写提示词
const DefaultContinuationPrompt = "Continue the assistant response after the following prefix. Return only the continuation, without repeating the prefix."

// Config 保存服务的全局配置
type Config struct {
	AutoStart              bool
	HideDockIcon           bool
	RequestBodyLog         bool
	LogFile                bool
	LogMaxTotalMB          int
	AdminAuthEnabled       bool
	AdminUsername          string
	AdminPassword          string
	BuildNativeNonstream   bool
	NonstreamKeepalive     bool
	FakeStream             bool
	YouTubeLinks           bool
	StripThoughtSignatures bool
	SearchModelAliases     bool
	GoogleSearch           bool
	FirstEventHedge        string
	FirstEventHedgeModels  string
	ContinuationRewrite    bool
	ContinuationPrompt     string
	AuthStates             string
	ListenAddr             string
	PreviewListenAddr      string
	ProxyAPIKey            string
	APIKeyAccounts         string
	Proxy                  string
	InitTimeout            time.Duration
	RequestTimeout         time.Duration
	WarmWorkerLimit        int
	MaxActiveWorkers       int
	WarmStartupConcurrency int
	PerAccountConcurrency  int
	RoutingStrategy        string
	AccountOrder           string
	UpstreamChannels       []string
	ModelMapping           string
	ModelChannels          string
	ModelAccounts          string
	WAABackend             string
	MediaOutput            string
	MediaBaseURL           string
	// Invalid 是加载时取值无效、改用默认值的配置项
	Invalid []InvalidValue
	// Environment 是取自进程环境变量、覆盖 .env 的配置键
	Environment []string
}

// workerDefaultKeys 是默认值随 WAA 后端变化的键
var workerDefaultKeys = []string{"WARM_WORKER_LIMIT", "MAX_ACTIVE_WORKERS"}

// defaultsFor 返回 WAA 后端为 backend 时的默认配置，Camoufox 后端的常驻预热账户数与活动 Worker 上限较小
func defaultsFor(backend string) Config {
	cfg := Default()
	if backend == WAABackendCamoufox {
		cfg.WarmWorkerLimit, cfg.MaxActiveWorkers = camoufoxWarmWorkerLimit, camoufoxMaxActiveWorkers
	}
	return cfg
}

// Default 返回可直接启动的默认配置
func Default() Config {
	return Config{
		AdminUsername:          "admin",
		LogFile:                true,
		LogMaxTotalMB:          500,
		BuildNativeNonstream:   true,
		NonstreamKeepalive:     true,
		SearchModelAliases:     true,
		FirstEventHedge:        FirstEventHedgeAuto,
		FirstEventHedgeModels:  DefaultFirstEventHedgeModels,
		ContinuationRewrite:    true,
		ContinuationPrompt:     DefaultContinuationPrompt,
		AuthStates:             defaultAuthStates,
		ListenAddr:             defaultListenAddr,
		PreviewListenAddr:      defaultPreviewListenAddr,
		InitTimeout:            defaultInitTimeout,
		RequestTimeout:         defaultRequestTimeout,
		WarmWorkerLimit:        defaultWarmWorkerLimit,
		MaxActiveWorkers:       defaultMaxActiveWorkers,
		WarmStartupConcurrency: defaultWarmConcurrency,
		PerAccountConcurrency:  defaultAccountConcurrency,
		RoutingStrategy:        "round-robin",
		UpstreamChannels:       []string{"playground"},
		WAABackend:             WAABackendSpiderMonkey,
		MediaOutput:            MediaOutputInline,
	}
}

// InvalidValue 是加载时取值无效、改用默认值的配置项
type InvalidValue struct {
	Key    string
	Value  string
	Reason string
}

// secretKeys 是取值可能含凭据的配置键，无效时不记录原值
var secretKeys = []string{"PROXY", "PROXY_API_KEY", "API_KEY_ACCOUNTS", "ADMIN_PASSWORD"}

// invalidValue 返回无效配置项的记录，凭据键不带原值
func invalidValue(key, value string, err error) InvalidValue {
	if slices.Contains(secretKeys, key) {
		value = ""
	}
	return InvalidValue{Key: key, Value: value, Reason: err.Error()}
}

// configSetters 把每个配置键的文本值解析到 Config
var configSetters = map[string]func(*Config, string) error{
	"AUTO_START":               boolSetter("AUTO_START", func(c *Config) *bool { return &c.AutoStart }),
	"HIDE_DOCK_ICON":           boolSetter("HIDE_DOCK_ICON", func(c *Config) *bool { return &c.HideDockIcon }),
	"REQUEST_BODY_LOG":         boolSetter("REQUEST_BODY_LOG", func(c *Config) *bool { return &c.RequestBodyLog }),
	"LOG_FILE":                 boolSetter("LOG_FILE", func(c *Config) *bool { return &c.LogFile }),
	"ADMIN_AUTH_ENABLED":       boolSetter("ADMIN_AUTH_ENABLED", func(c *Config) *bool { return &c.AdminAuthEnabled }),
	"BUILD_NATIVE_NONSTREAM":   boolSetter("BUILD_NATIVE_NONSTREAM", func(c *Config) *bool { return &c.BuildNativeNonstream }),
	"NONSTREAM_KEEPALIVE":      boolSetter("NONSTREAM_KEEPALIVE", func(c *Config) *bool { return &c.NonstreamKeepalive }),
	"FAKE_STREAM":              boolSetter("FAKE_STREAM", func(c *Config) *bool { return &c.FakeStream }),
	"SEARCH_MODEL_ALIASES":     boolSetter("SEARCH_MODEL_ALIASES", func(c *Config) *bool { return &c.SearchModelAliases }),
	"GOOGLE_SEARCH":            boolSetter("GOOGLE_SEARCH", func(c *Config) *bool { return &c.GoogleSearch }),
	"YOUTUBE_LINKS":            boolSetter("YOUTUBE_LINKS", func(c *Config) *bool { return &c.YouTubeLinks }),
	"STRIP_THOUGHT_SIGNATURES": boolSetter("STRIP_THOUGHT_SIGNATURES", func(c *Config) *bool { return &c.StripThoughtSignatures }),
	"CONTINUATION_REWRITE":     boolSetter("CONTINUATION_REWRITE", func(c *Config) *bool { return &c.ContinuationRewrite }),
	"LOG_MAX_TOTAL_MB":         intSetter("LOG_MAX_TOTAL_MB", func(c *Config) *int { return &c.LogMaxTotalMB }),
	"WARM_WORKER_LIMIT":        intSetter("WARM_WORKER_LIMIT", func(c *Config) *int { return &c.WarmWorkerLimit }),
	"MAX_ACTIVE_WORKERS":       intSetter("MAX_ACTIVE_WORKERS", func(c *Config) *int { return &c.MaxActiveWorkers }),
	"WARM_STARTUP_CONCURRENCY": intSetter("WARM_STARTUP_CONCURRENCY", func(c *Config) *int { return &c.WarmStartupConcurrency }),
	"PER_ACCOUNT_CONCURRENCY":  intSetter("PER_ACCOUNT_CONCURRENCY", func(c *Config) *int { return &c.PerAccountConcurrency }),
	"INIT_TIMEOUT":             durationSetter("INIT_TIMEOUT", func(c *Config) *time.Duration { return &c.InitTimeout }),
	"REQUEST_TIMEOUT":          durationSetter("REQUEST_TIMEOUT", func(c *Config) *time.Duration { return &c.RequestTimeout }),
	"ADMIN_USERNAME":           textSetter(func(c *Config) *string { return &c.AdminUsername }, strings.TrimSpace),
	"ADMIN_PASSWORD":           textSetter(func(c *Config) *string { return &c.AdminPassword }, func(value string) string { return value }),
	"FIRST_EVENT_HEDGE":        textSetter(func(c *Config) *string { return &c.FirstEventHedge }, lowerTrim),
	"FIRST_EVENT_HEDGE_MODELS": textSetter(func(c *Config) *string { return &c.FirstEventHedgeModels }, strings.TrimSpace),
	"CONTINUATION_PROMPT":      textSetter(func(c *Config) *string { return &c.ContinuationPrompt }, strings.TrimSpace),
	"AISTUDIO_AUTH_STATES":     textSetter(func(c *Config) *string { return &c.AuthStates }, strings.TrimSpace),
	"LISTEN_ADDR":              textSetter(func(c *Config) *string { return &c.ListenAddr }, strings.TrimSpace),
	"PREVIEW_LISTEN_ADDR":      textSetter(func(c *Config) *string { return &c.PreviewListenAddr }, strings.TrimSpace),
	"PROXY_API_KEY":            textSetter(func(c *Config) *string { return &c.ProxyAPIKey }, strings.TrimSpace),
	"PROXY":                    textSetter(func(c *Config) *string { return &c.Proxy }, strings.TrimSpace),
	"ROUTING_STRATEGY":         textSetter(func(c *Config) *string { return &c.RoutingStrategy }, strings.TrimSpace),
	"ACCOUNT_ORDER":            textSetter(func(c *Config) *string { return &c.AccountOrder }, strings.TrimSpace),
	"MODEL_MAPPING":            textSetter(func(c *Config) *string { return &c.ModelMapping }, strings.TrimSpace),
	"MODEL_CHANNELS":           textSetter(func(c *Config) *string { return &c.ModelChannels }, strings.TrimSpace),
	"MODEL_ACCOUNTS":           textSetter(func(c *Config) *string { return &c.ModelAccounts }, strings.TrimSpace),
	"API_KEY_ACCOUNTS":         textSetter(func(c *Config) *string { return &c.APIKeyAccounts }, strings.TrimSpace),
	"WAA_BACKEND":              setWAABackend,
	"MEDIA_OUTPUT":             textSetter(func(c *Config) *string { return &c.MediaOutput }, lowerTrim),
	"MEDIA_BASE_URL":           textSetter(func(c *Config) *string { return &c.MediaBaseURL }, strings.TrimSpace),
	"UPSTREAM_CHANNELS": func(c *Config, value string) error {
		c.UpstreamChannels = parseUpstreamChannels(value)
		return nil
	},
}

func boolSetter(key string, field func(*Config) *bool) func(*Config, string) error {
	return func(c *Config, value string) error {
		parsed, err := strconv.ParseBool(strings.TrimSpace(value))
		if err != nil {
			return fmt.Errorf("%s 必须是 true 或 false", key)
		}
		*field(c) = parsed
		return nil
	}
}

func intSetter(key string, field func(*Config) *int) func(*Config, string) error {
	return func(c *Config, value string) error {
		parsed, err := parsePositiveInt(key, value)
		if err != nil {
			return err
		}
		*field(c) = parsed
		return nil
	}
}

func durationSetter(key string, field func(*Config) *time.Duration) func(*Config, string) error {
	return func(c *Config, value string) error {
		parsed, err := parsePositiveDuration(key, value)
		if err != nil {
			return err
		}
		*field(c) = parsed
		return nil
	}
}

func textSetter(field func(*Config) *string, normalize func(string) string) func(*Config, string) error {
	return func(c *Config, value string) error {
		*field(c) = normalize(value)
		return nil
	}
}

func lowerTrim(value string) string {
	return strings.ToLower(strings.TrimSpace(value))
}

// setWAABackend 设置 WAA 后端，camoufox 以外的取值都使用 spidermonkey
func setWAABackend(c *Config, value string) error {
	c.WAABackend = WAABackendSpiderMonkey
	if lowerTrim(value) == WAABackendCamoufox {
		c.WAABackend = WAABackendCamoufox
	}
	return nil
}

// configCheck 是一条配置校验与校验失败时改回默认值的键
type configCheck struct {
	keys  []string
	check func(Config) error
}

// configChecks 是加载与保存时的全部取值校验
var configChecks = []configCheck{
	{[]string{"AISTUDIO_AUTH_STATES"}, func(c Config) error {
		if strings.TrimSpace(c.AuthStates) == "" {
			return fmt.Errorf("AISTUDIO_AUTH_STATES 不能为空")
		}
		return nil
	}},
	{[]string{"LISTEN_ADDR"}, func(c Config) error { return validateListenAddr("LISTEN_ADDR", c.ListenAddr) }},
	{[]string{"PREVIEW_LISTEN_ADDR"}, func(c Config) error { return validateListenAddr("PREVIEW_LISTEN_ADDR", c.PreviewListenAddr) }},
	{[]string{"LISTEN_ADDR", "PREVIEW_LISTEN_ADDR"}, func(c Config) error {
		if listenPort(c.ListenAddr) == listenPort(c.PreviewListenAddr) {
			return fmt.Errorf("PREVIEW_LISTEN_ADDR 的端口不能与 LISTEN_ADDR 相同")
		}
		return nil
	}},
	{[]string{"PROXY"}, func(c Config) error {
		for _, proxy := range ProxyList(c.Proxy) {
			if err := ValidateProxy(proxy); err != nil {
				return err
			}
		}
		return nil
	}},
	{[]string{"INIT_TIMEOUT"}, func(c Config) error {
		if c.InitTimeout <= 0 {
			return fmt.Errorf("INIT_TIMEOUT 必须是正数时长")
		}
		return nil
	}},
	{[]string{"REQUEST_TIMEOUT"}, func(c Config) error {
		if c.RequestTimeout <= 0 {
			return fmt.Errorf("REQUEST_TIMEOUT 必须是正数时长")
		}
		return nil
	}},
	{[]string{"WARM_WORKER_LIMIT", "MAX_ACTIVE_WORKERS"}, func(c Config) error {
		if c.WarmWorkerLimit <= 0 {
			return fmt.Errorf("WARM_WORKER_LIMIT 必须是正整数")
		}
		if c.MaxActiveWorkers < c.WarmWorkerLimit {
			return fmt.Errorf("MAX_ACTIVE_WORKERS 必须大于或等于 WARM_WORKER_LIMIT")
		}
		return nil
	}},
	{[]string{"WARM_STARTUP_CONCURRENCY", "MAX_ACTIVE_WORKERS"}, func(c Config) error {
		if c.WarmStartupConcurrency <= 0 || c.WarmStartupConcurrency > c.MaxActiveWorkers {
			return fmt.Errorf("WARM_STARTUP_CONCURRENCY 必须是 1 到 MAX_ACTIVE_WORKERS")
		}
		return nil
	}},
	{[]string{"PER_ACCOUNT_CONCURRENCY"}, func(c Config) error {
		if c.PerAccountConcurrency <= 0 {
			return fmt.Errorf("PER_ACCOUNT_CONCURRENCY 必须是正整数")
		}
		return nil
	}},
	{[]string{"ROUTING_STRATEGY"}, func(c Config) error {
		if c.RoutingStrategy != "round-robin" && c.RoutingStrategy != "fill-first" {
			return fmt.Errorf("ROUTING_STRATEGY 必须是 round-robin 或 fill-first")
		}
		return nil
	}},
	{[]string{"UPSTREAM_CHANNELS"}, func(c Config) error { return validateUpstreamChannels(c.UpstreamChannels) }},
	{[]string{"MODEL_MAPPING"}, func(c Config) error {
		_, err := ParseModelMapping(c.ModelMapping)
		return err
	}},
	{[]string{"MODEL_CHANNELS"}, func(c Config) error {
		_, err := ParseModelChannels(c.ModelChannels)
		return err
	}},
	{[]string{"MODEL_ACCOUNTS"}, func(c Config) error {
		_, err := ParseModelAccounts(c.ModelAccounts)
		return err
	}},
	{[]string{"API_KEY_ACCOUNTS"}, func(c Config) error {
		_, err := ParseAPIKeyAccounts(c.APIKeyAccounts, c.ProxyAPIKey)
		return err
	}},
	{[]string{"WAA_BACKEND"}, func(c Config) error {
		if c.WAABackend != WAABackendCamoufox && c.WAABackend != WAABackendSpiderMonkey {
			return fmt.Errorf("WAA_BACKEND 必须是 camoufox 或 spidermonkey")
		}
		return nil
	}},
	{[]string{"FIRST_EVENT_HEDGE"}, func(c Config) error {
		_, _, err := ParseFirstEventHedge(c.FirstEventHedge)
		return err
	}},
	{[]string{"MEDIA_OUTPUT"}, func(c Config) error {
		if c.MediaOutput != MediaOutputInline && c.MediaOutput != MediaOutputURL {
			return fmt.Errorf("MEDIA_OUTPUT 必须是 inline 或 url")
		}
		return nil
	}},
	{[]string{"MEDIA_BASE_URL"}, func(c Config) error {
		if c.MediaBaseURL == "" {
			return nil
		}
		base, err := url.Parse(c.MediaBaseURL)
		if err != nil || (base.Scheme != "http" && base.Scheme != "https") || base.Host == "" {
			return fmt.Errorf("MEDIA_BASE_URL 必须是 http 或 https 地址")
		}
		return nil
	}},
	{[]string{"LOG_MAX_TOTAL_MB"}, func(c Config) error {
		if c.LogMaxTotalMB < 2*LogFileMB {
			return fmt.Errorf("LOG_MAX_TOTAL_MB 不能小于 %d", 2*LogFileMB)
		}
		return nil
	}},
}

// Load 从指定 env 文件和进程环境加载配置，取值无效的项改用默认值并记入 Invalid，两者都没有 WAA 后端时按账户状态目录确定后端并追加到文件
func Load(path string) (Config, error) {
	values, commented, err := readEnvFile(path)
	if err != nil {
		return Config{}, err
	}
	if !commented && upgradeEnv(values) {
		if err := writeEnv(path, values); err != nil {
			return Config{}, err
		}
	}
	cfg := Default()
	for _, key := range configKeys {
		if value, ok := os.LookupEnv(key); ok {
			values[key] = value
			cfg.Environment = append(cfg.Environment, key)
		}
	}
	for _, key := range configKeys {
		if value, ok := values[key]; ok {
			if err := configSetters[key](&cfg, value); err != nil {
				cfg.Invalid = append(cfg.Invalid, invalidValue(key, value, err))
			}
		}
	}
	if _, ok := values["WAA_BACKEND"]; !ok {
		if camoufoxOnlyAccounts(cfg.AuthStates) {
			cfg.WAABackend = WAABackendCamoufox
		}
		if err := appendEnv(path, "WAA_BACKEND", cfg.WAABackend); err != nil {
			return Config{}, err
		}
	}
	defaults := defaultsFor(cfg.WAABackend).envValues()
	for _, key := range workerDefaultKeys {
		if _, ok := values[key]; !ok {
			configSetters[key](&cfg, defaults[key])
		}
	}
	for failed := true; failed; {
		failed = false
		for _, check := range configChecks {
			err := check.check(cfg)
			if err == nil {
				continue
			}
			failed = true
			for _, key := range check.keys {
				configSetters[key](&cfg, defaults[key])
				value, ok := values[key]
				if ok && !slices.ContainsFunc(cfg.Invalid, func(item InvalidValue) bool { return item.Key == key }) {
					cfg.Invalid = append(cfg.Invalid, invalidValue(key, value, err))
				}
			}
		}
	}
	if err := cfg.Validate(); err != nil {
		return Config{}, err
	}
	return cfg, nil
}

// legacyEnvKeys 是 v0.2.18 及之前的版本保存设置时总会写出的键
var legacyEnvKeys = []string{
	"AISTUDIO_AUTH_STATES", "LISTEN_ADDR", "PROXY_API_KEY", "PROXY", "INIT_TIMEOUT", "REQUEST_TIMEOUT", "WARM_WORKER_LIMIT",
	"MAX_ACTIVE_WORKERS", "WARM_STARTUP_CONCURRENCY", "PER_ACCOUNT_CONCURRENCY", "ROUTING_STRATEGY", "UPSTREAM_CHANNELS", "WAA_BACKEND",
}

// upgradeEnv 改写旧版本写出的没有注释的全量 .env：四项旧值改为当前值，WAA 后端保留为当前取值，删去其余等于默认值的键，返回是否改写
func upgradeEnv(values map[string]string) bool {
	if slices.ContainsFunc(legacyEnvKeys, func(key string) bool {
		_, ok := values[key]
		return !ok
	}) {
		return false
	}
	defaults := Default().envValues()
	isDefault := func(key, value string) bool {
		parsed := Default()
		return configSetters[key](&parsed, value) == nil && parsed.envValues()[key] == defaults[key]
	}
	if slices.Equal(parseUpstreamChannels(values["UPSTREAM_CHANNELS"]), []string{"playground", "build"}) {
		delete(values, "UPSTREAM_CHANNELS")
	}
	if timeout, err := time.ParseDuration(strings.TrimSpace(values["REQUEST_TIMEOUT"])); err == nil && timeout == 5*time.Minute {
		delete(values, "REQUEST_TIMEOUT")
	}
	if lowerTrim(values["FIRST_EVENT_HEDGE"]) == FirstEventHedgeOff {
		delete(values, "FIRST_EVENT_HEDGE")
	}
	backend := Default()
	configSetters["WAA_BACKEND"](&backend, values["WAA_BACKEND"])
	values["WAA_BACKEND"] = backend.WAABackend
	for key, value := range values {
		if key != "WAA_BACKEND" && isDefault(key, value) {
			delete(values, key)
		}
	}
	return true
}

// camoufoxOnlyAccounts 判断账户状态目录中已有账户而都没有 SpiderMonkey 的解释器缓存，即账户只在 Camoufox 后端下运行过
func camoufoxOnlyAccounts(authStates string) bool {
	found := false
	for _, directory := range strings.Split(authStates, ",") {
		directory = strings.TrimSpace(directory)
		if _, err := os.Stat(filepath.Join(directory, WAAInterpreterCacheName)); err == nil {
			return false
		}
		accounts, _ := filepath.Glob(filepath.Join(directory, "*", "account.json"))
		found = found || len(accounts) > 0
	}
	return found
}

// KeepEnvironment 返回把进程环境变量决定的项及其无效记录取 saved 中的值后的配置
func (c Config) KeepEnvironment(saved Config) Config {
	values := saved.envValues()
	for _, key := range saved.Environment {
		configSetters[key](&c, values[key])
	}
	c.Environment = saved.Environment
	c.Invalid = slices.DeleteFunc(slices.Clone(saved.Invalid), func(item InvalidValue) bool { return !slices.Contains(saved.Environment, item.Key) })
	return c
}

// envValues 返回每个配置键在 .env 中的文本值
func (c Config) envValues() map[string]string {
	return map[string]string{
		"AUTO_START":               strconv.FormatBool(c.AutoStart),
		"HIDE_DOCK_ICON":           strconv.FormatBool(c.HideDockIcon),
		"REQUEST_BODY_LOG":         strconv.FormatBool(c.RequestBodyLog),
		"LOG_FILE":                 strconv.FormatBool(c.LogFile),
		"LOG_MAX_TOTAL_MB":         strconv.Itoa(c.LogMaxTotalMB),
		"ADMIN_AUTH_ENABLED":       strconv.FormatBool(c.AdminAuthEnabled),
		"ADMIN_USERNAME":           c.AdminUsername,
		"ADMIN_PASSWORD":           c.AdminPassword,
		"BUILD_NATIVE_NONSTREAM":   strconv.FormatBool(c.BuildNativeNonstream),
		"NONSTREAM_KEEPALIVE":      strconv.FormatBool(c.NonstreamKeepalive),
		"FAKE_STREAM":              strconv.FormatBool(c.FakeStream),
		"YOUTUBE_LINKS":            strconv.FormatBool(c.YouTubeLinks),
		"STRIP_THOUGHT_SIGNATURES": strconv.FormatBool(c.StripThoughtSignatures),
		"SEARCH_MODEL_ALIASES":     strconv.FormatBool(c.SearchModelAliases),
		"GOOGLE_SEARCH":            strconv.FormatBool(c.GoogleSearch),
		"FIRST_EVENT_HEDGE":        c.FirstEventHedge,
		"FIRST_EVENT_HEDGE_MODELS": c.FirstEventHedgeModels,
		"CONTINUATION_REWRITE":     strconv.FormatBool(c.ContinuationRewrite),
		"CONTINUATION_PROMPT":      c.ContinuationPrompt,
		"AISTUDIO_AUTH_STATES":     c.AuthStates,
		"LISTEN_ADDR":              c.ListenAddr,
		"PREVIEW_LISTEN_ADDR":      c.PreviewListenAddr,
		"PROXY_API_KEY":            c.ProxyAPIKey,
		"PROXY":                    c.Proxy,
		"INIT_TIMEOUT":             c.InitTimeout.String(),
		"REQUEST_TIMEOUT":          c.RequestTimeout.String(),
		"WARM_WORKER_LIMIT":        strconv.Itoa(c.WarmWorkerLimit),
		"MAX_ACTIVE_WORKERS":       strconv.Itoa(c.MaxActiveWorkers),
		"WARM_STARTUP_CONCURRENCY": strconv.Itoa(c.WarmStartupConcurrency),
		"PER_ACCOUNT_CONCURRENCY":  strconv.Itoa(c.PerAccountConcurrency),
		"ROUTING_STRATEGY":         c.RoutingStrategy,
		"ACCOUNT_ORDER":            c.AccountOrder,
		"UPSTREAM_CHANNELS":        strings.Join(c.UpstreamChannels, ","),
		"MODEL_MAPPING":            c.ModelMapping,
		"MODEL_CHANNELS":           c.ModelChannels,
		"MODEL_ACCOUNTS":           c.ModelAccounts,
		"API_KEY_ACCOUNTS":         c.APIKeyAccounts,
		"WAA_BACKEND":              c.WAABackend,
		"MEDIA_OUTPUT":             c.MediaOutput,
		"MEDIA_BASE_URL":           c.MediaBaseURL,
	}
}

// Save 将 WAA 后端与其余和默认值不同的配置原子写入指定 env 文件，进程环境变量决定的项保留文件中原有的值；文件中没有的常驻预热账户数与活动 Worker 上限仍等于原后端的默认值时不写入，随后端取默认值
func (c Config) Save(path string) error {
	if err := c.Validate(); err != nil {
		return err
	}
	saved, _, err := readEnvFile(path)
	if err != nil {
		return err
	}
	values, defaults := c.envValues(), defaultsFor(c.WAABackend).envValues()
	previous := Default()
	if value, ok := saved["WAA_BACKEND"]; ok {
		_ = configSetters["WAA_BACKEND"](&previous, value)
	}
	previousDefaults := defaultsFor(previous.WAABackend).envValues()
	file := make(map[string]string)
	for _, key := range configKeys {
		_, inFile := saved[key]
		switch {
		case slices.Contains(c.Environment, key):
			if inFile {
				file[key] = saved[key]
			}
		case key == "WAA_BACKEND":
			file[key] = values[key]
		case slices.Contains(workerDefaultKeys, key) && !inFile && values[key] == previousDefaults[key]:
		case values[key] != defaults[key]:
			file[key] = values[key]
		}
	}
	return writeEnv(path, file)
}

// writeEnv 按 configKeys 的顺序原子写入 values 中的键
func writeEnv(path string, values map[string]string) error {
	var output strings.Builder
	for _, key := range configKeys {
		if value, ok := values[key]; ok {
			output.WriteString(key)
			output.WriteByte('=')
			output.WriteString(formatEnvValue(value))
			output.WriteByte('\n')
		}
	}
	return atomicWrite(path, []byte(output.String()), 0o600)
}

// appendEnv 在 env 文件末尾原子追加一个键，文件不存在时创建
func appendEnv(path, key, value string) error {
	content, err := os.ReadFile(path)
	if err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("读取配置文件: %w", err)
	}
	if len(content) > 0 && content[len(content)-1] != '\n' {
		content = append(content, '\n')
	}
	return atomicWrite(path, append(content, key+"="+formatEnvValue(value)+"\n"...), 0o600)
}

// Validate 校验配置值是否能用于服务启动
func (c Config) Validate() error {
	if c.AdminAuthEnabled && (strings.TrimSpace(c.AdminUsername) == "" || strings.TrimSpace(c.AdminPassword) == "") {
		return fmt.Errorf("开启管理登录需要 ADMIN_USERNAME 与 ADMIN_PASSWORD")
	}
	for _, check := range configChecks {
		if err := check.check(c); err != nil {
			return err
		}
	}
	return nil
}

// ParseFirstEventHedge 解析 FIRST_EVENT_HEDGE，返回是否按近期 p95 自适应与自定义的等待时长，off 时都为零值
func ParseFirstEventHedge(value string) (bool, time.Duration, error) {
	switch value {
	case FirstEventHedgeOff:
		return false, 0, nil
	case FirstEventHedgeAuto:
		return true, 0, nil
	}
	seconds, err := strconv.Atoi(value)
	if err != nil || seconds <= 0 {
		return false, 0, fmt.Errorf("FIRST_EVENT_HEDGE 必须是 off、auto 或正整数秒")
	}
	return false, time.Duration(seconds) * time.Second, nil
}

// ModelList 解析逗号分隔的模型 ID 列表
func ModelList(value string) []string {
	var models []string
	for item := range strings.SplitSeq(value, ",") {
		if item = strings.TrimPrefix(strings.TrimSpace(item), "models/"); item != "" && !slices.Contains(models, item) {
			models = append(models, item)
		}
	}
	return models
}

// ModelAlias 表示客户端模型名到目录模型 ID 的一项映射
type ModelAlias struct {
	Alias string
	Model string
}

// ParseAPIKeyAccounts 解析逗号分隔的 API key:账户|账户，以最后一个冒号分隔，跳过不在 apiKeys 中的 key
func ParseAPIKeyAccounts(value string, apiKeys string) (map[string][]string, error) {
	keys := APIKeys(apiKeys)
	accounts := make(map[string][]string)
	for _, item := range strings.Split(value, ",") {
		item = strings.TrimSpace(item)
		if item == "" {
			continue
		}
		index := strings.LastIndexByte(item, ':')
		key := strings.TrimSpace(item[:max(index, 0)])
		ids := accountIDs(item[index+1:])
		if index < 0 || key == "" || len(ids) == 0 {
			return nil, fmt.Errorf("API_KEY_ACCOUNTS 的每一项写成 API key:账户|账户")
		}
		if !slices.Contains(keys, key) {
			continue
		}
		if _, exists := accounts[key]; exists {
			return nil, fmt.Errorf("API_KEY_ACCOUNTS 中同一 key 重复")
		}
		accounts[key] = ids
	}
	return accounts, nil
}

// ParseModelAccounts 解析逗号分隔的 模型 ID:账户|账户，以最后一个冒号分隔，返回每个模型可用的账户 ID
func ParseModelAccounts(value string) (map[string][]string, error) {
	accounts := make(map[string][]string)
	for item := range strings.SplitSeq(value, ",") {
		item = strings.TrimSpace(item)
		if item == "" {
			continue
		}
		index := strings.LastIndexByte(item, ':')
		model := strings.TrimPrefix(strings.TrimSpace(item[:max(index, 0)]), "models/")
		ids := accountIDs(item[index+1:])
		if index < 0 || model == "" || len(ids) == 0 {
			return nil, fmt.Errorf("MODEL_ACCOUNTS 的每一项写成 模型 ID:账户|账户，%q 不符合", item)
		}
		if _, exists := accounts[model]; exists {
			return nil, fmt.Errorf("MODEL_ACCOUNTS 中模型 %s 重复", model)
		}
		accounts[model] = ids
	}
	return accounts, nil
}

// accountIDs 返回以 | 分隔、去掉空白的各个账户 ID
func accountIDs(value string) []string {
	var ids []string
	for id := range strings.SplitSeq(value, "|") {
		if id = strings.TrimSpace(id); id != "" {
			ids = append(ids, id)
		}
	}
	return ids
}

// APIKeys 返回 PROXY_API_KEY 中逗号分隔、去掉空白的各个 key
func APIKeys(value string) []string {
	var keys []string
	for key := range strings.SplitSeq(value, ",") {
		if key = strings.TrimSpace(key); key != "" {
			keys = append(keys, key)
		}
	}
	return keys
}

// ParseModelMapping 解析逗号分隔的 别名:模型 ID，别名可含冒号，以最后一个冒号分隔
func ParseModelMapping(value string) ([]ModelAlias, error) {
	var mapping []ModelAlias
	for _, item := range strings.Split(value, ",") {
		item = strings.TrimSpace(item)
		if item == "" {
			continue
		}
		index := strings.LastIndexByte(item, ':')
		alias, model := strings.TrimSpace(item[:max(index, 0)]), strings.TrimSpace(item[index+1:])
		if index < 0 || alias == "" || model == "" {
			return nil, fmt.Errorf("MODEL_MAPPING 的每一项写成 别名:模型 ID，%q 不符合", item)
		}
		mapping = append(mapping, ModelAlias{Alias: alias, Model: strings.TrimPrefix(model, "models/")})
	}
	return mapping, nil
}

// ParseModelChannels 解析逗号分隔的 模型 ID:通道，返回每个模型限定使用的通道
func ParseModelChannels(value string) (map[string]string, error) {
	channels := make(map[string]string)
	for _, item := range strings.Split(value, ",") {
		item = strings.TrimSpace(item)
		if item == "" {
			continue
		}
		index := strings.LastIndexByte(item, ':')
		model := strings.TrimPrefix(strings.TrimSpace(item[:max(index, 0)]), "models/")
		channel := strings.ToLower(strings.TrimSpace(item[index+1:]))
		if index < 0 || model == "" || !slices.Contains(upstreamChannels, channel) {
			return nil, fmt.Errorf("MODEL_CHANNELS 的每一项写成 模型 ID:playground 或 模型 ID:build，%q 不符合", item)
		}
		if _, exists := channels[model]; exists {
			return nil, fmt.Errorf("MODEL_CHANNELS 中模型 %s 重复", model)
		}
		channels[model] = channel
	}
	return channels, nil
}

// parseUpstreamChannels 把逗号分隔的通道列表拆成去空白的小写名称
func parseUpstreamChannels(value string) []string {
	var channels []string
	for _, item := range strings.Split(value, ",") {
		if item = strings.ToLower(strings.TrimSpace(item)); item != "" {
			channels = append(channels, item)
		}
	}
	return channels
}

// validateUpstreamChannels 要求至少一个已知且不重复的上游通道
func validateUpstreamChannels(channels []string) error {
	if len(channels) == 0 {
		return fmt.Errorf("UPSTREAM_CHANNELS 至少包含 playground 或 build")
	}
	for index, channel := range channels {
		if !slices.Contains(upstreamChannels, channel) {
			return fmt.Errorf("UPSTREAM_CHANNELS 只能包含 playground 与 build")
		}
		if slices.Contains(channels[:index], channel) {
			return fmt.Errorf("UPSTREAM_CHANNELS 通道 %s 重复", channel)
		}
	}
	return nil
}

// ProxyList 返回 PROXY 中以逗号分隔的代理 URL
func ProxyList(value string) []string {
	var proxies []string
	for proxy := range strings.SplitSeq(value, ",") {
		if proxy = strings.TrimSpace(proxy); proxy != "" && !slices.Contains(proxies, proxy) {
			proxies = append(proxies, proxy)
		}
	}
	return proxies
}

// ValidateProxy 校验账户或全局代理 URL
func ValidateProxy(value string) error {
	value = strings.TrimSpace(value)
	if value == "" {
		return nil
	}
	parsed, err := url.Parse(value)
	if err != nil || parsed.Hostname() == "" {
		return fmt.Errorf("PROXY 必须是 http、https 或 socks5 URL")
	}
	switch parsed.Scheme {
	case "http", "https", "socks5":
	default:
		return fmt.Errorf("PROXY 必须是 http、https 或 socks5 URL")
	}
	if parsed.User != nil && parsed.User.Username() == "" {
		return fmt.Errorf("PROXY 认证信息缺少用户名")
	}
	if parsed.Path != "" && parsed.Path != "/" || parsed.RawQuery != "" || parsed.Fragment != "" {
		return fmt.Errorf("PROXY 不能包含路径、查询参数或片段")
	}
	return nil
}

// readEnvFile 读取 env 文件中的配置键，并返回文件是否含注释行
func readEnvFile(path string) (map[string]string, bool, error) {
	values := make(map[string]string)
	commented := false
	file, err := os.Open(path)
	if os.IsNotExist(err) {
		return values, false, nil
	}
	if err != nil {
		return nil, false, fmt.Errorf("读取配置文件: %w", err)
	}
	defer file.Close()

	scanner := bufio.NewScanner(file)
	for lineNumber := 1; scanner.Scan(); lineNumber++ {
		line := strings.TrimSpace(scanner.Text())
		if strings.HasPrefix(line, "#") {
			commented = true
			continue
		}
		if line == "" {
			continue
		}
		key, raw, ok := strings.Cut(line, "=")
		if !ok {
			return nil, false, fmt.Errorf("%s:%d 缺少等号", path, lineNumber)
		}
		key = strings.TrimSpace(key)
		if !slices.Contains(configKeys[:], key) {
			continue
		}
		value, err := parseEnvValue(strings.TrimSpace(raw))
		if err != nil {
			return nil, false, fmt.Errorf("%s:%d: %w", path, lineNumber, err)
		}
		values[key] = value
	}
	if err := scanner.Err(); err != nil {
		return nil, false, fmt.Errorf("读取配置文件: %w", err)
	}
	return values, commented, nil
}

func parseEnvValue(value string) (string, error) {
	if value == "" {
		return "", nil
	}
	if value[0] == '\'' {
		if len(value) < 2 || value[len(value)-1] != '\'' {
			return "", fmt.Errorf("单引号未闭合")
		}
		return value[1 : len(value)-1], nil
	}
	if value[0] == '"' {
		parsed, err := strconv.Unquote(value)
		if err != nil {
			return "", fmt.Errorf("双引号值无效")
		}
		return parsed, nil
	}
	if index := strings.Index(value, " #"); index >= 0 {
		value = strings.TrimSpace(value[:index])
	}
	return value, nil
}

func formatEnvValue(value string) string {
	if strings.ContainsAny(value, " \t\r\n#\"'") {
		return strconv.Quote(value)
	}
	return value
}

func parsePositiveDuration(key string, value string) (time.Duration, error) {
	duration, err := time.ParseDuration(strings.TrimSpace(value))
	if err != nil || duration <= 0 {
		return 0, fmt.Errorf("%s 必须是正数时长，例如 30s 或 5m", key)
	}
	return duration, nil
}

func parsePositiveInt(key string, value string) (int, error) {
	parsed, err := strconv.Atoi(strings.TrimSpace(value))
	if err != nil || parsed <= 0 {
		return 0, fmt.Errorf("%s 必须是正整数", key)
	}
	return parsed, nil
}

func validateListenAddr(key, value string) error {
	_, port, err := net.SplitHostPort(strings.TrimSpace(value))
	if err != nil || port == "" {
		return fmt.Errorf("%s 必须是 host:port", key)
	}
	if parsed, err := strconv.ParseUint(port, 10, 16); err != nil || parsed == 0 {
		return fmt.Errorf("%s 端口必须是 1 到 65535", key)
	}
	return nil
}

// listenPort 返回监听地址中的端口
func listenPort(value string) string {
	_, port, _ := net.SplitHostPort(strings.TrimSpace(value))
	return port
}

func atomicWrite(path string, data []byte, mode os.FileMode) error {
	temporary, err := os.CreateTemp(filepath.Dir(path), ".env-*.tmp")
	if err != nil {
		return fmt.Errorf("创建临时配置: %w", err)
	}
	temporaryPath := temporary.Name()
	defer os.Remove(temporaryPath)

	if err := temporary.Chmod(mode); err != nil {
		temporary.Close()
		return fmt.Errorf("设置配置权限: %w", err)
	}
	if _, err := bytes.NewReader(data).WriteTo(temporary); err != nil {
		temporary.Close()
		return fmt.Errorf("写入配置: %w", err)
	}
	if err := temporary.Sync(); err != nil {
		temporary.Close()
		return fmt.Errorf("同步配置: %w", err)
	}
	if err := temporary.Close(); err != nil {
		return fmt.Errorf("关闭配置: %w", err)
	}
	if err := os.Rename(temporaryPath, path); err != nil {
		return fmt.Errorf("替换配置: %w", err)
	}
	return nil
}
