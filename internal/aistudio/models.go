package aistudio

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"strconv"
	"strings"
)

var capabilityNames = map[int64]string{
	1:  "chat_model",
	9:  "code_execution",
	10: "function_declarations",
	12: "google_search",
	13: "browse",
	20: "video_route",
	21: "image_route",
	25: "thinking",
	26: "live_route",
	35: "thinking_budget",
	37: "speech_route",
	43: "media_resolution",
	46: "speech_translation",
	47: "aspect_ratio",
	48: "code_assistant",
	49: "output_resolution",
	52: "thinking_level",
	53: "music_route",
	54: "image_search",
	58: "google_maps",
	59: "interaction_route",
	74: "transcription_word_timestamps",
	76: "transcription_language_codes",
	77: "transcription_output",
	80: "transcription_speaker_labels",
	81: "transcription_custom_vocabulary",
	84: "transcription_smart",
	85: "speech_metadata",
	91: "output_continuation",
}

var secondaryCapabilityNames = map[int64]string{}

var aspectRatios = map[int64]string{
	1: "1:1", 2: "9:16", 3: "16:9", 4: "3:4", 5: "4:3",
	6: "3:2", 7: "2:3", 8: "5:4", 9: "4:5", 10: "21:9",
	11: "9:21", 12: "1:4", 13: "4:1", 14: "1:8", 15: "8:1",
}

var imageResolutions = map[int64]string{1: "1K", 2: "2K", 3: "4K", 4: "512"}
var videoDurations = map[int64]string{1: "5", 2: "6", 3: "7", 4: "8", 5: "4"}
var videoResolutions = map[int64]string{1: "720p", 2: "1080p", 3: "4k", 4: "368p", 5: "360p"}
var videoFrameRates = map[int64]string{1: "24fps"}
var videoTasks = map[int64]string{1: "text_to_video", 2: "image_to_video", 3: "reference_to_video", 4: "edit", 5: "extend"}

// videoInputs 为视频能力 field 70[7] 中的输入方式编号，4 为起始帧与尾帧，5 为参考图
var videoInputs = map[int64]string{4: "frames", 5: "reference_images"}

// GenerationDefaults 保存 ListModels 返回的生成默认值
type GenerationDefaults struct {
	MaxOutputTokens      int64
	Temperature          *float64
	TopP                 *float64
	TopK                 *int
	Thinking             bool
	ThinkingBudget       bool
	ThinkingLevel        bool
	DefaultThinkingLevel int64
	ThinkingLevels       []int64
	// ImageRoute 表示图像生成模型（官网 GenerateContent 走扩展约束）
	ImageRoute bool
	// OutputResolution 表示支持输出分辨率（官网会带 imageConfig 与扩展槽位）
	OutputResolution bool
	// InteractionStream 表示官网经 CreateInteractionStream 生成的模型
	InteractionStream bool
	// InteractionTask 表示官网经 CreateInteraction 创建后台任务、经 GetInteractionStream 读取结果的模型
	InteractionTask bool
	// VideoResolution 为视频输出分辨率选项中的第一项，Interaction 视频输出格式的默认分辨率
	VideoResolution string
	// ContinuedOutputTokens 表示自动续写模型的输出总上限，其他模型为 0
	ContinuedOutputTokens int64
}

type modelEntry struct {
	model    Model
	defaults GenerationDefaults
}

type modelCatalog struct {
	models  []Model
	entries map[string]modelEntry
}

// ParseModels 解码 ListModels 的现场数组协议
func ParseModels(source io.Reader) ([]Model, error) {
	catalog, err := parseModelCatalog(source)
	if err != nil {
		return nil, err
	}
	return cloneModels(catalog.models), nil
}

