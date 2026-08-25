package aistudio

import (
	"context"
	"encoding/json"
)

// Role 表示规范消息角色
type Role string

const (
	// RoleUser 表示用户消息
	RoleUser Role = "user"
	// RoleAssistant 表示模型消息
	RoleAssistant Role = "assistant"
	// RoleTool 表示工具结果消息
	RoleTool Role = "tool"
)

// Blob 表示内联二进制内容
type Blob struct {
	MIME string `json:"mime"`
	Data []byte `json:"data"`
}

// FileRef 表示已上传文件引用
type FileRef struct {
	ID   string `json:"id"`
	Name string `json:"name,omitempty"`
	MIME string `json:"mime,omitempty"`
}

// FunctionCall 表示模型发起的函数调用
type FunctionCall struct {
	ID               string          `json:"id"`
	Name             string          `json:"name"`
	Arguments        json.RawMessage `json:"arguments"`
	ThoughtSignature string          `json:"thought_signature,omitempty"`
}

// FunctionResult 表示客户端返回的函数结果
type FunctionResult struct {
	ID      string          `json:"id"`
	Name    string          `json:"name,omitempty"`
	Content json.RawMessage `json:"content"`
}

// ExecutableCode 表示 AI Studio 内置代码执行器生成的代码
type ExecutableCode struct {
	Language string `json:"language"`
	Code     string `json:"code"`
}

// CodeExecutionResult 表示 AI Studio 内置代码执行器返回的结果
type CodeExecutionResult struct {
	Outcome string `json:"outcome"`
	Output  string `json:"output,omitempty"`
}

// SearchEntryPoint 表示 Google Search 返回的搜索入口
type SearchEntryPoint struct {
	RenderedContent string `json:"rendered_content,omitempty"`
	SDKBlob         string `json:"sdk_blob,omitempty"`
}

// GroundingChunk 表示 Google 工具检索到的一个来源
type GroundingChunk struct {
	Source  string `json:"source"`
	URI     string `json:"uri,omitempty"`
	Title   string `json:"title,omitempty"`
	Text    string `json:"text,omitempty"`
	PlaceID string `json:"place_id,omitempty"`
}

// GroundingSegment 表示正文中由来源支撑的片段
type GroundingSegment struct {
	PartIndex  int    `json:"part_index,omitempty"`
	StartIndex int    `json:"start_index,omitempty"`
	EndIndex   int    `json:"end_index,omitempty"`
	Text       string `json:"text,omitempty"`
}

// GroundingSupport 表示正文片段与来源下标的关联
type GroundingSupport struct {
	Segment          GroundingSegment `json:"segment"`
	ChunkIndices     []int            `json:"chunk_indices,omitempty"`
	ConfidenceScores []float64        `json:"confidence_scores,omitempty"`
}

// GroundingMetadata 表示 Google Search、URL Context 和 Maps 的来源信息
type GroundingMetadata struct {
	SearchEntryPoint       *SearchEntryPoint  `json:"search_entry_point,omitempty"`
	Chunks                 []GroundingChunk   `json:"chunks,omitempty"`
	Supports               []GroundingSupport `json:"supports,omitempty"`
	DynamicRetrievalScore  *float64           `json:"dynamic_retrieval_score,omitempty"`
	WebSearchQueries       []string           `json:"web_search_queries,omitempty"`
	MapsWidgetContextToken string             `json:"maps_widget_context_token,omitempty"`
}

// Part 表示规范消息中的一个内容块
type Part struct {
	Text                string               `json:"text,omitempty"`
	InlineData          *Blob                `json:"inline_data,omitempty"`
	File                *FileRef             `json:"file,omitempty"`
	FunctionCall        *FunctionCall        `json:"function_call,omitempty"`
	FunctionResult      *FunctionResult      `json:"function_result,omitempty"`
	ExecutableCode      *ExecutableCode      `json:"executable_code,omitempty"`
	CodeExecutionResult *CodeExecutionResult `json:"code_execution_result,omitempty"`
	ThoughtSignature    string               `json:"thought_signature,omitempty"`
}

