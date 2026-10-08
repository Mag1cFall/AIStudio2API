package api

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"slices"
	"strings"

	"github.com/Mag1cFall/AIStudio2API/internal/aistudio"
)

// openAITranslationModel 是把转录文本译为英文的默认文本模型
const openAITranslationModel = "gemini-flash-latest"

// handleOpenAITranslation 先转录音频，再用文本模型把转录分段译为英文，按 OpenAI Translations 格式返回
func (s *server) handleOpenAITranslation(w http.ResponseWriter, r *http.Request) {
	service, ok := s.service.(aistudio.TranscriptionService)
	if !ok {
		writeOpenAIError(w, http.StatusBadRequest, "invalid_request", "audio translation is unavailable")
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, openAIFileMaxBytes+openAIFileRequestOverhead)
	if err := r.ParseMultipartForm(transcriptionMultipartMemory); err != nil {
		if r.MultipartForm != nil {
			_ = r.MultipartForm.RemoveAll()
		}
		writeFileParseError(w, r, err)
		return
	}
	defer r.MultipartForm.RemoveAll()
	responseFormat := strings.TrimSpace(r.FormValue("response_format"))
	if responseFormat == "" {
		responseFormat = "json"
	}
	if !slices.Contains([]string{"json", "text", "srt", "verbose_json", "vtt"}, responseFormat) {
		writeOpenAIError(w, http.StatusBadRequest, "invalid_request", "response_format must be json, text, srt, verbose_json or vtt")
		return
	}
	temperature, err := transcriptionTemperature(r.FormValue("temperature"))
	if err != nil {
		writeOpenAIError(w, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}
	transcribeModel, translateModel, err := s.translationModels(r.Context(), r.FormValue("model"))
	if err != nil {
		if shouldWriteRequestError(r, err) {
			writeOpenAIError(w, statusFromError(err), openAIErrorCode(err), err.Error())
		}
		return
	}
	file, header, mimeType, ok := openAIAudioFile(w, r)
	if !ok {
		return
	}
	defer file.Close()
	timestamps := responseFormat != "json" && responseFormat != "text"
	config := aistudio.GenerationConfig{}
	if timestamps {
		config.TranscriptionConfig = &aistudio.TranscriptionConfig{WordTimestamps: true}
	}
	transcript, err := service.Transcribe(r.Context(), aistudio.TranscriptionRequest{
		ID: newID("translation"), Model: transcribeModel, Name: header.Filename, MIME: mimeType,
		Size: header.Size, Reader: file, Config: config,
	})
	if err != nil {
		if shouldWriteRequestError(r, err) {
			writeOpenAIError(w, statusFromError(err), openAIErrorCode(err), err.Error())
		}
		return
	}
	segments := transcript.Segments
	if !timestamps || len(segments) == 0 {
		segments = []aistudio.TranscriptMetadata{{Text: transcript.Text}}
	}
	if strings.TrimSpace(transcript.Text) == "" {
		segments = nil
	} else {
		sources := make([]string, len(segments))
		for index, segment := range segments {
			sources[index] = strings.TrimSpace(segment.Text)
		}
		translated, err := s.translateSegments(r.Context(), translateModel, sources, strings.TrimSpace(r.FormValue("prompt")), temperature)
		if err != nil {
			if shouldWriteRequestError(r, err) {
				writeOpenAIError(w, statusFromError(err), openAIErrorCode(err), err.Error())
			}
			return
		}
		for index := range segments {
			segments[index].Text = strings.TrimSpace(translated[index])
		}
	}
	texts := make([]string, 0, len(segments))
	for _, segment := range segments {
		if segment.Text != "" {
			texts = append(texts, segment.Text)
		}
	}
	text := strings.Join(texts, " ")
	if responseFormat != "verbose_json" {
		writeTranscriptionResponse(w, responseFormat, "", aistudio.TranscriptionResult{Text: text, Segments: segments})
		return
	}
	items, _, duration := openAITranscriptionMetadata(segments)
	writeJSON(w, http.StatusOK, openAITranscriptionResponse{Task: "translate", Language: "english", Duration: duration, Text: text, Segments: items})
}

// translationModels 返回转录模型与翻译文本模型：model 是转录模型或省略时使用默认文本模型翻译，是其他模型时由默认转录模型转录、该模型翻译
func (s *server) translationModels(ctx context.Context, model string) (string, string, error) {
	model = strings.TrimPrefix(strings.TrimSpace(model), "models/")
	if model == "" {
		return aistudio.DefaultTranscriptionModel, openAITranslationModel, nil
	}
	models, err := s.service.Models(ctx)
	if err != nil {
		return "", "", err
	}
	if entry, ok := lookupPublicModel(models, model); ok && !entry.Capabilities["transcription_output"] {
		return aistudio.DefaultTranscriptionModel, entry.ID, nil
	}
	return model, openAITranslationModel, nil
}

// translateSegments 用文本模型把转录分段逐段译为英文，返回与输入数量和顺序相同的译文
func (s *server) translateSegments(ctx context.Context, model string, sources []string, prompt string, temperature *float64) ([]string, error) {
	encoded, _ := json.Marshal(sources)
	instruction := "Translate each transcript segment in the JSON array below into English. Return the translations in the segments array with the same number of items in the same order. Keep text that is already English unchanged.\n\n"
	if prompt != "" {
		instruction += "Context and style guidance: " + prompt + "\n\n"
	}
	schema := fmt.Sprintf(`{"type":"object","properties":{"segments":{"type":"array","items":{"type":"string"},"minItems":%d,"maxItems":%d}},"required":["segments"]}`, len(sources), len(sources))
	events, err := s.service.Generate(ctx, aistudio.GenerateRequest{
		ID: newID("translation"), Unary: true, Model: model,
		Contents: []aistudio.Content{{Role: aistudio.RoleUser, Parts: []aistudio.Part{{Text: instruction + string(encoded)}}}},
		Config:   aistudio.GenerationConfig{Temperature: temperature, ResponseMIMEType: "application/json", ResponseSchema: json.RawMessage(schema)},
	})
	if err != nil {
		return nil, err
	}
	result, err := consumeEvents(ctx, events, nil)
	if err != nil {
		return nil, err
	}
	var output struct {
		Segments []string `json:"segments"`
	}
	if err := json.Unmarshal([]byte(result.text.String()), &output); err != nil || len(output.Segments) != len(sources) {
		return nil, fmt.Errorf("AI Studio returned %d translated segments for %d transcript segments: %q", len(output.Segments), len(sources), result.text.String())
	}
	return output.Segments, nil
}