func parseModelCatalog(source io.Reader) (modelCatalog, error) {
	raw, err := io.ReadAll(newSparseJSONReader(source))
	if err != nil {
		return modelCatalog{}, fmt.Errorf("读取 ListModels: %w", err)
	}
	root, err := rawArray(raw, "$", raw)
	if err != nil {
		return modelCatalog{}, withMethod(err, "ListModels")
	}
	if len(root) == 0 || isJSONNull(root[0]) {
		return modelCatalog{}, &ProtocolEvidenceError{Method: "ListModels", Path: "$[0]", Detail: "缺少模型列表", Raw: raw}
	}
	rows, err := rawArray(root[0], "$[0]", raw)
	if err != nil {
		return modelCatalog{}, withMethod(err, "ListModels")
	}
	catalog := modelCatalog{models: make([]Model, 0, len(rows)), entries: make(map[string]modelEntry, len(rows))}
	for index, rowRaw := range rows {
		entry, err := decodeModelRow(rowRaw, index)
		if err != nil {
			return modelCatalog{}, err
		}
		if _, exists := catalog.entries[entry.model.ID]; exists {
			return modelCatalog{}, &ProtocolEvidenceError{Method: "ListModels", Path: fmt.Sprintf("$[0][%d][0]", index), Detail: "模型 ID 重复", Raw: rowRaw}
		}
		catalog.models = append(catalog.models, entry.model)
		catalog.entries[entry.model.ID] = entry
	}
	for _, base := range catalog.models {
		if !base.Capabilities["code_assistant"] {
			continue
		}
		entry := assistantModelEntry(catalog.entries[base.ID])
		if _, exists := catalog.entries[entry.model.ID]; exists {
			continue
		}
		catalog.models = append(catalog.models, entry.model)
		catalog.entries[entry.model.ID] = entry
	}
	return catalog, nil
}

func decodeModelRow(raw json.RawMessage, rowIndex int) (modelEntry, error) {
	path := fmt.Sprintf("$[0][%d]", rowIndex)
	row, err := rawArray(raw, path, raw)
	if err != nil {
		return modelEntry{}, withMethod(err, "ListModels")
	}
	wireName, err := requiredStringField(row, 0, path, raw)
	if err != nil {
		return modelEntry{}, err
	}
	displayName, err := requiredStringField(row, 3, path, raw)
	if err != nil {
		return modelEntry{}, err
	}
	description, err := optionalStringField(row, 4, path, raw)
	if err != nil {
		return modelEntry{}, err
	}
	inputLimit, err := optionalIntField(row, 5, path, raw)
	if err != nil {
		return modelEntry{}, err
	}
	outputLimit, err := optionalIntField(row, 6, path, raw)
	if err != nil {
		return modelEntry{}, err
	}
	methods, err := stringSliceField(row, 7, path, raw)
	if err != nil {
		return modelEntry{}, err
	}
	capabilityCodes, err := intSliceField(row, 64, path, raw)
	if err != nil {
		return modelEntry{}, err
	}
	secondaryCodes, err := intSliceField(row, 74, path, raw)
	if err != nil {
		return modelEntry{}, err
	}
	capabilities := make(map[string]bool)
	for _, code := range capabilityCodes {
		name, known := capabilityNames[code]
		if known {
			capabilities[name] = true
		}
		capabilities["capability_code_"+strconv.FormatInt(code, 10)] = true
	}
	for _, code := range secondaryCodes {
		if name, known := secondaryCapabilityNames[code]; known {
			capabilities[name] = true
		}
		capabilities["secondary_capability_code_"+strconv.FormatInt(code, 10)] = true
	}
	if !isJSONNull(rawAt(row, 78)) {
		// field 79 InteractionConfig 表示模型只经 Interactions API 生成
		capabilities["interactions_api"] = true
	}
	accessModes, err := intSliceField(row, 82, path, raw)
	if err != nil {
		return modelEntry{}, err
	}
	paidCode, err := optionalIntField(row, 77, path, raw)
	if err != nil {
		return modelEntry{}, err
	}
	options, err := decodeCapabilityOptions(row, path, raw)
	if err != nil {
		return modelEntry{}, err
	}
	voices, err := decodeVoiceDetails(rawAt(row, 66), path+"[66]", raw)
	if err != nil {
		return modelEntry{}, err
	}
	liveVoices, err := decodeVoiceDetails(rawAt(row, 65), path+"[65]", raw)
	if err != nil {
		return modelEntry{}, err
	}
	languages, err := decodeLanguages(rawAt(row, 67), path+"[67]", raw)
	if err != nil {
		return modelEntry{}, err
	}
	id := strings.TrimPrefix(wireName, "models/")
	if id == "" {
		return modelEntry{}, &ProtocolEvidenceError{Method: "ListModels", Path: path + "[0]", Detail: "模型 ID 为空", Raw: raw}
	}
	model := Model{
		ID:                id,
		Name:              displayName,
		Description:       description,
		Methods:           append([]string(nil), methods...),
		InputTokenLimit:   inputLimit,
		OutputTokenLimit:  outputLimit,
		Capabilities:      capabilities,
		CapabilityOptions: options,
		VoiceDetails:      append(voices, liveVoices...),
		Languages:         languages,
		AccessModes:       append([]int64(nil), accessModes...),
		Paid:              paidCode == 2,
	}
	defaults, err := decodeGenerationDefaults(row, path, raw, capabilities)
	if err != nil {
		return modelEntry{}, err
	}
	if capabilities["output_continuation"] {
		defaults.ContinuedOutputTokens = inputLimit
	}
	model.Temperature, model.TopP, model.TopK = defaults.Temperature, defaults.TopP, defaults.TopK
	if field := rawAt(row, 27); !isJSONNull(field) {
		value, err := rawFloat64(field, path+"[27]", raw)
		if err != nil {
			return modelEntry{}, withMethod(err, "ListModels")
		}
		model.MaxTemperature = &value
	}
	compatibility, err := decodeToolCompatibility(rawAt(row, 47), path+"[47]", raw)
	if err != nil {
		return modelEntry{}, err
	}
	model.ToolCompatibility = compatibility
	if model.Details, err = decodeModelDetails(rawAt(row, 38), path+"[38]", raw); err != nil {
		return modelEntry{}, err
	}
	if defaults.ThinkingLevel {
		for _, level := range defaults.ThinkingLevels {
			if name, ok := buildThinkingLevels[level]; ok {
				model.ThinkingLevels = append(model.ThinkingLevels, name)
			}
		}
		model.DefaultThinkingLevel = buildThinkingLevels[defaults.DefaultThinkingLevel]
	}
	if defaults.ThinkingBudget {
		if model.ThinkingBudget, err = decodeThinkingBudget(rawAt(row, 71), path+"[71]", raw); err != nil {
			return modelEntry{}, err
		}
	}
	return modelEntry{model: model, defaults: defaults}, nil
}