// Content 表示一条规范消息
type Content struct {
	Role  Role   `json:"role"`
	Parts []Part `json:"parts"`
}

// FunctionDeclaration 表示客户端声明的函数
type FunctionDeclaration struct {
	Name        string          `json:"name"`
	Description string          `json:"description,omitempty"`
	Parameters  json.RawMessage `json:"parameters,omitempty"`
}

// ToolConfig 表示工具启用策略
type ToolConfig struct {
	Mode string `json:"mode,omitempty"`
}

// Tools 表示一次请求启用的工具
type Tools struct {
	Functions  []FunctionDeclaration `json:"functions,omitempty"`
	Google     []string              `json:"google,omitempty"`
	ToolConfig ToolConfig            `json:"tool_config,omitempty"`
}

// ResponseModality 表示模型输出模态
type ResponseModality string

const (
	// ResponseModalityText 表示文本输出
	ResponseModalityText ResponseModality = "TEXT"
	// ResponseModalityImage 表示图片输出
	ResponseModalityImage ResponseModality = "IMAGE"
	// ResponseModalityAudio 表示音频输出
	ResponseModalityAudio ResponseModality = "AUDIO"
)

// ImageConfig 表示图片生成参数
type ImageConfig struct {
	AspectRatio string `json:"aspect_ratio,omitempty"`
	ImageSize   string `json:"image_size,omitempty"`
}

// SpeakerVoiceConfig 表示多说话人的声音选择
type SpeakerVoiceConfig struct {
	Speaker   string `json:"speaker"`
	VoiceName string `json:"voice_name"`
}

// SpeechConfig 表示语音生成参数
type SpeechConfig struct {
	VoiceName string               `json:"voice_name,omitempty"`
	Speakers  []SpeakerVoiceConfig `json:"speakers,omitempty"`
}

// GenerationConfig 表示已验证的生成参数
type GenerationConfig struct {
	Temperature        *float64           `json:"temperature,omitempty"`
	TopP               *float64           `json:"top_p,omitempty"`
	TopK               *int               `json:"top_k,omitempty"`
	MaxOutputTokens    *int64             `json:"max_output_tokens,omitempty"`
	StopSequences      []string           `json:"stop_sequences,omitempty"`
	ResponseMIMEType   string             `json:"response_mime_type,omitempty"`
	ResponseSchema     json.RawMessage    `json:"response_schema,omitempty"`
	ResponseModalities []ResponseModality `json:"response_modalities,omitempty"`
	ImageConfig        *ImageConfig       `json:"image_config,omitempty"`
	SpeechConfig       *SpeechConfig      `json:"speech_config,omitempty"`
	ReasoningEffort    string             `json:"reasoning_effort,omitempty"`
	ThinkingBudget     *int64             `json:"thinking_budget,omitempty"`
	Seed               *int64             `json:"seed,omitempty"`
}

// GenerateRequest 表示供应商无关的生成请求
type GenerateRequest struct {
	ID        string           `json:"id"`
	Model     string           `json:"model"`
	System    string           `json:"system,omitempty"`
	Contents  []Content        `json:"contents"`
	Config    GenerationConfig `json:"config,omitempty"`
	Tools     Tools            `json:"tools,omitempty"`
	AccountID string           `json:"account_id,omitempty"`
}

// TokenCountRequest 表示计数请求
type TokenCountRequest struct {
	Model    string    `json:"model"`
	System   string    `json:"system,omitempty"`
	Contents []Content `json:"contents"`
}

// TokenCount 表示上游返回的权威计数
type TokenCount struct {
	InputTokens int64 `json:"input_tokens"`
}

