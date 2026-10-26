package aistudio

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestMediaProcessingTraceRoundTrip(t *testing.T) {
	for _, result := range []bool{false, true} {
		part := Part{ThoughtSignature: "processing-signature"}
		step := &MediaProcessingStep{ID: "media_call_1"}
		index, kind := 25, EventMediaProcessingCall
		if result {
			part.ProcessingResult, index, kind = step, 26, EventMediaProcessingResult
		} else {
			part.ProcessingCall = step
		}
		wire, err := encodePart(part)
		if err != nil {
			t.Fatal(err)
		}
		if !jsonValueEqual(t, wire[index], []any{int64(13), nil, nil, "media_call_1"}) {
			t.Fatalf("media processing wire = %#v", wire[index])
		}
		raw, err := json.Marshal(wire)
		if err != nil {
			t.Fatal(err)
		}
		events, err := NewFrameDecoder().decodePart(raw, "$part", raw)
		if err != nil {
			t.Fatal(err)
		}
		if len(events) != 1 || events[0].Kind != kind || events[0].MediaProcessing == nil || events[0].MediaProcessing.ID != step.ID || events[0].ThoughtSignature != part.ThoughtSignature {
			t.Fatalf("processing events = %#v", events)
		}
		if events[0].ToolCall != nil {
			t.Fatal("server processing became a host function call")
		}
		build, err := encodeBuildPart(part)
		if err != nil {
			t.Fatal(err)
		}
		field := "toolCall"
		if result {
			field = "toolResponse"
		}
		value, ok := build[field].(map[string]any)
		if !ok || value["toolType"] != "MEDIA_PROCESSING" || value["id"] != step.ID {
			t.Fatalf("Build processing projection = %#v", build)
		}
	}
}

func TestBuildMediaProcessingTrace(t *testing.T) {
	const raw = `{"candidates":[{"content":{"role":"model","parts":[
		{"toolCall":{"toolType":"MEDIA_PROCESSING","id":"video_call","toolName":"inspect","args":{"start_offset":2}},"thoughtSignature":"call-signature"},
		{"toolResponse":{"toolType":"MEDIA_PROCESSING","id":"video_call","response":{"loaded":true}},"thoughtSignature":"result-signature"},
		{"text":"Video answer"}
	]},"finishReason":"STOP"}],"usageMetadata":{"promptTokenCount":10,"candidatesTokenCount":2,"totalTokenCount":12}}`
	decoder := &BuildStreamDecoder{}
	events, err := decoder.Decode([]byte(raw))
	if err != nil {
		t.Fatal(err)
	}
	if err := decoder.End(); err != nil {
		t.Fatal(err)
	}
	if len(events) != 5 || events[0].Kind != EventMediaProcessingCall || events[1].Kind != EventMediaProcessingResult || events[2].Kind != EventText || events[3].Kind != EventUsage || events[4].Kind != EventFinish {
		t.Fatalf("Build event order = %#v", events)
	}
	for index, signature := range []string{"call-signature", "result-signature"} {
		if events[index].MediaProcessing == nil || events[index].MediaProcessing.ID != "video_call" || events[index].ThoughtSignature != signature || events[index].ToolCall != nil {
			t.Fatalf("Build server processing identity/authority = %#v", events[index])
		}
	}
	if events[0].MediaProcessing.Name != "inspect" || !strings.Contains(string(events[0].MediaProcessing.Data), "start_offset") {
		t.Fatalf("Build processing arguments = %#v", events[0].MediaProcessing)
	}
}

func TestBuildMediaProcessingMalformedPart(t *testing.T) {
	for _, part := range []string{
		`{"toolCall":{"toolType":"MEDIA_PROCESSING","args":[]}}`,
		`{"text":"text","toolCall":{"toolType":"MEDIA_PROCESSING"}}`,
		`{"toolCall":{"toolType":"MEDIA_PROCESSING"},"toolResponse":{"toolType":"MEDIA_PROCESSING"}}`,
	} {
		var parsed buildResponsePart
		if err := json.Unmarshal([]byte(part), &parsed); err != nil {
			t.Fatal(err)
		}
		if _, err := decodeBuildPart(parsed, 0, []byte(part)); err == nil {
			t.Fatalf("malformed Build processing part accepted: %s", part)
		}
	}
}

func TestMediaProcessingPayloadAndMalformedWire(t *testing.T) {
	step := &MediaProcessingStep{ID: "media_call_2", Name: "inspect", Data: json.RawMessage(`{"start":2,"end":4}`)}
	call, err := encodePart(Part{ProcessingCall: step})
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(call)
	events, err := NewFrameDecoder().decodePart(raw, "$part", raw)
	if err != nil || len(events) != 1 {
		t.Fatalf("decode = %#v, %v", events, err)
	}
	got := events[0].MediaProcessing
	var values map[string]int
	if err := json.Unmarshal(got.Data, &values); err != nil {
		t.Fatal(err)
	}
	if got.Name != "inspect" || len(values) != 2 || values["start"] != 2 || values["end"] != 4 {
		t.Fatalf("processing payload = %#v", got)
	}
	call[25] = []any{int64(13), nil, nil, true}
	raw, _ = json.Marshal(call)
	if _, err := NewFrameDecoder().decodePart(raw, "$part", raw); err == nil {
		t.Fatal("malformed processing ID accepted")
	}
	if _, err := encodePart(Part{Text: "text", ProcessingCall: step}); err == nil {
		t.Fatal("multiple part variants accepted")
	}
}
