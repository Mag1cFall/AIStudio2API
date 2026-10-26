package api

import (
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Mag1cFall/AIStudio2API/internal/aistudio"
)

func TestInteractionStepsProjectMediaProcessing(t *testing.T) {
	result := generationResult{events: []aistudio.Event{
		{Kind: aistudio.EventMediaProcessingCall, MediaProcessing: &aistudio.MediaProcessingStep{ID: "video_call_1"}, ThoughtSignature: "call-signature"},
		{Kind: aistudio.EventMediaProcessingResult, MediaProcessing: &aistudio.MediaProcessingStep{ID: "video_call_1"}, ThoughtSignature: "result-signature"},
	}}

	steps, err := interactionSteps(result, interactionAudioFormat{}, true)
	if err != nil {
		t.Fatal(err)
	}
	if len(steps) != 2 {
		t.Fatalf("steps = %#v, want one processing call and result", steps)
	}
	if got := steps[0]; got["type"] != "processing_call" || got["id"] != "video_call_1" || got["signature"] != "call-signature" {
		t.Errorf("processing call = %#v", got)
	}
	if got := steps[1]; got["type"] != "processing_result" || got["call_id"] != "video_call_1" || got["signature"] != "result-signature" {
		t.Errorf("processing result = %#v", got)
	}
	for _, step := range steps {
		if step["type"] == "function_call" || step["type"] == "model_output" {
			t.Errorf("server processing step was projected as host/model output: %#v", step)
		}
	}
}

func TestStreamInteractionMediaProcessingProtocol(t *testing.T) {
	store := false
	request := interactionRequest{Store: &store}
	generate := aistudio.GenerateRequest{ID: "interaction_1", Model: "model"}
	events := make(chan aistudio.Event, 3)
	events <- aistudio.Event{Kind: aistudio.EventMediaProcessingCall, MediaProcessing: &aistudio.MediaProcessingStep{ID: "video_call_1"}, ThoughtSignature: "call-signature"}
	events <- aistudio.Event{Kind: aistudio.EventMediaProcessingResult, MediaProcessing: &aistudio.MediaProcessingStep{ID: "video_call_1"}, ThoughtSignature: "result-signature"}
	events <- aistudio.Event{Kind: aistudio.EventFinish, FinishReason: "STOP"}
	close(events)

	response := httptest.NewRecorder()
	s := &server{}
	s.streamInteraction(response, httptest.NewRequest("POST", "/v1/interactions", nil), request, generate, false, responseHistory{}, nil, "2026-10-26T00:00:00Z", events)

	type frame struct {
		event string
		data  map[string]any
	}
	var frames []frame
	for _, block := range strings.Split(strings.TrimSpace(response.Body.String()), "\n\n") {
		if block == "" {
			continue
		}
		var current frame
		for _, line := range strings.Split(block, "\n") {
			if strings.HasPrefix(line, "event: ") {
				current.event = strings.TrimPrefix(line, "event: ")
			}
			if strings.HasPrefix(line, "data: ") {
				if err := json.Unmarshal([]byte(strings.TrimPrefix(line, "data: ")), &current.data); err != nil {
					t.Fatalf("decode stream frame: %v", err)
				}
			}
		}
		frames = append(frames, current)
	}
	wantEvents := []string{"interaction.created", "step.start", "step.delta", "step.stop", "step.start", "step.delta", "step.stop", "interaction.completed"}
	if len(frames) != len(wantEvents) {
		t.Fatalf("stream frames = %#v", frames)
	}
	for i, want := range wantEvents {
		if frames[i].event != want {
			t.Errorf("frame %d event = %q, want %q", i, frames[i].event, want)
		}
	}
	assertFrameValue := func(index int, field string, want any) {
		t.Helper()
		if got := frames[index].data[field]; got != want {
			t.Errorf("frame %d %s = %#v, want %#v", index, field, got, want)
		}
	}
	for _, test := range []struct {
		startIndex int
		deltaIndex int
		kind       string
		idField    string
		signature  string
	}{
		{startIndex: 1, deltaIndex: 2, kind: "processing_call", idField: "id", signature: "call-signature"},
		{startIndex: 4, deltaIndex: 5, kind: "processing_result", idField: "call_id", signature: "result-signature"},
	} {
		start, _ := frames[test.startIndex].data["step"].(map[string]any)
		delta, _ := frames[test.deltaIndex].data["delta"].(map[string]any)
		if start["type"] != test.kind || start[test.idField] != "video_call_1" {
			t.Errorf("%s start step = %#v", test.kind, start)
		}
		if _, ok := start["signature"]; ok {
			t.Errorf("%s signature appeared before its delta: %#v", test.kind, start)
		}
		if delta["type"] != test.kind || delta["signature"] != test.signature {
			t.Errorf("%s delta = %#v", test.kind, delta)
		}
		assertFrameValue(test.startIndex, "index", float64(test.startIndex/3))
		assertFrameValue(test.deltaIndex, "index", float64(test.startIndex/3))
		assertFrameValue(test.startIndex+2, "index", float64(test.startIndex/3))
	}
	if strings.Contains(response.Body.String(), `"type":"function_call"`) {
		t.Fatal("server media processing emitted a host function call")
	}
}
