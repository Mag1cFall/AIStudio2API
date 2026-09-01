package api

import (
	"context"
	"crypto/subtle"
	"errors"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/Mag1cFall/AIStudio2API/internal/aistudio"
)

type accessLogContextKey struct{}

type accessLogMetadata struct {
	mu              sync.Mutex
	admin           AdminService
	method          string
	path            string
	started         bool
	generation      bool
	model           string
	account         string
	finishReason    string
	err             string
	canceled        bool
	failureStatus   int
	firstEvent      time.Duration
	firstContent    time.Duration
	upstreamBytes   int64
	contentChars    int
	outputTokens    int64
	reasoningTokens int64
	inputMessages   int
	inputTextChars  int
	inputMedia      int
	inputMediaBytes int64
	inputFiles      int
	temperature     string
	topP            string
	thinking        string
	maxOutputTokens string
	requestID       string
}

type accessLogSnapshot struct {
	generation      bool
	model           string
	account         string
	finishReason    string
	requestErr      string
	canceled        bool
	failureStatus   int
	firstEvent      time.Duration
	firstContent    time.Duration
	upstreamBytes   int64
	contentChars    int
	outputTokens    int64
	reasoningTokens int64
	inputMessages   int
	inputTextChars  int
	inputMedia      int
	inputMediaBytes int64
	inputFiles      int
	temperature     string
	topP            string
	thinking        string
	maxOutputTokens string
	requestID       string
}

type accessLogResponseWriter struct {
	http.ResponseWriter
	status   int
	metadata *accessLogMetadata
}

func (writer *accessLogResponseWriter) WriteHeader(status int) {
	if writer.status != 0 {
		return
	}
	writer.status = status
	writer.ResponseWriter.WriteHeader(status)
}

func (writer *accessLogResponseWriter) Write(data []byte) (int, error) {
	if writer.status == 0 {
		writer.WriteHeader(http.StatusOK)
	}
	return writer.ResponseWriter.Write(data)
}

func (writer *accessLogResponseWriter) Flush() {
	if writer.status == 0 {
		writer.WriteHeader(http.StatusOK)
	}
	_ = http.NewResponseController(writer.ResponseWriter).Flush()
}

func (writer *accessLogResponseWriter) Unwrap() http.ResponseWriter {
	return writer.ResponseWriter
}

func (writer *accessLogResponseWriter) setError(message string) {
	writer.metadata.setError(message)
}

func (metadata *accessLogMetadata) setTarget(model string, account string) {
	metadata.mu.Lock()
	if model = strings.TrimSpace(model); model != "" {
		metadata.model = strings.TrimPrefix(model, "models/")
	}
	if account = strings.TrimSpace(account); account != "" {
		metadata.account = account
	}
	metadata.mu.Unlock()
	metadata.start(false)
}

func (metadata *accessLogMetadata) start(force bool) {
	metadata.mu.Lock()
	if metadata.started || !force && metadata.account == "" {
		metadata.mu.Unlock()
		return
	}
	metadata.started = true
	admin := metadata.admin
	entry := AccessLog{
		Method: metadata.method, Path: metadata.path, Model: metadata.model, Account: metadata.account,
		Temperature: metadata.temperature, TopP: metadata.topP, Thinking: metadata.thinking,
		MaxOutputTokens: metadata.maxOutputTokens, Generation: metadata.generation, RequestID: metadata.requestID,
		InputMessages: metadata.inputMessages, InputTextChars: metadata.inputTextChars,
		InputMedia: metadata.inputMedia, InputMediaBytes: metadata.inputMediaBytes, InputFiles: metadata.inputFiles,
	}
	metadata.mu.Unlock()
	if admin != nil {
		admin.RecordAccessStart(entry)
	}
}

func (metadata *accessLogMetadata) setError(message string) {
	message = strings.TrimSpace(message)
	if message == "" {
		return
	}
	metadata.mu.Lock()
	metadata.err = message
	metadata.mu.Unlock()
}

