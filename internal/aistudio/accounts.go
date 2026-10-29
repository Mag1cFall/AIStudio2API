package aistudio

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/mail"
	"os"
	"os/user"
	"path/filepath"
	"reflect"
	"runtime"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/Mag1cFall/AIStudio2API/internal/camoufoxnative"
	appconfig "github.com/Mag1cFall/AIStudio2API/internal/config"
	"github.com/gofrs/flock"
	"golang.org/x/sync/singleflight"
)

const (
	accountConfigName = "account.json"
	storageStateName  = "storage-state.json"
	runtimeStateName  = "runtime-state.json"
	catalogCacheName  = "catalog.json"
	globalCooldownKey = "*"
	externalLeasePoll = 100 * time.Millisecond
	runtimeLockPoll   = 25 * time.Millisecond
	runtimeLockLimit  = 2 * time.Second
)

// AccountState 表示账户当前是否可调度
type AccountState string

const (
	// AccountReady 表示账户可以接收请求
	AccountReady AccountState = "ready"
	// AccountBusy 表示账户存在活动请求
	AccountBusy AccountState = "busy"
	// AccountCooldown 表示账户或模型处于冷却期
	AccountCooldown AccountState = "cooldown"
	// AccountAuthRequired 表示账户需要重新登录
	AccountAuthRequired AccountState = "auth_required"
	// AccountUnavailable 表示账户初始化或运行失败
	AccountUnavailable AccountState = "unavailable"
	// AccountDisabled 表示账户已停用
	AccountDisabled AccountState = "disabled"
)

var (
	// ErrInvalidArgument 表示请求参数在发送前已确定无效
	ErrInvalidArgument = errors.New("AI Studio 请求参数无效")
	// ErrModelNotFound 表示实时目录中不存在请求模型
	ErrModelNotFound = errors.New("AI Studio 实时目录中没有请求模型")
	// ErrNoEligibleAccount 表示没有账户具备请求所需能力
	ErrNoEligibleAccount = errors.New("没有符合条件的 AI Studio 账户")
	// errNoAccounts 表示账户池中没有任何账户
	errNoAccounts = fmt.Errorf("%w：在管理页面账户页添加账户", ErrNoEligibleAccount)
	// ErrAccountNotFound 表示稳定账户 ID 不存在
	ErrAccountNotFound = errors.New("账户不存在")
	// ErrAccountLeased 表示账户当前存在进程内或跨进程租约
	ErrAccountLeased = errors.New("账户正在使用")
	// ErrAccountCoolingDown 表示租约账户在请求发出前进入冷却
	ErrAccountCoolingDown = errors.New("账户已进入冷却")
	// ErrResourceNotFound 表示资源没有创建账户映射
	ErrResourceNotFound = errors.New("资源账户映射不存在")
)

// AllCoolingError 表示请求的全部候选账户都在冷却且不会很快恢复
type AllCoolingError struct {
	ModelID string
	Until   time.Time
	Reason  string
}

func (e *AllCoolingError) Error() string {
	message := fmt.Sprintf("模型 %s 的可用账户均在冷却，最早恢复时间 %s", e.ModelID, e.Until.Local().Format(time.RFC3339))
	if e.Reason == "" {
		return message
	}
	return message + "；" + e.Reason
}

// RetryAt 返回最早恢复的时刻
func (e *AllCoolingError) RetryAt(time.Time) time.Time {
	return e.Until
}

// HTTPStatus 返回额度耗尽对应的公开状态码
func (e *AllCoolingError) HTTPStatus() int {
	return http.StatusTooManyRequests
}

// ErrorCode 返回 OpenAI 兼容的额度错误代码
func (e *AllCoolingError) ErrorCode() string {
	return "rate_limit_exceeded"
}

// AccountsNotReadyError 表示支持请求的账户都处于不可调度状态
type AccountsNotReadyError struct {
	Reasons []string
}

func (e *AccountsNotReadyError) Error() string {
	return ErrNoEligibleAccount.Error() + "：" + strings.Join(e.Reasons, "；")
}

func (e *AccountsNotReadyError) Unwrap() error {
	return ErrNoEligibleAccount
}

// accountStateLabels 为不可调度账户状态的中文说明
var accountStateLabels = map[AccountState]string{
	AccountAuthRequired: "需要重新登录",
	AccountUnavailable:  "不可用",
	AccountDisabled:     "已停用",
}

// AccountConfig 表示账户目录中的固定最小配置
type AccountConfig struct {
	Label    string `json:"label"`
	Enabled  bool   `json:"enabled"`
	Proxy    string `json:"proxy"`
	Locale   string `json:"locale"`
	Timezone string `json:"timezone"`
	AuthUser int    `json:"auth_user,omitempty"`
}

// ResourceBinding 记录上游资源的创建账户
type ResourceBinding struct {
	Kind      string                 `json:"kind,omitempty"`
	Name      string                 `json:"name,omitempty"`
	MIME      string                 `json:"mime,omitempty"`
	Size      int64                  `json:"size,omitempty"`
	Purpose   string                 `json:"purpose,omitempty"`
	CreatedAt time.Time              `json:"created_at"`
	Video     *VideoResourceMetadata `json:"video,omitempty"`
	// Alias 是客户端上传时指定的公开文件名
	Alias string `json:"alias,omitempty"`
	// SHA256 是文件内容的 SHA-256 摘要，Base64 编码
	SHA256 string `json:"sha256,omitempty"`
	// VideoDuration 是视频文件的时长
	VideoDuration time.Duration `json:"video_duration,omitempty"`
	// UsedAt 是内联附件最近一次被请求引用的时间
	UsedAt time.Time `json:"used_at,omitzero"`
	// DisplayName 是内联附件在请求中给出的文件名
	DisplayName string `json:"display_name,omitempty"`
}

// VideoResourceMetadata 保存 OpenAI 视频对象的持久字段与可下载的结果文件
type VideoResourceMetadata struct {
	Model   string `json:"model"`
	Seconds string `json:"seconds"`
	Size    string `json:"size"`
	File    string `json:"file,omitempty"`
}

type accountRuntimeState struct {
	Cooldowns          map[string]CooldownState   `json:"cooldowns,omitempty"`
	Resources          map[string]ResourceBinding `json:"resources,omitempty"`
	ModelAccess        map[string]ModelAccess     `json:"model_access,omitempty"`
	BenefitTier        BenefitTier                `json:"benefit_tier,omitempty"`
	CatalogFingerprint string                     `json:"catalog_fingerprint,omitempty"`
	Proxy              string                     `json:"proxy,omitempty"`
}

// ModelAccessState 表示账户对单个模型的实测调用资格
type ModelAccessState string

const (
	// ModelAccessVerified 表示账户已成功调用模型
	ModelAccessVerified ModelAccessState = "verified"
)

// ModelAccess 保存账户模型资格的实测结果
type ModelAccess struct {
	State     ModelAccessState `json:"state"`
	CheckedAt time.Time        `json:"checked_at"`
	Reason    string           `json:"reason,omitempty"`
}

// Account 表示一个稳定目录对应的 AI Studio 账户
type Account struct {
	ID                    string        `json:"id"`
	Directory             string        `json:"-"`
	ConfigPath            string        `json:"-"`
	StoragePath           string        `json:"-"`
	RuntimePath           string        `json:"-"`
	Config                AccountConfig `json:"config"`
	StorageState          StorageState  `json:"-"`
	Models                []Model       `json:"models,omitempty"`
	BenefitTier           BenefitTier   `json:"benefit_tier"`
	State                 AccountState  `json:"state"`
	LastUsed              time.Time     `json:"last_used,omitempty"`
	runtime               accountRuntimeState
	runtimeData           []byte
	active                int
	exclusive             bool
	exclusiveWaiters      int
	authRefreshers        int
	leaseLock             *flock.Flock
	storageMu             sync.Mutex
	runtimeMu             sync.Mutex
	persistenceLocked     bool
	authGeneration        uint64
	authCheckedAt         time.Time
	modelAccessGeneration uint64
	stateMessage          string
	catalogErr            error
	initializedAt         time.Time
	buildModels           []Model
	cachedCatalog         modelCatalog
	boundProxy            atomic.Pointer[string]
}

// AccountStatus 表示管理界面使用的脱敏账户状态
type AccountStatus struct {
	ID          string                   `json:"id"`
	Label       string                   `json:"label"`
	State       AccountState             `json:"state"`
	Enabled     bool                     `json:"enabled"`
	Proxy       string                   `json:"proxy"`
	Egress      string                   `json:"egress"`
	Locale      string                   `json:"locale"`
	Timezone    string                   `json:"timezone"`
	Models      []string                 `json:"models"`
	BenefitTier string                   `json:"benefit_tier"`
	Cooldowns   map[string]CooldownState `json:"-"`
	LastUsed    *time.Time               `json:"last_used,omitempty"`
	Message     string                   `json:"message,omitempty"`
}

// AccountSelection 描述账户调度所需的能力或粘性条件
type AccountSelection struct {
	ModelID           string
	ModelAccessScope  string
	Method            string
	Capability        string
	AccountID         string
	ResourceID        string
	AllowedAccountIDs []string
	PlaygroundOnly    bool
	Channel           Channel
	PreferredChannel  Channel
	anyTier           bool
	anyState          bool
}

const preferredBootstrapModelID = "gemini-flash-latest"

// ModelAccessKey 返回关联真实目录模型的独立资格键
func ModelAccessKey(scope string, modelID string) string {
	scope = strings.TrimSpace(scope)
	modelID = strings.TrimPrefix(strings.TrimSpace(modelID), "models/")
	if scope == "" || modelID == "" {
		return modelID
	}
	return scope + ":" + modelID
}

// AccountCandidateGroups 表示 warm 与 standby 账户的实时可调度状态
type AccountCandidateGroups struct {
	WarmReady        []string
	WarmAvailable    []string
	WarmBusy         []string
	StandbyReady     []string
	StandbyBusy      []string
	EarliestCooldown CooldownState
	Eligible         bool
}

// AccountCandidateState 表示账户候选的模型资格状态
type AccountCandidateState struct {
	ModelAccess ModelAccessState
}

// AccountStore 从一个或多个账户文件或目录加载账户
type AccountStore struct {
	paths []string
}

// AccountPool 在账户之间执行能力与并发槽位调度
type AccountPool struct {
	mu                    sync.Mutex
	accounts              []*Account
	byID                  map[string]*Account
	resources             map[string]string
	perAccountConcurrency int
	routingStrategy       string
	lastPicked            map[string]string
	lastPickedChannel     map[string]Channel
	channels              []Channel
	modelChannels         map[string]Channel
	changed               chan struct{}
	// sessionCheck 以官网页面的方式打开 AI Studio 检查账户会话，为空时认证失败直接标记需要重新登录
	sessionCheck func(context.Context, *Account) error
	// sessions 合并同一账户并发的会话检查
	sessions singleflight.Group
	// fileBindMu 串行化文件绑定，使公开文件名的重名检查与绑定成为一步
	fileBindMu sync.Mutex
}

// AccountLease 表示一个账户请求槽位
type AccountLease struct {
	pool                  *AccountPool
	account               *Account
	exclusive             bool
	authGeneration        uint64
	modelAccessGeneration uint64
	checkedAt             time.Time
	refreshRuntime        bool
	channel               Channel
	operation             sync.Mutex
	released              bool
	once                  sync.Once
	err                   error
	// sessionOnce 使同一租约内的认证失败只检查一次会话
	sessionOnce sync.Once
	// sessionErr 是本租约会话检查的结果
	sessionErr error
}

// AccountRuntimeLease 保证同一邮箱只有一个 WAA runtime
type AccountRuntimeLease struct {
	lock *flock.Flock
	once sync.Once
	err  error
}

// AccountPublishLease 保护新账户从稳定目录发布到运行时
type AccountPublishLease struct {
	account     *Account
	requestLock *flock.Flock
	runtimeLock *flock.Flock
	once        sync.Once
	err         error
}

// DefaultAccountConfig 返回新账户的最小配置
func DefaultAccountConfig(label string) AccountConfig {
	return AccountConfig{
		Label:    strings.TrimSpace(label),
		Enabled:  true,
		Locale:   DefaultAccountLocale(),
		Timezone: DefaultAccountTimezone(),
	}
}

// NewAccountStore 创建账户目录存储
func NewAccountStore(paths ...string) *AccountStore {
	cleaned := make([]string, 0, len(paths))
	for _, value := range paths {
		value = strings.TrimSpace(value)
		if value != "" {
			cleaned = append(cleaned, value)
		}
	}
	return &AccountStore{paths: cleaned}
}

// Load 扫描账户目录并恢复冷却与资源粘性
func (s *AccountStore) Load() ([]*Account, error) {
	if len(s.paths) == 0 {
		return nil, fmt.Errorf("账户路径为空")
	}
	directories := make([]string, 0)
	for _, source := range s.paths {
		absolute, err := filepath.Abs(source)
		if err != nil {
			return nil, fmt.Errorf("解析账户路径 %q: %w", source, err)
		}
		info, err := os.Stat(absolute)
		if os.IsNotExist(err) {
			continue
		}
		if err != nil {
			return nil, fmt.Errorf("读取账户路径 %q: %w", source, err)
		}
		if !info.IsDir() {
			if filepath.Base(absolute) != storageStateName {
				return nil, fmt.Errorf("账户文件必须命名为 %s", storageStateName)
			}
			directories = append(directories, filepath.Dir(absolute))
			continue
		}
		if fileExists(filepath.Join(absolute, storageStateName)) || fileExists(filepath.Join(absolute, accountConfigName)) {
			directories = append(directories, absolute)
			continue
		}
		entries, err := os.ReadDir(absolute)
		if err != nil {
			return nil, fmt.Errorf("扫描账户目录 %q: %w", source, err)
		}
		for _, entry := range entries {
			if !entry.IsDir() {
				continue
			}
			if strings.HasPrefix(entry.Name(), ".account-") && strings.HasSuffix(entry.Name(), ".tmp") {
				continue
			}
			directory := filepath.Join(absolute, entry.Name())
			if fileExists(filepath.Join(directory, storageStateName)) || fileExists(filepath.Join(directory, accountConfigName)) {
				directories = append(directories, directory)
			}
		}
	}
	sort.Strings(directories)

	accounts := make([]*Account, 0, len(directories))
	ids := make(map[string]struct{}, len(directories))
	resources := make(map[string]string)
	for _, directory := range directories {
		account, err := loadAccount(directory)
		if err != nil {
			return nil, err
		}
		if _, exists := ids[account.ID]; exists {
			return nil, fmt.Errorf("账户 ID 重复: %s", account.ID)
		}
		ids[account.ID] = struct{}{}
		for resourceID := range account.runtime.Resources {
			if owner, exists := resources[resourceID]; exists {
				return nil, fmt.Errorf("资源 %s 同时绑定账户 %s 和 %s", resourceID, owner, account.ID)
			}
			resources[resourceID] = account.ID
		}
		accounts = append(accounts, account)
	}
	return accounts, nil
}

