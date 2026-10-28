package chromeauth

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Mag1cFall/AIStudio2API/internal/aistudio"
)

func newMockCookieStorage() aistudio.StorageState {
	return aistudio.StorageState{
		Cookies: []aistudio.StateCookie{
			{Name: "SAPISID", Value: "mock_sapisid", Domain: ".google.com", Path: "/"},
			{Name: "__Secure-1PAPISID", Value: "mock_1papisid", Domain: ".google.com", Path: "/"},
			{Name: "__Secure-3PAPISID", Value: "mock_3papisid", Domain: ".google.com", Path: "/"},
			{Name: "SID", Value: "mock_sid", Domain: ".google.com", Path: "/"},
		},
		Origins: []aistudio.StorageOrigin{},
	}
}

func mockMultiAccountServer() *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/":
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`"WIu0Nc":"test_api_key"`))
		case r.URL.Path == "/prompts/new_chat":
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`"oPEP7c":"first@example.com"`))
		case r.URL.Path == "/u/1/prompts/new_chat":
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`"oPEP7c":"second@example.com"`))
		case r.URL.Path == "/u/2/prompts/new_chat":
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`"oPEP7c":"third@example.com"`))
		case strings.HasPrefix(r.URL.Path, "/u/"):
			w.WriteHeader(http.StatusNotFound)
		default:
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`[[{"name":"models/gemini-2.5-flash","supportedGenerationMethods":["generateContent"]}]]`))
		}
	}))
}

func TestChatURLForAuthUser(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name     string
		authUser int
		want     string
	}{
		{name: "default user zero", authUser: 0, want: "https://aistudio.google.com/prompts/new_chat"},
		{name: "sub-account user one", authUser: 1, want: "https://aistudio.google.com/u/1/prompts/new_chat"},
		{name: "sub-account user two", authUser: 2, want: "https://aistudio.google.com/u/2/prompts/new_chat"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := chatURLForAuthUser(tc.authUser)
			if got != tc.want {
				t.Fatalf("chatURLForAuthUser(%d) = %q, want %q", tc.authUser, got, tc.want)
			}
		})
	}
}

func TestVerifyAuthUserMock(t *testing.T) {
	server := mockMultiAccountServer()
	defer server.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	state := newMockCookieStorage()
	client := server.Client()

	tests := []struct {
		authUser  int
		wantEmail string
	}{
		{authUser: 0, wantEmail: "first@example.com"},
		{authUser: 1, wantEmail: "second@example.com"},
		{authUser: 2, wantEmail: "third@example.com"},
	}

	for _, tc := range tests {
		url := server.URL + "/prompts/new_chat"
		if tc.authUser > 0 {
			url = server.URL + "/u/" + string(rune('0'+tc.authUser)) + "/prompts/new_chat"
		}
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
		if err != nil {
			t.Fatal(err)
		}
		req.Header.Set("User-Agent", "TestAgent")
		resp, err := client.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()

		if resp.StatusCode != http.StatusOK {
			t.Fatalf("auth_user=%d returned status %d", tc.authUser, resp.StatusCode)
		}
	}

	_ = state
}
