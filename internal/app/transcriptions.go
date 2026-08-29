package app

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/Mag1cFall/AIStudio2API/internal/aistudio"
	"github.com/Mag1cFall/AIStudio2API/internal/api"
)

// Transcribe 跟踪音频转录使用的账户与生成结果
func (service *trackedService) Transcribe(
	ctx context.Context,
	request aistudio.TranscriptionRequest,
) (aistudio.TranscriptionResult, error) {
	api.SetAccessLogTarget(ctx, request.Model, "")
	api.SetAccessLogGenerationConfig(ctx, request.Config)
	api.StartAccessLog(ctx)
	requestCtx, cancel, err := service.dataRequestContext(ctx)
	if err != nil {
		api.SetAccessLogError(ctx, err)
		service.requests.start(aistudio.GenerateRequest{ID: request.ID, Model: request.Model}, func() {})
		service.requests.finish(request.ID, "failed", err)
		return aistudio.TranscriptionResult{}, err
	}
	service.requests.start(aistudio.GenerateRequest{
		ID: request.ID, Model: request.Model, Config: request.Config,
	}, cancel)
	request.CandidateAccountIDs, err = service.transcriptionCandidates(requestCtx, request.Model)
	if err != nil {
		api.SetAccessLogError(requestCtx, err)
		service.requests.finish(request.ID, finalRequestState(err), err)
		cancel()
		return aistudio.TranscriptionResult{}, err
	}
	workerGenerations := make(map[string]uint64)
	recoveredWorkers := make(map[string]struct{})
	request.ObserveAccountFailure = func(accountID string, cause error) {
		label := accountID
		for _, status := range service.pool.Status() {
			if status.ID == accountID {
				label = status.Label
				break
			}
		}
		service.requests.log(label, "WARN", fmt.Sprintf(
			"账号切换 | 模型=%s\n原因: %s",
			strings.TrimPrefix(request.Model, "models/"), strings.TrimSpace(cause.Error()),
		))
	}
	request.RecoverWAARuntime = func(recoveryCtx context.Context, accountID string, cause error) (bool, error) {
		if !aistudio.TranscriptionGenerationFailure(cause) {
			return false, nil
		}
		workerFailed := service.workers.WorkerFailed(accountID)
		workerReplaced := errors.Is(cause, errAccountWorkerReplaced)
		if recoveryCtx.Err() != nil || !needsWAARuntimeRecovery(cause, false, workerFailed, workerReplaced) {
			return false, nil
		}
		waaRuntimeFailed := aistudio.DefinitiveWAARuntimeFailure(cause)
		expectedGeneration := workerGenerations[accountID]
		recovered, currentGeneration, recoveryErr := service.recoverWorkerOnce(
			accountID, expectedGeneration, recoveredWorkers, true, workerFailed || waaRuntimeFailed,
		)
		if recoveryErr != nil {
			return false, recoveryErr
		}
		if !recovered {
			return false, nil
		}
		if !currentGeneration {
			return true, nil
		}
		label := accountID
		for _, status := range service.pool.Status() {
			if status.ID == accountID {
				label = status.Label
				break
			}
		}
		modelID := strings.TrimPrefix(request.Model, "models/")
		if workerFailed || waaRuntimeFailed {
			service.requests.log(label, "WARN", "WAA Worker 重建 | 模型="+modelID)
		}
		service.requests.log(label, "WARN", "WAA Worker 已更新 | 模型="+modelID+" | 重放当前请求")
		return true, nil
	}
	observed := aistudio.ContextWithAccountSelectionObserver(requestCtx, func(account *aistudio.Account) {
		workerGenerations[account.ID] = service.workers.WorkerGeneration(account.ID)
		api.SetAccessLogTarget(requestCtx, request.Model, account.Config.Label)
		service.requests.markRunning(request.ID, account.ID, account.Config.Label)
	})
	transcriptions, ok := service.service.(aistudio.TranscriptionService)
	if !ok {
		err = fmt.Errorf("transcription service 不可用")
	} else {
		var result aistudio.TranscriptionResult
		result, err = transcriptions.Transcribe(observed, request)
		api.SetAccessLogFirstEvent(requestCtx, result.FirstEvent)
		api.SetAccessLogGenerationResult(
			requestCtx, result.FirstContent, len(result.Text), result.Usage.OutputTokens, result.Usage.ReasoningTokens,
		)
		api.SetAccessLogTarget(requestCtx, result.ProviderModel, "")
		api.SetAccessLogFinishReason(requestCtx, result.FinishReason)
		api.SetAccessLogError(requestCtx, err)
		service.requests.finish(request.ID, finalRequestState(err), err)
		cancel()
		return result, err
	}
	api.SetAccessLogError(requestCtx, err)
	service.requests.finish(request.ID, finalRequestState(err), err)
	cancel()
	return aistudio.TranscriptionResult{}, err
}

func (service *trackedService) transcriptionCandidates(ctx context.Context, model string) ([]string, error) {
	modelID := strings.TrimPrefix(strings.TrimSpace(model), "models/")
	groups, err := service.pool.ClassifyCandidates(
		ctx,
		aistudio.AccountSelection{
			ModelID: modelID, ModelAccessScope: modelID,
			Method: "generateContent", Capability: "transcription_output",
		},
		service.workers.WarmAccountIDs(),
	)
	if err != nil {
		return nil, err
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	warmAvailable := append(append([]string(nil), groups.WarmReady...), groups.WarmAvailable...)
	candidates := service.preferFast(warmAvailable, modelID)
	candidates = append(candidates, service.preferFast(groups.StandbyReady, modelID)...)
	candidates = append(candidates, service.preferFast(groups.WarmBusy, modelID)...)
	candidates = append(candidates, service.preferFast(groups.StandbyBusy, modelID)...)
	if len(candidates) == 0 {
		return nil, aistudio.ErrNoEligibleAccount
	}
	return candidates, nil
}

var _ aistudio.TranscriptionService = (*trackedService)(nil)
