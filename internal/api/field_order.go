package api

import (
	"bytes"
	_ "embed"
	"encoding/json"
	"maps"
	"slices"
	"strings"
)

// fieldOrderJSON 是各公开协议输出对象的官方字段顺序表
//
//go:embed field_order.json
var fieldOrderJSON []byte

// fieldOrder 是一个公开协议输出对象的官方字段顺序：Global 为全协议统一的先后，Kinds 按对象种类给出先后，TypeFirst 为 type 在前、其余按字母序
type fieldOrder struct {
	Global    []string            `json:"global"`
	Kinds     map[string][]string `json:"kinds"`
	TypeFirst bool                `json:"typeFirst"`
	Opaque    []string            `json:"opaque"`
	global    map[string]int
	kinds     map[string]map[string]int
	protocol  string
}

// fieldOrders 是 gemini、chat、responses、anthropic 与 interactions 输出的字段顺序
var fieldOrders = loadFieldOrders()

// loadFieldOrders 解析内嵌的字段顺序表
func loadFieldOrders() map[string]*fieldOrder {
	var orders map[string]*fieldOrder
	if err := json.Unmarshal(fieldOrderJSON, &orders); err != nil {
		panic(err)
	}
	for protocol, order := range orders {
		order.protocol = protocol
		order.global = fieldRanks(order.Global)
		order.kinds = make(map[string]map[string]int, len(order.Kinds))
		for kind, keys := range order.Kinds {
			order.kinds[kind] = fieldRanks(keys)
		}
	}
	return orders
}

// fieldRanks 返回字段名到先后位置的映射
func fieldRanks(keys []string) map[string]int {
	if len(keys) == 0 {
		return nil
	}
	ranks := make(map[string]int, len(keys))
	for index, key := range keys {
		ranks[key] = index
	}
	return ranks
}

// anthropicStartUsage 是 message_start 的用量字段
type anthropicStartUsage struct {
	InputTokens              int64 `json:"input_tokens"`
	CacheCreationInputTokens int64 `json:"cache_creation_input_tokens"`
	CacheReadInputTokens     int64 `json:"cache_read_input_tokens"`
	OutputTokens             int64 `json:"output_tokens"`
}

// officialOrder 将响应投影为固定字段结构与动态字段对象
func officialOrder(protocol string, value any) any {
	return fieldOrders[protocol].value(value, "")
}

// orderedJSON 是按官方字段顺序编码的动态对象
type orderedJSON struct {
	fields map[string]any
	order  *fieldOrder
	parent string
}

// value 为动态字段准备嵌套对象，其余值交给 encoding/json
func (order *fieldOrder) value(value any, parent string) any {
	if slices.Contains(order.Opaque, parent) {
		return value
	}
	switch value := value.(type) {
	case nil:
		return json.RawMessage("null")
	case map[string]any:
		if value == nil {
			return value
		}
		fields := make(map[string]any, len(value))
		for key, item := range value {
			fields[key] = order.value(item, key)
		}
		return order.object(fields, parent)
	case map[string]string:
		if value == nil {
			return value
		}
		fields := make(map[string]any, len(value))
		for key, item := range value {
			fields[key] = item
		}
		return order.object(fields, parent)
	case map[string]json.RawMessage:
		if value == nil {
			return value
		}
		fields := make(map[string]any, len(value))
		for key, item := range value {
			fields[key] = item
		}
		return order.object(fields, parent)
	case []map[string]any:
		if value == nil {
			return value
		}
		items := make([]any, len(value))
		for index, item := range value {
			items[index] = order.value(item, parent)
		}
		return items
	case []any:
		if value == nil {
			return value
		}
		items := make([]any, len(value))
		for index, item := range value {
			items[index] = order.value(item, parent)
		}
		return items
	}
	return value
}

// MarshalJSON 只排列当前动态对象的键，字段值由 encoding/json 编码
func (o orderedJSON) MarshalJSON() ([]byte, error) {
	keys := slices.Collect(maps.Keys(o.fields))
	ranks := o.order.ranksFor(o.fields, o.parent)
	slices.SortFunc(keys, func(a, b string) int { return o.order.compare(ranks, a, b) })
	var buffer bytes.Buffer
	buffer.WriteByte('{')
	for index, key := range keys {
		if index > 0 {
			buffer.WriteByte(',')
		}
		name, _ := json.Marshal(key)
		item, err := json.Marshal(o.fields[key])
		if err != nil {
			return nil, err
		}
		buffer.Write(name)
		buffer.WriteByte(':')
		buffer.Write(item)
	}
	buffer.WriteByte('}')
	return buffer.Bytes(), nil
}