// Create 创建并锁定以认证邮箱命名的账户目录
func (s *AccountStore) Create(accountConfig AccountConfig, state StorageState) (*Account, *AccountPublishLease, error) {
	if len(s.paths) != 1 {
		return nil, nil, fmt.Errorf("创建账户需要一个账户根目录")
	}
	if err := accountConfig.Validate(); err != nil {
		return nil, nil, err
	}
	if err := state.Validate(); err != nil {
		return nil, nil, err
	}
	root, err := filepath.Abs(s.paths[0])
	if err != nil {
		return nil, nil, fmt.Errorf("解析账户根目录: %w", err)
	}
	if err := os.MkdirAll(root, 0o755); err != nil {
		return nil, nil, fmt.Errorf("创建账户根目录: %w", err)
	}
	id, err := accountEmailID(accountConfig, state)
	if err != nil {
		return nil, nil, err
	}
	accountConfig.Label = id
	temporary, err := os.MkdirTemp(root, ".account-*.tmp")
	if err != nil {
		return nil, nil, fmt.Errorf("创建临时账户目录: %w", err)
	}
	defer os.RemoveAll(temporary)
	if err := writeAccountConfig(filepath.Join(temporary, accountConfigName), accountConfig); err != nil {
		return nil, nil, err
	}
	if err := WriteStorageState(filepath.Join(temporary, storageStateName), state); err != nil {
		return nil, nil, err
	}
	directory := filepath.Join(root, id)
	account, err := loadAccount(temporary)
	if err != nil {
		return nil, nil, err
	}
	account.ID = id
	account.Directory = directory
	account.ConfigPath = filepath.Join(directory, accountConfigName)
	account.StoragePath = filepath.Join(directory, storageStateName)
	account.RuntimePath = filepath.Join(directory, runtimeStateName)
	publishLease, err := acquireAccountPublishLease(account, false)
	if err != nil {
		return nil, nil, err
	}
	if err := os.Rename(temporary, directory); err != nil {
		return nil, nil, errors.Join(fmt.Errorf("保存账户目录: %w", err), publishLease.Release())
	}
	if err := validatePersistentAccountFiles(account); err != nil {
		return nil, nil, errors.Join(err, os.RemoveAll(directory), publishLease.Release())
	}
	return account, publishLease, nil
}

// Delete 删除账户的稳定目录
func (s *AccountStore) Delete(account *Account) error {
	directory := account.Directory
	if _, err := os.Stat(directory); err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return fmt.Errorf("读取账户目录: %w", err)
	}
	account.storageMu.Lock()
	lockedByPool := account.persistenceLocked
	account.storageMu.Unlock()
	if lockedByPool {
		if err := os.RemoveAll(directory); err != nil {
			return fmt.Errorf("删除账户目录: %w", err)
		}
		return nil
	}
	leaseLock, err := acquireAccountFileLease(account.StoragePath)
	if errors.Is(err, ErrAccountLeased) {
		return fmt.Errorf("%w: %s", ErrAccountLeased, account.ID)
	}
	if err != nil {
		return err
	}
	account.runtimeMu.Lock()
	runtimeLock, runtimeErr := lockRuntimeState(context.Background(), account)
	if runtimeErr != nil {
		account.runtimeMu.Unlock()
		if leaseLock != nil {
			_ = leaseLock.Unlock()
		}
		return runtimeErr
	}
	deleteErr := os.RemoveAll(directory)
	if runtimeLock != nil {
		deleteErr = errors.Join(deleteErr, runtimeLock.Unlock())
	}
	account.runtimeMu.Unlock()
	if leaseLock != nil {
		deleteErr = errors.Join(deleteErr, leaseLock.Unlock())
	}
	if deleteErr != nil {
		return fmt.Errorf("删除账户目录: %w", deleteErr)
	}
	return nil
}

// Validate 校验账户固定配置
func (c AccountConfig) Validate() error {
	if _, err := normalizeAccountEmail(c.Label); err != nil {
		return err
	}
	if strings.TrimSpace(c.Locale) == "" {
		return fmt.Errorf("账户 locale 不能为空")
	}
	if strings.TrimSpace(c.Timezone) == "" {
		return fmt.Errorf("账户 timezone 不能为空")
	}
	if c.AuthUser < 0 || c.AuthUser > 9 {
		return fmt.Errorf("账户 auth_user 无效: %d", c.AuthUser)
	}
	if err := appconfig.ValidateProxy(c.Proxy); err != nil {
		return fmt.Errorf("账户 proxy 无效: %w", err)
	}
	return nil
}

// EffectiveProxy 返回账户固定代理、代理池绑定或全局代理
func (a *Account) EffectiveProxy(globalProxy string) string {
	if strings.TrimSpace(a.Config.Proxy) != "" {
		return strings.TrimSpace(a.Config.Proxy)
	}
	return a.PoolProxy(globalProxy)
}

// SetPoolProxy 设置账户加入账户池前使用的代理池绑定
func (a *Account) SetPoolProxy(proxy string) {
	if proxy = strings.TrimSpace(proxy); proxy != "" {
		a.boundProxy.Store(&proxy)
	}
}

// PoolProxy 返回账户的代理池绑定，未绑定时返回全局代理
func (a *Account) PoolProxy(globalProxy string) string {
	if bound := a.boundProxy.Load(); bound != nil {
		return *bound
	}
	return strings.TrimSpace(globalProxy)
}

// AcceptLanguage 返回账户 locale 对应的请求语言头
func (a *Account) AcceptLanguage() string {
	locale := strings.TrimSpace(a.Config.Locale)
	language, _, _ := strings.Cut(locale, "-")
	if language == "" || strings.EqualFold(language, locale) {
		return locale
	}
	return locale + "," + strings.ToLower(language) + ";q=0.9"
}

// SupportsModel 判断账户实时目录是否包含模型
func (a *Account) SupportsModel(modelID string) bool {
	modelID = strings.TrimPrefix(strings.TrimSpace(modelID), "models/")
	if modelID == "" {
		return true
	}
	for _, model := range a.Models {
		if modelMatchesID(model, modelID) && modelAllowedByTier(model, a.BenefitTier) {
			return true
		}
	}
	return false
}

// SupportsMethod 判断账户模型是否声明目标方法
func (a *Account) SupportsMethod(modelID string, method string) bool {
	if strings.TrimSpace(method) == "" {
		return a.SupportsModel(modelID)
	}
	modelID = strings.TrimPrefix(strings.TrimSpace(modelID), "models/")
	for _, model := range a.Models {
		if !modelMatchesID(model, modelID) {
			continue
		}
		if !modelAllowedByTier(model, a.BenefitTier) {
			return false
		}
		for _, candidate := range model.Methods {
			if candidate == method {
				return true
			}
		}
	}
	return false
}

func accountSupportsSelection(account *Account, selection AccountSelection) bool {
	modelID := strings.TrimPrefix(strings.TrimSpace(selection.ModelID), "models/")
	if modelID == "" {
		return true
	}
	for _, model := range account.Models {
		if !modelMatchesID(model, modelID) {
			continue
		}
		if !selection.anyTier && !modelAllowedByTier(model, account.BenefitTier) {
			return false
		}
		if strings.TrimSpace(selection.Method) != "" && !hasMethod(model, selection.Method) {
			return false
		}
		return strings.TrimSpace(selection.Capability) == "" || model.Capabilities[selection.Capability]
	}
	return false
}

// CatalogModels 返回完整目录，每项带有启用账户可以调用的通道，按 callableFirst 排列
func (p *AccountPool) CatalogModels(models []Model) []Model {
	p.mu.Lock()
	defer p.mu.Unlock()
	catalog := make([]Model, len(models))
	for index, model := range models {
		model.Channels = p.modelChannelsLocked(model, false)
		catalog[index] = model
	}
	return callableFirst(catalog)
}

// PublicModels 返回已启用通道可以提供的目录项，只因账户权益不足而无法调用的模型同样列出且通道为空，按 callableFirst 排列
func (p *AccountPool) PublicModels(models []Model) []Model {
	p.mu.Lock()
	defer p.mu.Unlock()
	public := make([]Model, 0, len(models))
	for _, model := range models {
		model.Channels = p.modelChannelsLocked(model, false)
		if len(model.Channels) == 0 && len(p.modelChannelsLocked(model, true)) == 0 {
			continue
		}
		public = append(public, model)
	}
	return callableFirst(public)
}

// callableFirst 保持原顺序依次排列有通道的普通模型、有通道的 Interactions 代理模型与没有通道的模型
func callableFirst(models []Model) []Model {
	ordered := make([]Model, 0, len(models))
	var agents, restricted []Model
	for _, model := range models {
		switch {
		case len(model.Channels) == 0:
			restricted = append(restricted, model)
		case model.Capabilities["interaction_route"]:
			agents = append(agents, model)
		default:
			ordered = append(ordered, model)
		}
	}
	return append(append(ordered, agents...), restricted...)
}

