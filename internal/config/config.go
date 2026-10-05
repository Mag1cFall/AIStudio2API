package config

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

const (
	defaultAuthStates         = "auth"
	defaultListenAddr         = "127.0.0.1:2048"
	defaultInitTimeout        = 2 * time.Minute
	defaultRequestTimeout     = 5 * time.Minute
	defaultWarmWorkerLimit    = 5
	defaultMaxActiveWorkers   = 10
	defaultWarmConcurrency    = 2
	defaultAccountConcurrency = 2
)

var configKeys = [...]string{
	"AISTUDIO_AUTH_STATES",
	"LISTEN_ADDR",
	"PROXY_API_KEY",
	"PROXY",
	"INIT_TIMEOUT",
	"REQUEST_TIMEOUT",
	"WARM_WORKER_LIMIT",
	"MAX_ACTIVE_WORKERS",
	"WARM_STARTUP_CONCURRENCY",
	"PER_ACCOUNT_CONCURRENCY",
	"ROUTING_STRATEGY",
	"UPSTREAM_CHANNELS",
	"TEMPORARY_CHAT",
	"WAA_BACKEND",
	"ADMIN_AUTH_ENABLED",
	"ADMIN_USERNAME",
	"ADMIN_PASSWORD",
	"BUILD_NATIVE_NONSTREAM",
}

// upstreamChannels 表示生成请求可启用的上游通道
var upstreamChannels = []string{"playground", "build"}

// WAABackendCamoufox 表示由 Camoufox 页面承担 WAA 生命周期
const WAABackendCamoufox = "camoufox"

// WAABackendGo 表示由纯 Go VM 承担 WAA 生命周期
const WAABackendGo = "go"

// Config 保存服务的全局配置
type Config struct {
	AdminAuthEnabled       bool          `json:"admin_auth_enabled"`
	AdminUsername          string        `json:"admin_username"`
	AdminPassword          string        `json:"-"`
	BuildNativeNonstream   bool          `json:"build_native_nonstream"`
	AuthStates             string        `json:"auth_states"`
	ListenAddr             string        `json:"listen_addr"`
	ProxyAPIKey            string        `json:"proxy_api_key"`
	Proxy                  string        `json:"proxy"`
	InitTimeout            time.Duration `json:"-"`
	RequestTimeout         time.Duration `json:"-"`
	WarmWorkerLimit        int           `json:"warm_worker_limit"`
	MaxActiveWorkers       int           `json:"max_active_workers"`
	WarmStartupConcurrency int           `json:"warm_startup_concurrency"`
	PerAccountConcurrency  int           `json:"per_account_concurrency"`
	RoutingStrategy        string        `json:"routing_strategy"`
	UpstreamChannels       []string      `json:"upstream_channels"`
	TemporaryChat          bool          `json:"temporary_chat"`
	WAABackend             string        `json:"waa_backend"`
}

// Default 返回可直接启动的默认配置
func Default() Config {
	return Config{
		AdminUsername:          "admin",
		BuildNativeNonstream:   true,
		AuthStates:             defaultAuthStates,
		ListenAddr:             defaultListenAddr,
		InitTimeout:            defaultInitTimeout,
		RequestTimeout:         defaultRequestTimeout,
		WarmWorkerLimit:        defaultWarmWorkerLimit,
		MaxActiveWorkers:       defaultMaxActiveWorkers,
		WarmStartupConcurrency: defaultWarmConcurrency,
		PerAccountConcurrency:  defaultAccountConcurrency,
		RoutingStrategy:        "round-robin",
		UpstreamChannels:       append([]string(nil), upstreamChannels...),
		WAABackend:             WAABackendCamoufox,
	}
}

