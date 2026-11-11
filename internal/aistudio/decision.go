package aistudio

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
)

// DecisionRequest 是 Decisions API 请求，Model 为实时目录中带 createDecision 方法的模型，Input 为各问题共用的输入
type DecisionRequest struct {
	Model     string
	Input     []Part
	Questions []DecisionQuestion
	AccountID string
}

// DecisionQuestion 是一个决策问题，Type 为 noul、choice 或 score
type DecisionQuestion struct {
	Key          string
	Type         string
	Instructions []string
	// Criteria 是 noul 为真与为假的说明，HasCriteria 表示请求给出了 criteria
	TrueDescription  string
	FalseDescription string
	HasCriteria      bool
	// Options 是 choice 的选项 ID 与说明
	Options []DecisionOption
	// Levels 是 score 从低到高各等级的说明
	Levels []string
}

// DecisionOption 是 choice 问题的一个选项
type DecisionOption struct {
	ID          string
	Description string
}

// DecisionAnswer 是一个问题的回答，Probabilities 与 Legend 按上游顺序排列
type DecisionAnswer struct {
	Key           string
	Type          string
	Confidence    float64
	Noul          float64
	Choice        string
	Score         float64
	Probabilities []DecisionProbability
	Legend        []DecisionProbability
}

// DecisionProbability 是选项或等级的概率，Legend 中 Description 为等级说明
type DecisionProbability struct {
	Key         string
	Value       float64
	Description string
}

// DecisionModalityTokens 是一种输入模态的 token 数，Modality 为 text、image 或 audio
type DecisionModalityTokens struct {
	Modality string
	Tokens   int64
}

// DecisionResult 是 CreateDecision 的回答与输入用量
type DecisionResult struct {
	Answers         []DecisionAnswer
	InputTokens     int64
	InputByModality []DecisionModalityTokens
	TotalTokens     int64
}

// DecisionService 以 Gemini Decision 模型回答决策问题
type DecisionService interface {
	CreateDecision(context.Context, DecisionRequest) (DecisionResult, error)
}

// DecisionProtectedTransport 原子完成 CreateDecision fresh WAA proof、field 5 写入和同 context 发送
type DecisionProtectedTransport interface {
	DoProtectedDecision(context.Context, DecisionRequest, RPCRequest) (*RPCResponse, error)
}

var _ DecisionProtectedTransport = (*WorkerProtectedTransport)(nil)

// decisionModalities 是 CreateDecision 用量中的模态枚举
var decisionModalities = map[int64]string{1: "text", 2: "image", 3: "audio"}

// CreateDecision 以目录中有该模型 createDecision 方法的账户回答决策问题
func (s *PooledService) CreateDecision(ctx context.Context, request DecisionRequest) (DecisionResult, error) {
	request.Model = strings.TrimPrefix(strings.TrimSpace(request.Model), "models/")
	lease, owned, err := resolveAccountLease(ctx, s.pool, AccountSelection{
		ModelID: request.Model, Method: "createDecision", AccountID: strings.TrimSpace(request.AccountID),
	})
	if err != nil {
		return DecisionResult{}, err
	}
	request.AccountID = lease.Account().ID
	result, err := s.client.CreateDecision(ContextWithAccountLease(ctx, lease), request)
	if owned {
		err = errors.Join(err, lease.Release())
	}
	return result, err
}

// CreateDecision 发送 CreateDecision 并返回各问题的回答
func (c *Client) CreateDecision(ctx context.Context, request DecisionRequest) (DecisionResult, error) {
	entry, err := c.modelEntry(ctx, request.AccountID, request.Model)
	if err != nil {
		return DecisionResult{}, err
	}
	if !hasMethod(entry.model, "createDecision") {
		return DecisionResult{}, fmt.Errorf("%w: 模型 %q 的实时目录没有 createDecision 方法", ErrInvalidArgument, entry.model.ID)
	}
	body, err := EncodeCreateDecisionRequest(request)
	if err != nil {
		return DecisionResult{}, fmt.Errorf("%w: %v", ErrInvalidArgument, err)
	}
	rpc := newRPCRequest("CreateDecision", request.AccountID, "", body, false)
	c.applyBenefitTier(rpc.Method, request.AccountID, rpc.Header)
	response, err := c.protected.(DecisionProtectedTransport).DoProtectedDecision(ctx, request, rpc)
	if err != nil {
		return DecisionResult{}, fmt.Errorf("发送 AI Studio CreateDecision: %w", err)
	}
	response, err = validateRPCResponse("CreateDecision", response)
	if err != nil {
		return DecisionResult{}, err
	}
	defer response.Body.Close()
	return DecodeCreateDecisionResponse(response.Body)
}

