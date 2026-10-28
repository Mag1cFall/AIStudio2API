package aistudio

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"slices"
	"strconv"
	"strings"
)

// UnverifiedProtocolError 表示请求使用了尚未验证的协议能力
type UnverifiedProtocolError struct {
	Feature string
}

// Error 返回未验证协议能力的说明
func (e *UnverifiedProtocolError) Error() string {
	return "AI Studio 协议能力尚无成功现场证据: " + e.Feature
}

// EncodeCountTokensRequest 编码 CountTokens 请求
func EncodeCountTokensRequest(request TokenCountRequest) ([]byte, error) {
	tools, explicitTools, err := encodeRequestedTools(request.Tools)
	if err != nil {
		return nil, err
	}
	contents, err := encodeContents(request.Contents)
	if err != nil {
		return nil, err
	}
	if len(contents) == 0 && len(request.System) == 0 {
		return nil, fmt.Errorf("CountTokens contents 不能为空")
	}
	resolution, err := playgroundMediaResolution(request.MediaResolution, request.Contents)
	if err != nil {
		return nil, err
	}
	if len(request.System) > 0 || explicitTools || resolution != nil || countTokensNeedsGenerateRequest(request.Contents) {
		length := 2
		if resolution != nil {
			length = 4
		}
		if len(request.System) > 0 {
			length = 6
		}
		if explicitTools {
			length = 7
		}
		generate := make([]any, length)
		generate[0] = wireModelName(request.Model)
		if len(contents) > 0 {
			generate[1] = contents
		}
		if resolution != nil {
			config := make([]any, 18)
			config[17] = resolution
			generate[3] = config
		}
		if len(request.System) > 0 {
			generate[5] = encodeSystemInstruction(request.System)
		}
		if explicitTools {
			generate[6] = tools
		}
		return json.Marshal([]any{wireModelName(request.Model), nil, generate})
	}
	return json.Marshal([]any{wireModelName(request.Model), contents})
}

// ParseTokenCount 解码 CountTokens field 1
func ParseTokenCount(source io.Reader) (TokenCount, error) {
	raw, err := io.ReadAll(newSparseJSONReader(source))
	if err != nil {
		return TokenCount{}, fmt.Errorf("读取 CountTokens: %w", err)
	}
	root, err := rawArray(raw, "$", raw)
	if err != nil {
		return TokenCount{}, withMethod(err, "CountTokens")
	}
	if len(root) == 0 || isJSONNull(root[0]) {
		return TokenCount{}, &ProtocolEvidenceError{Method: "CountTokens", Path: "$[0]", Detail: "缺少权威 token 总数", Raw: raw}
	}
	count, err := rawInt64(root[0], "$[0]", raw)
	if err != nil {
		return TokenCount{}, withMethod(err, "CountTokens")
	}
	details, err := decodeModalityTokenCounts(rawAt(root, 5), "$[5]", raw)
	if err != nil {
		return TokenCount{}, withMethod(err, "CountTokens")
	}
	result := TokenCount{InputTokens: count, PromptTokensDetails: details}
	if field := rawAt(root, 1); !isJSONNull(field) {
		system, err := decodeContentTokens(field, "$[1]", raw)
		if err != nil {
			return TokenCount{}, err
		}
		result.InputTokens -= system.RoleTokens
		result.PromptTokensDetails = subtractTextTokens(details, system.RoleTokens)
		result.System = &ContentTokens{PartTokens: system.PartTokens}
	}
	contents, err := rawItems(rawAt(root, 2), "$[2]", raw)
	if err != nil {
		return TokenCount{}, err
	}
	for index, item := range contents {
		content, err := decodeContentTokens(item, fmt.Sprintf("$[2][%d]", index), raw)
		if err != nil {
			return TokenCount{}, err
		}
		result.Contents = append(result.Contents, content)
	}
	tools, err := rawItems(rawAt(root, 3), "$[3]", raw)
	if err != nil {
		return TokenCount{}, err
	}
	for index, item := range tools {
		fields, err := rawArray(item, fmt.Sprintf("$[3][%d]", index), raw)
		if err != nil {
			return TokenCount{}, withMethod(err, "CountTokens")
		}
		declarations, err := intSliceField(fields, 0, fmt.Sprintf("$[3][%d]", index), raw)
		if err != nil {
			return TokenCount{}, withMethod(err, "CountTokens")
		}
		result.Tools = append(result.Tools, ToolTokens{FunctionDeclarationTokens: declarations})
	}
	return result, nil
}