// decodeThinkingBudget 解析 field 72 的思考预算：field 1 默认值、2 上限、3 下限、4 能否关闭思考
func decodeThinkingBudget(value json.RawMessage, path string, evidence json.RawMessage) (*ThinkingBudget, error) {
	if isJSONNull(value) {
		return nil, nil
	}
	message, err := rawArray(value, path, evidence)
	if err != nil {
		return nil, withMethod(err, "ListModels")
	}
	budget := &ThinkingBudget{}
	for index, target := range []**int64{&budget.Default, &budget.Max, &budget.Min} {
		if field := rawAt(message, index); !isJSONNull(field) {
			number, err := rawInt64(field, fmt.Sprintf("%s[%d]", path, index), evidence)
			if err != nil {
				return nil, withMethod(err, "ListModels")
			}
			*target = &number
		}
	}
	if field := rawAt(message, 3); !isJSONNull(field) {
		flag, err := rawInt64(field, path+"[3]", evidence)
		if err != nil {
			return nil, withMethod(err, "ListModels")
		}
		disable := flag != 0
		budget.CanDisable = &disable
	}
	return budget, nil
}

// toolNames 是 ListModels field 48 工具兼容对中的工具编号
var toolNames = map[int64]string{
	1: "code_execution", 2: "function_declarations", 3: "structured_output", 5: "google_search",
	6: "url_context", 7: "image_search", 8: "google_maps",
}

