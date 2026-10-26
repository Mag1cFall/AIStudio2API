package api

import (
	"bytes"
	"encoding/json"
	"testing"

	"github.com/Mag1cFall/AIStudio2API/internal/aistudio"
)

func TestGeminiVideoInputFieldMappings(t *testing.T) {
	for _, test := range []struct {
		name string
		json string
	}{
		{
			name: "camel case",
			json: `{"inlineData":{"mimeType":"video/mp4","data":"dmlkZW8="},"videoMetadata":{"startOffset":"0s","endOffset":"3.25s","fps":0.5},"mediaProcessing":"STATIC"}`,
		},
		{
			name: "snake case",
			json: `{"inline_data":{"mime_type":"video/mp4","data":"dmlkZW8="},"video_metadata":{"start_offset":"0s","end_offset":"3.25s","fps":0.5},"media_processing":"STATIC"}`,
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			var part geminiPart
			if err := json.Unmarshal([]byte(test.json), &part); err != nil {
				t.Fatal(err)
			}
			mapped, _, err := mapGeminiParts([]geminiPart{part})
			if err != nil {
				t.Fatal(err)
			}
			if len(mapped) != 1 || mapped[0].VideoMetadata == nil || mapped[0].VideoMetadata.StartOffset != "0s" || mapped[0].VideoMetadata.EndOffset != "3.25s" || mapped[0].VideoMetadata.FPS == nil || *mapped[0].VideoMetadata.FPS != 0.5 {
				t.Fatalf("mapped video metadata = %#v", mapped)
			}
			if mapped[0].MediaProcessing != "STATIC" {
				t.Fatalf("media processing = %q, want STATIC", mapped[0].MediaProcessing)
			}
		})
	}
}

func TestInteractionVideoInputProcessing(t *testing.T) {
	tests := []struct {
		name       string
		processing string
		wantMode   string
		wantStart  string
		wantEnd    string
		wantFPS    float64
	}{
		{name: "static string", processing: `"static"`, wantMode: "STATIC"},
		{name: "agentic string", processing: `"agentic"`, wantMode: "AGENTIC"},
		{name: "static options", processing: `{"type":"static","start_offset":2,"end_offset":4,"fps":0.5}`, wantMode: "STATIC", wantStart: "2s", wantEnd: "4s", wantFPS: 0.5},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			raw := `{"type":"video","data":"dmlkZW8=","mime_type":"video/mp4","processing":` + test.processing + `}`
			var input interactionInput
			if err := json.Unmarshal([]byte(raw), &input); err != nil {
				t.Fatal(err)
			}
			parts, err := input.parts()
			if err != nil {
				t.Fatal(err)
			}
			if len(parts) != 1 || parts[0].MediaProcessing != test.wantMode {
				t.Fatalf("interaction video parts = %#v", parts)
			}
			if test.wantStart == "" {
				if parts[0].VideoMetadata != nil {
					t.Fatalf("unexpected video metadata: %#v", parts[0].VideoMetadata)
				}
				return
			}
			metadata := parts[0].VideoMetadata
			if metadata == nil || metadata.StartOffset != test.wantStart || metadata.EndOffset != test.wantEnd || metadata.FPS == nil || *metadata.FPS != test.wantFPS {
				t.Fatalf("interaction video metadata = %#v", metadata)
			}
		})
	}
}

