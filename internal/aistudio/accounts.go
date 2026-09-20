package aistudio

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/mail"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"sync"
	"time"

	appconfig "github.com/Mag1cFall/AIStudio2API/internal/config"
	"github.com/gofrs/flock"
)

const (
	accountConfigName = "account.json"
	storageStateName  = "storage-state.json"
	runtimeStateName  = "runtime-state.json"
	globalCooldownKey = "*"
	externalLeasePoll = 100 * time.Millisecond
	runtimeLockPoll   = 25 * time.Millisecond
	runtimeLockLimit  = 2 * time.Second
)

// AccountState indicates whether an account is currently schedulable
type AccountState string

const (
	// AccountReady indicates that the account can accept requests
	AccountReady AccountState = "ready"
	// AccountBusy indicates that the account has active requests
	AccountBusy AccountState = "busy"
	// AccountCooldown indicates that the account or model is in a cooldown period
	AccountCooldown AccountState = "cooldown"
	// AccountAuthRequired indicates that the account requires re-authentication
	AccountAuthRequired AccountState = "auth_required"
	// AccountUnavailable indicates that account initialization or operation failed
	AccountUnavailable AccountState = "unavailable"
	// AccountDisabled indicates that the account is disabled
	AccountDisabled AccountState = "disabled"
)

var (
	// ErrInvalidArgument indicates that request arguments were determined invalid before sending
	ErrInvalidArgument = errors.New("invalid AI Studio request arguments")
	// ErrModelNotFound indicates that the requested model does not exist in the live catalog
	ErrModelNotFound = errors.New("requested model not found in AI Studio live catalog")
	// ErrNoEligibleAccount indicates that no account has the capabilities required for the request
	ErrNoEligibleAccount = errors.New("no eligible AI Studio account available")
	// ErrAccountNotFound indicates that the stable account ID does not exist
	ErrAccountNotFound = errors.New("account not found")
	// ErrAccountLeased indicates that the account currently has an in-process or cross-process lease
	ErrAccountLeased = errors.New("account is busy")
	// ErrResourceNotFound indicates that no account mapping was created for the resource
	ErrResourceNotFound = errors.New("resource account mapping not found")
	errAccountLeaseBusy = ErrAccountLeased
)

// AccountConfig represents the fixed minimal configuration in an account directory
type AccountConfig struct {
	Label    string `json:"label"`
	Enabled  bool   `json:"enabled"`
	Proxy    string `json:"proxy"`
	Locale   string `json:"locale"`
	Timezone string `json:"timezone"`
}

// ResourceBinding records the creator account for an upstream resource
type ResourceBinding struct {
	Kind      string                 `json:"kind,omitempty"`
	Name      string                 `json:"name,omitempty"`
	MIME      string                 `json:"mime,omitempty"`
	Size      int64                  `json:"size,omitempty"`
	Purpose   string                 `json:"purpose,omitempty"`
	CreatedAt time.Time              `json:"created_at"`
	Video     *VideoResourceMetadata `json:"video,omitempty"`
}

// VideoResourceMetadata stores persistent fields for an OpenAI video object
type VideoResourceMetadata struct {
	Model   string `json:"model"`
	Seconds string `json:"seconds"`
	Size    string `json:"size"`
}

type accountRuntimeState struct {
	Cooldowns          map[string]CooldownState   `json:"cooldowns,omitempty"`
	Resources          map[string]ResourceBinding `json:"resources,omitempty"`
	ModelAccess        map[string]ModelAccess     `json:"model_access,omitempty"`
	BenefitTier        BenefitTier                `json:"benefit_tier,omitempty"`
	CatalogFingerprint string                     `json:"catalog_fingerprint,omitempty"`
}

// ModelAccessState represents the tested access qualification of an account for a single model
type ModelAccessState string

const (
	// ModelAccessVerified indicates that the account has successfully called the model
	ModelAccessVerified ModelAccessState = "verified"
)

// ModelAccess stores the tested result of an account's model access qualification
type ModelAccess struct {
	State     ModelAccessState `json:"state"`
	CheckedAt time.Time        `json:"checked_at"`
	Reason    string           `json:"reason,omitempty"`
}

// Account represents an AI Studio account corresponding to a stable directory
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
	active                int
	exclusive             bool
	authRefreshers        int
	leaseLock             *flock.Flock
	leasePath             string
	storageMu             sync.Mutex
	runtimeMu             sync.Mutex
	persistenceLocked     bool
	authGeneration        uint64
	authCheckedAt         time.Time
	modelAccessGeneration uint64
	stateMessage          string
	initializedAt         time.Time
}

// AccountStatus represents the sanitized account status used by the management interface
type AccountStatus struct {
	ID          string                   `json:"id"`
	Label       string                   `json:"label"`
	State       AccountState             `json:"state"`
	Enabled     bool                     `json:"enabled"`
	Proxy       string                   `json:"proxy"`
	Locale      string                   `json:"locale"`
	Timezone    string                   `json:"timezone"`
	Models      []string                 `json:"models"`
	BenefitTier string                   `json:"benefit_tier"`
	Cooldowns   map[string]CooldownState `json:"-"`
	LastUsed    *time.Time               `json:"last_used,omitempty"`
	Message     string                   `json:"message,omitempty"`
}

// AccountSelection describes the capability or stickiness requirements for account scheduling
type AccountSelection struct {
	ModelID           string
	ModelAccessScope  string
	Method            string
	Capability        string
	AccountID         string
	ResourceID        string
	AllowedAccountIDs []string
}

const preferredBootstrapModelID = "gemini-flash-latest"

// ModelAccessKey returns an independent qualification key for associating real catalog models
func ModelAccessKey(scope string, modelID string) string {
	scope = strings.TrimSpace(scope)
	modelID = strings.TrimPrefix(strings.TrimSpace(modelID), "models/")
	if scope == "" || modelID == "" {
		return modelID
	}
	return scope + ":" + modelID
}

// AccountCandidateGroups represents the real-time schedulable status of warm and standby accounts
type AccountCandidateGroups struct {
	WarmReady        []string
	WarmAvailable    []string
	WarmBusy         []string
	StandbyReady     []string
	StandbyBusy      []string
	EarliestCooldown time.Time
	Eligible         bool
}

// AccountCandidateState represents real-time scheduling metrics for an account candidate
type AccountCandidateState struct {
	ModelAccess   ModelAccessState
	Active        int
	AvailableSlot int
}

