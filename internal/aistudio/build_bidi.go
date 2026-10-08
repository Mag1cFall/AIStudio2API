package aistudio

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"slices"
	"strings"
)

const (
	// buildBidiWebChannelURL 是 Build 宿主页转发 Gemini Live WebSocket 的 WebChannel 地址
	buildBidiWebChannelURL = "https://webchannel-alkalimakersuite-pa.clients6.google.com/v1/proxy:proxyBidiStreamedCall"
	// buildLivePath 是 Build 代理 BidiGenerateContent 的 WebSocket 路径
	buildLivePath = "/ws/google.ai.generativelanguage.v1beta.GenerativeService.BidiGenerateContent"
	// buildMusicPath 是 Build 代理 BidiGenerateMusic 的 WebSocket 路径
	buildMusicPath = "/ws/google.ai.generativelanguage.v1alpha.GenerativeService.BidiGenerateMusic"
)

// WeightedPrompt 表示实时音乐的一条加权提示词
type WeightedPrompt struct {
	Text   string  `json:"text"`
	Weight float64 `json:"weight"`
}

// buildPlaybackControls 列出实时音乐可用的播放控制
var buildPlaybackControls = []string{"PLAY", "PAUSE", "STOP", "RESET_CONTEXT"}

// buildBidiPath 按实时方法返回 Build 代理的 WebSocket 路径
func buildBidiPath(method string) string {
	if method == "bidiGenerateMusic" {
		return buildMusicPath
	}
	return buildLivePath
}

// encodeBuildBidiMessage 把一条 Gemini Live JSON 消息编码为 [路径, JSON, proof] 代理外层并返回 binding
func encodeBuildBidiMessage(path string, message any) ([]byte, string, error) {
	body, err := json.Marshal(message)
	if err != nil {
		return nil, "", fmt.Errorf("编码 Build 实时消息: %w", err)
	}
	wire, err := EncodeBuildProxyRequest(path, body, false)
	if err != nil {
		return nil, "", fmt.Errorf("编码 Build 实时代理外层: %w", err)
	}
	return wire, path + " " + string(body), nil
}

// EncodeBuildBidiSetup 编码 Build 代理的 Live 或实时音乐 setup，Live 字段与 Playground setup 的语义一致
func EncodeBuildBidiSetup(request BidiRequest) ([]byte, string, error) {
	model := strings.TrimPrefix(strings.TrimSpace(request.Model), "models/")
	setup := map[string]any{"model": wireModelName(model)}
	if request.method == "bidiGenerateMusic" {
		if len(request.Tools) > 0 || request.Translation != nil || request.Transcription != nil {
			return nil, "", fmt.Errorf("%w: 实时音乐模型 %s 不支持 tools、translation 或 transcription", ErrInvalidArgument, model)
		}
		if output := strings.ToLower(strings.TrimSpace(request.OutputModality)); output != "" && output != "audio" {
			return nil, "", fmt.Errorf("%w: 实时音乐模型 %s 的 output_modalities 必须是 [audio]", ErrInvalidArgument, model)
		}
		return encodeBuildBidiMessage(buildMusicPath, map[string]any{"setup": setup})
	}
	request.variant = bidiVariantConversation
	if err := validateLiveVariant(request); err != nil {
		return nil, "", err
	}
	setup["generationConfig"] = map[string]any{
		"responseModalities": []string{"AUDIO"},
		"speechConfig":       map[string]any{"voiceConfig": map[string]any{"prebuiltVoiceConfig": map[string]any{"voiceName": "Zephyr"}}},
		"mediaResolution":    "MEDIA_RESOLUTION_MEDIUM",
	}
	if len(request.Tools) > 0 {
		tools, _, err := encodeBuildTools(Tools{Functions: request.Tools})
		if err != nil {
			return nil, "", fmt.Errorf("%w: %v", ErrInvalidArgument, err)
		}
		setup["tools"] = tools
	}
	resumption := map[string]any{}
	if token := strings.TrimSpace(request.SessionToken); token != "" {
		resumption["handle"] = token
	}
	setup["sessionResumption"] = resumption
	setup["contextWindowCompression"] = map[string]any{"triggerTokens": "104857", "slidingWindow": map[string]any{"targetTokens": "52428"}}
	setup["inputAudioTranscription"] = map[string]any{}
	setup["outputAudioTranscription"] = map[string]any{}
	return encodeBuildBidiMessage(buildLivePath, map[string]any{"setup": setup})
}

