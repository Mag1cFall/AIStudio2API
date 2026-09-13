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
