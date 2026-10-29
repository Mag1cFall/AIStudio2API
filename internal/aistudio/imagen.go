package aistudio

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
)

// ImageRequest 是 Imagen 文生图请求，Model 为实时目录中带 predict 方法的模型
type ImageRequest struct {
	Model          string
	Prompt         string
	SampleCount    int
	NegativePrompt string
	AspectRatio    string
	ImageSize      string
	AccountID      string
}

// GeneratedImage 是 GenerateImage 返回的一张图片，BlockedReason 为被安全策略拦下的原因
type GeneratedImage struct {
	Data          string
	MIMEType      string
	BlockedReason string
}

// ImageService 生成 Imagen 图片
type ImageService interface {
	GenerateImage(context.Context, ImageRequest) ([]GeneratedImage, error)
}

// ImageProtectedTransport 原子完成 GenerateImage fresh WAA proof、field 3 写入和同 context 发送
type ImageProtectedTransport interface {
	DoProtectedImage(context.Context, ImageRequest, RPCRequest) (*RPCResponse, error)
}

var _ ImageProtectedTransport = (*WorkerProtectedTransport)(nil)

// GenerateImage 以目录中有该模型 predict 方法的账户生成图片
func (s *PooledService) GenerateImage(ctx context.Context, request ImageRequest) ([]GeneratedImage, error) {
	request.Model = strings.TrimPrefix(strings.TrimSpace(request.Model), "models/")
	lease, owned, err := resolveAccountLease(ctx, s.pool, AccountSelection{
		ModelID: request.Model, Method: "predict", AccountID: strings.TrimSpace(request.AccountID),
	})
	if err != nil {
		return nil, err
	}
	request.AccountID = lease.Account().ID
	images, err := s.client.GenerateImage(ContextWithAccountLease(ctx, lease), request)
	if owned {
		err = errors.Join(err, lease.Release())
	}
	return images, err
}

// GenerateImage 按官网 Imagen 页发送 GenerateImage 并返回图片
func (c *Client) GenerateImage(ctx context.Context, request ImageRequest) ([]GeneratedImage, error) {
	entry, err := c.modelEntry(ctx, request.AccountID, request.Model)
	if err != nil {
		return nil, err
	}
	if !hasMethod(entry.model, "predict") {
		return nil, fmt.Errorf("%w: 模型 %q 的实时目录没有 predict 方法", ErrInvalidArgument, entry.model.ID)
	}
	rpc := newRPCRequest("GenerateImage", request.AccountID, "", EncodeGenerateImageRequest(request), false)
	c.applyBenefitTier(rpc.Method, request.AccountID, rpc.Header)
	response, err := c.protected.(ImageProtectedTransport).DoProtectedImage(ctx, request, rpc)
	if err != nil {
		return nil, fmt.Errorf("发送 AI Studio GenerateImage: %w", err)
	}
	response, err = validateRPCResponse("GenerateImage", response)
	if err != nil {
		return nil, err
	}
	defer response.Body.Close()
	return ParseGenerateImageResponse(response.Body)
}

// DoProtectedImage 写入 GenerateImage fresh proof 后经 Worker 页面发送请求，binding 与页面输入框均为提示词
func (t *WorkerProtectedTransport) DoProtectedImage(ctx context.Context, request ImageRequest, rpc RPCRequest) (*RPCResponse, error) {
	return t.doPrepared(ctx, request.Prompt, request.Prompt, 3, AccountSelection{
		ModelID: request.Model, Method: "predict", AccountID: request.AccountID,
	}, rpc)
}

// EncodeGenerateImageRequest 编码官网 GenerateImage 数组协议：模型、提示词、proof 位与图片数、反向提示词、宽高比、输出分辨率配置
func EncodeGenerateImageRequest(request ImageRequest) []byte {
	config := []any{nullableInt(request.SampleCount), nullableString(request.NegativePrompt), nullableString(request.AspectRatio), nullableString(request.ImageSize)}
	for len(config) > 0 && config[len(config)-1] == nil {
		config = config[:len(config)-1]
	}
	body, _ := json.Marshal([]any{"models/" + request.Model, request.Prompt, nil, config})
	return body
}

// ParseGenerateImageResponse 解码 GenerateImage 响应字段 1 的图片：1 base64 数据、2 MIME、3 安全拦截原因
func ParseGenerateImageResponse(body io.Reader) ([]GeneratedImage, error) {
	var message []json.RawMessage
	if err := json.NewDecoder(body).Decode(&message); err != nil {
		return nil, fmt.Errorf("解析 AI Studio GenerateImage 响应: %w", err)
	}
	if len(message) == 0 || string(message[0]) == "null" {
		return nil, nil
	}
	var items [][]any
	if err := json.Unmarshal(message[0], &items); err != nil {
		return nil, fmt.Errorf("解析 AI Studio GenerateImage 图片: %w", err)
	}
	images := make([]GeneratedImage, 0, len(items))
	for _, item := range items {
		var image GeneratedImage
		if len(item) > 0 {
			image.Data, _ = item[0].(string)
		}
		if len(item) > 1 {
			image.MIMEType, _ = item[1].(string)
		}
		if len(item) > 2 {
			image.BlockedReason, _ = item[2].(string)
		}
		images = append(images, image)
	}
	return images, nil
}

func nullableInt(value int) any {
	if value == 0 {
		return nil
	}
	return value
}

func nullableString(value string) any {
	if value == "" {
		return nil
	}
	return value
}