// DoProtectedDecision 写入 CreateDecision fresh proof 后经账户的 WAA 运行时发送，binding 为输入各块以空格连接，页面输入框为空
func (t *WorkerProtectedTransport) DoProtectedDecision(ctx context.Context, request DecisionRequest, rpc RPCRequest) (*RPCResponse, error) {
	prompt, err := bindingPrompt(GenerateRequest{Contents: []Content{{Role: RoleUser, Parts: request.Input}}})
	if err != nil {
		return nil, err
	}
	return t.doPrepared(ctx, prompt, "", 5, "", AccountSelection{
		ModelID: request.Model, Method: "createDecision", AccountID: request.AccountID,
	}, rpc)
}

// EncodeCreateDecisionRequest 编码官网 CreateDecision 数组协议：模型、输入 Content 列表、问题 map 与 field 5 的 proof 位
func EncodeCreateDecisionRequest(request DecisionRequest) ([]byte, error) {
	if len(request.Input) == 0 {
		return nil, fmt.Errorf("input 不能为空")
	}
	input := make([]any, 0, len(request.Input))
	for index, part := range request.Input {
		content, err := decisionContent(part)
		if err != nil {
			return nil, fmt.Errorf("input[%d]: %w", index, err)
		}
		input = append(input, content)
	}
	questions := make([]any, 0, len(request.Questions))
	for _, question := range request.Questions {
		instructions := make([]any, 0, len(question.Instructions))
		for _, text := range question.Instructions {
			instructions = append(instructions, []any{[]any{text}})
		}
		// Question 依次为说明、noul、choice 与 score，只写出问题类型对应的一项
		wire := []any{instructions, nil, nil, nil}
		switch question.Type {
		case "noul":
			noul := []any{}
			if question.HasCriteria {
				noul = []any{[]any{question.TrueDescription, question.FalseDescription}}
			}
			wire[1] = noul
		case "choice":
			options := make([]any, 0, len(question.Options))
			for _, option := range question.Options {
				options = append(options, []any{option.ID, option.Description})
			}
			wire[2] = []any{options}
		case "score":
			wire[3] = []any{question.Levels}
		default:
			return nil, fmt.Errorf("questions.%s.type 必须是 noul、choice 或 score", question.Key)
		}
		questions = append(questions, []any{question.Key, trimWireNil(wire)})
	}
	return json.Marshal([]any{"models/" + request.Model, input, questions, nil, nil})
}

// decisionContent 编码一个输入块：field 1 文本，field 2 至 5 依次为图片、音频、文档与视频的 MIME 枚举、Base64 数据或 URI 与分辨率
func decisionContent(part Part) ([]any, error) {
	if part.ExternalMedia != nil {
		return nil, fmt.Errorf("Decisions API 不接受 YouTube 链接")
	}
	if part.InlineData == nil && part.File == nil {
		return []any{[]any{part.Text}}, nil
	}
	mime, data, uri := "", "", ""
	if part.InlineData != nil {
		mime, data = part.InlineData.MIME, base64.StdEncoding.EncodeToString(part.InlineData.Data)
	} else {
		mime, uri = part.File.MIME, part.File.ID
	}
	field, code, ok := interactionMediaCode(mime)
	if !ok {
		return nil, fmt.Errorf("不支持的 MIME 类型 %q", mime)
	}
	media := []any{code, emptyNil(data), emptyNil(uri), nil}
	if part.MediaResolution != "" {
		resolution, ok := mediaResolutions[part.MediaResolution]
		if !ok {
			return nil, fmt.Errorf("不支持的分辨率 %q", part.MediaResolution)
		}
		media[3] = resolution
	}
	content := make([]any, field+1)
	content[field] = trimWireNil(media)
	return content, nil
}

// interactionMediaCode 返回 MIME 所属媒体在 Content 中的数组下标与 MIME 枚举
func interactionMediaCode(mime string) (int, int64, bool) {
	base, _, _ := strings.Cut(strings.ToLower(strings.TrimSpace(mime)), ";")
	for field, table := range interactionMediaMIME {
		for code, value := range table.codes {
			if value == base {
				return field, code, true
			}
		}
	}
	return 0, 0, false
}

