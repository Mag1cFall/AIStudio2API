package aistudio

import (
	"encoding/json"
	"math"
	"strings"
	"testing"
)

func TestVideoInputProtocolEncoding(t *testing.T) {
	fps := 0.5
	part := Part{
		InlineData:       &Blob{MIME: "video/mp4", Data: []byte("video")},
		VideoMetadata:    &VideoMetadata{StartOffset: "0s", EndOffset: "2.000000003s", FPS: &fps},
		MediaProcessing:  "STATIC",
		ThoughtSignature: "video-signature",
	}
	encodedPart, err := encodePart(part)
	if err != nil {
		t.Fatal(err)
	}
	if len(encodedPart) <= 39 || !jsonValueEqual(t, encodedPart[2], []any{"video/mp4", "dmlkZW8="}) {
		t.Fatalf("video data wire = %#v", encodedPart)
	}
	if got, want := encodedPart[14], "video-signature"; got != want {
		t.Fatalf("thought signature = %#v, want %q", got, want)
	}
	metadata, ok := encodedPart[15].([]any)
	if !ok || len(metadata) != 3 {
		t.Fatalf("video metadata = %#v", encodedPart[15])
	}
	if got := metadata[0]; !jsonValueEqual(t, got, []any{int64(0)}) {
		t.Fatalf("explicit zero start offset = %#v", got)
	}
	if got := metadata[1]; !jsonValueEqual(t, got, []any{int64(2), int64(3)}) {
		t.Fatalf("end offset = %#v", got)
	}
	if metadata[2] != fps || encodedPart[39] != int64(1) {
		t.Fatalf("fps/media processing = %#v/%#v", metadata[2], encodedPart[39])
	}

	request := GenerateRequest{Model: "video-model", Contents: []Content{{Role: RoleUser, Parts: []Part{{Text: "before"}, part, {Text: "after"}}}}}
	defaults := GenerationDefaults{MaxOutputTokens: 1024}
	body, err := EncodeGenerateContentRequest(request, defaults, RequestContext{})
	if err != nil {
		t.Fatal(err)
	}
	var generate []any
	if err := json.Unmarshal(body, &generate); err != nil {
		t.Fatal(err)
	}
	content := generate[1].([]any)[0].([]any)
	requestParts := content[0].([]any)
	if len(requestParts) != 3 || !jsonValueEqual(t, requestParts[0], []any{nil, "before"}) || !jsonValueEqual(t, requestParts[1], encodedPart) || !jsonValueEqual(t, requestParts[2], []any{nil, "after"}) {
		t.Fatalf("GenerateContent part order = %#v", requestParts)
	}

	tokens, err := EncodeCountTokensRequest(TokenCountRequest{Model: "video-model", Contents: request.Contents})
	if err != nil {
		t.Fatal(err)
	}
	var count []any
	if err := json.Unmarshal(tokens, &count); err != nil {
		t.Fatal(err)
	}
	countGenerate := count[2].([]any)
	countParts := countGenerate[1].([]any)[0].([]any)[0].([]any)
	if len(countParts) != 3 || !jsonValueEqual(t, countParts[1], encodedPart) {
		t.Fatalf("CountTokens did not preserve video part: %#v", countGenerate)
	}

	buildPart, err := encodeBuildPart(part)
	if err != nil {
		t.Fatal(err)
	}
	var buildMetadata map[string]any
	if err := json.Unmarshal(mustJSON(t, buildPart), &buildMetadata); err != nil {
		t.Fatal(err)
	}
	if buildMetadata["videoMetadata"] == nil || buildMetadata["mediaProcessing"] != "STATIC" || buildMetadata["thoughtSignature"] != "video-signature" {
		t.Fatalf("Build video part = %#v", buildMetadata)
	}

	filePart, err := encodePart(Part{File: &FileRef{ID: "files/video"}, MediaProcessing: "STATIC"})
	if err != nil {
		t.Fatalf("video file reference without MIME was rejected: %v", err)
	}
	if len(filePart) <= 39 || filePart[39] != int64(1) {
		t.Fatalf("video file processing field = %#v", filePart)
	}
}

