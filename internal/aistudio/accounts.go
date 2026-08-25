package aistudio

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	appconfig "github.com/Mag1cFall/AIStudio2API/internal/config"
	"github.com/gofrs/flock"
)

const (
	accountConfigName  = "account.json"
	storageStateName   = "storage-state.json"
	runtimeStateName   = "runtime-state.json"
	globalCooldownKey  = "*"
	defaultAccountLang = "en-US"
	defaultAccountZone = "UTC"
	externalLeasePoll  = 100 * time.Millisecond
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
	// ErrAccountNotFound 表示稳定账户 ID 不存在
	ErrAccountNotFound = errors.New("账户不存在")
	// ErrAccountLeased 表示账户当前存在进程内或跨进程租约
	ErrAccountLeased = errors.New("账户正在使用")
	// ErrResourceNotFound 表示资源没有创建账户映射
	ErrResourceNotFound = errors.New("资源账户映射不存在")
	errAccountLeaseBusy = ErrAccountLeased
)

// AccountConfig 表示账户目录中的固定最小配置
type AccountConfig struct {
	Label    string `json:"label"`
	Enabled  bool   `json:"enabled"`
	Proxy    string `json:"proxy"`
	Locale   string `json:"locale"`
	Timezone string `json:"timezone"`
}

// ResourceBinding 记录上游资源的创建账户
type ResourceBinding struct {
	Kind      string    `json:"kind,omitempty"`
	CreatedAt time.Time `json:"created_at"`
}

type accountRuntimeState struct {
	Cooldowns map[string]CooldownState   `json:"cooldowns,omitempty"`
	Resources map[string]ResourceBinding `json:"resources,omitempty"`
}

// Account 表示一个稳定目录对应的 AI Studio 账户
type Account struct {
	ID             string        `json:"id"`
	Directory      string        `json:"-"`
	ConfigPath     string        `json:"-"`
	StoragePath    string        `json:"-"`
	RuntimePath    string        `json:"-"`
	Config         AccountConfig `json:"config"`
	StorageState   StorageState  `json:"-"`
	Models         []Model       `json:"models,omitempty"`
	State          AccountState  `json:"state"`
	LastUsed       time.Time     `json:"last_used,omitempty"`
	runtime        accountRuntimeState
	active         int
	exclusive      bool
	leaseLock      *flock.Flock
	leasePath      string
	storageMu      sync.Mutex
	authGeneration uint64
	stateMessage   string
	initializedAt  time.Time
}

// AccountStatus 表示管理界面使用的脱敏账户状态
type AccountStatus struct {
	ID        string                   `json:"id"`
	Label     string                   `json:"label"`
	State     AccountState             `json:"state"`
	Enabled   bool                     `json:"enabled"`
	Proxy     string                   `json:"proxy"`
	Locale    string                   `json:"locale"`
	Timezone  string                   `json:"timezone"`
	Models    []string                 `json:"models"`
	Cooldowns map[string]CooldownState `json:"-"`
	LastUsed  *time.Time               `json:"last_used,omitempty"`
	Message   string                   `json:"message,omitempty"`
}

// AccountSelection 描述账户调度所需的能力或粘性条件
type AccountSelection struct {
	ModelID           string
	Method            string
	AccountID         string
	ResourceID        string
	AllowedAccountIDs []string
}

// BootstrapModelID 是 WAA 初始化使用的实时模型
const BootstrapModelID = "gemini-flash-latest"

// AccountCandidateGroups 表示 warm 与 standby 账户的实时可调度状态
type AccountCandidateGroups struct {
	WarmReady        []string
	WarmBusy         []string
	StandbyReady     []string
	StandbyBusy      []string
	EarliestCooldown time.Time
	Eligible         bool
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
	next                  int
	changed               chan struct{}
}

// AccountLease 表示一个账户请求槽位
type AccountLease struct {
	pool           *AccountPool
	account        *Account
	exclusive      bool
	authGeneration uint64
	operation      sync.Mutex
	released       bool
	once           sync.Once
	err            error
}