// object 返回固定响应的具名结构体或动态字段对象
func (order *fieldOrder) object(fields map[string]any, parent string) any {
	switch order.protocol {
	case "chat":
		if mapString(fields, "object") == "chat.completion" {
			return chatCompletionJSON{
				ID:                fields["id"],
				Object:            fields["object"],
				Created:           fields["created"],
				Model:             fields["model"],
				Choices:           fields["choices"],
				Usage:             fields["usage"],
				ServiceTier:       fields["service_tier"],
				ProviderModel:     fields["provider_model"],
				SystemFingerprint: fields["system_fingerprint"],
			}
		}
		if mapString(fields, "object") == "chat.completion.chunk" {
			return chatChunkJSON{
				ID:                fields["id"],
				Object:            fields["object"],
				Created:           fields["created"],
				Model:             fields["model"],
				SystemFingerprint: fields["system_fingerprint"],
				Choices:           fields["choices"],
				ProviderModel:     fields["provider_model"],
				Usage:             fields["usage"],
			}
		}
	case "gemini":
		if fields["modelVersion"] != nil || fields["candidates"] != nil || fields["promptFeedback"] != nil {
			return geminiGenerationJSON{
				Candidates:     fields["candidates"],
				PromptFeedback: fields["promptFeedback"],
				UsageMetadata:  fields["usageMetadata"],
				ModelVersion:   fields["modelVersion"],
				ResponseID:     fields["responseId"],
				TurnToken:      fields["turnToken"],
				DebugInfo:      fields["debugInfo"],
			}
		}
		if parent == "candidates" {
			return geminiCandidateJSON{
				Content:              fields["content"],
				FinishReason:         fields["finishReason"],
				Index:                fields["index"],
				FinishMessage:        fields["finishMessage"],
				SafetyRatings:        fields["safetyRatings"],
				CitationMetadata:     fields["citationMetadata"],
				GroundingMetadata:    fields["groundingMetadata"],
				DetailedFinishReason: fields["detailedFinishReason"],
				URLContextMetadata:   fields["urlContextMetadata"],
			}
		}
	case "anthropic":
		if mapString(fields, "type") == "message" && mapString(fields, "role") == "assistant" {
			return anthropicMessageJSON{
				ID:                   fields["id"],
				Type:                 fields["type"],
				Role:                 fields["role"],
				Content:              fields["content"],
				Model:                fields["model"],
				StopReason:           fields["stop_reason"],
				StopSequence:         fields["stop_sequence"],
				Usage:                fields["usage"],
				ContextManagement:    fields["context_management"],
				ProviderFinishReason: fields["provider_finish_reason"],
				ProviderModel:        fields["provider_model"],
			}
		}
	case "responses":
		if mapString(fields, "object") == "response" {
			return responsesJSON{
				ID:                   fields["id"],
				Object:               fields["object"],
				CreatedAt:            fields["created_at"],
				Status:               fields["status"],
				CompletedAt:          fields["completed_at"],
				Background:           fields["background"],
				Error:                fields["error"],
				IncompleteDetails:    fields["incomplete_details"],
				Instructions:         fields["instructions"],
				MaxOutputTokens:      fields["max_output_tokens"],
				Model:                fields["model"],
				Output:               fields["output"],
				ParallelToolCalls:    fields["parallel_tool_calls"],
				PreviousResponseID:   fields["previous_response_id"],
				Reasoning:            fields["reasoning"],
				Temperature:          fields["temperature"],
				Text:                 fields["text"],
				ToolChoice:           fields["tool_choice"],
				Tools:                fields["tools"],
				TopP:                 fields["top_p"],
				Truncation:           fields["truncation"],
				Usage:                fields["usage"],
				Metadata:             fields["metadata"],
				OutputText:           fields["output_text"],
				ProviderFinishReason: fields["provider_finish_reason"],
				ProviderModel:        fields["provider_model"],
			}
		}
	case "interactions":
		if mapString(fields, "object") == "interaction" {
			return interactionJSON{
				Created: fields["created"],
				ID:      fields["id"],
				Model:   fields["model"],
				Object:  fields["object"],
				Status:  fields["status"],
				Steps:   fields["steps"],
				Updated: fields["updated"],
				Usage:   fields["usage"],
			}
		}
	case "openai":
		if mapString(fields, "object") == "model" {
			return openAIModelJSON{
				ID:                         fields["id"],
				Object:                     fields["object"],
				Created:                    fields["created"],
				OwnedBy:                    fields["owned_by"],
				ShutdownDate:               fields["shutdown_date"],
				AccessModes:                fields["access_modes"],
				Capabilities:               fields["capabilities"],
				CapabilityOptions:          fields["capability_options"],
				Channels:                   fields["channels"],
				DefaultThinkingLevel:       fields["default_thinking_level"],
				Description:                fields["description"],
				InputTokenLimit:            fields["input_token_limit"],
				Name:                       fields["name"],
				OutputTokenLimit:           fields["output_token_limit"],
				Paid:                       fields["paid"],
				SupportedGenerationMethods: fields["supported_generation_methods"],
				ThinkingLevels:             fields["thinking_levels"],
			}
		}
	}
	return orderedJSON{fields: fields, order: order, parent: parent}
}