// Load 从指定 env 文件和进程环境加载配置
func Load(path string) (Config, error) {
	values, err := readEnvFile(path)
	if err != nil {
		return Config{}, err
	}
	for _, key := range configKeys {
		if value, ok := os.LookupEnv(key); ok {
			values[key] = value
		}
	}

	cfg := Default()
	if value, ok := values["ADMIN_AUTH_ENABLED"]; ok {
		cfg.AdminAuthEnabled, err = strconv.ParseBool(strings.TrimSpace(value))
		if err != nil {
			return Config{}, fmt.Errorf("ADMIN_AUTH_ENABLED 必须是 true 或 false")
		}
	}
	if value, ok := values["ADMIN_USERNAME"]; ok {
		cfg.AdminUsername = strings.TrimSpace(value)
	}
	if value, ok := values["ADMIN_PASSWORD"]; ok {
		cfg.AdminPassword = value
	}
	if value, ok := values["BUILD_NATIVE_NONSTREAM"]; ok {
		cfg.BuildNativeNonstream, err = strconv.ParseBool(strings.TrimSpace(value))
		if err != nil {
			return Config{}, fmt.Errorf("BUILD_NATIVE_NONSTREAM 必须是 true 或 false")
		}
	}
	if value, ok := values["AISTUDIO_AUTH_STATES"]; ok {
		cfg.AuthStates = strings.TrimSpace(value)
	}
	if value, ok := values["LISTEN_ADDR"]; ok {
		cfg.ListenAddr = strings.TrimSpace(value)
	}
	if value, ok := values["PROXY_API_KEY"]; ok {
		cfg.ProxyAPIKey = strings.TrimSpace(value)
	}
	if value, ok := values["PROXY"]; ok {
		cfg.Proxy = strings.TrimSpace(value)
	}
	if value, ok := values["INIT_TIMEOUT"]; ok {
		cfg.InitTimeout, err = parsePositiveDuration("INIT_TIMEOUT", value)
		if err != nil {
			return Config{}, err
		}
	}
	if value, ok := values["REQUEST_TIMEOUT"]; ok {
		cfg.RequestTimeout, err = parsePositiveDuration("REQUEST_TIMEOUT", value)
		if err != nil {
			return Config{}, err
		}
	}
	if value, ok := values["WARM_WORKER_LIMIT"]; ok {
		cfg.WarmWorkerLimit, err = parsePositiveInt("WARM_WORKER_LIMIT", value)
		if err != nil {
			return Config{}, err
		}
	}
	if value, ok := values["MAX_ACTIVE_WORKERS"]; ok {
		cfg.MaxActiveWorkers, err = parsePositiveInt("MAX_ACTIVE_WORKERS", value)
		if err != nil {
			return Config{}, err
		}
	}
	if value, ok := values["WARM_STARTUP_CONCURRENCY"]; ok {
		cfg.WarmStartupConcurrency, err = parsePositiveInt("WARM_STARTUP_CONCURRENCY", value)
		if err != nil {
			return Config{}, err
		}
	}
	if value, ok := values["PER_ACCOUNT_CONCURRENCY"]; ok {
		cfg.PerAccountConcurrency, err = parsePositiveInt("PER_ACCOUNT_CONCURRENCY", value)
		if err != nil {
			return Config{}, err
		}
	}
	if value, ok := values["ROUTING_STRATEGY"]; ok {
		cfg.RoutingStrategy = strings.TrimSpace(value)
	}
	if value, ok := values["UPSTREAM_CHANNELS"]; ok {
		cfg.UpstreamChannels = ParseUpstreamChannels(value)
	}
	if value, ok := values["TEMPORARY_CHAT"]; ok {
		cfg.TemporaryChat, err = strconv.ParseBool(strings.TrimSpace(value))
		if err != nil {
			return Config{}, fmt.Errorf("TEMPORARY_CHAT 必须是 true 或 false")
		}
	}
	if value, ok := values["WAA_BACKEND"]; ok {
		cfg.WAABackend = strings.ToLower(strings.TrimSpace(value))
	}
	if err := cfg.Validate(); err != nil {
		return Config{}, err
	}
	return cfg, nil
}

