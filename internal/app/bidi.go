package app

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/Mag1cFall/AIStudio2API/internal/aistudio"
	"github.com/Mag1cFall/AIStudio2API/internal/api"
)

// OpenBidi 将实时会话绑定到当前生成服务生命周期
func (service *trackedService) OpenBidi(ctx context.Context, request aistudio.BidiRequest) (*aistudio.BidiSession, error) {
	api.SetAccessLogTarget(ctx, request.Model, "")
	requestCtx, cancel, err := service.bidiRequestContext(ctx)
	if err != nil {
		api.SetAccessLogError(ctx, err)
		return nil, err
	}
	if request.AccountID == "" && len(request.AllowedAccountIDs) == 0 {
		request.AllowedAccountIDs, err = service.bidiCandidates(requestCtx, request.Model, request.ModelAccessScope)
		if err != nil {
			api.SetAccessLogError(ctx, err)
			cancel()
			return nil, err
		}
	}
	workerGenerations := make(map[string]uint64)
	recoveredWorkers := make(map[string]struct{})
	request.ObserveWAARuntime = func(accountID string, generation uint64) {
		workerGenerations[accountID] = generation
	}
	request.ObserveModelAccessChange = service.publishModelAccess
	request.ObserveModelAccessFailure = func(accountID string) {
		service.forgetPerformance(accountID, request.Model)
	}
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
		workerFailed := service.workers.WorkerFailed(accountID)
		waaRuntimeFailed := aistudio.DefinitiveWAARuntimeFailure(cause)
		workerReplaced := errors.Is(cause, errAccountWorkerReplaced)
		recoverCurrentGeneration := needsWAARuntimeRecovery(cause, false, workerFailed, workerReplaced)
		if recoveryCtx.Err() != nil || !recoverCurrentGeneration {
			return false, nil
		}
		expectedGeneration := workerGenerations[accountID]
		recovered, _, recoveryErr := service.recoverWorkerOnce(
			accountID, expectedGeneration, recoveredWorkers,
			recoverCurrentGeneration, workerFailed || waaRuntimeFailed,
		)
		return recovered, recoveryErr
	}
	requestCtx = aistudio.ContextWithAccountSelectionObserver(requestCtx, func(account *aistudio.Account) {
		workerGenerations[account.ID] = service.workers.WorkerGeneration(account.ID)
		api.SetAccessLogTarget(ctx, request.Model, account.Config.Label)
	})
	bidi, ok := service.service.(aistudio.BidiService)
	if !ok {
		cancel()
		return nil, fmt.Errorf("bidi service 不可用")
	}
	session, err := bidi.OpenBidi(requestCtx, request)
	if err != nil {
		api.SetAccessLogError(ctx, err)
		cancel()
		return nil, err
	}
	return session, nil
}

// WorkerGeneration 返回当前 preparer 对应的 Worker 版本号
func (preparer *accountWorkerPreparer) WorkerGeneration() uint64 {
	return preparer.account.generation.Load()
}

func (service *trackedService) bidiCandidates(ctx context.Context, model string, modelAccessScope string) ([]string, error) {
	modelID := strings.TrimPrefix(strings.TrimSpace(model), "models/")
	groups, err := service.pool.ClassifyCandidates(
		ctx,
		aistudio.AccountSelection{
			ModelID: modelID, ModelAccessScope: modelAccessScope, Method: "bidiGenerateContent",
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
	candidates := service.preferBidiAccounts(warmAvailable, modelID, modelAccessScope)
	candidates = append(candidates, service.preferBidiAccounts(groups.StandbyReady, modelID, modelAccessScope)...)
	candidates = append(candidates, service.preferBidiAccounts(groups.WarmBusy, modelID, modelAccessScope)...)
	candidates = append(candidates, service.preferBidiAccounts(groups.StandbyBusy, modelID, modelAccessScope)...)
	if len(candidates) == 0 {
		return nil, aistudio.ErrNoEligibleAccount
	}
	return candidates, nil
}

// preferBidiAccounts 按实测资格与实时负载排列账户
func (service *trackedService) preferBidiAccounts(accountIDs []string, modelID string, modelAccessScope string) []string {
	candidates := service.preferFast(accountIDs, modelID)
	if strings.TrimSpace(modelAccessScope) == "" {
		return candidates
	}
	states := service.pool.CandidateStatesForScope(candidates, modelID, modelAccessScope)
	verified := make([]string, 0, len(candidates))
	unverified := make([]string, 0, len(candidates))
	for _, accountID := range candidates {
		if states[accountID].ModelAccess == aistudio.ModelAccessVerified {
			verified = append(verified, accountID)
			continue
		}
		unverified = append(unverified, accountID)
	}
	return append(verified, unverified...)
}

func (service *trackedService) bidiRequestContext(ctx context.Context) (context.Context, context.CancelFunc, error) {
	service.lifecycleMu.Lock()
	if service.state.Load() != serviceRunning || service.dataContext == nil {
		service.lifecycleMu.Unlock()
		return nil, nil, &serviceStoppedError{}
	}
	requestCtx, cancel := context.WithCancel(ctx)
	stopData := context.AfterFunc(service.dataContext, cancel)
	context.AfterFunc(requestCtx, func() {
		stopData()
	})
	service.lifecycleMu.Unlock()
	return requestCtx, func() {
		stopData()
		cancel()
	}, nil
}
