package api

import (
	"bytes"
	"cmp"
	"context"
	"encoding/base64"
	"encoding/binary"
	"errors"
	"fmt"
	"image"
	"image/color"
	"image/gif"
	"image/jpeg"
	"image/png"
	"math"
	"mime"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"time"

	// nativewebp 为 image.Decode 注册 WebP 解码器
	_ "github.com/HugoSmits86/nativewebp"
	"github.com/Mag1cFall/AIStudio2API/internal/aistudio"
	"github.com/Mag1cFall/AIStudio2API/internal/imagecodec"
	jpegdecode "github.com/Mag1cFall/AIStudio2API/internal/imagecodec/jpeg"
	"github.com/Mag1cFall/AIStudio2API/internal/imagecodec/libwebp"
	"golang.org/x/image/draw"
)

type openAIImageRequest struct {
	Model          string `json:"model"`
	Prompt         string `json:"prompt"`
	N              int    `json:"n"`
	Size           string `json:"size"`
	Quality        string `json:"quality"`
	ResponseFormat string `json:"response_format"`
	Stream         bool   `json:"stream"`
	// OutputFormat 与 OutputCompression 是输出图片的编码与 JPEG、WebP 压缩级别
	OutputFormat      string `json:"output_format"`
	OutputCompression *int   `json:"output_compression"`
	// SafetyIdentifier 与 User 是调用方的终端用户标识，响应回传其中非空的一个
	SafetyIdentifier string `json:"safety_identifier"`
	User             string `json:"user"`
}