// encodeBuildLiveText 编码 Build Live 的文本实时输入
func encodeBuildLiveText(text string) ([]byte, string, error) {
	if err := validateBidiText(text); err != nil {
		return nil, "", err
	}
	return encodeBuildBidiMessage(buildLivePath, map[string]any{"realtimeInput": map[string]any{"text": text}})
}

// encodeBuildLiveMedia 编码 Build Live 的 16 kHz PCM 音频或 JPEG 图像实时输入
func encodeBuildLiveMedia(mimeType string, data []byte) ([]byte, string, error) {
	mimeType = strings.TrimSpace(mimeType)
	if err := validateBidiMedia(mimeType, data); err != nil {
		return nil, "", err
	}
	blob := map[string]any{"mimeType": mimeType, "data": base64.StdEncoding.EncodeToString(data)}
	field := "video"
	if mimeType == "audio/pcm" {
		blob["mimeType"] = "audio/pcm;rate=16000"
		field = "audio"
	}
	return encodeBuildBidiMessage(buildLivePath, map[string]any{"realtimeInput": map[string]any{field: blob}})
}

// encodeBuildLiveToolResponses 编码 Build Live 的函数响应
func encodeBuildLiveToolResponses(results []FunctionResult) ([]byte, string, error) {
	if err := validateBidiToolResponses(results); err != nil {
		return nil, "", err
	}
	responses := make([]any, 0, len(results))
	for _, result := range results {
		response, err := buildJSONObject(result.Content)
		if err != nil {
			return nil, "", fmt.Errorf("%w: bidi function response content %v", ErrInvalidArgument, err)
		}
		responses = append(responses, map[string]any{"id": result.ID, "name": result.Name, "response": response})
	}
	return encodeBuildBidiMessage(buildLivePath, map[string]any{"toolResponse": map[string]any{"functionResponses": responses}})
}

// webChannelURL 返回会话使用的 WebChannel 地址
func (s *BidiSession) webChannelURL() string {
	if s.buildPath != "" {
		return buildBidiWebChannelURL
	}
	return bidiWebChannelURL
}

// bidiProofField 返回 WebChannel 客户端消息中 WAA proof 的字段号
func bidiProofField(build bool) int {
	if build {
		return buildProofField
	}
	return 6
}

// liveFrame 按会话通道编码 Live 客户端帧：Playground 会话用 playground，Build Live 会话用 build，实时音乐会话返回参数错误
func (s *BidiSession) liveFrame(playground, build func() ([]byte, string, error)) ([]byte, string, error) {
	switch s.buildPath {
	case "":
		return playground()
	case buildLivePath:
		return build()
	default:
		return nil, "", fmt.Errorf("%w: 实时音乐会话只接受 music_prompts、music_config 与 playback", ErrInvalidArgument)
	}
}

// parsePayload 按会话通道解码 backchannel payload
func (s *BidiSession) parsePayload(raw json.RawMessage) ([]BidiEvent, error) {
	if s.buildPath != "" {
		return ParseBuildBidiServerPayload(raw)
	}
	return ParseBidiServerPayload(raw)
}

// Channel 返回会话使用的上游通道
func (s *BidiSession) Channel() Channel {
	return s.lease.Channel()
}

// encodeBuildMusicPrompts 编码实时音乐的加权提示词
func encodeBuildMusicPrompts(prompts []WeightedPrompt) ([]byte, string, error) {
	if len(prompts) == 0 {
		return nil, "", fmt.Errorf("%w: weighted_prompts 不能为空", ErrInvalidArgument)
	}
	for index, prompt := range prompts {
		if strings.TrimSpace(prompt.Text) == "" {
			return nil, "", fmt.Errorf("%w: weighted_prompts[%d].text 不能为空", ErrInvalidArgument, index)
		}
	}
	return encodeBuildBidiMessage(buildMusicPath, map[string]any{"clientContent": map[string]any{"weightedPrompts": prompts}})
}

// encodeBuildMusicConfig 编码实时音乐的生成参数，字段沿用 Gemini API LiveMusicGenerationConfig
func encodeBuildMusicConfig(config json.RawMessage) ([]byte, string, error) {
	var object map[string]any
	if err := json.Unmarshal(config, &object); err != nil || object == nil {
		return nil, "", fmt.Errorf("%w: music_config 必须是 JSON 对象", ErrInvalidArgument)
	}
	return encodeBuildBidiMessage(buildMusicPath, map[string]any{"musicGenerationConfig": object})
}