// decodeToolCompatibility 解析 field 48 的工具兼容对，未知编号写为 tool_<N>
func decodeToolCompatibility(value json.RawMessage, path string, evidence json.RawMessage) ([][2]string, error) {
	if isJSONNull(value) {
		return nil, nil
	}
	message, err := rawArray(value, path, evidence)
	if err != nil {
		return nil, withMethod(err, "ListModels")
	}
	if isJSONNull(rawAt(message, 0)) {
		return nil, nil
	}
	pairs, err := rawArray(rawAt(message, 0), path+"[0]", evidence)
	if err != nil {
		return nil, withMethod(err, "ListModels")
	}
	name := func(code int64) string {
		if known, ok := toolNames[code]; ok {
			return known
		}
		return "tool_" + strconv.FormatInt(code, 10)
	}
	result := make([][2]string, 0, len(pairs))
	for index, pair := range pairs {
		pairPath := fmt.Sprintf("%s[0][%d]", path, index)
		fields, err := rawArray(pair, pairPath, evidence)
		if err != nil {
			return nil, withMethod(err, "ListModels")
		}
		first, err := optionalIntField(fields, 0, pairPath, evidence)
		if err != nil {
			return nil, err
		}
		second, err := optionalIntField(fields, 1, pairPath, evidence)
		if err != nil {
			return nil, err
		}
		result = append(result, [2]string{name(first), name(second)})
	}
	return result, nil
}

func decodeCapabilityOptions(row []json.RawMessage, path string, evidence json.RawMessage) (map[string][]string, error) {
	options := make(map[string][]string)
	aliases, err := decodeAliases(rawAt(row, 56), path+"[56]", evidence)
	if err != nil {
		return nil, err
	}
	appendOption(options, "aliases", aliases)
	voices, err := decodeFirstStrings(rawAt(row, 66), path+"[66]", evidence)
	if err != nil {
		return nil, err
	}
	liveVoices, err := decodeFirstStrings(rawAt(row, 65), path+"[65]", evidence)
	if err != nil {
		return nil, err
	}
	appendOption(options, "voices", append(voices, liveVoices...))
	languages, err := decodeFirstStrings(rawAt(row, 67), path+"[67]", evidence)
	if err != nil {
		return nil, err
	}
	appendOption(options, "languages", languages)
	imageRatios, err := decodeMappedCodes(rawAt(row, 75), path+"[75]", evidence, aspectRatios)
	if err != nil {
		return nil, err
	}
	appendOption(options, "image_aspect_ratios", imageRatios)
	imageSizes, err := decodeMappedCodes(rawAt(row, 76), path+"[76]", evidence, imageResolutions)
	if err != nil {
		return nil, err
	}
	appendOption(options, "image_output_resolutions", imageSizes)
	videoRaw := rawAt(row, 70)
	if !isJSONNull(videoRaw) {
		video, err := rawArray(videoRaw, path+"[70]", evidence)
		if err != nil {
			return nil, withMethod(err, "ListModels")
		}
		ratios, err := decodeMappedCodes(rawAt(video, 4), path+"[70][4]", evidence, aspectRatios)
		if err != nil {
			return nil, err
		}
		appendOption(options, "video_aspect_ratios", ratios)
		durations, err := decodeMappedCodes(rawAt(video, 5), path+"[70][5]", evidence, videoDurations)
		if err != nil {
			return nil, err
		}
		appendOption(options, "video_durations_seconds", durations)
		resolutions, err := decodeMappedCodes(rawAt(video, 9), path+"[70][9]", evidence, videoResolutions)
		if err != nil {
			return nil, err
		}
		selectable, err := decodeMappedCodes(rawAt(video, 7), path+"[70][7]", evidence, map[int64]string{3: "resolution"})
		if err != nil {
			return nil, err
		}
		if len(resolutions) == 0 && len(selectable) > 0 {
			// 视频能力含编号 3 而未列出分辨率时，官网提供 720p、1080p、4k
			resolutions = []string{"720p", "1080p", "4k"}
		}
		appendOption(options, "video_output_resolutions", resolutions)
		inputs, err := decodeMappedCodes(rawAt(video, 7), path+"[70][7]", evidence, videoInputs)
		if err != nil {
			return nil, err
		}
		appendOption(options, "video_inputs", inputs)
		// field 70[10] 为时长滑块的下限、上限、步长与默认值，记为 名称=秒数
		durationRange, err := decodeMappedCodes(rawAt(video, 10), path+"[70][10]", evidence, nil)
		if err != nil {
			return nil, err
		}
		for index, name := range []string{"min", "max", "step", "default"} {
			if index < len(durationRange) {
				durationRange[index] = name + "=" + durationRange[index]
			}
		}
		appendOption(options, "video_duration_range", durationRange)
		frameRates, err := decodeMappedCodes(rawAt(video, 11), path+"[70][11]", evidence, videoFrameRates)
		if err != nil {
			return nil, err
		}
		appendOption(options, "video_frame_rates", frameRates)
	}
	tasks, err := decodeMappedCodes(rawAt(row, 84), path+"[84]", evidence, videoTasks)
	if err != nil {
		return nil, err
	}
	appendOption(options, "video_tasks", tasks)
	if len(options) == 0 {
		return nil, nil
	}
	return options, nil
}

