package aistudio

import (
	"bytes"
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"log/slog"
	"slices"
	"strconv"
	"strings"
	"time"
)

// omniVideoPrefix 是由 Omni generateContent 视频输出完成的视频任务 ID 前缀
const omniVideoPrefix = "omni-"

// generatedVideoModel 返回目录中没有 predictLongRunning、经 generateContent 输出视频的模型
func (p *AccountPool) generatedVideoModel(modelID string) (Model, bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.hasModelMethodLocked(modelID, "predictLongRunning") {
		return Model{}, false
	}
	for _, account := range p.accounts {
		if account == nil {
			continue
		}
		for _, model := range account.Models {
			if modelMatchesID(model, modelID) && IsVideoModel(model) {
				return model, true
			}
		}
	}
	return Model{}, false
}

// VideoMethod 返回模型创建视频任务所用的上游方法：经 generateContent 输出视频的模型为 generateContent，其余为 predictLongRunning
func (p *AccountPool) VideoMethod(modelID string) string {
	if _, ok := p.generatedVideoModel(strings.TrimPrefix(strings.TrimSpace(modelID), "models/")); ok {
		return "generateContent"
	}
	return "predictLongRunning"
}

// generateOmniVideo 以 Omni 的 generateContent 视频输出完成长任务，绑定任务后立即返回，后台生成的 MP4 上传为账户 Drive 中的视频并绑定为结果
func (s *PooledService) generateOmniVideo(ctx context.Context, request VideoRequest, model Model) (VideoOperation, error) {
	generate, err := omniVideoGenerateRequest(request, model)
	if err != nil {
		return VideoOperation{}, fmt.Errorf("%w: %v", ErrInvalidArgument, err)
	}
	taskCtx, cancelTask, err := request.TaskContext()
	if err != nil {
		return VideoOperation{}, err
	}
	selection := AccountSelection{
		ModelID: model.ID, Method: "generateContent", AccountID: strings.TrimSpace(request.AccountID),
		ResourceID: videoImageResourceID(request),
	}
	lease, owned, err := resolveAccountLease(ctx, s.pool, selection)
	var ownerCooling *AllCoolingError
	if selection.ResourceID != "" && selection.AccountID == "" && (errors.Is(err, ErrNoEligibleAccount) || errors.As(err, &ownerCooling)) {
		selection.ResourceID = ""
		lease, owned, err = resolveAccountLease(ctx, s.pool, selection)
	}
	if err != nil {
		cancelTask()
		return VideoOperation{}, err
	}
	operationID := omniVideoPrefix + strings.ToLower(rand.Text())
	video := generate.Config.VideoConfig
	binding, err := lease.BindVideoOperation(ctx, operationID, VideoResourceMetadata{
		Model:   request.Model,
		Seconds: strconv.FormatInt(video.DurationSeconds, 10),
		Size:    videoOutputSize(VideoRequest{AspectRatio: video.AspectRatio, Resolution: video.Resolution}),
	})
	generate.AccountID = lease.Account().ID
	if owned {
		err = errors.Join(err, lease.Release())
	}
	if err != nil {
		cancelTask()
		return VideoOperation{}, err
	}
	s.omniVideos.Store(operationID, nil)
	go func() {
		defer cancelTask()
		s.runOmniVideo(taskCtx, operationID, generate)
	}()
	operation := VideoOperation{ID: operationID}
	applyVideoOperationBinding(&operation, binding)
	return operation, nil
}

