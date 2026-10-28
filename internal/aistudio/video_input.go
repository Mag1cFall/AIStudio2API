package aistudio

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

// videoOffsetPattern 匹配秒数后带 s 的 Duration 字符串，小数最多九位
var videoOffsetPattern = regexp.MustCompile(`^([0-9]+)(?:\.([0-9]{1,9}))?s$`)

// mediaProcessingCodes 是 Part field 40 视频处理方式的枚举值
var mediaProcessingCodes = map[string]int64{"MEDIA_PROCESSING_UNSPECIFIED": 0, "STATIC": 1, "AGENTIC": 2}

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

// validateVideoParts 在取得账户前校验视频截取的 Duration 格式、终点晚于起点与处理方式取值
func validateVideoParts(contents []Content) error {
	for _, content := range contents {
		for _, part := range content.Parts {
			if _, ok := mediaProcessingCodes[part.MediaProcessing]; part.MediaProcessing != "" && !ok {
				return fmt.Errorf("未知 mediaProcessing %q", part.MediaProcessing)
			}
			video := part.VideoMetadata
			if video == nil {
				continue
			}
			var offsets [2][2]int64
			for index, value := range []string{video.StartOffset, video.EndOffset} {
				if value == "" {
					continue
				}
				seconds, nanos, err := videoOffset(value)
				if err != nil {
					return err
				}
				offsets[index] = [2]int64{seconds, nanos}
			}
			start, end := offsets[0], offsets[1]
			if video.StartOffset != "" && video.EndOffset != "" && (end[0] < start[0] || end[0] == start[0] && end[1] <= start[1]) {
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
