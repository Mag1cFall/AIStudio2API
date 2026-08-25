package aistudio

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"strings"
)

// UnverifiedProtocolError 表示当前现场证据尚不足以发送某项能力
type UnverifiedProtocolError struct {
	Feature string
}

// Error 返回未验证协议边界
func (e *UnverifiedProtocolError) Error() string {
	return "AI Studio 协议能力尚无成功现场证据: " + e.Feature
}

// EncodeCountTokensRequest 编码现场确认的 CountTokens 请求
func EncodeCountTokensRequest(request TokenCountRequest) ([]byte, error) {
	contents, err := encodeContents(request.Contents)
	if err != nil {
		return nil, err
	}
	if len(contents) == 0 && request.System == "" {
		return nil, fmt.Errorf("CountTokens contents 不能为空")
	}
	if request.System != "" || countTokensNeedsGenerateRequest(request.Contents) {
		length := 2
		if request.System != "" {
			length = 6
		}
		generate := make([]any, length)
		generate[0] = wireModelName(request.Model)
		if len(contents) > 0 {
			generate[1] = contents
		}
		if request.System != "" {
			generate[5] = encodeSystemInstruction(request.System)
		}
		return json.Marshal([]any{wireModelName(request.Model), nil, generate})
	}
	return json.Marshal([]any{wireModelName(request.Model), contents})
}

// ParseTokenCount 解码现场确认的 CountTokens field 1
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
	return TokenCount{InputTokens: count}, nil
}

func (c *Client) CountTokens(ctx context.Context, request TokenCountRequest) (TokenCount, error) {
	return c.CountTokensForAccount(ctx, "", request)
}

// CountTokensForAccount 使用指定账户调用权威 token 计数
func (c *Client) CountTokensForAccount(ctx context.Context, accountID string, request TokenCountRequest) (TokenCount, error) {
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
	functionNames := make(map[string]string)
	for index, content := range contents {
		encoded, err := encodeContent(content, functionNames)
		if err != nil {
			return nil, fmt.Errorf("编码 content %d: %w", index, err)
		}
		wire = append(wire, encoded)
	}
	return wire, nil
}

func encodeContent(content Content, functionNames map[string]string) ([]any, error) {
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
		if part.FunctionCall != nil && part.FunctionCall.ID != "" {
			functionNames[part.FunctionCall.ID] = part.FunctionCall.Name
		}
		if part.FunctionResult != nil && part.FunctionResult.Name == "" {
			part.FunctionResult = cloneFunctionResult(part.FunctionResult)
			part.FunctionResult.Name = functionNames[part.FunctionResult.ID]
		}
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
	if part.File != nil {
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
	if variants == 0 && part.ThoughtSignature != "" {
		return setPartThoughtSignature([]any{}, part.ThoughtSignature), nil
	}
	if variants != 1 {
		return nil, fmt.Errorf("part 必须且只能设置一种内容")
	}
	if part.Text != "" {
		return setPartThoughtSignature([]any{nil, part.Text}, part.ThoughtSignature), nil
	}
	if part.InlineData != nil {
		if part.InlineData.MIME == "" || len(part.InlineData.Data) == 0 {
			return nil, fmt.Errorf("inline data 缺少 MIME 或数据")
		}
		wire := []any{nil, nil, []any{part.InlineData.MIME, base64.StdEncoding.EncodeToString(part.InlineData.Data)}}
		return setPartThoughtSignature(wire, part.ThoughtSignature), nil
	}
	if part.File != nil {
		wire, err := encodeFilePart(part.File)
		if err != nil {
			return nil, err
		}
		return setPartThoughtSignature(wire, part.ThoughtSignature), nil
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
		return setPartThoughtSignature(wire, signature), nil
	}
	if part.FunctionResult != nil {
		if part.FunctionResult.Name == "" {
			return nil, fmt.Errorf("function result 缺少名称且无法按 call ID 解析")
		}
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

func encodeSystemInstruction(system string) []any {
	return []any{[]any{[]any{nil, system}}, "user"}
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
			if part.InlineData != nil || part.File != nil || part.FunctionCall != nil || part.FunctionResult != nil ||
				part.ExecutableCode != nil || part.CodeExecutionResult != nil {
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
