package aistudio

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

// videoOffsetPattern 匹配秒数后带 s 的 Duration 字符串，小数最多九位
var videoOffsetPattern = regexp.MustCompile(`^([0-9]+)(?:\.([0-9]{1,9}))?s$`)

// mediaProcessingCodes 是 Part field 40 视频处理方式的枚举值
var mediaProcessingCodes = map[string]int64{"MEDIA_PROCESSING_UNSPECIFIED": 0, "STATIC": 1, "AGENTIC": 2}

// mediaProcessingToolType 是 ToolCall 与 ToolResponse field 1 中视频理解处理的工具类型
const mediaProcessingToolType = 13

// videoOffset 把 Duration 字符串解析为秒与纳秒
func videoOffset(value string) (int64, int64, error) {
	match := videoOffsetPattern.FindStringSubmatch(value)
	if match == nil {
		return 0, 0, fmt.Errorf("video offset %q 必须是秒数加 s 的 Duration，例如 \"2.5s\"", value)
	}
	seconds, err := strconv.ParseInt(match[1], 10, 64)
	if err != nil {
		return 0, 0, fmt.Errorf("video offset %q: %w", value, err)
	}
	var nanos int64
	if match[2] != "" {
		nanos, _ = strconv.ParseInt(match[2]+strings.Repeat("0", 9-len(match[2])), 10, 64)
	}
	return seconds, nanos, nil
}

// videoOffsetWire 把 Duration 字符串编码为 google.protobuf.Duration 的 [seconds, nanos]
func videoOffsetWire(value string) ([]any, error) {
	seconds, nanos, err := videoOffset(value)
	if err != nil {
		return nil, err
	}
	if nanos == 0 {
		return []any{seconds}, nil
	}
	return []any{seconds, nanos}, nil
}

// validateVideoOffsets 校验视频截取同时设置起止时终点晚于起点
func validateVideoOffsets(contents []Content) error {
	for _, content := range contents {
		for _, part := range content.Parts {
			video := part.VideoMetadata
			if video == nil || video.StartOffset == "" || video.EndOffset == "" {
				continue
			}
			startSeconds, startNanos, err := videoOffset(video.StartOffset)
			if err != nil {
				return err
			}
			endSeconds, endNanos, err := videoOffset(video.EndOffset)
			if err != nil {
				return err
			}
			if endSeconds < startSeconds || endSeconds == startSeconds && endNanos <= startNanos {
				return fmt.Errorf("video endOffset %s 必须晚于 startOffset %s", video.EndOffset, video.StartOffset)
			}
		}
	}
	return nil
}

// encodeVideoPart 为媒体 Part 写入 field 16 视频元数据与 field 40 处理方式，再写入思考签名
func encodeVideoPart(wire []any, part Part) ([]any, error) {
	if video := part.VideoMetadata; video != nil {
		var metadata []any
		for index, offset := range []string{video.StartOffset, video.EndOffset} {
			if offset == "" {
				continue
			}
			value, err := videoOffsetWire(offset)
			if err != nil {
				return nil, err
			}
			metadata = setWireField(metadata, index, value)
		}
		if video.FPS != nil {
			metadata = setWireField(metadata, 2, *video.FPS)
		}
		if metadata != nil {
			wire = setWireField(wire, 15, metadata)
		}
	}
	if part.MediaProcessing != "" {
		code, ok := mediaProcessingCodes[part.MediaProcessing]
		if !ok {
			return nil, fmt.Errorf("未知 mediaProcessing %q", part.MediaProcessing)
		}
		if code != 0 {
			wire = setWireField(wire, 39, code)
		}
	}
	return setPartThoughtSignature(wire, part.ThoughtSignature), nil
}

// encodeMediaProcessingStep 编码 ToolCall 或 ToolResponse：工具类型、名称、Struct 与调用 ID
func encodeMediaProcessingStep(step *MediaProcessingStep) ([]any, error) {
	wire := []any{int64(mediaProcessingToolType), nil, nil, step.ID}
	if step.Name != "" {
		wire[1] = step.Name
	}
	if len(step.Data) != 0 {
		value, err := encodeWireStructJSON(step.Data)
		if err != nil {
			return nil, fmt.Errorf("media processing data: %w", err)
		}
		wire[2] = value
	}
	return wire, nil
}

// decodeMediaProcessingStep 解码视频理解处理类型的 ToolCall 或 ToolResponse，其他工具类型返回 nil
func decodeMediaProcessingStep(raw json.RawMessage, path string, evidence json.RawMessage) (*MediaProcessingStep, error) {
	fields, err := rawArray(raw, path, evidence)
	if err != nil {
		return nil, withMethod(err, "GenerateContent")
	}
	toolType, err := rawInt64(rawAt(fields, 0), path+"[0]", evidence)
	if err != nil {
		return nil, withMethod(err, "GenerateContent")
	}
	if toolType != mediaProcessingToolType {
		return nil, nil
	}
	step := &MediaProcessingStep{}
	if value := rawAt(fields, 1); !isJSONNull(value) {
		if step.Name, err = rawString(value, path+"[1]", evidence); err != nil {
			return nil, withMethod(err, "GenerateContent")
		}
	}
	if value := rawAt(fields, 2); !isJSONNull(value) {
		if step.Data, err = decodeWireStruct(value, path+"[2]", evidence); err != nil {
			return nil, err
		}
	}
	if value := rawAt(fields, 3); !isJSONNull(value) {
		if step.ID, err = rawString(value, path+"[3]", evidence); err != nil {
			return nil, withMethod(err, "GenerateContent")
		}
	}
	return step, nil
}