func decodeGenerationDefaults(row []json.RawMessage, path string, evidence json.RawMessage, capabilities map[string]bool) (GenerationDefaults, error) {
	defaults := GenerationDefaults{
		Thinking: capabilities["thinking"], ThinkingBudget: capabilities["thinking_budget"],
		ThinkingLevel: capabilities["thinking_level"], DefaultThinkingLevel: 2,
		ImageRoute: capabilities["image_route"], OutputResolution: capabilities["output_resolution"],
	}
	maxOutput, err := optionalIntField(row, 6, path, evidence)
	if err != nil {
		return GenerationDefaults{}, err
	}
	defaults.MaxOutputTokens = maxOutput
	if raw := rawAt(row, 8); !isJSONNull(raw) {
		value, err := rawFloat64(raw, path+"[8]", evidence)
		if err != nil {
			return GenerationDefaults{}, withMethod(err, "ListModels")
		}
		defaults.Temperature = &value
	}
	if raw := rawAt(row, 9); !isJSONNull(raw) {
		value, err := rawFloat64(raw, path+"[9]", evidence)
		if err != nil {
			return GenerationDefaults{}, withMethod(err, "ListModels")
		}
		defaults.TopP = &value
	}
	if raw := rawAt(row, 10); !isJSONNull(raw) {
		value, err := rawInt64(raw, path+"[10]", evidence)
		if err != nil {
			return GenerationDefaults{}, withMethod(err, "ListModels")
		}
		converted := int(value)
		defaults.TopK = &converted
	}
	if raw := rawAt(row, 78); !isJSONNull(raw) {
		// field 79 InteractionConfig：field 3 为后台任务，field 4 类型 1 为 agent、2 为模型
		interaction, err := rawArray(raw, path+"[78]", evidence)
		if err != nil {
			return GenerationDefaults{}, withMethod(err, "ListModels")
		}
		kind, err := optionalIntField(interaction, 3, path+"[78]", evidence)
		if err != nil {
			return GenerationDefaults{}, err
		}
		background := false
		if value := rawAt(interaction, 2); !isJSONNull(value) {
			background, err = rawBool(value, path+"[78][2]", evidence)
			if err != nil {
				return GenerationDefaults{}, withMethod(err, "ListModels")
			}
		}
		defaults.InteractionStream = kind == 2 && !background
		defaults.InteractionTask = background
	}
	if video := rawAt(row, 70); !isJSONNull(video) {
		items, err := rawArray(video, path+"[70]", evidence)
		if err != nil {
			return GenerationDefaults{}, withMethod(err, "ListModels")
		}
		resolutions, err := decodeMappedCodes(rawAt(items, 9), path+"[70][9]", evidence, videoResolutions)
		if err != nil {
			return GenerationDefaults{}, err
		}
		if len(resolutions) > 0 {
			defaults.VideoResolution = resolutions[0]
		}
	}
	if raw := rawAt(row, 71); !isJSONNull(raw) {
		thinking, err := rawArray(raw, path+"[71]", evidence)
		if err != nil {
			return GenerationDefaults{}, withMethod(err, "ListModels")
		}
		level, err := optionalIntField(thinking, 5, path+"[71]", evidence)
		if err != nil {
			return GenerationDefaults{}, err
		}
		if level > 0 {
			defaults.DefaultThinkingLevel = level
		}
		levels, err := intSliceField(thinking, 6, path+"[71]", evidence)
		if err != nil {
			return GenerationDefaults{}, err
		}
		defaults.ThinkingLevels = levels
	}
	return defaults, nil
}

func (c *Client) Models(ctx context.Context) ([]Model, error) {
	return c.ModelsForAccount(ctx, "")
}

