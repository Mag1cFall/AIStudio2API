package aistudio

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
)

// VideoFramesRequest 是按 Drive 视频文件抽帧并统计 token 数的请求，VideoMetadata 为视频设置的截取与帧率
type VideoFramesRequest struct {
	Model         string
	FileID        string
	VideoMetadata *VideoMetadata
}

// VideoFrameService 统计视频文件按视频设置抽帧后的 token 数
type VideoFrameService interface {
	VideoFrameTokens(context.Context, VideoFramesRequest) (int64, error)
}

// encodeVideoFramesRequest 按官网编码 StreamExtractVideoFrames：field 1 模型、field 3 Drive 文件，设置截取或帧率时 field 7 为截取与帧率
func encodeVideoFramesRequest(request VideoFramesRequest) ([]byte, error) {
	wire := []any{wireModelName(request.Model), nil, []any{request.FileID}}
	metadata, err := videoMetadataWire(request.VideoMetadata)
	if err != nil {
		return nil, err
	}
	if metadata != nil {
		wire = append(wire, nil, nil, nil, metadata)
	}
	return json.Marshal(wire)
}

// decodeVideoFrameTokens 读取 StreamExtractVideoFrames 流中各条消息 field 5 的 token 数，取最后一条
func decodeVideoFrameTokens(raw []byte) (int64, error) {
	chunks, err := rawArray(raw, "$", raw)
	if err != nil {
		return 0, err
	}
	tokens, found := int64(0), false
	for index, chunk := range chunks {
		items, err := rawArray(chunk, fmt.Sprintf("$[%d]", index), raw)
		if err != nil || len(items) == 0 {
			continue
		}
		message, err := rawArray(items[0], fmt.Sprintf("$[%d][0]", index), raw)
		if err != nil || len(message) < 5 {
			continue
		}
		if value, err := rawInt64(message[4], fmt.Sprintf("$[%d][0][4]", index), raw); err == nil {
			tokens, found = value, true
		}
	}
	if !found {
		return 0, &ProtocolEvidenceError{Method: "StreamExtractVideoFrames", Path: "$[*][0][4]", Detail: "缺少 token 数", Raw: cloneRaw(raw)}
	}
	return tokens, nil
}

// VideoFrameTokensForAccount 以指定账户调用 StreamExtractVideoFrames 并返回按视频设置抽帧后的 token 数
func (c *Client) VideoFrameTokensForAccount(ctx context.Context, accountID string, request VideoFramesRequest) (int64, error) {
	body, err := encodeVideoFramesRequest(request)
	if err != nil {
		return 0, fmt.Errorf("%w: %v", ErrInvalidArgument, err)
	}
	response, err := c.do(ctx, "StreamExtractVideoFrames", accountID, "", body, true)
	if err != nil {
		return 0, err
	}
	defer response.Body.Close()
	raw, err := io.ReadAll(response.Body)
	if err != nil {
		return 0, fmt.Errorf("读取 AI Studio StreamExtractVideoFrames: %w", err)
	}
	return decodeVideoFrameTokens(raw)
}

// VideoFrameTokens 以视频文件所属账户统计按视频设置抽帧后的 token 数，与官网视频附件显示的 token 数相同
func (s *PooledService) VideoFrameTokens(ctx context.Context, request VideoFramesRequest) (int64, error) {
	lease, owned, err := resolveAccountLease(ctx, s.pool, AccountSelection{ResourceID: strings.TrimSpace(request.FileID)})
	if err != nil {
		return 0, err
	}
	tokens, err := s.client.VideoFrameTokensForAccount(ContextWithAccountLease(ctx, lease), lease.Account().ID, request)
	if DefinitiveAuthenticationFailure(err) {
		err = errors.Join(err, lease.ConfirmAuthenticationFailure(ctx, err))
	}
	if owned {
		err = errors.Join(err, lease.Release())
	}
	return tokens, err
}
