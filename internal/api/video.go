package api

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"mime"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/Mag1cFall/AIStudio2API/internal/aistudio"
)

type geminiVideoRequest struct {
	Instances []struct {
		Prompt string                 `json:"prompt"`
		Image  *geminiVideoImageInput `json:"image"`
	} `json:"instances"`
	Parameters struct {
		NumberOfVideos int             `json:"numberOfVideos"`
		SampleCount    int             `json:"sampleCount"`
		AspectRatio    string          `json:"aspectRatio"`
		Duration       json.RawMessage `json:"durationSeconds"`
		Resolution     string          `json:"resolution"`
	} `json:"parameters"`
}

type geminiVideoImageInput struct {
	InlineData *geminiVideoInlineData `json:"inlineData"`
	FileData   *geminiVideoFileData   `json:"fileData"`
}

type geminiVideoInlineData struct {
	MIMEType string `json:"mimeType"`
	Data     string `json:"data"`
}

type geminiVideoFileData struct {
	MIMEType string `json:"mimeType"`
	FileURI  string `json:"fileUri"`
}

type openAIVideoRequest struct {
	Model          string `json:"model"`
	Prompt         string `json:"prompt"`
	Seconds        string `json:"seconds"`
	Size           string `json:"size"`
	InputReference string `json:"input_reference"`
}

type openAIVideoState struct {
	Model     string
	Seconds   string
	Size      string
	CreatedAt int64
}

func (s *server) handleGeminiVideoCreate(w http.ResponseWriter, r *http.Request, model string) {
	service, ok := s.service.(aistudio.VideoService)
	if !ok {
		writeGeminiError(w, http.StatusBadRequest, "INVALID_ARGUMENT", "video generation is unavailable")
		return
	}
	var request geminiVideoRequest
	if err := decodeJSON(r, &request); err != nil {
		writeGeminiError(w, http.StatusBadRequest, "INVALID_ARGUMENT", err.Error())
		return
	}
	videoRequest, err := request.toVideoRequest(model)
	if err != nil {
		writeGeminiError(w, http.StatusBadRequest, "INVALID_ARGUMENT", err.Error())
		return
	}
	videoRequest = normalizeVideoDefaults(videoRequest)
	operation, err := service.GenerateVideo(r.Context(), videoRequest)
	if err != nil {
		writeGeminiError(w, statusFromError(err), geminiErrorStatus(err), err.Error())
		return
	}
	s.videoStates.Store(operation.ID, openAIVideoState{
		Model: model, Seconds: strconv.Itoa(videoRequest.DurationSeconds), Size: geminiVideoSize(videoRequest), CreatedAt: time.Now().Unix(),
	})
	writeJSON(w, http.StatusOK, map[string]any{"name": "operations/" + operation.ID})
}

func (request geminiVideoRequest) toVideoRequest(model string) (aistudio.VideoRequest, error) {
	if len(request.Instances) != 1 || strings.TrimSpace(request.Instances[0].Prompt) == "" {
		return aistudio.VideoRequest{}, fmt.Errorf("instances must contain one prompt")
	}
	count := request.Parameters.NumberOfVideos
	if count == 0 {
		count = request.Parameters.SampleCount
	}
	duration, err := videoDuration(request.Parameters.Duration)
	if err != nil {
		return aistudio.VideoRequest{}, err
	}
	result := aistudio.VideoRequest{
		Model: model, Prompt: request.Instances[0].Prompt, Count: count,
		AspectRatio: request.Parameters.AspectRatio, DurationSeconds: duration, Resolution: request.Parameters.Resolution,
	}
	if request.Instances[0].Image != nil {
		result.StartImage, err = geminiVideoImage(request.Instances[0].Image)
		if err != nil {
			return aistudio.VideoRequest{}, err
		}
	}
	return result, nil
}

func geminiVideoImage(input *geminiVideoImageInput) (*aistudio.VideoImage, error) {
	if input.InlineData != nil && input.FileData != nil {
		return nil, fmt.Errorf("image must contain exactly one of inlineData or fileData")
	}
	if input.InlineData != nil {
		data, err := base64.StdEncoding.DecodeString(input.InlineData.Data)
		if err != nil {
			return nil, fmt.Errorf("image.inlineData.data: %w", err)
		}
		return &aistudio.VideoImage{InlineData: &aistudio.Blob{MIME: input.InlineData.MIMEType, Data: data}}, nil
	}
	if input.FileData != nil {
		return &aistudio.VideoImage{File: &aistudio.FileRef{ID: input.FileData.FileURI, MIME: input.FileData.MIMEType}}, nil
	}
	return nil, fmt.Errorf("image requires inlineData or fileData")
}

