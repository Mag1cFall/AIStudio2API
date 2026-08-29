package api

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"strconv"
	"strings"

	"github.com/Mag1cFall/AIStudio2API/internal/aistudio"
)

type openAITranscriptionSegment struct {
	ID      int     `json:"id"`
	Start   float64 `json:"start"`
	End     float64 `json:"end"`
	Text    string  `json:"text"`
	Speaker string  `json:"speaker,omitempty"`
}

type openAITranscriptionWord struct {
	Word    string  `json:"word"`
	Start   float64 `json:"start"`
	End     float64 `json:"end"`
	Speaker string  `json:"speaker,omitempty"`
}

type openAITranscriptionUsage struct {
	InputTokens  int64 `json:"input_tokens"`
	OutputTokens int64 `json:"output_tokens"`
	TotalTokens  int64 `json:"total_tokens"`
}

type openAITranscriptionResponse struct {
	Task     string                       `json:"task,omitempty"`
	Language string                       `json:"language,omitempty"`
	Duration float64                      `json:"duration,omitempty"`
	Text     string                       `json:"text"`
	Segments []openAITranscriptionSegment `json:"segments,omitempty"`
	Words    []openAITranscriptionWord    `json:"words,omitempty"`
	Usage    *openAITranscriptionUsage    `json:"usage,omitempty"`
}

const transcriptionMultipartMemory = 1 << 20