type openAISpeechRequest struct {
	Model          string  `json:"model"`
	Input          string  `json:"input"`
	Voice          string  `json:"voice"`
	ResponseFormat string  `json:"response_format"`
	Speed          float64 `json:"speed"`
	Instructions   string  `json:"instructions"`
	// StreamFormat 为 sse 时以 speech.audio.delta 与 speech.audio.done 事件输出，省略或为 audio 时返回音频文件
	StreamFormat string `json:"stream_format"`
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
			writeOpenAIRequestError(w, err)
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
	if !request.Stream {
		var stopKeepalive func()
		w, stopKeepalive = s.nonstreamKeepalive(w, r)
		defer stopKeepalive()
	}
	search := s.prepareSearch(aistudio.GenerateRequest{Model: request.Model})
	model, _ := LookupPublicModel(models, search.Model)
	imageConfig, err := openAIImageConfig(request.Size, request.Quality, model.CapabilityOptions)
	if err != nil {
		writeOpenAIError(w, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}
	target, _ := openAIImageSize(request.Size)
	format, compression, err := openAIImageEncoding(request.OutputFormat, request.OutputCompression)
	if err != nil {
		writeOpenAIError(w, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}
	var stream *streamKeepalive
	if request.Stream {
		stream = keepStreamAlive(w, sseHeartbeat)
		defer stream.stop()
	}
	// streamed 在流式响应头已由心跳写出时以 SSE 错误事件写出 err 并返回 true
	streamed := func(err error) bool {
		if stream == nil || !stream.stop() {
			return false
		}
		writeOpenAISSEError(stream, err)
		return true
	}
	results, err := s.generateImages(r.Context(), aistudio.GenerateRequest{
		Unary: true,
		Model: search.Model,
		Tools: search.Tools,
		Contents: []aistudio.Content{{
			Role: aistudio.RoleUser, Parts: append(inputs, aistudio.Part{Text: request.Prompt}),
		}},
		Config: aistudio.GenerationConfig{
			ResponseModalities: []aistudio.ResponseModality{aistudio.ResponseModalityImage, aistudio.ResponseModalityText},
			ImageConfig:        imageConfig,
		},
	}, request.N)
	if err != nil {
		if shouldWriteRequestError(r, err) && !streamed(err) {
			writeOpenAIRequestError(w, err)
		}
		return
	}
	created := time.Now().Unix()
	quality := openAIImageQuality(request.Quality, imageConfig)
	data := make([]map[string]any, 0, len(results))
	var size image.Point
	for _, result := range results {
		images := 0
		for _, media := range result.media {
			if !strings.HasPrefix(media.MIME, "image/") {
				continue
			}
			media, size, err = fitImage(media, target, format, compression)
			if err != nil {
				if !streamed(err) {
					writeOpenAIError(w, http.StatusBadGateway, "upstream_error", err.Error())
				}
				return
			}
			encoded := base64.StdEncoding.EncodeToString(media.Data)
			item := map[string]any{}
			if request.Stream {
				item["type"] = event + ".completed"
				item["b64_json"] = encoded
				item["created_at"] = created
				item["output_format"] = strings.TrimPrefix(format, "image/")
				item["size"], item["quality"], item["background"] = fmt.Sprintf("%dx%d", size.X, size.Y), quality, "opaque"
				item["usage"] = openAIImageUsage(result.usage)
				if identifier := cmp.Or(request.SafetyIdentifier, request.User); identifier != "" {
					item["safety_identifier"] = identifier
				}
			} else if request.ResponseFormat == "url" {
				item["url"] = "data:" + media.MIME + ";base64," + encoded
			} else {
				item["b64_json"] = encoded
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
			if !streamed(errors.New(message)) {
				writeOpenAIError(w, http.StatusBadGateway, "upstream_error", message)
			}
			return
		}
	}
	if !request.Stream {
		usages := make([]*aistudio.Usage, 0, len(results))
		for _, result := range results {
			usages = append(usages, result.usage)
		}
		body := map[string]any{
			"created": created, "data": data, "background": "opaque", "output_format": strings.TrimPrefix(format, "image/"),
			"quality": quality, "size": fmt.Sprintf("%dx%d", size.X, size.Y), "usage": openAIImageUsage(usages...),
		}
		if identifier := cmp.Or(request.SafetyIdentifier, request.User); identifier != "" {
			body["safety_identifier"] = identifier
		}
		writeJSON(w, http.StatusOK, officialOrder("openai", body))
		return
	}
	if err := stream.open(); err != nil {
		return
	}
	for _, item := range data {
		if err := writeSSE(stream, item["type"].(string), officialOrder("openai", item)); err != nil {
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
		return consumeEvents(images.ctx, events)
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

// openAIImageConfig 将 OpenAI size 与 quality 换成上游宽高比与分辨率：size 取最接近的宽高比与能覆盖该尺寸的最小分辨率，quality 对应的分辨率更高时取 quality 的分辨率
func openAIImageConfig(size string, quality string, options map[string][]string) (*aistudio.ImageConfig, error) {
	config := &aistudio.ImageConfig{}
	target, err := openAIImageSize(size)
	if err != nil {
		return nil, err
	}
	resolutions := options["image_output_resolutions"]
	if target != (image.Point{}) {
		ratios := options["image_aspect_ratios"]
		if len(ratios) == 0 {
			ratios = openAIImageAspectRatios
		}
		config.AspectRatio = aistudio.NearestOption(math.Log(float64(target.X))-math.Log(float64(target.Y)), ratios, aistudio.AspectRatioLog)
		config.ImageSize = imageResolutionCovering(math.Sqrt(float64(target.X)*float64(target.Y)), resolutions)
	}
	var requested string
	switch strings.ToLower(strings.TrimSpace(quality)) {
	case "", "auto":
	case "low", "standard":
		requested = "1K"
	case "medium", "hd":
		requested = "2K"
	case "high", "xhigh", "max":
		requested = "4K"
	default:
		return nil, fmt.Errorf("quality must be auto, low, medium, high, xhigh, max, standard or hd")
	}
	if requested != "" && len(resolutions) > 0 {
		pixels, _ := imageResolutionPixels(requested)
		requested = aistudio.NearestOption(pixels, resolutions, imageResolutionPixels)
	}
	current, _ := imageResolutionPixels(config.ImageSize)
	if pixels, _ := imageResolutionPixels(requested); pixels > current {
		config.ImageSize = requested
	}
	if config.AspectRatio == "" && config.ImageSize == "" {
		return nil, nil
	}
	return config, nil
}

// openAIImageSize 解析 OpenAI size 的 WIDTHxHEIGHT，auto 或省略时为零
func openAIImageSize(size string) (image.Point, error) {
	size = strings.ToLower(strings.TrimSpace(size))
	if size == "" || size == "auto" {
		return image.Point{}, nil
	}
	widthText, heightText, ok := strings.Cut(size, "x")
	width, widthErr := strconv.Atoi(widthText)
	height, heightErr := strconv.Atoi(heightText)
	if !ok || widthErr != nil || heightErr != nil || width <= 0 || height <= 0 {
		return image.Point{}, fmt.Errorf("size must be auto or WIDTHxHEIGHT")
	}
	return image.Pt(width, height), nil
}

// imageResolutionCovering 返回分辨率选项中边长不小于 edge 的最小一档，edge 超过全部选项时返回最大一档，没有选项时为空
func imageResolutionCovering(edge float64, resolutions []string) string {
	covering, coveringPixels := "", math.Inf(1)
	largest, largestPixels := "", 0.0
	for _, option := range resolutions {
		pixels, ok := imageResolutionPixels(option)
		switch {
		case !ok:
		case pixels >= edge && pixels < coveringPixels:
			covering, coveringPixels = option, pixels
		case pixels > largestPixels:
			largest, largestPixels = option, pixels
		}
	}
	return cmp.Or(covering, largest)
}

// openAIImageQuality 返回响应中的 quality：请求的 low、medium、high、xhigh、max 原样返回，其余按生成分辨率换为 low、medium 或 high
func openAIImageQuality(quality string, config *aistudio.ImageConfig) string {
	switch quality = strings.ToLower(strings.TrimSpace(quality)); quality {
	case "low", "medium", "high", "xhigh", "max":
		return quality
	}
	if config != nil {
		switch config.ImageSize {
		case "4K":
			return "high"
		case "2K":
			return "medium"
		}
	}
	return "low"
}

// openAIImageEncoding 返回 output_format 对应的 MIME 类型与 output_compression 给出的 JPEG 与 WebP 质量，省略时为 PNG 与 100
func openAIImageEncoding(format string, compression *int) (string, int, error) {
	quality := 100
	if compression != nil {
		if *compression < 0 || *compression > 100 {
			return "", 0, fmt.Errorf("output_compression must be between 0 and 100")
		}
		quality = *compression
	}
	switch strings.ToLower(strings.TrimSpace(format)) {
	case "", "png":
		return "image/png", quality, nil
	case "jpeg", "jpg":
		return "image/jpeg", quality, nil
	case "webp":
		return "image/webp", quality, nil
	}
	return "", 0, fmt.Errorf("output_format must be png, jpeg or webp")
}

// openAIImageUsage 合计上游用量为 OpenAI 图片用量，输入与输出的图片与文本 token 取上游按模态细分的 IMAGE 与 TEXT 计数
func openAIImageUsage(usages ...*aistudio.Usage) map[string]any {
	var input, output, total, inputText, inputImages, outputText, outputImages int64
	for _, usage := range usages {
		if usage == nil {
			continue
		}
		input += usage.InputTokens
		output += usage.OutputTokens
		total += usage.TotalTokens
		if usage.Details != nil {
			inputText += modalityTokens(usage.Details.PromptTokensDetails, "TEXT")
			inputImages += modalityTokens(usage.Details.PromptTokensDetails, "IMAGE")
			outputText += modalityTokens(usage.Details.CandidatesTokensDetails, "TEXT")
			outputImages += modalityTokens(usage.Details.CandidatesTokensDetails, "IMAGE")
		}
	}
	return map[string]any{
		"input_tokens":          input,
		"input_tokens_details":  map[string]any{"image_tokens": inputImages, "text_tokens": inputText},
		"output_tokens":         output,
		"output_tokens_details": map[string]any{"image_tokens": outputImages, "text_tokens": outputText},
		"total_tokens":          total,
	}
}

// fitImage 把图片居中裁切缩放为 size 并编码为 mimeType，返回实际尺寸；size 为零或图片任一边小于 size 时保持原尺寸，不放大
func fitImage(media aistudio.Blob, size image.Point, mimeType string, compression int) (aistudio.Blob, image.Point, error) {
	config, _, err := image.DecodeConfig(bytes.NewReader(media.Data))
	if err != nil {
		return media, image.Point{}, fmt.Errorf("decode %s output: %w", media.MIME, err)
	}
	bounds := image.Pt(config.Width, config.Height)
	resize := size != (image.Point{}) && bounds != size && bounds.X >= size.X && bounds.Y >= size.Y
	if !resize && media.MIME == mimeType && (mimeType == "image/png" || compression == 100) {
		return media, bounds, nil
	}
	decoded, err := decodeImage(media.Data)
	if err != nil {
		return media, image.Point{}, fmt.Errorf("decode %s output: %w", media.MIME, err)
	}
	if resize {
		decoded = coverImage(decoded, size)
	}
	encoded, err := encodeImage(decoded, mimeType, compression)
	if err != nil {
		return media, image.Point{}, fmt.Errorf("convert %s output to %s: %w", media.MIME, mimeType, err)
	}
	return aistudio.Blob{MIME: mimeType, Data: encoded}, decoded.Bounds().Size(), nil
}

// decodeImage 以 imagecodec/jpeg 解码 JPEG，其余格式经 image.Decode 解码
func decodeImage(data []byte) (image.Image, error) {
	if bytes.HasPrefix(data, []byte("\xff\xd8")) {
		return jpegdecode.Decode(data)
	}
	decoded, _, err := image.Decode(bytes.NewReader(data))
	return decoded, err
}

// coverImage 从图片中央取与 size 同宽高比的最大区域，缩放为 size
func coverImage(source image.Image, size image.Point) image.Image {
	crop := source.Bounds()
	if crop.Dx()*size.Y > crop.Dy()*size.X {
		width := crop.Dy() * size.X / size.Y
		crop.Min.X += (crop.Dx() - width) / 2
		crop.Max.X = crop.Min.X + width
	} else {
		height := crop.Dx() * size.Y / size.X
		crop.Min.Y += (crop.Dy() - height) / 2
		crop.Max.Y = crop.Min.Y + height
	}
	return imagecodec.Scale(source, crop, size)
}

// encodeImage 把图片编码为 PNG、JPEG 或有损 WebP，quality 为 JPEG 与 WebP 质量，YCbCr 图片的 PNG 为 8 位 RGB
func encodeImage(img image.Image, mimeType string, quality int) ([]byte, error) {
	var encoded bytes.Buffer
	var err error
	switch mimeType {
	case "image/jpeg":
		err = jpeg.Encode(&encoded, img, &jpeg.Options{Quality: quality})
	case "image/webp":
		err = libwebp.Encode(&encoded, img, quality)
	default:
		if ycbcr, ok := img.(*image.YCbCr); ok {
			img = imagecodec.YCbCrToRGBA(ycbcr)
		}
		err = imagecodec.EncodePNG(&encoded, img)
	}
	return encoded.Bytes(), err
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
		format = "mp3"
	}
	if err := speechFormatError(format); err != nil {
		writeOpenAIError(w, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}
	streamFormat := strings.TrimSpace(request.StreamFormat)
	if streamFormat != "" && streamFormat != "audio" && streamFormat != "sse" {
		writeOpenAIError(w, http.StatusBadRequest, "invalid_request", "stream_format must be audio or sse")
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
		Unary: streamFormat != "sse",
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
			writeOpenAIRequestError(w, err)
		}
		return
	}
	if streamFormat == "sse" {
		streamSpeech(w, r, events, format)
		return
	}
	result, err := consumeEvents(r.Context(), events)
	if err != nil {
		if shouldWriteRequestError(r, err) {
			writeOpenAIRequestError(w, err)
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

// streamSpeech 以 speech.audio.delta 写出音频，pcm 随上游逐块输出，其他格式在结束时整段输出，最后写出带用量的 speech.audio.done
func streamSpeech(w http.ResponseWriter, r *http.Request, events <-chan aistudio.Event, format string) {
	stream, err := openStream(w, sseHeartbeat)
	defer stream.stop()
	if err != nil {
		return
	}
	w = stream
	delta := func(data []byte) error {
		return writeSSE(w, "", map[string]any{"type": "speech.audio.delta", "audio": base64.StdEncoding.EncodeToString(data)})
	}
	result, err := consumeStreamEvents(r.Context(), events, func(event aistudio.Event) error {
		if format != "pcm" || !audioEvent(event) {
			return nil
		}
		data, _, err := encodeSpeechResponse(*event.InlineData, "pcm")
		if err != nil {
			return err
		}
		return delta(data)
	})
	if err == nil && format != "pcm" {
		var media aistudio.Blob
		if media, err = joinedAudio(result.media); err == nil {
			var data []byte
			if data, _, err = encodeSpeechResponse(media, format); err == nil {
				err = delta(data)
			}
		}
	}
	if err != nil {
		if shouldWriteRequestError(r, err) {
			writeOpenAISSEError(w, err)
		}
		return
	}
	usage := aistudio.Usage{}
	if result.usage != nil {
		usage = *result.usage
	}
	_ = writeSSE(w, "", map[string]any{"type": "speech.audio.done", "usage": map[string]any{
		"input_tokens": usage.InputTokens, "output_tokens": usage.OutputTokens, "total_tokens": usage.TotalTokens,
	}})
}

// speechFormatError 在生成前拒绝 OpenAI Speech 未定义的 response_format
func speechFormatError(format string) error {
	if _, ok := audioContentTypes[format]; ok || format == "wav" || format == "pcm" {
		return nil
	}
	return fmt.Errorf("response_format must be mp3, opus, aac, flac, wav or pcm")
}

// convertImage 把图片转码为 image/jpeg，已是 JPEG 时原样返回
func convertImage(media aistudio.Blob) (aistudio.Blob, error) {
	if strings.HasPrefix(media.MIME, "image/jpeg") {
		return media, nil
	}
	converted, _, err := fitImage(media, image.Point{}, "image/jpeg", jpeg.DefaultQuality)
	return converted, err
}

// audioEvent 表示事件是内联音频
func audioEvent(event aistudio.Event) bool {
	return event.Kind == aistudio.EventMedia && event.InlineData != nil && strings.HasPrefix(event.InlineData.MIME, "audio/")
}

func joinedAudio(values []aistudio.Blob) (aistudio.Blob, error) {
	var joined aistudio.Blob
	for _, media := range values {
		if strings.HasPrefix(strings.ToLower(media.MIME), "audio/wav") || strings.HasPrefix(strings.ToLower(media.MIME), "audio/x-wav") {
			var err error
			media, err = wavPCM(media)
			if err != nil {
				return aistudio.Blob{}, err
			}
		}
		if !strings.HasPrefix(media.MIME, "audio/") {
			continue
		}
		if joined.MIME == "" {
			joined.MIME = media.MIME
		}
		if joined.MIME != media.MIME {
			return aistudio.Blob{}, fmt.Errorf("AI Studio returned multiple audio formats")
		}
		joined.Data = append(joined.Data, media.Data...)
	}
	if len(joined.Data) == 0 {
		return aistudio.Blob{}, fmt.Errorf("AI Studio did not return audio")
	}
	return joined, nil
}

func encodeSpeechResponse(media aistudio.Blob, format string) ([]byte, string, error) {
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
func wavPCM(media aistudio.Blob) (aistudio.Blob, error) {
	data := media.Data
	if len(data) < 12 || string(data[:4]) != "RIFF" || string(data[8:12]) != "WAVE" {
		return aistudio.Blob{}, fmt.Errorf("AI Studio returned invalid WAV audio")
	}
	end := uint64(binary.LittleEndian.Uint32(data[4:8])) + 8
	if end > uint64(len(data)) || end < 12 {
		return aistudio.Blob{}, fmt.Errorf("AI Studio returned truncated WAV audio")
	}
	var pcm []byte
	var rate uint32
	var channels uint16
	for offset := uint64(12); offset+8 <= end; {
		kind := string(data[offset : offset+4])
		size := uint64(binary.LittleEndian.Uint32(data[offset+4 : offset+8]))
		start := offset + 8
		if size > end-start {
			return aistudio.Blob{}, fmt.Errorf("AI Studio returned a truncated WAV chunk")
		}
		chunk := data[start : start+size]
		switch kind {
		case "fmt ":
			if len(chunk) < 16 || binary.LittleEndian.Uint16(chunk[:2]) != 1 || binary.LittleEndian.Uint16(chunk[14:16]) != 16 {
				return aistudio.Blob{}, fmt.Errorf("AI Studio WAV audio must use PCM16")
			}
			channels = binary.LittleEndian.Uint16(chunk[2:4])
			rate = binary.LittleEndian.Uint32(chunk[4:8])
		case "data":
			pcm = append(pcm, chunk...)
		}
		offset = start + size + size%2
	}
	if rate == 0 || channels == 0 || len(pcm) == 0 || len(pcm)%(int(channels)*2) != 0 {
		return aistudio.Blob{}, fmt.Errorf("AI Studio WAV audio is missing valid PCM data or format")
	}
	return aistudio.Blob{MIME: fmt.Sprintf("audio/l16;rate=%d;channels=%d", rate, channels), Data: pcm}, nil
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
		if frame, config, ok := decodeGIFFrame(data); ok {
			canvas := image.NewNRGBA(image.Rect(0, 0, config.Width, config.Height))
			transparent := false
			for _, entry := range frame.Palette {
				_, _, _, alpha := entry.RGBA()
				transparent = transparent || alpha == 0
			}
			// GIF 背景色来自全局色表，透明首帧保留透明画布
			if palette, ok := config.ColorModel.(color.Palette); ok && !transparent && int(data[11]) < len(palette) {
				draw.Draw(canvas, canvas.Bounds(), image.NewUniform(palette[data[11]]), image.Point{}, draw.Src)
			}
			draw.Draw(canvas, frame.Bounds(), frame, frame.Bounds().Min, draw.Over)
			var buf bytes.Buffer
			if err := png.Encode(&buf, canvas); err == nil {
				return "image/png", buf.Bytes()
			}
		}
	}
	return mimeType, data
}

// decodeGIFFrame 解码 GIF 的首帧与逻辑画布配置
func decodeGIFFrame(data []byte) (*image.Paletted, image.Config, bool) {
	img, err := gif.Decode(bytes.NewReader(data))
	if err != nil {
		return nil, image.Config{}, false
	}
	config, err := gif.DecodeConfig(bytes.NewReader(data))
	if err != nil {
		return nil, image.Config{}, false
	}
	return img.(*image.Paletted), config, true
}
