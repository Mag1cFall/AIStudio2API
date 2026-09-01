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

// managedService 表示可整体替换的生成服务
type managedService interface {
	aistudio.Service
	State() string
}

// runtimeGeneration 保存同一份配置装配的完整运行时
type runtimeGeneration struct {
	service         managedService
	admin           api.AdminService
	config          config.Config
	lifecycleCancel context.CancelFunc
	closeRuntime    func() error
}

// runtimeFactory 创建一个完整生成服务实例
type runtimeFactory func(context.Context, context.Context, config.Config, *requestRegistry) (*runtimeGeneration, error)

// cancelLifecycle 取消当前生成服务实例的全部后台操作
func (generation *runtimeGeneration) cancelLifecycle() {
	generation.lifecycleCancel()
}

// Close 取消当前生成服务实例并释放运行时
func (generation *runtimeGeneration) Close() error {
	generation.cancelLifecycle()
	return generation.closeRuntime()
}

// dataConfigOverrides 保存当前进程的命令行生成服务配置覆盖
type dataConfigOverrides struct {
	authStates *string
	proxy      *string
}

// Apply 将命令行覆盖应用到新实例配置
func (overrides dataConfigOverrides) Apply(cfg *config.Config) {
	if overrides.authStates != nil {
		cfg.AuthStates = *overrides.authStates
	}
	if overrides.proxy != nil {
		cfg.Proxy = *overrides.proxy
	}
}

// runtimeManager 在固定管理监听器内切换完整生成服务
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

// newRuntimeManager 创建进程级管理器与初始生成服务
func newRuntimeManager(
	ctx context.Context,
	configPath string,
	cfg config.Config,
	overrides dataConfigOverrides,
) (*runtimeManager, error) {
	requests := newRequestRegistry(ctx)
	manager := &runtimeManager{
		lifecycle: ctx, configPath: configPath, activeManagement: cfg,
		overrides: overrides, requests: requests, factory: buildRuntimeGeneration,
	}
	generation, err := manager.factory(ctx, ctx, cfg, requests)
	if err != nil {
		return nil, err
	}
	manager.current = generation
	return manager, nil
}

// buildRuntimeGeneration 从配置快照创建账户池、Worker 与协议运行时
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
		service: service, admin: admin, config: cfg,
		lifecycleCancel: lifecycleCancel, closeRuntime: closeRuntime,
	}, nil
}

// StartService 从最新配置创建并启动新生成服务
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
		manager.requests.log("service", "WARN", "旧生成服务关闭失败 | 错误="+err.Error())
	}
	return status, startErr
}

// finishStart 清理本轮生成服务启动取消句柄
func (manager *runtimeManager) finishStart(cancel context.CancelFunc) {
	manager.mu.Lock()
	manager.startCancel = nil
	manager.mu.Unlock()
	cancel()
}

// StopService 停止当前生成服务并保持管理监听器运行
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

// Close 释放当前生成服务
func (manager *runtimeManager) Close() error {
	manager.mu.Lock()
	defer manager.mu.Unlock()
	return manager.current.Close()
}

// Models 返回当前生成服务模型
func (manager *runtimeManager) Models(ctx context.Context) ([]aistudio.Model, error) {
	manager.mu.RLock()
	defer manager.mu.RUnlock()
	return manager.current.service.Models(ctx)
}

// CountTokens 由当前生成服务计数
func (manager *runtimeManager) CountTokens(ctx context.Context, request aistudio.TokenCountRequest) (aistudio.TokenCount, error) {
	manager.mu.RLock()
	defer manager.mu.RUnlock()
	return manager.current.service.CountTokens(ctx, request)
}

// Generate 由当前生成服务生成事件流
func (manager *runtimeManager) Generate(ctx context.Context, request aistudio.GenerateRequest) (<-chan aistudio.Event, error) {
	manager.mu.RLock()
	defer manager.mu.RUnlock()
	return manager.current.service.Generate(ctx, request)
}