// rawItems 读取可为空的 CountTokens 重复字段
func rawItems(raw json.RawMessage, path string, evidence json.RawMessage) ([]json.RawMessage, error) {
	if isJSONNull(raw) {
		return nil, nil
	}
	items, err := rawArray(raw, path, evidence)
	if err != nil {
		return nil, withMethod(err, "CountTokens")
	}
	return items, nil
}

// decodeContentTokens 解码 CountTokens 的 [逐 Part token 数, 角色 token 数]
func decodeContentTokens(raw json.RawMessage, path string, evidence json.RawMessage) (ContentTokens, error) {
	fields, err := rawArray(raw, path, evidence)
	if err != nil {
		return ContentTokens{}, withMethod(err, "CountTokens")
	}
	parts, err := intSliceField(fields, 0, path, evidence)
	if err != nil {
		return ContentTokens{}, withMethod(err, "CountTokens")
	}
	role, err := optionalIntField(fields, 1, path, evidence)
	if err != nil {
		return ContentTokens{}, withMethod(err, "CountTokens")
	}
	return ContentTokens{PartTokens: parts, RoleTokens: role}, nil
}

// subtractTextTokens 返回 TEXT 模态减去 tokens 后的模态细分
func subtractTextTokens(details []ModalityTokenCount, tokens int64) []ModalityTokenCount {
	result := slices.Clone(details)
	for index := range result {
		if result[index].Modality == "TEXT" {
			result[index].TokenCount -= tokens
		}
	}
	return result
}

func (c *Client) CountTokens(ctx context.Context, request TokenCountRequest) (TokenCount, error) {
	return c.CountTokensForAccount(ctx, "", request)
}

// CountTokensForAccount 使用指定账户调用权威 token 计数，Build 租约经 Build 代理计数
func (c *Client) CountTokensForAccount(ctx context.Context, accountID string, request TokenCountRequest) (TokenCount, error) {
	if lease, ok := AccountLeaseFromContext(ctx); ok && lease.Channel() == ChannelBuild {
		return c.buildCountTokens(ctx, accountID, request)
	}
	if entry, err := c.modelEntry(ctx, accountID, request.Model); err == nil && entry.model.Capabilities["assistant_route"] {
		request.Model = assistantBaseModelID(request.Model)
	}
	body, err := EncodeCountTokensRequest(request)
	if err != nil {
		return TokenCount{}, fmt.Errorf("%w: %v", ErrInvalidArgument, err)
	}
	response, err := c.do(ctx, "CountTokens", accountID, "", body, false)
	if err != nil {
		return TokenCount{}, err
	}
	defer response.Body.Close()
	return ParseTokenCount(response.Body)
}

func encodeContents(contents []Content) ([]any, error) {
	wire := make([]any, 0, len(contents))
	var pendingCalls []FunctionCall
	for index, content := range contents {
		if len(content.Parts) == 0 {
			continue
		}
		linked, err := linkFunctionResults(content, &pendingCalls)
		if err != nil {
			return nil, fmt.Errorf("编码 content %d: %w", index, err)
		}
		encoded, err := encodeContent(linked)
		if err != nil {
			return nil, fmt.Errorf("编码 content %d: %w", index, err)
		}
		wire = append(wire, encoded)
	}
	return wire, nil
}

// linkFunctionResults 记录 content 中的函数调用，按调用 ID 或唯一待返回调用为缺少名称的函数结果补齐名称，无法关联时返回错误
func linkFunctionResults(content Content, pendingCalls *[]FunctionCall) (Content, error) {
	if content.Role == RoleUser && !slices.ContainsFunc(content.Parts, func(part Part) bool { return part.FunctionResult != nil }) {
		*pendingCalls = nil
	}
	cloned := false
	for index, part := range content.Parts {
		if part.FunctionCall != nil {
			*pendingCalls = append(*pendingCalls, *part.FunctionCall)
		}
		result := part.FunctionResult
		if result == nil {
			continue
		}
		matched := slices.IndexFunc(*pendingCalls, func(call FunctionCall) bool { return result.ID != "" && call.ID == result.ID })
		if matched < 0 && len(*pendingCalls) == 1 && (result.Name == "" || result.Name == (*pendingCalls)[0].Name) {
			matched = 0
		}
		if matched < 0 {
			if result.Name == "" {
				return Content{}, fmt.Errorf("part %d: function result 缺少名称且无法唯一关联调用", index)
			}
			continue
		}
		if result.Name == "" {
			if !cloned {
				content.Parts = slices.Clone(content.Parts)
				cloned = true
			}
			named := cloneFunctionResult(result)
			named.Name = (*pendingCalls)[matched].Name
			content.Parts[index].FunctionResult = named
		}
		*pendingCalls = slices.Delete(*pendingCalls, matched, matched+1)
	}
	return content, nil
}

