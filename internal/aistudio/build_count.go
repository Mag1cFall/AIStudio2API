package aistudio

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"strings"
)

// EncodeBuildCountTokensRequest 编码 countTokens 的 Gemini API 路径与请求体，内容、系统指令、工具与媒体分辨率放在 generateContentRequest
func EncodeBuildCountTokensRequest(request TokenCountRequest) (string, []byte, error) {
	model := strings.TrimPrefix(strings.TrimSpace(request.Model), "models/")
	contents, err := encodeBuildContents(request.Contents)
	if err != nil {
		return "", nil, err
	}
	generate := map[string]any{"model": "models/" + model, "contents": contents}
	if system := buildSystemInstruction(request.System); system != nil {
		generate["systemInstruction"] = system
	}
	tools, err := encodeBuildTools(request.Tools)
	if err != nil {
		return "", nil, err
	}
	if len(tools) > 0 {
		generate["tools"] = tools
	}
	if request.MediaResolution != "" {
		generate["generationConfig"] = map[string]any{"mediaResolution": request.MediaResolution}
	}
	body, err := json.Marshal(map[string]any{"generateContentRequest": generate})
	if err != nil {
		return "", nil, fmt.Errorf("编码 Build countTokens: %w", err)
	}
	return "/v1beta/models/" + model + ":countTokens", body, nil
}

// DecodeBuildCountTokensResponse 解码 ProxyUnaryCall 返回的 CountTokensResponse
func DecodeBuildCountTokensResponse(source io.Reader) (TokenCount, error) {
	raw, err := io.ReadAll(source)
	if err != nil {
		return TokenCount{}, fmt.Errorf("读取 AI Studio %s: %w", buildProxyUnaryMethod, err)
	}
	decoded, err := decodeJSONValue(raw)
	if err != nil {
		return TokenCount{}, &ProtocolEvidenceError{Method: buildProxyUnaryMethod, Path: "$", Detail: err.Error(), Raw: cloneRaw(raw)}
	}
	content, err := buildProxyResponseBody(decoded, "$")
	if err != nil {
		return TokenCount{}, err
	}
	var response struct {
		TotalTokens              *int64               `json:"totalTokens"`
		SystemInstructionsTokens *ContentTokens       `json:"systemInstructionsTokens"`
		ContentTokens            []ContentTokens      `json:"contentTokens"`
		ToolTokens               []ToolTokens         `json:"toolTokens"`
		PromptTokensDetails      []ModalityTokenCount `json:"promptTokensDetails"`
	}
	if err := json.Unmarshal(content, &response); err != nil || response.TotalTokens == nil {
		return TokenCount{}, &ProtocolEvidenceError{Method: buildProxyUnaryMethod, Path: "$totalTokens", Detail: "缺少权威 token 总数", Raw: cloneRaw(content)}
	}
	return TokenCount{
		InputTokens: *response.TotalTokens, PromptTokensDetails: response.PromptTokensDetails,
		System: response.SystemInstructionsTokens, Contents: response.ContentTokens, Tools: response.ToolTokens,
	}, nil
}

// buildCountTokens 在 Build 租约上经 ProxyUnaryCall 调用 countTokens
func (c *Client) buildCountTokens(ctx context.Context, accountID string, request TokenCountRequest) (TokenCount, error) {
	transport, ok := c.protected.(BuildProtectedTransport)
	if !ok {
		return TokenCount{}, fmt.Errorf("AI Studio protected transport 不支持 Build 代理请求")
	}
	path, body, err := EncodeBuildCountTokensRequest(request)
	if err != nil {
		return TokenCount{}, fmt.Errorf("%w: %v", ErrInvalidArgument, err)
	}
	proxy, err := EncodeBuildProxyRequest(path, body, true)
	if err != nil {
		return TokenCount{}, fmt.Errorf("编码 Build 代理请求: %w", err)
	}
	rpc := newRPCRequest(buildProxyUnaryMethod, accountID, "", proxy, false)
	c.applyBenefitTier(rpc.Method, accountID, rpc.Header)
	selection := AccountSelection{
		ModelID: request.Model, ModelAccessScope: ModelAccessKey("count-tokens", request.Model), Method: "countTokens", AccountID: accountID, Channel: ChannelBuild,
	}
	response, err := transport.DoProtectedBuild(ctx, selection, contentsText(request.Contents), rpc)
	if err != nil {
		return TokenCount{}, fmt.Errorf("发送 AI Studio %s: %w", buildProxyUnaryMethod, err)
	}
	response, err = validateRPCResponse(buildProxyUnaryMethod, response)
	if err != nil {
		return TokenCount{}, err
	}
	defer response.Body.Close()
	return DecodeBuildCountTokensResponse(response.Body)
}
