package api

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Mag1cFall/AIStudio2API/internal/aistudio"
)

func TestPublicAccountErrors(t *testing.T) {
	detail := "pool-user@example.com 需要重新登录：https://accounts.google.com/ServiceLogin?secret=private"
	failure := fmt.Errorf("generation: %w", &aistudio.AccountsNotReadyError{Reasons: []string{detail}})
	for name, write := range map[string]func(http.ResponseWriter, error){
		"openai":    writeOpenAIRequestError,
		"anthropic": writeAnthropicRequestError,
		"gemini":    writeGeminiRequestError,
	} {
		t.Run(name, func(t *testing.T) {
			recorder := httptest.NewRecorder()
			metadata := &accessLogMetadata{}
			writer := &accessLogResponseWriter{ResponseWriter: recorder, metadata: metadata}
			write(writer, failure)
			var body struct {
				Error struct {
					Message string `json:"message"`
				} `json:"error"`
			}
			if err := json.Unmarshal(recorder.Body.Bytes(), &body); err != nil {
				t.Fatal(err)
			}
			if recorder.Code != http.StatusServiceUnavailable || !strings.Contains(body.Error.Message, "No available account for this request") {
				t.Fatalf("response = %d %s", recorder.Code, recorder.Body.String())
			}
			if strings.Contains(recorder.Body.String(), "example.com") || strings.Contains(recorder.Body.String(), "secret") {
				t.Fatalf("public error = %s", recorder.Body.String())
			}
			if metadata.err != failure.Error() {
				t.Fatalf("diagnostic = %q", metadata.err)
			}
		})
	}
	t.Run("openai stream", func(t *testing.T) {
		recorder := httptest.NewRecorder()
		writeOpenAISSEError(recorder, failure)
		if !strings.Contains(recorder.Body.String(), "No available account for this request") || strings.Contains(recorder.Body.String(), "example.com") {
			t.Fatalf("event = %s", recorder.Body.String())
		}
	})
	t.Run("anthropic stream", func(t *testing.T) {
		recorder := httptest.NewRecorder()
		writer := anthropicStreamWriter{send: func(event string, payload any) error { return writeSSE(recorder, event, payload) }}
		if err := writer.error(failure); err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(recorder.Body.String(), "No available account for this request") || strings.Contains(recorder.Body.String(), "example.com") {
			t.Fatalf("event = %s", recorder.Body.String())
		}
	})
	for name, payload := range map[string]any{
		"responses":          responsesError(failure),
		"gemini stream":      geminiErrorPayload(failure, 0),
		"interaction stream": interactionErrorEvent(failure, "event-1"),
		"live":               bidiErrorMessage(failure),
	} {
		t.Run(name, func(t *testing.T) {
			data, err := json.Marshal(payload)
			if err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(string(data), "No available account for this request") || strings.Contains(string(data), "example.com") {
				t.Fatalf("error = %s", data)
			}
		})
	}
}

func TestPublicErrorRetryAndValidation(t *testing.T) {
	cooling := &aistudio.AllCoolingError{ModelID: "gemini", Until: time.Now().Add(time.Minute), Reason: "pool-user@example.com: quota exhausted"}
	recorder := httptest.NewRecorder()
	writeOpenAIRequestError(recorder, cooling)
	if recorder.Code != http.StatusTooManyRequests || recorder.Header().Get("Retry-After") == "" || !strings.Contains(recorder.Body.String(), "Please try again in") || strings.Contains(recorder.Body.String(), "example.com") {
		t.Fatalf("response = %d %s %s", recorder.Code, recorder.Header(), recorder.Body.String())
	}
	payload := geminiErrorPayload(cooling, 60)
	if payload["message"] != "Rate limit exceeded" || payload["details"] == nil {
		t.Fatalf("gemini error = %v", payload)
	}
	invalid := fmt.Errorf("%w: max_output_tokens must be positive", aistudio.ErrInvalidArgument)
	recorder = httptest.NewRecorder()
	writeOpenAIRequestError(recorder, invalid)
	if recorder.Code != http.StatusBadRequest || !strings.Contains(recorder.Body.String(), "max_output_tokens must be positive") {
		t.Fatalf("validation = %d %s", recorder.Code, recorder.Body.String())
	}
	recorder = httptest.NewRecorder()
	writeOpenAIRequestError(recorder, errors.New("proxy password and internal address"))
	if recorder.Code != http.StatusBadGateway || strings.Contains(recorder.Body.String(), "proxy password") {
		t.Fatalf("server error = %d %s", recorder.Code, recorder.Body.String())
	}
}

func TestPublicUpstreamAuthenticationError(t *testing.T) {
	for _, status := range []int{http.StatusUnauthorized, http.StatusForbidden} {
		failure := aistudio.AccountError("pool-user@example.com", &aistudio.RPCError{StatusCode: status, Message: "login redirect https://accounts.google.com/private"})
		recorder := httptest.NewRecorder()
		writeOpenAIRequestError(recorder, failure)
		if recorder.Code != status || !strings.Contains(recorder.Body.String(), "Upstream account unavailable") || strings.Contains(recorder.Body.String(), "example.com") || strings.Contains(recorder.Body.String(), "accounts.google.com") {
			t.Fatalf("authentication = %d %s", recorder.Code, recorder.Body.String())
		}
	}
}
