package app

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/Mag1cFall/AIStudio2API/internal/aistudio"
	"github.com/Mag1cFall/AIStudio2API/internal/api"
	"github.com/Mag1cFall/AIStudio2API/internal/config"
)

// managedService represents a hot-swappable generation service.
type managedService interface {
	aistudio.Service
	State() string
}

// runtimeGeneration holds a complete runtime assembled from a single configuration snapshot.
type runtimeGeneration struct {
	service         managedService
	admin           api.AdminService
	config          config.Config
	lifecycleCancel context.CancelFunc
	closeRuntime    func() error
}

// runtimeFactory creates a complete generation service instance.
type runtimeFactory func(context.Context, context.Context, config.Config, *requestRegistry) (*runtimeGeneration, error)

// cancelLifecycle cancels all background operations of the current generation instance.
func (generation *runtimeGeneration) cancelLifecycle() {
	generation.lifecycleCancel()
}

// Close cancels the current generation instance and releases runtime resources.
func (generation *runtimeGeneration) Close() error {
	generation.cancelLifecycle()
	return generation.closeRuntime()
}

// dataConfigOverrides stores command-line overrides for generation service configuration.
type dataConfigOverrides struct {
	authStates *string
	proxy      *string
}

// Apply applies command-line overrides to the target configuration.
func (overrides dataConfigOverrides) Apply(cfg *config.Config) {
	if overrides.authStates != nil {
		cfg.AuthStates = *overrides.authStates
	}
	if overrides.proxy != nil {
		cfg.Proxy = *overrides.proxy
	}
}

// runtimeManager hot-swaps generation services within a persistent admin listener.
type runtimeManager struct {
	lifecycle        context.Context
	configPath       string
	activeManagement config.Config
	overrides        dataConfigOverrides
	requests         *requestRegistry
	factory          runtimeFactory
	startMu          sync.Mutex
	mu               sync.RWMutex
	current          *runtimeGeneration
	startCancel      context.CancelFunc
}

// newRuntimeManager creates a process-level manager and initializes the first generation service.
func newRuntimeManager(
	ctx context.Context,
	configPath string,
	cfg config.Config,
	overrides dataConfigOverrides,
) (*runtimeManager, error) {
	requests := newRequestRegistry(ctx)

	manager := &runtimeManager{
		lifecycle:        ctx,
		configPath:       configPath,
		activeManagement: cfg,
		overrides:        overrides,
		requests:         requests,
		factory:          buildRuntimeGeneration,
	}

	generation, err := manager.factory(ctx, ctx, cfg, requests)
	if err != nil {
		return nil, err
	}

	manager.current = generation
	return manager, nil
}

// buildRuntimeGeneration creates an account pool, workers, and protocol runtimes from a configuration snapshot.
func buildRuntimeGeneration(
	launchCtx context.Context,
	parentLifecycle context.Context,
	cfg config.Config,
	requests *requestRegistry,
) (*runtimeGeneration, error) {
	lifecycle, lifecycleCancel := context.WithCancel(parentLifecycle)

	service, admin, closeRuntime, err := newRuntime(launchCtx, lifecycle, cfg, requests)
	if err != nil {
		lifecycleCancel()
		return nil, err
	}

	return &runtimeGeneration{
		service:         service,
		admin:           admin,
		config:          cfg,
		lifecycleCancel: lifecycleCancel,
		closeRuntime:    closeRuntime,
	}, nil
}

// StartService creates and starts a new generation service from the latest configuration.
func (manager *runtimeManager) StartService(ctx context.Context) (api.AdminStatus, error) {
	manager.startMu.Lock()
	defer manager.startMu.Unlock()

	manager.mu.Lock()
	current := manager.current

	if current.service.State() != "STOPPED" {
		status, err := current.admin.StartService(ctx)
		manager.mu.Unlock()
		return status, err
	}

	if _, err := current.admin.StopService(ctx); err != nil {
		manager.mu.Unlock()
		return api.AdminStatus{}, err
	}

	launchCtx, launchCancel := context.WithCancel(manager.lifecycle)
	manager.startCancel = launchCancel
	manager.mu.Unlock()

	cfg, err := config.Load(manager.configPath)
	if err != nil {
		manager.finishStart(launchCancel)
		return api.AdminStatus{}, err
	}

	manager.overrides.Apply(&cfg)
	if err := cfg.Validate(); err != nil {
		manager.finishStart(launchCancel)
		return api.AdminStatus{}, err
	}

	next, err := manager.factory(launchCtx, manager.lifecycle, cfg, manager.requests)
	if err != nil {
		manager.finishStart(launchCancel)
		return api.AdminStatus{}, err
	}

	if launchCtx.Err() != nil {
		manager.finishStart(launchCancel)
		_ = next.Close()
		return manager.Status(ctx)
	}

	manager.mu.Lock()
	current = manager.current
	manager.current = next
	manager.mu.Unlock()

	current.cancelLifecycle()
	status, startErr := next.admin.StartService(launchCtx)
	manager.finishStart(launchCancel)

	if err := current.Close(); err != nil {
		manager.requests.log("service", "WARN", "Failed to close previous generation service | error="+err.Error())
	}

	return status, startErr
}