// AccountStore loads accounts from one or more account files or directories
type AccountStore struct {
	paths []string
}

// AccountPool performs capability and concurrency slot scheduling across accounts
type AccountPool struct {
	mu                    sync.Mutex
	accounts              []*Account
	byID                  map[string]*Account
	resources             map[string]string
	perAccountConcurrency int
	routingStrategy       string
	lastPicked            map[string]string
	changed               chan struct{}
}

// AccountLease represents an account request slot
type AccountLease struct {
	pool                  *AccountPool
	account               *Account
	exclusive             bool
	authGeneration        uint64
	modelAccessGeneration uint64
	checkedAt             time.Time
	refreshRuntime        bool
	operation             sync.Mutex
	released              bool
	once                  sync.Once
	err                   error
}

// AccountRuntimeLease ensures that only one WAA runtime exists for the same email
type AccountRuntimeLease struct {
	lock *flock.Flock
	once sync.Once
	err  error
}

// AccountPublishLease protects the publishing of new accounts from stable directories to the runtime
type AccountPublishLease struct {
	account     *Account
	requestLock *flock.Flock
	runtimeLock *flock.Flock
	once        sync.Once
	err         error
}

// DefaultAccountConfig returns the minimal configuration for a new account
func DefaultAccountConfig(label string) AccountConfig {
	return AccountConfig{
		Label:    strings.TrimSpace(label),
		Enabled:  true,
		Locale:   DefaultAccountLocale(),
		Timezone: DefaultAccountTimezone(),
	}
}

// NewAccountStore creates an account directory store
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

// Load scans account directories and restores cooldown and resource stickiness
func (s *AccountStore) Load() ([]*Account, error) {
	if s == nil || len(s.paths) == 0 {
		return nil, fmt.Errorf("account path is empty")
	}
	directories := make([]string, 0)
	for _, source := range s.paths {
		absolute, err := filepath.Abs(source)
		if err != nil {
			return nil, fmt.Errorf("resolve account path %q: %w", source, err)
		}
		info, err := os.Stat(absolute)
		if os.IsNotExist(err) {
			continue
		}
		if err != nil {
			return nil, fmt.Errorf("read account path %q: %w", source, err)
		}
		if !info.IsDir() {
			if filepath.Base(absolute) != storageStateName {
				return nil, fmt.Errorf("account file must be named %s", storageStateName)
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
			return nil, fmt.Errorf("scan account directory %q: %w", source, err)
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
			return nil, fmt.Errorf("duplicate account ID: %s", account.ID)
		}
		ids[account.ID] = struct{}{}
		for resourceID := range account.runtime.Resources {
			if owner, exists := resources[resourceID]; exists {
				return nil, fmt.Errorf("resource %s bound to both accounts %s and %s", resourceID, owner, account.ID)
			}
			resources[resourceID] = account.ID
		}
		accounts = append(accounts, account)
	}
	return accounts, nil
}

// Create creates and locks an account directory named after the authenticated email
func (s *AccountStore) Create(accountConfig AccountConfig, state StorageState) (*Account, *AccountPublishLease, error) {
	if s == nil || len(s.paths) != 1 {
		return nil, nil, fmt.Errorf("creating an account requires an account root directory")
	}
	if err := accountConfig.Validate(); err != nil {
		return nil, nil, err
	}
	if err := state.Validate(); err != nil {
		return nil, nil, err
	}
	root, err := filepath.Abs(s.paths[0])
	if err != nil {
		return nil, nil, fmt.Errorf("resolve account root directory: %w", err)
	}
	if err := os.MkdirAll(root, 0o755); err != nil {
		return nil, nil, fmt.Errorf("create account root directory: %w", err)
	}
	id, err := accountEmailID(accountConfig, state)
	if err != nil {
		return nil, nil, err
	}
	accountConfig.Label = id
	temporary, err := os.MkdirTemp(root, ".account-*.tmp")
	if err != nil {
		return nil, nil, fmt.Errorf("create temporary account directory: %w", err)
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
		return nil, nil, errors.Join(fmt.Errorf("save account directory: %w", err), publishLease.Release())
	}
	if err := validatePersistentAccountFiles(account); err != nil {
		return nil, nil, errors.Join(err, os.RemoveAll(directory), publishLease.Release())
	}
	return account, publishLease, nil
}

// Delete deletes a stable account directory belonging to the current store
func (s *AccountStore) Delete(account *Account) error {
	if account == nil || strings.TrimSpace(account.ID) == "" || strings.TrimSpace(account.Directory) == "" {
		return fmt.Errorf("account is not initialized")
	}
	directory, err := filepath.Abs(account.Directory)
	if err != nil {
		return fmt.Errorf("resolve account directory: %w", err)
	}
	if filepath.Base(directory) != account.ID {
		return fmt.Errorf("account directory does not match stable ID")
	}
	owned, err := s.ownsDirectory(directory)
	if err != nil {
		return err
	}
	if !owned {
		return fmt.Errorf("account directory does not belong to current AccountStore: %s", directory)
	}
	info, err := os.Stat(directory)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return fmt.Errorf("read account directory: %w", err)
	}
	if !info.IsDir() {
		return fmt.Errorf("account path is not a directory: %s", directory)
	}
	account.storageMu.Lock()
	lockedByPool := account.persistenceLocked
	account.storageMu.Unlock()
	if lockedByPool {
		if err := os.RemoveAll(directory); err != nil {
			return fmt.Errorf("delete account directory: %w", err)
		}
		return nil
	}
	leaseLock, _, err := acquireAccountFileLease(account.StoragePath)
	if errors.Is(err, errAccountLeaseBusy) {
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
		return fmt.Errorf("delete account directory: %w", deleteErr)
	}
	return nil
}

func (s *AccountStore) ownsDirectory(directory string) (bool, error) {
	if s == nil || len(s.paths) == 0 {
		return false, fmt.Errorf("account path is empty")
	}
	for _, source := range s.paths {
		absolute, err := filepath.Abs(source)
		if err != nil {
			return false, fmt.Errorf("resolve account path %q: %w", source, err)
		}
		info, err := os.Stat(absolute)
		if os.IsNotExist(err) {
			continue
		}
		if err != nil {
			return false, fmt.Errorf("read account path %q: %w", source, err)
		}
		root := absolute
		if !info.IsDir() {
			root = filepath.Dir(absolute)
		}
		relative, err := filepath.Rel(root, directory)
		if err != nil {
			return false, fmt.Errorf("compare account path %q: %w", source, err)
		}
		if relative == "." || relative != ".." && !strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
			return true, nil
		}
	}
	return false, nil
}