// ModelsForAccount 读取指定账户的实时模型目录
func (c *Client) ModelsForAccount(ctx context.Context, accountID string) ([]Model, error) {
	catalog, err := c.loadModels(ctx, accountID)
	if err != nil {
		return nil, err
	}
	return cloneModels(catalog.models), nil
}

func (c *Client) loadModels(ctx context.Context, accountID string) (modelCatalog, error) {
	response, err := c.do(ctx, "ListModels", accountID, "", []byte("[]"), false)
	if err != nil {
		return modelCatalog{}, err
	}
	defer response.Body.Close()
	catalog, err := parseModelCatalog(response.Body)
	if err != nil {
		return modelCatalog{}, err
	}
	c.catalogMu.Lock()
	c.catalogs[accountID] = catalog
	c.catalogMu.Unlock()
	return catalog, nil
}

func (c *Client) modelEntry(ctx context.Context, accountID string, modelID string) (modelEntry, error) {
	normalized := strings.TrimPrefix(modelID, "models/")
	c.catalogMu.RLock()
	catalog, found := c.catalogs[accountID]
	entry, exists := catalog.lookup(normalized)
	c.catalogMu.RUnlock()
	if found && exists {
		return entry, nil
	}
	catalog, err := c.loadModels(ctx, accountID)
	if err != nil {
		return modelEntry{}, err
	}
	entry, exists = catalog.lookup(normalized)
	if !exists {
		return modelEntry{}, fmt.Errorf("%w: %s", ErrModelNotFound, normalized)
	}
	return entry, nil
}

// cachedModelEntries 返回各账户已载入目录中的模型条目，不发起网络请求
func (c *Client) cachedModelEntries(modelID string) []modelEntry {
	normalized := strings.TrimPrefix(strings.TrimSpace(modelID), "models/")
	c.catalogMu.RLock()
	defer c.catalogMu.RUnlock()
	var entries []modelEntry
	for _, catalog := range c.catalogs {
		if entry, exists := catalog.lookup(normalized); exists {
			entries = append(entries, entry)
		}
	}
	return entries
}

func (catalog modelCatalog) lookup(modelID string) (modelEntry, bool) {
	if entry, exists := catalog.entries[modelID]; exists {
		return entry, true
	}
	for _, model := range catalog.models {
		if !modelMatchesID(model, modelID) {
			continue
		}
		entry, exists := catalog.entries[model.ID]
		return entry, exists
	}
	return modelEntry{}, false
}

func requiredStringField(row []json.RawMessage, index int, path string, evidence json.RawMessage) (string, error) {
	value, err := optionalStringField(row, index, path, evidence)
	if err != nil {
		return "", err
	}
	if value == "" {
		return "", &ProtocolEvidenceError{Method: "ListModels", Path: fmt.Sprintf("%s[%d]", path, index), Detail: "必需字符串为空", Raw: evidence}
	}
	return value, nil
}

func optionalStringField(row []json.RawMessage, index int, path string, evidence json.RawMessage) (string, error) {
	raw := rawAt(row, index)
	if isJSONNull(raw) {
		return "", nil
	}
	value, err := rawString(raw, fmt.Sprintf("%s[%d]", path, index), evidence)
	if err != nil {
		return "", withMethod(err, "ListModels")
	}
	return value, nil
}

func optionalIntField(row []json.RawMessage, index int, path string, evidence json.RawMessage) (int64, error) {
	raw := rawAt(row, index)
	if isJSONNull(raw) {
		return 0, nil
	}
	value, err := rawInt64(raw, fmt.Sprintf("%s[%d]", path, index), evidence)
	if err != nil {
		return 0, withMethod(err, "ListModels")
	}
	return value, nil
}

func stringSliceField(row []json.RawMessage, index int, path string, evidence json.RawMessage) ([]string, error) {
	raw := rawAt(row, index)
	if isJSONNull(raw) {
		return nil, nil
	}
	items, err := rawArray(raw, fmt.Sprintf("%s[%d]", path, index), evidence)
	if err != nil {
		return nil, withMethod(err, "ListModels")
	}
	values := make([]string, 0, len(items))
	for itemIndex, item := range items {
		value, err := rawString(item, fmt.Sprintf("%s[%d][%d]", path, index, itemIndex), evidence)
		if err != nil {
			return nil, withMethod(err, "ListModels")
		}
		values = append(values, value)
	}
	return values, nil
}

