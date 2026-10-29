package api

import (
	"net/http"

	"github.com/Mag1cFall/AIStudio2API/internal/aistudio"
)

// geminiPredictRequest 是 Gemini API Imagen predict 请求
type geminiPredictRequest struct {
	Instances []struct {
		Prompt string `json:"prompt"`
	} `json:"instances"`
	Parameters struct {
		SampleCount      int    `json:"sampleCount"`
		AspectRatio      string `json:"aspectRatio"`
		NegativePrompt   string `json:"negativePrompt"`
		ImageSize        string `json:"imageSize"`
		IncludeRAIReason bool   `json:"includeRaiReason"`
	} `json:"parameters"`
}

// handleGeminiPredict 把 Imagen predict 请求转为 GenerateImage，响应为 predictions 中的 base64 图片
func (s *server) handleGeminiPredict(w http.ResponseWriter, r *http.Request, model string) {
	service, ok := s.service.(aistudio.ImageService)
	if !ok {
		writeGeminiError(w, http.StatusBadRequest, "INVALID_ARGUMENT", "image generation is unavailable")
		return
	}
	var request geminiPredictRequest
	if err := decodeJSON(r, &request); err != nil {
		writeGeminiError(w, http.StatusBadRequest, "INVALID_ARGUMENT", err.Error())
		return
	}
	if len(request.Instances) != 1 {
		writeGeminiError(w, http.StatusBadRequest, "INVALID_ARGUMENT", "instances must contain one instance")
		return
	}
	var stopKeepalive func()
	w, stopKeepalive = s.nonstreamKeepalive(w, r)
	defer stopKeepalive()
	images, err := service.GenerateImage(r.Context(), aistudio.ImageRequest{
		Model:          model,
		Prompt:         request.Instances[0].Prompt,
		SampleCount:    request.Parameters.SampleCount,
		NegativePrompt: request.Parameters.NegativePrompt,
		AspectRatio:    request.Parameters.AspectRatio,
		ImageSize:      request.Parameters.ImageSize,
	})
	if err != nil {
		if shouldWriteRequestError(r, err) {
			writeGeminiRequestError(w, err)
		}
		return
	}
	predictions := make([]map[string]any, 0, len(images))
	for _, image := range images {
		switch {
		case image.BlockedReason != "":
			if request.Parameters.IncludeRAIReason {
				predictions = append(predictions, map[string]any{"raiFilteredReason": image.BlockedReason})
			}
		case image.Data != "" && image.MIMEType != "":
			predictions = append(predictions, map[string]any{"bytesBase64Encoded": image.Data, "mimeType": image.MIMEType})
		}
	}
	payload := map[string]any{}
	if len(predictions) > 0 {
		payload["predictions"] = predictions
	}
	writeJSON(w, http.StatusOK, payload)
}
