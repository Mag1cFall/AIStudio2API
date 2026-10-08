package app

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/Mag1cFall/AIStudio2API/internal/aistudio"
	"github.com/Mag1cFall/AIStudio2API/internal/api"
)

// Embed 跟踪 Build 通道 embedding 使用的账户、通道与输入 token
func (service *trackedService) Embed(ctx context.Context, request aistudio.EmbeddingRequest) (aistudio.EmbeddingResult, error) {
	api.SetAccessLogTarget(ctx, request.Model, "")
	api.StartAccessLog(ctx)
	request.Model = service.pool.CanonicalModelID(request.Model)
	modelID := strings.TrimPrefix(strings.TrimSpace(request.Model), "models/")
	requestCtx, cancel, err := service.dataRequestContext(ctx)
	if err != nil {
		api.SetAccessLogError(ctx, err)
		return aistudio.EmbeddingResult{}, err
	}
	defer cancel()
	service.requests.start(aistudio.GenerateRequest{ID: request.ID, Model: request.Model}, cancel)
	request.CandidateAccountIDs, err = service.embeddingCandidates(requestCtx, modelID)
	if err != nil {
		api.SetAccessLogError(requestCtx, err)
		service.requests.finish(request.ID, finalRequestState(err), err)
		return aistudio.EmbeddingResult{}, err
	}
	workerGenerations := make(map[string]uint64)
	recoveredWorkers := make(map[string]struct{})
	accountLabel := func(accountID string) string {
		for _, status := range service.pool.Status() {
			if status.ID == accountID {
				return status.Label
			}
		}
		return accountID
	}
	request.ObserveAccountFailure = func(accountID string, cause error) {
		service.requests.log(accountLabel(accountID), "WARN", fmt.Sprintf(
			"账号切换 | 模型=%s\n原因: %s", modelID, strings.TrimSpace(cause.Error()),
		))
	}
	request.RecoverWAARuntime = func(recoveryCtx context.Context, accountID string, cause error) (bool, error) {
		workerFailed := service.workers.WorkerFailed(accountID)
		workerReplaced := errors.Is(cause, errAccountWorkerReplaced)
		if recoveryCtx.Err() != nil || !needsWAARuntimeRecovery(cause, false, workerFailed, workerReplaced) {
			return false, nil
		}
		recovered, _, recoveryErr := service.recoverWorkerOnce(
			recoveryCtx, accountID, workerGenerations[accountID], recoveredWorkers,
			true, workerFailed || aistudio.DefinitiveWAARuntimeFailure(cause),
		)
		if recovered && recoveryErr == nil {
			service.requests.log(accountLabel(accountID), "WARN", "WAA Worker 重建 | 模型="+modelID+" | 重放当前请求")
		}
		return recovered, recoveryErr
	}
	observed := aistudio.ContextWithAccountSelectionObserver(requestCtx, func(account *aistudio.Account) {
		workerGenerations[account.ID] = service.workers.WorkerGeneration(account.ID)
		api.SetAccessLogChannel(requestCtx, string(aistudio.ChannelBuild))
		api.SetAccessLogTarget(requestCtx, modelID, account.Config.Label)
		api.MarkAccessLogScheduled(requestCtx)
		service.requests.markRunning(request.ID, account.ID, account.Config.Label)
		service.requests.markChannel(request.ID, string(aistudio.ChannelBuild))
	})
	embeddings, ok := service.service.(aistudio.EmbeddingService)
	if !ok {
		err = fmt.Errorf("embedding service 不可用")
		api.SetAccessLogError(requestCtx, err)
		service.requests.finish(request.ID, finalRequestState(err), err)
		return aistudio.EmbeddingResult{}, err
	}
	result, err := embeddings.Embed(observed, request)
	if err == nil {
		api.SetAccessLogGenerationResult(requestCtx, &aistudio.Usage{InputTokens: result.TokenCount, TotalTokens: result.TokenCount}, 0)
	}
	api.SetAccessLogError(requestCtx, err)
	service.requests.finish(request.ID, finalRequestState(err), err)
	return result, err
}

// embeddingCandidates 按已有 Worker 优先的顺序列出 Build 通道支持 embedding 模型的账户
func (service *trackedService) embeddingCandidates(ctx context.Context, modelID string) ([]string, error) {
	groups, err := service.pool.ClassifyCandidates(
		ctx, aistudio.AccountSelection{ModelID: modelID, Method: "embedContent"}, service.workers.WarmAccountIDs(),
	)
	if err != nil {
		return nil, err
	}
	standbyReady, readyBusyErr := service.workers.runtimeAvailable(groups.StandbyReady)
	standbyBusy, standbyBusyErr := service.workers.runtimeAvailable(groups.StandbyBusy)
	candidates := service.pool.OrderCandidates(append(append([]string(nil), groups.WarmReady...), groups.WarmAvailable...), modelID)
	candidates = append(candidates, service.pool.OrderCandidates(standbyReady, modelID)...)
	candidates = append(candidates, service.pool.OrderCandidates(groups.WarmBusy, modelID)...)
	candidates = append(candidates, service.pool.OrderCandidates(standbyBusy, modelID)...)
	if len(candidates) == 0 {
		if !groups.EarliestCooldown.IsZero() {
			return nil, &aistudio.AllCoolingError{ModelID: modelID, Until: groups.EarliestCooldown}
		}
		if busyErr := errors.Join(readyBusyErr, standbyBusyErr); busyErr != nil {
			return nil, busyErr
		}
		return nil, aistudio.ErrNoEligibleAccount
	}
	return candidates, nil
}

// Embed 由当前生成服务执行 embedding
func (manager *runtimeManager) Embed(ctx context.Context, request aistudio.EmbeddingRequest) (aistudio.EmbeddingResult, error) {
	manager.mu.RLock()
	defer manager.mu.RUnlock()
	service, ok := manager.current.service.(aistudio.EmbeddingService)
	if !ok {
		return aistudio.EmbeddingResult{}, fmt.Errorf("embedding service 不可用")
	}
	return service.Embed(ctx, request)
}

// DoProtectedBuild 在认证失败后续签同一账户并重放 Build 代理请求
func (transport *authRetryProtectedTransport) DoProtectedBuild(
	ctx context.Context,
	selection aistudio.AccountSelection,
	page string,
	rpc aistudio.RPCRequest,
) (*aistudio.RPCResponse, error) {
	buildTransport, ok := transport.transport.(aistudio.BuildProtectedTransport)
	if !ok {
		return nil, fmt.Errorf("protected transport 不支持 Build 代理请求")
	}
	return transport.refresher.do(ctx, rpc.Method, func() (*aistudio.RPCResponse, error) {
		return buildTransport.DoProtectedBuild(ctx, selection, page, rpc)
	})
}

var _ aistudio.EmbeddingService = (*trackedService)(nil)
var _ aistudio.EmbeddingService = (*runtimeManager)(nil)
var _ aistudio.BuildProtectedTransport = (*authRetryProtectedTransport)(nil)
