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
	"TEMPORARY_CHAT",
}

// Config holds the global configuration for the service.
type Config struct {
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
	TemporaryChat          bool          `json:"temporary_chat"`
}

// Default returns a default configuration ready for startup.
func Default() Config {
	return Config{
		AuthStates:             defaultAuthStates,
		ListenAddr:             defaultListenAddr,
		InitTimeout:            defaultInitTimeout,
		RequestTimeout:         defaultRequestTimeout,
		WarmWorkerLimit:        defaultWarmWorkerLimit,
		MaxActiveWorkers:       defaultMaxActiveWorkers,
		WarmStartupConcurrency: defaultWarmConcurrency,
		PerAccountConcurrency:  defaultAccountConcurrency,
		RoutingStrategy:        "round-robin",
	}
}

// Load reads configuration from the specified env file and environment variables.
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
	if value, ok := values["TEMPORARY_CHAT"]; ok {
		cfg.TemporaryChat, err = strconv.ParseBool(strings.TrimSpace(value))
		if err != nil {
			return Config{}, fmt.Errorf("TEMPORARY_CHAT must be true or false")
		}
	}

	if err := cfg.Validate(); err != nil {
		return Config{}, err
	}

	return cfg, nil
}

// Save atomically writes the configuration to the specified env file.
func (c Config) Save(path string) error {
	if err := c.Validate(); err != nil {
		return err
	}

	values := map[string]string{
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
		"TEMPORARY_CHAT":           strconv.FormatBool(c.TemporaryChat),
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

// Validate checks if the configuration values are valid for service startup.
func (c Config) Validate() error {
	if strings.TrimSpace(c.AuthStates) == "" {
		return fmt.Errorf("AISTUDIO_AUTH_STATES cannot be empty")
	}

	if err := validateListenAddr(c.ListenAddr); err != nil {
		return err
	}

	if err := ValidateProxy(c.Proxy); err != nil {
		return err
	}

	if c.InitTimeout <= 0 {
		return fmt.Errorf("INIT_TIMEOUT must be a positive duration")
	}

	if c.RequestTimeout <= 0 {
		return fmt.Errorf("REQUEST_TIMEOUT must be a positive duration")
	}

	if c.WarmWorkerLimit <= 0 {
		return fmt.Errorf("WARM_WORKER_LIMIT must be a positive integer")
	}

	if c.MaxActiveWorkers < c.WarmWorkerLimit {
		return fmt.Errorf("MAX_ACTIVE_WORKERS must be greater than or equal to WARM_WORKER_LIMIT")
	}

	if c.WarmStartupConcurrency <= 0 || c.WarmStartupConcurrency > c.WarmWorkerLimit {
		return fmt.Errorf("WARM_STARTUP_CONCURRENCY must be between 1 and WARM_WORKER_LIMIT")
	}

	if c.PerAccountConcurrency <= 0 {
		return fmt.Errorf("PER_ACCOUNT_CONCURRENCY must be a positive integer")
	}

	if c.RoutingStrategy != "round-robin" && c.RoutingStrategy != "fill-first" {
		return fmt.Errorf("ROUTING_STRATEGY must be round-robin or fill-first")
	}

	return nil
}

// MarshalJSON outputs durations in string format matching env conventions.
func (c Config) MarshalJSON() ([]byte, error) {
	type payload struct {
		AuthStates             string `json:"auth_states"`
		ListenAddr             string `json:"listen_addr"`
		ProxyAPIKey            string `json:"proxy_api_key"`
		Proxy                  string `json:"proxy"`
		InitTimeout            string `json:"init_timeout"`
		RequestTimeout         string `json:"request_timeout"`
		WarmWorkerLimit        int    `json:"warm_worker_limit"`
		MaxActiveWorkers       int    `json:"max_active_workers"`
		WarmStartupConcurrency int    `json:"warm_startup_concurrency"`
		PerAccountConcurrency  int    `json:"per_account_concurrency"`
		RoutingStrategy        string `json:"routing_strategy"`
		TemporaryChat          bool   `json:"temporary_chat"`
	}

	return json.Marshal(payload{
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
		TemporaryChat:          c.TemporaryChat,
	})
}

// UnmarshalJSON parses configuration from textual durations used in admin endpoints.
func (c *Config) UnmarshalJSON(data []byte) error {
	type payload struct {
		AuthStates             string `json:"auth_states"`
		ListenAddr             string `json:"listen_addr"`
		ProxyAPIKey            string `json:"proxy_api_key"`
		Proxy                  string `json:"proxy"`
		InitTimeout            string `json:"init_timeout"`
		RequestTimeout         string `json:"request_timeout"`
		WarmWorkerLimit        int    `json:"warm_worker_limit"`
		MaxActiveWorkers       int    `json:"max_active_workers"`
		WarmStartupConcurrency int    `json:"warm_startup_concurrency"`
		PerAccountConcurrency  int    `json:"per_account_concurrency"`
		RoutingStrategy        string `json:"routing_strategy"`
		TemporaryChat          bool   `json:"temporary_chat"`
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
		TemporaryChat:          value.TemporaryChat,
	}

	if err := parsed.Validate(); err != nil {
		return err
	}

	*c = parsed
	return nil
}

// ValidateProxy validates account or global proxy URLs.
func ValidateProxy(value string) error {
	value = strings.TrimSpace(value)
	if value == "" {
		return nil
	}

	parsed, err := url.Parse(value)
	if err != nil || parsed.Hostname() == "" {
		return fmt.Errorf("PROXY must be a valid http, https, or socks5 URL")
	}

	switch parsed.Scheme {
	case "http", "https", "socks5":
	default:
		return fmt.Errorf("PROXY must be a valid http, https, or socks5 URL")
	}

	if parsed.User != nil {
		return fmt.Errorf("PROXY cannot contain user authentication")
	}

	if parsed.Path != "" && parsed.Path != "/" || parsed.RawQuery != "" || parsed.Fragment != "" {
		return fmt.Errorf("PROXY cannot contain path, query parameters, or fragments")
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
		return nil, fmt.Errorf("read config file: %w", err)
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
			return nil, fmt.Errorf("%s:%d missing '=' separator", path, lineNumber)
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
		return nil, fmt.Errorf("read config file: %w", err)
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
			return "", fmt.Errorf("unclosed single quote")
		}
		return value[1 : len(value)-1], nil
	}

	if value[0] == '"' {
		parsed, err := strconv.Unquote(value)
		if err != nil {
			return "", fmt.Errorf("invalid double-quoted string")
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
		return 0, fmt.Errorf("%s must be a positive duration, e.g., 30s or 5m", key)
	}

	return duration, nil
}

func parsePositiveInt(key string, value string) (int, error) {
	parsed, err := strconv.Atoi(strings.TrimSpace(value))
	if err != nil || parsed <= 0 {
		return 0, fmt.Errorf("%s must be a positive integer", key)
	}

	return parsed, nil
}

func validateListenAddr(value string) error {
	host, port, err := net.SplitHostPort(strings.TrimSpace(value))
	if err != nil || port == "" {
		return fmt.Errorf("LISTEN_ADDR must be host:port")
	}

	if host == "" {
		host = "0.0.0.0"
	}

	if parsed, err := strconv.ParseUint(port, 10, 16); err != nil || parsed == 0 {
		return fmt.Errorf("LISTEN_ADDR port must be between 1 and 65535")
	}

	return nil
}

func atomicWrite(path string, data []byte, mode os.FileMode) error {
	target, err := filepath.Abs(path)
	if err != nil {
		return fmt.Errorf("resolve config path: %w", err)
	}

	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		return fmt.Errorf("create config directory: %w", err)
	}

	temporary, err := os.CreateTemp(filepath.Dir(target), ".env-*.tmp")
	if err != nil {
		return fmt.Errorf("create temp config file: %w", err)
	}
	temporaryPath := temporary.Name()
	defer os.Remove(temporaryPath)

	if err := temporary.Chmod(mode); err != nil {
		temporary.Close()
		return fmt.Errorf("chmod config file: %w", err)
	}

	if _, err := bytes.NewReader(data).WriteTo(temporary); err != nil {
		temporary.Close()
		return fmt.Errorf("write config file: %w", err)
	}

	if err := temporary.Sync(); err != nil {
		temporary.Close()
		return fmt.Errorf("sync config file: %w", err)
	}

	if err := temporary.Close(); err != nil {
		return fmt.Errorf("close config file: %w", err)
	}

	if err := os.Rename(temporaryPath, target); err != nil {
		return fmt.Errorf("replace config file: %w", err)
	}

	return nil
}
