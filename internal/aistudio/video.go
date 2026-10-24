package aistudio

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"math"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"
)

// VideoService 定义长任务视频适配器依赖的能力
type VideoService interface {
	GenerateVideo(context.Context, VideoRequest) (VideoOperation, error)
	GetGenerateVideoOperation(context.Context, string) (VideoOperation, error)
	DownloadFile(context.Context, string) (MediaStream, error)
	ListVideos(context.Context) ([]VideoOperation, error)
	DeleteVideo(context.Context, string) error
}

// videoNotFinishedError 表示视频任务尚未结束，结果文件还不能删除
type videoNotFinishedError struct {
	operationID string
}

func (err *videoNotFinishedError) Error() string {
	return fmt.Sprintf("视频任务 %s 尚未结束", err.operationID)
}

func (err *videoNotFinishedError) HTTPStatus() int {
	return http.StatusConflict
}

func (err *videoNotFinishedError) ErrorCode() string {
	return "video_not_ready"
}

// VideoImage 表示 Veo 帧图片
type VideoImage struct {
	InlineData *Blob
	File       *FileRef
}

// VideoRequest 表示一次生成一个视频的 Veo 长任务请求
type VideoRequest struct {
	Model             string
	Prompt            string
	AspectRatio       string
	DurationSeconds   int
	Resolution        string
	AccountID         string
	NegativePrompt    string
	StartImage        *VideoImage
	LastImage         *VideoImage
	ReferenceImages   []*VideoImage
	RecoverWAARuntime func(context.Context, string, error) (bool, error)
	// TaskContext 返回后台视频任务的上下文：生成服务停止时取消，期限从任务开始计算
	TaskContext func() (context.Context, context.CancelFunc, error)
}

// VideoOperation 表示 Veo 私有长任务状态
type VideoOperation struct {
	ID              string
	Done            bool
	File            *FileRef
	Model           string
	Seconds         string
	Size            string
	CreatedAt       time.Time
	accessCheckedAt time.Time
	// Missing 为上游已找不到该任务时的上游错误说明
	Missing string
	// Failure 为 Omni 视频任务生成失败的错误
	Failure error
}

// IsVideoModel 表示模型能完成视频长任务：目录提供 predictLongRunning，或经 generateContent 输出视频（如 Omni）
func IsVideoModel(model Model) bool {
	return hasMethod(model, "predictLongRunning") ||
		hasMethod(model, "generateContent") && len(model.CapabilityOptions["video_output_resolutions"]) > 0
}

// ModelAccessCheckedAt 返回上游接受视频任务的资格时间
func (operation VideoOperation) ModelAccessCheckedAt() time.Time {
	return operation.accessCheckedAt
}

// EncodeGenerateVideoRequest 编码当前网页 GenerateVideo 数组协议，提示词为空时提示词位与官网一样为 null
func EncodeGenerateVideoRequest(request VideoRequest) ([]byte, error) {
	if strings.TrimSpace(request.Model) == "" {
		return nil, fmt.Errorf("GenerateVideo 需要模型")
	}
	request = normalizeVideoRequest(request)
	config := []any{
		int64(1),
		request.AspectRatio,
		[]any{strconv.Itoa(request.DurationSeconds)},
		request.Resolution,
	}
	if request.LastImage != nil {
		inline, file, err := encodeVideoImage(request.LastImage)
		if err != nil {
			return nil, err
		}
		config = append(config, inline, file)
	}
	wire := make([]any, 8, 9)
	wire[0] = wireModelName(request.Model)
	if strings.TrimSpace(request.Prompt) != "" {
		wire[1] = request.Prompt
	}
	wire[2] = config
	if request.StartImage != nil {
		inline, file, err := encodeVideoImage(request.StartImage)
		if err != nil {
			return nil, err
		}
		wire[3] = inline
		wire[4] = file
	}
	if request.NegativePrompt != "" {
		wire[5] = request.NegativePrompt
	}
	if len(request.ReferenceImages) > 0 {
		references := make([]any, 0, len(request.ReferenceImages))
		for _, image := range request.ReferenceImages {
			inline, file, err := encodeVideoImage(image)
			if err != nil {
				return nil, err
			}
			if file != nil {
				references = append(references, []any{nil, file})
			} else {
				references = append(references, []any{inline})
			}
		}
		wire = append(wire, references)
	}
	return json.Marshal(wire)
}

