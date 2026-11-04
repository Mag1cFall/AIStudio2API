package api

import (
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"mime/multipart"
	"net/http"
	"slices"
	"strconv"
	"strings"

	"github.com/Mag1cFall/AIStudio2API/internal/aistudio"
)

// openAITranscriptionSegment 是 verbose_json 的 TranscriptionSegment，上游没有的解码统计取中性值
type openAITranscriptionSegment struct {
	ID               int     `json:"id"`
	Seek             int     `json:"seek"`
	Start            float64 `json:"start"`
	End              float64 `json:"end"`
	Text             string  `json:"text"`
	Tokens           []int   `json:"tokens"`
	Temperature      float64 `json:"temperature"`
	AvgLogprob       float64 `json:"avg_logprob"`
	CompressionRatio float64 `json:"compression_ratio"`
	NoSpeechProb     float64 `json:"no_speech_prob"`
	Speaker          string  `json:"speaker,omitempty"`
}

// openAIDiarizedSegment 是 diarized_json 与流式 transcript.text.segment 的分段
type openAIDiarizedSegment struct {
	Type    string  `json:"type"`
	ID      string  `json:"id"`
	Start   float64 `json:"start"`
	End     float64 `json:"end"`
	Text    string  `json:"text"`
	Speaker string  `json:"speaker"`
}

type openAITranscriptionWord struct {
	Word    string  `json:"word"`
	Start   float64 `json:"start"`
	End     float64 `json:"end"`
	Speaker string  `json:"speaker,omitempty"`
}

// audioTokensPerSecond 是 Gemini 音频输入每秒的 token 数
const audioTokensPerSecond = 32

const transcriptionMultipartMemory = 1 << 20