// Save 将配置原子写入指定 env 文件
func (c Config) Save(path string) error {
	if err := c.Validate(); err != nil {
		return err
	}
	values := map[string]string{
		"ADMIN_AUTH_ENABLED":       strconv.FormatBool(c.AdminAuthEnabled),
		"ADMIN_USERNAME":           c.AdminUsername,
		"ADMIN_PASSWORD":           c.AdminPassword,
		"BUILD_NATIVE_NONSTREAM":   strconv.FormatBool(c.BuildNativeNonstream),
		"AISTUDIO_AUTH_STATES":     c.AuthStates,
		"LISTEN_ADDR":              c.ListenAddr,
		"PROXY_API_KEY":            c.ProxyAPIKey,
		"PROXY":                    c.Proxy,
		"INIT_TIMEOUT":             c.InitTimeout.String(),
		"REQUEST_TIMEOUT":          c.RequestTimeout.String(),
		"WARM_WORKER_LIMIT":        strconv.Itoa(c.WarmWorkerLimit),
		"MAX_ACTIVE_WORKERS":       strconv.Itoa(c.MaxActiveWorkers),
		"WARM_STARTUP_CONCURRENCY": strconv.Itoa(c.WarmStartupConcurrency),
		"PER_ACCOUNT_CONCURRENCY":  strconv.Itoa(c.PerAccountConcurrency),
		"ROUTING_STRATEGY":         c.RoutingStrategy,
		"UPSTREAM_CHANNELS":        strings.Join(c.UpstreamChannels, ","),
		"TEMPORARY_CHAT":           strconv.FormatBool(c.TemporaryChat),
		"WAA_BACKEND":              c.WAABackend,
	}

	var output strings.Builder
	for _, key := range configKeys {
		output.WriteString(key)
		output.WriteByte('=')
		output.WriteString(formatEnvValue(values[key]))
		output.WriteByte('\n')
	}
	return atomicWrite(path, []byte(output.String()), 0o600)
}

// Validate 校验配置值是否能用于服务启动
func (c Config) Validate() error {
	if c.AdminAuthEnabled && (strings.TrimSpace(c.AdminUsername) == "" || strings.TrimSpace(c.AdminPassword) == "") {
		return fmt.Errorf("开启管理登录需要 ADMIN_USERNAME 与 ADMIN_PASSWORD")
	}
	if strings.TrimSpace(c.AuthStates) == "" {
		return fmt.Errorf("AISTUDIO_AUTH_STATES 不能为空")
	}
	if err := validateListenAddr(c.ListenAddr); err != nil {
		return err
	}
	if err := ValidateProxy(c.Proxy); err != nil {
		return err
	}
	if c.InitTimeout <= 0 {
		return fmt.Errorf("INIT_TIMEOUT 必须是正数时长")
	}
	if c.RequestTimeout <= 0 {
		return fmt.Errorf("REQUEST_TIMEOUT 必须是正数时长")
	}
	if c.WarmWorkerLimit <= 0 {
		return fmt.Errorf("WARM_WORKER_LIMIT 必须是正整数")
	}
	if c.MaxActiveWorkers < c.WarmWorkerLimit {
		return fmt.Errorf("MAX_ACTIVE_WORKERS 必须大于或等于 WARM_WORKER_LIMIT")
	}
	if c.WarmStartupConcurrency <= 0 || c.WarmStartupConcurrency > c.WarmWorkerLimit {
		return fmt.Errorf("WARM_STARTUP_CONCURRENCY 必须是 1 到 WARM_WORKER_LIMIT")
	}
	if c.PerAccountConcurrency <= 0 {
		return fmt.Errorf("PER_ACCOUNT_CONCURRENCY 必须是正整数")
	}
	if c.RoutingStrategy != "round-robin" && c.RoutingStrategy != "fill-first" {
		return fmt.Errorf("ROUTING_STRATEGY 必须是 round-robin 或 fill-first")
	}
	if err := validateUpstreamChannels(c.UpstreamChannels); err != nil {
		return err
	}
	if c.WAABackend != WAABackendCamoufox && c.WAABackend != WAABackendGo {
		return fmt.Errorf("WAA_BACKEND 必须是 camoufox 或 go")
	}
	return nil
}

