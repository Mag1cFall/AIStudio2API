package api

import (
	"bytes"
	"cmp"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"maps"
	"net/http"
	"slices"
	"strconv"

	"github.com/Mag1cFall/AIStudio2API/internal/aistudio"
)

// decisionRequest 是 Decisions API 请求：model、各问题共用的 input 与按问题 ID 给出的问题
type decisionRequest struct {
	Model     string                      `json:"model"`
	Input     json.RawMessage             `json:"input"`
	Questions map[string]decisionQuestion `json:"questions"`
}

// decisionQuestion 是一个决策问题，Criteria 在 noul 时为真假说明、choice 时为选项 ID 到说明、score 时为从低到高的等级说明
type decisionQuestion struct {
	Type         string          `json:"type"`
	Instructions json.RawMessage `json:"instructions"`
	Criteria     json.RawMessage `json:"criteria"`
}

// decisionAnswer 是一个问题的回答，字段顺序同官方 Decisions API
type decisionAnswer struct {
	Type          string        `json:"type"`
	Noul          *float64      `json:"noul,omitempty"`
	Choice        *string       `json:"choice,omitempty"`
	Score         *float64      `json:"score,omitempty"`
	Legend        orderedFields `json:"legend,omitempty"`
	Probabilities orderedFields `json:"probabilities,omitempty"`
	Confidence    float64       `json:"confidence"`
}

// decisionUsage 是 Decisions API 的 token 用量，输出始终为 0
type decisionUsage struct {
	TotalInputTokens      int64            `json:"total_input_tokens"`
	InputTokensByModality []map[string]any `json:"input_tokens_by_modality,omitempty"`
	TotalOutputTokens     int64            `json:"total_output_tokens"`
	TotalTokens           int64            `json:"total_tokens"`
}

// orderedFields 是按切片顺序编码的 JSON 对象成员
type orderedFields []orderedField

// orderedField 是 JSON 对象的一个成员
type orderedField struct {
	key   string
	value any
}

// MarshalJSON 按切片顺序写出对象成员
func (fields orderedFields) MarshalJSON() ([]byte, error) {
	var buffer bytes.Buffer
	buffer.WriteByte('{')
	for index, field := range fields {
		if index > 0 {
			buffer.WriteByte(',')
		}
		key, _ := json.Marshal(field.key)
		value, err := json.Marshal(field.value)
		if err != nil {
			return nil, err
		}
		buffer.Write(key)
		buffer.WriteByte(':')
		buffer.Write(value)
	}
	buffer.WriteByte('}')
	return buffer.Bytes(), nil
}

// handleDecision 以 Gemini Decision 模型回答 POST /v1beta/decisions 的决策问题
func (s *server) handleDecision(w http.ResponseWriter, r *http.Request) {
	service, ok := s.service.(aistudio.DecisionService)
	if !ok {
		writeGeminiError(w, http.StatusBadRequest, "INVALID_ARGUMENT", "decisions are unavailable")
		return
	}
	var request decisionRequest
	if err := decodeJSON(r, &request); err != nil {
		writeGeminiError(w, http.StatusBadRequest, "INVALID_ARGUMENT", err.Error())
		return
	}
	decision, err := request.decision()
	if err == nil {
		err = s.inlineDecisionFiles(r, decision.Input)
	}
	if err != nil {
		writeGeminiError(w, http.StatusBadRequest, "INVALID_ARGUMENT", err.Error())
		return
	}
	result, err := service.CreateDecision(r.Context(), decision)
	var blocked *aistudio.PromptFeedbackError
	if errors.As(err, &blocked) {
		writeDecisionBlocked(w, blocked)
		return
	}
	if err != nil {
		if shouldWriteRequestError(r, err) {
			writeGeminiRequestError(w, err)
		}
		return
	}
	SetAccessLogGenerationResult(r.Context(), &aistudio.Usage{InputTokens: result.InputTokens, TotalTokens: result.TotalTokens}, 0)
	answers := make(orderedFields, 0, len(result.Answers))
	for _, answer := range result.Answers {
		answers = append(answers, orderedField{key: answer.Key, value: decisionAnswerJSON(answer)})
	}
	usage := decisionUsage{TotalInputTokens: result.InputTokens, TotalTokens: result.TotalTokens}
	for _, item := range result.InputByModality {
		usage.InputTokensByModality = append(usage.InputTokensByModality, map[string]any{"modality": item.Modality, "tokens": item.Tokens})
	}
	writeJSON(w, http.StatusOK, struct {
		Answers orderedFields `json:"answers"`
		Usage   decisionUsage `json:"usage"`
	}{answers, usage})
}

// decisionBlockCodes 是官方 Decisions API 输入被拦截时错误 code 直接使用的拦截原因，其余原因为 content_blocked
var decisionBlockCodes = map[string]bool{"safety": true, "prohibited_content": true, "blocklist": true, "image_safety": true}

// writeDecisionBlocked 按官方 Decisions API 以 400 与拦截类别 code 返回输入被拦截的错误
func writeDecisionBlocked(w http.ResponseWriter, blocked *aistudio.PromptFeedbackError) {
	code := blocked.Reason
	if !decisionBlockCodes[code] {
		code = "content_blocked"
	}
	type blockError struct {
		Code    string `json:"code"`
		Message string `json:"message"`
	}
	writeJSON(w, http.StatusBadRequest, struct {
		Error blockError `json:"error"`
	}{blockError{code, blocked.Message}})
}