// chatCompletionJSON 是 Chat 非流式响应
type chatCompletionJSON struct {
	ID                any `json:"id,omitempty"`
	Object            any `json:"object,omitempty"`
	Created           any `json:"created,omitempty"`
	Model             any `json:"model,omitempty"`
	Choices           any `json:"choices,omitempty"`
	Usage             any `json:"usage,omitempty"`
	ServiceTier       any `json:"service_tier,omitempty"`
	ProviderModel     any `json:"provider_model,omitempty"`
	SystemFingerprint any `json:"system_fingerprint,omitempty"`
}

// chatChunkJSON 是 Chat 流式响应块
type chatChunkJSON struct {
	ID                any `json:"id,omitempty"`
	Object            any `json:"object,omitempty"`
	Created           any `json:"created,omitempty"`
	Model             any `json:"model,omitempty"`
	SystemFingerprint any `json:"system_fingerprint,omitempty"`
	Choices           any `json:"choices,omitempty"`
	ProviderModel     any `json:"provider_model,omitempty"`
	Usage             any `json:"usage,omitempty"`
}

// geminiGenerationJSON 是 Gemini 生成响应
type geminiGenerationJSON struct {
	Candidates     any `json:"candidates,omitempty"`
	PromptFeedback any `json:"promptFeedback,omitempty"`
	UsageMetadata  any `json:"usageMetadata,omitempty"`
	ModelVersion   any `json:"modelVersion,omitempty"`
	ResponseID     any `json:"responseId,omitempty"`
	TurnToken      any `json:"turnToken,omitempty"`
	DebugInfo      any `json:"debugInfo,omitempty"`
}

// geminiCandidateJSON 是 Gemini 候选响应
type geminiCandidateJSON struct {
	Content              any `json:"content,omitempty"`
	FinishReason         any `json:"finishReason,omitempty"`
	Index                any `json:"index,omitempty"`
	FinishMessage        any `json:"finishMessage,omitempty"`
	SafetyRatings        any `json:"safetyRatings,omitempty"`
	CitationMetadata     any `json:"citationMetadata,omitempty"`
	GroundingMetadata    any `json:"groundingMetadata,omitempty"`
	DetailedFinishReason any `json:"detailedFinishReason,omitempty"`
	URLContextMetadata   any `json:"urlContextMetadata,omitempty"`
}

// anthropicMessageJSON 是 Anthropic Message 响应
type anthropicMessageJSON struct {
	ID                   any `json:"id,omitempty"`
	Type                 any `json:"type,omitempty"`
	Role                 any `json:"role,omitempty"`
	Content              any `json:"content,omitempty"`
	Model                any `json:"model,omitempty"`
	StopReason           any `json:"stop_reason,omitempty"`
	StopSequence         any `json:"stop_sequence,omitempty"`
	Usage                any `json:"usage,omitempty"`
	ContextManagement    any `json:"context_management,omitempty"`
	ProviderFinishReason any `json:"provider_finish_reason,omitempty"`
	ProviderModel        any `json:"provider_model,omitempty"`
}

