package api

import (
	"bytes"
	"testing"
)

func TestDecodeBase64Flexible(t *testing.T) {
	stdStr := "/9j/4AAQSkZJRgABAQE="
	urlStr := "_9j_4AAQSkZJRgABAQE" // no padding, url-safe

	decodedStd, err := decodeBase64Flexible(stdStr)
	if err != nil {
		t.Fatalf("std failed: %v", err)
	}

	decodedURL, err := decodeBase64Flexible(urlStr)
	if err != nil {
		t.Fatalf("url-safe failed: %v", err)
	}

	if !bytes.Equal(decodedStd, decodedURL) {
		t.Fatalf("mismatch between std and url-safe decoded bytes")
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