// CanonicalModelID 把实时目录中的模型别名换成上游接受的正式模型 ID
func (p *AccountPool) CanonicalModelID(modelID string) string {
	trimmed := strings.TrimPrefix(strings.TrimSpace(modelID), "models/")
	if trimmed == "" {
		return modelID
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	canonical := ""
	for _, account := range p.accounts {
		for _, model := range account.Models {
			if model.ID == trimmed {
				return modelID
			}
			if canonical == "" && modelMatchesID(model, trimmed) {
				canonical = model.ID
			}
		}
	}
	if canonical == "" {
		return modelID
	}
	return canonical
}

func modelMatchesID(model Model, modelID string) bool {
	if model.ID == modelID {
		return true
	}
	for _, alias := range model.CapabilityOptions["aliases"] {
		if alias == modelID {
			return true
		}
	}
	return false
}

// NewAccountPool 创建账户独占调度池
func NewAccountPool(accounts []*Account, perAccountConcurrency int) *AccountPool {
	p := &AccountPool{
		accounts: append([]*Account(nil), accounts...), byID: make(map[string]*Account, len(accounts)),
		resources: make(map[string]string), perAccountConcurrency: perAccountConcurrency, changed: make(chan struct{}),
		routingStrategy: "round-robin", lastPicked: make(map[string]string), lastPickedChannel: make(map[string]Channel),
	}
	for _, account := range p.accounts {
		if account.runtime.Cooldowns == nil {
			account.runtime.Cooldowns = make(map[string]CooldownState)
		}
		if account.runtime.Resources == nil {
			account.runtime.Resources = make(map[string]ResourceBinding)
		}
		if account.runtime.ModelAccess == nil {
			account.runtime.ModelAccess = make(map[string]ModelAccess)
		}
		p.byID[account.ID] = account
		for resourceID := range account.runtime.Resources {
			p.resources[resourceID] = account.ID
		}
	}
	return p
}

// Account 返回稳定 ID 对应的账户
func (p *AccountPool) Account(accountID string) (*Account, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	account := p.byID[strings.TrimSpace(accountID)]
	if account == nil {
		return nil, fmt.Errorf("%w: %s", ErrAccountNotFound, accountID)
	}
	return account, nil
}

// Add 将新账户加入当前调度池
func (p *AccountPool) Add(account *Account) (resultErr error) {
	if account.ConfigPath != "" {
		account.storageMu.Lock()
		lockedExternally := account.persistenceLocked
		account.storageMu.Unlock()
		if !lockedExternally {
			leaseLock, err := acquireAccountFileLease(account.StoragePath)
			if errors.Is(err, ErrAccountLeased) {
				return fmt.Errorf("%w: %s", ErrAccountLeased, account.ID)
			}
			if err != nil {
				return err
			}
			defer func() {
				if leaseLock != nil {
					resultErr = errors.Join(resultErr, leaseLock.Unlock())
				}
			}()
			account.runtimeMu.Lock()
			defer account.runtimeMu.Unlock()
			runtimeLock, err := lockRuntimeState(context.Background(), account)
			if err != nil {
				return err
			}
			defer func() {
				if runtimeLock != nil {
					resultErr = errors.Join(resultErr, runtimeLock.Unlock())
				}
			}()
		}
		if err := validatePersistentAccountFiles(account); err != nil {
			return err
		}
		runtimeState, err := readRuntime(account.RuntimePath)
		if err != nil {
			return err
		}
		account.runtime = runtimeState
		account.BenefitTier = runtimeState.BenefitTier
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if _, exists := p.byID[account.ID]; exists {
		return fmt.Errorf("账户已存在: %s", account.ID)
	}
	if account.runtime.Cooldowns == nil {
		account.runtime.Cooldowns = make(map[string]CooldownState)
	}
	if account.runtime.Resources == nil {
		account.runtime.Resources = make(map[string]ResourceBinding)
	}
	if account.runtime.ModelAccess == nil {
		account.runtime.ModelAccess = make(map[string]ModelAccess)
	}
	for resourceID := range account.runtime.Resources {
		if owner, exists := p.resources[resourceID]; exists {
			return fmt.Errorf("资源 %s 已绑定账户 %s", resourceID, owner)
		}
	}
	p.accounts = append(p.accounts, account)
	p.byID[account.ID] = account
	for resourceID := range account.runtime.Resources {
		p.resources[resourceID] = account.ID
	}
	p.notifyLocked()
	return nil
}

// Remove 在账户空闲时删除持久目录并移出调度池
func (p *AccountPool) Remove(accountID string, deleteDirectory func(*Account) error) (*Account, error) {
	p.mu.Lock()
	accountID = strings.TrimSpace(accountID)
	account := p.byID[accountID]
	if account == nil {
		p.mu.Unlock()
		return nil, fmt.Errorf("%w: %s", ErrAccountNotFound, accountID)
	}
	if account.exclusive || account.exclusiveWaiters > 0 || account.active > 0 {
		p.mu.Unlock()
		return nil, fmt.Errorf("%w: %s", ErrAccountLeased, accountID)
	}
	account.exclusive = true
	p.notifyLocked()
	p.mu.Unlock()
	leaseLock, err := acquireAccountFileLease(account.StoragePath)
	if err != nil {
		p.mu.Lock()
		account.exclusive = false
		p.notifyLocked()
		p.mu.Unlock()
		if errors.Is(err, ErrAccountLeased) {
			return nil, fmt.Errorf("%w: %s", ErrAccountLeased, accountID)
		}
		return nil, err
	}
	account.runtimeMu.Lock()
	runtimeLock, err := lockRuntimeState(context.Background(), account)
	if err != nil {
		account.runtimeMu.Unlock()
		if leaseLock != nil {
			_ = leaseLock.Unlock()
		}
		p.mu.Lock()
		account.exclusive = false
		p.notifyLocked()
		p.mu.Unlock()
		return nil, err
	}
	account.storageMu.Lock()
	account.persistenceLocked = true
	account.storageMu.Unlock()
	deleteErr := deleteDirectory(account)
	account.storageMu.Lock()
	account.persistenceLocked = false
	account.storageMu.Unlock()
	var releaseErr error
	if runtimeLock != nil {
		releaseErr = runtimeLock.Unlock()
	}
	account.runtimeMu.Unlock()
	if leaseLock != nil {
		releaseErr = errors.Join(releaseErr, leaseLock.Unlock())
	}
	if deleteErr != nil {
		p.mu.Lock()
		account.exclusive = false
		p.notifyLocked()
		p.mu.Unlock()
		return nil, errors.Join(deleteErr, releaseErr)
	}
	p.mu.Lock()
	for resourceID, owner := range p.resources {
		if owner == accountID {
			delete(p.resources, resourceID)
		}
	}
	delete(p.byID, accountID)
	for index, candidate := range p.accounts {
		if candidate.ID == accountID {
			p.accounts = append(p.accounts[:index], p.accounts[index+1:]...)
			break
		}
	}
	p.notifyLocked()
	p.mu.Unlock()
	return account, releaseErr
}

// AcquireAccount 为管理操作获取不受调度状态限制的指定账户租约
func (p *AccountPool) AcquireAccount(ctx context.Context, accountID string) (*AccountLease, error) {
	accountID = strings.TrimSpace(accountID)
	p.mu.Lock()
	waitingAccount := p.byID[accountID]
	if waitingAccount == nil {
		p.mu.Unlock()
		return nil, fmt.Errorf("%w: %s", ErrAccountNotFound, accountID)
	}
	waitingAccount.exclusiveWaiters++
	p.notifyLocked()
	p.mu.Unlock()
	defer func() {
		p.mu.Lock()
		waitingAccount.exclusiveWaiters--
		p.notifyLocked()
		p.mu.Unlock()
	}()
	for {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		p.mu.Lock()
		account := p.byID[accountID]
		if account == nil {
			p.mu.Unlock()
			return nil, fmt.Errorf("%w: %s", ErrAccountNotFound, accountID)
		}
		if !account.exclusive && account.authRefreshers == 0 && account.active == 0 {
			checkedAt := time.Now().UTC()
			leaseLock, err := acquireAccountFileLease(account.StoragePath)
			if err == nil {
				account.exclusive = true
				account.leaseLock = leaseLock
				p.mu.Unlock()
				lease := &AccountLease{
					pool: p, account: account, exclusive: true, authGeneration: account.authGeneration,
					modelAccessGeneration: account.modelAccessGeneration, checkedAt: checkedAt,
				}
				if err := p.refreshAccountRuntime(ctx, account); err != nil {
					releaseErr := lease.Release()
					if errors.Is(err, ErrAccountNotFound) {
						p.markStaleAccountUnavailable(account)
					}
					return nil, errors.Join(err, releaseErr)
				}
				p.mu.Lock()
				lease.modelAccessGeneration = account.modelAccessGeneration
				p.mu.Unlock()
				return lease, nil
			}
			if !errors.Is(err, ErrAccountLeased) {
				p.mu.Unlock()
				return nil, err
			}
		}
		changed := p.changed
		p.mu.Unlock()

		timer := time.NewTimer(externalLeasePoll)
		select {
		case <-ctx.Done():
			timer.Stop()
			return nil, ctx.Err()
		case <-changed:
			timer.Stop()
		case <-timer.C:
		}
	}
}

// AcquireFor 按模型方法账户或资源粘性获取账户槽位
func (p *AccountPool) AcquireFor(ctx context.Context, selection AccountSelection) (*AccountLease, error) {
	if selection.ResourceID != "" {
		if err := p.refreshResource(ctx, selection.ResourceID); err != nil {
			return nil, err
		}
	}
	refreshedCandidates := false
	for {
		p.mu.Lock()
		if err := p.validateSelectionLocked(selection); err != nil {
			p.mu.Unlock()
			return nil, err
		}
		lease, earliest, waitable, err := p.tryAcquireLocked(selection, time.Now())
		if err != nil {
			p.mu.Unlock()
			return nil, err
		}
		if lease != nil {
			p.mu.Unlock()
			eligible, refreshErr := p.refreshAndValidateLease(ctx, lease, selection)
			if refreshErr != nil {
				releaseErr := lease.Release()
				if errors.Is(refreshErr, ErrAccountNotFound) {
					p.markStaleAccountUnavailable(lease.account)
					if selection.AccountID == "" && selection.ResourceID == "" {
						if releaseErr != nil {
							return nil, releaseErr
						}
						continue
					}
				}
				return nil, errors.Join(refreshErr, releaseErr)
			}
			if eligible {
				return lease, nil
			}
			if err := lease.Release(); err != nil {
				return nil, err
			}
			continue
		}
		if !refreshedCandidates && (!waitable || !earliest.IsZero()) {
			p.mu.Unlock()
			if err := p.refreshSelectionRuntimes(ctx, selection); err != nil {
				return nil, err
			}
			refreshedCandidates = true
			continue
		}
		if !waitable {
			err := p.noEligibleErrorLocked(selection)
			p.mu.Unlock()
			return nil, err
		}
		changed := p.changed
		p.mu.Unlock()

		if earliest.IsZero() {
			select {
			case <-ctx.Done():
				return nil, ctx.Err()
			case <-changed:
			}
			continue
		}
		delay := time.Until(earliest)
		if delay <= 0 {
			continue
		}
		timer := time.NewTimer(delay)
		select {
		case <-ctx.Done():
			timer.Stop()
			return nil, ctx.Err()
		case <-changed:
			timer.Stop()
		case <-timer.C:
		}
	}
}

// TryAcquireFor 尝试获取账户槽位并立即返回当前结果
func (p *AccountPool) TryAcquireFor(ctx context.Context, selection AccountSelection) (*AccountLease, bool, error) {
	if selection.ResourceID != "" {
		if err := p.refreshResource(ctx, selection.ResourceID); err != nil {
			return nil, false, err
		}
	}
	refreshedCandidates := false
	for {
		p.mu.Lock()
		if err := p.validateSelectionLocked(selection); err != nil {
			p.mu.Unlock()
			return nil, false, err
		}
		lease, earliest, waitable, err := p.tryAcquireLocked(selection, time.Now())
		p.mu.Unlock()
		if err != nil {
			return nil, waitable, err
		}
		if lease == nil && !refreshedCandidates && (!waitable || !earliest.IsZero()) {
			if err := p.refreshSelectionRuntimes(ctx, selection); err != nil {
				return nil, false, err
			}
			refreshedCandidates = true
			continue
		}
		if lease == nil {
			return lease, waitable, err
		}
		eligible, refreshErr := p.refreshAndValidateLease(ctx, lease, selection)
		if refreshErr != nil {
			releaseErr := lease.Release()
			if errors.Is(refreshErr, ErrAccountNotFound) {
				p.markStaleAccountUnavailable(lease.account)
				if selection.AccountID == "" && selection.ResourceID == "" {
					if releaseErr != nil {
						return nil, false, releaseErr
					}
					continue
				}
			}
			return nil, false, errors.Join(refreshErr, releaseErr)
		}
		if eligible {
			return lease, waitable, nil
		}
		if err := lease.Release(); err != nil {
			return nil, false, err
		}
	}
}

func (p *AccountPool) refreshAndValidateLease(
	ctx context.Context,
	lease *AccountLease,
	selection AccountSelection,
) (bool, error) {
	if lease.refreshRuntime {
		if err := p.refreshAccountRuntime(ctx, lease.account); err != nil {
			return false, err
		}
		lease.refreshRuntime = false
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	account := lease.account
	if p.byID[account.ID] != account {
		return false, ErrNoEligibleAccount
	}
	lease.modelAccessGeneration = account.modelAccessGeneration
	if resourceID := strings.TrimSpace(selection.ResourceID); resourceID != "" {
		if owner, exists := p.resources[resourceID]; !exists || owner != account.ID {
			return false, ErrResourceNotFound
		}
	}
	if selection.ModelID != "" && !p.channelSupportsLocked(account, lease.Channel(), selection) {
		return false, nil
	}
	if selection.ResourceID == "" && !selection.anyState {
		if _, active := accountCooldown(account, lease.CooldownScope(selectionAccessScope(selection)), time.Now()); active {
			return false, nil
		}
	}
	return true, nil
}

// NoEligibleError 返回列出候选账户不可调度原因的无账户错误
func (p *AccountPool) NoEligibleError(selection AccountSelection) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.noEligibleErrorLocked(selection)
}

func (p *AccountPool) noEligibleErrorLocked(selection AccountSelection) error {
	if len(p.accounts) == 0 {
		return errNoAccounts
	}
	selection.ModelID = strings.TrimPrefix(strings.TrimSpace(selection.ModelID), "models/")
	var reasons []string
	for _, account := range p.accounts {
		if account.Config.Enabled && account.State == AccountReady {
			continue
		}
		if selection.ModelID != "" && !p.accountSupportsAnyChannelLocked(account, selection) {
			continue
		}
		label, ok := accountStateLabels[account.State]
		if !ok {
			label = string(account.State)
		}
		if !account.Config.Enabled {
			label = accountStateLabels[AccountDisabled]
		}
		reason := account.ID + " " + label
		if message := strings.TrimSpace(account.stateMessage); message != "" {
			reason += "（" + message + "）"
		}
		reasons = append(reasons, reason)
	}
	if len(reasons) == 0 {
		return ErrNoEligibleAccount
	}
	return &AccountsNotReadyError{Reasons: reasons}
}

func (p *AccountPool) markStaleAccountUnavailable(account *Account) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.byID[account.ID] == account {
		account.State = AccountUnavailable
		account.stateMessage = ErrAccountNotFound.Error()
		for resourceID, owner := range p.resources {
			if owner == account.ID {
				delete(p.resources, resourceID)
			}
		}
		clear(account.runtime.Resources)
		p.notifyLocked()
	}
}

func (p *AccountPool) validateSelectionLocked(selection AccountSelection) error {
	modelID := strings.TrimPrefix(strings.TrimSpace(selection.ModelID), "models/")
	if modelID == "" {
		return nil
	}
	if !p.hasModelCatalogLocked() {
		return ErrNoEligibleAccount
	}
	if !p.hasModelLocked(modelID) {
		return fmt.Errorf("%w: %s", ErrModelNotFound, modelID)
	}
	if selection.Method != "" && !p.hasModelMethodLocked(modelID, selection.Method) {
		return fmt.Errorf("%w: 模型 %s 不支持 %s", ErrModelNotFound, modelID, selection.Method)
	}
	if selection.Capability != "" && !p.hasModelCapabilityLocked(modelID, selection.Capability) {
		return fmt.Errorf("%w: 模型 %s 不支持 %s", ErrModelNotFound, modelID, selection.Capability)
	}
	if limited, ok := p.modelChannels[modelID]; ok && limited != ChannelPlayground && selection.Method == "generateContent" &&
		(strings.TrimSpace(selection.ResourceID) != "" || selection.PlaygroundOnly) {
		return fmt.Errorf("%w: 模型 %s 限定使用 %s 通道，此请求含文件引用或只有 Playground 支持的参数，只能经 Playground 发送", ErrInvalidArgument, modelID, limited)
	}
	return p.modelAccessErrorLocked(selection)
}

// modelAccessErrorLocked 返回只因启用账户权益不足而无法调用模型时的错误
func (p *AccountPool) modelAccessErrorLocked(selection AccountSelection) error {
	if p.enabledAccountSupportsLocked(selection) {
		return nil
	}
	selection.anyTier = true
	if !p.enabledAccountSupportsLocked(selection) {
		return nil
	}
	modelID := strings.TrimPrefix(strings.TrimSpace(selection.ModelID), "models/")
	var modes []int64
	for _, account := range p.accounts {
		for _, model := range account.Models {
			if modelMatchesID(model, modelID) {
				modes = unionInt64(modes, model.AccessModes)
			}
		}
	}
	return &ModelAccessError{Model: modelID, AccessModes: modes}
}

// enabledAccountSupportsLocked 判断是否有启用账户的某个通道支持选择
func (p *AccountPool) enabledAccountSupportsLocked(selection AccountSelection) bool {
	for _, account := range p.accounts {
		if account.Config.Enabled && p.accountSupportsAnyChannelLocked(account, selection) {
			return true
		}
	}
	return false
}

// AcquireResource 获取创建资源的固定账户
func (p *AccountPool) AcquireResource(ctx context.Context, resourceID string) (*AccountLease, error) {
	return p.AcquireFor(ctx, AccountSelection{ResourceID: resourceID})
}

// Account 返回当前租约持有的账户
func (l *AccountLease) Account() *Account {
	return l.account
}

// ModelAccessGeneration 返回租约开始时的模型资格目录版本号
func (l *AccountLease) ModelAccessGeneration() uint64 {
	return l.modelAccessGeneration
}

// CheckedAt 返回当前账户请求取得租约的时间
func (l *AccountLease) CheckedAt() time.Time {
	return l.checkedAt
}

// MarkAuthenticationValid 保存当前租约确认的认证成功状态
func (l *AccountLease) MarkAuthenticationValid() error {
	return l.markAuthenticationStateAt(false, "", l.CheckedAt())
}

// ConfirmAuthenticationFailure 在官网页面确认账户会话失效时保存 cause 对应的认证失败状态，会话有效时账户状态不变
func (l *AccountLease) ConfirmAuthenticationFailure(ctx context.Context, cause error) error {
	return l.confirmAuthenticationFailureAt(ctx, cause, l.CheckedAt())
}

// markAuthenticationValidAt 保存长连接中指定轮次的认证成功状态
func (l *AccountLease) markAuthenticationValidAt(checkedAt time.Time) error {
	return l.markAuthenticationStateAt(false, "", checkedAt)
}

// confirmAuthenticationFailureAt 在账户会话失效时保存长连接中指定轮次的认证失败状态
func (l *AccountLease) confirmAuthenticationFailureAt(ctx context.Context, cause error, checkedAt time.Time) error {
	l.pool.mu.Lock()
	check := l.pool.sessionCheck
	l.pool.mu.Unlock()
	if check != nil && !errors.Is(cause, camoufoxnative.ErrAuthenticationRequired) {
		l.sessionOnce.Do(func() {
			_, l.sessionErr, _ = l.pool.sessions.Do(l.account.ID, func() (any, error) {
				err := check(ctx, l.account)
				if err != nil && !errors.Is(err, camoufoxnative.ErrAuthenticationRequired) {
					slog.Warn("账户会话检查失败", "account", l.account.ID, "error", err)
				}
				return nil, err
			})
		})
		if !errors.Is(l.sessionErr, camoufoxnative.ErrAuthenticationRequired) {
			return nil
		}
	}
	return l.markAuthenticationStateAt(true, cause.Error(), checkedAt)
}

// CoolingDown 返回租约账户当前是否处于全局或指定模型的冷却
func (l *AccountLease) CoolingDown(modelID string) bool {
	return l.pool.AccountCoolingDown(l.account.ID, l.CooldownScope(strings.TrimPrefix(strings.TrimSpace(modelID), "models/")))
}

// AccountCoolingDown 返回账户当前是否处于全局或指定模型的冷却
func (p *AccountPool) AccountCoolingDown(accountID string, modelID string) bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	account := p.byID[strings.TrimSpace(accountID)]
	if account == nil {
		return false
	}
	_, active := accountCooldown(account, strings.TrimPrefix(strings.TrimSpace(modelID), "models/"), time.Now())
	return active
}

// markAuthenticationStateAt 写回指定顺序时间的账户认证状态
func (l *AccountLease) markAuthenticationStateAt(required bool, reason string, checkedAt time.Time) error {
	l.operation.Lock()
	authGeneration := l.authGeneration
	l.operation.Unlock()
	l.pool.mu.Lock()
	defer l.pool.mu.Unlock()
	if l.pool.byID[l.account.ID] != l.account || authGeneration != l.account.authGeneration ||
		checkedAt.Before(l.account.authCheckedAt) {
		return nil
	}
	l.account.authCheckedAt = checkedAt
	if !l.account.Config.Enabled {
		l.account.State = AccountDisabled
	} else if required {
		l.account.State = AccountAuthRequired
	} else if l.account.State == AccountAuthRequired {
		l.account.State = AccountReady
	}
	if required {
		l.account.stateMessage = strings.TrimSpace(reason)
	} else if l.account.State == AccountReady {
		l.account.stateMessage = ""
	}
	l.pool.notifyLocked()
	return nil
}

// ModelAccessGeneration 返回账户当前模型资格目录版本号
func (p *AccountPool) ModelAccessGeneration(accountID string) uint64 {
	p.mu.Lock()
	defer p.mu.Unlock()
	account := p.byID[strings.TrimSpace(accountID)]
	if account == nil {
		return 0
	}
	return account.modelAccessGeneration
}

