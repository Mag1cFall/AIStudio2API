package aistudio

import (
	"context"
	"errors"
	"fmt"
	"io"
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

// VideoImage 表示视频起始帧或参考图
type VideoImage struct {
	InlineData *Blob
	File       *FileRef
}

// VideoRequest 表示一次生成一个视频的长任务请求
type VideoRequest struct {
	Model           string
	Prompt          string
	AspectRatio     string
	DurationSeconds int
	Resolution      string
	AccountID       string
	NegativePrompt  string
	StartImage      *VideoImage
	LastImage       *VideoImage
	ReferenceImages []*VideoImage
	// TaskContext 返回后台视频任务的上下文：生成服务停止时取消，期限从任务开始计算
	TaskContext func() (context.Context, context.CancelFunc, error)
}

// VideoOperation 表示视频长任务状态
type VideoOperation struct {
	ID        string
	Done      bool
	File      *FileRef
	Model     string
	Seconds   string
	Size      string
	CreatedAt time.Time
	// Prompt 是创建任务时的提示词
	Prompt string
	// CompletedAt 是结果视频登记时的 Unix 秒数，未完成时为 0
	CompletedAt int64
	// ExpiresAt 是结果视频到期删除的 Unix 秒数，没有结果视频时为 0
	ExpiresAt int64
	// Missing 为结果已无法得到的已结束任务的说明
	Missing string
	// Failure 为视频生成失败的错误
	Failure error
}

// IsVideoModel 表示模型经 generateContent 输出视频（如 Omni），能完成视频长任务
func IsVideoModel(model Model) bool {
	return hasMethod(model, "generateContent") && len(model.CapabilityOptions["video_output_resolutions"]) > 0
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

// GenerateVideo 以经 generateContent 输出视频的模型创建长任务
func (s *PooledService) GenerateVideo(ctx context.Context, request VideoRequest) (VideoOperation, error) {
	request = normalizeVideoRequest(request)
	modelID := strings.TrimPrefix(strings.TrimSpace(request.Model), "models/")
	model, ok := s.pool.generatedVideoModel(modelID)
	if !ok {
		return VideoOperation{}, fmt.Errorf("%w: 模型 %s 不输出视频", ErrModelNotFound, modelID)
	}
	return s.generateOmniVideo(ctx, request, model)
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

// GetGenerateVideoOperation 返回视频任务状态：结果视频已绑定时直接返回，否则按本进程的生成状态返回，服务重启前没有生成完成的任务为已结束且没有结果
func (s *PooledService) GetGenerateVideoOperation(ctx context.Context, operationID string) (VideoOperation, error) {
	state, tracked := s.omniVideos.Load(strings.TrimSpace(operationID))
	lease, owned, err := resolveAccountLease(ctx, s.pool, AccountSelection{ResourceID: strings.TrimSpace(operationID)})
	if err != nil {
		return VideoOperation{}, err
	}
	binding, err := lease.VideoOperationBinding(operationID)
	operation := VideoOperation{ID: operationID}
	if err == nil {
		switch failure, _ := state.(error); {
		case binding.Video.File != "":
			operation.Done, operation.File = true, &FileRef{ID: binding.Video.File, MIME: "video/mp4"}
		case !tracked:
			operation.Done, operation.Missing = true, "服务重启前任务没有生成完成"
		case failure != nil:
			operation.Done, operation.Failure = true, failure
		}
		applyVideoOperationBinding(&operation, binding)
	}
	if owned {
		err = errors.Join(err, lease.Release())
	}
	if err != nil {
		return VideoOperation{}, err
	}
	return operation, nil
}

// videoResultName 与官网视频页一致，返回以账户本地时间命名的视频文件名
func videoResultName(account *Account, now time.Time) (string, error) {
	location, err := time.LoadLocation(account.Config.Timezone)
	if err != nil {
		return "", err
	}
	return "Video " + now.In(location).Format("January 02, 2006 - 3:04PM") + ".mp4", nil
}

// ListVideos 刷新全部账户 runtime-state 后返回视频任务的持久元数据，按创建时间从新到旧排列
func (s *PooledService) ListVideos(ctx context.Context) ([]VideoOperation, error) {
	if err := s.pool.refreshAllRuntimes(ctx); err != nil {
		return nil, err
	}
	s.pool.mu.Lock()
	defer s.pool.mu.Unlock()
	videos := []VideoOperation{}
	for resourceID, accountID := range s.pool.resources {
		binding := s.pool.byID[accountID].runtime.Resources[resourceID]
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
	operation.Prompt = binding.Video.Prompt
	operation.CompletedAt = binding.Video.CompletedAt
	if binding.Video.File != "" {
		operation.ExpiresAt = binding.expiresAt().Unix()
	}
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
