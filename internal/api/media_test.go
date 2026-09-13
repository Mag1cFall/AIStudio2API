package api

import (
	"bytes"
	"encoding/json"
	"testing"
)

func TestDecodeBase64Flexible(t *testing.T) {
	stdStr := "/9j/4AAQSkZJRgABAQE="
	urlStr := "_9j_4AAQSkZJRgABAQE" // no padding, url-safe
	dataURL := "data:image/jpeg;base64,/9j/4AAQSkZJRgABAQE="

	decodedStd, err := decodeBase64Flexible(stdStr)
	if err != nil {
		t.Fatalf("std failed: %v", err)
	}

	decodedURL, err := decodeBase64Flexible(urlStr)
	if err != nil {
		t.Fatalf("url-safe failed: %v", err)
	}

	decodedDataURL, err := decodeBase64Flexible(dataURL)
	if err != nil {
		t.Fatalf("data url failed: %v", err)
	}

	if !bytes.Equal(decodedStd, decodedURL) {
		t.Fatalf("mismatch between std and url-safe decoded bytes")
	}
	if !bytes.Equal(decodedStd, decodedDataURL) {
		t.Fatalf("mismatch between std and data URL decoded bytes")
	}
}

func TestNormalizeImagePayload_GIF(t *testing.T) {
	gifB64 := "R0lGODlhAQABAIAAAAAAAP///yH5BAEAAAAALAAAAAABAAEAAAIBRAA7"
	raw, err := decodeBase64Flexible(gifB64)
	if err != nil {
		t.Fatalf("decode gif failed: %v", err)
	}

	mimeType, outData := normalizeImagePayload("image/gif", raw)
	if mimeType != "image/png" {
		t.Fatalf("expected image/png, got %s", mimeType)
	}
	if len(outData) == 0 {
		t.Fatalf("output png data is empty")
	}
}

func TestMapGeminiParts_Compatibility(t *testing.T) {
	rawPayloads := []string{
		// Standard camelCase
		`[{"inlineData": {"mimeType": "image/png", "data": "iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAYAAAAfFcSJAAAADUlEQVR42mNk+M9QDwADhgGAWjR9awAAAABJRU5ErkJggg=="}}]`,
		// CPA style: snake_case mime_type inside camelCase inlineData
		`[{"inlineData": {"mime_type": "image/png", "data": "iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAYAAAAfFcSJAAAADUlEQVR42mNk+M9QDwADhgGAWjR9awAAAABJRU5ErkJggg=="}}]`,
		// Python SDK style: pure snake_case inline_data
		`[{"inline_data": {"mime_type": "image/png", "data": "iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAYAAAAfFcSJAAAADUlEQVR42mNk+M9QDwADhgGAWjR9awAAAABJRU5ErkJggg=="}}]`,
		// snake_case file_data
		`[{"file_data": {"file_uri": "https://example.com/test.png", "mime_type": "image/png"}}]`,
	}

	for idx, raw := range rawPayloads {
		var parts []geminiPart
		if err := json.Unmarshal([]byte(raw), &parts); err != nil {
			t.Fatalf("case %d: failed to unmarshal: %v", idx, err)
		}
		mapped, _, err := mapGeminiParts(parts)
		if err != nil {
			t.Fatalf("case %d: mapGeminiParts failed: %v", idx, err)
		}
		if len(mapped) != 1 {
			t.Fatalf("case %d: expected 1 part, got %d", idx, len(mapped))
		}
	}
}

func TestGeminiVideoImage_Compatibility(t *testing.T) {
	rawPayloads := []string{
		`{"inlineData": {"mimeType": "image/png", "data": "iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAYAAAAfFcSJAAAADUlEQVR42mNk+M9QDwADhgGAWjR9awAAAABJRU5ErkJggg=="}}`,
		`{"inlineData": {"mime_type": "image/png", "data": "iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAYAAAAfFcSJAAAADUlEQVR42mNk+M9QDwADhgGAWjR9awAAAABJRU5ErkJggg=="}}`,
		`{"inline_data": {"mime_type": "image/png", "data": "iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAYAAAAfFcSJAAAADUlEQVR42mNk+M9QDwADhgGAWjR9awAAAABJRU5ErkJggg=="}}`,
		`{"file_data": {"file_uri": "https://example.com/test.png", "mime_type": "image/png"}}`,
	}

	for idx, raw := range rawPayloads {
		var input geminiVideoImageInput
		if err := json.Unmarshal([]byte(raw), &input); err != nil {
			t.Fatalf("case %d: failed to unmarshal: %v", idx, err)
		}
		img, err := geminiVideoImage(&input)
		if err != nil {
			t.Fatalf("case %d: geminiVideoImage failed: %v", idx, err)
		}
		if img == nil {
			t.Fatalf("case %d: expected non-nil image", idx)
		}
	}
}