// GenerateVideo 由当前生成服务创建视频任务
func (manager *runtimeManager) GenerateVideo(ctx context.Context, request aistudio.VideoRequest) (aistudio.VideoOperation, error) {
	manager.mu.RLock()
	defer manager.mu.RUnlock()
	service, ok := manager.current.service.(aistudio.VideoService)
	if !ok {
		return aistudio.VideoOperation{}, fmt.Errorf("video service 不可用")
	}
	return service.GenerateVideo(ctx, request)
}

// GetGenerateVideoOperation 由当前生成服务读取视频任务
func (manager *runtimeManager) GetGenerateVideoOperation(ctx context.Context, id string) (aistudio.VideoOperation, error) {
	manager.mu.RLock()
	defer manager.mu.RUnlock()
	service, ok := manager.current.service.(aistudio.VideoService)
	if !ok {
		return aistudio.VideoOperation{}, fmt.Errorf("video service 不可用")
	}
	return service.GetGenerateVideoOperation(ctx, id)
}

// DownloadFile 由当前生成服务下载文件
func (manager *runtimeManager) DownloadFile(ctx context.Context, id string) (aistudio.MediaStream, error) {
	manager.mu.RLock()
	defer manager.mu.RUnlock()
	service, ok := manager.current.service.(aistudio.VideoService)
	if !ok {
		return aistudio.MediaStream{}, fmt.Errorf("video service 不可用")
	}
	return service.DownloadFile(ctx, id)
}

// UploadFile 由当前生成服务上传文件
func (manager *runtimeManager) UploadFile(ctx context.Context, request aistudio.UploadRequest) (aistudio.FileRef, error) {
	manager.mu.RLock()
	defer manager.mu.RUnlock()
	service, ok := manager.current.service.(aistudio.FileService)
	if !ok {
		return aistudio.FileRef{}, fmt.Errorf("file service 不可用")
	}
	return service.UploadFile(ctx, request)
}

// FileMetadata 由当前生成服务读取文件元数据
func (manager *runtimeManager) FileMetadata(ctx context.Context, id string) (aistudio.FileMetadata, error) {
	manager.mu.RLock()
	defer manager.mu.RUnlock()
	service, ok := manager.current.service.(aistudio.FileService)
	if !ok {
		return aistudio.FileMetadata{}, fmt.Errorf("file service 不可用")
	}
	return service.FileMetadata(ctx, id)
}

// DeleteFile 由当前生成服务删除文件
func (manager *runtimeManager) DeleteFile(ctx context.Context, id string) error {
	manager.mu.RLock()
	defer manager.mu.RUnlock()
	service, ok := manager.current.service.(aistudio.FileService)
	if !ok {
		return fmt.Errorf("file service 不可用")
	}
	return service.DeleteFile(ctx, id)
}

// OpenBidi 由当前生成服务创建实时会话
func (manager *runtimeManager) OpenBidi(ctx context.Context, request aistudio.BidiRequest) (*aistudio.BidiSession, error) {
	manager.mu.RLock()
	defer manager.mu.RUnlock()
	service, ok := manager.current.service.(aistudio.BidiService)
	if !ok {
		return nil, fmt.Errorf("bidi service 不可用")
	}
	return service.OpenBidi(ctx, request)
}

// Transcribe 由当前生成服务执行音频转录
func (manager *runtimeManager) Transcribe(ctx context.Context, request aistudio.TranscriptionRequest) (aistudio.TranscriptionResult, error) {
	manager.mu.RLock()
	defer manager.mu.RUnlock()
	service, ok := manager.current.service.(aistudio.TranscriptionService)
	if !ok {
		return aistudio.TranscriptionResult{}, fmt.Errorf("transcription service 不可用")
	}
	return service.Transcribe(ctx, request)
}

// Status 返回当前生成服务状态
func (manager *runtimeManager) Status(ctx context.Context) (api.AdminStatus, error) {
	manager.mu.RLock()
	defer manager.mu.RUnlock()
	return manager.current.admin.Status(ctx)
}