func videoDuration(raw json.RawMessage) (int, error) {
	if len(raw) == 0 || string(raw) == "null" {
		return 0, nil
	}
	var value string
	if err := json.Unmarshal(raw, &value); err == nil {
		duration, err := strconv.Atoi(value)
		if err != nil {
			return 0, fmt.Errorf("durationSeconds must be an integer")
		}
		return duration, nil
	}
	var duration int
	if err := json.Unmarshal(raw, &duration); err != nil {
		return 0, fmt.Errorf("durationSeconds must be an integer")
	}
	return duration, nil
}

func (s *server) handleGeminiVideoOperation(w http.ResponseWriter, r *http.Request) {
	service, ok := s.service.(aistudio.VideoService)
	if !ok {
		writeGeminiError(w, http.StatusBadRequest, "INVALID_ARGUMENT", "video generation is unavailable")
		return
	}
	operationID := cleanOperationID(r.PathValue("operation"))
	operation, err := service.GetGenerateVideoOperation(r.Context(), operationID)
	if err != nil {
		writeGeminiError(w, statusFromError(err), geminiErrorStatus(err), err.Error())
		return
	}
	response := map[string]any{"name": "operations/" + operationID, "done": operation.Done}
	if operation.Done {
		samples := []any{}
		if operation.File != nil {
			samples = append(samples, map[string]any{"video": map[string]any{
				"uri": videoContentURL(r, operationID), "mimeType": "video/mp4",
			}})
		}
		response["response"] = map[string]any{"generateVideoResponse": map[string]any{"generatedSamples": samples}}
	}
	writeJSON(w, http.StatusOK, response)
}

