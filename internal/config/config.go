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
	defaultAuthStates     = "auth"
	defaultListenAddr     = "127.0.0.1:2048"
	defaultInitTimeout    = 2 * time.Minute
	defaultRequestTimeout = 5 * time.Minute
)

var configKeys = [...]string{
	"AISTUDIO_AUTH_STATES",
	"LISTEN_ADDR",
	"PROXY_API_KEY",
	"PROXY",
	"INIT_TIMEOUT",
	"REQUEST_TIMEOUT",
}

// Config 保存服务的六项全局配置
type Config struct {
	AuthStates     string        `json:"auth_states"`
	ListenAddr     string        `json:"listen_addr"`
	ProxyAPIKey    string        `json:"proxy_api_key"`
	Proxy          string        `json:"proxy"`
	InitTimeout    time.Duration `json:"-"`
	RequestTimeout time.Duration `json:"-"`
}

// Default 返回可直接启动的默认配置
func Default() Config {
	return Config{
		AuthStates:     defaultAuthStates,
		ListenAddr:     defaultListenAddr,
		InitTimeout:    defaultInitTimeout,
		RequestTimeout: defaultRequestTimeout,
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
	if err := cfg.Validate(); err != nil {
		return Config{}, err
	}
	return cfg, nil
}

// Save 将六项配置原子写入指定 env 文件
func (c Config) Save(path string) error {
	if err := c.Validate(); err != nil {
		return err
	}
	values := map[string]string{
		"AISTUDIO_AUTH_STATES": c.AuthStates,
		"LISTEN_ADDR":          c.ListenAddr,
		"PROXY_API_KEY":        c.ProxyAPIKey,
		"PROXY":                c.Proxy,
		"INIT_TIMEOUT":         c.InitTimeout.String(),
		"REQUEST_TIMEOUT":      c.RequestTimeout.String(),
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
	return nil
}

// MarshalJSON 将时长输出为 env 使用的文本格式
func (c Config) MarshalJSON() ([]byte, error) {
	type payload struct {
		AuthStates     string `json:"auth_states"`
		ListenAddr     string `json:"listen_addr"`
		ProxyAPIKey    string `json:"proxy_api_key"`
		Proxy          string `json:"proxy"`
		InitTimeout    string `json:"init_timeout"`
		RequestTimeout string `json:"request_timeout"`
	}
	return json.Marshal(payload{
		AuthStates:     c.AuthStates,
		ListenAddr:     c.ListenAddr,
		ProxyAPIKey:    c.ProxyAPIKey,
		Proxy:          c.Proxy,
		InitTimeout:    c.InitTimeout.String(),
		RequestTimeout: c.RequestTimeout.String(),
	})
}

// UnmarshalJSON 从管理接口使用的文本时长解析配置
func (c *Config) UnmarshalJSON(data []byte) error {
	type payload struct {
		AuthStates     string `json:"auth_states"`
		ListenAddr     string `json:"listen_addr"`
		ProxyAPIKey    string `json:"proxy_api_key"`
		Proxy          string `json:"proxy"`
		InitTimeout    string `json:"init_timeout"`
		RequestTimeout string `json:"request_timeout"`
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
		AuthStates:     strings.TrimSpace(value.AuthStates),
		ListenAddr:     strings.TrimSpace(value.ListenAddr),
		ProxyAPIKey:    strings.TrimSpace(value.ProxyAPIKey),
		Proxy:          strings.TrimSpace(value.Proxy),
		InitTimeout:    initTimeout,
		RequestTimeout: requestTimeout,
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
