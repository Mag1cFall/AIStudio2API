package aistudio

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"strings"
)

// EmbedContentRequest 表示一条 embedding 输入与参数
type EmbedContentRequest struct {
	Content              Content
	TaskType             string
	Title                string
	OutputDimensionality *int64
}

// EmbeddingRequest 定义一次经 Build 通道的 embedding 调用
type EmbeddingRequest struct {
	ID                    string
	Model                 string
	Requests              []EmbedContentRequest
	AccountID             string
	CandidateAccountIDs   []string
	RecoverWAARuntime     func(context.Context, string, error) (bool, error)
	ObserveAccountFailure func(string, error)
}

// EmbeddingResult 保存与输入顺序一致的向量和上游统计的输入 token 数
type EmbeddingResult struct {
	Embeddings [][]float64
	TokenCount int64
}

// EmbeddingService 定义 embedding 公开端点所需能力
type EmbeddingService interface {
	Embed(context.Context, EmbeddingRequest) (EmbeddingResult, error)
}

// BuildProtectedTransport 原子完成 Build 代理请求的 fresh WAA proof、field 3 写入和同 context 发送，string 参数为写入页面的提示词
type BuildProtectedTransport interface {
	DoProtectedBuild(context.Context, AccountSelection, string, RPCRequest) (*RPCResponse, error)
}

var _ BuildProtectedTransport = (*WorkerProtectedTransport)(nil)
var _ EmbeddingService = (*PooledService)(nil)

// buildEmbedContentRequest 按官网 SDK 的字段顺序表示 batchEmbedContents 的一条请求
type buildEmbedContentRequest struct {
	Content              buildEmbedContent `json:"content"`
	TaskType             string            `json:"taskType,omitempty"`
	Title                string            `json:"title,omitempty"`
	OutputDimensionality *int64            `json:"outputDimensionality,omitempty"`
	Model                string            `json:"model"`
}

type buildEmbedContent struct {
	Role  string `json:"role"`
	Parts []any  `json:"parts"`
}

// EncodeBuildEmbedRequest 编码 batchEmbedContents 的 Gemini API 路径与请求体，每条请求带模型名
func EncodeBuildEmbedRequest(model string, requests []EmbedContentRequest) (string, []byte, error) {
	wire := make([]buildEmbedContentRequest, 0, len(requests))
	for index, request := range requests {
		if len(request.Content.Parts) == 0 {
			return "", nil, fmt.Errorf("embedding 输入 %d 没有内容", index)
		}
		parts := make([]any, 0, len(request.Content.Parts))
		for partIndex, part := range request.Content.Parts {
			encoded, err := encodeBuildPart(part)
			if err != nil {
				return "", nil, fmt.Errorf("编码 embedding 输入 %d part %d: %w", index, partIndex, err)
			}
			parts = append(parts, encoded)
		}
		wire = append(wire, buildEmbedContentRequest{
			Content: buildEmbedContent{Role: "user", Parts: parts}, TaskType: request.TaskType, Title: request.Title,
			OutputDimensionality: request.OutputDimensionality, Model: "models/" + model,
		})
	}
	body, err := json.Marshal(map[string]any{"requests": wire})
	if err != nil {
		return "", nil, fmt.Errorf("编码 embedding 请求: %w", err)
	}
	return "/v1beta/models/" + model + ":batchEmbedContents", body, nil
}