// responsesJSON 是 Responses 响应资源
type responsesJSON struct {
	ID                   any `json:"id,omitempty"`
	Object               any `json:"object,omitempty"`
	CreatedAt            any `json:"created_at,omitempty"`
	Status               any `json:"status,omitempty"`
	CompletedAt          any `json:"completed_at,omitempty"`
	Background           any `json:"background,omitempty"`
	Error                any `json:"error,omitempty"`
	IncompleteDetails    any `json:"incomplete_details,omitempty"`
	Instructions         any `json:"instructions,omitempty"`
	MaxOutputTokens      any `json:"max_output_tokens,omitempty"`
	Model                any `json:"model,omitempty"`
	Output               any `json:"output,omitempty"`
	ParallelToolCalls    any `json:"parallel_tool_calls,omitempty"`
	PreviousResponseID   any `json:"previous_response_id,omitempty"`
	Reasoning            any `json:"reasoning,omitempty"`
	Temperature          any `json:"temperature,omitempty"`
	Text                 any `json:"text,omitempty"`
	ToolChoice           any `json:"tool_choice,omitempty"`
	Tools                any `json:"tools,omitempty"`
	TopP                 any `json:"top_p,omitempty"`
	Truncation           any `json:"truncation,omitempty"`
	Usage                any `json:"usage,omitempty"`
	Metadata             any `json:"metadata,omitempty"`
	OutputText           any `json:"output_text,omitempty"`
	ProviderFinishReason any `json:"provider_finish_reason,omitempty"`
	ProviderModel        any `json:"provider_model,omitempty"`
}

// interactionJSON 是 Interactions 响应资源
type interactionJSON struct {
	Created any `json:"created,omitempty"`
	ID      any `json:"id,omitempty"`
	Model   any `json:"model,omitempty"`
	Object  any `json:"object,omitempty"`
	Status  any `json:"status,omitempty"`
	Steps   any `json:"steps,omitempty"`
	Updated any `json:"updated,omitempty"`
	Usage   any `json:"usage,omitempty"`
}

// openAIModelJSON 是 OpenAI 模型资源
type openAIModelJSON struct {
	ID                         any `json:"id,omitempty"`
	Object                     any `json:"object,omitempty"`
	Created                    any `json:"created,omitempty"`
	OwnedBy                    any `json:"owned_by,omitempty"`
	ShutdownDate               any `json:"shutdown_date,omitempty"`
	AccessModes                any `json:"access_modes,omitempty"`
	Capabilities               any `json:"capabilities,omitempty"`
	CapabilityOptions          any `json:"capability_options,omitempty"`
	Channels                   any `json:"channels,omitempty"`
	DefaultThinkingLevel       any `json:"default_thinking_level,omitempty"`
	Description                any `json:"description,omitempty"`
	InputTokenLimit            any `json:"input_token_limit,omitempty"`
	Name                       any `json:"name,omitempty"`
	OutputTokenLimit           any `json:"output_token_limit,omitempty"`
	Paid                       any `json:"paid,omitempty"`
	SupportedGenerationMethods any `json:"supported_generation_methods,omitempty"`
	ThinkingLevels             any `json:"thinking_levels,omitempty"`
}

// ranksFor 返回对象所属种类的字段先后：Global 优先，否则按 type、object 或所在字段名查 Kinds
func (order *fieldOrder) ranksFor(object map[string]any, parent string) map[string]int {
	if order.global != nil {
		return order.global
	}
	if order.TypeFirst {
		return nil
	}
	for _, field := range []string{"type", "object"} {
		if name := mapString(object, field); name != "" {
			return order.kinds[field+":"+name]
		}
	}
	return order.kinds["key:"+parent]
}

// compare 比较同一对象中的两个字段：表中的字段按表的先后在前，其余按字母序
func (order *fieldOrder) compare(ranks map[string]int, a, b string) int {
	if order.TypeFirst && (a == "type") != (b == "type") {
		if a == "type" {
			return -1
		}
		return 1
	}
	left, leftKnown := ranks[a]
	right, rightKnown := ranks[b]
	switch {
	case leftKnown && rightKnown:
		return left - right
	case leftKnown:
		return -1
	case rightKnown:
		return 1
	}
	return strings.Compare(a, b)
}

// mapString 读取对象中字符串字段的值，原始 JSON 字段按 JSON 字符串解析
func mapString(object map[string]any, field string) string {
	switch value := object[field].(type) {
	case string:
		return value
	case json.RawMessage:
		var name string
		_ = json.Unmarshal(value, &name)
		return name
	}
	return ""
}
