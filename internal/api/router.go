package api

import (
	"fmt"
	"net/http"
	"sync/atomic"
	"time"

	"github.com/Mag1cFall/AIStudio2API/internal/aistudio"
)

// Config 定义公开 API 服务配置
type Config struct {
	APIKey string
	Admin  AdminService
}

type server struct {
	service        aistudio.Service
	config         Config
	responseStates *responseStateStore
}

var idSequence atomic.Uint64

// NewHandler 创建公开 API 路由
func NewHandler(service aistudio.Service, config Config) http.Handler {
	s := &server{service: service, config: config, responseStates: newResponseStateStore()}
	public := http.NewServeMux()
	public.HandleFunc("GET /v1/models", s.handleOpenAIModels)
	public.HandleFunc("POST /v1/chat/completions", s.handleChatCompletions)
	public.HandleFunc("POST /v1/responses", s.handleResponses)
	public.HandleFunc("POST /v1/files", s.handleOpenAIFileUpload)
	public.HandleFunc("GET /v1/files/{file}", s.handleOpenAIFileGet)
	public.HandleFunc("GET /v1/files/{file}/content", s.handleOpenAIFileContent)
	public.HandleFunc("DELETE /v1/files/{file}", s.handleOpenAIFileDelete)
	public.HandleFunc("POST /v1/images/generations", s.handleOpenAIImages)
	public.HandleFunc("POST /v1/audio/speech", s.handleOpenAISpeech)
	public.HandleFunc("POST /v1/audio/transcriptions", s.handleOpenAITranscription)
	public.HandleFunc("GET /v1/live", s.handleGeminiLive)
	public.HandleFunc("GET /v1/robotics/stream", s.handleRoboticsStream)
	public.HandleFunc("POST /v1/videos", s.handleOpenAIVideoCreate)
	public.HandleFunc("GET /v1/videos/{video}", s.handleOpenAIVideoGet)
	public.HandleFunc("GET /v1/videos/{video}/content", s.handleOpenAIVideoContent)
	public.HandleFunc("POST /v1/messages", s.handleAnthropicMessages)
	public.HandleFunc("POST /v1/messages/count_tokens", s.handleAnthropicCountTokens)
	public.HandleFunc("GET /v1beta/models", s.handleGeminiModels)
	public.HandleFunc("GET /v1beta/models/{model}", s.handleGeminiModel)
	public.HandleFunc("POST /v1beta/models/{action}", s.handleGeminiAction)
	public.HandleFunc("GET /v1beta/operations/{operation}", s.handleGeminiVideoOperation)

	control := http.NewServeMux()
	control.HandleFunc("GET /api/status", s.handleStatus)
	control.HandleFunc("GET /api/models", s.handleAdminModels)
	if config.Admin != nil {
		s.registerAdmin(control)
	}

	root := http.NewServeMux()
	root.Handle("GET /health", corsMiddleware(http.HandlerFunc(s.handleHealth)))
	root.Handle("/v1/", requestLoggingMiddleware(config.Admin, corsMiddleware(authMiddleware(config.APIKey, public))))
	root.Handle("/v1beta/", requestLoggingMiddleware(config.Admin, corsMiddleware(authMiddleware(config.APIKey, public))))
	root.Handle("/api/", loopbackMiddleware(sameOriginMiddleware(control)))
	return root
}

func newID(prefix string) string {
	return fmt.Sprintf("%s_%d_%d", prefix, time.Now().UnixNano(), idSequence.Add(1))
}
