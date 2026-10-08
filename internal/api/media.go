package api

import (
	"bytes"
	"cmp"
	"context"
	"encoding/base64"
	"encoding/binary"
	"fmt"
	"image"
	"image/color"
	"image/draw"
	"image/gif"
	"image/png"
	"math"
	"mime"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/Mag1cFall/AIStudio2API/internal/aistudio"
)

type openAIImageRequest struct {
	Model          string `json:"model"`
	Prompt         string `json:"prompt"`
	N              int    `json:"n"`
	Size           string `json:"size"`
	Quality        string `json:"quality"`
	ResponseFormat string `json:"response_format"`
	Stream         bool   `json:"stream"`
}

type openAISpeechRequest struct {
	Model          string  `json:"model"`
	Input          string  `json:"input"`
	Voice          string  `json:"voice"`
	ResponseFormat string  `json:"response_format"`
	Speed          float64 `json:"speed"`
	Instructions   string  `json:"instructions"`
}

func (s *server) handleOpenAIImages(w http.ResponseWriter, r *http.Request) {
	var request openAIImageRequest
	if err := decodeJSON(r, &request); err != nil {
		writeOpenAIError(w, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}
	s.writeOpenAIImages(w, r, request, nil, "image_generation")
}

// writeOpenAIImages 按 n 生成图片并写入 OpenAI 图片响应，inputs 为写在提示词之前的参考图片与说明，流式时每张图片发送一个 <event>.completed 事件
func (s *server) writeOpenAIImages(w http.ResponseWriter, r *http.Request, request openAIImageRequest, inputs []aistudio.Part, event string) {
	if strings.TrimSpace(request.Prompt) == "" {
		writeOpenAIError(w, http.StatusBadRequest, "invalid_request", "prompt is required")
		return
	}
	if request.N == 0 {
		request.N = 1
	}
	if request.N < 1 || request.N > maxImageCount {
		writeOpenAIError(w, http.StatusBadRequest, "invalid_request", fmt.Sprintf("n must be between 1 and %d", maxImageCount))
		return
	}
	models, err := s.service.Models(r.Context())
	if err != nil {
		if shouldWriteRequestError(r, err) {
			writeOpenAIError(w, statusFromError(err), openAIErrorCode(err), err.Error())
		}
		return
	}
	if request.Model == "" {
		request.Model = defaultCatalogModel(models, func(model aistudio.Model) bool {
			return model.Capabilities["image_route"] && slices.Contains(model.Methods, "generateContent")
		})
	}
	if request.Model == "" {
		writeOpenAIError(w, http.StatusBadRequest, "invalid_request", "model is required")
		return
	}
	model, _ := lookupPublicModel(models, request.Model)
	imageConfig, err := openAIImageConfig(request.Size, request.Quality, model.CapabilityOptions)
	if err != nil {
		writeOpenAIError(w, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}
	results, err := s.generateImages(r.Context(), aistudio.GenerateRequest{
		Unary: true,
		Model: request.Model,
		Contents: []aistudio.Content{{
			Role: aistudio.RoleUser, Parts: append(inputs, aistudio.Part{Text: request.Prompt}),
		}},
		Config: aistudio.GenerationConfig{
			ResponseModalities: []aistudio.ResponseModality{aistudio.ResponseModalityImage, aistudio.ResponseModalityText},
			ImageConfig:        imageConfig,
		},
	}, request.N)
	if err != nil {
		if shouldWriteRequestError(r, err) {
			writeOpenAIError(w, statusFromError(err), openAIErrorCode(err), err.Error())
		}
		return
	}
	created := time.Now().Unix()
	data := make([]map[string]any, 0, len(results))
	for _, result := range results {
		images := 0
		for _, media := range result.media {
			if !strings.HasPrefix(media.MIME, "image/") || len(media.Data) == 0 {
				continue
			}
			encoded := base64.StdEncoding.EncodeToString(media.Data)
			item := map[string]any{}
			if request.Stream {
				item["type"] = event + ".completed"
				item["b64_json"] = encoded
				item["created_at"] = created
				item["output_format"] = strings.TrimPrefix(media.MIME, "image/")
				item["size"], item["quality"], item["background"] = cmp.Or(request.Size, "auto"), cmp.Or(request.Quality, "auto"), "auto"
			} else if request.ResponseFormat == "b64_json" {
				item["b64_json"] = encoded
			} else {
				item["url"] = "data:" + media.MIME + ";base64," + encoded
			}
			if result.text.Len() > 0 {
				item["revised_prompt"] = result.text.String()
			}
			data = append(data, item)
			images++
		}
		if images == 0 {
			message := "AI Studio did not return an image"
			if reason := result.finishReason; reason != "" && reason != "stop" {
				message += ": finish reason " + reason
			}
			writeOpenAIError(w, http.StatusBadGateway, "upstream_error", message)
			return
		}
	}
	if !request.Stream {
		writeJSON(w, http.StatusOK, map[string]any{"created": created, "data": data})
		return
	}
	if err := streamHeaders(w); err != nil {
		return
	}
	for _, item := range data {
		if err := writeSSE(w, item["type"].(string), item); err != nil {
			return
		}
	}
}

// maxImageCount 是 OpenAI Images n 的上限
const maxImageCount = 10

// generateImages 按 count 并发生成图片并按序返回结果，任一生成失败时取消其余生成并返回首个错误
func (s *server) generateImages(ctx context.Context, request aistudio.GenerateRequest, count int) ([]generationResult, error) {
	request.ID = newID("image")
	images := s.startChatChoices(ctx, request, count, false)
	defer images.cancel()
	if err := images.err; err != nil {
		images.settle(ctx)
		return nil, err
	}
	results, err := images.run(func(_ int, events <-chan aistudio.Event) (generationResult, error) {
		return consumeEvents(images.ctx, events, nil)
	})
	images.settle(ctx)
	images.record(ctx, results)
	return results, err
}

// defaultCatalogModel 返回实时目录中首个满足条件的模型 ID
func defaultCatalogModel(models []aistudio.Model, match func(aistudio.Model) bool) string {
	for _, model := range models {
		if match(model) {
			return model.ID
		}
	}
	return ""
}

// openAIImageAspectRatios 为模型目录未列出宽高比时的候选宽高比
var openAIImageAspectRatios = []string{"1:1", "2:3", "3:2", "3:4", "4:3", "4:5", "5:4", "9:16", "16:9", "21:9"}

// openAIImageConfig 将 OpenAI size 与 quality 换成模型选项中最接近的宽高比与分辨率
func openAIImageConfig(size string, quality string, options map[string][]string) (*aistudio.ImageConfig, error) {
	config := &aistudio.ImageConfig{}
	if size = strings.ToLower(strings.TrimSpace(size)); size != "" && size != "auto" {
		width, height, ok := imageDimensions(size)
		if !ok {
			return nil, fmt.Errorf("size must be auto or WIDTHxHEIGHT")
		}
		ratios := options["image_aspect_ratios"]
		if len(ratios) == 0 {
			ratios = openAIImageAspectRatios
		}
		config.AspectRatio = aistudio.NearestOption(math.Log(width)-math.Log(height), ratios, aistudio.AspectRatioLog)
	}
	switch strings.ToLower(strings.TrimSpace(quality)) {
	case "", "auto":
	case "low", "standard":
		config.ImageSize = "1K"
	case "medium", "hd":
		config.ImageSize = "2K"
	case "high", "xhigh", "max":
		config.ImageSize = "4K"
	default:
		return nil, fmt.Errorf("quality must be auto, low, medium, high, xhigh, max, standard or hd")
	}
	if resolutions := options["image_output_resolutions"]; config.ImageSize != "" && len(resolutions) > 0 {
		pixels, _ := imageResolutionPixels(config.ImageSize)
		config.ImageSize = aistudio.NearestOption(pixels, resolutions, imageResolutionPixels)
	}
	if config.AspectRatio == "" && config.ImageSize == "" {
		return nil, nil
	}
	return config, nil
}

// imageDimensions 解析 WIDTHxHEIGHT 形式的图片尺寸
func imageDimensions(size string) (float64, float64, bool) {
	widthText, heightText, ok := strings.Cut(size, "x")
	width, widthErr := strconv.Atoi(widthText)
	height, heightErr := strconv.Atoi(heightText)
	if !ok || widthErr != nil || heightErr != nil || width <= 0 || height <= 0 {
		return 0, 0, false
	}
	return float64(width), float64(height), true
}

// imageResolutionPixels 返回 512、1K 等图片分辨率的长边像素
func imageResolutionPixels(value string) (float64, bool) {
	value = strings.ToUpper(strings.TrimSpace(value))
	scale := 1
	if strings.HasSuffix(value, "K") {
		scale = 1024
	}
	number, err := strconv.Atoi(strings.TrimSuffix(value, "K"))
	if err != nil || number <= 0 {
		return 0, false
	}
	return float64(number * scale), true
}

func (s *server) handleOpenAISpeech(w http.ResponseWriter, r *http.Request) {
	var request openAISpeechRequest
	if err := decodeJSON(r, &request); err != nil {
		writeOpenAIError(w, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}
	if request.Model == "" || strings.TrimSpace(request.Input) == "" {
		writeOpenAIError(w, http.StatusBadRequest, "invalid_request", "model and input are required")
		return
	}
	if request.Speed != 0 && (request.Speed < 0.25 || request.Speed > 4) {
		writeOpenAIError(w, http.StatusBadRequest, "invalid_request", "speed must be between 0.25 and 4.0")
		return
	}
	format := strings.ToLower(strings.TrimSpace(request.ResponseFormat))
	if format == "" {
		format = "wav"
	}
	if err := speechFormatError(format); err != nil {
		writeOpenAIError(w, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}
	part := aistudio.Part{Text: strings.TrimSpace(request.Input)}
	style := strings.TrimSpace(request.Instructions)
	if request.Speed != 0 && request.Speed != 1 {
		style = strings.TrimSpace(fmt.Sprintf("%s Speak at %g times the normal speaking rate.", style, request.Speed))
	}
	if style != "" {
		part.SpeechMetadata = &aistudio.SpeechMetadata{Style: style}
	}
	events, err := s.service.Generate(r.Context(), aistudio.GenerateRequest{
		ID:    newID("speech"),
		Unary: true,
		Model: request.Model,
		Contents: []aistudio.Content{{
			Role: aistudio.RoleUser, Parts: []aistudio.Part{part},
		}},
		Config: aistudio.GenerationConfig{
			ResponseModalities: []aistudio.ResponseModality{aistudio.ResponseModalityAudio},
			SpeechConfig:       &aistudio.SpeechConfig{VoiceName: request.Voice},
		},
	})
	if err != nil {
		if shouldWriteRequestError(r, err) {
			writeOpenAIError(w, statusFromError(err), openAIErrorCode(err), err.Error())
		}
		return
	}
	result, err := consumeEvents(r.Context(), events, nil)
	if err != nil {
		if shouldWriteRequestError(r, err) {
			writeOpenAIError(w, statusFromError(err), openAIErrorCode(err), err.Error())
		}
		return
	}
	media, err := joinedAudio(result.media)
	if err != nil {
		writeOpenAIError(w, http.StatusBadGateway, "upstream_error", err.Error())
		return
	}
	data, contentType, err := encodeSpeechResponse(media, format)
	if err != nil {
		writeOpenAIError(w, http.StatusBadGateway, "upstream_error", err.Error())
		return
	}
	w.Header().Set("Content-Type", contentType)
	w.Header().Set("Content-Length", strconv.Itoa(len(data)))
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(data)
}

// speechFormatError 在生成前拒绝 OpenAI Speech 未定义的 response_format
func speechFormatError(format string) error {
	if _, ok := audioContentTypes[format]; ok || format == "wav" || format == "pcm" {
		return nil
	}
	return fmt.Errorf("response_format must be mp3, opus, aac, flac, wav or pcm")
}

func joinedAudio(values []aistudio.Media) (aistudio.Media, error) {
	var joined aistudio.Media
	for _, media := range values {
		if strings.HasPrefix(strings.ToLower(media.MIME), "audio/wav") || strings.HasPrefix(strings.ToLower(media.MIME), "audio/x-wav") {
			var err error
			media, err = wavPCM(media)
			if err != nil {
				return aistudio.Media{}, err
			}
		}
		if !strings.HasPrefix(media.MIME, "audio/") || len(media.Data) == 0 {
			continue
		}
		if joined.MIME == "" {
			joined.MIME = media.MIME
		}
		if joined.MIME != media.MIME {
			return aistudio.Media{}, fmt.Errorf("AI Studio returned multiple audio formats")
		}
		joined.Data = append(joined.Data, media.Data...)
	}
	if len(joined.Data) == 0 {
		return aistudio.Media{}, fmt.Errorf("AI Studio did not return audio")
	}
	return joined, nil
}

func encodeSpeechResponse(media aistudio.Media, format string) ([]byte, string, error) {
	baseType, _, err := mime.ParseMediaType(media.MIME)
	if err != nil {
		return nil, "", fmt.Errorf("AI Studio returned invalid audio MIME %q", media.MIME)
	}
	switch {
	case format == "wav" && (baseType == "audio/wav" || baseType == "audio/x-wav"):
		return media.Data, "audio/wav", nil
	case format == "mp3" && baseType == "audio/mpeg":
		return media.Data, "audio/mpeg", nil
	case format == "pcm" && baseType != "audio/wav" && baseType != "audio/x-wav" && baseType != "audio/mpeg":
		return media.Data, media.MIME, nil
	}
	audio, err := mediaPCM(media)
	if err != nil {
		return nil, "", err
	}
	switch format {
	case "pcm":
		return audio.Data, fmt.Sprintf("audio/l16;rate=%d;channels=%d", audio.SampleRate, audio.Channels), nil
	case "wav":
		return pcmWAV(audio.Data, audio.SampleRate, audio.Channels), "audio/wav", nil
	}
	data, _, err := encodeAudio(audio, audioOutput{Format: format})
	return data, audioContentTypes[format], err
}

func pcmWAV(pcm []byte, sampleRate int, channels int) []byte {
	buffer := bytes.NewBuffer(make([]byte, 0, 44+len(pcm)))
	buffer.WriteString("RIFF")
	_ = binary.Write(buffer, binary.LittleEndian, uint32(36+len(pcm)))
	buffer.WriteString("WAVEfmt ")
	_ = binary.Write(buffer, binary.LittleEndian, uint32(16))
	_ = binary.Write(buffer, binary.LittleEndian, uint16(1))
	_ = binary.Write(buffer, binary.LittleEndian, uint16(channels))
	_ = binary.Write(buffer, binary.LittleEndian, uint32(sampleRate))
	_ = binary.Write(buffer, binary.LittleEndian, uint32(sampleRate*channels*2))
	_ = binary.Write(buffer, binary.LittleEndian, uint16(channels*2))
	_ = binary.Write(buffer, binary.LittleEndian, uint16(16))
	buffer.WriteString("data")
	_ = binary.Write(buffer, binary.LittleEndian, uint32(len(pcm)))
	buffer.Write(pcm)
	return buffer.Bytes()
}

// wavPCM 提取 RIFF WAVE 的 PCM16 数据并保留采样率与声道
func wavPCM(media aistudio.Media) (aistudio.Media, error) {
	data := media.Data
	if len(data) < 12 || string(data[:4]) != "RIFF" || string(data[8:12]) != "WAVE" {
		return aistudio.Media{}, fmt.Errorf("AI Studio returned invalid WAV audio")
	}
	end := uint64(binary.LittleEndian.Uint32(data[4:8])) + 8
	if end > uint64(len(data)) || end < 12 {
		return aistudio.Media{}, fmt.Errorf("AI Studio returned truncated WAV audio")
	}
	var pcm []byte
	var rate uint32
	var channels uint16
	for offset := uint64(12); offset+8 <= end; {
		kind := string(data[offset : offset+4])
		size := uint64(binary.LittleEndian.Uint32(data[offset+4 : offset+8]))
		start := offset + 8
		if size > end-start {
			return aistudio.Media{}, fmt.Errorf("AI Studio returned a truncated WAV chunk")
		}
		chunk := data[start : start+size]
		switch kind {
		case "fmt ":
			if len(chunk) < 16 || binary.LittleEndian.Uint16(chunk[:2]) != 1 || binary.LittleEndian.Uint16(chunk[14:16]) != 16 {
				return aistudio.Media{}, fmt.Errorf("AI Studio WAV audio must use PCM16")
			}
			channels = binary.LittleEndian.Uint16(chunk[2:4])
			rate = binary.LittleEndian.Uint32(chunk[4:8])
		case "data":
			pcm = append(pcm, chunk...)
		}
		offset = start + size + size%2
	}
	if rate == 0 || channels == 0 || len(pcm) == 0 || len(pcm)%(int(channels)*2) != 0 {
		return aistudio.Media{}, fmt.Errorf("AI Studio WAV audio is missing valid PCM data or format")
	}
	return aistudio.Media{MIME: fmt.Sprintf("audio/l16;rate=%d;channels=%d", rate, channels), Data: pcm}, nil
}

// decodeBase64Flexible 根据字母表和填充形式解码 Base64 与 Data URL
func decodeBase64Flexible(s string) ([]byte, error) {
	s = strings.TrimSpace(s)
	if idx := strings.Index(s, ","); idx != -1 && strings.HasPrefix(s, "data:") {
		s = s[idx+1:]
	}
	encoding := base64.StdEncoding
	if index := strings.IndexAny(s, "+/-_"); index >= 0 && (s[index] == '-' || s[index] == '_') {
		encoding = base64.URLEncoding
	}
	if !strings.HasSuffix(s, "=") {
		encoding = encoding.WithPadding(base64.NoPadding)
	}
	return encoding.DecodeString(s)
}

// normalizeImagePayload 将 GIF 首帧按逻辑画布转换为 PNG 图片
func normalizeImagePayload(mimeType string, data []byte) (string, []byte) {
	lowerMIME := strings.ToLower(strings.TrimSpace(mimeType))
	if lowerMIME == "image/gif" || (len(data) >= 3 && string(data[:3]) == "GIF") {
		if img, err := gif.Decode(bytes.NewReader(data)); err == nil {
			config, err := gif.DecodeConfig(bytes.NewReader(data))
			if err != nil {
				return mimeType, data
			}
			canvas := image.NewNRGBA(image.Rect(0, 0, config.Width, config.Height))
			transparent := false
			for _, entry := range img.(*image.Paletted).Palette {
				_, _, _, alpha := entry.RGBA()
				transparent = transparent || alpha == 0
			}
			// GIF 背景色来自全局色表，透明首帧保留透明画布
			if palette, ok := config.ColorModel.(color.Palette); ok && !transparent && int(data[11]) < len(palette) {
				draw.Draw(canvas, canvas.Bounds(), image.NewUniform(palette[data[11]]), image.Point{}, draw.Src)
			}
			draw.Draw(canvas, img.Bounds(), img, img.Bounds().Min, draw.Over)
			var buf bytes.Buffer
			if err := png.Encode(&buf, canvas); err == nil {
				return "image/png", buf.Bytes()
			}
		}
	}
	return mimeType, data
}
