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
	APIKey           string
	Admin            AdminService
	AdminAuthEnabled bool
	AdminUsername    string
	AdminPassword    string
	Ledger           RequestLedger
}

type server struct {
	service           aistudio.Service
	config            Config
	responseStates    *responseStateStore
	uploads           *geminiUploads
	thoughtSignatures *thoughtSignatureStore
	pairing           *pairingTokens
}

var idSequence atomic.Uint64

// NewHandler 创建公开 API 路由
func NewHandler(service aistudio.Service, config Config) http.Handler {
	s := &server{
		service: service, config: config, responseStates: newResponseStateStore(), uploads: newGeminiUploads(),
		thoughtSignatures: newThoughtSignatureStore(), pairing: &pairingTokens{},
	}
	public := http.NewServeMux()
	public.HandleFunc("GET /v1/models", s.handleOpenAIModels)
	public.HandleFunc("GET /v1/models/{model...}", s.handleOpenAIModel)
	public.HandleFunc("POST /v1/chat/completions", s.handleChatCompletions)
	public.HandleFunc("POST /v1/embeddings", s.handleOpenAIEmbeddings)
	public.HandleFunc("POST /v1/responses", s.handleResponses)
	public.HandleFunc("POST /v1/responses/input_tokens", s.handleResponsesInputTokens)
	public.HandleFunc("GET /v1/responses/{response}", s.handleResponseGet)
	public.HandleFunc("DELETE /v1/responses/{response}", s.handleResponseDelete)
	public.HandleFunc("GET /v1/responses/{response}/input_items", s.handleResponseInputItems)
	public.HandleFunc("POST /v1/responses/{response}/cancel", s.handleResponseCancel)
	public.HandleFunc("POST /v1/interactions", s.handleInteraction)
	public.HandleFunc("POST /v1beta/interactions", s.handleInteraction)
	public.HandleFunc("GET /v1/interactions/{id}", s.handleInteractionGet)
	public.HandleFunc("GET /v1beta/interactions/{id}", s.handleInteractionGet)
	public.HandleFunc("DELETE /v1/interactions/{id}", s.handleInteractionDelete)
	public.HandleFunc("DELETE /v1beta/interactions/{id}", s.handleInteractionDelete)
	public.HandleFunc("POST /v1/interactions/{id}/cancel", s.handleInteractionCancel)
	public.HandleFunc("POST /v1beta/interactions/{id}/cancel", s.handleInteractionCancel)
	public.HandleFunc("POST /v1/files", s.handleFileUpload)
	public.HandleFunc("GET /v1/files", s.handleFileList)
	public.HandleFunc("GET /v1/files/{file}", s.handleFileGet)
	public.HandleFunc("GET /v1/files/{file}/content", s.handleFileContent)
	public.HandleFunc("DELETE /v1/files/{file}", s.handleFileDelete)
	public.HandleFunc("POST /v1/images/generations", s.handleOpenAIImages)
	public.HandleFunc("POST /v1/images/edits", s.handleOpenAIImageEdits)
	public.HandleFunc("POST /v1/audio/speech", s.handleOpenAISpeech)
	public.HandleFunc("POST /v1/audio/transcriptions", s.handleOpenAITranscription)
	public.HandleFunc("POST /v1/audio/translations", s.handleOpenAITranslation)
	public.HandleFunc("GET /v1/live", s.handleGeminiLive)
	public.HandleFunc("GET /v1/robotics/stream", s.handleRoboticsStream)
	public.HandleFunc("POST /v1/videos", s.handleOpenAIVideoCreate)
	public.HandleFunc("GET /v1/videos", s.handleOpenAIVideoList)
	public.HandleFunc("GET /v1/videos/{video}", s.handleOpenAIVideoGet)
	public.HandleFunc("DELETE /v1/videos/{video}", s.handleOpenAIVideoDelete)
	public.HandleFunc("GET /v1/videos/{video}/content", s.handleOpenAIVideoContent)
	public.HandleFunc("POST /v1/messages", s.handleAnthropicMessages)
	public.HandleFunc("POST /v1/messages/count_tokens", s.handleAnthropicCountTokens)
	public.HandleFunc("GET /v1beta/models", s.handleGeminiModels)
	public.HandleFunc("GET /v1beta/models/{model...}", s.handleGeminiModel)
	public.HandleFunc("POST /v1beta/models/{action}", s.handleGeminiAction)
	public.HandleFunc("GET /v1beta/operations/{operation}", s.handleGeminiVideoOperation)
	public.HandleFunc("POST /upload/v1beta/files", s.handleGeminiFileUpload)
	public.HandleFunc("GET /v1beta/files", s.handleGeminiFileList)
	public.HandleFunc("GET /v1beta/files/{file}", s.handleGeminiFileGet)
	public.HandleFunc("DELETE /v1beta/files/{file}", s.handleGeminiFileDelete)

	control := http.NewServeMux()
	control.HandleFunc("GET /api/status", s.handleStatus)
	control.HandleFunc("GET /api/models", s.handleAdminModels)
	if config.Admin != nil {
		s.registerAdmin(control)
	}
	if config.Ledger != nil {
		s.registerLedger(control)
	}

	root := http.NewServeMux()
	root.Handle("GET /health", corsMiddleware(http.HandlerFunc(s.handleHealth)))
	publicHandler := bodyLimitMiddleware(browserOriginMiddleware(config.APIKey, authMiddleware(config.APIKey, requestBodyMiddleware(config.Ledger, public))))
	loggedHandler := requestLoggingMiddleware(config.Admin, corsMiddleware(publicHandler))
	root.Handle("/v1/", loggedHandler)
	root.Handle("/v1beta/", loggedHandler)
	root.Handle("/upload/", requestLoggingMiddleware(config.Admin, corsMiddleware(s.geminiUploadHandler(publicHandler))))
	root.Handle("/api/", newAdminAuth(config).handler(control))
	if config.Admin != nil {
		root.HandleFunc("POST /api/pairing/accounts", s.handlePairingAccount)
	}
	return root
}

func newID(prefix string) string {
	return fmt.Sprintf("%s_%d_%d", prefix, time.Now().UnixNano(), idSequence.Add(1))
}