// finishStart cleans up the start cancellation handle for the current cycle.
func (manager *runtimeManager) finishStart(cancel context.CancelFunc) {
	manager.mu.Lock()
	manager.startCancel = nil
	manager.mu.Unlock()

	cancel()
}

// StopService stops the current generation service while keeping the admin listener active.
func (manager *runtimeManager) StopService(ctx context.Context) (api.AdminStatus, error) {
	manager.mu.RLock()
	cancel := manager.startCancel
	current := manager.current
	manager.mu.RUnlock()

	if cancel != nil {
		cancel()
	}

	return current.admin.StopService(ctx)
}

// Close releases the current generation service.
func (manager *runtimeManager) Close() error {
	manager.mu.Lock()
	defer manager.mu.Unlock()

	return manager.current.Close()
}

// Models returns the current generation service models.
func (manager *runtimeManager) Models(ctx context.Context) ([]aistudio.Model, error) {
	manager.mu.RLock()
	defer manager.mu.RUnlock()

	return manager.current.service.Models(ctx)
}

// CountTokens counts tokens using the current generation service.
func (manager *runtimeManager) CountTokens(ctx context.Context, request aistudio.TokenCountRequest) (aistudio.TokenCount, error) {
	manager.mu.RLock()
	defer manager.mu.RUnlock()

	return manager.current.service.CountTokens(ctx, request)
}

// Generate generates an event stream using the current generation service.
func (manager *runtimeManager) Generate(ctx context.Context, request aistudio.GenerateRequest) (<-chan aistudio.Event, error) {
	manager.mu.RLock()
	defer manager.mu.RUnlock()

	return manager.current.service.Generate(ctx, request)
}

// GenerateVideo creates a video generation task using the current generation service.
func (manager *runtimeManager) GenerateVideo(ctx context.Context, request aistudio.VideoRequest) (aistudio.VideoOperation, error) {
	manager.mu.RLock()
	defer manager.mu.RUnlock()

	service, ok := manager.current.service.(aistudio.VideoService)
	if !ok {
		return aistudio.VideoOperation{}, fmt.Errorf("video service is unavailable")
	}

	return service.GenerateVideo(ctx, request)
}

// GetGenerateVideoOperation retrieves a video task using the current generation service.
func (manager *runtimeManager) GetGenerateVideoOperation(ctx context.Context, id string) (aistudio.VideoOperation, error) {
	manager.mu.RLock()
	defer manager.mu.RUnlock()

	service, ok := manager.current.service.(aistudio.VideoService)
	if !ok {
		return aistudio.VideoOperation{}, fmt.Errorf("video service is unavailable")
	}

	return service.GetGenerateVideoOperation(ctx, id)
}

// DownloadFile downloads a file using the current generation service.
func (manager *runtimeManager) DownloadFile(ctx context.Context, id string) (aistudio.MediaStream, error) {
	manager.mu.RLock()
	defer manager.mu.RUnlock()

	service, ok := manager.current.service.(aistudio.VideoService)
	if !ok {
		return aistudio.MediaStream{}, fmt.Errorf("video service is unavailable")
	}

	return service.DownloadFile(ctx, id)
}

// UploadFile uploads a file using the current generation service.
func (manager *runtimeManager) UploadFile(ctx context.Context, request aistudio.UploadRequest) (aistudio.FileRef, error) {
	manager.mu.RLock()
	defer manager.mu.RUnlock()

	service, ok := manager.current.service.(aistudio.FileService)
	if !ok {
		return aistudio.FileRef{}, fmt.Errorf("file service is unavailable")
	}

	return service.UploadFile(ctx, request)
}

// FileMetadata reads file metadata using the current generation service.
func (manager *runtimeManager) FileMetadata(ctx context.Context, id string) (aistudio.FileMetadata, error) {
	manager.mu.RLock()
	defer manager.mu.RUnlock()

	service, ok := manager.current.service.(aistudio.FileService)
	if !ok {
		return aistudio.FileMetadata{}, fmt.Errorf("file service is unavailable")
	}

	return service.FileMetadata(ctx, id)
}