// ParseUpstreamChannels 把逗号分隔的通道列表拆成去空白的小写名称
func ParseUpstreamChannels(value string) []string {
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
	seen := make(map[string]struct{}, len(channels))
	for _, channel := range channels {
		known := false
		for _, candidate := range upstreamChannels {
			known = known || channel == candidate
		}
		if !known {
			return fmt.Errorf("UPSTREAM_CHANNELS 只能包含 playground 与 build")
		}
		if _, exists := seen[channel]; exists {
			return fmt.Errorf("UPSTREAM_CHANNELS 通道 %s 重复", channel)
		}
		seen[channel] = struct{}{}
	}
	return nil
}

// MarshalJSON 将时长输出为 env 使用的文本格式
func (c Config) MarshalJSON() ([]byte, error) {
	type payload struct {
		AdminAuthEnabled       bool     `json:"admin_auth_enabled"`
		AdminUsername          string   `json:"admin_username"`
		BuildNativeNonstream   bool     `json:"build_native_nonstream"`
		AuthStates             string   `json:"auth_states"`
		ListenAddr             string   `json:"listen_addr"`
		ProxyAPIKey            string   `json:"proxy_api_key"`
		Proxy                  string   `json:"proxy"`
		InitTimeout            string   `json:"init_timeout"`
		RequestTimeout         string   `json:"request_timeout"`
		WarmWorkerLimit        int      `json:"warm_worker_limit"`
		MaxActiveWorkers       int      `json:"max_active_workers"`
		WarmStartupConcurrency int      `json:"warm_startup_concurrency"`
		PerAccountConcurrency  int      `json:"per_account_concurrency"`
		RoutingStrategy        string   `json:"routing_strategy"`
		UpstreamChannels       []string `json:"upstream_channels"`
		TemporaryChat          bool     `json:"temporary_chat"`
		WAABackend             string   `json:"waa_backend"`
	}
	return json.Marshal(payload{
		AdminAuthEnabled:       c.AdminAuthEnabled,
		AdminUsername:          c.AdminUsername,
		BuildNativeNonstream:   c.BuildNativeNonstream,
		AuthStates:             c.AuthStates,
		ListenAddr:             c.ListenAddr,
		ProxyAPIKey:            c.ProxyAPIKey,
		Proxy:                  c.Proxy,
		InitTimeout:            c.InitTimeout.String(),
		RequestTimeout:         c.RequestTimeout.String(),
		WarmWorkerLimit:        c.WarmWorkerLimit,
		MaxActiveWorkers:       c.MaxActiveWorkers,
		WarmStartupConcurrency: c.WarmStartupConcurrency,
		PerAccountConcurrency:  c.PerAccountConcurrency,
		RoutingStrategy:        c.RoutingStrategy,
		UpstreamChannels:       c.UpstreamChannels,
		TemporaryChat:          c.TemporaryChat,
		WAABackend:             c.WAABackend,
	})
}

