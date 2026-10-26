package api

import (
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Mag1cFall/AIStudio2API/internal/aistudio"
)

func TestGeminiMediaProcessingOutputAndHistory(t *testing.T) {
	events := []aistudio.Event{
		{Kind: aistudio.EventMediaProcessingCall, MediaProcessing: &aistudio.MediaProcessingStep{ID: "video_call"}, ThoughtSignature: "call-signature"},
		{Kind: aistudio.EventMediaProcessingResult, MediaProcessing: &aistudio.MediaProcessingStep{ID: "video_call"}, ThoughtSignature: "result-signature"},
		{Kind: aistudio.EventText, Text: "Video answer"},
	}
	parts := geminiOutputParts(generationResult{events: events})
	if len(parts) != 3 {
		t.Fatalf("output parts = %#v", parts)
	}
	for i, key := range []string{"toolCall", "toolResponse"} {
		tool, ok := parts[i][key].(map[string]any)
		if !ok || tool["toolType"] != "MEDIA_PROCESSING" || tool["id"] != "video_call" {
			t.Fatalf("native processing part = %#v", parts[i])
		}
		if parts[i]["functionCall"] != nil || parts[i]["thoughtSignature"] != events[i].ThoughtSignature {
			t.Fatalf("processing authority/signature = %#v", parts[i])
		}
		stream := geminiStreamCandidate(events[i], 0)
		streamParts := stream["content"].(map[string]any)["parts"].([]any)
		if len(streamParts) != 1 || streamParts[0].(map[string]any)[key] == nil {
			t.Fatalf("stream processing projection = %#v", stream)
		}
	}
	raw, err := json.Marshal(parts)
	if err != nil {
		t.Fatal(err)
	}
	var input []geminiPart
	if err := json.Unmarshal(raw, &input); err != nil {
		t.Fatal(err)
	}
	canonical, hasHostResult, err := mapGeminiParts(input)
	if err != nil || hasHostResult || len(canonical) != 3 {
		t.Fatalf("history mapping = %#v, %v, %v", canonical, hasHostResult, err)
	}
	if canonical[0].ProcessingCall == nil || canonical[1].ProcessingResult == nil || canonical[0].ThoughtSignature != "call-signature" || canonical[1].ThoughtSignature != "result-signature" {
		t.Fatalf("history lost processing trace: %#v", canonical)
	}
	if _, err := aistudio.EncodeCountTokensRequest(aistudio.TokenCountRequest{Model: "m", Contents: []aistudio.Content{{Role: aistudio.RoleAssistant, Parts: canonical}}}); err != nil {
		t.Fatalf("processing history count encoding: %v", err)
	}
}

func TestInteractionProcessingHistoryPreservesSignatures(t *testing.T) {
	contents, err := interactionContents(json.RawMessage(`[
		{"type":"thought","signature":"prior-signature"},
		{"type":"processing_call","id":"video_call","signature":"call-signature"},
		{"type":"processing_result","call_id":"video_call","signature":"result-signature"}
	]`))
	if err != nil {
		t.Fatal(err)
	}
	if len(contents) != 1 || contents[0].Role != aistudio.RoleAssistant || len(contents[0].Parts) != 3 {
		t.Fatalf("processing history = %#v", contents)
	}
	parts := contents[0].Parts
	if parts[0].ThoughtSignature != "prior-signature" || parts[1].ThoughtSignature != "call-signature" || parts[2].ThoughtSignature != "result-signature" || parts[1].ProcessingCall == nil || parts[2].ProcessingResult == nil {
		t.Fatalf("processing signatures were overwritten: %#v", parts)
	}
	for _, raw := range []string{`[{"type":"processing_call"}]`, `[{"type":"processing_result"}]`} {
		if _, err := interactionContents(json.RawMessage(raw)); err == nil {
			t.Fatalf("missing processing ID accepted: %s", raw)
		}
	}
}