func intSliceField(row []json.RawMessage, index int, path string, evidence json.RawMessage) ([]int64, error) {
	raw := rawAt(row, index)
	if isJSONNull(raw) {
		return nil, nil
	}
	items, err := rawArray(raw, fmt.Sprintf("%s[%d]", path, index), evidence)
	if err != nil {
		return nil, withMethod(err, "ListModels")
	}
	values := make([]int64, 0, len(items))
	for itemIndex, item := range items {
		value, err := rawInt64(item, fmt.Sprintf("%s[%d][%d]", path, index, itemIndex), evidence)
		if err != nil {
			return nil, withMethod(err, "ListModels")
		}
		values = append(values, value)
	}
	return values, nil
}

func decodeAliases(raw json.RawMessage, path string, evidence json.RawMessage) ([]string, error) {
	if isJSONNull(raw) {
		return nil, nil
	}
	items, err := rawArray(raw, path, evidence)
	if err != nil {
		return nil, withMethod(err, "ListModels")
	}
	if len(items) > 0 && len(items[0]) > 0 && items[0][0] == '"' {
		name, err := rawString(items[0], path+"[0]", evidence)
		if err != nil {
			return nil, withMethod(err, "ListModels")
		}
		return []string{strings.TrimPrefix(name, "models/")}, nil
	}
	aliases := make([]string, 0, len(items))
	for index, item := range items {
		pair, err := rawArray(item, fmt.Sprintf("%s[%d]", path, index), evidence)
		if err != nil {
			return nil, withMethod(err, "ListModels")
		}
		if len(pair) == 0 {
			continue
		}
		name, err := rawString(pair[0], fmt.Sprintf("%s[%d][0]", path, index), evidence)
		if err != nil {
			return nil, withMethod(err, "ListModels")
		}
		aliases = append(aliases, strings.TrimPrefix(name, "models/"))
	}
	return aliases, nil
}

// decodeVoiceDetails 读取 field 67（TTS）或 field 66（Live）的音色介绍：名称、试听地址、音高、描述列表、语言、口音、性别、特质与用途
func decodeVoiceDetails(raw json.RawMessage, path string, evidence json.RawMessage) ([]VoiceDetail, error) {
	if isJSONNull(raw) {
		return nil, nil
	}
	items, err := rawArray(raw, path, evidence)
	if err != nil {
		return nil, withMethod(err, "ListModels")
	}
	voices := make([]VoiceDetail, 0, len(items))
	for index, item := range items {
		itemPath := fmt.Sprintf("%s[%d]", path, index)
		row, err := rawArray(item, itemPath, evidence)
		if err != nil {
			return nil, withMethod(err, "ListModels")
		}
		text := func(field int) (string, error) {
			value := rawAt(row, field)
			if isJSONNull(value) {
				return "", nil
			}
			result, err := rawString(value, fmt.Sprintf("%s[%d]", itemPath, field), evidence)
			return result, withMethod(err, "ListModels")
		}
		var voice VoiceDetail
		for field, target := range []*string{&voice.Name, &voice.SampleURL, &voice.Pitch} {
			if *target, err = text(field); err != nil {
				return nil, err
			}
		}
		if voice.Name == "" {
			continue
		}
		if descriptions := rawAt(row, 3); !isJSONNull(descriptions) {
			list, err := rawArray(descriptions, itemPath+"[3]", evidence)
			if err != nil {
				return nil, withMethod(err, "ListModels")
			}
			for position, entry := range list {
				description, err := rawString(entry, fmt.Sprintf("%s[3][%d]", itemPath, position), evidence)
				if err != nil {
					return nil, withMethod(err, "ListModels")
				}
				voice.Descriptions = append(voice.Descriptions, description)
			}
		}
		for field, target := range map[int]*string{4: &voice.Locale, 5: &voice.Accent, 6: &voice.Gender, 7: &voice.Trait, 8: &voice.UseCase} {
			if *target, err = text(field); err != nil {
				return nil, err
			}
		}
		voices = append(voices, voice)
	}
	return voices, nil
}