// Accounts 返回当前生成服务账户
func (manager *runtimeManager) Accounts(ctx context.Context) ([]api.AdminAccount, error) {
	manager.mu.RLock()
	defer manager.mu.RUnlock()
	return manager.current.admin.Accounts(ctx)
}

// CreateAccount 在当前生成服务创建账户
func (manager *runtimeManager) CreateAccount(ctx context.Context, input api.AccountCreateInput) (api.AdminAccount, error) {
	manager.mu.RLock()
	defer manager.mu.RUnlock()
	return manager.current.admin.CreateAccount(ctx, input)
}

// ChromeImportProfiles 返回当前生成服务可导入的 Chrome 账号
func (manager *runtimeManager) ChromeImportProfiles(ctx context.Context) ([]api.ChromeImportProfile, error) {
	manager.mu.RLock()
	defer manager.mu.RUnlock()
	return manager.current.admin.ChromeImportProfiles(ctx)
}

// ImportChromeAccounts 在当前生成服务批量导入 Chrome 账号
func (manager *runtimeManager) ImportChromeAccounts(ctx context.Context, input api.ChromeImportInput) ([]api.AdminAccount, error) {
	manager.mu.RLock()
	defer manager.mu.RUnlock()
	return manager.current.admin.ImportChromeAccounts(ctx, input)
}

// UpdateAccount 在当前生成服务更新账户
func (manager *runtimeManager) UpdateAccount(ctx context.Context, id string, input api.AccountInput) (api.AdminAccount, error) {
	manager.mu.RLock()
	defer manager.mu.RUnlock()
	return manager.current.admin.UpdateAccount(ctx, id, input)
}

// DeleteAccount 在当前生成服务删除账户
func (manager *runtimeManager) DeleteAccount(ctx context.Context, id string) error {
	manager.mu.RLock()
	defer manager.mu.RUnlock()
	return manager.current.admin.DeleteAccount(ctx, id)
}

// LoginAccount 在当前生成服务登录账户
func (manager *runtimeManager) LoginAccount(ctx context.Context, id string) (api.AdminAccount, error) {
	manager.mu.RLock()
	defer manager.mu.RUnlock()
	return manager.current.admin.LoginAccount(ctx, id)
}

// VerifyAccount 在当前生成服务验证账户
func (manager *runtimeManager) VerifyAccount(ctx context.Context, id string) (api.AdminAccount, error) {
	manager.mu.RLock()
	defer manager.mu.RUnlock()
	return manager.current.admin.VerifyAccount(ctx, id)
}

// ClearLogs 清空进程级管理日志
func (manager *runtimeManager) ClearLogs(context.Context) error {
	manager.requests.clearLogs()
	return nil
}

// RuntimeConfig 返回已保存配置与进程级生效状态
func (manager *runtimeManager) RuntimeConfig(ctx context.Context) (api.RuntimeConfig, error) {
	manager.mu.RLock()
	value, err := manager.current.admin.RuntimeConfig(ctx)
	if err == nil {
		value = manager.decorateRuntimeConfig(value, manager.current.config)
	}
	manager.mu.RUnlock()
	return value, err
}

// UpdateRuntimeConfig 保存下一次启动生成服务时使用的配置
func (manager *runtimeManager) UpdateRuntimeConfig(ctx context.Context, value api.RuntimeConfig) (api.RuntimeConfig, error) {
	manager.mu.RLock()
	updated, err := manager.current.admin.UpdateRuntimeConfig(ctx, value)
	if err == nil {
		updated = manager.decorateRuntimeConfig(updated, manager.current.config)
	}
	manager.mu.RUnlock()
	return updated, err
}

// Cooldowns 返回当前生成服务冷却状态
func (manager *runtimeManager) Cooldowns(ctx context.Context) ([]api.AdminCooldown, error) {
	manager.mu.RLock()
	defer manager.mu.RUnlock()
	return manager.current.admin.Cooldowns(ctx)
}