// omniVideoGenerateRequest 把视频长任务写为 Omni 的 generateContent 请求，模型目录列出的任务中没有所需任务时返回错误
func omniVideoGenerateRequest(request VideoRequest, model Model) (GenerateRequest, error) {
	if request.LastImage != nil {
		return GenerateRequest{}, fmt.Errorf("模型 %s 没有尾帧输入", model.ID)
	}
	request, err := fitVideoOptions(request, model)
	if err != nil {
		return GenerateRequest{}, err
	}
	images, task := request.ReferenceImages, "reference_to_video"
	if len(images) == 0 && request.StartImage != nil {
		images, task = []*VideoImage{request.StartImage}, "image_to_video"
	} else if len(images) == 0 {
		task = "text_to_video"
	}
	if tasks := model.CapabilityOptions["video_tasks"]; len(tasks) > 0 && !slices.Contains(tasks, task) {
		return GenerateRequest{}, fmt.Errorf("模型 %s 没有 %s 任务", model.ID, task)
	}
	parts := make([]Part, 0, len(images)+1)
	for _, image := range images {
		parts = append(parts, Part{InlineData: image.InlineData, File: image.File})
	}
	prompt := request.Prompt
	if negative := strings.TrimSpace(request.NegativePrompt); negative != "" {
		prompt += "\n\nNegative prompt: " + negative
	}
	if strings.TrimSpace(prompt) != "" {
		parts = append(parts, Part{Text: prompt})
	}
	// 文字生成视频与只有图片时与官网一样不带任务
	if task == "text_to_video" || strings.TrimSpace(prompt) == "" {
		task = ""
	}
	return GenerateRequest{
		Model:    model.ID,
		Contents: []Content{{Role: RoleUser, Parts: parts}},
		Config: GenerationConfig{
			VideoConfig: &VideoConfig{
				AspectRatio: request.AspectRatio, DurationSeconds: int64(request.DurationSeconds),
				Resolution: request.Resolution, Task: task,
			},
			HideThinking: true,
		},
	}, nil
}

// runOmniVideo 在后台生成 Omni 视频并保存结果，失败时记录错误供轮询返回
func (s *PooledService) runOmniVideo(ctx context.Context, operationID string, generate GenerateRequest) {
	if err := s.saveOmniVideo(ctx, operationID, generate); err != nil {
		slog.Warn("Omni 视频任务失败", "operation", operationID, "error", err)
		s.omniVideos.Store(operationID, err)
		return
	}
	s.omniVideos.Delete(operationID)
}

// saveOmniVideo 生成 Omni 视频并把 MP4 上传到任务所在账户的 Drive，绑定为任务结果
func (s *PooledService) saveOmniVideo(ctx context.Context, operationID string, generate GenerateRequest) error {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	parts := generate.Contents[0].Parts
	for index, part := range parts {
		var image *Blob
		switch {
		case part.InlineData != nil:
			image = part.InlineData
		case part.File != nil && s.pool.resourceAccountID(part.File.ID) != generate.AccountID:
			var err error
			if image, err = s.downloadFileBlob(ctx, part.File.ID); err != nil {
				return err
			}
		default:
			continue
		}
		file, err := s.UploadFile(ctx, UploadRequest{
			AccountID: generate.AccountID, Purpose: "assistants", Name: fmt.Sprintf("%s-image-%d", operationID, index),
			MIME: image.MIME, Size: int64(len(image.Data)), Reader: bytes.NewReader(image.Data),
		})
		if err != nil {
			return err
		}
		defer func() {
			cleanupCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), driveCleanupTimeout)
			defer cancel()
			if err := s.DeleteFile(cleanupCtx, file.ID); err != nil {
				slog.Warn("Omni 视频输入图片删除失败", "operation", operationID, "file", file.ID, "error", err)
			}
		}()
		parts[index] = Part{File: &file}
	}
	events, err := s.Generate(ctx, generate)
	if err != nil {
		return err
	}
	var video *Blob
	finish := ""
	for event := range events {
		switch {
		case event.Err != nil:
			return event.Err
		case event.Kind == EventMedia && event.InlineData != nil && strings.HasPrefix(event.InlineData.MIME, "video/"):
			video = event.InlineData
		case event.FinishReason != "":
			finish = strings.TrimSpace(event.FinishReason + " " + event.FinishMessage)
		}
	}
	if video == nil {
		return fmt.Errorf("模型 %s 没有返回视频：%s", generate.Model, finish)
	}
	lease, owned, err := resolveAccountLease(ctx, s.pool, AccountSelection{ResourceID: operationID})
	if err != nil {
		return err
	}
	name, err := videoResultName(lease.Account(), time.Now())
	if err == nil {
		var file FileRef
		file, err = s.client.UploadFile(ContextWithAccountLease(ctx, lease), UploadRequest{
			AccountID: lease.Account().ID, Name: name, MIME: "video/mp4",
			Size: int64(len(video.Data)), Reader: bytes.NewReader(video.Data),
		})
		if err == nil {
			err = lease.BindVideoResult(ctx, operationID, file.ID)
		}
	}
	if owned {
		err = errors.Join(err, lease.Release())
	}
	return err
}