// Model 表示实时模型目录中的模型
type Model struct {
	ID                string              `json:"id"`
	Name              string              `json:"name"`
	Description       string              `json:"description,omitempty"`
	Methods           []string            `json:"methods"`
	InputTokenLimit   int64               `json:"input_token_limit,omitempty"`
	OutputTokenLimit  int64               `json:"output_token_limit,omitempty"`
	Capabilities      map[string]bool     `json:"capabilities,omitempty"`
	CapabilityOptions map[string][]string `json:"capability_options,omitempty"`
}

// Usage 表示一次生成的 token 用量
type Usage struct {
	InputTokens         int64 `json:"input_tokens"`
	OutputTokens        int64 `json:"output_tokens"`
	ReasoningTokens     int64 `json:"reasoning_tokens,omitempty"`
	ToolTokens          int64 `json:"tool_tokens,omitempty"`
	TotalTokens         int64 `json:"total_tokens"`
	OutputTokensMissing bool  `json:"-"`
}

// Citation 表示模型返回的来源
type Citation struct {
	URL       string `json:"url"`
	Title     string `json:"title,omitempty"`
	Start     int    `json:"start,omitempty"`
	End       int    `json:"end,omitempty"`
	Publisher string `json:"publisher,omitempty"`
}

// Media 表示生成的媒体产物
type Media struct {
	URL      string `json:"url,omitempty"`
	Data     []byte `json:"data,omitempty"`
	MIME     string `json:"mime,omitempty"`
	Name     string `json:"name,omitempty"`
	Width    int    `json:"width,omitempty"`
	Height   int    `json:"height,omitempty"`
	Duration int64  `json:"duration_ms,omitempty"`
}

// EventKind 表示规范流事件类型
type EventKind string

const (
	// EventText 表示正文增量
	EventText EventKind = "text"
	// EventReasoning 表示思考摘要增量
	EventReasoning EventKind = "reasoning"
	// EventToolCall 表示函数调用
	EventToolCall EventKind = "tool_call"
	// EventExecutableCode 表示内置代码执行器生成代码
	EventExecutableCode EventKind = "executable_code"
	// EventCodeExecutionResult 表示内置代码执行结果
	EventCodeExecutionResult EventKind = "code_execution_result"
	// EventGrounding 表示 Google 工具来源信息
	EventGrounding EventKind = "grounding"
	// EventCitation 表示来源信息
	EventCitation EventKind = "citation"
	// EventMedia 表示媒体产物
	EventMedia EventKind = "media"
	// EventThoughtSignature 表示独立思考签名
	EventThoughtSignature EventKind = "thought_signature"
	// EventUsage 表示权威用量
	EventUsage EventKind = "usage"
	// EventFinish 表示正常完成
	EventFinish EventKind = "finish"
	// EventError 表示上游错误
	EventError EventKind = "error"
)

// Event 表示协议核心输出的规范事件
type Event struct {
	Kind                EventKind            `json:"kind"`
	Text                string               `json:"text,omitempty"`
	ToolCall            *FunctionCall        `json:"tool_call,omitempty"`
	ExecutableCode      *ExecutableCode      `json:"executable_code,omitempty"`
	CodeExecutionResult *CodeExecutionResult `json:"code_execution_result,omitempty"`
	Grounding           *GroundingMetadata   `json:"grounding,omitempty"`
	Citation            *Citation            `json:"citation,omitempty"`
	Media               *Media               `json:"media,omitempty"`
	Usage               *Usage               `json:"usage,omitempty"`
	FinishReason        string               `json:"finish_reason,omitempty"`
	ProviderModel       string               `json:"provider_model,omitempty"`
	ThoughtSignature    string               `json:"thought_signature,omitempty"`
	Err                 error                `json:"-"`
}

// Service 定义公开协议适配器依赖的最小能力
type Service interface {
	Models(context.Context) ([]Model, error)
	CountTokens(context.Context, TokenCountRequest) (TokenCount, error)
	Generate(context.Context, GenerateRequest) (<-chan Event, error)
}