// decision 把请求转换为上游决策请求，问题按 ID 排序
func (request decisionRequest) decision() (aistudio.DecisionRequest, error) {
	input, err := decisionInput(request.Input)
	if err != nil {
		return aistudio.DecisionRequest{}, fmt.Errorf("input: %w", err)
	}
	if len(request.Questions) == 0 {
		return aistudio.DecisionRequest{}, fmt.Errorf("questions must contain at least one question")
	}
	decision := aistudio.DecisionRequest{Model: request.Model, Input: input}
	for _, key := range slices.Sorted(maps.Keys(request.Questions)) {
		question, err := request.Questions[key].decision(key)
		if err != nil {
			return aistudio.DecisionRequest{}, fmt.Errorf("questions.%s: %w", key, err)
		}
		decision.Questions = append(decision.Questions, question)
	}
	return decision, nil
}

// decision 转换一个问题的说明与 criteria
func (question decisionQuestion) decision(key string) (aistudio.DecisionQuestion, error) {
	result := aistudio.DecisionQuestion{Key: key, Type: question.Type}
	instructions, err := decisionContents(question.Instructions)
	if err != nil {
		return result, fmt.Errorf("instructions: %w", err)
	}
	for _, content := range instructions {
		if content.Type != "text" || content.Text == nil {
			return result, fmt.Errorf("instructions only accept text content")
		}
		result.Instructions = append(result.Instructions, *content.Text)
	}
	if len(result.Instructions) == 0 {
		return result, fmt.Errorf("instructions are required")
	}
	switch question.Type {
	case "noul":
		if rawJSONConfigured(question.Criteria) {
			var criteria struct {
				TrueDescription  string `json:"true_description"`
				FalseDescription string `json:"false_description"`
			}
			if err := json.Unmarshal(question.Criteria, &criteria); err != nil {
				return result, fmt.Errorf("criteria: %w", err)
			}
			result.HasCriteria, result.TrueDescription, result.FalseDescription = true, criteria.TrueDescription, criteria.FalseDescription
		}
	case "choice":
		var options map[string]string
		if err := json.Unmarshal(question.Criteria, &options); err != nil {
			return result, fmt.Errorf("criteria: %w", err)
		}
		for _, id := range slices.Sorted(maps.Keys(options)) {
			result.Options = append(result.Options, aistudio.DecisionOption{ID: id, Description: options[id]})
		}
	case "score":
		if err := json.Unmarshal(question.Criteria, &result.Levels); err != nil {
			return result, fmt.Errorf("criteria: %w", err)
		}
	default:
		return result, fmt.Errorf("type must be noul, choice or score")
	}
	return result, nil
}

// decisionContents 解析字符串、单个内容块或内容块数组
func decisionContents(raw json.RawMessage) ([]interactionInput, error) {
	var text string
	if json.Unmarshal(raw, &text) == nil {
		return []interactionInput{{Type: "text", Text: &text}}, nil
	}
	return interactionList[interactionInput](raw)
}

// decisionInput 把 input 的文本、图片、音频、文档与视频内容块转换为 part
func decisionInput(raw json.RawMessage) ([]aistudio.Part, error) {
	contents, err := decisionContents(raw)
	if err != nil {
		return nil, err
	}
	var parts []aistudio.Part
	for _, content := range contents {
		converted, err := content.parts()
		if err != nil {
			return nil, err
		}
		parts = append(parts, converted...)
	}
	if len(parts) == 0 {
		return nil, fmt.Errorf("at least one content block is required")
	}
	return parts, nil
}

// inlineDecisionFiles 把经本服务 Files API 上传的文件下载为内联数据，http 与 https 链接原样交给上游
func (s *server) inlineDecisionFiles(r *http.Request, parts []aistudio.Part) error {
	for index := range parts {
		part := &parts[index]
		if part.File == nil || remoteMediaURL(part.File.ID) {
			continue
		}
		service, ok := s.service.(aistudio.FileService)
		if !ok {
			return fmt.Errorf("file content is unavailable")
		}
		media, err := service.DownloadFile(r.Context(), part.File.ID)
		if err != nil {
			return err
		}
		data, err := io.ReadAll(media.Body)
		closeErr := media.Body.Close()
		if err = cmp.Or(err, closeErr); err != nil {
			return err
		}
		part.InlineData = &aistudio.Blob{MIME: cmp.Or(part.File.MIME, media.MIME), Data: data}
		part.File = nil
	}
	return nil
}

// decisionAnswerJSON 按官方形状写出回答：choice 概率从高到低，score 概率与等级说明按等级序号
func decisionAnswerJSON(answer aistudio.DecisionAnswer) decisionAnswer {
	result := decisionAnswer{Type: answer.Type, Confidence: answer.Confidence}
	probabilities := slices.Clone(answer.Probabilities)
	switch answer.Type {
	case "noul":
		result.Noul = &answer.Noul
	case "choice":
		result.Choice = &answer.Choice
		slices.SortStableFunc(probabilities, func(a, b aistudio.DecisionProbability) int { return cmp.Compare(b.Value, a.Value) })
	case "score":
		result.Score = &answer.Score
		legend := slices.Clone(answer.Legend)
		slices.SortFunc(legend, compareDecisionLevel)
		slices.SortFunc(probabilities, compareDecisionLevel)
		for _, item := range legend {
			result.Legend = append(result.Legend, orderedField{key: item.Key, value: item.Description})
		}
	}
	for _, item := range probabilities {
		result.Probabilities = append(result.Probabilities, orderedField{key: item.Key, value: item.Value})
	}
	return result
}

// compareDecisionLevel 按等级序号比较 score 的概率或等级说明
func compareDecisionLevel(a, b aistudio.DecisionProbability) int {
	left, _ := strconv.Atoi(a.Key)
	right, _ := strconv.Atoi(b.Key)
	return cmp.Compare(left, right)
}