// DecodeBuildEmbedResponse 解码 ProxyUnaryCall 返回的 BatchEmbedContentsResponse，向量数必须与请求数一致
func DecodeBuildEmbedResponse(source io.Reader, expected int) (EmbeddingResult, error) {
	raw, err := io.ReadAll(source)
	if err != nil {
		return EmbeddingResult{}, fmt.Errorf("读取 AI Studio %s: %w", buildProxyUnaryMethod, err)
	}
	decoded, err := decodeJSONValue(raw)
	if err != nil {
		return EmbeddingResult{}, &ProtocolEvidenceError{Method: buildProxyUnaryMethod, Path: "$", Detail: err.Error(), Raw: cloneRaw(raw)}
	}
	content, err := buildProxyResponseBody(decoded, "$")
	if err != nil {
		return EmbeddingResult{}, err
	}
	var response struct {
		Embeddings []struct {
			Values []float64 `json:"values"`
		} `json:"embeddings"`
		TokenCount json.Number `json:"tokenCount"`
	}
	if err := json.Unmarshal(content, &response); err != nil {
		return EmbeddingResult{}, &ProtocolEvidenceError{Method: buildProxyUnaryMethod, Path: "$embeddings", Detail: err.Error(), Raw: cloneRaw(content)}
	}
	if len(response.Embeddings) != expected {
		return EmbeddingResult{}, &ProtocolEvidenceError{
			Method: buildProxyUnaryMethod, Path: "$embeddings",
			Detail: fmt.Sprintf("返回 %d 个向量，请求 %d 条", len(response.Embeddings), expected), Raw: cloneRaw(content),
		}
	}
	result := EmbeddingResult{Embeddings: make([][]float64, 0, expected)}
	for _, embedding := range response.Embeddings {
		result.Embeddings = append(result.Embeddings, embedding.Values)
	}
	if response.TokenCount != "" {
		if result.TokenCount, err = response.TokenCount.Int64(); err != nil {
			return EmbeddingResult{}, &ProtocolEvidenceError{Method: buildProxyUnaryMethod, Path: "$tokenCount", Detail: err.Error(), Raw: cloneRaw(content)}
		}
	}
	return result, nil
}

// DoProtectedBuild 把 page 写入页面提示词并写入 fresh proof 后通过账户 Worker 发送 Build 代理请求，binding 为路径与请求体
func (t *WorkerProtectedTransport) DoProtectedBuild(ctx context.Context, selection AccountSelection, page string, rpc RPCRequest) (*RPCResponse, error) {
	prompt, err := buildBindingPrompt(rpc.Body)
	if err != nil {
		return nil, err
	}
	return t.doBrowserPrepared(ctx, prompt, page, buildProofField, selection, rpc)
}

// buildEmbedBatchLimit 是上游 batchEmbedContents 单次接受的最大请求数
const buildEmbedBatchLimit = 100

// embed 在当前 Build 租约上按上游批量上限依次发送 batchEmbedContents 并按输入顺序合并结果
func (c *Client) embed(ctx context.Context, request EmbeddingRequest) (EmbeddingResult, error) {
	transport, ok := c.protected.(BuildProtectedTransport)
	if !ok {
		return EmbeddingResult{}, fmt.Errorf("AI Studio protected transport 不支持 Build 代理请求")
	}
	var result EmbeddingResult
	for start := 0; start < len(request.Requests); start += buildEmbedBatchLimit {
		batch, err := c.embedBatch(ctx, transport, request, request.Requests[start:min(start+buildEmbedBatchLimit, len(request.Requests))])
		if err != nil {
			return EmbeddingResult{}, err
		}
		result.Embeddings = append(result.Embeddings, batch.Embeddings...)
		result.TokenCount += batch.TokenCount
	}
	return result, nil
}

// embedBatch 发送一次 batchEmbedContents
func (c *Client) embedBatch(ctx context.Context, transport BuildProtectedTransport, request EmbeddingRequest, requests []EmbedContentRequest) (EmbeddingResult, error) {
	path, body, err := EncodeBuildEmbedRequest(request.Model, requests)
	if err != nil {
		return EmbeddingResult{}, fmt.Errorf("%w: %v", ErrInvalidArgument, err)
	}
	proxy, err := EncodeBuildProxyRequest(path, body, true)
	if err != nil {
		return EmbeddingResult{}, fmt.Errorf("编码 Build 代理请求: %w", err)
	}
	rpc := newRPCRequest(buildProxyUnaryMethod, request.AccountID, request.ID, proxy, false)
	c.applyBenefitTier(rpc.Method, request.AccountID, rpc.Header)
	selection := AccountSelection{ModelID: request.Model, Method: "embedContent", AccountID: request.AccountID}
	inputs := make([]Content, 0, len(requests))
	for _, item := range requests {
		inputs = append(inputs, item.Content)
	}
	response, err := transport.DoProtectedBuild(ctx, selection, contentsText(inputs), rpc)
	if err != nil {
		return EmbeddingResult{}, fmt.Errorf("发送 AI Studio %s: %w", buildProxyUnaryMethod, err)
	}
	response, err = validateRPCResponse(buildProxyUnaryMethod, response)
	if err != nil {
		return EmbeddingResult{}, err
	}
	defer response.Body.Close()
	return DecodeBuildEmbedResponse(response.Body, len(requests))
}