// SaveStorageState 在租约内原子写回认证状态
func (l *AccountLease) SaveStorageState(state StorageState) error {
	l.operation.Lock()
	defer l.operation.Unlock()
	if l.released {
		return fmt.Errorf("账户租约已释放")
	}
	l.account.storageMu.Lock()
	defer l.account.storageMu.Unlock()
	if err := WriteStorageState(l.account.StoragePath, state); err != nil {
		return err
	}
	l.pool.mu.Lock()
	l.account.StorageState = state
	l.account.authGeneration++
	l.authGeneration = l.account.authGeneration
	l.account.authCheckedAt = time.Time{}
	l.pool.mu.Unlock()
	return nil
}

// AuthGeneration 返回账户当前的认证版本号
func (l *AccountLease) AuthGeneration() uint64 {
	l.pool.mu.Lock()
	defer l.pool.mu.Unlock()
	return l.account.authGeneration
}

// WaitForAuthRefresh 等待其他正常请求释放，或等到 generation 之后已经提交的新认证
func (l *AccountLease) WaitForAuthRefresh(ctx context.Context, generation uint64) error {
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		l.pool.mu.Lock()
		ready := l.exclusive || generation != l.account.authGeneration || l.account.active <= l.account.authRefreshers
		changed := l.pool.changed
		l.pool.mu.Unlock()
		if ready {
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-changed:
		}
	}
}

// RefreshStorageState 为 generation 下失效的认证提交一次新认证，generation 之后已有提交时直接复用
func (l *AccountLease) RefreshStorageState(
	generation uint64,
	update func(*StorageState) error,
	prepareCommit func() (func(bool), error),
) error {
	l.operation.Lock()
	defer l.operation.Unlock()
	if l.released {
		return fmt.Errorf("账户租约已释放")
	}
	l.account.storageMu.Lock()
	defer l.account.storageMu.Unlock()
	l.pool.mu.Lock()
	currentGeneration := l.account.authGeneration
	l.pool.mu.Unlock()
	if generation != currentGeneration {
		l.authGeneration = currentGeneration
		return nil
	}
	state, err := LoadStorageState(l.account.StoragePath)
	if err != nil {
		return err
	}
	if err := update(&state); err != nil {
		return err
	}
	finishCommit, err := prepareCommit()
	if err != nil {
		return err
	}
	if err := WriteStorageState(l.account.StoragePath, state); err != nil {
		finishCommit(false)
		return err
	}
	l.pool.mu.Lock()
	l.account.StorageState = state
	l.account.authGeneration++
	l.authGeneration = l.account.authGeneration
	l.account.authCheckedAt = time.Time{}
	l.pool.mu.Unlock()
	finishCommit(true)
	return nil
}

// BeginAuthRefresh 为当前账户取得认证刷新独占窗口
func (l *AccountLease) BeginAuthRefresh() (func(), bool) {
	l.operation.Lock()
	defer l.operation.Unlock()
	l.pool.mu.Lock()
	if l.released {
		l.pool.mu.Unlock()
		return nil, false
	}
	if l.exclusive {
		l.pool.mu.Unlock()
		return func() {}, true
	}
	if l.account.exclusive {
		l.pool.mu.Unlock()
		return nil, false
	}
	l.account.authRefreshers++
	l.pool.notifyLocked()
	l.pool.mu.Unlock()
	return func() {
		l.pool.mu.Lock()
		l.account.authRefreshers--
		l.pool.notifyLocked()
		l.pool.mu.Unlock()
	}, true
}

// SaveConfig 在租约内原子写回账户固定配置
func (l *AccountLease) SaveConfig(value AccountConfig) error {
	l.operation.Lock()
	defer l.operation.Unlock()
	if l.released {
		return fmt.Errorf("账户租约已释放")
	}
	l.account.storageMu.Lock()
	defer l.account.storageMu.Unlock()
	if err := writeAccountConfig(l.account.ConfigPath, value); err != nil {
		return err
	}
	l.pool.mu.Lock()
	wasDisabled := l.account.State == AccountDisabled
	l.account.Config = value
	if !value.Enabled {
		l.account.State = AccountDisabled
		l.account.stateMessage = ""
	} else if wasDisabled {
		l.account.State = initialAccountState(value, l.account.StorageState)
		l.account.stateMessage = ""
	}
	l.pool.notifyLocked()
	l.pool.mu.Unlock()
	return nil
}

// ReloadStorageState 在租约内重新读取认证状态
func (l *AccountLease) ReloadStorageState() (StorageState, error) {
	l.operation.Lock()
	defer l.operation.Unlock()
	if l.released {
		return StorageState{}, fmt.Errorf("账户租约已释放")
	}
	l.account.storageMu.Lock()
	defer l.account.storageMu.Unlock()
	state, err := LoadStorageState(l.account.StoragePath)
	if err != nil {
		return StorageState{}, err
	}
	l.pool.mu.Lock()
	l.account.StorageState = state
	l.pool.mu.Unlock()
	return state, nil
}

// ReplaceCookies 以固定指纹浏览器当前 Cookie 替换账户最新持久状态，只有轮换 Cookie 变化且磁盘状态在 rotatingCookieWriteInterval 内写过时不写盘
func (l *AccountLease) ReplaceCookies(cookies []StateCookie) error {
	if len(cookies) == 0 {
		return nil
	}
	l.operation.Lock()
	defer l.operation.Unlock()
	if l.released {
		return fmt.Errorf("账户租约已释放")
	}
	l.account.storageMu.Lock()
	defer l.account.storageMu.Unlock()
	state, err := LoadStorageState(l.account.StoragePath)
	if err != nil {
		return err
	}
	if onlyRotatingCookiesChanged(state.Cookies, cookies) {
		if info, err := os.Stat(l.account.StoragePath); err == nil && time.Since(info.ModTime()) < rotatingCookieWriteInterval {
			return nil
		}
	}
	state.Cookies = append([]StateCookie(nil), cookies...)
	if err := WriteStorageState(l.account.StoragePath, state); err != nil {
		return err
	}
	l.pool.mu.Lock()
	l.account.StorageState = state
	l.pool.mu.Unlock()
	return nil
}

// BindResource 将资源固定到当前租约账户
func (l *AccountLease) BindResource(resourceID string, kind string) error {
	l.operation.Lock()
	defer l.operation.Unlock()
	if l.released {
		return fmt.Errorf("账户租约已释放")
	}
	l.account.storageMu.Lock()
	defer l.account.storageMu.Unlock()
	return l.pool.BindResourceKind(resourceID, l.account.ID, kind)
}

// BindVideoOperation 保存视频任务账户与公开对象元数据
func (l *AccountLease) BindVideoOperation(
	ctx context.Context,
	resourceID string,
	metadata VideoResourceMetadata,
) (ResourceBinding, error) {
	l.operation.Lock()
	defer l.operation.Unlock()
	if l.released {
		return ResourceBinding{}, fmt.Errorf("账户租约已释放")
	}
	l.account.storageMu.Lock()
	defer l.account.storageMu.Unlock()
	return l.pool.bindVideoOperation(ctx, resourceID, l.account.ID, metadata)
}

// BindVideoResult 保存视频任务可下载的结果文件并绑定该文件
func (l *AccountLease) BindVideoResult(ctx context.Context, operationID string, fileID string) error {
	l.operation.Lock()
	defer l.operation.Unlock()
	if l.released {
		return fmt.Errorf("账户租约已释放")
	}
	l.account.storageMu.Lock()
	defer l.account.storageMu.Unlock()
	return l.pool.bindVideoResult(ctx, strings.TrimSpace(operationID), strings.TrimSpace(fileID), l.account.ID)
}

// VideoOperationBinding 返回当前账户持有的视频任务元数据
func (l *AccountLease) VideoOperationBinding(resourceID string) (ResourceBinding, error) {
	resourceID = strings.TrimSpace(resourceID)
	l.pool.mu.Lock()
	defer l.pool.mu.Unlock()
	if l.pool.byID[l.account.ID] != l.account || l.pool.resources[resourceID] != l.account.ID {
		return ResourceBinding{}, fmt.Errorf("%w: %s", ErrResourceNotFound, resourceID)
	}
	binding, exists := l.account.runtime.Resources[resourceID]
	if !exists || binding.Kind != "video-operation" || binding.Video == nil {
		return ResourceBinding{}, fmt.Errorf("%w: %s", ErrResourceNotFound, resourceID)
	}
	metadata := *binding.Video
	binding.Video = &metadata
	return binding, nil
}

// ReplaceResource 原子替换当前租约账户的单个资源绑定
func (l *AccountLease) ReplaceResource(previousResourceID string, resourceID string, kind string) error {
	l.operation.Lock()
	defer l.operation.Unlock()
	if l.released {
		return fmt.Errorf("账户租约已释放")
	}
	l.account.storageMu.Lock()
	defer l.account.storageMu.Unlock()
	return l.pool.replaceResource(previousResourceID, resourceID, l.account.ID, kind)
}

// MergeSetCookieHeaders 将响应 Cookie 合并到账户最新持久状态
func (l *AccountLease) MergeSetCookieHeaders(headers []string, requestURL string, now time.Time) error {
	l.operation.Lock()
	defer l.operation.Unlock()
	if l.released {
		return fmt.Errorf("账户租约已释放")
	}
	l.account.storageMu.Lock()
	defer l.account.storageMu.Unlock()
	state, err := LoadStorageState(l.account.StoragePath)
	if err != nil {
		return err
	}
	if err := state.MergeSetCookieHeaders(headers, requestURL, now); err != nil {
		return err
	}
	if err := WriteStorageState(l.account.StoragePath, state); err != nil {
		return err
	}
	l.pool.mu.Lock()
	l.account.StorageState = state
	l.pool.mu.Unlock()
	return nil
}

// Release 释放账户文件和进程内租约
func (l *AccountLease) Release() error {
	if l == nil {
		return nil
	}
	l.once.Do(func() {
		l.operation.Lock()
		defer l.operation.Unlock()
		l.released = true
		l.pool.mu.Lock()
		if l.exclusive {
			l.account.exclusive = false
		} else if l.account.active > 0 {
			l.account.active--
		}
		if l.account.active == 0 && !l.account.exclusive && l.account.authRefreshers == 0 && l.account.leaseLock != nil {
			if err := l.account.leaseLock.Unlock(); err != nil {
				l.err = err
			}
			l.account.leaseLock = nil
		}
		l.pool.notifyLocked()
		l.pool.mu.Unlock()
	})
	return l.err
}

// SetCatalog 替换账户的权益等级与实时模型目录
func (p *AccountPool) SetCatalog(accountID string, tier BenefitTier, models []Model) error {
	fingerprint, err := accountCatalogFingerprint(tier, models)
	if err != nil {
		return err
	}
	_, err = p.updateRuntime(accountID, func(account *Account, runtimeState *accountRuntimeState) (bool, func(*Account), error) {
		previousGeneration := account.modelAccessGeneration
		firstCatalog := account.initializedAt.IsZero()
		tierChanged := account.BenefitTier != tier
		catalogChanged := !firstCatalog && catalogEntriesChanged(account.Models, models)
		runtimeChanged := runtimeState.CatalogFingerprint != fingerprint || runtimeState.BenefitTier != tier
		persistedCatalogChanged := firstCatalog && runtimeState.CatalogFingerprint != fingerprint
		if tierChanged || persistedCatalogChanged {
			runtimeChanged = runtimeChanged || len(runtimeState.ModelAccess) > 0
			clear(runtimeState.ModelAccess)
		} else if !firstCatalog {
			for modelID := range runtimeState.ModelAccess {
				catalogModelID := modelAccessCatalogModelID(account.Models, models, modelID)
				if modelCatalogEntryChanged(account.Models, models, catalogModelID) && catalogChanged {
					delete(runtimeState.ModelAccess, modelID)
					runtimeChanged = true
				}
			}
		}
		runtimeState.BenefitTier = tier
		runtimeState.CatalogFingerprint = fingerprint
		return runtimeChanged, func(account *Account) {
			if (firstCatalog || tierChanged || catalogChanged) && account.modelAccessGeneration == previousGeneration {
				account.modelAccessGeneration++
			}
			account.BenefitTier = tier
			account.Models = cloneModels(models)
			account.catalogErr = nil
			account.initializedAt = time.Now()
			if account.State == AccountUnavailable {
				account.State = AccountReady
				account.stateMessage = ""
			}
		}, nil
	})
	return err
}

// ForgetModelAccessVerifiedIfGeneration 删除当前目录版本下过期的模型成功记录
func (p *AccountPool) ForgetModelAccessVerifiedIfGeneration(
	accountID string,
	modelID string,
	generation uint64,
	checkedAt time.Time,
) (bool, error) {
	modelID = strings.TrimPrefix(strings.TrimSpace(modelID), "models/")
	if modelID == "" {
		return false, fmt.Errorf("模型 ID 不能为空")
	}
	checkedAt = checkedAt.UTC()
	if checkedAt.IsZero() {
		return false, fmt.Errorf("模型资格检查时间不能为空")
	}
	forgotten := false
	_, err := p.updateRuntime(accountID, func(account *Account, runtimeState *accountRuntimeState) (bool, func(*Account), error) {
		if generation != account.modelAccessGeneration {
			return false, nil, nil
		}
		canonicalModelID := canonicalAccountModelID(account, modelID)
		current := runtimeState.ModelAccess[canonicalModelID]
		if !current.CheckedAt.Before(checkedAt) {
			return false, nil, nil
		}
		forgotten = current.State == ModelAccessVerified
		runtimeState.ModelAccess[canonicalModelID] = ModelAccess{CheckedAt: checkedAt}
		return true, nil, nil
	})
	return forgotten, err
}

// MarkModelAccessVerifiedIfGeneration 保存当前目录版本下的模型生成成功记录
func (p *AccountPool) MarkModelAccessVerifiedIfGeneration(
	accountID string,
	modelID string,
	generation uint64,
	checkedAt time.Time,
) (bool, error) {
	modelID = strings.TrimPrefix(strings.TrimSpace(modelID), "models/")
	if modelID == "" {
		return false, fmt.Errorf("模型 ID 不能为空")
	}
	checkedAt = checkedAt.UTC()
	if checkedAt.IsZero() {
		return false, fmt.Errorf("模型资格检查时间不能为空")
	}
	p.mu.Lock()
	account := p.byID[accountID]
	if account == nil {
		p.mu.Unlock()
		return false, fmt.Errorf("账户不存在: %s", accountID)
	}
	canonicalModelID := canonicalAccountModelID(account, modelID)
	current := account.runtime.ModelAccess[canonicalModelID]
	_, cooling := account.runtime.Cooldowns[canonicalModelID]
	globalAccess := account.runtime.ModelAccess[globalCooldownKey]
	_, globalCooling := account.runtime.Cooldowns[globalCooldownKey]
	unchanged := generation == account.modelAccessGeneration && current.State == ModelAccessVerified &&
		!cooling && !current.CheckedAt.Before(checkedAt) && !globalCooling &&
		!globalAccess.CheckedAt.Before(checkedAt)
	p.mu.Unlock()
	if unchanged {
		return false, nil
	}
	changed := false
	_, err := p.updateRuntime(accountID, func(account *Account, runtimeState *accountRuntimeState) (bool, func(*Account), error) {
		if generation != account.modelAccessGeneration {
			return false, nil, nil
		}
		canonicalModelID := canonicalAccountModelID(account, modelID)
		current := runtimeState.ModelAccess[canonicalModelID]
		runtimeChanged := false
		if !current.CheckedAt.After(checkedAt) {
			if current.CheckedAt.Before(checkedAt) || current.State != ModelAccessVerified {
				runtimeChanged = true
			}
			if _, exists := runtimeState.Cooldowns[canonicalModelID]; exists {
				runtimeChanged = true
			}
			changed = modelAccessState(account, canonicalModelID) != ModelAccessVerified
			runtimeState.ModelAccess[canonicalModelID] = ModelAccess{
				State: ModelAccessVerified, CheckedAt: checkedAt.UTC(),
			}
			delete(runtimeState.Cooldowns, canonicalModelID)
		}
		globalAccess := runtimeState.ModelAccess[globalCooldownKey]
		if !globalAccess.CheckedAt.After(checkedAt) {
			if globalAccess.CheckedAt.Before(checkedAt) {
				runtimeChanged = true
			}
			if _, exists := runtimeState.Cooldowns[globalCooldownKey]; exists {
				runtimeChanged = true
			}
			globalAccess.CheckedAt = checkedAt.UTC()
			runtimeState.ModelAccess[globalCooldownKey] = globalAccess
			delete(runtimeState.Cooldowns, globalCooldownKey)
		}
		return runtimeChanged, nil, nil
	})
	if err != nil {
		return false, err
	}
	return changed, nil
}