// Validate validates the fixed configuration of an account
func (c AccountConfig) Validate() error {
	if _, err := normalizeAccountEmail(c.Label); err != nil {
		return err
	}
	if strings.TrimSpace(c.Locale) == "" {
		return fmt.Errorf("account locale cannot be empty")
	}
	if strings.TrimSpace(c.Timezone) == "" {
		return fmt.Errorf("account timezone cannot be empty")
	}
	if err := appconfig.ValidateProxy(c.Proxy); err != nil {
		return fmt.Errorf("account proxy is invalid: %w", err)
	}
	return nil
}

// EffectiveProxy returns the account fixed proxy or global proxy
func (a *Account) EffectiveProxy(globalProxy string) string {
	if a != nil && strings.TrimSpace(a.Config.Proxy) != "" {
		return strings.TrimSpace(a.Config.Proxy)
	}
	return strings.TrimSpace(globalProxy)
}

// AcceptLanguage returns the request language header corresponding to the account locale
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

// SupportsModel checks whether the account's live catalog contains the model
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

// SupportsMethod checks whether the account model declares the target method
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
		if !modelAllowedByTier(model, account.BenefitTier) {
			return false
		}
		if strings.TrimSpace(selection.Method) != "" && !hasMethod(model, selection.Method) {
			return false
		}
		return strings.TrimSpace(selection.Capability) == "" || model.Capabilities[selection.Capability]
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