// DeleteFile deletes a file using the current generation service.
func (manager *runtimeManager) DeleteFile(ctx context.Context, id string) error {
	manager.mu.RLock()
	defer manager.mu.RUnlock()

	service, ok := manager.current.service.(aistudio.FileService)
	if !ok {
		return fmt.Errorf("file service is unavailable")
	}

	return service.DeleteFile(ctx, id)
}

// OpenBidi establishes a bidirectional streaming session using the current generation service.
func (manager *runtimeManager) OpenBidi(ctx context.Context, request aistudio.BidiRequest) (*aistudio.BidiSession, error) {
	manager.mu.RLock()
	defer manager.mu.RUnlock()

	service, ok := manager.current.service.(aistudio.BidiService)
	if !ok {
		return nil, fmt.Errorf("bidi service is unavailable")
	}

	return service.OpenBidi(ctx, request)
}

// Transcribe performs audio transcription using the current generation service.
func (manager *runtimeManager) Transcribe(ctx context.Context, request aistudio.TranscriptionRequest) (aistudio.TranscriptionResult, error) {
	manager.mu.RLock()
	defer manager.mu.RUnlock()

	service, ok := manager.current.service.(aistudio.TranscriptionService)
	if !ok {
		return aistudio.TranscriptionResult{}, fmt.Errorf("transcription service is unavailable")
	}

	return service.Transcribe(ctx, request)
}

// Status returns the status of the current generation service.
func (manager *runtimeManager) Status(ctx context.Context) (api.AdminStatus, error) {
	manager.mu.RLock()
	defer manager.mu.RUnlock()

	return manager.current.admin.Status(ctx)
}

// Accounts returns accounts managed by the current generation service.
func (manager *runtimeManager) Accounts(ctx context.Context) ([]api.AdminAccount, error) {
	manager.mu.RLock()
	defer manager.mu.RUnlock()

	return manager.current.admin.Accounts(ctx)
}

// CreateAccount creates an account in the current generation service.
func (manager *runtimeManager) CreateAccount(ctx context.Context, input api.AccountCreateInput) (api.AdminAccount, error) {
	manager.mu.RLock()
	defer manager.mu.RUnlock()

	return manager.current.admin.CreateAccount(ctx, input)
}

// ChromeImportProfiles returns importable Chrome profiles for the current generation service.
func (manager *runtimeManager) ChromeImportProfiles(ctx context.Context) ([]api.ChromeImportProfile, error) {
	manager.mu.RLock()
	defer manager.mu.RUnlock()

	return manager.current.admin.ChromeImportProfiles(ctx)
}

// ImportChromeAccounts imports Chrome accounts in bulk into the current generation service.
func (manager *runtimeManager) ImportChromeAccounts(ctx context.Context, input api.ChromeImportInput) ([]api.AdminAccount, error) {
	manager.mu.RLock()
	defer manager.mu.RUnlock()

	return manager.current.admin.ImportChromeAccounts(ctx, input)
}

// UpdateAccount updates an account in the current generation service.
func (manager *runtimeManager) UpdateAccount(ctx context.Context, id string, input api.AccountInput) (api.AdminAccount, error) {
	manager.mu.RLock()
	defer manager.mu.RUnlock()

	return manager.current.admin.UpdateAccount(ctx, id, input)
}

// DeleteAccount deletes an account from the current generation service.
func (manager *runtimeManager) DeleteAccount(ctx context.Context, id string) error {
	manager.mu.RLock()
	defer manager.mu.RUnlock()

	return manager.current.admin.DeleteAccount(ctx, id)
}

// LoginAccount logs in an account in the current generation service.
func (manager *runtimeManager) LoginAccount(ctx context.Context, id string) (api.AdminAccount, error) {
	manager.mu.RLock()
	defer manager.mu.RUnlock()

	return manager.current.admin.LoginAccount(ctx, id)
}

// VerifyAccount verifies an account in the current generation service.
func (manager *runtimeManager) VerifyAccount(ctx context.Context, id string) (api.AdminAccount, error) {
	manager.mu.RLock()
	defer manager.mu.RUnlock()

	return manager.current.admin.VerifyAccount(ctx, id)
}

// ClearLogs clears process-level admin logs.
func (manager *runtimeManager) ClearLogs(context.Context) error {
	manager.requests.clearLogs()
	return nil
}

// RuntimeConfig returns the saved configuration along with process-level active states.
func (manager *runtimeManager) RuntimeConfig(ctx context.Context) (api.RuntimeConfig, error) {
	manager.mu.RLock()
	value, err := manager.current.admin.RuntimeConfig(ctx)
	if err == nil {
		value = manager.decorateRuntimeConfig(value, manager.current.config)
	}
	manager.mu.RUnlock()

	return value, err
}

