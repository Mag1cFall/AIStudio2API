package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/Mag1cFall/AIStudio2API/internal/aistudio"
)

type httpStatusError interface {
	HTTPStatus() int
}

type statusCodeError interface {
	StatusCode() int
}

type errorCodeProvider interface {
	ErrorCode() string
}

func decodeJSON(r *http.Request, target any) error {
	decoder := json.NewDecoder(r.Body)
	if err := decoder.Decode(target); err != nil {
		return err
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		return fmt.Errorf("request body must contain one JSON value")
	}
	return nil
}

func writeJSON(w http.ResponseWriter, status int, payload any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(payload)
}

func streamHeaders(w http.ResponseWriter) {
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.WriteHeader(http.StatusOK)
	if flusher, ok := w.(http.Flusher); ok {
		flusher.Flush()
	}
}

func writeSSE(w http.ResponseWriter, event string, payload any) error {
	data, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	if event != "" {
		if _, err := fmt.Fprintf(w, "event: %s\n", event); err != nil {
			return err
		}
	}
	if _, err := fmt.Fprintf(w, "data: %s\n\n", data); err != nil {
		return err
	}
	if flusher, ok := w.(http.Flusher); ok {
		flusher.Flush()
	}
	return nil
}

func writeSSEText(w http.ResponseWriter, data string) error {
	if _, err := fmt.Fprintf(w, "data: %s\n\n", data); err != nil {
		return err
	}
	if flusher, ok := w.(http.Flusher); ok {
		flusher.Flush()
	}
	return nil
}

func writeSSEHeartbeat(w http.ResponseWriter) error {
	if _, err := io.WriteString(w, ": ping\n\n"); err != nil {
		return err
	}
	if flusher, ok := w.(http.Flusher); ok {
		flusher.Flush()
	}
	return nil
}

func statusFromError(err error) int {
	if errors.Is(err, context.Canceled) {
		return http.StatusServiceUnavailable
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return http.StatusGatewayTimeout
	}
	if errors.Is(err, aistudio.ErrNoEligibleAccount) {
		return http.StatusBadRequest
	}
	if errors.Is(err, aistudio.ErrInvalidArgument) {
		return http.StatusBadRequest
	}
	if errors.Is(err, aistudio.ErrModelNotFound) {
		return http.StatusNotFound
	}
	if isUnverifiedProtocolError(err) {
		return http.StatusBadRequest
	}
	var rpcError *aistudio.RPCError
	if errors.As(err, &rpcError) && rpcError.StatusCode == http.StatusNotFound {
		return http.StatusBadGateway
	}
	var statusErr httpStatusError
	if errors.As(err, &statusErr) {
		return validHTTPStatus(statusErr.HTTPStatus())
	}
	var codeErr statusCodeError
	if errors.As(err, &codeErr) {
		return validHTTPStatus(codeErr.StatusCode())
	}
	return http.StatusBadGateway
}

// shouldWriteRequestError 判断仍在线的客户端是否需要收到结构化错误
func shouldWriteRequestError(r *http.Request, err error) bool {
	return err != nil && (!errors.Is(err, context.Canceled) || r.Context().Err() == nil)
}

func isUnverifiedProtocolError(err error) bool {
	var unverified *aistudio.UnverifiedProtocolError
	return errors.As(err, &unverified)
}

func validHTTPStatus(status int) int {
	if status >= 400 && status <= 599 {
		return status
	}
	return http.StatusBadGateway
}

func codeFromError(err error, defaultCode string) string {
	var codeErr errorCodeProvider
	if errors.As(err, &codeErr) && codeErr.ErrorCode() != "" {
		return codeErr.ErrorCode()
	}
	return defaultCode
}

func writeAuthError(w http.ResponseWriter, r *http.Request) {
	switch protocolForRequest(r) {
	case "anthropic":
		writeAnthropicError(w, http.StatusUnauthorized, "authentication_error", "Invalid API Key")
	case "gemini":
		writeGeminiError(w, http.StatusUnauthorized, "UNAUTHENTICATED", "Invalid API Key")
	case "admin":
		writeAdminError(w, http.StatusUnauthorized, "invalid_api_key", "Invalid API Key")
	default:
		writeOpenAIError(w, http.StatusUnauthorized, "invalid_api_key", "Invalid API Key")
	}
}