func (s *server) handleOpenAITranscription(w http.ResponseWriter, r *http.Request) {
	service, ok := s.service.(aistudio.TranscriptionService)
	if !ok {
		writeOpenAIError(w, http.StatusBadRequest, "invalid_request", "audio transcription is unavailable")
		return
	}
	if !parseAudioForm(w, r) {
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
	granularities, err := transcriptionGranularities(r.MultipartForm.Value)
	if err != nil {
		writeOpenAIError(w, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}
	stream, _, err := transcriptionBool(r, "stream")
	if err != nil {
		writeOpenAIError(w, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}
	wordTimestamps, wordTimestampsSet, err := transcriptionBool(r, "word_timestamps")
	if err != nil {
		writeOpenAIError(w, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}
	words := slices.Contains(granularities, "word") || wordTimestampsSet && wordTimestamps
	speakerLabels, speakerLabelsSet, err := transcriptionBool(r, "speaker_labels")
	if err != nil {
		writeOpenAIError(w, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}
	smartTranscription, _, err := transcriptionBool(r, "smart_transcription")
	if err != nil {
		writeOpenAIError(w, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}
	if smartTranscription && (wordTimestamps || speakerLabels) {
		writeOpenAIError(w, http.StatusBadRequest, "invalid_request", "smart_transcription cannot be combined with word_timestamps or speaker_labels")
		return
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
	timed := subtitleFormat(responseFormat) || responseFormat == "verbose_json" || responseFormat == "diarized_json"
	if timed && !wordTimestampsSet && !smartTranscription && len(vocabulary) == 0 {
		wordTimestamps = true
	}
	if responseFormat == "diarized_json" && !speakerLabelsSet && !smartTranscription {
		speakerLabels = true
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
	request := aistudio.TranscriptionRequest{
		ID: newID("transcription"), Model: model, Name: header.Filename, MIME: mimeType,
		Size: header.Size, Reader: file, Config: config,
	}
	if stream {
		streamTranscription(w, r, service, request, responseFormat)
		return
	}
	result, err := service.Transcribe(r.Context(), request)
	if err != nil && !shouldWriteRequestError(r, err) {
		return
	}
	if err != nil {
		writeOpenAIRequestError(w, err)
		return
	}
	writeTranscriptionResponse(w, responseFormat, "transcribe", language, result, words, wavSeconds(file, header.Size))
}

// streamTranscription 以 SSE 写出转录：每段正文为 transcript.text.delta，diarized_json 另为每个分段写出 transcript.text.segment，结束时写出带全文与用量的 transcript.text.done
func streamTranscription(w http.ResponseWriter, r *http.Request, service aistudio.TranscriptionService, request aistudio.TranscriptionRequest, responseFormat string) {
	started := false
	segments := 0
	request.Emit = func(event aistudio.Event) error {
		if !started {
			if err := streamHeaders(w); err != nil {
				return err
			}
			started = true
		}
		if responseFormat == "diarized_json" && event.Transcript != nil {
			segment := diarizedSegment(segments, *event.Transcript)
			segments++
			return writeSSE(w, "", segment)
		}
		return writeSSE(w, "", map[string]any{"type": "transcript.text.delta", "delta": event.Text})
	}
	result, err := service.Transcribe(r.Context(), request)
	if err != nil {
		if !shouldWriteRequestError(r, err) {
			return
		}
		if started {
			writeOpenAISSEError(w, err)
		} else {
			writeOpenAIRequestError(w, err)
		}
		return
	}
	if !started && streamHeaders(w) != nil {
		return
	}
	done := map[string]any{"type": "transcript.text.done", "text": result.Text}
	if usage := transcriptionTokens(result.Usage); usage != nil {
		done["usage"] = usage
	}
	_ = writeSSE(w, "", done)
}

// transcriptionGranularities 读取 timestamp_granularities 的取值，只接受 word 与 segment
func transcriptionGranularities(form map[string][]string) ([]string, error) {
	values := append(slices.Clone(form["timestamp_granularities[]"]), form["timestamp_granularities"]...)
	for _, value := range values {
		if value != "word" && value != "segment" {
			return nil, errors.New("timestamp_granularities must be word or segment")
		}
	}
	return values, nil
}

// parseAudioForm 限制请求体并解析转录与翻译的 multipart 表单，失败时写入 OpenAI 错误并返回 false
func parseAudioForm(w http.ResponseWriter, r *http.Request) bool {
	r.Body = http.MaxBytesReader(w, r.Body, openAIFileMaxBytes+openAIFileRequestOverhead)
	if err := r.ParseMultipartForm(transcriptionMultipartMemory); err != nil {
		if r.MultipartForm != nil {
			_ = r.MultipartForm.RemoveAll()
		}
		writeFileParseError(w, r, err)
		return false
	}
	return true
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

// transcriptionBool 读取表单中最后一个同名布尔字段，返回取值与是否给出
func transcriptionBool(r *http.Request, name string) (bool, bool, error) {
	values := r.MultipartForm.Value[name]
	if len(values) == 0 || strings.TrimSpace(values[len(values)-1]) == "" {
		return false, false, nil
	}
	parsed, err := strconv.ParseBool(strings.TrimSpace(values[len(values)-1]))
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
	if err != nil || math.IsNaN(temperature) || temperature < 0 || temperature > 2 {
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

// writeTranscriptionResponse 按 response_format 写出转录或翻译结果，words 为 true 时 verbose_json 带逐词时间，seconds 为从文件头读出的音频秒数
func writeTranscriptionResponse(w http.ResponseWriter, responseFormat string, task string, language string, result aistudio.TranscriptionResult, words bool, seconds float64) {
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
	writeJSON(w, http.StatusOK, officialOrder("openai", transcriptionBody(responseFormat, task, language, result, words, seconds)))
}

// transcriptionBody 返回 json、verbose_json 或 diarized_json 的响应体：json 与 diarized_json 带 token 用量，verbose_json 带按音频时长计的用量
func transcriptionBody(responseFormat string, task string, language string, result aistudio.TranscriptionResult, words bool, seconds float64) map[string]any {
	duration := transcriptionDuration(result, seconds)
	switch responseFormat {
	case "verbose_json":
		segments, items := openAITranscriptionMetadata(result.Segments)
		body := map[string]any{"task": task, "duration": duration, "text": result.Text, "segments": segments, "usage": map[string]any{"type": "duration", "seconds": duration}}
		if language != "" {
			body["language"] = language
		}
		if words {
			body["words"] = items
		}
		return body
	case "diarized_json":
		segments := make([]openAIDiarizedSegment, 0, len(result.Segments))
		for index, item := range result.Segments {
			segments = append(segments, diarizedSegment(index, item))
		}
		body := map[string]any{"task": task, "duration": duration, "text": result.Text, "segments": segments}
		if usage := transcriptionTokens(result.Usage); usage != nil {
			body["usage"] = usage
		}
		return body
	}
	body := map[string]any{"text": result.Text}
	if usage := transcriptionTokens(result.Usage); usage != nil {
		body["usage"] = usage
	}
	return body
}

// diarizedSegment 返回第 index 个上游转录分段对应的 diarized_json 分段
func diarizedSegment(index int, item aistudio.TranscriptMetadata) openAIDiarizedSegment {
	start, end := transcriptBounds(item.Words)
	return openAIDiarizedSegment{Type: "transcript.text.segment", ID: fmt.Sprintf("seg_%d", index), Start: start, End: end, Text: item.Text, Speaker: item.Speaker}
}

// transcriptionTokens 返回 type 为 tokens 的转录用量，上游没有用量时返回 nil
func transcriptionTokens(usage aistudio.Usage) map[string]any {
	if usage.TotalTokens == 0 && usage.InputTokens == 0 && usage.OutputTokens == 0 {
		return nil
	}
	tokens := map[string]any{"type": "tokens", "input_tokens": usage.InputTokens, "output_tokens": usage.OutputTokens, "total_tokens": usage.TotalTokens}
	if usage.Details != nil && len(usage.Details.PromptTokensDetails) > 0 {
		tokens["input_token_details"] = map[string]any{
			"audio_tokens": modalityTokens(usage.Details.PromptTokensDetails, "AUDIO"),
			"text_tokens":  modalityTokens(usage.Details.PromptTokensDetails, "TEXT"),
		}
	}
	return tokens
}

// transcriptionDuration 返回输入音频的秒数：有文件头秒数时取它，否则取最后一个词的结束时间与上游音频 token 数按每秒 32 个换算两者中的较大值
func transcriptionDuration(result aistudio.TranscriptionResult, seconds float64) float64 {
	if seconds > 0 {
		return seconds
	}
	duration := 0.0
	if details := result.Usage.Details; details != nil {
		duration = float64(modalityTokens(details.PromptTokensDetails, "AUDIO")) / audioTokensPerSecond
	}
	for _, item := range result.Segments {
		if _, end := transcriptBounds(item.Words); end > duration {
			duration = end
		}
	}
	return duration
}

// wavSeconds 按 RIFF WAVE 文件头的 data 块长度与 fmt 块字节率返回音频秒数，不是 WAV 或缺少这两个块时返回 0
func wavSeconds(file io.ReaderAt, size int64) float64 {
	chunk := make([]byte, 16)
	if _, err := file.ReadAt(chunk[:12], 0); err != nil || string(chunk[:4]) != "RIFF" || string(chunk[8:12]) != "WAVE" {
		return 0
	}
	byteRate := uint32(0)
	for offset := int64(12); offset+8 <= size; {
		if _, err := file.ReadAt(chunk[:8], offset); err != nil {
			return 0
		}
		length := int64(binary.LittleEndian.Uint32(chunk[4:8]))
		start := offset + 8
		switch string(chunk[:4]) {
		case "fmt ":
			if _, err := file.ReadAt(chunk, start); err != nil {
				return 0
			}
			byteRate = binary.LittleEndian.Uint32(chunk[8:12])
		case "data":
			if byteRate == 0 {
				return 0
			}
			return float64(min(length, size-start)) / float64(byteRate)
		}
		offset = start + length + length%2
	}
	return 0
}

func openAITranscriptionMetadata(metadata []aistudio.TranscriptMetadata) ([]openAITranscriptionSegment, []openAITranscriptionWord) {
	segments := make([]openAITranscriptionSegment, 0, len(metadata))
	words := make([]openAITranscriptionWord, 0)
	for index, item := range metadata {
		start, end := transcriptBounds(item.Words)
		segments = append(segments, openAITranscriptionSegment{
			ID: index, Start: start, End: end, Text: item.Text, Tokens: []int{}, Speaker: item.Speaker,
		})
		for _, word := range item.Words {
			words = append(words, openAITranscriptionWord{
				Word: word.Word, Start: transcriptSeconds(word.Start), End: transcriptSeconds(word.End), Speaker: item.Speaker,
			})
		}
	}
	return segments, words
}

// transcriptionSubtitles 按转录分段渲染 SRT 或 WebVTT 字幕
func transcriptionSubtitles(format string, metadata []aistudio.TranscriptMetadata) string {
	segments, _ := openAITranscriptionMetadata(metadata)
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

func transcriptBounds(words []aistudio.TranscriptWord) (float64, float64) {
	if len(words) == 0 {
		return 0, 0
	}
	return transcriptSeconds(words[0].Start), transcriptSeconds(words[len(words)-1].End)
}

func transcriptSeconds(duration aistudio.TranscriptDuration) float64 {
	return float64(duration.Seconds) + float64(duration.Nanos)/1_000_000_000
}