// ResetModelAccess 清空账户的实测模型资格
func (p *AccountPool) ResetModelAccess(accountID string) error {
	_, err := p.updateRuntime(accountID, func(account *Account, runtimeState *accountRuntimeState) (bool, func(*Account), error) {
		if len(runtimeState.ModelAccess) == 0 {
			return false, func(account *Account) { account.modelAccessGeneration++ }, nil
		}
		clear(runtimeState.ModelAccess)
		return true, func(account *Account) { account.modelAccessGeneration++ }, nil
	})
	return err
}

// CandidateStates 返回候选账户的模型资格状态
func (p *AccountPool) CandidateStates(accountIDs []string, modelID string) map[string]AccountCandidateState {
	return p.CandidateStatesForScope(accountIDs, modelID, "")
}

// CandidateStatesForScope 返回指定资格范围的候选账户状态
func (p *AccountPool) CandidateStatesForScope(
	accountIDs []string,
	modelID string,
	modelAccessScope string,
) map[string]AccountCandidateState {
	result := make(map[string]AccountCandidateState, len(accountIDs))
	modelID = strings.TrimPrefix(strings.TrimSpace(modelID), "models/")
	modelAccessScope = strings.TrimSpace(modelAccessScope)
	if modelAccessScope == "" {
		modelAccessScope = modelID
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	for _, accountID := range accountIDs {
		account := p.byID[accountID]
		if account == nil {
			continue
		}
		result[accountID] = AccountCandidateState{ModelAccess: modelAccessState(account, modelAccessScope)}
	}
	return result
}

// PreferWarmPool 按权益分层轮选初始热账户
func (p *AccountPool) PreferWarmPool(accountIDs []string) []string {
	type warmCandidate struct {
		id       string
		coverage int
	}
	p.mu.Lock()
	groups := make(map[int][]warmCandidate)
	for _, accountID := range accountIDs {
		account := p.byID[accountID]
		if account == nil {
			continue
		}
		coverage := 0
		for _, model := range account.Models {
			if account.SupportsMethod(model.ID, "generateContent") {
				coverage++
			}
		}
		priority := benefitTierPriority(account.BenefitTier)
		groups[priority] = append(groups[priority], warmCandidate{id: accountID, coverage: coverage})
	}
	p.mu.Unlock()
	priorities := make([]int, 0, len(groups))
	for priority, candidates := range groups {
		priorities = append(priorities, priority)
		sort.SliceStable(candidates, func(left int, right int) bool {
			return candidates[left].coverage > candidates[right].coverage
		})
		groups[priority] = candidates
	}
	sort.Ints(priorities)
	result := make([]string, 0, len(accountIDs))
	for len(result) < len(accountIDs) {
		added := false
		for _, priority := range priorities {
			candidates := groups[priority]
			if len(candidates) == 0 {
				continue
			}
			result = append(result, candidates[0].id)
			groups[priority] = candidates[1:]
			added = true
		}
		if !added {
			break
		}
	}
	return result
}

// MarkCooldownIfGeneration 保存当前目录版本下的作用域冷却
func (p *AccountPool) MarkCooldownIfGeneration(
	accountID string,
	modelAccessScope string,
	generation uint64,
	checkedAt time.Time,
	until time.Time,
	reason string,
) error {
	if !until.After(time.Now()) {
		return fmt.Errorf("冷却期限必须在未来")
	}
	checkedAt = checkedAt.UTC()
	if checkedAt.IsZero() {
		return fmt.Errorf("冷却检查时间不能为空")
	}
	modelAccessScope = strings.TrimSpace(modelAccessScope)
	if modelAccessScope == "" {
		modelAccessScope = globalCooldownKey
	}
	_, err := p.updateRuntime(accountID, func(account *Account, runtimeState *accountRuntimeState) (bool, func(*Account), error) {
		if generation != account.modelAccessGeneration {
			return false, nil, nil
		}
		modelAccessScope = canonicalAccountModelID(account, modelAccessScope)
		currentAccess := runtimeState.ModelAccess[modelAccessScope]
		if !currentAccess.CheckedAt.Before(checkedAt) {
			return false, nil, nil
		}
		next := CooldownState{Until: until.UTC(), Reason: reason}
		currentAccess.CheckedAt = checkedAt
		runtimeState.ModelAccess[modelAccessScope] = currentAccess
		runtimeState.Cooldowns[modelAccessScope] = next
		return true, nil, nil
	})
	return err
}

// ModelRequiresSubscription 判断账户实时目录中的模型是否只对 Pro 或 Ultra 权益开放
func (p *AccountPool) ModelRequiresSubscription(accountID string, modelID string) bool {
	modelID = strings.TrimPrefix(strings.TrimSpace(modelID), "models/")
	p.mu.Lock()
	defer p.mu.Unlock()
	account := p.byID[accountID]
	if account == nil {
		return false
	}
	for _, model := range account.Models {
		if modelMatchesID(model, modelID) {
			return !modelAllowedByTier(model, BenefitTierFree)
		}
	}
	return false
}

// MarkQuotaCooldownIfGeneration 保存当前目录版本下的额度冷却，返回实际生效的冷却期限；递增冷却被较新结果跳过时返回零值
func (p *AccountPool) MarkQuotaCooldownIfGeneration(
	accountID string,
	modelAccessScope string,
	generation uint64,
	checkedAt time.Time,
	cooldown QuotaCooldown,
) (time.Time, error) {
	if !cooldown.Escalate {
		return cooldown.Until, p.MarkCooldownIfGeneration(accountID, modelAccessScope, generation, checkedAt, cooldown.Until, cooldown.Reason)
	}
	checkedAt = checkedAt.UTC()
	if checkedAt.IsZero() {
		return time.Time{}, fmt.Errorf("冷却检查时间不能为空")
	}
	modelAccessScope = strings.TrimSpace(modelAccessScope)
	if modelAccessScope == "" {
		modelAccessScope = globalCooldownKey
	}
	var until time.Time
	_, err := p.updateRuntime(accountID, func(account *Account, runtimeState *accountRuntimeState) (bool, func(*Account), error) {
		if generation != account.modelAccessGeneration {
			return false, nil, nil
		}
		modelAccessScope = canonicalAccountModelID(account, modelAccessScope)
		currentAccess := runtimeState.ModelAccess[modelAccessScope]
		if !currentAccess.CheckedAt.Before(checkedAt) {
			return false, nil, nil
		}
		previous, exists := runtimeState.Cooldowns[modelAccessScope]
		next := planLimitCooldown(previous, exists, time.Now(), cooldown.Reason)
		until = next.Until
		currentAccess.CheckedAt = checkedAt
		runtimeState.ModelAccess[modelAccessScope] = currentAccess
		runtimeState.Cooldowns[modelAccessScope] = next
		return true, nil, nil
	})
	return until, err
}

// ClearCooldownIfGeneration 清除当前目录版本下的作用域冷却
func (p *AccountPool) ClearCooldownIfGeneration(
	accountID string,
	modelAccessScope string,
	generation uint64,
	checkedAt time.Time,
) error {
	checkedAt = checkedAt.UTC()
	if checkedAt.IsZero() {
		return fmt.Errorf("冷却检查时间不能为空")
	}
	modelAccessScope = strings.TrimSpace(modelAccessScope)
	if modelAccessScope == "" {
		modelAccessScope = globalCooldownKey
	}
	_, err := p.updateRuntime(accountID, func(account *Account, runtimeState *accountRuntimeState) (bool, func(*Account), error) {
		if generation != account.modelAccessGeneration {
			return false, nil, nil
		}
		modelAccessScope = canonicalAccountModelID(account, modelAccessScope)
		currentAccess := runtimeState.ModelAccess[modelAccessScope]
		changed := false
		if !currentAccess.CheckedAt.After(checkedAt) {
			_, cooling := runtimeState.Cooldowns[modelAccessScope]
			if currentAccess.CheckedAt.Before(checkedAt) || cooling {
				changed = true
				currentAccess.CheckedAt = checkedAt
				runtimeState.ModelAccess[modelAccessScope] = currentAccess
				delete(runtimeState.Cooldowns, modelAccessScope)
			}
		}
		if modelAccessScope != globalCooldownKey {
			globalAccess := runtimeState.ModelAccess[globalCooldownKey]
			if !globalAccess.CheckedAt.After(checkedAt) {
				_, cooling := runtimeState.Cooldowns[globalCooldownKey]
				if globalAccess.CheckedAt.Before(checkedAt) || cooling {
					changed = true
					globalAccess.CheckedAt = checkedAt
					runtimeState.ModelAccess[globalCooldownKey] = globalAccess
					delete(runtimeState.Cooldowns, globalCooldownKey)
				}
			}
		}
		return changed, nil, nil
	})
	return err
}

// ClearCooldown 清除账户在作用域上的冷却，作用域为全局 *、模型 ID 或 build:<模型 ID>
func (p *AccountPool) ClearCooldown(accountID string, scope string) error {
	_, err := p.updateRuntime(accountID, func(_ *Account, runtimeState *accountRuntimeState) (bool, func(*Account), error) {
		if _, exists := runtimeState.Cooldowns[scope]; !exists {
			return false, nil, nil
		}
		delete(runtimeState.Cooldowns, scope)
		return true, nil, nil
	})
	return err
}

// BindResourceKind 将带类型的资源 ID 固定到创建账户
func (p *AccountPool) BindResourceKind(resourceID string, accountID string, kind string) error {
	resourceID = strings.TrimSpace(resourceID)
	if resourceID == "" {
		return fmt.Errorf("资源 ID 不能为空")
	}
	_, err := p.updateRuntime(accountID, func(_ *Account, runtimeState *accountRuntimeState) (bool, func(*Account), error) {
		if owner, exists := p.resources[resourceID]; exists && owner != accountID {
			return false, nil, fmt.Errorf("资源 %s 已绑定账户 %s", resourceID, owner)
		}
		if _, exists := runtimeState.Resources[resourceID]; exists {
			return false, nil, nil
		}
		runtimeState.Resources[resourceID] = ResourceBinding{Kind: strings.TrimSpace(kind), CreatedAt: time.Now().UTC()}
		return true, nil, nil
	})
	return err
}

func (p *AccountPool) bindVideoOperation(
	ctx context.Context,
	resourceID string,
	accountID string,
	metadata VideoResourceMetadata,
) (ResourceBinding, error) {
	resourceID = strings.TrimSpace(resourceID)
	metadata.Model = strings.TrimSpace(metadata.Model)
	metadata.Seconds = strings.TrimSpace(metadata.Seconds)
	metadata.Size = strings.TrimSpace(metadata.Size)
	if resourceID == "" || metadata.Model == "" || metadata.Seconds == "" || metadata.Size == "" {
		return ResourceBinding{}, fmt.Errorf("视频任务元数据不完整")
	}
	var bound ResourceBinding
	_, err := p.updateRuntimeContext(ctx, accountID, func(_ *Account, runtimeState *accountRuntimeState) (bool, func(*Account), error) {
		if owner, exists := p.resources[resourceID]; exists && owner != accountID {
			return false, nil, fmt.Errorf("资源 %s 已绑定账户 %s", resourceID, owner)
		}
		if existing, exists := runtimeState.Resources[resourceID]; exists {
			if existing.Kind != "video-operation" || existing.Video == nil {
				return false, nil, fmt.Errorf("资源 %s 不是视频任务", resourceID)
			}
			bound = existing
			return false, nil, nil
		}
		bound = ResourceBinding{
			Kind: "video-operation", CreatedAt: time.Now().UTC(), Video: &metadata,
		}
		runtimeState.Resources[resourceID] = bound
		return true, nil, nil
	})
	if err != nil {
		return ResourceBinding{}, err
	}
	return bound, nil
}

func (p *AccountPool) bindVideoResult(ctx context.Context, operationID string, fileID string, accountID string) error {
	_, err := p.updateRuntimeContext(ctx, accountID, func(_ *Account, runtimeState *accountRuntimeState) (bool, func(*Account), error) {
		if owner, exists := p.resources[fileID]; exists && owner != accountID {
			return false, nil, fmt.Errorf("资源 %s 已绑定账户 %s", fileID, owner)
		}
		operation, exists := runtimeState.Resources[operationID]
		if !exists || operation.Kind != "video-operation" || operation.Video == nil {
			return false, nil, fmt.Errorf("%w: %s", ErrResourceNotFound, operationID)
		}
		metadata := *operation.Video
		metadata.File = fileID
		operation.Video = &metadata
		runtimeState.Resources[operationID] = operation
		if _, exists := runtimeState.Resources[fileID]; !exists {
			runtimeState.Resources[fileID] = ResourceBinding{Kind: "video-file", CreatedAt: time.Now().UTC()}
		}
		return true, nil, nil
	})
	return err
}

func (p *AccountPool) replaceResource(previousResourceID string, resourceID string, accountID string, kind string) error {
	previousResourceID = strings.TrimSpace(previousResourceID)
	resourceID = strings.TrimSpace(resourceID)
	kind = strings.TrimSpace(kind)
	_, err := p.updateRuntime(accountID, func(_ *Account, runtimeState *accountRuntimeState) (bool, func(*Account), error) {
		for _, candidate := range []string{previousResourceID, resourceID} {
			if candidate == "" {
				continue
			}
			if owner, exists := p.resources[candidate]; exists && owner != accountID {
				return false, nil, fmt.Errorf("资源 %s 已绑定账户 %s", candidate, owner)
			}
		}
		changed := false
		if previousResourceID != "" && previousResourceID != resourceID {
			if _, exists := runtimeState.Resources[previousResourceID]; exists {
				delete(runtimeState.Resources, previousResourceID)
				changed = true
			}
		}
		binding, exists := runtimeState.Resources[resourceID]
		if !exists {
			runtimeState.Resources[resourceID] = ResourceBinding{Kind: kind, CreatedAt: time.Now().UTC()}
			return true, nil, nil
		}
		if binding.Kind == "" && kind != "" {
			binding.Kind = kind
			runtimeState.Resources[resourceID] = binding
			changed = true
		}
		return changed, nil, nil
	})
	return err
}

// UnbindResource 删除终态资源的账户映射
func (p *AccountPool) UnbindResource(resourceID string) error {
	return p.unbindResourceContext(context.Background(), resourceID)
}

func (p *AccountPool) unbindResourceContext(ctx context.Context, resourceID string) error {
	p.mu.Lock()
	accountID, exists := p.resources[resourceID]
	p.mu.Unlock()
	if !exists {
		if err := p.refreshResource(ctx, resourceID); err != nil {
			return err
		}
		p.mu.Lock()
		accountID, exists = p.resources[resourceID]
		p.mu.Unlock()
	}
	if !exists {
		return ErrResourceNotFound
	}
	_, err := p.updateRuntimeContext(ctx, accountID, func(_ *Account, runtimeState *accountRuntimeState) (bool, func(*Account), error) {
		if _, exists := runtimeState.Resources[resourceID]; !exists {
			return false, nil, ErrResourceNotFound
		}
		delete(runtimeState.Resources, resourceID)
		return true, nil, nil
	})
	return err
}

// MarkAuthRequired 将账户标记为需要重新登录
func (p *AccountPool) MarkAuthRequired(accountID string, reason string) error {
	return p.setAccountState(accountID, AccountAuthRequired, reason)
}

// recordCatalogFailure 记录账户最近一次权益或目录读取失败的原因
func (p *AccountPool) recordCatalogFailure(accountID string, err error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if account := p.byID[accountID]; account != nil {
		account.catalogErr = err
	}
}

// MarkReady 将账户恢复为可调度状态
func (p *AccountPool) MarkReady(accountID string) error {
	return p.setAccountState(accountID, AccountReady, "")
}

// Status 返回账户池的脱敏状态
func (p *AccountPool) Status() []AccountStatus {
	p.mu.Lock()
	defer p.mu.Unlock()
	now := time.Now()
	statuses := make([]AccountStatus, 0, len(p.accounts))
	for _, account := range p.accounts {
		state := account.State
		_, active := accountCooldown(account, "", now)
		if !account.Config.Enabled {
			state = AccountDisabled
		} else if state == AccountReady && (account.exclusive || account.exclusiveWaiters > 0 || account.authRefreshers > 0 || account.active > 0) {
			state = AccountBusy
		} else if state == AccountReady && active {
			state = AccountCooldown
		}
		models := make([]string, 0, len(account.Models))
		for _, model := range account.Models {
			models = append(models, model.ID)
		}
		sort.Strings(models)
		tier := account.BenefitTier.String()
		message := account.stateMessage
		if account.runtime.CatalogFingerprint == "" {
			tier = "Unknown"
		}
		if message == "" && account.catalogErr != nil && (account.runtime.CatalogFingerprint == "" || errors.Is(account.catalogErr, camoufoxnative.ErrRegionNotSupported)) {
			message = account.catalogErr.Error()
		}
		status := AccountStatus{
			ID:          account.ID,
			Label:       account.Config.Label,
			State:       state,
			Enabled:     account.Config.Enabled,
			Proxy:       account.Config.Proxy,
			Egress:      account.EffectiveProxy(""),
			Locale:      account.Config.Locale,
			Timezone:    account.Config.Timezone,
			Models:      models,
			BenefitTier: tier,
			Cooldowns:   cloneCooldowns(account.runtime.Cooldowns),
			Message:     message,
		}
		if !account.LastUsed.IsZero() {
			status.LastUsed = timePointer(account.LastUsed)
		}
		statuses = append(statuses, status)
	}
	return statuses
}

// ClassifyCandidates 按 warm 集合分类目标请求的候选账户
func (p *AccountPool) ClassifyCandidates(
	ctx context.Context,
	selection AccountSelection,
	warmAccountIDs []string,
) (AccountCandidateGroups, error) {
	selection.ModelID = strings.TrimPrefix(strings.TrimSpace(selection.ModelID), "models/")
	if selection.ResourceID != "" {
		if err := p.refreshResource(ctx, selection.ResourceID); err != nil {
			return AccountCandidateGroups{}, err
		}
	}
	for attempt := 0; attempt < 2; attempt++ {
		p.mu.Lock()
		groups, err := p.classifyCandidatesLocked(selection, warmAccountIDs)
		p.mu.Unlock()
		if err != nil {
			return AccountCandidateGroups{}, err
		}
		available := len(groups.WarmReady)+len(groups.WarmAvailable)+len(groups.WarmBusy)+
			len(groups.StandbyReady)+len(groups.StandbyBusy) > 0
		if attempt == 0 && (!groups.Eligible || !available && !groups.EarliestCooldown.Until.IsZero()) {
			if err := p.refreshSelectionRuntimes(ctx, selection); err != nil {
				return AccountCandidateGroups{}, err
			}
			continue
		}
		return groups, nil
	}
	return AccountCandidateGroups{}, ErrNoEligibleAccount
}

func (p *AccountPool) classifyCandidatesLocked(
	selection AccountSelection,
	warmAccountIDs []string,
) (AccountCandidateGroups, error) {
	if len(p.accounts) == 0 {
		return AccountCandidateGroups{}, errNoAccounts
	}
	modelID := selection.ModelID
	if modelID != "" {
		if !p.hasModelCatalogLocked() {
			return AccountCandidateGroups{}, ErrNoEligibleAccount
		}
		if !p.hasModelLocked(modelID) {
			return AccountCandidateGroups{}, fmt.Errorf("%w: %s", ErrModelNotFound, modelID)
		}
		if selection.Method != "" && !p.hasModelMethodLocked(modelID, selection.Method) {
			return AccountCandidateGroups{}, fmt.Errorf("%w: 模型 %s 不支持 %s", ErrModelNotFound, modelID, selection.Method)
		}
		if err := p.modelAccessErrorLocked(selection); err != nil {
			return AccountCandidateGroups{}, err
		}
	}
	indices, err := p.selectionIndicesLocked(selection)
	if err != nil {
		return AccountCandidateGroups{}, err
	}
	warm := make(map[string]struct{}, len(warmAccountIDs))
	for _, accountID := range warmAccountIDs {
		if accountID = strings.TrimSpace(accountID); accountID != "" {
			warm[accountID] = struct{}{}
		}
	}
	now := time.Now()
	groups := AccountCandidateGroups{}
	for _, index := range indices {
		account := p.accounts[index]
		if !account.Config.Enabled || account.State != AccountReady {
			continue
		}
		if modelID != "" && !p.accountSupportsAnyChannelLocked(account, selection) {
			continue
		}
		groups.Eligible = true
		if cooldown, active := p.accountChannelCooldownLocked(account, selection, now); active {
			if groups.EarliestCooldown.Until.IsZero() || cooldown.Until.Before(groups.EarliestCooldown.Until) {
				groups.EarliestCooldown = cooldown
			}
			continue
		}
		_, isWarm := warm[account.ID]
		switch {
		case isWarm && !account.exclusive && account.exclusiveWaiters == 0 && account.authRefreshers == 0 && account.active == 0:
			groups.WarmReady = append(groups.WarmReady, account.ID)
		case isWarm && !account.exclusive && account.exclusiveWaiters == 0 && account.authRefreshers == 0 && account.active < p.perAccountConcurrency:
			groups.WarmAvailable = append(groups.WarmAvailable, account.ID)
		case isWarm:
			groups.WarmBusy = append(groups.WarmBusy, account.ID)
		case account.exclusive || account.exclusiveWaiters > 0 || account.authRefreshers > 0 || account.active > 0:
			groups.StandbyBusy = append(groups.StandbyBusy, account.ID)
		default:
			groups.StandbyReady = append(groups.StandbyReady, account.ID)
		}
	}
	return groups, nil
}

func (p *AccountPool) tryAcquireLocked(selection AccountSelection, now time.Time) (*AccountLease, time.Time, bool, error) {
	indices, err := p.selectionIndicesLocked(selection)
	if err != nil {
		return nil, time.Time{}, false, err
	}
	waitable := false
	var earliest time.Time
	for _, candidate := range p.channelCandidatesLocked(indices, selection) {
		account := p.accounts[candidate.index]
		if !selection.anyState && (!account.Config.Enabled || account.State != AccountReady) {
			continue
		}
		if selection.ModelID != "" && !p.channelSupportsLocked(account, candidate.channel, selection) {
			continue
		}
		waitable = true
		if account.exclusive || account.exclusiveWaiters > 0 || account.authRefreshers > 0 || account.active >= p.perAccountConcurrency {
			continue
		}
		if selection.ResourceID == "" && !selection.anyState {
			if cooldown, active := accountCooldown(account, ChannelCooldownScope(candidate.channel, selectionAccessScope(selection)), now); active {
				if earliest.IsZero() || cooldown.Until.Before(earliest) {
					earliest = cooldown.Until
				}
				continue
			}
		}
		if candidate.channel != selection.PreferredChannel && p.preferredChannelOpenLocked(account, selection, now) {
			continue
		}
		refreshRuntime := false
		if account.active == 0 {
			leaseLock, err := acquireAccountFileLease(account.StoragePath)
			if errors.Is(err, ErrAccountLeased) {
				pollAt := now.Add(externalLeasePoll)
				if earliest.IsZero() || pollAt.Before(earliest) {
					earliest = pollAt
				}
				continue
			}
			if err != nil {
				return nil, time.Time{}, false, err
			}
			account.leaseLock = leaseLock
			refreshRuntime = leaseLock != nil
		}
		account.active++
		account.LastUsed = now
		p.lastPicked[selectionAccessScope(selection)] = account.ID
		p.lastPickedChannel[selectionAccessScope(selection)] = candidate.channel
		return &AccountLease{
			pool: p, account: account, authGeneration: account.authGeneration,
			modelAccessGeneration: account.modelAccessGeneration, refreshRuntime: refreshRuntime, checkedAt: now.UTC(),
			channel: candidate.channel,
		}, time.Time{}, true, nil
	}
	return nil, earliest, waitable, nil
}

func (p *AccountPool) hasModelLocked(modelID string) bool {
	for _, account := range p.accounts {
		for _, model := range account.Models {
			if modelMatchesID(model, modelID) {
				return true
			}
		}
	}
	return p.hasBuildModelLocked(modelID, "")
}

func (p *AccountPool) hasModelCatalogLocked() bool {
	for _, account := range p.accounts {
		if len(account.Models) > 0 {
			return true
		}
	}
	return false
}

func (p *AccountPool) hasModelMethodLocked(modelID string, method string) bool {
	for _, account := range p.accounts {
		for _, model := range account.Models {
			if modelMatchesID(model, modelID) && hasMethod(model, method) {
				return true
			}
		}
	}
	return p.hasBuildModelLocked(modelID, method)
}

func (p *AccountPool) hasModelCapabilityLocked(modelID string, capability string) bool {
	for _, account := range p.accounts {
		for _, model := range account.Models {
			if modelMatchesID(model, modelID) && model.Capabilities[capability] {
				return true
			}
		}
	}
	return false
}

// BootstrapModels 返回账户实时目录中的 WAA 初始化模型
func (p *AccountPool) BootstrapModels(accountID string) ([]string, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	account := p.byID[strings.TrimSpace(accountID)]
	if account == nil {
		return nil, fmt.Errorf("账户不存在: %s", accountID)
	}
	models := accountBootstrapModels(account)
	if len(models) == 0 {
		return nil, fmt.Errorf("账户 %s 的实时目录没有可用 WAA 初始化模型", account.ID)
	}
	return models, nil
}

// BootstrapModel 返回账户使用的通用 WAA 初始化模型
func (p *AccountPool) BootstrapModel(accountID string) (string, error) {
	models, err := p.BootstrapModels(accountID)
	if err != nil {
		return "", err
	}
	return models[0], nil
}

func accountBootstrapModels(account *Account) []string {
	models := make([]string, 0, len(account.Models))
	seen := make(map[string]struct{}, len(account.Models))
	appendModel := func(modelID string) {
		if _, exists := seen[modelID]; exists || !waaBootstrapModelEligible(account, modelID) {
			return
		}
		seen[modelID] = struct{}{}
		models = append(models, modelID)
	}
	appendModel(preferredBootstrapModelID)
	for _, model := range account.Models {
		appendModel(model.ID)
	}
	return models
}

func waaBootstrapModelEligible(account *Account, modelID string) bool {
	for _, model := range account.Models {
		if !modelMatchesID(model, modelID) || !hasMethod(model, "generateContent") ||
			!modelAllowedByTier(model, account.BenefitTier) || !model.Capabilities["chat_model"] || model.Capabilities["assistant_route"] {
			continue
		}
		return true
	}
	return false
}

func (p *AccountPool) selectionIndicesLocked(selection AccountSelection) ([]int, error) {
	var allowed map[string]struct{}
	if selection.AllowedAccountIDs != nil {
		allowed = make(map[string]struct{}, len(selection.AllowedAccountIDs))
		for _, accountID := range selection.AllowedAccountIDs {
			if accountID = strings.TrimSpace(accountID); accountID != "" {
				allowed[accountID] = struct{}{}
			}
		}
	}
	accountID := strings.TrimSpace(selection.AccountID)
	if selection.ResourceID != "" {
		owner, exists := p.resources[selection.ResourceID]
		if !exists {
			return nil, ErrResourceNotFound
		}
		if accountID != "" && accountID != owner {
			return nil, fmt.Errorf("资源 %s 绑定账户 %s", selection.ResourceID, owner)
		}
		accountID = owner
	}
	if accountID != "" {
		for index, account := range p.accounts {
			if account.ID == accountID {
				if allowed != nil {
					if _, exists := allowed[accountID]; !exists {
						return nil, ErrNoEligibleAccount
					}
				}
				return []int{index}, nil
			}
		}
		return nil, ErrNoEligibleAccount
	}
	indices := make([]int, 0, len(p.accounts))
	for index, account := range p.accounts {
		if allowed != nil {
			if _, exists := allowed[account.ID]; !exists {
				continue
			}
		}
		indices = append(indices, index)
	}
	sort.Slice(indices, func(left, right int) bool {
		return p.accounts[indices[left]].ID < p.accounts[indices[right]].ID
	})
	if p.routingStrategy == "round-robin" {
		last := p.lastPicked[selectionAccessScope(selection)]
		start := sort.Search(len(indices), func(index int) bool { return p.accounts[indices[index]].ID > last })
		indices = append(indices[start:], indices[:start]...)
	}
	return indices, nil
}

// SetRoutingStrategy 设置账户轮询或优先填满策略
func (p *AccountPool) SetRoutingStrategy(strategy string) {
	p.mu.Lock()
	p.routingStrategy = strategy
	p.mu.Unlock()
}

// SetSessionCheck 设置认证失败后确认账户会话是否失效的检查
func (p *AccountPool) SetSessionCheck(check func(context.Context, *Account) error) {
	p.mu.Lock()
	p.sessionCheck = check
	p.mu.Unlock()
}

// SetPerAccountConcurrency 设置单账户同时处理的请求数并唤醒等待中的请求
func (p *AccountPool) SetPerAccountConcurrency(limit int) {
	p.mu.Lock()
	p.perAccountConcurrency = limit
	p.notifyLocked()
	p.mu.Unlock()
}

// OrderCandidates 按当前策略排列候选账户而不推进轮询位置
func (p *AccountPool) OrderCandidates(accountIDs []string, modelAccessScope string) []string {
	if len(accountIDs) == 0 {
		return nil
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	indices, _ := p.selectionIndicesLocked(AccountSelection{AllowedAccountIDs: accountIDs, ModelAccessScope: modelAccessScope})
	ordered := make([]string, 0, len(indices))
	for _, index := range indices {
		ordered = append(ordered, p.accounts[index].ID)
	}
	return ordered
}

func (p *AccountPool) setAccountState(accountID string, state AccountState, reason string) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	account := p.byID[accountID]
	if account == nil {
		return fmt.Errorf("账户不存在: %s", accountID)
	}
	if !account.Config.Enabled {
		account.State = AccountDisabled
	} else {
		account.State = state
	}
	account.stateMessage = strings.TrimSpace(reason)
	if state == AccountReady || state == AccountAuthRequired {
		account.authCheckedAt = time.Now().UTC()
	}
	p.notifyLocked()
	return nil
}

// EnabledAccounts 返回已启用账户 ID 与其中 ready 或 busy 的账户数量
func (p *AccountPool) EnabledAccounts() ([]string, int) {
	p.mu.Lock()
	defer p.mu.Unlock()
	now := time.Now()
	ids := make([]string, 0, len(p.accounts))
	schedulable := 0
	for _, account := range p.accounts {
		if !account.Config.Enabled {
			continue
		}
		ids = append(ids, account.ID)
		_, cooling := accountCooldown(account, "", now)
		if account.exclusive || account.exclusiveWaiters > 0 || account.authRefreshers > 0 || account.active > 0 || account.State == AccountReady && !cooling {
			schedulable++
		}
	}
	return ids, schedulable
}

// Activity 返回账户是否存在活动租约或独占操作及最近使用时间
func (p *AccountPool) Activity(accountID string) (bool, time.Time) {
	p.mu.Lock()
	defer p.mu.Unlock()
	account := p.byID[accountID]
	if account == nil {
		return false, time.Time{}
	}
	return account.exclusive || account.exclusiveWaiters > 0 || account.authRefreshers > 0 || account.active > 0, account.LastUsed
}

// Changed 返回账户池下一次租约或状态变化时关闭的通道
func (p *AccountPool) Changed() <-chan struct{} {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.changed
}

func (p *AccountPool) notifyLocked() {
	close(p.changed)
	p.changed = make(chan struct{})
}

func loadAccount(directory string) (*Account, error) {
	directory, err := filepath.Abs(directory)
	if err != nil {
		return nil, fmt.Errorf("解析账户目录: %w", err)
	}
	id := filepath.Base(directory)
	if id == "." || id == string(filepath.Separator) || strings.TrimSpace(id) == "" {
		return nil, fmt.Errorf("账户目录缺少稳定 ID")
	}
	configPath := filepath.Join(directory, accountConfigName)
	storagePath := filepath.Join(directory, storageStateName)
	accountConfig, err := readAccountConfig(configPath)
	if err != nil {
		return nil, fmt.Errorf("账户 %s: %w", id, err)
	}
	state, err := LoadStorageState(storagePath)
	if err != nil {
		return nil, fmt.Errorf("账户 %s: %w", id, err)
	}
	runtimePath := filepath.Join(directory, runtimeStateName)
	runtimeState, err := readRuntime(runtimePath)
	if err != nil {
		return nil, fmt.Errorf("账户 %s: %w", id, err)
	}
	catalog, buildModels := readCatalogCache(filepath.Join(directory, catalogCacheName), runtimeState)
	return &Account{
		ID:            id,
		Directory:     directory,
		ConfigPath:    configPath,
		StoragePath:   storagePath,
		RuntimePath:   runtimePath,
		Config:        accountConfig,
		StorageState:  state,
		Models:        cloneModels(catalog.models),
		BenefitTier:   runtimeState.BenefitTier,
		State:         initialAccountState(accountConfig, state),
		runtime:       runtimeState,
		buildModels:   buildModels,
		cachedCatalog: catalog,
	}, nil
}

// accountCatalogCache 是 catalog.json 保存的账户最近一次同步的 ListModels 响应与 Build 模型目录
type accountCatalogCache struct {
	ListModels  json.RawMessage `json:"list_models"`
	BuildModels []Model         `json:"build_models,omitempty"`
}

// readCatalogCache 读取 catalog.json 并解析其中的 ListModels 响应，内容与 runtime-state 的目录指纹不一致或无法读取时返回空目录
func readCatalogCache(filePath string, runtimeState accountRuntimeState) (modelCatalog, []Model) {
	data, err := os.ReadFile(filePath)
	if err != nil {
		return modelCatalog{}, nil
	}
	var cache accountCatalogCache
	if err := json.Unmarshal(data, &cache); err != nil || len(cache.ListModels) == 0 {
		return modelCatalog{}, nil
	}
	catalog, err := parseModelCatalog(bytes.NewReader(cache.ListModels))
	if err != nil {
		return modelCatalog{}, nil
	}
	fingerprint, err := accountCatalogFingerprint(runtimeState.BenefitTier, catalog.models)
	if err != nil || fingerprint != runtimeState.CatalogFingerprint {
		return modelCatalog{}, nil
	}
	return catalog, cache.BuildModels
}

// takeCachedCatalogs 取出各账户载入时从 catalog.json 恢复的 ListModels 目录
func (p *AccountPool) takeCachedCatalogs() map[string]modelCatalog {
	p.mu.Lock()
	defer p.mu.Unlock()
	catalogs := make(map[string]modelCatalog)
	for _, account := range p.accounts {
		if len(account.cachedCatalog.models) > 0 {
			catalogs[account.ID] = account.cachedCatalog
			account.cachedCatalog = modelCatalog{}
		}
	}
	return catalogs
}

// SaveCatalogCache 把账户的 ListModels 响应与当前 Build 模型目录写入账户目录的 catalog.json
func (p *AccountPool) SaveCatalogCache(accountID string, source json.RawMessage) error {
	p.mu.Lock()
	account := p.byID[strings.TrimSpace(accountID)]
	if account == nil {
		p.mu.Unlock()
		return ErrAccountNotFound
	}
	if account.Directory == "" {
		p.mu.Unlock()
		return nil
	}
	filePath := filepath.Join(account.Directory, catalogCacheName)
	data, err := json.Marshal(accountCatalogCache{ListModels: source, BuildModels: account.buildModels})
	p.mu.Unlock()
	if err != nil {
		return fmt.Errorf("编码 %s: %w", catalogCacheName, err)
	}
	if existing, err := os.ReadFile(filePath); err == nil && bytes.Equal(existing, data) {
		return nil
	}
	return atomicWriteFile(filePath, data, 0o600)
}

// cachedBenefitTiers 返回已载入模型目录的账户的权益等级
func (p *AccountPool) cachedBenefitTiers() map[string]BenefitTier {
	p.mu.Lock()
	defer p.mu.Unlock()
	tiers := make(map[string]BenefitTier)
	for _, account := range p.accounts {
		if len(account.Models) > 0 {
			tiers[account.ID] = account.BenefitTier
		}
	}
	return tiers
}

func initialAccountState(accountConfig AccountConfig, state StorageState) AccountState {
	if !accountConfig.Enabled {
		return AccountDisabled
	}
	now := time.Now()
	for _, item := range signatureCookies {
		if _, ok := state.CookieValue(item.name, aiStudioOrigin+"/", now); !ok {
			return AccountAuthRequired
		}
	}
	return AccountReady
}

func readAccountConfig(filePath string) (AccountConfig, error) {
	file, err := os.Open(filePath)
	if err != nil {
		return AccountConfig{}, fmt.Errorf("读取 %s: %w", accountConfigName, err)
	}
	defer file.Close()
	decoder := json.NewDecoder(file)
	decoder.DisallowUnknownFields()
	var value AccountConfig
	if err := decoder.Decode(&value); err != nil {
		return AccountConfig{}, fmt.Errorf("解析 %s: %w", accountConfigName, err)
	}
	if err := ensureJSONEnd(decoder); err != nil {
		return AccountConfig{}, fmt.Errorf("解析 %s: %w", accountConfigName, err)
	}
	if err := value.Validate(); err != nil {
		return AccountConfig{}, err
	}
	return value, nil
}

func writeAccountConfig(filePath string, value AccountConfig) error {
	if err := value.Validate(); err != nil {
		return err
	}
	data, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return fmt.Errorf("编码 %s: %w", accountConfigName, err)
	}
	return atomicWriteFile(filePath, append(data, '\n'), 0o600)
}

