//go:build conservative

package api

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestConservativeRejectsPairing(t *testing.T) {
	handler := NewHandler(nil, Config{})
	for _, request := range []*http.Request{
		httptest.NewRequest(http.MethodPost, "/api/pairing", nil),
		httptest.NewRequest(http.MethodGet, "/api/pairing", nil),
		httptest.NewRequest(http.MethodPost, "/api/pairing/accounts", strings.NewReader(`{}`)),
		httptest.NewRequest(http.MethodPut, "/api/pairing/accounts/account/cookies", strings.NewReader(`{}`)),
	} {
		recorder := httptest.NewRecorder()
		handler.ServeHTTP(recorder, request)
		if recorder.Code != http.StatusForbidden {
			t.Fatalf("%s %s = %d", request.Method, request.URL.Path, recorder.Code)
		}
	}
}