func (metadata *accessLogMetadata) setRequestError(err error) {
	if err == nil {
		return
	}
	metadata.mu.Lock()
	metadata.err = strings.TrimSpace(err.Error())
	metadata.canceled = errors.Is(err, context.Canceled)
	if metadata.canceled {
		metadata.failureStatus = 499
	} else {
		metadata.failureStatus = statusFromError(err)
	}
	metadata.mu.Unlock()
}

func (metadata *accessLogMetadata) setFinishReason(reason string) {
	metadata.mu.Lock()
	metadata.finishReason = strings.TrimSpace(reason)
	metadata.mu.Unlock()
}

func (metadata *accessLogMetadata) setGenerationResult(
	firstContent time.Duration,
	contentChars int,
	outputTokens int64,
	reasoningTokens int64,
) {
	metadata.mu.Lock()
	metadata.firstContent = firstContent
	metadata.contentChars = contentChars
	metadata.outputTokens = outputTokens
	metadata.reasoningTokens = reasoningTokens
	metadata.mu.Unlock()
}

func (metadata *accessLogMetadata) setGenerationConfig(config aistudio.GenerationConfig) {
	metadata.mu.Lock()
	metadata.generation = true
	metadata.temperature = formatLogFloat(config.Temperature)
	metadata.topP = formatLogFloat(config.TopP)
	metadata.thinking = formatLogThinking(config)
	metadata.maxOutputTokens = formatLogInt(config.MaxOutputTokens)
	metadata.mu.Unlock()
}

func (metadata *accessLogMetadata) setGenerationInput(request aistudio.GenerateRequest) {
	textChars := utf8.RuneCountInString(request.System)
	media := 0
	var mediaBytes int64
	files := 0
	for _, content := range request.Contents {
		for _, part := range content.Parts {
			textChars += utf8.RuneCountInString(part.Text)
			if part.InlineData != nil {
				media++
				mediaBytes += int64(len(part.InlineData.Data))
			}
			if part.ExternalMedia != nil {
				media++
			}
			if part.File != nil {
				files++
			}
		}
	}
	metadata.mu.Lock()
	metadata.requestID = strings.TrimSpace(request.ID)
	metadata.inputMessages = len(request.Contents)
	metadata.inputTextChars = textChars
	metadata.inputMedia = media
	metadata.inputMediaBytes = mediaBytes
	metadata.inputFiles = files
	metadata.mu.Unlock()
}

func (metadata *accessLogMetadata) setUpstreamBytes(bytes int64) {
	metadata.mu.Lock()
	metadata.upstreamBytes = bytes
	metadata.mu.Unlock()
}

func (metadata *accessLogMetadata) setFirstEvent(firstEvent time.Duration) {
	metadata.mu.Lock()
	if metadata.firstEvent == 0 {
		metadata.firstEvent = firstEvent
	}
	metadata.mu.Unlock()
}

func (metadata *accessLogMetadata) snapshot() accessLogSnapshot {
	metadata.mu.Lock()
	snapshot := accessLogSnapshot{
		generation: metadata.generation,
		model:      metadata.model, account: metadata.account, finishReason: metadata.finishReason,
		requestErr: metadata.err, canceled: metadata.canceled,
		failureStatus: metadata.failureStatus,
		firstEvent:    metadata.firstEvent, firstContent: metadata.firstContent, contentChars: metadata.contentChars,
		upstreamBytes: metadata.upstreamBytes, outputTokens: metadata.outputTokens, reasoningTokens: metadata.reasoningTokens,
		inputMessages: metadata.inputMessages, inputTextChars: metadata.inputTextChars,
		inputMedia: metadata.inputMedia, inputMediaBytes: metadata.inputMediaBytes, inputFiles: metadata.inputFiles,
		temperature: metadata.temperature, topP: metadata.topP,
		thinking: metadata.thinking, maxOutputTokens: metadata.maxOutputTokens, requestID: metadata.requestID,
	}
	metadata.mu.Unlock()
	return snapshot
}