func TestStoredInteractionMediaProcessingContinuation(t *testing.T) {
	for _, stream := range []bool{false, true} {
		name := "nonstream"
		if stream {
			name = "stream"
		}
		t.Run(name, func(t *testing.T) {
			call := &aistudio.MediaProcessingStep{ID: "video_call", Name: "inspect", Data: json.RawMessage(`{"start":2}`)}
			reply := &aistudio.MediaProcessingStep{ID: "video_call", Data: json.RawMessage(`{"loaded":true}`)}
			events := []aistudio.Event{
				{Kind: aistudio.EventMediaProcessingCall, MediaProcessing: call, ThoughtSignature: "call-signature"},
				{Kind: aistudio.EventMediaProcessingResult, MediaProcessing: reply, ThoughtSignature: "result-signature"},
				{Kind: aistudio.EventText, Text: "Video answer"},
				{Kind: aistudio.EventFinish, FinishReason: "stop"},
			}
			store := newResponseStateStore()
			s := &server{responseStates: store}
			request := interactionRequest{Model: "video-model", Input: json.RawMessage(`"Inspect the video"`)}
			current := []aistudio.Content{{Role: aistudio.RoleUser, Parts: []aistudio.Part{{Text: "Inspect the video"}}}}
			if stream {
				channel := make(chan aistudio.Event, len(events))
				for _, event := range events {
					channel <- event
				}
				close(channel)
				response := httptest.NewRecorder()
				s.streamInteraction(response, httptest.NewRequest("POST", "/v1beta/interactions", nil), request,
					aistudio.GenerateRequest{ID: "stored-video", Model: request.Model}, false, responseHistory{}, current, "2026-10-28T00:00:00Z", channel)
				if !strings.Contains(response.Body.String(), "interaction.completed") {
					t.Fatalf("stream did not complete: %s", response.Body.String())
				}
			} else {
				s.storeInteraction(request, responseHistory{}, current, generationResult{events: events}, map[string]any{"id": "stored-video"})
			}
			call.ID, reply.ID = "changed-call", "changed-result"
			call.Data[0], reply.Data[0] = '[', '['
			history, ok := store.Load("stored-video")
			if !ok || !history.Interaction || len(history.Contents) != 2 {
				t.Fatalf("stored interaction history = %#v", history)
			}
			parts := history.Contents[1].Parts
			if len(parts) != 3 || parts[0].ProcessingCall == nil || parts[1].ProcessingResult == nil {
				t.Fatalf("stored navigation parts = %#v", parts)
			}
			if parts[0].ProcessingCall.ID != "video_call" || parts[1].ProcessingResult.ID != "video_call" ||
				parts[0].ProcessingCall.Name != "inspect" || string(parts[0].ProcessingCall.Data) != `{"start":2}` ||
				string(parts[1].ProcessingResult.Data) != `{"loaded":true}` || parts[0].ThoughtSignature != "call-signature" || parts[1].ThoughtSignature != "result-signature" {
				t.Fatalf("stored processing identity or payload changed: %#v", parts)
			}
			contents := append(history.Contents, aistudio.Content{Role: aistudio.RoleUser, Parts: []aistudio.Part{{Text: "Continue"}}})
			encoded, err := aistudio.EncodeGenerateContentRequest(aistudio.GenerateRequest{Model: request.Model, Contents: contents}, aistudio.GenerationDefaults{MaxOutputTokens: 64}, aistudio.RequestContext{})
			if err != nil {
				t.Fatal(err)
			}
			var wire []any
			if err := json.Unmarshal(encoded, &wire); err != nil {
				t.Fatal(err)
			}
			modelParts := wire[1].([]any)[1].([]any)[0].([]any)
			for index, field := range []int{25, 26} {
				part := modelParts[index].([]any)
				step := part[field].([]any)
				if step[0] != float64(13) || step[3] != "video_call" || part[14] != parts[index].ThoughtSignature {
					t.Fatalf("continuation navigation wire = %#v", part)
				}
			}
		})
	}
}