// UpdateRuntimeConfig saves configuration to be used on the next generation service launch.
func (manager *runtimeManager) UpdateRuntimeConfig(ctx context.Context, value api.RuntimeConfig) (api.RuntimeConfig, error) {
	manager.mu.RLock()
	updated, err := manager.current.admin.UpdateRuntimeConfig(ctx, value)
	if err == nil {
		updated = manager.decorateRuntimeConfig(updated, manager.current.config)
	}
	manager.mu.RUnlock()

	return updated, err
}

// Cooldowns returns the cooldown states for the current generation service.
func (manager *runtimeManager) Cooldowns(ctx context.Context) ([]api.AdminCooldown, error) {
	manager.mu.RLock()
	defer manager.mu.RUnlock()

	return manager.current.admin.Cooldowns(ctx)
}

// Requests returns active requests across the process.
func (manager *runtimeManager) Requests(ctx context.Context) ([]api.AdminRequest, error) {
	manager.mu.RLock()
	defer manager.mu.RUnlock()

	return manager.current.admin.Requests(ctx)
}

// CancelRequest cancels an active request by ID.
func (manager *runtimeManager) CancelRequest(ctx context.Context, id string) error {
	manager.mu.RLock()
	defer manager.mu.RUnlock()

	return manager.current.admin.CancelRequest(ctx, id)
}

// Events creates an admin event stream that persists across generation service restarts.
func (manager *runtimeManager) Events(ctx context.Context) (<-chan api.AdminEvent, error) {
	return openAdminEvents(ctx, manager.lifecycle, manager.requests, manager)
}

// RecordAccessStart records the start of a public API request.
func (manager *runtimeManager) RecordAccessStart(entry api.AccessLog) {
	manager.mu.RLock()
	manager.current.admin.RecordAccessStart(entry)
	manager.mu.RUnlock()
}

// RecordAccessLog records the result of a public API request.
func (manager *runtimeManager) RecordAccessLog(entry api.AccessLog) {
	manager.mu.RLock()
	manager.current.admin.RecordAccessLog(entry)
	manager.mu.RUnlock()
}

// decorateRuntimeConfig marks whether changes require a management or service restart.
func (manager *runtimeManager) decorateRuntimeConfig(value api.RuntimeConfig, active config.Config) api.RuntimeConfig {
	value.ActiveListenAddr = manager.activeManagement.ListenAddr
	value.ActiveAPIKey = manager.activeManagement.ProxyAPIKey
	value.ManagementRestartRequired = value.ListenAddr != value.ActiveListenAddr || value.APIKey != value.ActiveAPIKey
	value.ServiceRestartRequired = !sameDataConfig(value, active, manager.overrides)

	return value
}

// sameDataConfig compares saved configuration with active generation service configuration.
func sameDataConfig(value api.RuntimeConfig, active config.Config, overrides dataConfigOverrides) bool {
	initTimeout, initErr := time.ParseDuration(value.InitTimeout)
	requestTimeout, requestErr := time.ParseDuration(value.RequestTimeout)
	if initErr != nil || requestErr != nil {
		return false
	}

	saved := config.Config{
		AuthStates:             value.AuthStates,
		Proxy:                  value.Proxy,
		InitTimeout:            initTimeout,
		RequestTimeout:         requestTimeout,
		WarmWorkerLimit:        value.WarmWorkerLimit,
		MaxActiveWorkers:       value.MaxActiveWorkers,
		WarmStartupConcurrency: value.WarmStartupConcurrency,
		PerAccountConcurrency:  value.PerAccountConcurrency,
		TemporaryChat:          value.TemporaryChat,
		RoutingStrategy:        value.RoutingStrategy,
	}

	overrides.Apply(&saved)

	return saved.AuthStates == active.AuthStates &&
		saved.Proxy == active.Proxy &&
		saved.InitTimeout == active.InitTimeout &&
		saved.RequestTimeout == active.RequestTimeout &&
		saved.WarmWorkerLimit == active.WarmWorkerLimit &&
		saved.MaxActiveWorkers == active.MaxActiveWorkers &&
		saved.WarmStartupConcurrency == active.WarmStartupConcurrency &&
		saved.PerAccountConcurrency == active.PerAccountConcurrency &&
		saved.TemporaryChat == active.TemporaryChat &&
		saved.RoutingStrategy == active.RoutingStrategy
}

var _ aistudio.Service = (*runtimeManager)(nil)
var _ aistudio.VideoService = (*runtimeManager)(nil)
var _ aistudio.FileService = (*runtimeManager)(nil)
var _ aistudio.BidiService = (*runtimeManager)(nil)
var _ aistudio.TranscriptionService = (*runtimeManager)(nil)
var _ api.AdminService = (*runtimeManager)(nil)