// SetAccessLogFirstEvent 写入首个上游语义事件耗时
func SetAccessLogFirstEvent(ctx context.Context, firstEvent time.Duration) {
	if metadata, ok := ctx.Value(accessLogContextKey{}).(*accessLogMetadata); ok {
		metadata.setFirstEvent(firstEvent)
	}
}

// SetAccessLogGenerationConfig 写入生成请求采用的参数
func SetAccessLogGenerationConfig(ctx context.Context, config aistudio.GenerationConfig) {
	if metadata, ok := ctx.Value(accessLogContextKey{}).(*accessLogMetadata); ok {
		metadata.setGenerationConfig(config)
	}
}

// SetAccessLogGenerationInput 写入生成请求输入摘要
func SetAccessLogGenerationInput(ctx context.Context, request aistudio.GenerateRequest) {
	if metadata, ok := ctx.Value(accessLogContextKey{}).(*accessLogMetadata); ok {
		metadata.setGenerationInput(request)
	}
}

// SetAccessLogUpstreamBytes 写入上游响应体字节数
func SetAccessLogUpstreamBytes(ctx context.Context, bytes int64) {
	if metadata, ok := ctx.Value(accessLogContextKey{}).(*accessLogMetadata); ok {
		metadata.setUpstreamBytes(bytes)
	}
}

// StartAccessLog 立即写入已经完成解析的请求开始记录
func StartAccessLog(ctx context.Context) {
	if metadata, ok := ctx.Value(accessLogContextKey{}).(*accessLogMetadata); ok {
		metadata.start(true)
	}
}

func formatLogFloat(value *float64) string {
	if value == nil {
		return "默认"
	}
	return strconv.FormatFloat(*value, 'f', -1, 64)
}

func formatLogInt(value *int64) string {
	if value == nil {
		return "默认"
	}
	return strconv.FormatInt(*value, 10)
}

func formatLogThinking(config aistudio.GenerationConfig) string {
	if effort := strings.TrimSpace(config.ReasoningEffort); effort != "" {
		return effort
	}
	if config.ThinkingBudget != nil {
		return "预算" + strconv.FormatInt(*config.ThinkingBudget, 10)
	}
	return "默认"
}

// SetAccessLogTarget 写入请求实际使用的模型与账户
func SetAccessLogTarget(ctx context.Context, model string, account string) {
	if metadata, ok := ctx.Value(accessLogContextKey{}).(*accessLogMetadata); ok {
		metadata.setTarget(model, account)
	}
}

// SetAccessLogError 写入请求最终错误
func SetAccessLogError(ctx context.Context, err error) {
	if err == nil {
		return
	}
	if metadata, ok := ctx.Value(accessLogContextKey{}).(*accessLogMetadata); ok {
		metadata.setRequestError(err)
	}
}

// SetAccessLogFinishReason 写入生成请求的上游终止原因
func SetAccessLogFinishReason(ctx context.Context, reason string) {
	if metadata, ok := ctx.Value(accessLogContextKey{}).(*accessLogMetadata); ok {
		metadata.setFinishReason(reason)
	}
}

// SetAccessLogGenerationResult 写入生成流的完成摘要
func SetAccessLogGenerationResult(
	ctx context.Context,
	firstContent time.Duration,
	contentChars int,
	outputTokens int64,
	reasoningTokens int64,
) {
	if metadata, ok := ctx.Value(accessLogContextKey{}).(*accessLogMetadata); ok {
		metadata.setGenerationResult(firstContent, contentChars, outputTokens, reasoningTokens)
	}
}

