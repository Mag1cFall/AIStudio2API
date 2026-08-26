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
	47: "aspect_ratio",
	49: "output_resolution",
	52: "thinking_level",
	53: "music_route",
	54: "image_search",
	58: "google_maps",
	59: "interaction_route",
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
	if len(root) != 1 {
		return modelCatalog{}, &ProtocolEvidenceError{Method: "ListModels", Path: "$", Detail: "根数组字段数不是现场确认的 1", Raw: raw}
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
		AccessModes:       append([]int64(nil), accessModes...),
		Paid:              paidCode == 2,
	}
	defaults, err := decodeGenerationDefaults(row, path, raw, capabilities)
	if err != nil {
		return modelEntry{}, err
	}
	return modelEntry{model: model, defaults: defaults}, nil
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
	appendOption(options, "voices", voices)
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
		appendOption(options, "video_output_resolutions", resolutions)
	}
	if len(options) == 0 {
		return nil, nil
	}
	return options, nil
}

func decodeGenerationDefaults(row []json.RawMessage, path string, evidence json.RawMessage, capabilities map[string]bool) (GenerationDefaults, error) {
	defaults := GenerationDefaults{
		Thinking: capabilities["thinking"], ThinkingBudget: capabilities["thinking_budget"],
		ThinkingLevel: capabilities["thinking_level"], DefaultThinkingLevel: 2,
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
		if value, exists := mapping[code]; exists {
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