// Embed 在支持模型的账户 Build 通道上调用 batchEmbedContents，额度错误写入 build:<模型> 冷却后换账户重试
func (s *PooledService) Embed(ctx context.Context, request EmbeddingRequest) (EmbeddingResult, error) {
	modelID := strings.TrimPrefix(strings.TrimSpace(request.Model), "models/")
	if modelID == "" {
		return EmbeddingResult{}, fmt.Errorf("%w: embedding model 不能为空", ErrInvalidArgument)
	}
	request.Model = modelID
	selection := AccountSelection{ModelID: modelID, Method: "embedContent"}
	maxAttempts := accountAttemptLimit(s.pool, false)
	attempted := make(map[string]struct{}, maxAttempts)
	recoveryAccountID := ""
	var requestErr error
	for attempt := 0; attempt < maxAttempts; attempt++ {
		selection.AllowedAccountIDs = remainingTranscriptionCandidates(request.CandidateAccountIDs, attempted)
		if request.CandidateAccountIDs != nil && len(selection.AllowedAccountIDs) == 0 {
			break
		}
		selection.AccountID = recoveryAccountID
		recoveryAccountID = ""
		lease, _, err := resolveAccountLease(ctx, s.pool, selection)
		if err != nil {
			if requestErr != nil && errors.Is(err, ErrNoEligibleAccount) {
				return EmbeddingResult{}, requestErr
			}
			return EmbeddingResult{}, err
		}
		accountID := lease.Account().ID
		attempted[accountID] = struct{}{}
		request.AccountID = accountID
		result, err := s.client.embed(ContextWithAccountLease(ctx, lease), request)
		if err == nil {
			accessGeneration, checkedAt, scope := lease.ModelAccessGeneration(), lease.CheckedAt(), lease.CooldownScope(modelID)
			if stateErr := errors.Join(lease.MarkAuthenticationValid(), lease.Release()); stateErr != nil {
				return result, stateErr
			}
			go func() {
				if _, err := s.pool.MarkModelAccessVerifiedIfGeneration(accountID, scope, accessGeneration, checkedAt); err != nil {
					slog.Error("账户 embedding 资格保存失败", "account", accountID, "model", modelID, "error", err)
				}
			}()
			return result, nil
		}
		requestErr = err
		retryable := retryableAccountError(err)
		var stateErr error
		if retryable {
			stateErr = s.markRetryableFailure(lease, lease.CooldownScope(modelID), err)
		}
		if releaseErr := lease.Release(); stateErr != nil || releaseErr != nil {
			return EmbeddingResult{}, errors.Join(requestErr, stateErr, releaseErr)
		}
		if request.RecoverWAARuntime != nil {
			recovered, recoveryErr := request.RecoverWAARuntime(ctx, accountID, err)
			if recoveryErr != nil {
				return EmbeddingResult{}, errors.Join(requestErr, recoveryErr)
			}
			if recovered {
				delete(attempted, accountID)
				recoveryAccountID = accountID
				maxAttempts++
				continue
			}
		}
		if (!retryable && !errors.Is(err, ErrAccountLeased)) || ctx.Err() != nil {
			return EmbeddingResult{}, requestErr
		}
		if request.ObserveAccountFailure != nil {
			request.ObserveAccountFailure(accountID, err)
		}
	}
	if requestErr != nil {
		return EmbeddingResult{}, requestErr
	}
	return EmbeddingResult{}, ErrNoEligibleAccount
}