// Requests 返回进程级活动请求
func (manager *runtimeManager) Requests(ctx context.Context) ([]api.AdminRequest, error) {
	manager.mu.RLock()
	defer manager.mu.RUnlock()
	return manager.current.admin.Requests(ctx)
}

// CancelRequest 取消进程级活动请求
func (manager *runtimeManager) CancelRequest(ctx context.Context, id string) error {
	manager.mu.RLock()
	defer manager.mu.RUnlock()
	return manager.current.admin.CancelRequest(ctx, id)
}

// Events 创建生成服务实例切换期间持续可用的管理事件流
func (manager *runtimeManager) Events(ctx context.Context) (<-chan api.AdminEvent, error) {
	return openAdminEvents(ctx, manager.lifecycle, manager.requests, manager)
}

// RecordAccessStart 记录公开 API 请求开始
func (manager *runtimeManager) RecordAccessStart(entry api.AccessLog) {
	manager.mu.RLock()
	manager.current.admin.RecordAccessStart(entry)
	manager.mu.RUnlock()
}

// RecordAccessLog 记录公开 API 请求结果
func (manager *runtimeManager) RecordAccessLog(entry api.AccessLog) {
	manager.mu.RLock()
	manager.current.admin.RecordAccessLog(entry)
	manager.mu.RUnlock()
}

// decorateRuntimeConfig 标记配置所属的进程级与生成服务生效时机
func (manager *runtimeManager) decorateRuntimeConfig(value api.RuntimeConfig, active config.Config) api.RuntimeConfig {
	value.ActiveListenAddr = manager.activeManagement.ListenAddr
	value.ActiveAPIKey = manager.activeManagement.ProxyAPIKey
	value.ManagementRestartRequired = value.ListenAddr != value.ActiveListenAddr || value.APIKey != value.ActiveAPIKey
	value.ServiceRestartRequired = !sameDataConfig(value, active, manager.overrides)
	return value
}

// sameDataConfig 比较已保存配置与当前生成服务配置
func sameDataConfig(value api.RuntimeConfig, active config.Config, overrides dataConfigOverrides) bool {
	initTimeout, initErr := time.ParseDuration(value.InitTimeout)
	requestTimeout, requestErr := time.ParseDuration(value.RequestTimeout)
	if initErr != nil || requestErr != nil {
		return false
	}
	saved := config.Config{
		AuthStates: value.AuthStates, Proxy: value.Proxy,
		InitTimeout: initTimeout, RequestTimeout: requestTimeout,
		WarmWorkerLimit: value.WarmWorkerLimit, MaxActiveWorkers: value.MaxActiveWorkers,
		WarmStartupConcurrency: value.WarmStartupConcurrency,
		PerAccountConcurrency:  value.PerAccountConcurrency, TemporaryChat: value.TemporaryChat,
	}
	overrides.Apply(&saved)
	return saved.AuthStates == active.AuthStates && saved.Proxy == active.Proxy &&
		saved.InitTimeout == active.InitTimeout && saved.RequestTimeout == active.RequestTimeout &&
		saved.WarmWorkerLimit == active.WarmWorkerLimit && saved.MaxActiveWorkers == active.MaxActiveWorkers &&
		saved.WarmStartupConcurrency == active.WarmStartupConcurrency &&
		saved.PerAccountConcurrency == active.PerAccountConcurrency && saved.TemporaryChat == active.TemporaryChat
}

var _ aistudio.Service = (*runtimeManager)(nil)
var _ aistudio.VideoService = (*runtimeManager)(nil)
var _ aistudio.FileService = (*runtimeManager)(nil)
var _ aistudio.BidiService = (*runtimeManager)(nil)
var _ aistudio.TranscriptionService = (*runtimeManager)(nil)
var _ api.AdminService = (*runtimeManager)(nil)