// encodeBuildPlaybackControl 编码实时音乐的播放控制
func encodeBuildPlaybackControl(control string) ([]byte, string, error) {
	control = strings.ToUpper(strings.TrimSpace(control))
	if !slices.Contains(buildPlaybackControls, control) {
		return nil, "", fmt.Errorf("%w: playback_control 必须是 play、pause、stop 或 reset_context", ErrInvalidArgument)
	}
	return encodeBuildBidiMessage(buildMusicPath, map[string]any{"playbackControl": control})
}

// sendMusic 校验会话为实时音乐后编码并发送一条 BidiGenerateMusic 客户端消息，text 为消息中的用户文本
func (s *BidiSession) sendMusic(ctx context.Context, text string, encode func() ([]byte, string, error)) error {
	if s.buildPath != buildMusicPath {
		return fmt.Errorf("%w: 模型 %s 不是实时音乐模型", ErrInvalidArgument, s.model)
	}
	body, binding, err := encode()
	if err != nil {
		return err
	}
	return s.sendProtected(ctx, body, binding, text, false, true)
}

// SendMusicPrompts 发送实时音乐的加权提示词
func (s *BidiSession) SendMusicPrompts(ctx context.Context, prompts []WeightedPrompt) error {
	texts := make([]string, 0, len(prompts))
	for _, prompt := range prompts {
		texts = append(texts, prompt.Text)
	}
	return s.sendMusic(ctx, strings.Join(texts, " "), func() ([]byte, string, error) { return encodeBuildMusicPrompts(prompts) })
}

// SendMusicConfig 发送实时音乐的生成参数
func (s *BidiSession) SendMusicConfig(ctx context.Context, config json.RawMessage) error {
	return s.sendMusic(ctx, "", func() ([]byte, string, error) { return encodeBuildMusicConfig(config) })
}

// SendPlaybackControl 发送实时音乐的播放控制
func (s *BidiSession) SendPlaybackControl(ctx context.Context, control string) error {
	return s.sendMusic(ctx, "", func() ([]byte, string, error) { return encodeBuildPlaybackControl(control) })
}

// buildLiveServerMessage 表示 Build 代理转发的 Gemini Live 与实时音乐服务端消息
type buildLiveServerMessage struct {
	SetupComplete *json.RawMessage `json:"setupComplete"`
	ServerContent *struct {
		ModelTurn *struct {
			Parts []struct {
				Text       *string `json:"text"`
				Thought    bool    `json:"thought"`
				InlineData *struct {
					MIMEType string `json:"mimeType"`
					Data     []byte `json:"data"`
				} `json:"inlineData"`
			} `json:"parts"`
		} `json:"modelTurn"`
		InputTranscription  *BidiTranscription `json:"inputTranscription"`
		OutputTranscription *BidiTranscription `json:"outputTranscription"`
		Interrupted         bool               `json:"interrupted"`
		GenerationComplete  bool               `json:"generationComplete"`
		TurnComplete        bool               `json:"turnComplete"`
		AudioChunks         []struct {
			MIMEType string `json:"mimeType"`
			Data     []byte `json:"data"`
		} `json:"audioChunks"`
	} `json:"serverContent"`
	ToolCall *struct {
		FunctionCalls []struct {
			ID   string          `json:"id"`
			Name string          `json:"name"`
			Args json.RawMessage `json:"args"`
		} `json:"functionCalls"`
	} `json:"toolCall"`
	ToolCallCancellation *struct {
		IDs []string `json:"ids"`
	} `json:"toolCallCancellation"`
	UsageMetadata           json.RawMessage `json:"usageMetadata"`
	GoAway                  json.RawMessage `json:"goAway"`
	SessionResumptionUpdate *struct {
		NewHandle string `json:"newHandle"`
		Resumable bool   `json:"resumable"`
	} `json:"sessionResumptionUpdate"`
}

// ParseBuildBidiServerPayload 解码 Build 代理 WebChannel 的一条业务 payload：状态、控制标记或 ProxyResponse 列表
func ParseBuildBidiServerPayload(raw json.RawMessage) ([]BidiEvent, error) {
	return parseWebChannelPayload(raw, func(response []json.RawMessage, _ json.RawMessage, path string) ([]BidiEvent, error) {
		text, err := rawString(rawAt(response, 0), path+"[0]", raw)
		if err != nil {
			return nil, withBidiMethod(err)
		}
		return parseBuildLiveServerMessage([]byte(text))
	})
}