// DecodeCreateDecisionResponse 解码 CreateDecision 响应：field 1 为问题键与回答，field 2 为用量（输入 token、各模态 token 与 field 10 的总 token），输入被拦截时 field 3 为拦截原因与说明，返回 PromptFeedbackError
func DecodeCreateDecisionResponse(body io.Reader) (DecisionResult, error) {
	raw, err := io.ReadAll(body)
	if err != nil {
		return DecisionResult{}, fmt.Errorf("读取 AI Studio CreateDecision 响应: %w", err)
	}
	fail := func(path string, detail string) error {
		return &ProtocolEvidenceError{Method: "CreateDecision", Path: path, Detail: detail, Raw: raw}
	}
	var wire struct {
		Answers [][]json.RawMessage
		Usage   []json.RawMessage
	}
	var root []json.RawMessage
	if json.Unmarshal(raw, &root) != nil || len(root) == 0 || json.Unmarshal(root[0], &wire.Answers) != nil {
		return DecisionResult{}, fail("$[0]", "期望回答数组")
	}
	if len(root) > 1 && json.Unmarshal(root[1], &wire.Usage) != nil {
		return DecisionResult{}, fail("$[1]", "期望用量数组")
	}
	if len(root) > 2 && !isJSONNull(root[2]) {
		var reason int64
		var message string
		if json.Unmarshal(root[2], &[]any{&reason, &message}) != nil {
			return DecisionResult{}, fail("$[2]", "期望拦截原因与说明")
		}
		return DecisionResult{}, &PromptFeedbackError{Reason: decodePromptBlockReason(reason), Message: message, Raw: raw}
	}
	result := DecisionResult{Answers: make([]DecisionAnswer, 0, len(wire.Answers))}
	for index, pair := range wire.Answers {
		var key string
		var fields []json.RawMessage
		if len(pair) != 2 || json.Unmarshal(pair[0], &key) != nil || json.Unmarshal(pair[1], &fields) != nil {
			return DecisionResult{}, fail(fmt.Sprintf("$[0][%d]", index), "期望问题键与回答")
		}
		answer, err := decodeDecisionAnswer(key, fields)
		if err != nil {
			return DecisionResult{}, fail(fmt.Sprintf("$[0][%d][1]", index), err.Error())
		}
		result.Answers = append(result.Answers, answer)
	}
	var modalities [][2]int64
	for index, target := range map[int]any{0: &result.InputTokens, 1: &modalities, 9: &result.TotalTokens} {
		if len(wire.Usage) > index && !isJSONNull(wire.Usage[index]) && json.Unmarshal(wire.Usage[index], target) != nil {
			return DecisionResult{}, fail(fmt.Sprintf("$[1][%d]", index), "用量无效")
		}
	}
	for _, item := range modalities {
		result.InputByModality = append(result.InputByModality, DecisionModalityTokens{Modality: decisionModalities[item[0]], Tokens: item[1]})
	}
	return result, nil
}

// decodeDecisionAnswer 解码一个回答：field 1 为置信度；field 4 为 noul，第 2 项为 P(true)；field 5 为 choice，第 1、2 项为选项与各选项概率；field 6 为 score，第 1、4、5 项为期望等级、各等级概率与各等级说明
func decodeDecisionAnswer(key string, fields []json.RawMessage) (DecisionAnswer, error) {
	answer := DecisionAnswer{Key: key}
	if len(fields) == 0 || json.Unmarshal(fields[0], &answer.Confidence) != nil {
		return answer, fmt.Errorf("回答缺少置信度")
	}
	field := func(index int) []json.RawMessage {
		var items []json.RawMessage
		if len(fields) > index {
			_ = json.Unmarshal(fields[index], &items)
		}
		return items
	}
	switch noul, choice, score := field(3), field(4), field(5); {
	case len(noul) > 1:
		answer.Type = "noul"
		if json.Unmarshal(noul[1], &answer.Noul) != nil {
			return answer, fmt.Errorf("noul 概率无效")
		}
	case len(choice) > 1:
		answer.Type = "choice"
		var probabilities [][2]json.RawMessage
		if json.Unmarshal(choice[0], &answer.Choice) != nil || json.Unmarshal(choice[1], &probabilities) != nil {
			return answer, fmt.Errorf("choice 回答无效")
		}
		for _, pair := range probabilities {
			item := DecisionProbability{}
			if json.Unmarshal(pair[0], &item.Key) != nil || json.Unmarshal(pair[1], &item.Value) != nil {
				return answer, fmt.Errorf("choice 概率无效")
			}
			answer.Probabilities = append(answer.Probabilities, item)
		}
	case len(score) > 4:
		answer.Type = "score"
		var probabilities, legend [][2]json.RawMessage
		if json.Unmarshal(score[0], &answer.Score) != nil || json.Unmarshal(score[3], &probabilities) != nil || json.Unmarshal(score[4], &legend) != nil {
			return answer, fmt.Errorf("score 回答无效")
		}
		for _, pair := range probabilities {
			item := DecisionProbability{Key: string(pair[0])}
			if json.Unmarshal(pair[1], &item.Value) != nil {
				return answer, fmt.Errorf("score 概率无效")
			}
			answer.Probabilities = append(answer.Probabilities, item)
		}
		for _, pair := range legend {
			item := DecisionProbability{Key: string(pair[0])}
			if json.Unmarshal(pair[1], &item.Description) != nil {
				return answer, fmt.Errorf("score 等级说明无效")
			}
			answer.Legend = append(answer.Legend, item)
		}
	default:
		return answer, fmt.Errorf("回答没有 noul、choice 或 score")
	}
	return answer, nil
}
