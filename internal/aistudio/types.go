package aistudio

import (
	"context"
	"encoding/json"
	"time"
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

// ExternalMedia 表示可由 AI Studio 直接读取的外部媒体
type ExternalMedia struct {
	MIME string `json:"mime"`
	URL  string `json:"url"`
}

// FileRef 表示已上传文件引用
type FileRef struct {
	ID   string `json:"id"`
	Name string `json:"name,omitempty"`
	MIME string `json:"mime,omitempty"`
}

// UploadRef 表示经 AI Studio /_/upload 上传的附件引用
type UploadRef struct {
	MIME string `json:"mime"`
	Path string `json:"path"`
	Name string `json:"name"`
	Size int64  `json:"size"`
}

// VideoMetadata 是视频 Part 的截取起止与采样帧率，起止为 Duration 字符串
type VideoMetadata struct {
	StartOffset string   `json:"start_offset,omitempty"`
	EndOffset   string   `json:"end_offset,omitempty"`
	FPS         *float64 `json:"fps,omitempty"`
}

// ServerToolStep 是上游服务端工具的一次调用或其结果，Type 为 Gemini API ToolType 枚举名，Data 为调用参数或结果
type ServerToolStep struct {
	ID   string          `json:"id,omitempty"`
	Type string          `json:"type"`
	Name string          `json:"name,omitempty"`
	Data json.RawMessage `json:"data,omitempty"`
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
	// WillContinue 表示非阻塞函数之后还会发送同一调用的响应
	WillContinue bool `json:"will_continue,omitempty"`
}

// ExecutableCode 表示 AI Studio 内置代码执行器生成的代码
type ExecutableCode struct {
	Language string `json:"language"`
	Code     string `json:"code"`
	ID       string `json:"id,omitempty"`
}

// CodeExecutionResult 表示 AI Studio 内置代码执行器返回的结果
type CodeExecutionResult struct {
	Outcome string `json:"outcome"`
	Output  string `json:"output,omitempty"`
	ID      string `json:"id,omitempty"`
}

// SearchEntryPoint 表示 Google Search 返回的搜索入口
type SearchEntryPoint struct {
	RenderedContent string `json:"rendered_content,omitempty"`
	SDKBlob         string `json:"sdk_blob,omitempty"`
}

// GroundingChunk 表示 Google 工具检索到的一个来源，Source 为 web、image、retrieved_context 或 maps
type GroundingChunk struct {
	Source  string `json:"source"`
	URI     string `json:"uri,omitempty"`
	Title   string `json:"title,omitempty"`
	Text    string `json:"text,omitempty"`
	PlaceID string `json:"place_id,omitempty"`
	// ImageURI 是图片搜索来源的图片地址，URI 为图片所在网页
	ImageURI string `json:"image_uri,omitempty"`
	Domain   string `json:"domain,omitempty"`
	// PlaceAnswerSources 是 Maps 来源的评论片段
	PlaceAnswerSources json.RawMessage `json:"place_answer_sources,omitempty"`
	// SiteName 与 SearchResultMapping 是网页来源的站点名与它在搜索结果中的位置
	SiteName            string          `json:"site_name,omitempty"`
	SearchResultMapping json.RawMessage `json:"search_result_mapping,omitempty"`
	// Origin 与 ReferenceID 是上游给出的来源产生方式与引用编号
	Origin      string `json:"origin,omitempty"`
	ReferenceID string `json:"reference_id,omitempty"`
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
	// RenderedParts 是该片段对应的图片等渲染 Part 下标
	RenderedParts []int `json:"rendered_parts,omitempty"`
	// Origin 是上游给出的支撑关系产生方式
	Origin string `json:"origin,omitempty"`
}

// GroundingMetadata 表示 Google Search、URL Context 和 Maps 的来源信息
type GroundingMetadata struct {
	SearchEntryPoint       *SearchEntryPoint  `json:"search_entry_point,omitempty"`
	Chunks                 []GroundingChunk   `json:"chunks,omitempty"`
	Supports               []GroundingSupport `json:"supports,omitempty"`
	DynamicRetrievalScore  *float64           `json:"dynamic_retrieval_score,omitempty"`
	WebSearchQueries       []string           `json:"web_search_queries,omitempty"`
	ImageSearchQueries     []string           `json:"image_search_queries,omitempty"`
	MapsWidgetContextToken string             `json:"maps_widget_context_token,omitempty"`
	URLContext             []URLMetadata      `json:"url_context,omitempty"`
}

// URLMetadata 表示 URL context 工具读取的一个网址与读取结果
type URLMetadata struct {
	RetrievedURL string `json:"retrieved_url"`
	Status       string `json:"status"`
}

// Part 表示规范消息中的一个内容块
type Part struct {
	Text                string               `json:"text,omitempty"`
	InlineData          *Blob                `json:"inline_data,omitempty"`
	ExternalMedia       *ExternalMedia       `json:"external_media,omitempty"`
	File                *FileRef             `json:"file,omitempty"`
	Upload              *UploadRef           `json:"upload,omitempty"`
	FunctionCall        *FunctionCall        `json:"function_call,omitempty"`
	FunctionResult      *FunctionResult      `json:"function_result,omitempty"`
	ExecutableCode      *ExecutableCode      `json:"executable_code,omitempty"`
	CodeExecutionResult *CodeExecutionResult `json:"code_execution_result,omitempty"`
	Thought             bool                 `json:"thought,omitempty"`
	ThoughtSignature    string               `json:"thought_signature,omitempty"`
	SpeechMetadata      *SpeechMetadata      `json:"speech_metadata,omitempty"`
	VideoMetadata       *VideoMetadata       `json:"video_metadata,omitempty"`
	// MediaProcessing 为视频处理方式 STATIC 或 AGENTIC
	MediaProcessing string          `json:"media_processing,omitempty"`
	ToolCall        *ServerToolStep `json:"tool_call,omitempty"`
	ToolResponse    *ServerToolStep `json:"tool_response,omitempty"`
	// Transcript 是转录正文附带的说话人和逐词时间
	Transcript *TranscriptMetadata `json:"transcript,omitempty"`
	// DisplayName 是媒体给模型引用的名称
	DisplayName string `json:"display_name,omitempty"`
	// MediaResolution 是该媒体的输入分辨率，取 Gemini API 枚举名
	MediaResolution string `json:"media_resolution,omitempty"`
}

// SpeechMetadata 表示语音文本分段的说话人与风格
type SpeechMetadata struct {
	Speaker string `json:"speaker,omitempty"`
	Style   string `json:"style,omitempty"`
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
	// Response 是函数返回值的 JSON Schema，Playground 没有对应字段
	Response json.RawMessage `json:"response,omitempty"`
	Strict   bool            `json:"strict,omitempty"`
	// Behavior 为 Live 函数的调用方式 BLOCKING 或 NON_BLOCKING，不区分大小写
	Behavior string `json:"behavior,omitempty"`
}

// ToolConfig 表示工具启用策略
type ToolConfig struct {
	Mode                 string   `json:"mode,omitempty"`
	AllowedFunctionNames []string `json:"allowed_function_names,omitempty"`
	ParallelCalls        *bool    `json:"parallel_calls,omitempty"`
	// ServerSideInvocations 要求响应包含服务端工具的调用与结果 Part
	ServerSideInvocations bool `json:"server_side_invocations,omitempty"`
	// Retrieval 是 Google 工具使用的用户位置与语言
	Retrieval *RetrievalConfig `json:"retrieval,omitempty"`
}

// RetrievalConfig 是 toolConfig.retrievalConfig 的用户坐标与语言代码
type RetrievalConfig struct {
	Latitude     *float64 `json:"latitude,omitempty"`
	Longitude    *float64 `json:"longitude,omitempty"`
	LanguageCode string   `json:"language_code,omitempty"`
}

// ComputerUse 是 Computer Use 工具的运行环境与预置函数设置
type ComputerUse struct {
	Environment                    string   `json:"environment"`
	ExcludedPredefinedFunctions    []string `json:"excluded_predefined_functions,omitempty"`
	DisabledSafetyPolicies         []string `json:"disabled_safety_policies,omitempty"`
	EnablePromptInjectionDetection bool     `json:"enable_prompt_injection_detection,omitempty"`
}

// MCPServer 是由上游连接并调用的 Streamable HTTP MCP 服务器
type MCPServer struct {
	Name             string            `json:"name"`
	URL              string            `json:"url"`
	Headers          map[string]string `json:"headers,omitempty"`
	Timeout          string            `json:"timeout,omitempty"`
	SSEReadTimeout   string            `json:"sse_read_timeout,omitempty"`
	TerminateOnClose *bool             `json:"terminate_on_close,omitempty"`
	AllowedTools     []string          `json:"allowed_tools,omitempty"`
}

// GoogleSearchTimeRange 表示 Google Search 的检索时间范围
type GoogleSearchTimeRange struct {
	StartTime time.Time `json:"start_time,omitzero"`
	EndTime   time.Time `json:"end_time,omitzero"`
}

// GoogleSearchOptions 表示 Google Search 的检索类型与时间范围
type GoogleSearchOptions struct {
	WebSearch      bool                   `json:"web_search,omitempty"`
	ImageSearch    bool                   `json:"image_search,omitempty"`
	TimeRange      *GoogleSearchTimeRange `json:"time_range,omitempty"`
	ContextSize    string                 `json:"context_size,omitempty"`
	UserLocation   json.RawMessage        `json:"user_location,omitempty"`
	AllowedDomains []string               `json:"allowed_domains,omitempty"`
	BlockedDomains []string               `json:"blocked_domains,omitempty"`
}

// Tools 表示一次请求启用的工具
type Tools struct {
	Functions    []FunctionDeclaration `json:"functions,omitempty"`
	Google       []string              `json:"google,omitempty"`
	GoogleSearch *GoogleSearchOptions  `json:"google_search,omitempty"`
	// MapsWidget 要求 Google Maps 在来源信息中返回 widget context token
	MapsWidget  bool         `json:"maps_widget,omitempty"`
	ComputerUse *ComputerUse `json:"computer_use,omitempty"`
	MCPServers  []MCPServer  `json:"mcp_servers,omitempty"`
	// FileSearch 是 Gemini API fileSearch 工具的原始设置
	FileSearch json.RawMessage `json:"file_search,omitempty"`
	ToolConfig ToolConfig      `json:"tool_config,omitempty"`
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

// VideoConfig 是 Omni 视频输出设置：宽高比、时长秒数、分辨率与任务，未设置的项使用模型默认值
type VideoConfig struct {
	AspectRatio     string `json:"aspect_ratio,omitempty"`
	DurationSeconds int64  `json:"duration_seconds,omitempty"`
	Resolution      string `json:"resolution,omitempty"`
	Task            string `json:"task,omitempty"`
}

// SafetySetting 表示一个安全类别的拦截阈值，取 Gemini API 枚举名
type SafetySetting struct {
	Category  string `json:"category"`
	Threshold string `json:"threshold"`
}

// SpeakerVoiceConfig 表示多说话人的声音选择
type SpeakerVoiceConfig struct {
	Speaker   string `json:"speaker"`
	VoiceName string `json:"voice_name"`
	// Voice 是 VoiceConfig.voice 的说话人标识，取预置音色名或已存音色 ID
	Voice string `json:"voice,omitempty"`
}

// SpeechConfig 表示语音生成参数
type SpeechConfig struct {
	VoiceName string               `json:"voice_name,omitempty"`
	Voice     string               `json:"voice,omitempty"`
	Speakers  []SpeakerVoiceConfig `json:"speakers,omitempty"`
	// LanguageCode 是 BCP-47 语言代码
	LanguageCode string `json:"language_code,omitempty"`
	// Mode 表示多说话人朗读模式 VERBATIM 或 CONVERSATIONAL
	Mode string `json:"mode,omitempty"`
}

// TranscriptionConfig 表示音频转录参数
type TranscriptionConfig struct {
	WordTimestamps     bool     `json:"word_timestamps,omitempty"`
	SpeakerLabels      bool     `json:"speaker_labels,omitempty"`
	CustomVocabulary   []string `json:"custom_vocabulary,omitempty"`
	LanguageCodes      []string `json:"language_codes,omitempty"`
	SmartTranscription bool     `json:"smart_transcription,omitempty"`
	// AdaptationPhrases 是提高识别率的短语
	AdaptationPhrases []string `json:"adaptation_phrases,omitempty"`
	// LanguageHints 是音频中可能出现的语言代码
	LanguageHints []string `json:"language_hints,omitempty"`
	// LanguageAuto 要求模型自动检测语言
	LanguageAuto bool `json:"language_auto,omitempty"`
}

// GenerationConfig 表示已验证的生成参数
type GenerationConfig struct {
	Temperature      *float64 `json:"temperature,omitempty"`
	TopP             *float64 `json:"top_p,omitempty"`
	TopK             *int     `json:"top_k,omitempty"`
	MaxOutputTokens  *int64   `json:"max_output_tokens,omitempty"`
	StopSequences    []string `json:"stop_sequences,omitempty"`
	ResponseMIMEType string   `json:"response_mime_type,omitempty"`
	// ResponseSchema 是 Gemini API responseSchema 形式的 OpenAPI Schema
	ResponseSchema json.RawMessage `json:"response_schema,omitempty"`
	// ResponseJSONSchema 是 JSON Schema 形式的结构化输出约束
	ResponseJSONSchema  json.RawMessage      `json:"response_json_schema,omitempty"`
	ResponseModalities  []ResponseModality   `json:"response_modalities,omitempty"`
	ImageConfig         *ImageConfig         `json:"image_config,omitempty"`
	VideoConfig         *VideoConfig         `json:"video_config,omitempty"`
	SpeechConfig        *SpeechConfig        `json:"speech_config,omitempty"`
	TranscriptionConfig *TranscriptionConfig `json:"transcription_config,omitempty"`
	ReasoningEffort     string               `json:"reasoning_effort,omitempty"`
	ThinkingBudget      *int64               `json:"thinking_budget,omitempty"`
	Seed                *int64               `json:"seed,omitempty"`
	// MediaResolution 为输入媒体分辨率，取 Gemini API 枚举名
	MediaResolution string `json:"media_resolution,omitempty"`
	// EnhancedCivicAnswers 开启公民议题增强回答
	EnhancedCivicAnswers bool `json:"enhanced_civic_answers,omitempty"`
	// HideThinking 控制公开响应的思考块可见性
	HideThinking bool `json:"hide_thinking,omitempty"`
}

// GenerateRequest 表示供应商无关的生成请求
type GenerateRequest struct {
	ID    string `json:"id"`
	Model string `json:"model"`
	// System 是系统指令的各段文本
	System    []string         `json:"system,omitempty"`
	Contents  []Content        `json:"contents"`
	Config    GenerationConfig `json:"config,omitempty"`
	Tools     Tools            `json:"tools,omitempty"`
	AccountID string           `json:"account_id,omitempty"`
	// SafetySettings 覆盖对应类别的拦截阈值，未列出的类别保持关闭
	SafetySettings []SafetySetting `json:"safety_settings,omitempty"`
	// ServiceTier 是 Gemini API 服务等级 flex、standard 或 priority
	ServiceTier string `json:"service_tier,omitempty"`
	// Store 设置上游是否记录本次请求
	Store *bool `json:"store,omitempty"`
	// Labels 是请求的计费标签
	Labels map[string]string `json:"labels,omitempty"`
	// ImageRoute 内部标记：图像生成模型（由模型目录能力推导）
	ImageRoute bool `json:"-"`
	// Unary 标记单次非流式请求（在 Build 代理中使用 ProxyUnaryCall）
	Unary bool `json:"-"`
	// Background 标记没有客户端等待首事件的后台任务
	Background bool `json:"-"`
	// Truncate 在生成前按模型上下文窗口移除最早的完整轮次
	Truncate bool `json:"-"`
	// continuationRound 为自动续写的轮次，首轮为 0
	continuationRound int
	// continuationToken 为上一轮响应给出的续写 token
	continuationToken string
}

// TokenCountRequest 表示计数请求
type TokenCountRequest struct {
	Model    string    `json:"model"`
	System   []string  `json:"system,omitempty"`
	Contents []Content `json:"contents"`
	Tools    Tools     `json:"tools,omitempty"`
	// MediaResolution 为请求级输入媒体分辨率，取 Gemini API 枚举名
	MediaResolution string `json:"media_resolution,omitempty"`
}

// TokenCount 表示上游返回的权威计数
type TokenCount struct {
	InputTokens int64 `json:"input_tokens"`
	// PromptTokensDetails 是按模态细分的输入 token 数
	PromptTokensDetails []ModalityTokenCount `json:"prompt_tokens_details,omitempty"`
	// System 是系统指令各 Part 的 token 数，上游未给出时为 nil
	System *ContentTokens `json:"system,omitempty"`
	// Contents 是各 content 的逐 Part 与角色 token 数
	Contents []ContentTokens `json:"contents,omitempty"`
	// Tools 是各工具的 token 数
	Tools []ToolTokens `json:"tools,omitempty"`
}

// ContentTokens 是一个 content 或系统指令的逐 Part token 数与角色 token 数
type ContentTokens struct {
	PartTokens []int64 `json:"partTokens,omitempty"`
	RoleTokens int64   `json:"roleTokens,omitempty"`
}

// ToolTokens 是一个工具的逐函数声明 token 数
type ToolTokens struct {
	FunctionDeclarationTokens []int64 `json:"functionDeclarationTokens,omitempty"`
}

// Model 表示实时模型目录中的模型
type Model struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
	Version     string `json:"version,omitempty"`
	// Stage 是模型阶段，取值为 Gemini API ModelStage 枚举名
	Stage            string   `json:"stage,omitempty"`
	Methods          []string `json:"methods"`
	InputTokenLimit  int64    `json:"input_token_limit,omitempty"`
	OutputTokenLimit int64    `json:"output_token_limit,omitempty"`
	Temperature      *float64 `json:"temperature,omitempty"`
	MaxTemperature   *float64 `json:"max_temperature,omitempty"`
	TopP             *float64 `json:"top_p,omitempty"`
	TopK             *int     `json:"top_k,omitempty"`
	// ThinkingLevels 是模型支持的思考档位，按目录 field 72 的顺序排列
	ThinkingLevels       []string `json:"thinking_levels,omitempty"`
	DefaultThinkingLevel string   `json:"default_thinking_level,omitempty"`
	// ThinkingBudget 是可设思考预算的模型的预算默认值、上下限与能否关闭思考，上游未给出的项省略
	ThinkingBudget *ThinkingBudget `json:"thinking_budget,omitempty"`
	// ToolCompatibility 是可同时开启的工具对
	ToolCompatibility [][2]string         `json:"tool_compatibility,omitempty"`
	Details           *ModelDetails       `json:"details,omitempty"`
	Capabilities      map[string]bool     `json:"capabilities,omitempty"`
	CapabilityOptions map[string][]string `json:"capability_options,omitempty"`
	// VoiceDetails 是 TTS 与 Live 模型的音色介绍，顺序与官网音色列表一致
	VoiceDetails []VoiceDetail `json:"voice_details,omitempty"`
	// Languages 是实时翻译与转录模型可选的语言，顺序与官网语言列表一致
	Languages   []Language `json:"languages,omitempty"`
	AccessModes []int64    `json:"access_modes,omitempty"`
	Paid        bool       `json:"paid,omitempty"`
	Channels    []string   `json:"channels,omitempty"`
}

// ThinkingBudget 是 ListModels field 72 的思考预算配置
type ThinkingBudget struct {
	Default    *int64 `json:"default,omitempty"`
	Max        *int64 `json:"max,omitempty"`
	Min        *int64 `json:"min,omitempty"`
	CanDisable *bool  `json:"can_disable,omitempty"`
}

// Language 是模型可选语言的代码与显示名
type Language struct {
	Code string `json:"code"`
	Name string `json:"name"`
}

// VoiceDetail 是一个音色的试听地址、音高、描述、口音、性别、特质与用途
type VoiceDetail struct {
	Name         string   `json:"name"`
	SampleURL    string   `json:"sample_url,omitempty"`
	Pitch        string   `json:"pitch,omitempty"`
	Descriptions []string `json:"descriptions,omitempty"`
	Locale       string   `json:"locale,omitempty"`
	Accent       string   `json:"accent,omitempty"`
	Gender       string   `json:"gender,omitempty"`
	Trait        string   `json:"trait,omitempty"`
	UseCase      string   `json:"use_case,omitempty"`
}

// ModelDetails 是 ListModels field 39 的模型说明，日期为 Unix 秒
type ModelDetails struct {
	Summary         string       `json:"summary,omitempty"`
	Alias           string       `json:"alias,omitempty"`
	Warning         string       `json:"warning,omitempty"`
	DocsURL         string       `json:"docs_url,omitempty"`
	ModelCardURL    string       `json:"model_card_url,omitempty"`
	ReleaseDate     int64        `json:"release_date,omitempty"`
	KnowledgeCutoff int64        `json:"knowledge_cutoff,omitempty"`
	Pricing         []ModelPrice `json:"pricing,omitempty"`
	PricingURL      string       `json:"pricing_url,omitempty"`
	PricingNote     string       `json:"pricing_note,omitempty"`
}

// ModelPrice 是模型价格的一行：Text 为上游给出的整句说明；Input、Output 为每百万 token 美元，按次计价时对应 PerRequest 为真；UpTo 为该档输入 token 上限，Thinking 标记思考档
type ModelPrice struct {
	Text             string   `json:"text,omitempty"`
	Label            string   `json:"label,omitempty"`
	UpTo             int64    `json:"up_to,omitempty"`
	Thinking         bool     `json:"thinking,omitempty"`
	Input            *float64 `json:"input,omitempty"`
	InputPerRequest  bool     `json:"input_per_request,omitempty"`
	Output           *float64 `json:"output,omitempty"`
	OutputPerRequest bool     `json:"output_per_request,omitempty"`
}

// Usage 表示一次生成的 token 用量，CachedTokens 是 InputTokens 中命中上游缓存的部分
type Usage struct {
	InputTokens         int64 `json:"input_tokens"`
	CachedTokens        int64 `json:"cached_tokens,omitempty"`
	OutputTokens        int64 `json:"output_tokens"`
	ReasoningTokens     int64 `json:"reasoning_tokens,omitempty"`
	ToolTokens          int64 `json:"tool_tokens,omitempty"`
	TotalTokens         int64 `json:"total_tokens"`
	OutputTokensMissing bool  `json:"-"`
	// Details 是上游按模态细分的用量与服务等级
	Details *UsageDetails `json:"-"`
}

// ModalityTokenCount 是单一模态的 token 数
type ModalityTokenCount struct {
	Modality   string `json:"modality"`
	TokenCount int64  `json:"tokenCount,omitempty"`
}

// UsageDetails 是 Gemini API usageMetadata 的模态细分与服务等级
type UsageDetails struct {
	PromptTokensDetails        []ModalityTokenCount `json:"promptTokensDetails,omitempty"`
	CacheTokensDetails         []ModalityTokenCount `json:"cacheTokensDetails,omitempty"`
	CandidatesTokensDetails    []ModalityTokenCount `json:"candidatesTokensDetails,omitempty"`
	ToolUsePromptTokensDetails []ModalityTokenCount `json:"toolUsePromptTokensDetails,omitempty"`
	ServiceTier                string               `json:"serviceTier,omitempty"`
	// BilledToolCalls、ToolCallStats、ModelInvocationTokenCounts、RawPromptTokenCount 与 NonGroundingModelInvocationTokenCounts 是 Gemini API 输出但不在公开定义中的用量字段
	BilledToolCalls                        json.RawMessage `json:"billedToolCalls,omitempty"`
	ToolCallStats                          json.RawMessage `json:"toolCallStats,omitempty"`
	ModelInvocationTokenCounts             json.RawMessage `json:"modelInvocationTokenCounts,omitempty"`
	RawPromptTokenCount                    int64           `json:"rawPromptTokenCount,omitempty"`
	NonGroundingModelInvocationTokenCounts json.RawMessage `json:"nonGroundingModelInvocationTokenCounts,omitempty"`
}

// present 表示上游给出了任一细分或服务等级
func (details UsageDetails) present() bool {
	return len(details.PromptTokensDetails)+len(details.CacheTokensDetails)+len(details.CandidatesTokensDetails)+len(details.ToolUsePromptTokensDetails)+len(details.BilledToolCalls)+len(details.ToolCallStats)+len(details.ModelInvocationTokenCounts)+len(details.NonGroundingModelInvocationTokenCounts) > 0 || details.ServiceTier != "" || details.RawPromptTokenCount != 0
}

// Citation 表示模型返回的来源
type Citation struct {
	URL       string `json:"url"`
	Title     string `json:"title,omitempty"`
	Start     int    `json:"start,omitempty"`
	End       int    `json:"end,omitempty"`
	Publisher string `json:"publisher,omitempty"`
	License   string `json:"license,omitempty"`
}

// TranscriptDuration 表示转录时间值
type TranscriptDuration struct {
	Seconds int64 `json:"seconds"`
	Nanos   int64 `json:"nanos,omitempty"`
}

// TranscriptWord 是转录中的一个词与起止时间
type TranscriptWord struct {
	Word  string             `json:"word"`
	Start TranscriptDuration `json:"start"`
	End   TranscriptDuration `json:"end"`
}

// TranscriptMetadata 表示转录正文附带的说话人和逐词时间
type TranscriptMetadata struct {
	Text    string           `json:"text"`
	Speaker string           `json:"speaker,omitempty"`
	Words   []TranscriptWord `json:"words,omitempty"`
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
	// EventServerToolCall 表示上游服务端工具调用
	EventServerToolCall EventKind = "server_tool_call"
	// EventServerToolResponse 表示上游服务端工具结果
	EventServerToolResponse EventKind = "server_tool_response"
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

// Event 表示协议核心输出的规范事件，内容事件携带上游返回的完整 Part
type Event struct {
	Kind EventKind `json:"kind"`
	Part
	Grounding     *GroundingMetadata `json:"grounding,omitempty"`
	Citation      *Citation          `json:"citation,omitempty"`
	Usage         *Usage             `json:"usage,omitempty"`
	FinishReason  string             `json:"finish_reason,omitempty"`
	FinishMessage string             `json:"finish_message,omitempty"`
	StopSequence  string             `json:"stop_sequence,omitempty"`
	ProviderModel string             `json:"provider_model,omitempty"`
	// SafetyRatings 是结束事件携带的候选安全评级
	SafetyRatings []SafetyRating `json:"safety_ratings,omitempty"`
	// TurnToken 是上游响应帧的轮次令牌
	TurnToken string `json:"turn_token,omitempty"`
	// DebugInfo 是 Build 通道单次响应的 debugInfo
	DebugInfo json.RawMessage `json:"debug_info,omitempty"`
	// DetailedFinishReason 是 Build 通道结束事件的 detailedFinishReason
	DetailedFinishReason json.RawMessage `json:"detailed_finish_reason,omitempty"`
	// ResponseID 是上游给出的该响应的 responseId
	ResponseID string `json:"response_id,omitempty"`
	// FrameUsage 是事件所在上游帧给出的累计用量
	FrameUsage *Usage `json:"frame_usage,omitempty"`
	// FinishFrame 表示事件来自带结束原因的上游帧
	FinishFrame bool  `json:"finish_frame,omitempty"`
	Err         error `json:"-"`
}

// SafetyRating 是候选或输入在一个安全类别上的评级，取值为 Gemini API 枚举名
type SafetyRating struct {
	Category    string `json:"category"`
	Probability string `json:"probability"`
	Blocked     bool   `json:"blocked,omitempty"`
}

// Service 定义公开协议适配器依赖的最小能力
type Service interface {
	Models(context.Context) ([]Model, error)
	CountTokens(context.Context, TokenCountRequest) (TokenCount, error)
	Generate(context.Context, GenerateRequest) (<-chan Event, error)
}