// parseBuildLiveServerMessage 按 Playground 解码顺序把 Gemini Live JSON 消息转换为实时事件
func parseBuildLiveServerMessage(raw []byte) ([]BidiEvent, error) {
	var message buildLiveServerMessage
	if err := json.Unmarshal(raw, &message); err != nil {
		return nil, &ProtocolEvidenceError{Method: "BidiGenerateContent", Path: "$proxy[0]", Detail: "Gemini Live JSON 无效: " + err.Error(), Raw: cloneRaw(raw)}
	}
	events := make([]BidiEvent, 0, 4)
	if message.SetupComplete != nil {
		events = append(events, BidiEvent{Kind: BidiEventSetupComplete})
	}
	if content := message.ServerContent; content != nil {
		if content.ModelTurn != nil {
			for _, part := range content.ModelTurn.Parts {
				switch {
				case part.Text != nil && part.Thought:
					encoded, _ := json.Marshal(Event{Kind: EventReasoning, Text: *part.Text})
					events = append(events, BidiEvent{Kind: BidiEventProvider, Raw: encoded})
				case part.Text != nil:
					events = append(events, BidiEvent{Kind: BidiEventText, Text: *part.Text})
				case part.InlineData != nil:
					events = append(events, BidiEvent{Kind: BidiEventMedia, Media: &Media{MIME: part.InlineData.MIMEType, Data: part.InlineData.Data}})
				}
			}
		}
		for _, chunk := range content.AudioChunks {
			events = append(events, BidiEvent{Kind: BidiEventMedia, Media: &Media{MIME: chunk.MIMEType, Data: chunk.Data}})
		}
		if content.InputTranscription != nil {
			events = append(events, BidiEvent{Kind: BidiEventInputTranscription, Text: content.InputTranscription.Text, Transcription: content.InputTranscription})
		}
		if content.OutputTranscription != nil {
			events = append(events, BidiEvent{Kind: BidiEventOutputTranscription, Text: content.OutputTranscription.Text, Transcription: content.OutputTranscription})
		}
		for _, flag := range []struct {
			set  bool
			kind BidiEventKind
		}{{content.Interrupted, BidiEventInterrupted}, {content.GenerationComplete, BidiEventGenerationComplete}, {content.TurnComplete, BidiEventTurnComplete}} {
			if flag.set {
				events = append(events, BidiEvent{Kind: flag.kind})
			}
		}
	}
	if message.ToolCall != nil {
		for index, call := range message.ToolCall.FunctionCalls {
			if strings.TrimSpace(call.ID) == "" {
				return nil, &ProtocolEvidenceError{
					Method: "BidiGenerateContent", Path: fmt.Sprintf("$.toolCall.functionCalls[%d].id", index), Detail: "tool call 缺少调用 ID", Raw: cloneRaw(raw),
				}
			}
			arguments := bytes.TrimSpace(call.Args)
			if len(arguments) == 0 {
				arguments = []byte("{}")
			}
			events = append(events, BidiEvent{Kind: BidiEventToolCall, ToolCall: &FunctionCall{ID: call.ID, Name: call.Name, Arguments: cloneRaw(arguments)}})
		}
	}
	if message.ToolCallCancellation != nil {
		events = append(events, BidiEvent{Kind: BidiEventToolCallCancellation, ToolCallIDs: message.ToolCallCancellation.IDs})
	}
	if len(message.UsageMetadata) > 0 {
		events = append(events, BidiEvent{Kind: BidiEventUsage, Raw: cloneRaw(message.UsageMetadata)})
	}
	if len(message.GoAway) > 0 {
		events = append(events, BidiEvent{Kind: BidiEventGoAway, Raw: cloneRaw(message.GoAway)})
	}
	if update := message.SessionResumptionUpdate; update != nil {
		events = append(events, BidiEvent{Kind: BidiEventSessionResumption, SessionToken: update.NewHandle, Resumable: update.Resumable})
	}
	if len(events) == 0 {
		events = append(events, BidiEvent{Kind: BidiEventProvider, Raw: cloneRaw(raw)})
	}
	return events, nil
}
