package camoufoxnative

import (
	"net/http"
	"reflect"
	"testing"
)

func TestBrowserRequestHeadersUsesOfficialOrderAndWhitelist(t *testing.T) {
	headers := http.Header{
		"Authorization":                   {"SAPISIDHASH test"},
		"Content-Type":                    {"application/json+protobuf"},
		"Cookie":                          {"must-not-be-forwarded"},
		"User-Agent":                      {"must-not-be-forwarded"},
		"X-Aistudio-G1-Tier":              {"TIER1"},
		"X-Aistudio-Visit-Id":             {"visit"},
		"X-Goog-Api-Key":                  {"api-key"},
		"X-Goog-Authuser":                 {"0"},
		"X-Goog-Ext-519733851-Bin":        {"extension"},
		"X-User-Agent":                    {"grpc-web-javascript/0.1"},
		"X-Unrelated-Implementation-Note": {"must-not-be-forwarded"},
	}
	expected := [][2]string{
		{"X-AIStudio-G1-Tier", "TIER1"},
		{"X-AIStudio-Visit-Id", "visit"},
		{"X-Goog-Ext-519733851-bin", "extension"},
		{"X-Goog-Api-Key", "api-key"},
		{"X-Goog-AuthUser", "0"},
		{"Authorization", "SAPISIDHASH test"},
		{"Content-Type", "application/json+protobuf"},
		{"X-User-Agent", "grpc-web-javascript/0.1"},
	}
	if actual := browserRequestHeaders(headers); !reflect.DeepEqual(actual, expected) {
		t.Fatalf("browser headers = %#v", actual)
	}
}