func TestVideoInputValidation(t *testing.T) {
	fps := 24.01
	tests := []struct {
		name string
		part Part
	}{
		{name: "bad duration syntax", part: Part{InlineData: &Blob{MIME: "video/mp4", Data: []byte("x")}, VideoMetadata: &VideoMetadata{StartOffset: "1m"}}},
		{name: "too many fractional digits", part: Part{InlineData: &Blob{MIME: "video/mp4", Data: []byte("x")}, VideoMetadata: &VideoMetadata{StartOffset: "1.1234567890s"}}},
		{name: "end not later", part: Part{InlineData: &Blob{MIME: "video/mp4", Data: []byte("x")}, VideoMetadata: &VideoMetadata{StartOffset: "2s", EndOffset: "2s"}}},
		{name: "fps out of range", part: Part{InlineData: &Blob{MIME: "video/mp4", Data: []byte("x")}, VideoMetadata: &VideoMetadata{FPS: &fps}}},
		{name: "non-finite fps", part: Part{InlineData: &Blob{MIME: "video/mp4", Data: []byte("x")}, VideoMetadata: &VideoMetadata{FPS: floatPointer(math.Inf(1))}}},
		{name: "unknown processing", part: Part{InlineData: &Blob{MIME: "video/mp4", Data: []byte("x")}, MediaProcessing: "FAST"}},
		{name: "agentic conflicts with metadata", part: Part{InlineData: &Blob{MIME: "video/mp4", Data: []byte("x")}, VideoMetadata: &VideoMetadata{StartOffset: "0s"}, MediaProcessing: "AGENTIC"}},
		{name: "non-video media", part: Part{InlineData: &Blob{MIME: "image/png", Data: []byte("x")}, MediaProcessing: "STATIC"}},
		{name: "text part", part: Part{Text: "video", MediaProcessing: "STATIC"}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if _, err := encodePart(test.part); err == nil {
				t.Fatal("expected invalid video options to fail")
			}
		})
	}

	video := Part{InlineData: &Blob{MIME: "video/mp4", Data: []byte("x")}, MediaProcessing: "AGENTIC"}
	request := GenerateRequest{Model: "m", Contents: []Content{{Role: RoleUser, Parts: []Part{video}}}}
	entry := modelEntry{model: Model{ID: "m", Capabilities: map[string]bool{}}, defaults: GenerationDefaults{MaxOutputTokens: 1024}}
	if err := validateGenerateEntry(request, entry); err == nil || !strings.Contains(err.Error(), "agentic video") {
		t.Fatalf("unsupported agentic video error = %v", err)
	}
	entry.model.Capabilities["agentic_video"] = true
	if err := validateGenerateEntry(request, entry); err != nil {
		t.Fatalf("supported agentic video rejected: %v", err)
	}
}

func TestVideoInputLegacyWireUnchanged(t *testing.T) {
	request := GenerateRequest{Model: "m", Contents: []Content{{Role: RoleUser, Parts: []Part{{Text: "hello"}}}}}
	got, err := EncodeGenerateContentRequest(request, GenerationDefaults{MaxOutputTokens: 1024}, RequestContext{})
	if err != nil {
		t.Fatal(err)
	}
	const want = `["models/m",[[[[null,"hello"]],"user"]],[[null,null,7,5],[null,null,8,5],[null,null,9,5],[null,null,10,5]],[null,null,null,1024,null,null,null,null,null,null,null,null,null,1],null,null,null,null,null,null,1]`
	if string(got) != want {
		t.Fatalf("legacy GenerateContent wire = %s, want %s", got, want)
	}
	count, err := EncodeCountTokensRequest(TokenCountRequest{Model: "m", Contents: request.Contents})
	if err != nil {
		t.Fatal(err)
	}
	if string(count) != `["models/m",[[[[null,"hello"]],"user"]]]` {
		t.Fatalf("legacy CountTokens wire = %s", count)
	}
}

func TestInteractionRoutedVideoOptionsFailInsteadOfDisappearing(t *testing.T) {
	for _, part := range []Part{
		{File: &FileRef{ID: "video"}, VideoMetadata: &VideoMetadata{StartOffset: "1s"}},
		{File: &FileRef{ID: "video"}, MediaProcessing: "STATIC"},
		{ProcessingCall: &MediaProcessingStep{ID: "call"}, ThoughtSignature: "call-signature"},
		{ProcessingResult: &MediaProcessingStep{ID: "call"}, ThoughtSignature: "result-signature"},
	} {
		if _, _, _, err := encodeInteractionSteps([]Content{{Role: RoleAssistant, Parts: []Part{part}}}); err == nil {
			t.Fatal("private Interaction route silently accepted unsupported video options/history")
		}
	}
}

func jsonValueEqual(t *testing.T, got, want any) bool {
	t.Helper()
	gotJSON, err := json.Marshal(got)
	if err != nil {
		t.Fatal(err)
	}
	wantJSON, err := json.Marshal(want)
	if err != nil {
		t.Fatal(err)
	}
	return string(gotJSON) == string(wantJSON)
}

func mustJSON(t *testing.T, value any) []byte {
	t.Helper()
	encoded, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return encoded
}

func floatPointer(value float64) *float64 { return &value }