func encodeContent(content Content) ([]any, error) {
	role := ""
	switch content.Role {
	case RoleUser:
		role = "user"
	case RoleAssistant:
		role = "model"
	case RoleTool:
		role = "user"
	default:
		return nil, fmt.Errorf("未知 content role %q", content.Role)
	}
	if len(content.Parts) == 0 {
		return nil, fmt.Errorf("content parts 不能为空")
	}
	parts := make([]any, 0, len(content.Parts))
	for index, part := range content.Parts {
		encoded, err := encodePart(part)
		if err != nil {
			return nil, fmt.Errorf("编码 part %d: %w", index, err)
		}
		parts = append(parts, encoded)
	}
	return []any{parts, role}, nil
}

func encodePart(part Part) ([]any, error) {
	variants := 0
	if part.Text != "" {
		variants++
	}
	if part.InlineData != nil {
		variants++
	}
	if part.ExternalMedia != nil {
		variants++
	}
	if part.File != nil {
		variants++
	}
	if part.Upload != nil {
		variants++
	}
	if part.FunctionCall != nil {
		variants++
	}
	if part.FunctionResult != nil {
		variants++
	}
	if part.ExecutableCode != nil {
		variants++
	}
	if part.CodeExecutionResult != nil {
		variants++
	}
	if part.ToolCall != nil || part.ToolResponse != nil {
		variants++
	}
	if variants == 0 && part.ThoughtSignature != "" {
		return setPartThoughtSignature([]any{}, part.ThoughtSignature), nil
	}
	if variants != 1 {
		return nil, fmt.Errorf("part 必须且只能设置一种内容")
	}
	if part.Text != "" {
		wire := []any{nil, part.Text}
		if part.Thought {
			for len(wire) <= 12 {
				wire = append(wire, nil)
			}
			wire[12] = true
		}
		if metadata := part.SpeechMetadata; metadata != nil && (metadata.Speaker != "" || metadata.Style != "") {
			for len(wire) <= 40 {
				wire = append(wire, nil)
			}
			// Part field 41 SpeechMetadata：speaker、style
			encoded := []any{nil}
			if metadata.Speaker != "" {
				encoded[0] = metadata.Speaker
			}
			if metadata.Style != "" {
				encoded = append(encoded, metadata.Style)
			}
			wire[40] = encoded
		}
		return setPartThoughtSignature(wire, part.ThoughtSignature), nil
	}
	if part.InlineData != nil {
		if part.InlineData.MIME == "" || len(part.InlineData.Data) == 0 {
			return nil, fmt.Errorf("inline data 缺少 MIME 或数据")
		}
		wire := []any{nil, nil, []any{part.InlineData.MIME, base64.StdEncoding.EncodeToString(part.InlineData.Data)}}
		return encodeVideoPart(wire, part)
	}
	if part.ExternalMedia != nil {
		if part.ExternalMedia.MIME == "" || part.ExternalMedia.URL == "" {
			return nil, fmt.Errorf("外部媒体缺少 MIME 或 URL")
		}
		wire := make([]any, 7)
		wire[6] = []any{part.ExternalMedia.MIME, part.ExternalMedia.URL}
		if part.DisplayName != "" {
			// FileData field 6 display_name
			wire[6] = []any{part.ExternalMedia.MIME, part.ExternalMedia.URL, nil, nil, nil, part.DisplayName}
		}
		return encodeVideoPart(wire, part)
	}
	if part.File != nil {
		wire, err := encodeFilePart(part.File)
		if err != nil {
			return nil, err
		}
		return encodeVideoPart(wire, part)
	}
	if upload := part.Upload; upload != nil {
		// Part field 18 上传附件：MIME、上传路径、文件名、字节数
		wire := make([]any, 18)
		wire[17] = []any{upload.MIME, upload.Path, upload.Name, strconv.FormatInt(upload.Size, 10)}
		return encodeVideoPart(wire, part)
	}
	if part.FunctionCall != nil {
		if err := validateFunctionCall(part.FunctionCall); err != nil {
			return nil, err
		}
		arguments, err := encodeWireStructJSON(part.FunctionCall.Arguments)
		if err != nil {
			return nil, fmt.Errorf("function call arguments: %w", err)
		}
		call := []any{part.FunctionCall.Name, arguments}
		if part.FunctionCall.ID != "" {
			call = append(call, part.FunctionCall.ID)
		}
		wire := make([]any, 11)
		wire[10] = call
		signature := part.ThoughtSignature
		if signature == "" {
			signature = part.FunctionCall.ThoughtSignature
		}
		if signature == "" {
			signature = "skip_thought_signature_validator"
		}
		return setPartThoughtSignature(wire, signature), nil
	}
	if part.FunctionResult != nil {
		response, err := encodeWireStructJSON(part.FunctionResult.Content)
		if err != nil {
			return nil, fmt.Errorf("function result content: %w", err)
		}
		result := []any{part.FunctionResult.Name, response}
		if part.FunctionResult.ID != "" {
			result = append(result, part.FunctionResult.ID)
		}
		wire := make([]any, 12)
		wire[11] = result
		return setPartThoughtSignature(wire, part.ThoughtSignature), nil
	}
	if part.ToolCall != nil || part.ToolResponse != nil {
		step, index := part.ToolCall, 25
		if step == nil {
			step, index = part.ToolResponse, 26
		}
		value, err := encodeServerToolStep(step)
		if err != nil {
			return nil, err
		}
		wire := make([]any, index+1)
		wire[index] = value
		return setPartThoughtSignature(wire, part.ThoughtSignature), nil
	}
	if part.ExecutableCode != nil {
		language, ok := map[string]int64{"LANGUAGE_UNSPECIFIED": 0, "PYTHON": 1}[part.ExecutableCode.Language]
		if !ok {
			return nil, fmt.Errorf("未识别的 executable code language %q", part.ExecutableCode.Language)
		}
		wire := make([]any, 8)
		wire[7] = []any{language, part.ExecutableCode.Code}
		return setPartThoughtSignature(wire, part.ThoughtSignature), nil
	}
	outcome, ok := map[string]int64{
		"OUTCOME_UNSPECIFIED":       0,
		"OUTCOME_OK":                1,
		"OUTCOME_FAILED":            2,
		"OUTCOME_DEADLINE_EXCEEDED": 3,
	}[part.CodeExecutionResult.Outcome]
	if !ok {
		return nil, fmt.Errorf("未识别的 code execution outcome %q", part.CodeExecutionResult.Outcome)
	}
	result := []any{outcome}
	if part.CodeExecutionResult.Output != "" {
		result = append(result, part.CodeExecutionResult.Output)
	}
	wire := make([]any, 9)
	wire[8] = result
	return setPartThoughtSignature(wire, part.ThoughtSignature), nil
}

func encodeSystemInstruction(system []string) []any {
	parts := make([]any, 0, len(system))
	for _, text := range system {
		parts = append(parts, []any{nil, text})
	}
	return []any{parts, "user"}
}

func wireModelName(model string) string {
	if strings.HasPrefix(model, "models/") {
		return model
	}
	return "models/" + model
}

func countTokensNeedsGenerateRequest(contents []Content) bool {
	for _, content := range contents {
		for _, part := range content.Parts {
			if part.InlineData != nil || part.ExternalMedia != nil || part.File != nil || part.FunctionCall != nil || part.FunctionResult != nil ||
				part.ExecutableCode != nil || part.CodeExecutionResult != nil || part.ToolCall != nil || part.ToolResponse != nil {
				return true
			}
		}
	}
	return false
}

func cloneFunctionResult(result *FunctionResult) *FunctionResult {
	cloned := *result
	cloned.Content = append(json.RawMessage(nil), result.Content...)
	return &cloned
}

func setPartThoughtSignature(wire []any, signature string) []any {
	if signature == "" {
		return wire
	}
	for len(wire) <= 14 {
		wire = append(wire, nil)
	}
	wire[14] = signature
	return wire
}
