package api

import (
	"bytes"
	"encoding/json"
	"fmt"
	"math/big"
	"strings"

	"github.com/Mag1cFall/AIStudio2API/internal/aistudio"
)

// canonical maps only the server-owned media processing tool, never a host function.
func (value geminiProcessingStep) canonical(call bool) (*aistudio.MediaProcessingStep, error) {
	typeName := value.ToolType
	if typeName == "" {
		typeName = value.ToolTypeSnake
	}
	if typeName != "MEDIA_PROCESSING" {
		return nil, fmt.Errorf("unsupported server tool type %q", typeName)
	}
	name := value.ToolName
	if name == "" {
		name = value.ToolNameSnake
	}
	data := value.Response
	if call {
		data = value.Args
	}
	return &aistudio.MediaProcessingStep{ID: value.ID, Name: name, Data: data}, nil
}

// geminiProcessingPart preserves native video navigation calls and their results.
func geminiProcessingPart(event aistudio.Event) map[string]any {
	step := event.MediaProcessing
	if step == nil {
		return nil
	}
	value := map[string]any{"toolType": "MEDIA_PROCESSING"}
	if step.ID != "" {
		value["id"] = step.ID
	}
	key, payload := "toolResponse", "response"
	if event.Kind == aistudio.EventMediaProcessingCall {
		key, payload = "toolCall", "args"
		if step.Name != "" {
			value["toolName"] = step.Name
		}
	}
	if len(step.Data) != 0 {
		value[payload] = step.Data
	}
	return map[string]any{key: value}
}

// interactionVideoProcessing maps the documented string or static object union.
func interactionVideoProcessing(raw json.RawMessage) (*geminiVideoMetadata, string, error) {
	var mode string
	if json.Unmarshal(raw, &mode) == nil {
		switch mode {
		case "static":
			return nil, "STATIC", nil
		case "agentic":
			return nil, "AGENTIC", nil
		default:
			return nil, "", fmt.Errorf("unknown video processing mode %q", mode)
		}
	}
	var config struct {
		Type  string          `json:"type"`
		Start json.RawMessage `json:"start_offset"`
		End   json.RawMessage `json:"end_offset"`
		FPS   *float64        `json:"fps"`
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&config); err != nil {
		return nil, "", fmt.Errorf("video processing: %w", err)
	}
	if config.Type != "static" {
		return nil, "", fmt.Errorf("video processing object type must be static")
	}
	start, err := interactionVideoOffset(config.Start)
	if err != nil {
		return nil, "", fmt.Errorf("video start_offset: %w", err)
	}
	end, err := interactionVideoOffset(config.End)
	if err != nil {
		return nil, "", fmt.Errorf("video end_offset: %w", err)
	}
	return &geminiVideoMetadata{StartOffset: start, EndOffset: end, FPS: config.FPS}, "STATIC", nil
}

// interactionVideoOffset accepts documented duration strings and numeric seconds.
func interactionVideoOffset(raw json.RawMessage) (string, error) {
	if !rawJSONConfigured(raw) {
		return "", nil
	}
	var value string
	if json.Unmarshal(raw, &value) == nil {
		return value, nil
	}
	var seconds json.Number
	if err := json.Unmarshal(raw, &seconds); err != nil {
		return "", fmt.Errorf("expected a duration string or numeric seconds")
	}
	offset, ok := new(big.Rat).SetString(seconds.String())
	if !ok || offset.Sign() < 0 {
		return "", fmt.Errorf("video offset must be nonnegative numeric seconds")
	}
	offset.Mul(offset, big.NewRat(1_000_000_000, 1))
	if !offset.IsInt() {
		return "", fmt.Errorf("video offset exceeds nanosecond precision")
	}
	whole, nanos := new(big.Int), new(big.Int)
	whole.QuoRem(offset.Num(), big.NewInt(1_000_000_000), nanos)
	if !whole.IsInt64() {
		return "", fmt.Errorf("video offset seconds exceed int64 range")
	}
	if nanos.Sign() == 0 {
		return whole.String() + "s", nil
	}
	fraction := strings.TrimRight(fmt.Sprintf("%09d", nanos.Int64()), "0")
	return whole.String() + "." + fraction + "s", nil
}