func protocolForRequest(r *http.Request) string {
	switch {
	case strings.HasPrefix(r.URL.Path, "/v1beta/"):
		return "gemini"
	case strings.HasPrefix(r.URL.Path, "/v1/messages"), r.Header.Get("Anthropic-Version") != "":
		return "anthropic"
	case strings.HasPrefix(r.URL.Path, "/api/"):
		return "admin"
	default:
		return "openai"
	}
}

func writeOpenAIError(w http.ResponseWriter, status int, code string, message string) {
	setAccessLogResponseError(w, message)
	writeJSON(w, status, map[string]any{"error": map[string]any{
		"message": message,
		"type":    openAIErrorType(status, code),
		"code":    code,
	}})
}

func openAIErrorType(status int, code string) string {
	if status == http.StatusBadRequest || status == http.StatusUnauthorized || status == http.StatusForbidden || status == http.StatusNotFound || code == "invalid_request" || code == "invalid_api_key" || code == "model_not_found" {
		return "invalid_request_error"
	}
	return "api_error"
}

func writeAnthropicError(w http.ResponseWriter, status int, errorType string, message string) {
	setAccessLogResponseError(w, message)
	writeJSON(w, status, map[string]any{
		"type": "error",
		"error": map[string]string{
			"type":    errorType,
			"message": message,
		},
	})
}

func writeGeminiError(w http.ResponseWriter, status int, statusName string, message string) {
	setAccessLogResponseError(w, message)
	writeJSON(w, status, map[string]any{"error": map[string]any{
		"code":    status,
		"message": message,
		"status":  statusName,
	}})
}

func writeAdminError(w http.ResponseWriter, status int, code string, message string) {
	writeJSON(w, status, map[string]any{"error": map[string]string{
		"code":    code,
		"message": message,
	}})
}

func openAIErrorCode(err error) string {
	if errors.Is(err, context.Canceled) {
		return "request_canceled"
	}
	if errors.Is(err, aistudio.ErrModelNotFound) {
		return "model_not_found"
	}
	if errors.Is(err, aistudio.ErrNoEligibleAccount) {
		return "account_required"
	}
	if errors.Is(err, aistudio.ErrInvalidArgument) {
		return "invalid_request"
	}
	if isUnverifiedProtocolError(err) {
		return "invalid_request"
	}
	switch statusFromError(err) {
	case http.StatusNotFound:
		return codeFromError(err, "model_not_found")
	default:
		return codeFromError(err, "upstream_error")
	}
}

func anthropicErrorType(err error) string {
	if isUnverifiedProtocolError(err) {
		return "invalid_request_error"
	}
	switch statusFromError(err) {
	case http.StatusBadRequest:
		return "invalid_request_error"
	case http.StatusUnauthorized:
		return "authentication_error"
	case http.StatusForbidden:
		return "permission_error"
	case http.StatusNotFound:
		return "not_found_error"
	case http.StatusRequestEntityTooLarge:
		return "request_too_large"
	case http.StatusTooManyRequests:
		return "rate_limit_error"
	case 529:
		return "overloaded_error"
	default:
		return "api_error"
	}
}

func geminiErrorStatus(err error) string {
	switch statusFromError(err) {
	case http.StatusBadRequest:
		return "INVALID_ARGUMENT"
	case http.StatusUnauthorized:
		return "UNAUTHENTICATED"
	case http.StatusForbidden:
		return "PERMISSION_DENIED"
	case http.StatusNotFound:
		return "NOT_FOUND"
	case http.StatusTooManyRequests:
		return "RESOURCE_EXHAUSTED"
	case http.StatusServiceUnavailable:
		return "UNAVAILABLE"
	case http.StatusGatewayTimeout:
		return "DEADLINE_EXCEEDED"
	default:
		return "INTERNAL"
	}
}
