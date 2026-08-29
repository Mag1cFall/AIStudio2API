package aistudio

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"strings"
	"time"
)

const DefaultTranscriptionModel = "gemini-3.5-transcribe"

var errTranscriptionStreamClosed = errors.New("AI Studio transcription stream closed before finish")

type transcriptionStageError struct {
	stage string
	err   error
}

func (err *transcriptionStageError) Error() string {
	return err.err.Error()
}

func (err *transcriptionStageError) Unwrap() error {
	return err.err
}

// TranscriptionGenerationFailure 判断错误是否来自转录生成阶段
func TranscriptionGenerationFailure(err error) bool {
	var stageError *transcriptionStageError
	return errors.As(err, &stageError) && stageError.stage == "generate"
}

// TranscriptionRequest 表示一次音频上传与转录请求
type TranscriptionRequest struct {
	ID                    string
	Model                 string
	Name                  string
	MIME                  string
	Size                  int64
	Reader                io.ReadSeeker
	Config                GenerationConfig
	CandidateAccountIDs   []string
	RecoverWAARuntime     func(context.Context, string, error) (bool, error)
	ObserveAccountFailure func(string, error)
}

// TranscriptionResult 表示完整转录及其上游元数据
type TranscriptionResult struct {
	Text            string
	Segments        []TranscriptMetadata
	Usage           Usage
	FinishReason    string
	ProviderModel   string
	FirstEvent      time.Duration
	FirstContent    time.Duration
	accessCheckedAt time.Time
}

// TranscriptionService 定义音频转录公开端点所需能力
type TranscriptionService interface {
	Transcribe(context.Context, TranscriptionRequest) (TranscriptionResult, error)
}

// Transcribe 在同一账户租约内完成上传与生成
func (s *PooledService) Transcribe(ctx context.Context, request TranscriptionRequest) (TranscriptionResult, error) {
	modelID := strings.TrimPrefix(strings.TrimSpace(request.Model), "models/")
	if modelID == "" {
		modelID = DefaultTranscriptionModel
	}
	if strings.TrimSpace(request.Name) == "" || strings.TrimSpace(request.MIME) == "" || request.Size <= 0 || request.Reader == nil {
		return TranscriptionResult{}, fmt.Errorf("%w: 转录文件需要名称、MIME 和数据", ErrInvalidArgument)
	}
	request.Model = modelID
	selection := AccountSelection{
		ModelID: modelID, ModelAccessScope: modelID, Method: "generateContent", Capability: "transcription_output",
	}
	_, pinned := AccountLeaseFromContext(ctx)
	maxAttempts := accountAttemptLimit(s.pool, pinned)
	attempted := make(map[string]struct{}, maxAttempts)
	var result TranscriptionResult
	var requestErr error
	startedAt := time.Now()
	recoveryAccountID := ""
	for attempt := 0; attempt < maxAttempts; attempt++ {
		selection.AllowedAccountIDs = remainingTranscriptionCandidates(request.CandidateAccountIDs, attempted)
		if request.CandidateAccountIDs != nil && len(selection.AllowedAccountIDs) == 0 {
			break
		}
		selection.AccountID = recoveryAccountID
		recoveryAccountID = ""
		lease, owned, err := resolveAccountLease(ctx, s.pool, selection)
		if err != nil {
			if requestErr != nil && errors.Is(err, ErrNoEligibleAccount) {
				break
			}
			return result, err
		}
		accountID := lease.Account().ID
		accessGeneration := lease.ModelAccessGeneration()
		checkedAt := lease.CheckedAt()
		attempted[accountID] = struct{}{}
		result, requestErr = s.transcribeWithLease(ctx, lease, request, startedAt)
		result.accessCheckedAt = checkedAt
		var earlyStateErr error
		if owned && DefinitiveModelAccessFailure(requestErr) {
			earlyStateErr = s.markRetryableFailure(lease, modelID, requestErr)
		}
		var releaseErr error
		if owned {
			releaseErr = lease.Release()
		}
		if earlyStateErr != nil || releaseErr != nil {
			return result, errors.Join(requestErr, earlyStateErr, releaseErr)
		}
		if requestErr == nil {
			if stateErr := lease.MarkAuthenticationValid(); stateErr != nil {
				return result, stateErr
			}
			if transcriptionVerifiesModelAccess(result) {
				go func() {
					if _, err := s.pool.MarkModelAccessVerifiedIfGeneration(
						accountID, modelID, accessGeneration, checkedAt,
					); err != nil {
						slog.Error("账户模型资格保存失败", "account", accountID, "model", modelID, "error", err)
					}
				}()
			}
			return result, nil
		}
		if !owned {
			return result, requestErr
		}
		if request.RecoverWAARuntime != nil {
			recovered, recoveryErr := request.RecoverWAARuntime(ctx, accountID, requestErr)
			if recoveryErr != nil {
				return result, errors.Join(requestErr, recoveryErr)
			}
			if recovered {
				delete(attempted, accountID)
				recoveryAccountID = accountID
				maxAttempts++
				continue
			}
		}
		if !transcriptionRetryableAccountError(ctx, requestErr) {
			return result, requestErr
		}
		if request.ObserveAccountFailure != nil {
			request.ObserveAccountFailure(accountID, requestErr)
		}
		var stateErr error
		if TranscriptionGenerationFailure(requestErr) {
			if !DefinitiveModelAccessFailure(requestErr) {
				stateErr = s.markRetryableFailure(lease, modelID, requestErr)
			}
		} else if DefinitiveAuthenticationFailure(requestErr) {
			stateErr = lease.MarkAuthenticationRequired(requestErr.Error())
		} else {
			stateErr = s.pool.MarkCooldownIfGeneration(
				accountID, "", accessGeneration, checkedAt,
				time.Now().Add(30*time.Second), requestErr.Error(),
			)
		}
		if stateErr != nil {
			return result, errors.Join(requestErr, stateErr)
		}
	}
	if requestErr != nil {
		return result, requestErr
	}
	return result, ErrNoEligibleAccount
}

