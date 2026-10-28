package api

import (
	"encoding/json"
	"testing"

	"github.com/Mag1cFall/AIStudio2API/internal/aistudio"
)

// TestGeminiVideoProcessingRoundTrip 保留视频截取、采样与处理方式，服务端处理步骤按 Gemini 形状输出后可作为下一轮历史读回
func TestGeminiVideoProcessingRoundTrip(t *testing.T) {
	var request geminiRequest
	if err := json.Unmarshal([]byte(`{"contents":[{"role":"user","parts":[
		{"fileData":{"mimeType":"video/*","fileUri":"https://www.youtube.com/watch?v=rOVtjJkqtiA"},"videoMetadata":{"startOffset":"2s","endOffset":"4.5s","fps":0.5},"mediaProcessing":"STATIC"},
		{"text":"describe"}]}]}`), &request); err != nil {
		t.Fatal(err)
	}
	generate, err := request.toGenerateRequest("id", "m")
	if err != nil {
		t.Fatal(err)
	}
	video := generate.Contents[0].Parts[0]
	if video.ExternalMedia == nil || video.MediaProcessing != "STATIC" || video.VideoMetadata == nil ||
		video.VideoMetadata.StartOffset != "2s" || video.VideoMetadata.EndOffset != "4.5s" || video.VideoMetadata.FPS == nil || *video.VideoMetadata.FPS != 0.5 {
		t.Fatalf("video part = %#v", video)
	}

	events := []aistudio.Event{
		{Kind: aistudio.EventServerToolCall, ToolCall: &aistudio.ServerToolStep{ID: "call_1"}, ThoughtSignature: "call-signature"},
		{Kind: aistudio.EventServerToolResponse, ToolResponse: &aistudio.ServerToolStep{ID: "call_1"}, ThoughtSignature: "result-signature"},
		{Kind: aistudio.EventText, Text: "answer"},
	}
	output, err := json.Marshal(map[string]any{"contents": []any{map[string]any{"role": "model", "parts": geminiOutputParts(generationResult{events: events})}}})
	if err != nil {
		t.Fatal(err)
	}
	var history geminiRequest
	if err := json.Unmarshal(output, &history); err != nil {
		t.Fatal(err)
	}
	replayed, err := history.toGenerateRequest("id", "m")
	if err != nil {
		t.Fatal(err)
	}
	parts := replayed.Contents[0].Parts
	if len(parts) != 3 || parts[0].ToolCall == nil || parts[0].ToolCall.ID != "call_1" || parts[0].ThoughtSignature != "call-signature" ||
		parts[1].ToolResponse == nil || parts[1].ToolResponse.ID != "call_1" || parts[1].ThoughtSignature != "result-signature" || parts[2].Text != "answer" {
		t.Fatalf("history parts = %s", output)
	}
}