func TestInteractionVideoInputRejectsInvalidProcessing(t *testing.T) {
	for _, test := range []struct {
		processing  string
		mapperFails bool
	}{
		{processing: `"unknown"`, mapperFails: true},
		{processing: `{"type":"static","start_offset":{}}`, mapperFails: true},
		{processing: `{"type":"static","startOffset":2}`, mapperFails: true},
		{processing: `{"type":"static","start_offset":"1m"}`},
		{processing: `{"type":"static","start_offset":2,"end_offset":2}`},
		{processing: `{"type":"static","fps":0}`},
		{processing: `{"type":"static","fps":25}`},
		{processing: `{"type":"agentic","fps":1}`, mapperFails: true},
	} {
		raw := `{"type":"video","data":"dmlkZW8=","mime_type":"video/mp4","processing":` + test.processing + `}`
		var input interactionInput
		if err := json.Unmarshal([]byte(raw), &input); err != nil {
			t.Fatal(err)
		}
		parts, err := input.parts()
		if test.mapperFails {
			if err == nil {
				t.Errorf("processing %s unexpectedly accepted by mapper", test.processing)
			}
			continue
		}
		if err != nil {
			t.Fatalf("valid processing shape %s did not map: %v", test.processing, err)
		}
		request := aistudio.GenerateRequest{Model: "m", Contents: []aistudio.Content{{Role: aistudio.RoleUser, Parts: parts}}}
		if _, err := aistudio.EncodeGenerateContentRequest(request, aistudio.GenerationDefaults{MaxOutputTokens: 1024}, aistudio.RequestContext{}); err == nil {
			t.Errorf("processing %s unexpectedly passed canonical wire validation", test.processing)
		}
	}
}

func TestInteractionNumericVideoOffsets(t *testing.T) {
	for _, test := range []struct {
		input string
		want  string
		wire  string
	}{
		{input: "1e-3", want: "0.001s", wire: `[0,1000000]`},
		{input: "2e0", want: "2s", wire: `[2]`},
		{input: "1e-9", want: "0.000000001s", wire: `[0,1]`},
		{input: "100e-10", want: "0.00000001s", wire: `[0,10]`},
		{input: "1.25E+1", want: "12.5s", wire: `[12,500000000]`},
		{input: "9007199254740993e0", want: "9007199254740993s", wire: `[9007199254740993]`},
		{input: "9223372036854775807e0", want: "9223372036854775807s", wire: `[9223372036854775807]`},
		{input: "0e999999999", want: "0s", wire: `[0]`},
	} {
		t.Run(test.input, func(t *testing.T) {
			var input interactionInput
			raw := `{"type":"video","data":"dmlkZW8=","mime_type":"video/mp4","processing":{"type":"static","start_offset":` + test.input + `}}`
			if err := json.Unmarshal([]byte(raw), &input); err != nil {
				t.Fatal(err)
			}
			parts, err := input.parts()
			if err != nil {
				t.Fatal(err)
			}
			if got := parts[0].VideoMetadata.StartOffset; got != test.want {
				t.Fatalf("offset = %q, want %q", got, test.want)
			}
			request := aistudio.GenerateRequest{Model: "m", Contents: []aistudio.Content{{Role: aistudio.RoleUser, Parts: parts}}}
			encoded, err := aistudio.EncodeGenerateContentRequest(request, aistudio.GenerationDefaults{MaxOutputTokens: 64}, aistudio.RequestContext{})
			if err != nil {
				t.Fatal(err)
			}
			var wire []any
			decoder := json.NewDecoder(bytes.NewReader(encoded))
			decoder.UseNumber()
			if err := decoder.Decode(&wire); err != nil {
				t.Fatal(err)
			}
			part := wire[1].([]any)[0].([]any)[0].([]any)[0].([]any)
			duration, err := json.Marshal(part[15].([]any)[0])
			if err != nil || string(duration) != test.wire {
				t.Fatalf("offset wire = %s, want %s: %v", duration, test.wire, err)
			}
		})
	}
}

func TestInteractionNumericVideoOffsetLimits(t *testing.T) {
	for _, value := range []string{"-1e-3", "1e-10", "1.0000000001", "9223372036854775808", "1e1000000000", "1e-1000000000"} {
		if _, err := interactionVideoOffset(json.RawMessage(value)); err == nil {
			t.Errorf("unrepresentable numeric offset %s accepted", value)
		}
	}
}
