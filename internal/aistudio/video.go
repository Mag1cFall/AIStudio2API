package aistudio

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"
)

// VideoService 定义长任务视频适配器依赖的能力
type VideoService interface {
	GenerateVideo(context.Context, VideoRequest) (VideoOperation, error)
	GetGenerateVideoOperation(context.Context, string) (VideoOperation, error)
	DownloadFile(context.Context, string) (Media, error)
}

// VideoImage 表示 Veo 起始帧
type VideoImage struct {
	InlineData *Blob
	File       *FileRef
}

// VideoRequest 表示一次 Veo 长任务请求
type VideoRequest struct {
	Model           string
	Prompt          string
	Count           int
	AspectRatio     string
	DurationSeconds int
	Resolution      string
	AccountID       string
	StartImage      *VideoImage
}

// VideoOperation 表示 Veo 私有长任务状态
type VideoOperation struct {
	ID   string
	Done bool
	File *FileRef
	Raw  json.RawMessage
}

// EncodeGenerateVideoRequest 编码当前网页 GenerateVideo 数组协议
func EncodeGenerateVideoRequest(request VideoRequest) ([]byte, error) {
	if strings.TrimSpace(request.Model) == "" || strings.TrimSpace(request.Prompt) == "" {
		return nil, fmt.Errorf("GenerateVideo 需要模型和提示词")
	}
	request = normalizeVideoRequest(request)
	if request.Count != 1 {
		return nil, fmt.Errorf("GenerateVideo 当前模型只支持一个结果")
	}
	config := []any{
		int64(request.Count),
		request.AspectRatio,
		[]any{strconv.Itoa(request.DurationSeconds)},
		request.Resolution,
	}
	wire := make([]any, 8)
	wire[0] = wireModelName(request.Model)
	wire[1] = request.Prompt
	wire[2] = config
	if request.StartImage != nil {
		inline, file, err := encodeVideoImage(request.StartImage)
		if err != nil {
			return nil, err
		}
		wire[3] = inline
		wire[4] = file
	}
	return json.Marshal(wire)
}

func encodeVideoImage(image *VideoImage) (any, any, error) {
	if image == nil {
		return nil, nil, nil
	}
	if (image.InlineData == nil) == (image.File == nil) {
		return nil, nil, fmt.Errorf("Veo 起始帧必须且只能设置 inline data 或 Drive file")
	}
	if image.InlineData != nil {
		if !strings.HasPrefix(strings.ToLower(image.InlineData.MIME), "image/") || len(image.InlineData.Data) == 0 {
			return nil, nil, fmt.Errorf("Veo inline 起始帧需要图片 MIME 和数据")
		}
		return []any{image.InlineData.MIME, base64.StdEncoding.EncodeToString(image.InlineData.Data)}, nil, nil
	}
	if strings.TrimSpace(image.File.ID) == "" {
		return nil, nil, fmt.Errorf("Veo Drive 起始帧缺少文件 ID")
	}
	return nil, []any{image.File.ID}, nil
}

func normalizeVideoRequest(request VideoRequest) VideoRequest {
	if request.Count == 0 {
		request.Count = 1
	}
	if strings.TrimSpace(request.AspectRatio) == "" {
		request.AspectRatio = "16:9"
	}
	if request.DurationSeconds == 0 {
		request.DurationSeconds = 4
	}
	if strings.TrimSpace(request.Resolution) == "" {
		request.Resolution = "720p"
	}
	return request
}

// EncodeGetGenerateVideoOperationRequest 编码当前网页轮询数组协议
func EncodeGetGenerateVideoOperationRequest(operationID string) ([]byte, error) {
	if strings.TrimSpace(operationID) == "" {
		return nil, fmt.Errorf("GetGenerateVideoOperation 需要 operation ID")
	}
	return json.Marshal([]any{operationID})
}