func readRuntime(filePath string) (accountRuntimeState, error) {
	data, err := readRuntimeData(filePath)
	if err != nil {
		return accountRuntimeState{}, err
	}
	return decodeRuntime(data)
}

// readRuntimeData 读取 runtime-state.json 的内容，文件不存在时返回 nil
func readRuntimeData(filePath string) ([]byte, error) {
	data, err := os.ReadFile(filePath)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("读取 %s: %w", runtimeStateName, err)
	}
	return data, nil
}

// decodeRuntime 解析 runtime-state.json 的内容，nil 为空运行态
func decodeRuntime(data []byte) (accountRuntimeState, error) {
	value := accountRuntimeState{
		Cooldowns:   make(map[string]CooldownState),
		Resources:   make(map[string]ResourceBinding),
		ModelAccess: make(map[string]ModelAccess),
	}
	if data == nil {
		return value, nil
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&value); err != nil {
		return accountRuntimeState{}, fmt.Errorf("解析 %s: %w", runtimeStateName, err)
	}
	if err := ensureJSONEnd(decoder); err != nil {
		return accountRuntimeState{}, fmt.Errorf("解析 %s: %w", runtimeStateName, err)
	}
	return value, nil
}

// writeRuntime 原子写入 runtime-state.json 并返回写入的内容，路径为空时不写
func writeRuntime(filePath string, value accountRuntimeState) ([]byte, error) {
	if filePath == "" {
		return nil, nil
	}
	data, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("编码 %s: %w", runtimeStateName, err)
	}
	data = append(data, '\n')
	return data, atomicWriteFile(filePath, data, 0o600)
}