// DefaultAccountConfig 返回新账户的最小配置
func DefaultAccountConfig(label string) AccountConfig {
	return AccountConfig{
		Label:    strings.TrimSpace(label),
		Enabled:  true,
		Locale:   defaultAccountLang,
		Timezone: defaultAccountZone,
	}
}

// NewAccountStore 创建账户目录存储
func NewAccountStore(paths ...string) *AccountStore {
	if len(paths) == 0 {
		paths = []string{"auth"}
	}
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
	if s == nil || len(s.paths) == 0 {
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

// Create 创建具有不可变随机 ID 的账户目录
func (s *AccountStore) Create(accountConfig AccountConfig, state StorageState) (*Account, error) {
	if s == nil || len(s.paths) != 1 {
		return nil, fmt.Errorf("创建账户需要一个账户根目录")
	}
	if err := accountConfig.Validate(); err != nil {
		return nil, err
	}
	if err := state.Validate(); err != nil {
		return nil, err
	}
	root, err := filepath.Abs(s.paths[0])
	if err != nil {
		return nil, fmt.Errorf("解析账户根目录: %w", err)
	}
	if err := os.MkdirAll(root, 0o755); err != nil {
		return nil, fmt.Errorf("创建账户根目录: %w", err)
	}
	id, err := randomAccountID()
	if err != nil {
		return nil, err
	}
	temporary, err := os.MkdirTemp(root, ".account-*.tmp")
	if err != nil {
		return nil, fmt.Errorf("创建临时账户目录: %w", err)
	}
	defer os.RemoveAll(temporary)
	if err := writeAccountConfig(filepath.Join(temporary, accountConfigName), accountConfig); err != nil {
		return nil, err
	}
	if err := WriteStorageState(filepath.Join(temporary, storageStateName), state); err != nil {
		return nil, err
	}
	directory := filepath.Join(root, id)
	if err := os.Rename(temporary, directory); err != nil {
		return nil, fmt.Errorf("保存账户目录: %w", err)
	}
	return loadAccount(directory)
}

// Delete 删除属于当前存储的稳定账户目录
func (s *AccountStore) Delete(account *Account) error {
	if account == nil || strings.TrimSpace(account.ID) == "" || strings.TrimSpace(account.Directory) == "" {
		return fmt.Errorf("账户未初始化")
	}
	directory, err := filepath.Abs(account.Directory)
	if err != nil {
		return fmt.Errorf("解析账户目录: %w", err)
	}
	if filepath.Base(directory) != account.ID {
		return fmt.Errorf("账户目录与稳定 ID 不匹配")
	}
	owned, err := s.ownsDirectory(directory)
	if err != nil {
		return err
	}
	if !owned {
		return fmt.Errorf("账户目录不属于当前 AccountStore: %s", directory)
	}
	info, err := os.Stat(directory)
	if err != nil {
		return fmt.Errorf("读取账户目录: %w", err)
	}
	if !info.IsDir() {
		return fmt.Errorf("账户路径不是目录: %s", directory)
	}
	leaseLock, _, err := acquireAccountFileLease(account.StoragePath)
	if errors.Is(err, errAccountLeaseBusy) {
		return fmt.Errorf("%w: %s", ErrAccountLeased, account.ID)
	}
	if err != nil {
		return err
	}
	if err := os.RemoveAll(directory); err != nil {
		if leaseLock != nil {
			_ = leaseLock.Unlock()
		}
		return fmt.Errorf("删除账户目录: %w", err)
	}
	if leaseLock != nil {
		if err := leaseLock.Unlock(); err != nil {
			return fmt.Errorf("释放账户删除租约: %w", err)
		}
	}
	return nil
}

func (s *AccountStore) ownsDirectory(directory string) (bool, error) {
	if s == nil || len(s.paths) == 0 {
		return false, fmt.Errorf("账户路径为空")
	}
	for _, source := range s.paths {
		absolute, err := filepath.Abs(source)
		if err != nil {
			return false, fmt.Errorf("解析账户路径 %q: %w", source, err)
		}
		info, err := os.Stat(absolute)
		if os.IsNotExist(err) {
			continue
		}
		if err != nil {
			return false, fmt.Errorf("读取账户路径 %q: %w", source, err)
		}
		root := absolute
		if !info.IsDir() {
			root = filepath.Dir(absolute)
		}
		relative, err := filepath.Rel(root, directory)
		if err != nil {
			return false, fmt.Errorf("比较账户路径 %q: %w", source, err)
		}
		if relative == "." || relative != ".." && !strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
			return true, nil
		}
	}
	return false, nil
}

// Validate 校验账户固定配置
func (c AccountConfig) Validate() error {
	if strings.TrimSpace(c.Label) == "" {
		return fmt.Errorf("账户 label 不能为空")
	}
	if strings.TrimSpace(c.Locale) == "" {
		return fmt.Errorf("账户 locale 不能为空")
	}
	if strings.TrimSpace(c.Timezone) == "" {
		return fmt.Errorf("账户 timezone 不能为空")
	}
	if err := appconfig.ValidateProxy(c.Proxy); err != nil {
		return fmt.Errorf("账户 proxy 无效: %w", err)
	}
	return nil
}

// EffectiveProxy 返回账户固定代理或全局代理
func (a *Account) EffectiveProxy(globalProxy string) string {
	if a != nil && strings.TrimSpace(a.Config.Proxy) != "" {
		return strings.TrimSpace(a.Config.Proxy)
	}
	return strings.TrimSpace(globalProxy)
}

// AcceptLanguage 返回账户 locale 对应的请求语言头
func (a *Account) AcceptLanguage() string {
	if a == nil {
		return ""
	}
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
		if modelMatchesID(model, modelID) {
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
		for _, candidate := range model.Methods {
			if candidate == method {
				return true
			}
		}
	}
	return false
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
	}
	for _, account := range p.accounts {
		if account == nil {
			continue
		}
		if account.runtime.Cooldowns == nil {
			account.runtime.Cooldowns = make(map[string]CooldownState)
		}
		if account.runtime.Resources == nil {
			account.runtime.Resources = make(map[string]ResourceBinding)
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
	if p == nil {
		return nil, ErrAccountNotFound
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	account := p.byID[strings.TrimSpace(accountID)]
	if account == nil {
		return nil, fmt.Errorf("%w: %s", ErrAccountNotFound, accountID)
	}
	return account, nil
}

// Add 将新账户加入当前调度池
func (p *AccountPool) Add(account *Account) error {
	if p == nil || account == nil || strings.TrimSpace(account.ID) == "" {
		return fmt.Errorf("账户未初始化")
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
	if p == nil {
		return nil, ErrAccountNotFound
	}
	p.mu.Lock()
	accountID = strings.TrimSpace(accountID)
	account := p.byID[accountID]
	if account == nil {
		p.mu.Unlock()
		return nil, fmt.Errorf("%w: %s", ErrAccountNotFound, accountID)
	}
	if account.exclusive || account.active > 0 {
		p.mu.Unlock()
		return nil, fmt.Errorf("%w: %s", ErrAccountLeased, accountID)
	}
	if deleteDirectory == nil {
		p.mu.Unlock()
		return nil, fmt.Errorf("账户目录删除函数为空")
	}
	account.exclusive = true
	p.notifyLocked()
	p.mu.Unlock()
	if err := deleteDirectory(account); err != nil {
		p.mu.Lock()
		account.exclusive = false
		p.notifyLocked()
		p.mu.Unlock()
		return nil, err
	}
	p.mu.Lock()
	for resourceID, owner := range p.resources {
		if owner == accountID {
			delete(p.resources, resourceID)
		}
	}
	delete(p.byID, accountID)
	for index, candidate := range p.accounts {
		if candidate != nil && candidate.ID == accountID {
			p.accounts = append(p.accounts[:index], p.accounts[index+1:]...)
			break
		}
	}
	if p.next >= len(p.accounts) {
		p.next = 0
	}
	p.notifyLocked()
	p.mu.Unlock()
	return account, nil
}

// Acquire 为模型轮询获取一个账户槽位
func (p *AccountPool) Acquire(ctx context.Context, model string) (*AccountLease, error) {
	return p.AcquireFor(ctx, AccountSelection{ModelID: model})
}

// AcquireAccount 为管理操作获取不受调度状态限制的指定账户租约
func (p *AccountPool) AcquireAccount(ctx context.Context, accountID string) (*AccountLease, error) {
	if p == nil {
		return nil, ErrAccountNotFound
	}
	accountID = strings.TrimSpace(accountID)
	for {
		p.mu.Lock()
		account := p.byID[accountID]
		if account == nil {
			p.mu.Unlock()
			return nil, fmt.Errorf("%w: %s", ErrAccountNotFound, accountID)
		}
		if !account.exclusive && account.active == 0 {
			leaseLock, leasePath, err := acquireAccountFileLease(account.StoragePath)
			if err == nil {
				account.exclusive = true
				account.leaseLock = leaseLock
				account.leasePath = leasePath
				p.mu.Unlock()
				return &AccountLease{
					pool: p, account: account, exclusive: true, authGeneration: account.authGeneration,
				}, nil
			}
			if !errors.Is(err, errAccountLeaseBusy) {
				p.mu.Unlock()
				return nil, err
			}
		}
		changed := p.changed
		p.mu.Unlock()

		timer := time.NewTimer(externalLeasePoll)
		select {
		case <-ctx.Done():
			if !timer.Stop() {
				<-timer.C
			}
			return nil, ctx.Err()
		case <-changed:
			if !timer.Stop() {
				<-timer.C
			}
		case <-timer.C:
		}
	}
}

// AcquireFor 按模型方法账户或资源粘性获取账户槽位
func (p *AccountPool) AcquireFor(ctx context.Context, selection AccountSelection) (*AccountLease, error) {
	if p == nil {
		return nil, ErrNoEligibleAccount
	}
	for {
		p.mu.Lock()
		if modelID := strings.TrimPrefix(strings.TrimSpace(selection.ModelID), "models/"); modelID != "" {
			if !p.hasModelCatalogLocked() {
				p.mu.Unlock()
				return nil, ErrNoEligibleAccount
			}
			if !p.hasModelLocked(modelID) {
				p.mu.Unlock()
				return nil, fmt.Errorf("%w: %s", ErrModelNotFound, modelID)
			}
			if selection.Method != "" && !p.hasModelMethodLocked(modelID, selection.Method) {
				p.mu.Unlock()
				return nil, fmt.Errorf("%w: 模型 %s 不支持 %s", ErrModelNotFound, modelID, selection.Method)
			}
		}
		lease, earliest, waitable, err := p.tryAcquireLocked(selection, time.Now())
		if lease != nil || err != nil {
			p.mu.Unlock()
			return lease, err
		}
		if !waitable {
			p.mu.Unlock()
			return nil, ErrNoEligibleAccount
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
			if !timer.Stop() {
				<-timer.C
			}
			return nil, ctx.Err()
		case <-changed:
			if !timer.Stop() {
				<-timer.C
			}
		case <-timer.C:
		}
	}
}

// AcquireResource 获取创建资源的固定账户
func (p *AccountPool) AcquireResource(ctx context.Context, resourceID string) (*AccountLease, error) {
	return p.AcquireFor(ctx, AccountSelection{ResourceID: resourceID})
}

// Account 返回当前租约持有的账户
func (l *AccountLease) Account() *Account {
	if l == nil {
		return nil
	}
	return l.account
}

// SaveStorageState 在租约内原子写回认证状态
func (l *AccountLease) SaveStorageState(state StorageState) error {
	if l == nil || l.account == nil || l.pool == nil {
		return fmt.Errorf("账户租约未初始化")
	}
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
	l.pool.mu.Unlock()
	return nil
}

// RefreshStorageState 保证并发认证失效只续签一次
func (l *AccountLease) RefreshStorageState(update func(*StorageState) error, after func() error) error {
	if l == nil || l.account == nil || l.pool == nil || update == nil {
		return fmt.Errorf("账户租约未初始化")
	}
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
	if l.authGeneration != currentGeneration {
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
	if err := WriteStorageState(l.account.StoragePath, state); err != nil {
		return err
	}
	l.pool.mu.Lock()
	l.account.StorageState = state
	l.pool.mu.Unlock()
	if after != nil {
		if err := after(); err != nil {
			return err
		}
	}
	l.pool.mu.Lock()
	l.account.authGeneration++
	l.authGeneration = l.account.authGeneration
	l.pool.mu.Unlock()
	return nil
}

// SaveConfig 在租约内原子写回账户固定配置
func (l *AccountLease) SaveConfig(value AccountConfig) error {
	if l == nil || l.account == nil || l.pool == nil {
		return fmt.Errorf("账户租约未初始化")
	}
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
	if l == nil || l.account == nil || l.pool == nil {
		return StorageState{}, fmt.Errorf("账户租约未初始化")
	}
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

// BindResource 将资源固定到当前租约账户
func (l *AccountLease) BindResource(resourceID string, kind string) error {
	if l == nil || l.account == nil || l.pool == nil {
		return fmt.Errorf("账户租约未初始化")
	}
	l.operation.Lock()
	defer l.operation.Unlock()
	if l.released {
		return fmt.Errorf("账户租约已释放")
	}
	l.account.storageMu.Lock()
	defer l.account.storageMu.Unlock()
	return l.pool.BindResourceKind(resourceID, l.account.ID, kind)
}

// MergeSetCookieHeaders 将响应 Cookie 合并到账户最新持久状态
func (l *AccountLease) MergeSetCookieHeaders(headers []string, requestURL string, now time.Time) error {
	if l == nil || l.account == nil || l.pool == nil {
		return fmt.Errorf("账户租约未初始化")
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
		if l.account.active == 0 && !l.account.exclusive && l.account.leaseLock != nil {
			if err := l.account.leaseLock.Unlock(); err != nil {
				l.err = err
			}
			l.account.leaseLock = nil
			l.account.leasePath = ""
		}
		l.pool.notifyLocked()
		l.pool.mu.Unlock()
	})
	return l.err
}

// SetModels 替换账户的实时模型目录
func (p *AccountPool) SetModels(accountID string, models []Model) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	account := p.byID[accountID]
	if account == nil {
		return fmt.Errorf("账户不存在: %s", accountID)
	}
	account.Models = cloneAccountModels(models)
	account.initializedAt = time.Now()
	if account.State == AccountUnavailable {
		account.State = AccountReady
		account.stateMessage = ""
	}
	p.notifyLocked()
	return nil
}

// MarkCooldown 设置账户模型的权威冷却期限
func (p *AccountPool) MarkCooldown(accountID string, modelID string, until time.Time, reason string) error {
	if !until.After(time.Now()) {
		return fmt.Errorf("冷却期限必须在未来")
	}
	if strings.TrimSpace(modelID) == "" {
		modelID = globalCooldownKey
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	account := p.byID[accountID]
	if account == nil {
		return fmt.Errorf("账户不存在: %s", accountID)
	}
	runtimeState := cloneRuntime(account.runtime)
	runtimeState.Cooldowns[modelID] = CooldownState{Until: until, Reason: reason}
	if err := writeRuntime(account.RuntimePath, runtimeState); err != nil {
		return err
	}
	account.runtime = runtimeState
	p.notifyLocked()
	return nil
}

// BindResource 将资源 ID 固定到创建账户
func (p *AccountPool) BindResource(resourceID string, accountID string) error {
	return p.BindResourceKind(resourceID, accountID, "")
}

// BindResourceKind 将带类型的资源 ID 固定到创建账户
func (p *AccountPool) BindResourceKind(resourceID string, accountID string, kind string) error {
	resourceID = strings.TrimSpace(resourceID)
	if resourceID == "" {
		return fmt.Errorf("资源 ID 不能为空")
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	account := p.byID[accountID]
	if account == nil {
		return fmt.Errorf("账户不存在: %s", accountID)
	}
	if owner, exists := p.resources[resourceID]; exists && owner != accountID {
		return fmt.Errorf("资源 %s 已绑定账户 %s", resourceID, owner)
	}
	runtimeState := cloneRuntime(account.runtime)
	if _, exists := runtimeState.Resources[resourceID]; !exists {
		runtimeState.Resources[resourceID] = ResourceBinding{Kind: strings.TrimSpace(kind), CreatedAt: time.Now().UTC()}
	}
	if err := writeRuntime(account.RuntimePath, runtimeState); err != nil {
		return err
	}
	account.runtime = runtimeState
	p.resources[resourceID] = accountID
	p.notifyLocked()
	return nil
}

// UnbindResource 删除终态资源的账户映射
func (p *AccountPool) UnbindResource(resourceID string) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	accountID, exists := p.resources[resourceID]
	if !exists {
		return ErrResourceNotFound
	}
	account := p.byID[accountID]
	if account == nil {
		return fmt.Errorf("资源账户不存在: %s", accountID)
	}
	runtimeState := cloneRuntime(account.runtime)
	delete(runtimeState.Resources, resourceID)
	if err := writeRuntime(account.RuntimePath, runtimeState); err != nil {
		return err
	}
	account.runtime = runtimeState
	delete(p.resources, resourceID)
	p.notifyLocked()
	return nil
}

// MarkAuthRequired 将账户标记为需要重新登录
func (p *AccountPool) MarkAuthRequired(accountID string, reason string) error {
	return p.setAccountState(accountID, AccountAuthRequired, reason)
}

// MarkUnavailable 将账户标记为初始化或运行失败
func (p *AccountPool) MarkUnavailable(accountID string, reason string) error {
	return p.setAccountState(accountID, AccountUnavailable, reason)
}

// MarkReady 将账户恢复为可调度状态
func (p *AccountPool) MarkReady(accountID string) error {
	return p.setAccountState(accountID, AccountReady, "")
}

// Status 返回账户池的脱敏状态
func (p *AccountPool) Status() []AccountStatus {
	if p == nil {
		return nil
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	now := time.Now()
	statuses := make([]AccountStatus, 0, len(p.accounts))
	for _, account := range p.accounts {
		if account == nil {
			continue
		}
		state := account.State
		_, active := accountCooldown(account, "", now)
		if !account.Config.Enabled {
			state = AccountDisabled
		} else if account.exclusive || account.active > 0 {
			state = AccountBusy
		} else if state == AccountReady && active {
			state = AccountCooldown
		}
		models := make([]string, 0, len(account.Models))
		for _, model := range account.Models {
			models = append(models, model.ID)
		}
		sort.Strings(models)
		status := AccountStatus{
			ID:        account.ID,
			Label:     account.Config.Label,
			State:     state,
			Enabled:   account.Config.Enabled,
			Proxy:     account.Config.Proxy,
			Locale:    account.Config.Locale,
			Timezone:  account.Config.Timezone,
			Models:    models,
			Cooldowns: cloneCooldowns(account.runtime.Cooldowns),
			Message:   account.stateMessage,
		}
		if !account.LastUsed.IsZero() {
			status.LastUsed = timePointer(account.LastUsed)
		}
		statuses = append(statuses, status)
	}
	return statuses
}

// ClassifyCandidates 按 warm 集合分类目标请求的候选账户
func (p *AccountPool) ClassifyCandidates(selection AccountSelection, warmAccountIDs []string) (AccountCandidateGroups, error) {
	if p == nil {
		return AccountCandidateGroups{}, ErrNoEligibleAccount
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	modelID := strings.TrimPrefix(strings.TrimSpace(selection.ModelID), "models/")
	selection.ModelID = modelID
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
		if account == nil || !account.Config.Enabled || account.State != AccountReady {
			continue
		}
		if modelID != "" && !account.SupportsModel(modelID) {
			continue
		}
		if selection.Method != "" && !account.SupportsMethod(modelID, selection.Method) {
			continue
		}
		groups.Eligible = true
		if cooldown, active := accountCooldown(account, modelID, now); active {
			if groups.EarliestCooldown.IsZero() || cooldown.Until.Before(groups.EarliestCooldown) {
				groups.EarliestCooldown = cooldown.Until
			}
			continue
		}
		_, isWarm := warm[account.ID]
		switch {
		case isWarm && (account.exclusive || account.active >= p.perAccountConcurrency):
			groups.WarmBusy = append(groups.WarmBusy, account.ID)
		case isWarm:
			groups.WarmReady = append(groups.WarmReady, account.ID)
		case account.exclusive || account.active >= p.perAccountConcurrency:
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
	for _, index := range indices {
		account := p.accounts[index]
		if account == nil || !account.Config.Enabled || account.State != AccountReady {
			continue
		}
		if selection.ModelID != "" && !account.SupportsModel(selection.ModelID) {
			continue
		}
		if selection.Method != "" && !account.SupportsMethod(selection.ModelID, selection.Method) {
			continue
		}
		waitable = true
		if account.exclusive || account.active >= p.perAccountConcurrency {
			continue
		}
		if selection.ResourceID == "" {
			if cooldown, active := accountCooldown(account, selection.ModelID, now); active {
				if earliest.IsZero() || cooldown.Until.Before(earliest) {
					earliest = cooldown.Until
				}
				continue
			}
		}
		if account.active == 0 {
			leaseLock, leasePath, err := acquireAccountFileLease(account.StoragePath)
			if errors.Is(err, errAccountLeaseBusy) {
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
			account.leasePath = leasePath
		}
		account.active++
		account.LastUsed = now
		p.next = (index + 1) % max(1, len(p.accounts))
		return &AccountLease{
			pool: p, account: account, authGeneration: account.authGeneration,
		}, time.Time{}, true, nil
	}
	return nil, earliest, waitable, nil
}

func (p *AccountPool) hasModelLocked(modelID string) bool {
	for _, account := range p.accounts {
		if account == nil {
			continue
		}
		for _, model := range account.Models {
			if modelMatchesID(model, modelID) {
				return true
			}
		}
	}
	return false
}

func (p *AccountPool) hasModelCatalogLocked() bool {
	for _, account := range p.accounts {
		if account != nil && len(account.Models) > 0 {
			return true
		}
	}
	return false
}

func (p *AccountPool) hasModelMethodLocked(modelID string, method string) bool {
	for _, account := range p.accounts {
		if account == nil {
			continue
		}
		for _, model := range account.Models {
			if modelMatchesID(model, modelID) && hasMethod(model, method) {
				return true
			}
		}
	}
	return false
}

// BootstrapModel 返回账户实时目录中的稳定 WAA 初始化模型
func (p *AccountPool) BootstrapModel(accountID string) (string, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	account := p.byID[strings.TrimSpace(accountID)]
	if account == nil {
		return "", fmt.Errorf("账户不存在: %s", accountID)
	}
	for _, model := range account.Models {
		if model.ID == BootstrapModelID && hasMethod(model, "generateContent") &&
			hasMethod(model, "countTokens") && hasMethod(model, "createCachedContent") && model.Capabilities["chat_model"] {
			return model.ID, nil
		}
	}
	return "", fmt.Errorf("账户 %s 的实时目录没有 %s", account.ID, BootstrapModelID)
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
			if account != nil && account.ID == accountID {
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
	for offset := 0; offset < len(p.accounts); offset++ {
		index := (p.next + offset) % len(p.accounts)
		account := p.accounts[index]
		if allowed != nil {
			if account == nil {
				continue
			}
			if _, exists := allowed[account.ID]; !exists {
				continue
			}
		}
		indices = append(indices, index)
	}
	return indices, nil
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
	p.notifyLocked()
	return nil
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
	return &Account{
		ID:           id,
		Directory:    directory,
		ConfigPath:   configPath,
		StoragePath:  storagePath,
		RuntimePath:  runtimePath,
		Config:       accountConfig,
		StorageState: state,
		State:        initialAccountState(accountConfig, state),
		runtime:      runtimeState,
	}, nil
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
	value := accountRuntimeState{
		Cooldowns: make(map[string]CooldownState),
		Resources: make(map[string]ResourceBinding),
	}
	file, err := os.Open(filePath)
	if os.IsNotExist(err) {
		return value, nil
	}
	if err != nil {
		return accountRuntimeState{}, fmt.Errorf("读取 %s: %w", runtimeStateName, err)
	}
	defer file.Close()
	decoder := json.NewDecoder(file)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&value); err != nil {
		return accountRuntimeState{}, fmt.Errorf("解析 %s: %w", runtimeStateName, err)
	}
	if err := ensureJSONEnd(decoder); err != nil {
		return accountRuntimeState{}, fmt.Errorf("解析 %s: %w", runtimeStateName, err)
	}
	if value.Cooldowns == nil {
		value.Cooldowns = make(map[string]CooldownState)
	}
	if value.Resources == nil {
		value.Resources = make(map[string]ResourceBinding)
	}
	return value, nil
}

func writeRuntime(filePath string, value accountRuntimeState) error {
	if filePath == "" {
		return nil
	}
	data, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return fmt.Errorf("编码 %s: %w", runtimeStateName, err)
	}
	return atomicWriteFile(filePath, append(data, '\n'), 0o600)
}

func cloneRuntime(value accountRuntimeState) accountRuntimeState {
	result := accountRuntimeState{
		Cooldowns: make(map[string]CooldownState, len(value.Cooldowns)),
		Resources: make(map[string]ResourceBinding, len(value.Resources)),
	}
	for key, cooldown := range value.Cooldowns {
		result.Cooldowns[key] = cooldown
	}
	for key, binding := range value.Resources {
		result.Resources[key] = binding
	}
	return result
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

func acquireAccountFileLease(storagePath string) (*flock.Flock, string, error) {
	if storagePath == "" {
		return nil, "", nil
	}
	accountDirectory := filepath.Dir(storagePath)
	leaseDirectory := filepath.Join(filepath.Dir(accountDirectory), ".leases")
	if err := os.MkdirAll(leaseDirectory, 0o700); err != nil {
		return nil, "", fmt.Errorf("创建账户租约目录: %w", err)
	}
	leasePath := filepath.Join(leaseDirectory, filepath.Base(accountDirectory)+".lock")
	leaseLock := flock.New(leasePath)
	locked, err := leaseLock.TryLock()
	if err != nil {
		return nil, leasePath, fmt.Errorf("锁定账户租约: %w", err)
	}
	if !locked {
		return nil, leasePath, errAccountLeaseBusy
	}
	return leaseLock, leasePath, nil
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

func randomAccountID() (string, error) {
	value := make([]byte, 8)
	if _, err := rand.Read(value); err != nil {
		return "", fmt.Errorf("生成账户 ID: %w", err)
	}
	return "acc-" + hex.EncodeToString(value), nil
}

func cloneAccountModels(models []Model) []Model {
	result := make([]Model, len(models))
	for index, model := range models {
		result[index] = model
		result[index].Methods = append([]string(nil), model.Methods...)
		if model.Capabilities != nil {
			result[index].Capabilities = make(map[string]bool, len(model.Capabilities))
			for key, value := range model.Capabilities {
				result[index].Capabilities[key] = value
			}
		}
		if model.CapabilityOptions != nil {
			result[index].CapabilityOptions = make(map[string][]string, len(model.CapabilityOptions))
			for key, value := range model.CapabilityOptions {
				result[index].CapabilityOptions[key] = append([]string(nil), value...)
			}
		}
	}
	return result
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