// ParseVideoOperation 解码 GenerateVideo 与轮询返回的 operation
func ParseVideoOperation(source io.Reader, method string) (VideoOperation, error) {
	raw, err := io.ReadAll(newSparseJSONReader(source))
	if err != nil {
		return VideoOperation{}, fmt.Errorf("读取 %s: %w", method, err)
	}
	root, err := rawArray(raw, "$", raw)
	if err != nil {
		return VideoOperation{}, withMethod(err, method)
	}
	operation := VideoOperation{Raw: append(json.RawMessage(nil), raw...)}
	if method == "GenerateVideo" {
		if len(root) > 0 && !isJSONNull(root[0]) {
			operation.ID, err = rawString(root[0], "$[0]", raw)
			if err != nil {
				return VideoOperation{}, withMethod(err, method)
			}
		}
		if operation.ID == "" {
			return VideoOperation{}, &ProtocolEvidenceError{Method: method, Path: "$[0]", Detail: "缺少 operation ID", Raw: raw}
		}
		return operation, nil
	}
	return decodePolledVideoOperation(operation, root, raw)
}

func decodePolledVideoOperation(operation VideoOperation, root []json.RawMessage, raw json.RawMessage) (VideoOperation, error) {
	if len(root) > 0 && !isJSONNull(root[0]) {
		done, err := rawBool(root[0], "$[0]", raw)
		if err != nil {
			return VideoOperation{}, withMethod(err, "GetGenerateVideoOperation")
		}
		operation.Done = done
	}
	if len(root) > 1 && !isJSONNull(root[1]) {
		results, err := rawArray(root[1], "$[1]", raw)
		if err != nil {
			return VideoOperation{}, withMethod(err, "GetGenerateVideoOperation")
		}
		if len(results) > 0 && !isJSONNull(results[0]) {
			result, err := rawArray(results[0], "$[1][0]", raw)
			if err != nil {
				return VideoOperation{}, withMethod(err, "GetGenerateVideoOperation")
			}
			if len(result) == 0 || isJSONNull(result[0]) {
				return operation, nil
			}
			file, err := rawArray(result[0], "$[1][0][0]", raw)
			if err != nil {
				return VideoOperation{}, withMethod(err, "GetGenerateVideoOperation")
			}
			if len(file) == 0 || isJSONNull(file[0]) {
				return operation, nil
			}
			id, err := rawString(file[0], "$[1][0][0][0]", raw)
			if err != nil {
				return VideoOperation{}, withMethod(err, "GetGenerateVideoOperation")
			}
			operation.File = &FileRef{ID: id, MIME: "video/mp4"}
		}
	}
	return operation, nil
}

// GenerateVideo 创建 Veo 长任务
func (c *Client) GenerateVideo(ctx context.Context, request VideoRequest) (VideoOperation, error) {
	request = normalizeVideoRequest(request)
	entry, err := c.modelEntry(ctx, request.AccountID, request.Model)
	if err != nil {
		return VideoOperation{}, err
	}
	if !hasMethod(entry.model, "predictLongRunning") {
		return VideoOperation{}, fmt.Errorf("%w: 模型 %q 的实时目录没有 predictLongRunning 方法", ErrInvalidArgument, entry.model.ID)
	}
	if err := validateVideoOptions(request, entry.model); err != nil {
		return VideoOperation{}, fmt.Errorf("%w: %v", ErrInvalidArgument, err)
	}
	body, err := EncodeGenerateVideoRequest(request)
	if err != nil {
		return VideoOperation{}, fmt.Errorf("%w: %v", ErrInvalidArgument, err)
	}
	response, err := c.doProtectedVideo(ctx, request, body)
	if err != nil {
		return VideoOperation{}, err
	}
	defer response.Body.Close()
	return ParseVideoOperation(response.Body, "GenerateVideo")
}