type runtimeStateMutation func(*Account, *accountRuntimeState) (bool, func(*Account), error)

func (p *AccountPool) updateRuntime(accountID string, mutate runtimeStateMutation) (changed bool, resultErr error) {
	return p.updateRuntimeContext(context.Background(), accountID, mutate)
}

func (p *AccountPool) updateRuntimeContext(
	ctx context.Context,
	accountID string,
	mutate runtimeStateMutation,
) (changed bool, resultErr error) {
	p.mu.Lock()
	account := p.byID[strings.TrimSpace(accountID)]
	p.mu.Unlock()
	if account == nil {
		return false, fmt.Errorf("账户不存在: %s", accountID)
	}

	account.runtimeMu.Lock()
	defer account.runtimeMu.Unlock()
	p.mu.Lock()
	currentAccount := p.byID[account.ID]
	p.mu.Unlock()
	if currentAccount != account {
		return false, fmt.Errorf("账户不存在: %s", account.ID)
	}
	runtimeLock, err := lockRuntimeState(ctx, account)
	if err != nil {
		return false, err
	}
	defer func() {
		if runtimeLock != nil {
			resultErr = errors.Join(resultErr, runtimeLock.Unlock())
		}
	}()
	if err := validatePersistentAccountFiles(account); err != nil {
		if errors.Is(err, ErrAccountNotFound) {
			p.markStaleAccountUnavailable(account)
		}
		return false, err
	}

	current := cloneRuntime(account.runtime)
	var data []byte
	if account.RuntimePath == "" {
		current.BenefitTier = account.BenefitTier
	} else {
		if data, err = readRuntimeData(account.RuntimePath); err != nil {
			return false, err
		}
		if current, err = decodeRuntime(data); err != nil {
			return false, err
		}
	}
	p.mu.Lock()
	if p.byID[account.ID] != account {
		p.mu.Unlock()
		return false, fmt.Errorf("账户不存在: %s", account.ID)
	}
	refreshed, err := p.syncAccountRuntimeLocked(account, current)
	if err != nil {
		p.mu.Unlock()
		return false, err
	}
	working := cloneRuntime(current)
	changed, apply, err := mutate(account, &working)
	p.mu.Unlock()
	if err != nil {
		return false, err
	}
	if changed {
		if data, err = writeRuntime(account.RuntimePath, working); err != nil {
			return false, err
		}
	}

	p.mu.Lock()
	defer p.mu.Unlock()
	if p.byID[account.ID] != account {
		return false, fmt.Errorf("账户不存在: %s", account.ID)
	}
	synced, err := p.syncAccountRuntimeLocked(account, working)
	if err != nil {
		return false, err
	}
	account.runtimeData = data
	if apply != nil {
		apply(account)
	}
	if changed || refreshed || synced || apply != nil {
		p.notifyLocked()
	}
	return changed, nil
}

func (p *AccountPool) syncAccountRuntimeLocked(account *Account, runtimeState accountRuntimeState) (bool, error) {
	for resourceID := range runtimeState.Resources {
		if owner, exists := p.resources[resourceID]; exists && owner != account.ID {
			return false, fmt.Errorf("资源 %s 已绑定账户 %s", resourceID, owner)
		}
	}
	changed := account.BenefitTier != runtimeState.BenefitTier || !reflect.DeepEqual(account.runtime, runtimeState)
	if !changed {
		for resourceID := range runtimeState.Resources {
			if p.resources[resourceID] != account.ID {
				changed = true
				break
			}
		}
	}
	if !changed {
		for resourceID, owner := range p.resources {
			if owner == account.ID {
				if _, exists := runtimeState.Resources[resourceID]; !exists {
					changed = true
					break
				}
			}
		}
	}
	if !changed {
		return false, nil
	}
	for resourceID, owner := range p.resources {
		if owner == account.ID {
			delete(p.resources, resourceID)
		}
	}
	catalogChanged := account.runtime.BenefitTier != runtimeState.BenefitTier ||
		account.runtime.CatalogFingerprint != runtimeState.CatalogFingerprint
	account.runtime = cloneRuntime(runtimeState)
	account.BenefitTier = runtimeState.BenefitTier
	for resourceID := range runtimeState.Resources {
		p.resources[resourceID] = account.ID
	}
	if catalogChanged {
		account.modelAccessGeneration++
	}
	return true, nil
}

func lockRuntimeState(ctx context.Context, account *Account) (*flock.Flock, error) {
	if account.RuntimePath == "" {
		return nil, nil
	}
	leaseDirectory := filepath.Join(filepath.Dir(account.Directory), ".leases")
	if err := os.MkdirAll(leaseDirectory, 0o700); err != nil {
		return nil, fmt.Errorf("创建账户状态锁目录: %w", err)
	}
	lock := flock.New(filepath.Join(leaseDirectory, filepath.Base(account.Directory)+".runtime.lock"))
	lockCtx, cancel := context.WithTimeout(ctx, runtimeLockLimit)
	defer cancel()
	_, err := lock.TryLockContext(lockCtx, runtimeLockPoll)
	if err != nil {
		return nil, fmt.Errorf("锁定账户运行状态: %w", err)
	}
	return lock, nil
}

func validatePersistentAccountFiles(account *Account) error {
	if account.ConfigPath == "" {
		return nil
	}
	for _, filePath := range []string{account.ConfigPath, account.StoragePath} {
		info, err := os.Stat(filePath)
		if err != nil {
			if os.IsNotExist(err) {
				return fmt.Errorf("%w: %s", ErrAccountNotFound, account.ID)
			}
			return fmt.Errorf("读取账户持久文件: %w", err)
		}
		if !info.Mode().IsRegular() {
			return fmt.Errorf("%w: %s", ErrAccountNotFound, account.ID)
		}
	}
	return nil
}