func transcriptionVerifiesModelAccess(result TranscriptionResult) bool {
	return strings.TrimSpace(result.Text) != "" || len(result.Segments) > 0
}

func (s *PooledService) transcribeWithLease(
	ctx context.Context,
	lease *AccountLease,
	request TranscriptionRequest,
	startedAt time.Time,
) (result TranscriptionResult, resultErr error) {
	if _, err := request.Reader.Seek(0, io.SeekStart); err != nil {
		return TranscriptionResult{}, fmt.Errorf("重置转录文件: %w", err)
	}
	accountID := lease.Account().ID
	attemptCtx := ContextWithAccountLease(ctx, lease)
	file, token, err := s.client.uploadFile(attemptCtx, UploadRequest{
		AccountID: accountID,
		Name:      request.Name,
		MIME:      request.MIME,
		Size:      request.Size,
		Reader:    request.Reader,
	})
	if err != nil {
		return TranscriptionResult{}, &transcriptionStageError{stage: "upload", err: err}
	}
	defer func() {
		cleanupCtx, cancel := context.WithTimeout(context.WithoutCancel(attemptCtx), driveCleanupTimeout)
		cleanupErr := s.client.deleteDriveFile(cleanupCtx, accountID, token, file.ID)
		cancel()
		if cleanupErr == nil {
			return
		}
		cleanupErr = &transcriptionStageError{stage: "cleanup", err: fmt.Errorf("清理转录临时文件: %w", cleanupErr)}
		if resultErr != nil {
			slog.Warn("转录临时文件回收失败", "account", accountID, "file", file.ID, "error", cleanupErr)
			return
		}
		resultErr = cleanupErr
	}()
	events, err := s.client.Generate(attemptCtx, GenerateRequest{
		ID:        request.ID,
		Model:     request.Model,
		Contents:  []Content{{Role: RoleUser, Parts: []Part{{File: &file}}}},
		Config:    request.Config,
		AccountID: accountID,
	})
	if err != nil {
		return TranscriptionResult{}, &transcriptionStageError{stage: "generate", err: err}
	}
	result, err = collectTranscription(ctx, events, startedAt)
	if err != nil {
		return result, &transcriptionStageError{stage: "generate", err: err}
	}
	return result, nil
}

func collectTranscription(ctx context.Context, events <-chan Event, startedAt time.Time) (result TranscriptionResult, resultErr error) {
	var text strings.Builder
	defer func() {
		result.Text = text.String()
	}()
	finished := false
	for {
		select {
		case event, ok := <-events:
			if !ok {
				if finished {
					return result, nil
				}
				return result, errTranscriptionStreamClosed
			}
			if result.FirstEvent == 0 {
				result.FirstEvent = time.Since(startedAt)
			}
			if event.ProviderModel != "" {
				result.ProviderModel = event.ProviderModel
			}
			switch event.Kind {
			case EventText:
				if event.Text != "" && result.FirstContent == 0 {
					result.FirstContent = time.Since(startedAt)
				}
				text.WriteString(event.Text)
				if event.Transcript != nil {
					result.Segments = append(result.Segments, *event.Transcript)
				}
			case EventUsage:
				if event.Usage != nil {
					result.Usage = *event.Usage
				}
			case EventFinish:
				result.FinishReason = event.FinishReason
				finished = true
			case EventError:
				if event.Err == nil {
					return result, errors.New("AI Studio transcription returned an empty error event")
				}
				return result, event.Err
			}
		case <-ctx.Done():
			return result, ctx.Err()
		}
	}
}

func remainingTranscriptionCandidates(candidates []string, attempted map[string]struct{}) []string {
	if candidates == nil {
		return nil
	}
	result := make([]string, 0, len(candidates))
	for _, accountID := range candidates {
		if _, exists := attempted[accountID]; exists {
			continue
		}
		result = append(result, accountID)
	}
	return result
}

func transcriptionRetryableAccountError(ctx context.Context, err error) bool {
	if err == nil || ctx.Err() != nil || errors.Is(err, context.Canceled) {
		return false
	}
	var stageError *transcriptionStageError
	if errors.As(err, &stageError) && stageError.stage == "cleanup" {
		return false
	}
	if errors.Is(err, errTranscriptionStreamClosed) || incompleteTranscriptionStream(err) || errors.Is(err, context.DeadlineExceeded) {
		return true
	}
	if retryableAccountError(err) || DefinitiveWAARuntimeFailure(err) {
		return true
	}
	var networkError net.Error
	return errors.As(err, &networkError)
}

func incompleteTranscriptionStream(err error) bool {
	var protocolError *ProtocolEvidenceError
	return errors.As(err, &protocolError) && protocolError.Method == "GenerateContent" && protocolError.Path == "$" &&
		protocolError.Detail == "流结束前没有完成帧"
}

var _ TranscriptionService = (*PooledService)(nil)