func requestLoggingMiddleware(admin AdminService, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		started := time.Now()
		metadata := &accessLogMetadata{admin: admin, method: r.Method, path: r.URL.Path}
		writer := &accessLogResponseWriter{ResponseWriter: w, metadata: metadata}
		request := r.WithContext(context.WithValue(r.Context(), accessLogContextKey{}, metadata))
		next.ServeHTTP(writer, request)
		status := writer.status
		if status == 0 {
			status = http.StatusOK
		}
		snapshot := metadata.snapshot()
		if snapshot.canceled || errors.Is(r.Context().Err(), context.Canceled) {
			status = 499
		} else if status < http.StatusBadRequest && snapshot.failureStatus >= http.StatusBadRequest {
			status = snapshot.failureStatus
		}
		if admin != nil {
			admin.RecordAccessLog(AccessLog{
				Status: status, Latency: time.Since(started), FirstEvent: snapshot.firstEvent,
				FirstContent: snapshot.firstContent, UpstreamBytes: snapshot.upstreamBytes,
				ContentChars: snapshot.contentChars, OutputTokens: snapshot.outputTokens,
				ReasoningTokens: snapshot.reasoningTokens,
				InputMessages:   snapshot.inputMessages, InputTextChars: snapshot.inputTextChars,
				InputMedia: snapshot.inputMedia, InputMediaBytes: snapshot.inputMediaBytes, InputFiles: snapshot.inputFiles,
				Temperature: snapshot.temperature, TopP: snapshot.topP,
				Thinking: snapshot.thinking, MaxOutputTokens: snapshot.maxOutputTokens,
				RequestID: snapshot.requestID,
				Method:    r.Method, Path: r.URL.Path, Model: snapshot.model, Account: snapshot.account,
				FinishReason: snapshot.finishReason, Error: snapshot.requestErr,
				Canceled: snapshot.canceled, Generation: snapshot.generation,
			})
		}
	})
}

func setAccessLogResponseError(w http.ResponseWriter, message string) {
	if writer, ok := w.(interface{ setError(string) }); ok {
		writer.setError(message)
	}
}

func corsMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Headers", "Authorization, Content-Type, X-API-Key, X-Goog-API-Key, Anthropic-Version, Anthropic-Beta")
		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PUT, DELETE, OPTIONS")
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func loopbackMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		host, _, err := net.SplitHostPort(r.RemoteAddr)
		if err != nil || !net.ParseIP(host).IsLoopback() {
			writeAdminError(w, http.StatusForbidden, "control_plane_forbidden", "Control plane is only available from loopback")
			return
		}
		next.ServeHTTP(w, r)
	})
}

func sameOriginMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		originValue := strings.TrimSpace(r.Header.Get("Origin"))
		if originValue == "" {
			next.ServeHTTP(w, r)
			return
		}
		origin, err := url.Parse(originValue)
		if err != nil || origin.Host == "" || !strings.EqualFold(origin.Host, r.Host) {
			writeAdminError(w, http.StatusForbidden, "control_plane_origin_forbidden", "Control plane requires a same-origin browser request")
			return
		}
		next.ServeHTTP(w, r)
	})
}

func authMiddleware(requiredKey string, next http.Handler) http.Handler {
	requiredKey = strings.TrimSpace(requiredKey)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if requiredKey == "" {
			next.ServeHTTP(w, r)
			return
		}
		provided := requestAPIKey(r)
		if subtle.ConstantTimeCompare([]byte(provided), []byte(requiredKey)) != 1 {
			writeAuthError(w, r)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func requestAPIKey(r *http.Request) string {
	if key := strings.TrimSpace(r.URL.Query().Get("key")); key != "" {
		return key
	}
	if key := strings.TrimSpace(r.Header.Get("X-Goog-API-Key")); key != "" {
		return key
	}
	if key := strings.TrimSpace(r.Header.Get("X-API-Key")); key != "" {
		return key
	}
	authorization := strings.TrimSpace(r.Header.Get("Authorization"))
	scheme, key, ok := strings.Cut(authorization, " ")
	if ok && strings.EqualFold(scheme, "Bearer") {
		return strings.TrimSpace(key)
	}
	return ""
}