func (p *AccountPool) refreshAccountRuntime(ctx context.Context, account *Account) (resultErr error) {
	account.runtimeMu.Lock()
	defer account.runtimeMu.Unlock()
	p.mu.Lock()
	currentAccount := p.byID[account.ID]
	p.mu.Unlock()
	if currentAccount != account {
		return fmt.Errorf("%w: %s", ErrAccountNotFound, account.ID)
	}
	runtimeLock, err := lockRuntimeState(ctx, account)
	if err != nil {
		return err
	}
	defer func() {
		if runtimeLock != nil {
			resultErr = errors.Join(resultErr, runtimeLock.Unlock())
		}
	}()
	if err := validatePersistentAccountFiles(account); err != nil {
		return err
	}
	var current accountRuntimeState
	var data []byte
	if account.RuntimePath == "" {
		current = cloneRuntime(account.runtime)
		current.BenefitTier = account.BenefitTier
	} else {
		if data, err = readRuntimeData(account.RuntimePath); err != nil {
			return err
		}
		if account.runtimeData != nil && bytes.Equal(data, account.runtimeData) {
			return nil
		}
		if current, err = decodeRuntime(data); err != nil {
			return err
		}
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.byID[account.ID] != account {
		return fmt.Errorf("%w: %s", ErrAccountNotFound, account.ID)
	}
	changed, err := p.syncAccountRuntimeLocked(account, current)
	if err == nil {
		account.runtimeData = data
	}
	if changed {
		p.notifyLocked()
	}
	return err
}

// accountRuntimeRefreshResult 保存单账户运行态刷新结果
type accountRuntimeRefreshResult struct {
	account *Account
	err     error
}

// refreshAccountRuntimes 并发刷新独立账户运行态
func (p *AccountPool) refreshAccountRuntimes(ctx context.Context, accounts []*Account) []accountRuntimeRefreshResult {
	results := make([]accountRuntimeRefreshResult, len(accounts))
	var refreshes sync.WaitGroup
	for index, account := range accounts {
		refreshes.Add(1)
		go func(index int, account *Account) {
			defer refreshes.Done()
			results[index] = accountRuntimeRefreshResult{account: account, err: p.refreshAccountRuntime(ctx, account)}
		}(index, account)
	}
	refreshes.Wait()
	return results
}

func (p *AccountPool) refreshResource(ctx context.Context, resourceID string) error {
	resourceID = strings.TrimSpace(resourceID)
	p.mu.Lock()
	ownerID, exists := p.resources[resourceID]
	owner := p.byID[ownerID]
	p.mu.Unlock()
	if exists && owner != nil {
		if err := p.refreshAccountRuntime(ctx, owner); err != nil {
			if errors.Is(err, ErrAccountNotFound) {
				p.markStaleAccountUnavailable(owner)
				return fmt.Errorf("%w: %s", ErrResourceNotFound, resourceID)
			}
			return err
		}
		return nil
	}
	refreshErr := p.refreshAllRuntimes(ctx)
	p.mu.Lock()
	_, found := p.resources[resourceID]
	p.mu.Unlock()
	if found {
		return nil
	}
	return refreshErr
}

func (p *AccountPool) refreshSelectionRuntimes(ctx context.Context, selection AccountSelection) error {
	allowed := make(map[string]struct{}, len(selection.AllowedAccountIDs))
	for _, accountID := range selection.AllowedAccountIDs {
		if accountID = strings.TrimSpace(accountID); accountID != "" {
			allowed[accountID] = struct{}{}
		}
	}
	requestedAccountID := strings.TrimSpace(selection.AccountID)
	modelID := strings.TrimPrefix(strings.TrimSpace(selection.ModelID), "models/")
	p.mu.Lock()
	accounts := make([]*Account, 0, len(p.accounts))
	for _, account := range p.accounts {
		if !account.Config.Enabled || account.State != AccountReady {
			continue
		}
		if requestedAccountID != "" && account.ID != requestedAccountID {
			continue
		}
		if selection.AllowedAccountIDs != nil {
			if _, exists := allowed[account.ID]; !exists {
				continue
			}
		}
		if modelID != "" {
			supported := false
			for _, model := range account.Models {
				if modelMatchesID(model, modelID) &&
					(strings.TrimSpace(selection.Method) == "" || hasMethod(model, selection.Method)) {
					supported = true
					break
				}
			}
			if !supported {
				continue
			}
		}
		accounts = append(accounts, account)
	}
	p.mu.Unlock()
	var failures []error
	for _, result := range p.refreshAccountRuntimes(ctx, accounts) {
		if result.err == nil {
			continue
		}
		if errors.Is(result.err, ErrAccountNotFound) {
			p.markStaleAccountUnavailable(result.account)
			continue
		}
		failures = append(failures, result.err)
	}
	return errors.Join(failures...)
}

func cloneRuntime(value accountRuntimeState) accountRuntimeState {
	result := accountRuntimeState{
		Cooldowns:          make(map[string]CooldownState, len(value.Cooldowns)),
		Resources:          make(map[string]ResourceBinding, len(value.Resources)),
		ModelAccess:        make(map[string]ModelAccess, len(value.ModelAccess)),
		BenefitTier:        value.BenefitTier,
		CatalogFingerprint: value.CatalogFingerprint,
		Proxy:              value.Proxy,
	}
	for key, cooldown := range value.Cooldowns {
		result.Cooldowns[key] = cooldown
	}
	for key, binding := range value.Resources {
		if binding.Video != nil {
			metadata := *binding.Video
			binding.Video = &metadata
		}
		result.Resources[key] = binding
	}
	for key, access := range value.ModelAccess {
		result.ModelAccess[key] = access
	}
	return result
}

func accountCatalogFingerprint(tier BenefitTier, models []Model) (string, error) {
	catalog := cloneModels(models)
	sort.Slice(catalog, func(left int, right int) bool {
		return catalog[left].ID < catalog[right].ID
	})
	data, err := json.Marshal(struct {
		Tier   BenefitTier `json:"tier"`
		Models []Model     `json:"models"`
	}{Tier: tier, Models: catalog})
	if err != nil {
		return "", fmt.Errorf("编码账户模型目录指纹: %w", err)
	}
	return fmt.Sprintf("%x", sha256.Sum256(data)), nil
}

func accountCooldown(account *Account, modelID string, now time.Time) (CooldownState, bool) {
	var selected CooldownState
	for _, key := range []string{globalCooldownKey, modelID} {
		if key == "" {
			continue
		}
		cooldown, exists := account.runtime.Cooldowns[key]
		if !exists || !cooldown.Active(now) {
			continue
		}
		if selected.Until.IsZero() || cooldown.Until.After(selected.Until) {
			selected = cooldown
		}
	}
	return selected, !selected.Until.IsZero()
}

func selectionAccessScope(selection AccountSelection) string {
	if scope := strings.TrimSpace(selection.ModelAccessScope); scope != "" {
		return scope
	}
	return strings.TrimPrefix(strings.TrimSpace(selection.ModelID), "models/")
}

func acquireAccountFileLease(storagePath string) (*flock.Flock, error) {
	if storagePath == "" {
		return nil, nil
	}
	accountDirectory := filepath.Dir(storagePath)
	leaseDirectory := filepath.Join(filepath.Dir(accountDirectory), ".leases")
	if err := os.MkdirAll(leaseDirectory, 0o700); err != nil {
		return nil, fmt.Errorf("创建账户租约目录: %w", err)
	}
	leaseLock := flock.New(filepath.Join(leaseDirectory, filepath.Base(accountDirectory)+".lock"))
	locked, err := leaseLock.TryLock()
	if err != nil {
		return nil, fmt.Errorf("锁定账户租约: %w", err)
	}
	if !locked {
		return nil, ErrAccountLeased
	}
	return leaseLock, nil
}

func acquireAccountPublishLease(account *Account, validate bool) (*AccountPublishLease, error) {
	requestLock, err := acquireAccountFileLease(account.StoragePath)
	if errors.Is(err, ErrAccountLeased) {
		return nil, fmt.Errorf("%w: %s", ErrAccountLeased, account.ID)
	}
	if err != nil {
		return nil, err
	}
	account.runtimeMu.Lock()
	runtimeLock, err := lockRuntimeState(context.Background(), account)
	if err != nil {
		account.runtimeMu.Unlock()
		if requestLock != nil {
			_ = requestLock.Unlock()
		}
		return nil, err
	}
	if validate {
		if err := validatePersistentAccountFiles(account); err != nil {
			if runtimeLock != nil {
				_ = runtimeLock.Unlock()
			}
			account.runtimeMu.Unlock()
			if requestLock != nil {
				_ = requestLock.Unlock()
			}
			return nil, err
		}
	}
	account.storageMu.Lock()
	account.persistenceLocked = true
	account.storageMu.Unlock()
	return &AccountPublishLease{account: account, requestLock: requestLock, runtimeLock: runtimeLock}, nil
}

// Release 结束新账户运行时发布窗口
func (lease *AccountPublishLease) Release() error {
	lease.once.Do(func() {
		lease.account.storageMu.Lock()
		lease.account.persistenceLocked = false
		lease.account.storageMu.Unlock()
		if lease.runtimeLock != nil {
			lease.err = lease.runtimeLock.Unlock()
		}
		lease.account.runtimeMu.Unlock()
		if lease.requestLock != nil {
			lease.err = errors.Join(lease.err, lease.requestLock.Unlock())
		}
	})
	return lease.err
}

// AcquireAccountRuntimeLease 锁定当前用户下的账户 WAA runtime
func AcquireAccountRuntimeLease(accountID string) (*AccountRuntimeLease, error) {
	accountID, err := normalizeAccountEmail(accountID)
	if err != nil {
		return nil, err
	}
	cacheRoot, err := userCacheRoot()
	if err != nil {
		return nil, fmt.Errorf("读取用户缓存目录: %w", err)
	}
	directory := filepath.Join(cacheRoot, "AIStudio2API", "runtime-leases")
	if err := os.MkdirAll(directory, 0o700); err != nil {
		return nil, fmt.Errorf("创建 WAA runtime 租约目录: %w", err)
	}
	lock := flock.New(filepath.Join(directory, accountID+".lock"))
	locked, err := lock.TryLock()
	if err != nil {
		return nil, fmt.Errorf("锁定账户 WAA runtime: %w", err)
	}
	if !locked {
		return nil, fmt.Errorf("%w: %s 已由另一个 AIStudio2API runtime 使用", ErrAccountLeased, accountID)
	}
	return &AccountRuntimeLease{lock: lock}, nil
}

// userCacheRoot 返回当前用户的缓存目录，目录环境变量缺失时按系统账户主目录推导同一位置
func userCacheRoot() (string, error) {
	if directory, err := os.UserCacheDir(); err == nil {
		return directory, nil
	}
	current, err := user.Current()
	if err != nil {
		return "", err
	}
	if !filepath.IsAbs(current.HomeDir) {
		return "", fmt.Errorf("当前用户主目录无效: %q", current.HomeDir)
	}
	switch runtime.GOOS {
	case "windows":
		return filepath.Join(current.HomeDir, "AppData", "Local"), nil
	case "darwin", "ios":
		return filepath.Join(current.HomeDir, "Library", "Caches"), nil
	default:
		return filepath.Join(current.HomeDir, ".cache"), nil
	}
}

// Release 释放账户 WAA runtime 锁
func (lease *AccountRuntimeLease) Release() error {
	lease.once.Do(func() {
		lease.err = lease.lock.Unlock()
	})
	return lease.err
}

func ensureJSONEnd(decoder *json.Decoder) error {
	var trailing any
	err := decoder.Decode(&trailing)
	if errors.Is(err, io.EOF) {
		return nil
	}
	if err == nil {
		return fmt.Errorf("文件包含多个 JSON 值")
	}
	return err
}

func accountEmailID(accountConfig AccountConfig, state StorageState) (string, error) {
	candidate := strings.TrimSpace(accountConfig.Label)
	if extension, exists, err := state.AuthExtension(); err != nil {
		return "", err
	} else if exists && strings.TrimSpace(extension.Source.Email) != "" {
		candidate = strings.TrimSpace(extension.Source.Email)
	}
	return normalizeAccountEmail(candidate)
}

func normalizeAccountEmail(candidate string) (string, error) {
	candidate = strings.TrimSpace(candidate)
	address, err := mail.ParseAddress(candidate)
	if err != nil || !strings.EqualFold(strings.TrimSpace(address.Address), candidate) {
		return "", fmt.Errorf("账户必须填写 Google 邮箱")
	}
	id := strings.ToLower(strings.TrimSpace(address.Address))
	if id == "." || id == ".." || strings.ContainsAny(id, `<>:"/\|?*`) {
		return "", fmt.Errorf("账户邮箱不能用作目录名: %s", id)
	}
	return id, nil
}

func canonicalAccountModelID(account *Account, modelID string) string {
	for _, model := range account.Models {
		if modelMatchesID(model, modelID) {
			return model.ID
		}
	}
	return modelID
}

func modelAccessState(account *Account, modelID string) ModelAccessState {
	return account.runtime.ModelAccess[canonicalAccountModelID(account, modelID)].State
}

func modelCatalogEntryChanged(current []Model, next []Model, modelID string) bool {
	currentModel, currentFound := findCatalogModel(current, modelID)
	nextModel, nextFound := findCatalogModel(next, modelID)
	return currentFound != nextFound || !reflect.DeepEqual(currentModel, nextModel)
}

func modelAccessCatalogModelID(current []Model, next []Model, modelAccessScope string) string {
	if _, found := findCatalogModel(current, modelAccessScope); found {
		return modelAccessScope
	}
	if _, found := findCatalogModel(next, modelAccessScope); found {
		return modelAccessScope
	}
	separator := strings.LastIndex(modelAccessScope, ":")
	if separator < 0 || separator+1 >= len(modelAccessScope) {
		return modelAccessScope
	}
	modelID := modelAccessScope[separator+1:]
	if _, found := findCatalogModel(current, modelID); found {
		return modelID
	}
	if _, found := findCatalogModel(next, modelID); found {
		return modelID
	}
	return modelAccessScope
}

func catalogEntriesChanged(current []Model, next []Model) bool {
	for _, model := range current {
		if modelCatalogEntryChanged(current, next, model.ID) {
			return true
		}
	}
	for _, model := range next {
		if modelCatalogEntryChanged(current, next, model.ID) {
			return true
		}
	}
	return false
}

func findCatalogModel(models []Model, modelID string) (Model, bool) {
	for _, model := range models {
		if modelMatchesID(model, modelID) {
			return model, true
		}
	}
	return Model{}, false
}

func benefitTierPriority(tier BenefitTier) int {
	switch tier {
	case BenefitTierFree:
		return 0
	case BenefitTierPlus:
		return 1
	case BenefitTierPro:
		return 2
	case BenefitTierUltra:
		return 3
	default:
		return 4
	}
}

func cloneCooldowns(cooldowns map[string]CooldownState) map[string]CooldownState {
	if len(cooldowns) == 0 {
		return nil
	}
	result := make(map[string]CooldownState, len(cooldowns))
	for key, cooldown := range cooldowns {
		result[key] = cooldown
	}
	return result
}

func fileExists(filePath string) bool {
	info, err := os.Stat(filePath)
	return err == nil && !info.IsDir()
}

func timePointer(value time.Time) *time.Time {
	copy := value
	return &copy
}