func (s *server) handleOpenAIVideoCreate(w http.ResponseWriter, r *http.Request) {
	service, ok := s.service.(aistudio.VideoService)
	if !ok {
		writeOpenAIError(w, http.StatusBadRequest, "invalid_request", "video generation is unavailable")
		return
	}
	request, image, err := parseOpenAIVideoRequest(r)
	if err != nil {
		writeOpenAIError(w, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}
	videoRequest, err := request.toVideoRequest(image)
	if err != nil {
		writeOpenAIError(w, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}
	videoRequest = normalizeVideoDefaults(videoRequest)
	operation, err := service.GenerateVideo(r.Context(), videoRequest)
	if err != nil {
		writeOpenAIError(w, statusFromError(err), openAIErrorCode(err), err.Error())
		return
	}
	state := openAIVideoState{Model: videoRequest.Model, Seconds: strconv.Itoa(videoRequest.DurationSeconds), Size: request.Size, CreatedAt: time.Now().Unix()}
	if state.Size == "" {
		state.Size = geminiVideoSize(videoRequest)
	}
	s.videoStates.Store(operation.ID, state)
	writeJSON(w, http.StatusOK, openAIVideoObject(operation, state))
}

func parseOpenAIVideoRequest(r *http.Request) (openAIVideoRequest, *aistudio.VideoImage, error) {
	mediaType, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if err != nil {
		return openAIVideoRequest{}, nil, fmt.Errorf("invalid Content-Type")
	}
	var request openAIVideoRequest
	var image *aistudio.VideoImage
	switch mediaType {
	case "application/json":
		if err := decodeJSON(r, &request); err != nil {
			return request, nil, err
		}
		if request.InputReference != "" {
			part, err := fileOrInlinePart(request.InputReference, "")
			if err != nil {
				return request, nil, err
			}
			image = &aistudio.VideoImage{InlineData: part.InlineData, File: part.File}
		}
	case "multipart/form-data":
		if err := r.ParseMultipartForm(32 << 20); err != nil {
			return request, nil, err
		}
		defer r.MultipartForm.RemoveAll()
		request.Model = r.FormValue("model")
		request.Prompt = r.FormValue("prompt")
		request.Seconds = r.FormValue("seconds")
		request.Size = r.FormValue("size")
		file, header, err := r.FormFile("input_reference")
		if err == nil {
			defer file.Close()
			data, readErr := io.ReadAll(file)
			if readErr != nil {
				return request, nil, readErr
			}
			mimeType := header.Header.Get("Content-Type")
			if mimeType == "" {
				mimeType = http.DetectContentType(data)
			}
			image = &aistudio.VideoImage{InlineData: &aistudio.Blob{MIME: mimeType, Data: data}}
		} else if err != http.ErrMissingFile {
			return request, nil, err
		}
	default:
		return request, nil, fmt.Errorf("Content-Type must be application/json or multipart/form-data")
	}
	return request, image, nil
}

func (request openAIVideoRequest) toVideoRequest(image *aistudio.VideoImage) (aistudio.VideoRequest, error) {
	if strings.TrimSpace(request.Prompt) == "" {
		return aistudio.VideoRequest{}, fmt.Errorf("prompt is required")
	}
	if strings.TrimSpace(request.Model) == "" {
		return aistudio.VideoRequest{}, fmt.Errorf("model is required")
	}
	duration := 0
	if request.Seconds != "" {
		var err error
		duration, err = strconv.Atoi(request.Seconds)
		if err != nil {
			return aistudio.VideoRequest{}, fmt.Errorf("seconds must be an integer")
		}
	}
	aspectRatio, resolution, err := openAIVideoSize(request.Size)
	if err != nil {
		return aistudio.VideoRequest{}, err
	}
	return aistudio.VideoRequest{
		Model: request.Model, Prompt: request.Prompt, Count: 1, DurationSeconds: duration,
		AspectRatio: aspectRatio, Resolution: resolution, StartImage: image,
	}, nil
}

func openAIVideoSize(size string) (string, string, error) {
	switch strings.ToLower(strings.TrimSpace(size)) {
	case "":
		return "", "", nil
	case "1280x720":
		return "16:9", "720p", nil
	case "720x1280":
		return "9:16", "720p", nil
	case "1792x1024", "1920x1080":
		return "16:9", "1080p", nil
	case "1024x1792", "1080x1920":
		return "9:16", "1080p", nil
	default:
		return "", "", fmt.Errorf("unsupported size %q", size)
	}
}

func (s *server) handleOpenAIVideoGet(w http.ResponseWriter, r *http.Request) {
	service, ok := s.service.(aistudio.VideoService)
	if !ok {
		writeOpenAIError(w, http.StatusBadRequest, "invalid_request", "video generation is unavailable")
		return
	}
	operationID := cleanOperationID(r.PathValue("video"))
	operation, err := service.GetGenerateVideoOperation(r.Context(), operationID)
	if err != nil {
		writeOpenAIError(w, statusFromError(err), openAIErrorCode(err), err.Error())
		return
	}
	state, _ := s.videoStates.Load(operationID)
	metadata, _ := state.(openAIVideoState)
	writeJSON(w, http.StatusOK, openAIVideoObject(operation, metadata))
}

func openAIVideoObject(operation aistudio.VideoOperation, state openAIVideoState) map[string]any {
	status := "queued"
	progress := 0
	if operation.Done {
		progress = 100
		if operation.File != nil {
			status = "completed"
		} else {
			status = "failed"
		}
	}
	return map[string]any{
		"id": operation.ID, "object": "video", "model": state.Model,
		"status": status, "progress": progress, "created_at": state.CreatedAt,
		"size": state.Size, "seconds": state.Seconds,
	}
}

func (s *server) handleOpenAIVideoContent(w http.ResponseWriter, r *http.Request) {
	service, ok := s.service.(aistudio.VideoService)
	if !ok {
		writeOpenAIError(w, http.StatusBadRequest, "invalid_request", "video generation is unavailable")
		return
	}
	if variant := r.URL.Query().Get("variant"); variant != "" && variant != "video" {
		writeOpenAIError(w, http.StatusBadRequest, "invalid_request", "only the video variant is available")
		return
	}
	operationID := cleanOperationID(r.PathValue("video"))
	operation, err := service.GetGenerateVideoOperation(r.Context(), operationID)
	if err != nil {
		writeOpenAIError(w, statusFromError(err), openAIErrorCode(err), err.Error())
		return
	}
	if !operation.Done || operation.File == nil {
		writeOpenAIError(w, http.StatusConflict, "video_not_ready", "video is not ready")
		return
	}
	media, err := service.DownloadFile(r.Context(), operation.File.ID)
	if err != nil {
		writeOpenAIError(w, statusFromError(err), openAIErrorCode(err), err.Error())
		return
	}
	mimeType := media.MIME
	if mimeType == "" {
		mimeType = "video/mp4"
	}
	w.Header().Set("Content-Type", mimeType)
	w.Header().Set("Content-Disposition", `attachment; filename="video.mp4"`)
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(media.Data)
}

func cleanOperationID(value string) string {
	return strings.TrimPrefix(strings.TrimSpace(value), "operations/")
}

func videoContentURL(r *http.Request, operationID string) string {
	scheme := "http"
	if r.TLS != nil {
		scheme = "https"
	}
	if forwarded := strings.TrimSpace(r.Header.Get("X-Forwarded-Proto")); forwarded != "" {
		scheme = forwarded
	}
	return scheme + "://" + r.Host + "/v1/videos/" + operationID + "/content"
}

func geminiVideoSize(request aistudio.VideoRequest) string {
	request = normalizeVideoDefaults(request)
	if request.Resolution == "1080p" {
		if request.AspectRatio == "9:16" {
			return "1080x1920"
		}
		return "1920x1080"
	}
	if request.Resolution == "4k" {
		if request.AspectRatio == "9:16" {
			return "2160x3840"
		}
		return "3840x2160"
	}
	if request.AspectRatio == "9:16" {
		return "720x1280"
	}
	return "1280x720"
}

func normalizeVideoDefaults(request aistudio.VideoRequest) aistudio.VideoRequest {
	if request.DurationSeconds == 0 {
		request.DurationSeconds = 4
	}
	if request.AspectRatio == "" {
		request.AspectRatio = "16:9"
	}
	if request.Resolution == "" {
		request.Resolution = "720p"
	}
	return request
}