// decodeLanguages 解析 ListModels field 68 的 [语言代码, 显示名] 列表
func decodeLanguages(raw json.RawMessage, path string, evidence json.RawMessage) ([]Language, error) {
	if isJSONNull(raw) {
		return nil, nil
	}
	items, err := rawArray(raw, path, evidence)
	if err != nil {
		return nil, withMethod(err, "ListModels")
	}
	languages := make([]Language, 0, len(items))
	for index, item := range items {
		itemPath := fmt.Sprintf("%s[%d]", path, index)
		row, err := rawArray(item, itemPath, evidence)
		if err != nil {
			return nil, withMethod(err, "ListModels")
		}
		var language Language
		for field, target := range []*string{&language.Code, &language.Name} {
			if value := rawAt(row, field); !isJSONNull(value) {
				if *target, err = rawString(value, fmt.Sprintf("%s[%d]", itemPath, field), evidence); err != nil {
					return nil, withMethod(err, "ListModels")
				}
			}
		}
		if language.Code != "" {
			languages = append(languages, language)
		}
	}
	return languages, nil
}

func decodeFirstStrings(raw json.RawMessage, path string, evidence json.RawMessage) ([]string, error) {
	if isJSONNull(raw) {
		return nil, nil
	}
	items, err := rawArray(raw, path, evidence)
	if err != nil {
		return nil, withMethod(err, "ListModels")
	}
	values := make([]string, 0, len(items))
	for index, item := range items {
		row, err := rawArray(item, fmt.Sprintf("%s[%d]", path, index), evidence)
		if err != nil {
			return nil, withMethod(err, "ListModels")
		}
		if len(row) == 0 || isJSONNull(row[0]) {
			continue
		}
		value, err := rawString(row[0], fmt.Sprintf("%s[%d][0]", path, index), evidence)
		if err != nil {
			return nil, withMethod(err, "ListModels")
		}
		values = append(values, value)
	}
	return values, nil
}

// decodeMappedCodes 把枚举数组按 mapping 换成名称，mapping 为 nil 时保留数值
func decodeMappedCodes(raw json.RawMessage, path string, evidence json.RawMessage, mapping map[int64]string) ([]string, error) {
	if isJSONNull(raw) {
		return nil, nil
	}
	items, err := rawArray(raw, path, evidence)
	if err != nil {
		return nil, withMethod(err, "ListModels")
	}
	values := make([]string, 0, len(items))
	for index, item := range items {
		code, err := rawInt64(item, fmt.Sprintf("%s[%d]", path, index), evidence)
		if err != nil {
			return nil, withMethod(err, "ListModels")
		}
		if mapping == nil {
			values = append(values, strconv.FormatInt(code, 10))
		} else if value, exists := mapping[code]; exists {
			values = append(values, value)
		}
	}
	return values, nil
}

func appendOption(options map[string][]string, key string, values []string) {
	if len(values) > 0 {
		options[key] = append([]string(nil), values...)
	}
}

func cloneModels(models []Model) []Model {
	result := make([]Model, len(models))
	for index, model := range models {
		result[index] = model
		result[index].Methods = append([]string(nil), model.Methods...)
		result[index].AccessModes = append([]int64(nil), model.AccessModes...)
		result[index].ThinkingLevels = append([]string(nil), model.ThinkingLevels...)
		result[index].ToolCompatibility = append([][2]string(nil), model.ToolCompatibility...)
		if model.Capabilities != nil {
			result[index].Capabilities = make(map[string]bool, len(model.Capabilities))
			for key, value := range model.Capabilities {
				result[index].Capabilities[key] = value
			}
		}
		if model.CapabilityOptions != nil {
			result[index].CapabilityOptions = make(map[string][]string, len(model.CapabilityOptions))
			for key, value := range model.CapabilityOptions {
				result[index].CapabilityOptions[key] = append([]string(nil), value...)
			}
		}
	}
	return result
}

func withMethod(err error, method string) error {
	if evidence, ok := err.(*ProtocolEvidenceError); ok {
		copy := *evidence
		copy.Method = method
		return &copy
	}
	return err
}
