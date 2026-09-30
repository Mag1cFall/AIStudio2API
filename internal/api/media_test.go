package api

import (
	"bytes"
	"encoding/json"
	"testing"

	"github.com/Mag1cFall/AIStudio2API/internal/aistudio"
)

// TestGeminiInlineDataCompatibility 验证不同 SDK 的内联媒体字段与 Base64 形式
func TestGeminiInlineDataCompatibility(t *testing.T) {
	want := []byte{0xfb, 0xff, 0xef}
	for _, test := range []struct {
		name string
		raw  string
	}{
		{name: "camel case", raw: `[{"inlineData":{"mimeType":"application/octet-stream","data":"+//v"}}]`},
		{name: "snake case", raw: `[{"inline_data":{"mime_type":"application/octet-stream","data":"-__v"}}]`},
		{name: "data URL", raw: `[{"inlineData":{"mimeType":"application/octet-stream","data":"data:application/octet-stream;base64,+//v"}}]`},
	} {
		t.Run(test.name, func(t *testing.T) {
			var input []geminiPart
			if err := json.Unmarshal([]byte(test.raw), &input); err != nil {
				t.Fatal(err)
			}
			parts, hasResult, err := mapGeminiParts(input)
			if err != nil {
				t.Fatal(err)
			}
			if hasResult || len(parts) != 1 || parts[0].InlineData == nil {
				t.Fatalf("mapped parts=%#v hasResult=%v", parts, hasResult)
			}
			if parts[0].InlineData.MIME != "application/octet-stream" || !bytes.Equal(parts[0].InlineData.Data, want) {
				t.Fatalf("inline data=%#v", parts[0].InlineData)
			}
		})
	}
}

// TestSpeechWAV 验证原生 WAV 的 PCM 输出、容器合并与音频元数据
func TestSpeechWAV(t *testing.T) {
	pcm := []byte{1, 2, 3, 4}
	wav := pcmWAV(pcm, 24000, 1)
	media := aistudio.Media{MIME: "audio/wav", Data: wav}
	data, mime, err := encodeSpeechResponse(media, "pcm")
	if err != nil || !bytes.Equal(data, pcm) || mime != "audio/l16;rate=24000;channels=1" {
		t.Fatalf("pcm=%v mime=%s err=%v", data, mime, err)
	}
	data, mime, err = encodeSpeechResponse(media, "wav")
	if err != nil || !bytes.Equal(data, wav) || mime != "audio/wav" {
		t.Fatalf("wav mime=%s err=%v", mime, err)
	}
	joined, err := joinedAudio([]aistudio.Media{media, media})
	if err != nil || !bytes.Equal(joined.Data, []byte{1, 2, 3, 4, 1, 2, 3, 4}) {
		t.Fatalf("joined=%v err=%v", joined.Data, err)
	}
	if _, err := wavPCM(aistudio.Media{MIME: "audio/wav", Data: wav[:20]}); err == nil {
		t.Fatal("truncated WAV accepted")
	}
	content, err := interactionMedia(media, "pcm")
	if err != nil || content["mime_type"] != "audio/l16" || content["sample_rate"] != 24000 || content["channels"] != 1 {
		t.Fatalf("interaction audio=%v err=%v", content, err)
	}
}