func (s *server) handleOpenAITranscription(w http.ResponseWriter, r *http.Request) {
	service, ok := s.service.(aistudio.TranscriptionService)
	if !ok {
		writeOpenAIError(w, http.StatusBadRequest, "invalid_request", "audio transcription is unavailable")
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, openAIFileMaxBytes+openAIFileRequestOverhead)
	if err := r.ParseMultipartForm(transcriptionMultipartMemory); err != nil {
		if r.MultipartForm != nil {
			_ = r.MultipartForm.RemoveAll()
		}
		writeOpenAIFileParseError(w, err)
		return
	}
	defer r.MultipartForm.RemoveAll()
	model := strings.TrimPrefix(strings.TrimSpace(r.FormValue("model")), "models/")
	if model == "" {
		model = aistudio.DefaultTranscriptionModel
	}
	if strings.TrimSpace(r.FormValue("prompt")) != "" {
		writeOpenAIError(w, http.StatusBadRequest, "invalid_request", "prompt is not supported by the observed transcription protocol")
		return
	}
	responseFormat := strings.TrimSpace(r.FormValue("response_format"))
	if responseFormat == "" {
		responseFormat = "json"
	}
	if !supportedTranscriptionResponseFormat(responseFormat) {
		writeOpenAIError(w, http.StatusBadRequest, "invalid_request", "response_format must be json, text, verbose_json, or diarized_json")
		return
	}
	wordTimestamps, wordTimestampsSet, err := transcriptionBool(r, "word_timestamps", false)
	if err != nil {
		writeOpenAIError(w, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}
	speakerLabels, speakerLabelsSet, err := transcriptionBool(r, "speaker_labels", false)
	if err != nil {
		writeOpenAIError(w, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}
	smartTranscription, _, err := transcriptionBool(r, "smart_transcription", false)
	if err != nil {
		writeOpenAIError(w, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}
	if smartTranscription {
		if wordTimestampsSet && wordTimestamps || speakerLabelsSet && speakerLabels {
			writeOpenAIError(w, http.StatusBadRequest, "invalid_request", "smart_transcription cannot be combined with word_timestamps or speaker_labels")
			return
		}
		wordTimestamps = false
		speakerLabels = false
	}
	temperature, err := transcriptionTemperature(r.FormValue("temperature"))
	if err != nil {
		writeOpenAIError(w, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}
	language := normalizeTranscriptionLanguage(r.FormValue("language"))
	vocabulary, err := transcriptionVocabulary(r.MultipartForm.Value["custom_vocabulary"])
	if err != nil {
		writeOpenAIError(w, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}
	if len(vocabulary) > 0 && wordTimestamps {
		writeOpenAIError(w, http.StatusBadRequest, "invalid_request", "custom_vocabulary cannot be combined with word_timestamps")
		return
	}
	file, header, err := r.FormFile("file")
	if err != nil {
		writeOpenAIError(w, http.StatusBadRequest, "invalid_request", "file is required")
		return
	}
	defer file.Close()
	if strings.TrimSpace(header.Filename) == "" {
		writeOpenAIError(w, http.StatusBadRequest, "invalid_request", "filename is required")
		return
	}
	if header.Size <= 0 {
		writeOpenAIError(w, http.StatusBadRequest, "invalid_request", "file must not be empty")
		return
	}
	if header.Size > openAIFileMaxBytes {
		writeOpenAIError(w, http.StatusRequestEntityTooLarge, "file_too_large", "file exceeds 512 MB")
		return
	}
	mimeType, err := multipartFileMIME(file, header.Header.Get("Content-Type"))
	if err != nil {
		writeOpenAIError(w, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}
	if !supportedTranscriptionMIME(mimeType) {
		writeOpenAIError(w, http.StatusBadRequest, "invalid_request", "file must contain supported audio")
		return
	}
	config := aistudio.GenerationConfig{
		Temperature: temperature,
		TranscriptionConfig: &aistudio.TranscriptionConfig{
			WordTimestamps: wordTimestamps, SpeakerLabels: speakerLabels,
			SmartTranscription: smartTranscription, CustomVocabulary: vocabulary,
		},
	}
	if language != "" {
		config.TranscriptionConfig.LanguageCodes = []string{language}
	}
	result, err := service.Transcribe(r.Context(), aistudio.TranscriptionRequest{
		ID: newID("transcription"), Model: model, Name: header.Filename, MIME: mimeType,
		Size: header.Size, Reader: file, Config: config,
	})
	if err != nil && !shouldWriteRequestError(r, err) {
		return
	}
	if err != nil {
		writeOpenAIError(w, statusFromError(err), openAIErrorCode(err), err.Error())
		return
	}
	writeTranscriptionResponse(w, responseFormat, language, result)
}

func supportedTranscriptionResponseFormat(value string) bool {
	switch value {
	case "json", "text", "verbose_json", "diarized_json":
		return true
	default:
		return false
	}
}

func transcriptionBool(r *http.Request, name string, defaultValue bool) (bool, bool, error) {
	values, exists := r.MultipartForm.Value[name]
	if !exists || len(values) == 0 || strings.TrimSpace(values[len(values)-1]) == "" {
		return defaultValue, false, nil
	}
	value := strings.TrimSpace(values[len(values)-1])
	parsed, err := strconv.ParseBool(value)
	if err != nil {
		return false, true, fmt.Errorf("%s must be true or false", name)
	}
	return parsed, true, nil
}

func transcriptionTemperature(value string) (*float64, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return nil, nil
	}
	temperature, err := strconv.ParseFloat(value, 64)
	if err != nil || math.IsNaN(temperature) || math.IsInf(temperature, 0) || temperature < 0 || temperature > 2 {
		return nil, errors.New("temperature must be between 0 and 2")
	}
	return &temperature, nil
}

func normalizeTranscriptionLanguage(value string) string {
	value = strings.TrimSpace(value)
	if strings.EqualFold(value, "detect") || strings.EqualFold(value, "auto") {
		return ""
	}
	return value
}

func transcriptionVocabulary(values []string) ([]string, error) {
	result := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		if strings.HasPrefix(value, "[") {
			var decoded []string
			if err := json.Unmarshal([]byte(value), &decoded); err != nil {
				return nil, errors.New("custom_vocabulary must be repeated text fields or a JSON string array")
			}
			for _, item := range decoded {
				if item = strings.TrimSpace(item); item != "" {
					result = append(result, item)
				}
			}
			continue
		}
		result = append(result, value)
	}
	return result, nil
}

func supportedTranscriptionMIME(value string) bool {
	value = strings.ToLower(strings.TrimSpace(strings.Split(value, ";")[0]))
	return strings.HasPrefix(value, "audio/") || value == "video/mp4" || value == "video/webm"
}

func writeTranscriptionResponse(
	w http.ResponseWriter,
	responseFormat string,
	language string,
	result aistudio.TranscriptionResult,
) {
	if responseFormat == "text" {
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		w.WriteHeader(http.StatusOK)
		_, _ = io.WriteString(w, result.Text)
		return
	}
	response := openAITranscriptionResponse{Text: result.Text}
	if responseFormat != "json" {
		response.Task = "transcribe"
		response.Language = language
		response.Segments, response.Words, response.Duration = openAITranscriptionMetadata(result.Segments)
	}
	if result.Usage.TotalTokens != 0 || result.Usage.InputTokens != 0 || result.Usage.OutputTokens != 0 {
		response.Usage = &openAITranscriptionUsage{
			InputTokens: result.Usage.InputTokens, OutputTokens: result.Usage.OutputTokens,
			TotalTokens: result.Usage.TotalTokens,
		}
	}
	writeJSON(w, http.StatusOK, response)
}

func openAITranscriptionMetadata(
	metadata []aistudio.TranscriptMetadata,
) ([]openAITranscriptionSegment, []openAITranscriptionWord, float64) {
	segments := make([]openAITranscriptionSegment, 0, len(metadata))
	words := make([]openAITranscriptionWord, 0)
	duration := 0.0
	for index, item := range metadata {
		start, end := transcriptBounds(item.Timestamps)
		segments = append(segments, openAITranscriptionSegment{
			ID: index, Start: start, End: end, Text: item.Text, Speaker: item.Speaker,
		})
		if end > duration {
			duration = end
		}
		parts := strings.Fields(item.Text)
		if len(parts) != len(item.Timestamps) {
			continue
		}
		for timestampIndex, timestamp := range item.Timestamps {
			words = append(words, openAITranscriptionWord{
				Word: parts[timestampIndex], Start: transcriptSeconds(timestamp.Start),
				End: transcriptSeconds(timestamp.End), Speaker: item.Speaker,
			})
		}
	}
	return segments, words, duration
}

func transcriptBounds(timestamps []aistudio.TranscriptTimestamp) (float64, float64) {
	if len(timestamps) == 0 {
		return 0, 0
	}
	return transcriptSeconds(timestamps[0].Start), transcriptSeconds(timestamps[len(timestamps)-1].End)
}

func transcriptSeconds(duration aistudio.TranscriptDuration) float64 {
	return float64(duration.Seconds) + float64(duration.Nanos)/1_000_000_000
}