func encodeVideoImage(image *VideoImage) (any, any, error) {
	if image == nil {
		return nil, nil, nil
	}
	if (image.InlineData == nil) == (image.File == nil) {
		return nil, nil, fmt.Errorf("Veo 帧图片必须且只能设置 inline data 或 Drive file")
	}
	if image.InlineData != nil {
		if !strings.HasPrefix(strings.ToLower(image.InlineData.MIME), "image/") || len(image.InlineData.Data) == 0 {
			return nil, nil, fmt.Errorf("Veo inline 帧图片需要图片 MIME 和数据")
		}
		return []any{image.InlineData.MIME, base64.StdEncoding.EncodeToString(image.InlineData.Data)}, nil, nil
	}
	if strings.TrimSpace(image.File.ID) == "" {
		return nil, nil, fmt.Errorf("Veo Drive 帧图片缺少文件 ID")
	}
	return nil, []any{image.File.ID}, nil
}

func normalizeVideoRequest(request VideoRequest) VideoRequest {
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
	var operation VideoOperation
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
	request, err = fitVideoOptions(request, entry.model)
	if err != nil {
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
	operation, err := ParseVideoOperation(response.Body, "GenerateVideo")
	if err != nil {
		return VideoOperation{}, err
	}
	operation.accessCheckedAt = time.Now().UTC()
	operation.Seconds = strconv.Itoa(request.DurationSeconds)
	operation.Size = videoOutputSize(request)
	return operation, nil
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
	request = normalizeVideoRequest(request)
	modelID := strings.TrimPrefix(strings.TrimSpace(request.Model), "models/")
	if model, ok := s.pool.generatedVideoModel(modelID); ok {
		return s.generateOmniVideo(ctx, request, model)
	}
	requestedAccountID := strings.TrimSpace(request.AccountID)
	selection := AccountSelection{
		ModelID: modelID, Method: "predictLongRunning", AccountID: requestedAccountID, ResourceID: videoImageResourceID(request),
	}
	maxAttempts := accountAttemptLimit(s.pool, selection.AccountID != "" || selection.ResourceID != "")
	recoveryAccountID := ""
	var operation VideoOperation
	var generateErr error
	for attempt := 0; attempt < maxAttempts; attempt++ {
		selection.AccountID = requestedAccountID
		if recoveryAccountID != "" {
			selection.AccountID = recoveryAccountID
			recoveryAccountID = ""
		}
		lease, owned, err := resolveAccountLease(ctx, s.pool, selection)
		if err != nil {
			if generateErr != nil && errors.Is(err, ErrNoEligibleAccount) {
				return operation, generateErr
			}
			return VideoOperation{}, err
		}
		request.AccountID = lease.Account().ID
		attemptCtx := ContextWithAccountLease(ctx, lease)
		operation, generateErr = s.client.GenerateVideo(attemptCtx, request)
		if generateErr == nil {
			operation.accessCheckedAt = lease.CheckedAt()
			generateErr = errors.Join(
				lease.MarkAuthenticationValid(),
				s.pool.ClearCooldownIfGeneration(
					request.AccountID, "", lease.ModelAccessGeneration(), lease.CheckedAt(),
				),
			)
		}
		if generateErr == nil {
			var binding ResourceBinding
			binding, generateErr = lease.BindVideoOperation(attemptCtx, operation.ID, VideoResourceMetadata{
				Model: request.Model, Seconds: operation.Seconds, Size: operation.Size,
			})
			if generateErr == nil {
				applyVideoOperationBinding(&operation, binding)
			}
		}
		if owned {
			generateErr = errors.Join(generateErr, lease.Release())
		}
		if generateErr == nil || !retryableAccountError(generateErr) {
			if generateErr == nil {
				return operation, nil
			}
			if request.RecoverWAARuntime == nil {
				return operation, generateErr
			}
		}
		if request.RecoverWAARuntime != nil {
			recovered, recoveryErr := request.RecoverWAARuntime(ctx, request.AccountID, generateErr)
			if recoveryErr != nil {
				return operation, errors.Join(generateErr, recoveryErr)
			}
			if recovered {
				recoveryAccountID = request.AccountID
				maxAttempts++
				continue
			}
		}
		if !retryableAccountError(generateErr) {
			return operation, generateErr
		}
		if stateErr := s.markRetryableFailure(lease, modelID, generateErr); stateErr != nil {
			return operation, errors.Join(generateErr, stateErr)
		}
	}
	return operation, generateErr
}

// videoImageResourceID 返回视频请求中第一张以文件引用给出的图片 ID，任务在该文件所属账户上运行
func videoImageResourceID(request VideoRequest) string {
	for _, image := range append([]*VideoImage{request.StartImage, request.LastImage}, request.ReferenceImages...) {
		if image != nil && image.File != nil {
			return strings.TrimSpace(image.File.ID)
		}
	}
	return ""
}

// downloadFileBlob 读取已绑定文件的完整内容
func (s *PooledService) downloadFileBlob(ctx context.Context, fileID string) (*Blob, error) {
	stream, err := s.DownloadFile(ctx, fileID)
	if err != nil {
		return nil, err
	}
	defer stream.Body.Close()
	data, err := io.ReadAll(stream.Body)
	if err != nil {
		return nil, err
	}
	return &Blob{MIME: stream.MIME, Data: data}, nil
}

// resourceAccountID 返回资源绑定的账户
func (p *AccountPool) resourceAccountID(resourceID string) string {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.resources[strings.TrimSpace(resourceID)]
}

// GetGenerateVideoOperation 使用 operation 创建账户轮询，首次完成时把临时结果文件复制为账户 Drive 中的视频并绑定，此后直接返回该视频；临时文件暂不可复制时报告未完成；Omni 任务按本进程的生成状态返回
func (s *PooledService) GetGenerateVideoOperation(ctx context.Context, operationID string) (VideoOperation, error) {
	omniState, omniTracked := s.omniVideos.Load(strings.TrimSpace(operationID))
	lease, owned, err := resolveAccountLease(ctx, s.pool, AccountSelection{ResourceID: strings.TrimSpace(operationID)})
	if err != nil {
		return VideoOperation{}, err
	}
	accountID := lease.Account().ID
	binding, bindingErr := lease.VideoOperationBinding(operationID)
	if bindingErr != nil {
		if owned {
			bindingErr = errors.Join(bindingErr, lease.Release())
		}
		return VideoOperation{}, bindingErr
	}
	if binding.Video.File != "" {
		operation := VideoOperation{ID: operationID, Done: true, File: &FileRef{ID: binding.Video.File, MIME: "video/mp4"}}
		applyVideoOperationBinding(&operation, binding)
		if owned {
			if err := lease.Release(); err != nil {
				return VideoOperation{}, err
			}
		}
		return operation, nil
	}
	if strings.HasPrefix(operationID, omniVideoPrefix) {
		operation := VideoOperation{ID: operationID}
		if !omniTracked {
			operation.Done, operation.Missing = true, "服务重启前任务没有生成完成"
		} else if failure, _ := omniState.(error); failure != nil {
			operation.Done, operation.Failure = true, failure
		}
		applyVideoOperationBinding(&operation, binding)
		if owned {
			if err := lease.Release(); err != nil {
				return VideoOperation{}, err
			}
		}
		return operation, nil
	}
	operation, pollErr := s.client.GetGenerateVideoOperation(ContextWithAccountLease(ctx, lease), accountID, operationID)
	var rpcError *RPCError
	if errors.As(pollErr, &rpcError) && rpcError.StatusCode == http.StatusNotFound {
		operation, pollErr = VideoOperation{ID: operationID, Done: true, Missing: rpcError.Message}, nil
	}
	applyVideoOperationBinding(&operation, binding)
	if pollErr == nil {
		pollErr = errors.Join(
			lease.MarkAuthenticationValid(),
			s.pool.ClearCooldownIfGeneration(
				accountID, "", lease.ModelAccessGeneration(), lease.CheckedAt(),
			),
		)
		if pollErr == nil && operation.Done && operation.File != nil {
			var fileID string
			fileID, pollErr = s.client.saveVideoResult(ContextWithAccountLease(ctx, lease), lease.Account(), operation.File.ID, time.Now())
			if videoResultNotReady(pollErr) {
				slog.Warn("视频结果临时文件暂不可复制，下次轮询重新获取", "operation", operationID, "file", operation.File.ID, "error", pollErr)
				operation.Done, operation.File, pollErr = false, nil, nil
			} else if pollErr == nil {
				operation.File.ID = fileID
				pollErr = lease.BindVideoResult(ctx, operationID, fileID)
			}
		}
	} else if DefinitiveAuthenticationFailure(pollErr) {
		pollErr = errors.Join(pollErr, lease.MarkAuthenticationRequired(pollErr.Error()))
	}
	if owned {
		pollErr = errors.Join(pollErr, lease.Release())
	}
	return operation, pollErr
}

// videoResultNotReady 判断刚完成任务的临时结果文件在 Drive 复制时返回 400 或 404
func videoResultNotReady(err error) bool {
	var rpcError *RPCError
	return errors.As(err, &rpcError) && rpcError.Method == "DriveCopy" &&
		(rpcError.StatusCode == http.StatusBadRequest || rpcError.StatusCode == http.StatusNotFound)
}

// saveVideoResult 按官网视频页把任务的临时结果文件复制为以账户本地时间命名的视频并返回副本 ID
func (c *Client) saveVideoResult(ctx context.Context, account *Account, fileID string, now time.Time) (string, error) {
	name, err := videoResultName(account, now)
	if err != nil {
		return "", err
	}
	token, err := c.GenerateAccessToken(ctx, account.ID)
	if err != nil {
		return "", err
	}
	drive, ok := c.transport.(DriveTransport)
	if !ok {
		return "", fmt.Errorf("AI Studio transport 不支持 Drive")
	}
	return drive.CopyDrive(ctx, account.ID, token, fileID, name)
}

// videoResultName 与官网视频页一致，返回以账户本地时间命名的视频文件名
func videoResultName(account *Account, now time.Time) (string, error) {
	location, err := time.LoadLocation(account.Config.Timezone)
	if err != nil {
		return "", err
	}
	return "Video " + now.In(location).Format("January 02, 2006 - 3:04PM") + ".mp4", nil
}

// ListVideos 返回全部视频任务的持久元数据
func (s *PooledService) ListVideos(ctx context.Context) ([]VideoOperation, error) {
	return s.pool.ListVideos(ctx)
}

// ListVideos 刷新全部账户 runtime-state 后返回视频任务，按创建时间从新到旧排列
func (p *AccountPool) ListVideos(ctx context.Context) ([]VideoOperation, error) {
	if err := p.refreshAllRuntimes(ctx); err != nil {
		return nil, err
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	videos := []VideoOperation{}
	for resourceID, accountID := range p.resources {
		binding := p.byID[accountID].runtime.Resources[resourceID]
		if binding.Kind != "video-operation" || binding.Video == nil {
			continue
		}
		operation := VideoOperation{ID: resourceID}
		applyVideoOperationBinding(&operation, binding)
		videos = append(videos, operation)
	}
	sort.Slice(videos, func(i, j int) bool {
		if !videos[i].CreatedAt.Equal(videos[j].CreatedAt) {
			return videos[i].CreatedAt.After(videos[j].CreatedAt)
		}
		return videos[i].ID < videos[j].ID
	})
	return videos, nil
}

// DeleteVideo 删除已结束视频任务的 Drive 结果文件与任务绑定，任务未结束时返回 409
func (s *PooledService) DeleteVideo(ctx context.Context, operationID string) error {
	operationID = strings.TrimSpace(operationID)
	operation, err := s.GetGenerateVideoOperation(ctx, operationID)
	if err != nil {
		return err
	}
	if !operation.Done {
		return &videoNotFinishedError{operationID: operationID}
	}
	if operation.File != nil {
		if err := s.deleteDriveResource(ctx, operation.File.ID); err != nil && !errors.Is(err, ErrResourceNotFound) {
			return err
		}
	}
	s.omniVideos.Delete(operationID)
	return s.pool.unbindResourceContext(ctx, operationID)
}

var _ VideoService = (*PooledService)(nil)

func applyVideoOperationBinding(operation *VideoOperation, binding ResourceBinding) {
	operation.Model = binding.Video.Model
	operation.Seconds = binding.Video.Seconds
	operation.Size = binding.Video.Size
	operation.CreatedAt = binding.CreatedAt
}

func videoOutputSize(request VideoRequest) string {
	width := "1280"
	height := "720"
	switch strings.ToLower(request.Resolution) {
	case "360p":
		width, height = "640", "360"
	case "1080p":
		width, height = "1920", "1080"
	case "4k":
		width, height = "3840", "2160"
	}
	if request.AspectRatio == "9:16" {
		return height + "x" + width
	}
	return width + "x" + height
}

// fitVideoOptions 将时长、宽高比与分辨率换成模型选项中最接近的取值，时长限制在模型时长范围内
func fitVideoOptions(request VideoRequest, model Model) (VideoRequest, error) {
	if request.DurationSeconds < 0 {
		return request, fmt.Errorf("视频时长不能为负数")
	}
	if options := model.CapabilityOptions["video_durations_seconds"]; len(options) > 0 {
		request.DurationSeconds, _ = strconv.Atoi(NearestOption(float64(request.DurationSeconds), options, videoDurationSeconds))
	}
	for _, bound := range model.CapabilityOptions["video_duration_range"] {
		name, value, _ := strings.Cut(bound, "=")
		seconds, _ := strconv.Atoi(value)
		if name == "min" {
			request.DurationSeconds = max(request.DurationSeconds, seconds)
		} else if name == "max" {
			request.DurationSeconds = min(request.DurationSeconds, seconds)
		}
	}
	if options := model.CapabilityOptions["video_aspect_ratios"]; len(options) > 0 {
		ratio, ok := AspectRatioLog(request.AspectRatio)
		if !ok {
			return request, fmt.Errorf("无法解析视频宽高比 %q", request.AspectRatio)
		}
		request.AspectRatio = NearestOption(ratio, options, AspectRatioLog)
	}
	if options := model.CapabilityOptions["video_output_resolutions"]; len(options) > 0 {
		height, ok := videoResolutionHeight(request.Resolution)
		if !ok {
			return request, fmt.Errorf("无法解析视频分辨率 %q", request.Resolution)
		}
		request.Resolution = NearestOption(height, options, videoResolutionHeight)
	}
	return request, nil
}

// NearestOption 返回度量值与目标最接近的选项，距离相同时取度量值较大者
func NearestOption(target float64, options []string, measure func(string) (float64, bool)) string {
	nearest := ""
	nearestDistance, nearestValue := math.Inf(1), math.Inf(-1)
	for _, option := range options {
		value, ok := measure(option)
		if !ok {
			continue
		}
		distance := math.Abs(value - target)
		if distance < nearestDistance || distance == nearestDistance && value > nearestValue {
			nearest, nearestDistance, nearestValue = option, distance, value
		}
	}
	return nearest
}

// AspectRatioLog 返回 W:H 宽高比的自然对数
func AspectRatioLog(value string) (float64, bool) {
	widthText, heightText, ok := strings.Cut(strings.TrimSpace(value), ":")
	width, widthErr := strconv.ParseFloat(strings.TrimSpace(widthText), 64)
	height, heightErr := strconv.ParseFloat(strings.TrimSpace(heightText), 64)
	if !ok || widthErr != nil || heightErr != nil || !(width > 0) || !(height > 0) {
		return 0, false
	}
	return math.Log(width) - math.Log(height), true
}

// videoDurationSeconds 解析视频时长选项的秒数
func videoDurationSeconds(value string) (float64, bool) {
	seconds, err := strconv.Atoi(strings.TrimSpace(value))
	return float64(seconds), err == nil
}

// videoResolutionHeight 返回 720p、4k 等视频分辨率的像素高度
func videoResolutionHeight(value string) (float64, bool) {
	value = strings.ToLower(strings.TrimSpace(value))
	scale := 0
	switch {
	case strings.HasSuffix(value, "p"):
		scale = 1
	case strings.HasSuffix(value, "k"):
		scale = 540
	}
	number, err := strconv.Atoi(value[:max(len(value)-1, 0)])
	if scale == 0 || err != nil || number <= 0 {
		return 0, false
	}
	return float64(number * scale), true
}
