package api

import (
	"fmt"
	"net/http"
	"sync"
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
	videoStates    sync.Map
}

var idSequence atomic.Uint64

// NewHandler 创建公开 API 路由
func NewHandler(service aistudio.Service, config Config) http.Handler {
	s := &server{service: service, config: config, responseStates: newResponseStateStore()}
	public := http.NewServeMux()
	public.HandleFunc("GET /v1/models", s.handleOpenAIModels)
	public.HandleFunc("POST /v1/chat/completions", s.handleChatCompletions)
	public.HandleFunc("POST /v1/responses", s.handleResponses)
	public.HandleFunc("POST /v1/images/generations", s.handleOpenAIImages)
	public.HandleFunc("POST /v1/audio/speech", s.handleOpenAISpeech)
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
	root.Handle("/health", corsMiddleware(http.HandlerFunc(s.handleHealth)))
	root.Handle("/v1/", requestLoggingMiddleware(config.Admin, corsMiddleware(authMiddleware(config.APIKey, public))))
	root.Handle("/v1beta/", requestLoggingMiddleware(config.Admin, corsMiddleware(authMiddleware(config.APIKey, public))))
	root.Handle("/api/", loopbackMiddleware(sameOriginMiddleware(control)))
	return root
}

func newID(prefix string) string {
	return fmt.Sprintf("%s_%d_%d", prefix, time.Now().UnixNano(), idSequence.Add(1))
}

// publicModels 返回当前公开端点能够实际调用的模型
func publicModels(models []aistudio.Model) []aistudio.Model {
	result := make([]aistudio.Model, 0, len(models))
	for _, model := range models {
		if model.Capabilities["interaction_route"] || !hasPublicModelMethod(model.Methods) {
			continue
		}
		result = append(result, model)
	}
	return result
}

// hasPublicModelMethod 判断模型是否能由公开端点执行
func hasPublicModelMethod(methods []string) bool {
	for _, method := range methods {
		if method == "generateContent" || method == "predictLongRunning" {
			return true
		}
	}
	return false
}