// NewAccountPool creates an exclusive account scheduling pool
func NewAccountPool(accounts []*Account, perAccountConcurrency int) *AccountPool {
	p := &AccountPool{
		accounts: append([]*Account(nil), accounts...), byID: make(map[string]*Account, len(accounts)),
		resources: make(map[string]string), perAccountConcurrency: perAccountConcurrency, changed: make(chan struct{}),
		routingStrategy: "round-robin", lastPicked: make(map[string]string),
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

// Account returns the account corresponding to the stable ID
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

// Add adds a new account to the current scheduling pool
func (p *AccountPool) Add(account *Account) (resultErr error) {
	if p == nil || account == nil || strings.TrimSpace(account.ID) == "" {
		return fmt.Errorf("account is not initialized")
	}
	if account.ConfigPath != "" {
		account.storageMu.Lock()
		lockedExternally := account.persistenceLocked
		account.storageMu.Unlock()
		if !lockedExternally {
			leaseLock, _, err := acquireAccountFileLease(account.StoragePath)
			if errors.Is(err, errAccountLeaseBusy) {
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
		return fmt.Errorf("account already exists: %s", account.ID)
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
			return fmt.Errorf("resource %s is already bound to account %s", resourceID, owner)
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

// Remove deletes the persistent directory and removes the account from the scheduling pool when idle
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
		return nil, fmt.Errorf("account directory delete function is nil")
	}
	account.exclusive = true
	p.notifyLocked()
	p.mu.Unlock()
	leaseLock, _, err := acquireAccountFileLease(account.StoragePath)
	if err != nil {
		p.mu.Lock()
		account.exclusive = false
		p.notifyLocked()
		p.mu.Unlock()
		if errors.Is(err, errAccountLeaseBusy) {
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
		if candidate != nil && candidate.ID == accountID {
			p.accounts = append(p.accounts[:index], p.accounts[index+1:]...)
			break
		}
	}
	p.notifyLocked()
	p.mu.Unlock()
	return account, releaseErr
}

// Acquire acquires an account slot for model round-robin
func (p *AccountPool) Acquire(ctx context.Context, model string) (*AccountLease, error) {
	return p.AcquireFor(ctx, AccountSelection{ModelID: model})
}

// AcquireAccount acquires a lease for a specified account unrestricted by scheduling state for management operations
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
		if !account.exclusive && account.authRefreshers == 0 && account.active == 0 {
			checkedAt := time.Now().UTC()
			leaseLock, leasePath, err := acquireAccountFileLease(account.StoragePath)
			if err == nil {
				account.exclusive = true
				account.leaseLock = leaseLock
				account.leasePath = leasePath
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

// AcquireFor acquires an account slot by model, method, account, or resource stickiness
func (p *AccountPool) AcquireFor(ctx context.Context, selection AccountSelection) (*AccountLease, error) {
	if p == nil {
		return nil, ErrNoEligibleAccount
	}
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

// TryAcquireFor attempts to acquire an account slot and returns the current result immediately
func (p *AccountPool) TryAcquireFor(ctx context.Context, selection AccountSelection) (*AccountLease, bool, error) {
	if p == nil {
		return nil, false, ErrNoEligibleAccount
	}
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
	if lease == nil || lease.account == nil {
		return false, ErrNoEligibleAccount
	}
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
	if selection.ModelID != "" && !accountSupportsSelection(account, selection) {
		return false, nil
	}
	if selection.ResourceID == "" {
		if _, active := accountCooldown(account, selectionAccessScope(selection), time.Now()); active {
			return false, nil
		}
	}
	return true, nil
}

func (p *AccountPool) markStaleAccountUnavailable(account *Account) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if account != nil && p.byID[account.ID] == account {
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
		return fmt.Errorf("%w: model %s does not support %s", ErrModelNotFound, modelID, selection.Method)
	}
	if selection.Capability != "" && !p.hasModelCapabilityLocked(modelID, selection.Capability) {
		return fmt.Errorf("%w: model %s does not support %s", ErrModelNotFound, modelID, selection.Capability)
	}
	return nil
}

// AcquireResource retrieves the fixed account that created the resource
func (p *AccountPool) AcquireResource(ctx context.Context, resourceID string) (*AccountLease, error) {
	return p.AcquireFor(ctx, AccountSelection{ResourceID: resourceID})
}

// Account returns the account held by the current lease
func (l *AccountLease) Account() *Account {
	if l == nil {
		return nil
	}
	return l.account
}

// ModelAccessGeneration returns the model access catalog generation at the start of the lease
func (l *AccountLease) ModelAccessGeneration() uint64 {
	if l == nil {
		return 0
	}
	return l.modelAccessGeneration
}

// CheckedAt returns the time when the lease was acquired for the current account request
func (l *AccountLease) CheckedAt() time.Time {
	if l == nil {
		return time.Time{}
	}
	return l.checkedAt
}

// MarkAuthenticationValid records the authenticated success state confirmed by the current lease
func (l *AccountLease) MarkAuthenticationValid() error {
	return l.markAuthenticationStateAt(false, "", l.CheckedAt())
}

// MarkAuthenticationRequired records the authentication failure state confirmed by the current lease
func (l *AccountLease) MarkAuthenticationRequired(reason string) error {
	return l.markAuthenticationStateAt(true, reason, l.CheckedAt())
}

// markAuthenticationValidAt records the authentication success state for a specific round in a persistent connection
func (l *AccountLease) markAuthenticationValidAt(checkedAt time.Time) error {
	return l.markAuthenticationStateAt(false, "", checkedAt)
}

// markAuthenticationRequiredAt records the authentication failure state for a specific round in a persistent connection
func (l *AccountLease) markAuthenticationRequiredAt(reason string, checkedAt time.Time) error {
	return l.markAuthenticationStateAt(true, reason, checkedAt)
}

// markAuthenticationStateAt writes back the account authentication state for the specified sequential time
func (l *AccountLease) markAuthenticationStateAt(required bool, reason string, checkedAt time.Time) error {
	if l == nil || l.account == nil || l.pool == nil {
		return fmt.Errorf("account lease is not initialized")
	}
	l.operation.Lock()
	authGeneration := l.authGeneration
	l.operation.Unlock()
	l.pool.mu.Lock()
	defer l.pool.mu.Unlock()
	if l.pool.byID[l.account.ID] != l.account || authGeneration != l.account.authGeneration ||
		checkedAt.Before(l.account.authCheckedAt) {
		return nil
	}
	if required && checkedAt.Equal(l.account.authCheckedAt) && l.account.State == AccountReady {
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

// ModelAccessGeneration returns the current model access catalog generation of the account
func (p *AccountPool) ModelAccessGeneration(accountID string) uint64 {
	if p == nil {
		return 0
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	account := p.byID[strings.TrimSpace(accountID)]
	if account == nil {
		return 0
	}
	return account.modelAccessGeneration
}

// SaveStorageState atomically writes back the storage state within the lease
func (l *AccountLease) SaveStorageState(state StorageState) error {
	if l == nil || l.account == nil || l.pool == nil {
		return fmt.Errorf("account lease is not initialized")
	}
	l.operation.Lock()
	defer l.operation.Unlock()
	if l.released {
		return fmt.Errorf("account lease has been released")
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

// RefreshStorageState ensures concurrent auth invalidations are committed only once
func (l *AccountLease) RefreshStorageState(
	update func(*StorageState) error,
	prepareCommit func() (func(bool), error),
) error {
	if l == nil || l.account == nil || l.pool == nil || update == nil || prepareCommit == nil {
		return fmt.Errorf("account lease is not initialized")
	}
	l.operation.Lock()
	defer l.operation.Unlock()
	if l.released {
		return fmt.Errorf("account lease has been released")
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

// BeginAuthRefresh acquires an exclusive auth refresh window for the current account
func (l *AccountLease) BeginAuthRefresh() (func(), bool) {
	if l == nil || l.account == nil || l.pool == nil {
		return nil, false
	}
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
	var once sync.Once
	return func() {
		once.Do(func() {
			l.pool.mu.Lock()
			if l.account.authRefreshers > 0 {
				l.account.authRefreshers--
			}
			l.pool.notifyLocked()
			l.pool.mu.Unlock()
		})
	}, true
}

// SaveConfig atomically writes back the fixed account configuration within the lease
func (l *AccountLease) SaveConfig(value AccountConfig) error {
	if l == nil || l.account == nil || l.pool == nil {
		return fmt.Errorf("account lease is not initialized")
	}
	l.operation.Lock()
	defer l.operation.Unlock()
	if l.released {
		return fmt.Errorf("account lease has been released")
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

// ReloadStorageState reloads the storage state within the lease
func (l *AccountLease) ReloadStorageState() (StorageState, error) {
	if l == nil || l.account == nil || l.pool == nil {
		return StorageState{}, fmt.Errorf("account lease is not initialized")
	}
	l.operation.Lock()
	defer l.operation.Unlock()
	if l.released {
		return StorageState{}, fmt.Errorf("account lease has been released")
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

// ReplaceCookies replaces the account's latest persistent state with the fixed-fingerprint browser's current cookies
func (l *AccountLease) ReplaceCookies(cookies []StateCookie) error {
	if l == nil || l.account == nil || l.pool == nil {
		return fmt.Errorf("account lease is not initialized")
	}
	if len(cookies) == 0 {
		return nil
	}
	l.operation.Lock()
	defer l.operation.Unlock()
	if l.released {
		return fmt.Errorf("account lease has been released")
	}
	l.account.storageMu.Lock()
	defer l.account.storageMu.Unlock()
	state, err := LoadStorageState(l.account.StoragePath)
	if err != nil {
		return err
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

// BindResource binds a resource to the current lease account
func (l *AccountLease) BindResource(resourceID string, kind string) error {
	if l == nil || l.account == nil || l.pool == nil {
		return fmt.Errorf("account lease is not initialized")
	}
	l.operation.Lock()
	defer l.operation.Unlock()
	if l.released {
		return fmt.Errorf("account lease has been released")
	}
	l.account.storageMu.Lock()
	defer l.account.storageMu.Unlock()
	return l.pool.BindResourceKind(resourceID, l.account.ID, kind)
}

// BindVideoOperation saves video operation account and public object metadata
func (l *AccountLease) BindVideoOperation(
	ctx context.Context,
	resourceID string,
	metadata VideoResourceMetadata,
) (ResourceBinding, error) {
	if l == nil || l.account == nil || l.pool == nil {
		return ResourceBinding{}, fmt.Errorf("account lease is not initialized")
	}
	l.operation.Lock()
	defer l.operation.Unlock()
	if l.released {
		return ResourceBinding{}, fmt.Errorf("account lease has been released")
	}
	l.account.storageMu.Lock()
	defer l.account.storageMu.Unlock()
	return l.pool.bindVideoOperation(ctx, resourceID, l.account.ID, metadata)
}

// VideoOperationBinding returns video operation metadata held by the current account
func (l *AccountLease) VideoOperationBinding(resourceID string) (ResourceBinding, error) {
	if l == nil || l.account == nil || l.pool == nil {
		return ResourceBinding{}, fmt.Errorf("account lease is not initialized")
	}
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

// ReplaceResource atomically replaces a single resource binding of the current lease account
func (l *AccountLease) ReplaceResource(previousResourceID string, resourceID string, kind string) error {
	if l == nil || l.account == nil || l.pool == nil {
		return fmt.Errorf("account lease is not initialized")
	}
	l.operation.Lock()
	defer l.operation.Unlock()
	if l.released {
		return fmt.Errorf("account lease has been released")
	}
	l.account.storageMu.Lock()
	defer l.account.storageMu.Unlock()
	return l.pool.replaceResource(previousResourceID, resourceID, l.account.ID, kind)
}

// MergeSetCookieHeaders merges response cookies into the account's latest persistent state
func (l *AccountLease) MergeSetCookieHeaders(headers []string, requestURL string, now time.Time) error {
	if l == nil || l.account == nil || l.pool == nil {
		return fmt.Errorf("account lease is not initialized")
	}
	l.operation.Lock()
	defer l.operation.Unlock()
	if l.released {
		return fmt.Errorf("account lease has been released")
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

// Release releases account file and in-process leases
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
			l.account.leasePath = ""
		}
		l.pool.notifyLocked()
		l.pool.mu.Unlock()
	})
	return l.err
}

// SetCatalog replaces the benefit tier and live model catalog of the account
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
			account.Models = cloneAccountModels(models)
			account.initializedAt = time.Now()
			if account.State == AccountUnavailable {
				account.State = AccountReady
				account.stateMessage = ""
			}
		}, nil
	})
	if err != nil {
		return err
	}
	return nil
}

// MarkModelAccessVerifiedIfGeneration records successful model generation in the current catalog generation
func (p *AccountPool) MarkModelAccessVerifiedIfGeneration(
	accountID string,
	modelID string,
	generation uint64,
	checkedAt time.Time,
) (bool, error) {
	return p.markModelAccessVerified(accountID, modelID, generation, checkedAt)
}

// ForgetModelAccessVerifiedIfGeneration deletes expired model success records in the current catalog generation
func (p *AccountPool) ForgetModelAccessVerifiedIfGeneration(
	accountID string,
	modelID string,
	generation uint64,
	checkedAt time.Time,
) (bool, error) {
	modelID = strings.TrimPrefix(strings.TrimSpace(modelID), "models/")
	if modelID == "" {
		return false, fmt.Errorf("model ID cannot be empty")
	}
	checkedAt = checkedAt.UTC()
	if checkedAt.IsZero() {
		return false, fmt.Errorf("model access check time cannot be zero")
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

func (p *AccountPool) markModelAccessVerified(
	accountID string,
	modelID string,
	generation uint64,
	checkedAt time.Time,
) (bool, error) {
	modelID = strings.TrimPrefix(strings.TrimSpace(modelID), "models/")
	if modelID == "" {
		return false, fmt.Errorf("model ID cannot be empty")
	}
	checkedAt = checkedAt.UTC()
	if checkedAt.IsZero() {
		return false, fmt.Errorf("model access check time cannot be zero")
	}
	p.mu.Lock()
	account := p.byID[accountID]
	if account == nil {
		p.mu.Unlock()
		return false, fmt.Errorf("account not found: %s", accountID)
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

// ResetModelAccess clears tested model qualifications of the account
func (p *AccountPool) ResetModelAccess(accountID string) error {
	_, err := p.updateRuntime(accountID, func(account *Account, runtimeState *accountRuntimeState) (bool, func(*Account), error) {
		if len(runtimeState.ModelAccess) == 0 {
			return false, func(account *Account) { account.modelAccessGeneration++ }, nil
		}
		clear(runtimeState.ModelAccess)
		return true, func(account *Account) { account.modelAccessGeneration++ }, nil
	})
	if err != nil {
		return err
	}
	return nil
}

// CandidateStates returns the benefit tier and real-time load of candidate accounts
func (p *AccountPool) CandidateStates(accountIDs []string, modelID string) map[string]AccountCandidateState {
	return p.CandidateStatesForScope(accountIDs, modelID, "")
}

// CandidateStatesForScope returns candidate account states for the specified qualification scope
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
		result[accountID] = AccountCandidateState{
			ModelAccess:   modelAccessState(account, modelAccessScope),
			Active:        account.active,
			AvailableSlot: max(0, p.perAccountConcurrency-account.active),
		}
	}
	return result
}

// PreferWarmPool round-robins initial warm accounts stratified by benefit tier
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

// MarkCooldownIfGeneration records scope cooldown in the current catalog generation
func (p *AccountPool) MarkCooldownIfGeneration(
	accountID string,
	modelAccessScope string,
	generation uint64,
	checkedAt time.Time,
	until time.Time,
	reason string,
) error {
	if !until.After(time.Now()) {
		return fmt.Errorf("cooldown expiration must be in the future")
	}
	checkedAt = checkedAt.UTC()
	if checkedAt.IsZero() {
		return fmt.Errorf("cooldown check time cannot be zero")
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

// ClearCooldownIfGeneration clears scope cooldown in the current catalog generation
func (p *AccountPool) ClearCooldownIfGeneration(
	accountID string,
	modelAccessScope string,
	generation uint64,
	checkedAt time.Time,
) error {
	checkedAt = checkedAt.UTC()
	if checkedAt.IsZero() {
		return fmt.Errorf("cooldown check time cannot be zero")
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

// BindResource binds a resource ID to the creator account
func (p *AccountPool) BindResource(resourceID string, accountID string) error {
	return p.BindResourceKind(resourceID, accountID, "")
}

// BindResourceKind binds a typed resource ID to the creator account
func (p *AccountPool) BindResourceKind(resourceID string, accountID string, kind string) error {
	resourceID = strings.TrimSpace(resourceID)
	if resourceID == "" {
		return fmt.Errorf("resource ID cannot be empty")
	}
	_, err := p.updateRuntime(accountID, func(_ *Account, runtimeState *accountRuntimeState) (bool, func(*Account), error) {
		if owner, exists := p.resources[resourceID]; exists && owner != accountID {
			return false, nil, fmt.Errorf("resource %s is already bound to account %s", resourceID, owner)
		}
		if _, exists := runtimeState.Resources[resourceID]; exists {
			return false, nil, nil
		}
		runtimeState.Resources[resourceID] = ResourceBinding{Kind: strings.TrimSpace(kind), CreatedAt: time.Now().UTC()}
		return true, nil, nil
	})
	if err != nil {
		return err
	}
	return nil
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
		return ResourceBinding{}, fmt.Errorf("video operation metadata is incomplete")
	}
	var bound ResourceBinding
	_, err := p.updateRuntimeContext(ctx, accountID, func(_ *Account, runtimeState *accountRuntimeState) (bool, func(*Account), error) {
		if owner, exists := p.resources[resourceID]; exists && owner != accountID {
			return false, nil, fmt.Errorf("resource %s is already bound to account %s", resourceID, owner)
		}
		if existing, exists := runtimeState.Resources[resourceID]; exists {
			if existing.Kind != "video-operation" || existing.Video == nil {
				return false, nil, fmt.Errorf("resource %s is not a video operation", resourceID)
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

func (p *AccountPool) replaceResource(previousResourceID string, resourceID string, accountID string, kind string) error {
	previousResourceID = strings.TrimSpace(previousResourceID)
	resourceID = strings.TrimSpace(resourceID)
	kind = strings.TrimSpace(kind)
	if resourceID == "" {
		return fmt.Errorf("resource ID cannot be empty")
	}
	_, err := p.updateRuntime(accountID, func(_ *Account, runtimeState *accountRuntimeState) (bool, func(*Account), error) {
		for _, candidate := range []string{previousResourceID, resourceID} {
			if candidate == "" {
				continue
			}
			if owner, exists := p.resources[candidate]; exists && owner != accountID {
				return false, nil, fmt.Errorf("resource %s is already bound to account %s", candidate, owner)
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
	if err != nil {
		return err
	}
	return nil
}

// UnbindResource deletes account mapping for terminal resources
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
	if err != nil {
		return err
	}
	return nil
}

// MarkAuthRequired marks an account as requiring re-authentication
func (p *AccountPool) MarkAuthRequired(accountID string, reason string) error {
	return p.setAccountState(accountID, AccountAuthRequired, reason)
}

// MarkUnavailable marks an account as failed during initialization or runtime
func (p *AccountPool) MarkUnavailable(accountID string, reason string) error {
	return p.setAccountState(accountID, AccountUnavailable, reason)
}

// MarkReady restores an account to schedulable state
func (p *AccountPool) MarkReady(accountID string) error {
	return p.setAccountState(accountID, AccountReady, "")
}

// Status returns the sanitized status of the account pool
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
		} else if account.exclusive || account.authRefreshers > 0 || account.active > 0 {
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
			ID:          account.ID,
			Label:       account.Config.Label,
			State:       state,
			Enabled:     account.Config.Enabled,
			Proxy:       account.Config.Proxy,
			Locale:      account.Config.Locale,
			Timezone:    account.Config.Timezone,
			Models:      models,
			BenefitTier: account.BenefitTier.String(),
			Cooldowns:   cloneCooldowns(account.runtime.Cooldowns),
			Message:     account.stateMessage,
		}
		if !account.LastUsed.IsZero() {
			status.LastUsed = timePointer(account.LastUsed)
		}
		statuses = append(statuses, status)
	}
	return statuses
}

// ClassifyCandidates classifies candidate accounts for the target request according to the warm pool
func (p *AccountPool) ClassifyCandidates(
	ctx context.Context,
	selection AccountSelection,
	warmAccountIDs []string,
) (AccountCandidateGroups, error) {
	if p == nil {
		return AccountCandidateGroups{}, ErrNoEligibleAccount
	}
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
		if attempt == 0 && (!groups.Eligible || !available && !groups.EarliestCooldown.IsZero()) {
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
	modelID := selection.ModelID
	modelAccessScope := selectionAccessScope(selection)
	if modelID != "" {
		if !p.hasModelCatalogLocked() {
			return AccountCandidateGroups{}, ErrNoEligibleAccount
		}
		if !p.hasModelLocked(modelID) {
			return AccountCandidateGroups{}, fmt.Errorf("%w: %s", ErrModelNotFound, modelID)
		}
		if selection.Method != "" && !p.hasModelMethodLocked(modelID, selection.Method) {
			return AccountCandidateGroups{}, fmt.Errorf("%w: model %s does not support %s", ErrModelNotFound, modelID, selection.Method)
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
		if modelID != "" && !accountSupportsSelection(account, selection) {
			continue
		}
		groups.Eligible = true
		if cooldown, active := accountCooldown(account, modelAccessScope, now); active {
			if groups.EarliestCooldown.IsZero() || cooldown.Until.Before(groups.EarliestCooldown) {
				groups.EarliestCooldown = cooldown.Until
			}
			continue
		}
		_, isWarm := warm[account.ID]
		switch {
		case isWarm && !account.exclusive && account.authRefreshers == 0 && account.active == 0:
			groups.WarmReady = append(groups.WarmReady, account.ID)
		case isWarm && !account.exclusive && account.authRefreshers == 0 && account.active < p.perAccountConcurrency:
			groups.WarmAvailable = append(groups.WarmAvailable, account.ID)
		case isWarm:
			groups.WarmBusy = append(groups.WarmBusy, account.ID)
		case account.exclusive || account.authRefreshers > 0 || account.active > 0:
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
		if selection.ModelID != "" && !accountSupportsSelection(account, selection) {
			continue
		}
		waitable = true
		if account.exclusive || account.authRefreshers > 0 || account.active >= p.perAccountConcurrency {
			continue
		}
		if selection.ResourceID == "" {
			if cooldown, active := accountCooldown(account, selectionAccessScope(selection), now); active {
				if earliest.IsZero() || cooldown.Until.Before(earliest) {
					earliest = cooldown.Until
				}
				continue
			}
		}
		refreshRuntime := false
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
			refreshRuntime = leaseLock != nil
		}
		account.active++
		account.LastUsed = now
		p.lastPicked[selectionAccessScope(selection)] = account.ID
		return &AccountLease{
			pool: p, account: account, authGeneration: account.authGeneration,
			modelAccessGeneration: account.modelAccessGeneration, refreshRuntime: refreshRuntime, checkedAt: now.UTC(),
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

func (p *AccountPool) hasModelCapabilityLocked(modelID string, capability string) bool {
	for _, account := range p.accounts {
		if account == nil {
			continue
		}
		for _, model := range account.Models {
			if modelMatchesID(model, modelID) && model.Capabilities[capability] {
				return true
			}
		}
	}
	return false
}

// BootstrapModels returns WAA initialization models from the account's live catalog
func (p *AccountPool) BootstrapModels(accountID string) ([]string, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	account := p.byID[strings.TrimSpace(accountID)]
	if account == nil {
		return nil, fmt.Errorf("account not found: %s", accountID)
	}
	models := accountBootstrapModels(account)
	if len(models) == 0 {
		return nil, fmt.Errorf("no usable WAA bootstrap model found in live catalog for account %s", account.ID)
	}
	return models, nil
}

// BootstrapModel returns the general WAA bootstrap model used by the account
func (p *AccountPool) BootstrapModel(accountID string) (string, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	account := p.byID[strings.TrimSpace(accountID)]
	if account == nil {
		return "", fmt.Errorf("account not found: %s", accountID)
	}
	models := accountBootstrapModels(account)
	if len(models) > 0 {
		return models[0], nil
	}
	return "", fmt.Errorf("no usable WAA bootstrap model found in live catalog for account %s", account.ID)
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
			!modelAllowedByTier(model, account.BenefitTier) || !model.Capabilities["chat_model"] {
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
			return nil, fmt.Errorf("resource %s is bound to account %s", selection.ResourceID, owner)
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
	for index, account := range p.accounts {
		if account == nil {
			continue
		}
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

// SetRoutingStrategy sets the account round-robin or fill-first routing strategy
func (p *AccountPool) SetRoutingStrategy(strategy string) {
	p.mu.Lock()
	p.routingStrategy = strategy
	p.mu.Unlock()
}

// OrderCandidates orders candidate accounts according to the current strategy without advancing the round-robin position
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
		return fmt.Errorf("account not found: %s", accountID)
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
		return nil, fmt.Errorf("resolve account directory: %w", err)
	}
	id := filepath.Base(directory)
	if id == "." || id == string(filepath.Separator) || strings.TrimSpace(id) == "" {
		return nil, fmt.Errorf("account directory missing stable ID")
	}
	configPath := filepath.Join(directory, accountConfigName)
	storagePath := filepath.Join(directory, storageStateName)
	accountConfig, err := readAccountConfig(configPath)
	if err != nil {
		return nil, fmt.Errorf("account %s: %w", id, err)
	}
	state, err := LoadStorageState(storagePath)
	if err != nil {
		return nil, fmt.Errorf("account %s: %w", id, err)
	}
	runtimePath := filepath.Join(directory, runtimeStateName)
	runtimeState, err := readRuntime(runtimePath)
	if err != nil {
		return nil, fmt.Errorf("account %s: %w", id, err)
	}
	return &Account{
		ID:           id,
		Directory:    directory,
		ConfigPath:   configPath,
		StoragePath:  storagePath,
		RuntimePath:  runtimePath,
		Config:       accountConfig,
		StorageState: state,
		BenefitTier:  runtimeState.BenefitTier,
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
		return AccountConfig{}, fmt.Errorf("read %s: %w", accountConfigName, err)
	}
	defer file.Close()
	decoder := json.NewDecoder(file)
	decoder.DisallowUnknownFields()
	var value AccountConfig
	if err := decoder.Decode(&value); err != nil {
		return AccountConfig{}, fmt.Errorf("parse %s: %w", accountConfigName, err)
	}
	if err := ensureJSONEnd(decoder); err != nil {
		return AccountConfig{}, fmt.Errorf("parse %s: %w", accountConfigName, err)
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
		return fmt.Errorf("encode %s: %w", accountConfigName, err)
	}
	return atomicWriteFile(filePath, append(data, '\n'), 0o600)
}

func readRuntime(filePath string) (accountRuntimeState, error) {
	value := accountRuntimeState{
		Cooldowns:   make(map[string]CooldownState),
		Resources:   make(map[string]ResourceBinding),
		ModelAccess: make(map[string]ModelAccess),
	}
	file, err := os.Open(filePath)
	if os.IsNotExist(err) {
		return value, nil
	}
	if err != nil {
		return accountRuntimeState{}, fmt.Errorf("read %s: %w", runtimeStateName, err)
	}
	defer file.Close()
	decoder := json.NewDecoder(file)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&value); err != nil {
		return accountRuntimeState{}, fmt.Errorf("parse %s: %w", runtimeStateName, err)
	}
	if err := ensureJSONEnd(decoder); err != nil {
		return accountRuntimeState{}, fmt.Errorf("parse %s: %w", runtimeStateName, err)
	}
	if value.Cooldowns == nil {
		value.Cooldowns = make(map[string]CooldownState)
	}
	if value.Resources == nil {
		value.Resources = make(map[string]ResourceBinding)
	}
	if value.ModelAccess == nil {
		value.ModelAccess = make(map[string]ModelAccess)
	}
	return value, nil
}

func writeRuntime(filePath string, value accountRuntimeState) error {
	if filePath == "" {
		return nil
	}
	data, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return fmt.Errorf("encode %s: %w", runtimeStateName, err)
	}
	return atomicWriteFile(filePath, append(data, '\n'), 0o600)
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
		return false, fmt.Errorf("account not found: %s", accountID)
	}

	account.runtimeMu.Lock()
	defer account.runtimeMu.Unlock()
	p.mu.Lock()
	currentAccount := p.byID[account.ID]
	p.mu.Unlock()
	if currentAccount != account {
		return false, fmt.Errorf("account not found: %s", account.ID)
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
	if account.RuntimePath == "" {
		current.BenefitTier = account.BenefitTier
	} else {
		current, err = readRuntime(account.RuntimePath)
		if err != nil {
			return false, err
		}
	}
	p.mu.Lock()
	if p.byID[account.ID] != account {
		p.mu.Unlock()
		return false, fmt.Errorf("account not found: %s", account.ID)
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
		if err := writeRuntime(account.RuntimePath, working); err != nil {
			return false, err
		}
	}

	p.mu.Lock()
	defer p.mu.Unlock()
	if p.byID[account.ID] != account {
		return false, fmt.Errorf("account not found: %s", account.ID)
	}
	synced, err := p.syncAccountRuntimeLocked(account, working)
	if err != nil {
		return false, err
	}
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
			return false, fmt.Errorf("resource %s is already bound to account %s", resourceID, owner)
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
	if account == nil || account.RuntimePath == "" {
		return nil, nil
	}
	accountDirectory := account.Directory
	if accountDirectory == "" {
		accountDirectory = filepath.Dir(account.RuntimePath)
	}
	leaseDirectory := filepath.Join(filepath.Dir(accountDirectory), ".leases")
	if err := os.MkdirAll(leaseDirectory, 0o700); err != nil {
		return nil, fmt.Errorf("create account state lock directory: %w", err)
	}
	lock := flock.New(filepath.Join(leaseDirectory, filepath.Base(accountDirectory)+".runtime.lock"))
	lockCtx, cancel := context.WithTimeout(ctx, runtimeLockLimit)
	defer cancel()
	_, err := lock.TryLockContext(lockCtx, runtimeLockPoll)
	if err != nil {
		return nil, fmt.Errorf("lock account runtime state: %w", err)
	}
	return lock, nil
}

func validatePersistentAccountFiles(account *Account) error {
	if account == nil || account.ConfigPath == "" {
		return nil
	}
	for _, filePath := range []string{account.ConfigPath, account.StoragePath} {
		if strings.TrimSpace(filePath) == "" {
			return fmt.Errorf("%w: %s", ErrAccountNotFound, account.ID)
		}
		info, err := os.Stat(filePath)
		if err != nil {
			if os.IsNotExist(err) {
				return fmt.Errorf("%w: %s", ErrAccountNotFound, account.ID)
			}
			return fmt.Errorf("read account persistent file: %w", err)
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
	current := cloneRuntime(account.runtime)
	if account.RuntimePath == "" {
		current.BenefitTier = account.BenefitTier
	} else {
		current, err = readRuntime(account.RuntimePath)
		if err != nil {
			return err
		}
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.byID[account.ID] != account {
		return fmt.Errorf("%w: %s", ErrAccountNotFound, account.ID)
	}
	changed, err := p.syncAccountRuntimeLocked(account, current)
	if changed {
		p.notifyLocked()
	}
	return err
}

// accountRuntimeRefreshResult stores the runtime state refresh result for a single account
type accountRuntimeRefreshResult struct {
	account *Account
	err     error
}

// refreshAccountRuntimes concurrently refreshes runtime states of independent accounts
func (p *AccountPool) refreshAccountRuntimes(ctx context.Context, accounts []*Account) []accountRuntimeRefreshResult {
	results := make([]accountRuntimeRefreshResult, len(accounts))
	var refreshes sync.WaitGroup
	for index, account := range accounts {
		if account == nil {
			continue
		}
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
	if exists && owner != nil {
		p.mu.Unlock()
		if err := p.refreshAccountRuntime(ctx, owner); err != nil {
			if errors.Is(err, ErrAccountNotFound) {
				p.markStaleAccountUnavailable(owner)
				return fmt.Errorf("%w: %s", ErrResourceNotFound, resourceID)
			}
			return err
		}
		return nil
	}
	accounts := append([]*Account(nil), p.accounts...)
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
	p.mu.Lock()
	_, found := p.resources[resourceID]
	p.mu.Unlock()
	if found {
		return nil
	}
	return errors.Join(failures...)
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
		if account == nil || !account.Config.Enabled || account.State != AccountReady {
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
	catalog := cloneAccountModels(models)
	sort.Slice(catalog, func(left int, right int) bool {
		return catalog[left].ID < catalog[right].ID
	})
	data, err := json.Marshal(struct {
		Tier   BenefitTier `json:"tier"`
		Models []Model     `json:"models"`
	}{Tier: tier, Models: catalog})
	if err != nil {
		return "", fmt.Errorf("encode account model catalog fingerprint: %w", err)
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

func acquireAccountFileLease(storagePath string) (*flock.Flock, string, error) {
	if storagePath == "" {
		return nil, "", nil
	}
	accountDirectory := filepath.Dir(storagePath)
	leaseDirectory := filepath.Join(filepath.Dir(accountDirectory), ".leases")
	if err := os.MkdirAll(leaseDirectory, 0o700); err != nil {
		return nil, "", fmt.Errorf("create account lease directory: %w", err)
	}
	leasePath := filepath.Join(leaseDirectory, filepath.Base(accountDirectory)+".lock")
	leaseLock := flock.New(leasePath)
	locked, err := leaseLock.TryLock()
	if err != nil {
		return nil, leasePath, fmt.Errorf("lock account lease: %w", err)
	}
	if !locked {
		return nil, leasePath, errAccountLeaseBusy
	}
	return leaseLock, leasePath, nil
}

func acquireAccountPublishLease(account *Account, validate bool) (*AccountPublishLease, error) {
	if account == nil || strings.TrimSpace(account.ID) == "" {
		return nil, fmt.Errorf("account is not initialized")
	}
	requestLock, _, err := acquireAccountFileLease(account.StoragePath)
	if errors.Is(err, errAccountLeaseBusy) {
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

// Release ends the publishing window for a new account runtime
func (lease *AccountPublishLease) Release() error {
	if lease == nil || lease.account == nil {
		return nil
	}
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

// AcquireAccountRuntimeLease locks the account WAA runtime under the current user
func AcquireAccountRuntimeLease(accountID string) (*AccountRuntimeLease, error) {
	accountID, err := normalizeAccountEmail(accountID)
	if err != nil {
		return nil, err
	}
	cacheRoot, err := os.UserCacheDir()
	if err != nil {
		return nil, fmt.Errorf("read user cache directory: %w", err)
	}
	directory := filepath.Join(cacheRoot, "AIStudio2API", "runtime-leases")
	if err := os.MkdirAll(directory, 0o700); err != nil {
		return nil, fmt.Errorf("create WAA runtime lease directory: %w", err)
	}
	lock := flock.New(filepath.Join(directory, accountID+".lock"))
	locked, err := lock.TryLock()
	if err != nil {
		return nil, fmt.Errorf("lock account WAA runtime: %w", err)
	}
	if !locked {
		return nil, fmt.Errorf("%w: %s is already in use by another AIStudio2API runtime", ErrAccountLeased, accountID)
	}
	return &AccountRuntimeLease{lock: lock}, nil
}

// Release releases the account WAA runtime lock
func (lease *AccountRuntimeLease) Release() error {
	if lease == nil || lease.lock == nil {
		return nil
	}
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
		return fmt.Errorf("file contains multiple JSON values")
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
		return "", fmt.Errorf("account must have a Google email")
	}
	id := strings.ToLower(strings.TrimSpace(address.Address))
	if id == "." || id == ".." || strings.ContainsAny(id, `<>:"/\|?*`) {
		return "", fmt.Errorf("account email cannot be used as directory name: %s", id)
	}
	return id, nil
}

func cloneAccountModels(models []Model) []Model {
	result := make([]Model, len(models))
	for index, model := range models {
		result[index] = model
		result[index].Methods = append([]string(nil), model.Methods...)
		result[index].AccessModes = append([]int64(nil), model.AccessModes...)
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