// GetGenerateVideoOperation 读取 Veo 长任务当前状态
func (c *Client) GetGenerateVideoOperation(ctx context.Context, accountID string, operationID string) (VideoOperation, error) {
	body, err := EncodeGetGenerateVideoOperationRequest(operationID)
	if err != nil {
		return VideoOperation{}, err
	}
	response, err := c.do(ctx, "GetGenerateVideoOperation", accountID, "", body, false)
	if err != nil {
		return VideoOperation{}, err
	}
	defer response.Body.Close()
	operation, err := ParseVideoOperation(response.Body, "GetGenerateVideoOperation")
	operation.ID = operationID
	return operation, err
}

// GenerateVideo 使用一个独占账户创建任务并保存 operation 绑定
func (s *PooledService) GenerateVideo(ctx context.Context, request VideoRequest) (VideoOperation, error) {
	modelID := strings.TrimPrefix(strings.TrimSpace(request.Model), "models/")
	resourceID := ""
	if request.StartImage != nil && request.StartImage.File != nil {
		resourceID = strings.TrimSpace(request.StartImage.File.ID)
	}
	selection := AccountSelection{
		ModelID: modelID, Method: "predictLongRunning", AccountID: strings.TrimSpace(request.AccountID), ResourceID: resourceID,
	}
	maxAttempts := accountAttemptLimit(s.pool, selection.AccountID != "" || selection.ResourceID != "")
	var operation VideoOperation
	var generateErr error
	for attempt := 0; attempt < maxAttempts; attempt++ {
		lease, owned, err := resolveAccountLease(ctx, s.pool, selection)
		if err != nil {
			if generateErr != nil && errors.Is(err, ErrNoEligibleAccount) {
				return operation, generateErr
			}
			return VideoOperation{}, err
		}
		request.AccountID = lease.Account().ID
		operation, generateErr = s.client.GenerateVideo(ContextWithAccountLease(ctx, lease), request)
		if generateErr == nil {
			generateErr = lease.BindResource(operation.ID, "video-operation")
		}
		if owned {
			generateErr = errors.Join(generateErr, lease.Release())
		}
		if generateErr == nil || !retryableAccountError(generateErr) {
			return operation, generateErr
		}
		if stateErr := s.markRetryableFailure(request.AccountID, modelID, generateErr); stateErr != nil {
			return operation, errors.Join(generateErr, stateErr)
		}
	}
	return operation, generateErr
}

// GetGenerateVideoOperation 使用 operation 创建账户轮询并绑定结果文件
func (s *PooledService) GetGenerateVideoOperation(ctx context.Context, operationID string) (VideoOperation, error) {
	lease, owned, err := resolveAccountLease(ctx, s.pool, AccountSelection{ResourceID: strings.TrimSpace(operationID)})
	if err != nil {
		return VideoOperation{}, err
	}
	accountID := lease.Account().ID
	operation, pollErr := s.client.GetGenerateVideoOperation(ContextWithAccountLease(ctx, lease), accountID, operationID)
	if pollErr == nil && operation.Done && operation.File != nil {
		pollErr = lease.BindResource(operation.File.ID, "video-file")
	}
	if owned {
		pollErr = errors.Join(pollErr, lease.Release())
	}
	if DefinitiveAuthenticationFailure(pollErr) {
		if stateErr := s.pool.MarkAuthRequired(accountID, pollErr.Error()); stateErr != nil {
			pollErr = errors.Join(pollErr, stateErr)
		}
	}
	return operation, pollErr
}

func validateVideoOptions(request VideoRequest, model Model) error {
	checks := []struct {
		name  string
		value string
	}{
		{name: "video_aspect_ratios", value: request.AspectRatio},
		{name: "video_durations_seconds", value: strconv.Itoa(request.DurationSeconds)},
		{name: "video_output_resolutions", value: request.Resolution},
	}
	for _, check := range checks {
		if check.value == "" {
			continue
		}
		allowed := model.CapabilityOptions[check.name]
		if len(allowed) == 0 {
			continue
		}
		found := false
		for _, value := range allowed {
			if strings.EqualFold(value, check.value) {
				found = true
				break
			}
		}
		if !found {
			return fmt.Errorf("模型 %q 不支持 %s=%s", model.ID, check.name, check.value)
		}
	}
	return nil
}
