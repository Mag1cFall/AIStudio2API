package api

import (
	"encoding/json"
	"fmt"
	"strings"
)

// interactionVideoProcessing 把 video 内容的 processing 映射为截取采样设置与处理方式，取值为 static、agentic 或 static 设置对象
func interactionVideoProcessing(raw json.RawMessage) (*geminiVideoMetadata, string, error) {
	var mode string
	if json.Unmarshal(raw, &mode) == nil {
		return nil, strings.ToUpper(mode), nil
	}
	var config struct {
		Type        string   `json:"type"`
		StartOffset string   `json:"start_offset"`
		EndOffset   string   `json:"end_offset"`
		FPS         *float64 `json:"fps"`
	}
	if err := json.Unmarshal(raw, &config); err != nil {
		return nil, "", fmt.Errorf("video processing: %w", err)
	}
	return &geminiVideoMetadata{StartOffset: config.StartOffset, EndOffset: config.EndOffset, FPS: config.FPS}, strings.ToUpper(config.Type), nil
}
