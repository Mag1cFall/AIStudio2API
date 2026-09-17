package api

import (
	"bytes"
	"encoding/json"
	"testing"
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
