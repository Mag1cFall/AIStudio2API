package api

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"mime/multipart"
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
		writeFileParseError(w, r, err)
		return
	}
	defer r.MultipartForm.RemoveAll()
	model := strings.TrimPrefix(strings.TrimSpace(r.FormValue("model")), "models/")
	if model == "" {
		model = aistudio.DefaultTranscriptionModel
	}
	responseFormat := strings.TrimSpace(r.FormValue("response_format"))
	if responseFormat == "" {
		responseFormat = "json"
	}
	if !supportedTranscriptionResponseFormat(responseFormat) {
		writeOpenAIError(w, http.StatusBadRequest, "invalid_request", "response_format must be json, text, srt, verbose_json, vtt, or diarized_json")
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
	if subtitleFormat(responseFormat) && !wordTimestampsSet && !smartTranscription && len(vocabulary) == 0 {
		wordTimestamps = true
	}
	if len(vocabulary) > 0 && wordTimestamps {
		writeOpenAIError(w, http.StatusBadRequest, "invalid_request", "custom_vocabulary cannot be combined with word_timestamps")
		return
	}
	file, header, mimeType, ok := openAIAudioFile(w, r)
	if !ok {
		return
	}
	defer file.Close()
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

// openAIAudioFile 读取 multipart 的 file 段并识别音频 MIME，失败时写入 OpenAI 错误并返回 false
func openAIAudioFile(w http.ResponseWriter, r *http.Request) (multipart.File, *multipart.FileHeader, string, bool) {
	file, header, err := r.FormFile("file")
	if err != nil {
		writeOpenAIError(w, http.StatusBadRequest, "invalid_request", "file is required")
		return nil, nil, "", false
	}
	fail := func(status int, code string, message string) (multipart.File, *multipart.FileHeader, string, bool) {
		_ = file.Close()
		writeOpenAIError(w, status, code, message)
		return nil, nil, "", false
	}
	if strings.TrimSpace(header.Filename) == "" {
		return fail(http.StatusBadRequest, "invalid_request", "filename is required")
	}
	if header.Size <= 0 {
		return fail(http.StatusBadRequest, "invalid_request", "file must not be empty")
	}
	if header.Size > openAIFileMaxBytes {
		return fail(http.StatusRequestEntityTooLarge, "file_too_large", "file exceeds 512 MB")
	}
	mimeType, err := multipartFileMIME(file, header.Header.Get("Content-Type"), header.Filename)
	if err != nil {
		return fail(http.StatusBadRequest, "invalid_request", err.Error())
	}
	if !supportedTranscriptionMIME(mimeType) {
		return fail(http.StatusBadRequest, "invalid_request", "file must contain supported audio")
	}
	return file, header, mimeType, true
}

func supportedTranscriptionResponseFormat(value string) bool {
	switch value {
	case "json", "text", "srt", "verbose_json", "vtt", "diarized_json":
		return true
	default:
		return false
	}
}

// subtitleFormat 判断转录格式是否为需要时间戳的字幕
func subtitleFormat(value string) bool {
	return value == "srt" || value == "vtt"
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
	if subtitleFormat(responseFormat) {
		contentType := "text/plain; charset=utf-8"
		if responseFormat == "vtt" {
			contentType = "text/vtt; charset=utf-8"
		}
		w.Header().Set("Content-Type", contentType)
		w.WriteHeader(http.StatusOK)
		_, _ = io.WriteString(w, transcriptionSubtitles(responseFormat, result.Segments))
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

// transcriptionSubtitles 按转录分段渲染 SRT 或 WebVTT 字幕
func transcriptionSubtitles(format string, metadata []aistudio.TranscriptMetadata) string {
	segments, _, _ := openAITranscriptionMetadata(metadata)
	var builder strings.Builder
	separator := ","
	if format == "vtt" {
		builder.WriteString("WEBVTT\n\n")
		separator = "."
	}
	for index, segment := range segments {
		if format == "srt" {
			fmt.Fprintf(&builder, "%d\n", index+1)
		}
		fmt.Fprintf(&builder, "%s --> %s\n%s\n\n",
			subtitleTimestamp(segment.Start, separator), subtitleTimestamp(segment.End, separator), strings.TrimSpace(segment.Text))
	}
	return builder.String()
}

// subtitleTimestamp 将秒数格式化为字幕时间戳
func subtitleTimestamp(seconds float64, separator string) string {
	milliseconds := int64(math.Round(seconds * 1000))
	return fmt.Sprintf("%02d:%02d:%02d%s%03d",
		milliseconds/3_600_000, milliseconds/60_000%60, milliseconds/1000%60, separator, milliseconds%1000)
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