// UnmarshalJSON 从管理接口使用的文本时长解析配置
func (c *Config) UnmarshalJSON(data []byte) error {
	type payload struct {
		AdminAuthEnabled       bool     `json:"admin_auth_enabled"`
		AdminUsername          string   `json:"admin_username"`
		AdminPassword          string   `json:"admin_password"`
		BuildNativeNonstream   bool     `json:"build_native_nonstream"`
		AuthStates             string   `json:"auth_states"`
		ListenAddr             string   `json:"listen_addr"`
		ProxyAPIKey            string   `json:"proxy_api_key"`
		Proxy                  string   `json:"proxy"`
		InitTimeout            string   `json:"init_timeout"`
		RequestTimeout         string   `json:"request_timeout"`
		WarmWorkerLimit        int      `json:"warm_worker_limit"`
		MaxActiveWorkers       int      `json:"max_active_workers"`
		WarmStartupConcurrency int      `json:"warm_startup_concurrency"`
		PerAccountConcurrency  int      `json:"per_account_concurrency"`
		RoutingStrategy        string   `json:"routing_strategy"`
		UpstreamChannels       []string `json:"upstream_channels"`
		TemporaryChat          bool     `json:"temporary_chat"`
		WAABackend             string   `json:"waa_backend"`
	}
	var value payload
	if err := json.Unmarshal(data, &value); err != nil {
		return err
	}
	initTimeout, err := parsePositiveDuration("INIT_TIMEOUT", value.InitTimeout)
	if err != nil {
		return err
	}
	requestTimeout, err := parsePositiveDuration("REQUEST_TIMEOUT", value.RequestTimeout)
	if err != nil {
		return err
	}
	parsed := Config{
		AdminAuthEnabled:       value.AdminAuthEnabled,
		AdminUsername:          strings.TrimSpace(value.AdminUsername),
		AdminPassword:          value.AdminPassword,
		BuildNativeNonstream:   value.BuildNativeNonstream,
		AuthStates:             strings.TrimSpace(value.AuthStates),
		ListenAddr:             strings.TrimSpace(value.ListenAddr),
		ProxyAPIKey:            strings.TrimSpace(value.ProxyAPIKey),
		Proxy:                  strings.TrimSpace(value.Proxy),
		InitTimeout:            initTimeout,
		RequestTimeout:         requestTimeout,
		WarmWorkerLimit:        value.WarmWorkerLimit,
		MaxActiveWorkers:       value.MaxActiveWorkers,
		WarmStartupConcurrency: value.WarmStartupConcurrency,
		PerAccountConcurrency:  value.PerAccountConcurrency,
		RoutingStrategy:        value.RoutingStrategy,
		UpstreamChannels:       value.UpstreamChannels,
		TemporaryChat:          value.TemporaryChat,
		WAABackend:             strings.ToLower(strings.TrimSpace(value.WAABackend)),
	}
	if err := parsed.Validate(); err != nil {
		return err
	}
	*c = parsed
	return nil
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
	if parsed.User != nil {
		return fmt.Errorf("PROXY 不能包含认证信息")
	}
	if parsed.Path != "" && parsed.Path != "/" || parsed.RawQuery != "" || parsed.Fragment != "" {
		return fmt.Errorf("PROXY 不能包含路径、查询参数或片段")
	}
	return nil
}

func readEnvFile(path string) (map[string]string, error) {
	values := make(map[string]string)
	if strings.TrimSpace(path) == "" {
		return values, nil
	}
	file, err := os.Open(path)
	if os.IsNotExist(err) {
		return values, nil
	}
	if err != nil {
		return nil, fmt.Errorf("读取配置文件: %w", err)
	}
	defer file.Close()

	scanner := bufio.NewScanner(file)
	for lineNumber := 1; scanner.Scan(); lineNumber++ {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		key, raw, ok := strings.Cut(line, "=")
		if !ok {
			return nil, fmt.Errorf("%s:%d 缺少等号", path, lineNumber)
		}
		key = strings.TrimSpace(key)
		if !isConfigKey(key) {
			continue
		}
		value, err := parseEnvValue(strings.TrimSpace(raw))
		if err != nil {
			return nil, fmt.Errorf("%s:%d: %w", path, lineNumber, err)
		}
		values[key] = value
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("读取配置文件: %w", err)
	}
	return values, nil
}

func isConfigKey(value string) bool {
	for _, key := range configKeys {
		if value == key {
			return true
		}
	}
	return false
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
	if value == "" {
		return ""
	}
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

func validateListenAddr(value string) error {
	host, port, err := net.SplitHostPort(strings.TrimSpace(value))
	if err != nil || port == "" {
		return fmt.Errorf("LISTEN_ADDR 必须是 host:port")
	}
	if host == "" {
		host = "0.0.0.0"
	}
	if parsed, err := strconv.ParseUint(port, 10, 16); err != nil || parsed == 0 {
		return fmt.Errorf("LISTEN_ADDR 端口必须是 1 到 65535")
	}
	return nil
}

func atomicWrite(path string, data []byte, mode os.FileMode) error {
	target, err := filepath.Abs(path)
	if err != nil {
		return fmt.Errorf("解析配置路径: %w", err)
	}
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		return fmt.Errorf("创建配置目录: %w", err)
	}
	temporary, err := os.CreateTemp(filepath.Dir(target), ".env-*.tmp")
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
	if err := os.Rename(temporaryPath, target); err != nil {
		return fmt.Errorf("替换配置: %w", err)
	}
	return nil
}
