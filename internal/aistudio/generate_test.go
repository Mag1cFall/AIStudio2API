package aistudio

import (
	"encoding/json"
	"reflect"
	"testing"
)

func TestSpeechRequestMatchesOfficialWire(t *testing.T) {
	temperature := 1.0
	topP := 0.95
	topK := 64
	request := GenerateRequest{
		Model: "gemini-3.1-flash-tts-preview",
		Contents: []Content{{
			Role:  RoleUser,
			Parts: []Part{{Text: "Hello"}},
		}},
		Config: GenerationConfig{
			ResponseModalities: []ResponseModality{ResponseModalityAudio},
			SpeechConfig:       &SpeechConfig{VoiceName: "Zephyr"},
		},
	}
	model := Model{Capabilities: map[string]bool{"speech_route": true}}
	request.Contents = applySpeechTranscript(request.Contents, model, request.Config)

	encoded, err := EncodeGenerateContentRequest(request, GenerationDefaults{
		MaxOutputTokens: 16384,
		Temperature:     &temperature,
		TopP:            &topP,
		TopK:            &topK,
	}, RequestContext{})
	if err != nil {
		t.Fatal(err)
	}
	var wire []any
	if err := json.Unmarshal(encoded, &wire); err != nil {
		t.Fatal(err)
	}
	config := wire[3].([]any)
	if config[3] != nil {
		t.Fatalf("audio request encoded max output tokens: %v", config[3])
	}
	if !reflect.DeepEqual(config[14], []any{float64(3)}) {
		t.Fatalf("audio modalities = %#v", config[14])
	}
	expectedSpeech := []any{[]any{[]any{"Zephyr"}}}
	if !reflect.DeepEqual(config[15], expectedSpeech) {
		t.Fatalf("speech config = %#v", config[15])
	}
	expectedContents := []any{
		[]any{
			[]any{[]any{nil, "## Transcript:\nHello"}},
			"user",
		},
	}
	if !reflect.DeepEqual(wire[1], expectedContents) {
		t.Fatalf("speech contents = %#v", wire[1])
	}
}

func TestAudioWithoutSpeechConfigKeepsDefaultMaxOutput(t *testing.T) {
	request := GenerateRequest{
		Model: "audio-model",
		Contents: []Content{{
			Role:  RoleUser,
			Parts: []Part{{Text: "Generate audio"}},
		}},
		Config: GenerationConfig{
			ResponseModalities: []ResponseModality{ResponseModalityAudio},
		},
	}
	encoded, err := EncodeGenerateContentRequest(request, GenerationDefaults{
		MaxOutputTokens: 65536,
	}, RequestContext{})
	if err != nil {
		t.Fatal(err)
	}
	var wire []any
	if err := json.Unmarshal(encoded, &wire); err != nil {
		t.Fatal(err)
	}
	config := wire[3].([]any)
	if config[3] != float64(65536) {
		t.Fatalf("audio max output tokens = %v", config[3])
	}
}
