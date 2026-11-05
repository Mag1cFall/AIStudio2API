package api

import (
	"bytes"
	"cmp"
	"context"
	"errors"
	"fmt"
	"image"
	"image/draw"
	"image/jpeg"
	"io"
	"mime"
	"mime/multipart"
	"net/http"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/Mag1cFall/AIStudio2API/internal/aistudio"
)

const (
	openAIFileMaxBytes        int64 = 512 << 20
	openAIFileRequestOverhead int64 = 1 << 20
	openAIFileFieldMaxBytes   int64 = 64 << 10
)

type openAIFileUploadResult struct {
	ref aistudio.FileRef
	err error
}

type openAIFileFieldsResult struct {
	value aistudio.UploadFields
	err   error
}

type completionReader struct {
	reader io.Reader
	done   chan error
	closed bool
}

func (reader *completionReader) Read(target []byte) (int, error) {
	count, err := reader.reader.Read(target)
	if err != nil && !reader.closed {
		reader.closed = true
		reader.done <- err
	}
	return count, err
}

type openAIFileObject struct {
	ID        string `json:"id"`
	Object    string `json:"object"`
	Bytes     int64  `json:"bytes"`
	CreatedAt int64  `json:"created_at"`
	ExpiresAt *int64 `json:"expires_at"`
	Filename  string `json:"filename"`
	Purpose   string `json:"purpose"`
	Status    string `json:"status"`
}

// handleFileUpload 上传文件，Anthropic 请求返回 Files API 文件元数据，其余返回 OpenAI 文件对象
func (s *server) handleFileUpload(w http.ResponseWriter, r *http.Request) {
	service, ok := s.service.(aistudio.FileService)
	if !ok {
		writeFileError(w, r, http.StatusBadRequest, "invalid_request", "file upload is unavailable")
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, openAIFileMaxBytes+openAIFileRequestOverhead)
	reader, err := r.MultipartReader()
	if err != nil {
		writeFileParseError(w, r, err)
		return
	}
	fields, file, filename, contentType, err := readOpenAIFileParts(reader)
	if err != nil {
		writeFileParseError(w, r, err)
		return
	}
	defer file.Close()
	mimeType, stream, err := multipartStreamMIME(file, contentType, filename)
	if err != nil {
		writeFileError(w, r, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}
	request := aistudio.UploadRequest{Name: filename, MIME: mimeType, Size: -1, MaxSize: openAIFileMaxBytes, Reader: stream}
	ref, err := uploadOpenAIFile(r.Context(), service, reader, request, fields)
	if err != nil {
		if !shouldWriteRequestError(r, err) {
			return
		}
		var maxBytes *http.MaxBytesError
		if errors.As(err, &maxBytes) {
			writeFileParseError(w, r, err)
			return
		}
		writeFileError(w, r, statusFromError(err), openAIErrorCode(err), err.Error())
		return
	}
	metadata, err := service.FileMetadata(r.Context(), ref.ID)
	if err != nil {
		if !shouldWriteRequestError(r, err) {
			return
		}
		writeFileError(w, r, statusFromError(err), openAIErrorCode(err), err.Error())
		return
	}
	writeJSON(w, http.StatusOK, officialOrder(fileProtocol(r), fileResponse(r, metadata)))
}

// readOpenAIFileParts 读取文件段及其之前的表单字段
func readOpenAIFileParts(reader *multipart.Reader) (openAIFileFields, *multipart.Part, string, string, error) {
	var fields openAIFileFields
	for {
		part, err := reader.NextPart()
		if errors.Is(err, io.EOF) {
			return fields, nil, "", "", errors.New("file is required")
		}
		if err != nil {
			return fields, nil, "", "", err
		}
		if part.FormName() == "file" && part.FileName() != "" {
			filename := strings.TrimSpace(part.FileName())
			if filename == "" {
				_ = part.Close()
				return fields, nil, "", "", errors.New("filename is required")
			}
			return fields, part, filename, part.Header.Get("Content-Type"), nil
		}
		value, err := readOpenAIFileField(part)
		_ = part.Close()
		if err != nil {
			return fields, nil, "", "", err
		}
		fields.add(part.FormName(), value)
	}
}

func readOpenAIFileField(part *multipart.Part) (string, error) {
	data, err := io.ReadAll(io.LimitReader(part, openAIFileFieldMaxBytes+1))
	if err != nil {
		return "", err
	}
	if int64(len(data)) > openAIFileFieldMaxBytes {
		return "", errors.New("multipart field exceeds 64 KB")
	}
	return string(data), nil
}

// uploadOpenAIFile 上传文件内容，读完文件段后再读取其后的表单字段，与文件段之前的字段一起确定用途与有效期
func uploadOpenAIFile(
	ctx context.Context,
	service aistudio.FileService,
	reader *multipart.Reader,
	request aistudio.UploadRequest,
	fields openAIFileFields,
) (aistudio.FileRef, error) {
	uploadCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	consumed := make(chan error, 1)
	request.Reader = &completionReader{reader: request.Reader, done: consumed}
	resolved := make(chan openAIFileFieldsResult, 1)
	request.ResolveFields = func(resolveCtx context.Context) (aistudio.UploadFields, error) {
		select {
		case result := <-resolved:
			return result.value, result.err
		case <-resolveCtx.Done():
			return aistudio.UploadFields{}, resolveCtx.Err()
		}
	}
	done := make(chan openAIFileUploadResult, 1)
	go func() {
		ref, err := service.UploadFile(uploadCtx, request)
		done <- openAIFileUploadResult{ref: ref, err: err}
	}()
	select {
	case result := <-done:
		return result.ref, result.err
	case readErr := <-consumed:
		if !errors.Is(readErr, io.EOF) {
			result := <-done
			return result.ref, result.err
		}
	}
	value, parseErr := aistudio.UploadFields{}, readOpenAIFileFields(reader, &fields)
	if parseErr == nil {
		value, parseErr = fields.upload()
	}
	resolved <- openAIFileFieldsResult{value: value, err: parseErr}
	result := <-done
	if parseErr != nil {
		return result.ref, parseErr
	}
	return result.ref, result.err
}

// defaultFilePurpose 为省略 purpose 时的文件用途
const defaultFilePurpose = "user_data"

// openAIFileFields 是上传表单中文件之外的 purpose 与 expires_after 字段，同名字段以先出现的为准
type openAIFileFields struct {
	purpose string
	anchor  string
	seconds string
}

// add 记录一个表单字段
func (fields *openAIFileFields) add(name string, value string) {
	value = strings.TrimSpace(value)
	switch name {
	case "purpose":
		fields.purpose = cmp.Or(fields.purpose, value)
	case "expires_after[anchor]":
		fields.anchor = cmp.Or(fields.anchor, value)
	case "expires_after[seconds]":
		fields.seconds = cmp.Or(fields.seconds, value)
	}
}

// openAIBatchFileRetention 是 purpose 为 batch 且没有 expires_after 的文件的保留时长
const openAIBatchFileRetention = 30 * 24 * time.Hour

// upload 返回文件的用途与有效期：省略 purpose 时为 user_data，expires_after 的秒数在 3600 至 2592000 之间，purpose 为 batch 且没有 expires_after 时保留 30 天，其余文件不过期
func (fields openAIFileFields) upload() (aistudio.UploadFields, error) {
	result := aistudio.UploadFields{Purpose: cmp.Or(fields.purpose, defaultFilePurpose)}
	if fields.anchor == "" && fields.seconds == "" {
		if result.Purpose == "batch" {
			result.ExpiresAfter = openAIBatchFileRetention
		}
		return result, nil
	}
	if fields.anchor != "created_at" {
		return result, fmt.Errorf("%w: expires_after[anchor] must be created_at", aistudio.ErrInvalidArgument)
	}
	seconds, err := strconv.ParseInt(fields.seconds, 10, 64)
	if err != nil || seconds < 3600 || seconds > 2592000 {
		return result, fmt.Errorf("%w: expires_after[seconds] must be an integer between 3600 and 2592000", aistudio.ErrInvalidArgument)
	}
	result.ExpiresAfter = time.Duration(seconds) * time.Second
	return result, nil
}

// readOpenAIFileFields 读取文件段之后的表单字段并记入 fields，其后的文件段不读取
func readOpenAIFileFields(reader *multipart.Reader, fields *openAIFileFields) error {
	for {
		part, err := reader.NextPart()
		if errors.Is(err, io.EOF) {
			return nil
		}
		if err != nil {
			if errors.Is(err, context.Canceled) {
				return err
			}
			return fmt.Errorf("%w: %w", aistudio.ErrInvalidArgument, err)
		}
		if part.FileName() != "" {
			_ = part.Close()
			continue
		}
		value, readErr := readOpenAIFileField(part)
		_ = part.Close()
		if readErr != nil {
			if errors.Is(readErr, context.Canceled) {
				return readErr
			}
			return fmt.Errorf("%w: %w", aistudio.ErrInvalidArgument, readErr)
		}
		fields.add(part.FormName(), value)
	}
}

// handleFileGet 返回上传文件的元数据
func (s *server) handleFileGet(w http.ResponseWriter, r *http.Request) {
	service, ok := s.service.(aistudio.FileService)
	if !ok {
		writeFileError(w, r, http.StatusBadRequest, "invalid_request", "file metadata is unavailable")
		return
	}
	metadata, err := service.FileMetadata(r.Context(), strings.TrimSpace(r.PathValue("file")))
	if err != nil {
		writeFileRequestError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, officialOrder(fileProtocol(r), fileResponse(r, metadata)))
}

// handleFileContent 返回上传文件内容
func (s *server) handleFileContent(w http.ResponseWriter, r *http.Request) {
	service, ok := s.service.(aistudio.FileService)
	if !ok {
		writeFileError(w, r, http.StatusBadRequest, "invalid_request", "file content is unavailable")
		return
	}
	fileID := strings.TrimSpace(r.PathValue("file"))
	metadata, err := service.FileMetadata(r.Context(), fileID)
	if err != nil {
		writeFileRequestError(w, r, err)
		return
	}
	media, err := service.DownloadFile(r.Context(), fileID)
	if err != nil {
		writeFileRequestError(w, r, err)
		return
	}
	size := media.Size
	if size < 0 {
		size = metadata.Size
	}
	w.Header().Set("Content-Type", cmp.Or(strings.TrimSpace(media.MIME), metadata.MIME))
	filename := cmp.Or(strings.TrimSpace(media.Name), metadata.Name)
	w.Header().Set("Content-Disposition", mime.FormatMediaType("attachment", map[string]string{"filename": filename}))
	writeMediaStream(w, r, media.Body, size)
}

// writeMediaStream 写出 200 与文件内容，size 非负时带 Content-Length，复制或关闭出错时记入访问日志
func writeMediaStream(w http.ResponseWriter, r *http.Request, body io.ReadCloser, size int64) {
	if size >= 0 {
		w.Header().Set("Content-Length", strconv.FormatInt(size, 10))
	}
	w.WriteHeader(http.StatusOK)
	_, copyErr := io.Copy(w, body)
	if err := errors.Join(copyErr, body.Close()); err != nil {
		SetAccessLogError(r.Context(), err)
	}
}

// handleFileDelete 删除上传文件
func (s *server) handleFileDelete(w http.ResponseWriter, r *http.Request) {
	service, ok := s.service.(aistudio.FileService)
	if !ok {
		writeFileError(w, r, http.StatusBadRequest, "invalid_request", "file deletion is unavailable")
		return
	}
	fileID := strings.TrimSpace(r.PathValue("file"))
	if err := service.DeleteFile(r.Context(), fileID); err != nil {
		writeFileRequestError(w, r, err)
		return
	}
	if anthropicFilesRequest(r) {
		writeJSON(w, http.StatusOK, officialOrder("anthropic", map[string]any{"id": fileID, "type": "file_deleted"}))
		return
	}
	writeJSON(w, http.StatusOK, officialOrder("openai", map[string]any{"id": fileID, "object": "file", "deleted": true}))
}

// handleFileList 列出上传文件，Anthropic 请求返回 Files API 列表，其余按 OpenAI 的 after、limit、order 与 purpose 分页过滤
func (s *server) handleFileList(w http.ResponseWriter, r *http.Request) {
	service, ok := s.service.(aistudio.FileService)
	if !ok {
		writeFileError(w, r, http.StatusBadRequest, "invalid_request", "file listing is unavailable")
		return
	}
	files, err := service.ListFiles(r.Context())
	if err != nil {
		writeFileRequestError(w, r, err)
		return
	}
	if anthropicFilesRequest(r) {
		writeAnthropicFileList(w, r, files)
		return
	}
	query := r.URL.Query()
	if purpose := query.Get("purpose"); purpose != "" {
		files = slices.DeleteFunc(files, func(file aistudio.FileMetadata) bool { return file.Purpose != purpose })
	}
	listed, hasMore, err := openAIListPage(query, files, func(file aistudio.FileMetadata) string { return file.ID }, listLimits{fallback: 10000, min: 1, max: 10000})
	if err != nil {
		writeOpenAIError(w, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}
	data := make([]openAIFileObject, 0, len(listed))
	for _, file := range listed {
		data = append(data, openAIFileResponse(file))
	}
	body := map[string]any{"object": "list", "data": data, "has_more": hasMore, "first_id": nil, "last_id": nil}
	if len(listed) > 0 {
		body["first_id"], body["last_id"] = listed[0].ID, listed[len(listed)-1].ID
	}
	writeJSON(w, http.StatusOK, officialOrder("openai", body))
}

// anthropicFilePagePrefix 是 Files API next_page 游标的前缀，游标其余部分为上一页最后一个文件的 ID
const anthropicFilePagePrefix = "page_"

// writeAnthropicFileList 按 Files API 的 ids、scope_id、limit 与 page 游标返回一页文件，响应带 next_page、first_id、last_id 与 has_more
func writeAnthropicFileList(w http.ResponseWriter, r *http.Request, files []aistudio.FileMetadata) {
	query := r.URL.Query()
	if query.Get("scope_id") != "" {
		files = nil
	}
	var listed []aistudio.FileMetadata
	hasMore := false
	if ids := strings.Split(strings.Join(append(query["ids"], query["ids[]"]...), ","), ","); slices.ContainsFunc(ids, func(id string) bool { return id != "" }) {
		listed = slices.DeleteFunc(files, func(file aistudio.FileMetadata) bool { return !slices.Contains(ids, file.ID) })
	} else {
		if page := query.Get("page"); page != "" {
			query.Set("after_id", strings.TrimPrefix(page, anthropicFilePagePrefix))
		}
		var err error
		listed, hasMore, err = anthropicListPage(query, files, func(file aistudio.FileMetadata) string { return file.ID }, listLimits{fallback: 20, min: 1, max: 1000})
		if err != nil {
			writeAnthropicError(w, http.StatusBadRequest, "invalid_request_error", err.Error())
			return
		}
	}
	data := make([]any, 0, len(listed))
	for _, file := range listed {
		data = append(data, fileResponse(r, file))
	}
	body := map[string]any{"data": data, "next_page": nil, "has_more": hasMore, "first_id": nil, "last_id": nil}
	if len(listed) > 0 {
		body["first_id"], body["last_id"] = listed[0].ID, listed[len(listed)-1].ID
	}
	if hasMore && query.Get("before_id") == "" {
		body["next_page"] = anthropicFilePagePrefix + listed[len(listed)-1].ID
	}
	writeJSON(w, http.StatusOK, officialOrder("anthropic", body))
}

func multipartStreamMIME(file io.Reader, value string, filename string) (string, io.Reader, error) {
	probe := make([]byte, 512)
	count, readErr := io.ReadFull(file, probe)
	if readErr != nil && !errors.Is(readErr, io.EOF) && !errors.Is(readErr, io.ErrUnexpectedEOF) {
		return "", nil, readErr
	}
	if count == 0 {
		return "", nil, errors.New("file must not be empty")
	}
	stream := io.MultiReader(bytes.NewReader(probe[:count]), file)
	mediaType := attachmentMIME(declaredMediaType(value), filename, probe[:count])
	if mediaType != "image/gif" {
		return mediaType, stream, nil
	}
	data, err := io.ReadAll(stream)
	if err != nil {
		return "", nil, err
	}
	if frame, config, ok := decodeGIFFrame(data); ok {
		data, mediaType = gifJPEG(frame, config), "image/jpeg"
	}
	return mediaType, bytes.NewReader(data), nil
}

// gifJPEG 与官网上传 GIF 时一样把首帧画在同尺寸白底上，编码为质量 92 的 JPEG
func gifJPEG(frame *image.Paletted, config image.Config) []byte {
	canvas := image.NewRGBA(image.Rect(0, 0, config.Width, config.Height))
	draw.Draw(canvas, canvas.Bounds(), image.White, image.Point{}, draw.Src)
	draw.Draw(canvas, frame.Bounds(), frame, frame.Bounds().Min, draw.Over)
	var buf bytes.Buffer
	_ = jpeg.Encode(&buf, canvas, &jpeg.Options{Quality: 92})
	return buf.Bytes()
}

func multipartFileMIME(file io.ReadSeeker, value string, filename string) (string, error) {
	if mediaType := declaredMediaType(value); mediaType != "" {
		return mediaType, nil
	}
	probe := make([]byte, 512)
	count, err := file.Read(probe)
	if err != nil && !errors.Is(err, io.EOF) {
		return "", err
	}
	if _, err := file.Seek(0, io.SeekStart); err != nil {
		return "", err
	}
	return detectMediaType(filename, probe[:count]), nil
}

// declaredMediaType 返回分段声明的具体媒体类型，缺失、不含子类型或为通用二进制时返回空
func declaredMediaType(value string) string {
	mediaType, _, _ := mime.ParseMediaType(value)
	if !strings.Contains(mediaType, "/") || mediaType == "application/octet-stream" {
		return ""
	}
	return mediaType
}

// attachmentMIME 返回附件发往上游的 MIME：类型缺失、不含子类型或为通用二进制时按内容前缀与文件名识别，内容为文本而类型不是 text/*、application/json 与 application/x-ipynb+json 时为 text/plain
func attachmentMIME(mimeType string, name string, prefix []byte) string {
	mediaType := declaredMediaType(mimeType)
	if mediaType == "" {
		mimeType = detectMediaType(name, prefix)
		mediaType = mimeType
	}
	if !strings.HasPrefix(mediaType, "text/") && mediaType != "application/json" && mediaType != "application/x-ipynb+json" && textContent(prefix) {
		return "text/plain"
	}
	return mimeType
}

// textContent 表示内容开头 512 字节为不含二进制控制字符的 UTF-8 文本，截断处不完整的字符不计
func textContent(data []byte) bool {
	head := data[:min(len(data), 512)]
	valid := utf8.Valid(head)
	for cut := 1; !valid && len(head) == 512 && cut < utf8.UTFMax; cut++ {
		valid = utf8.Valid(head[:len(head)-cut])
	}
	return valid && strings.HasPrefix(http.DetectContentType(head), "text/")
}

// audioExtensionTypes 为 OpenAI 转录接受的文件扩展名对应的 MIME
var audioExtensionTypes = map[string]string{
	".flac": "audio/flac", ".m4a": "audio/mp4", ".mp3": "audio/mpeg", ".mp4": "video/mp4", ".mpeg": "audio/mpeg",
	".mpga": "audio/mpeg", ".ogg": "audio/ogg", ".wav": "audio/wav", ".webm": "audio/webm",
}

func openAIFileResponse(metadata aistudio.FileMetadata) openAIFileObject {
	object := openAIFileObject{
		ID: metadata.ID, Object: "file", Bytes: metadata.Size, CreatedAt: metadata.CreatedAt.Unix(),
		Filename: metadata.Name, Purpose: metadata.Purpose, Status: "processed",
	}
	if !metadata.ExpiresAt.IsZero() {
		expires := metadata.ExpiresAt.Unix()
		object.ExpiresAt = &expires
	}
	return object
}

// anthropicFilesRequest 判断文件请求是否来自 Anthropic Files API 客户端
func anthropicFilesRequest(r *http.Request) bool {
	return r.Header.Get("Anthropic-Version") != "" || strings.Contains(r.Header.Get("Anthropic-Beta"), "files-api-")
}

// fileResponse 按请求协议返回 Anthropic 文件元数据或 OpenAI 文件对象
func fileResponse(r *http.Request, metadata aistudio.FileMetadata) any {
	if !anthropicFilesRequest(r) {
		return openAIFileResponse(metadata)
	}
	return map[string]any{
		"id": metadata.ID, "type": "file", "filename": metadata.Name, "mime_type": metadata.MIME,
		"size_bytes": metadata.Size, "created_at": metadata.CreatedAt.UTC().Format(time.RFC3339), "downloadable": true,
	}
}

// fileProtocol 返回文件请求所属协议的字段顺序名
func fileProtocol(r *http.Request) string {
	if anthropicFilesRequest(r) {
		return "anthropic"
	}
	return "openai"
}

// writeFileError 按文件请求的协议写入错误
func writeFileError(w http.ResponseWriter, r *http.Request, status int, code string, message string) {
	if anthropicFilesRequest(r) {
		writeAnthropicError(w, status, anthropicStatusErrorType(status), message)
		return
	}
	writeOpenAIError(w, status, code, message)
}

// writeFileRequestError 写入文件服务错误，文件不存在时返回 404
func writeFileRequestError(w http.ResponseWriter, r *http.Request, err error) {
	if !shouldWriteRequestError(r, err) {
		return
	}
	if errors.Is(err, aistudio.ErrResourceNotFound) {
		writeFileError(w, r, http.StatusNotFound, "file_not_found", "file not found")
		return
	}
	writeFileError(w, r, statusFromError(err), openAIErrorCode(err), err.Error())
}

// writeFileParseError 写入 multipart 请求体解析错误，超过大小上限时返回 413
func writeFileParseError(w http.ResponseWriter, r *http.Request, err error) {
	var maxBytes *http.MaxBytesError
	if errors.As(err, &maxBytes) {
		writeFileError(w, r, http.StatusRequestEntityTooLarge, "file_too_large", "file exceeds 512 MB")
		return
	}
	writeFileError(w, r, http.StatusBadRequest, "invalid_request", err.Error())
}

// listLimits 是列表接口 limit 的默认值与取值范围
type listLimits struct {
	fallback, min, max int
}

// listLimit 读取 limit 查询参数，缺失时使用默认值
func listLimit(query url.Values, limits listLimits) (int, error) {
	raw := query.Get("limit")
	if raw == "" {
		return limits.fallback, nil
	}
	value, err := strconv.Atoi(raw)
	if err != nil || value < limits.min || value > limits.max {
		return 0, fmt.Errorf("limit must be an integer between %d and %d", limits.min, limits.max)
	}
	return value, nil
}

// openAIListPage 按 order、after 与 limit 返回一页对象及其后是否还有对象，items 按创建时间从新到旧排列，未知的 after 返回空页
func openAIListPage[T any](query url.Values, items []T, id func(T) string, limits listLimits) ([]T, bool, error) {
	limit, err := listLimit(query, limits)
	if err != nil {
		return nil, false, err
	}
	switch query.Get("order") {
	case "", "desc":
	case "asc":
		items = slices.Clone(items)
		slices.Reverse(items)
	default:
		return nil, false, errors.New("order must be asc or desc")
	}
	start := 0
	if after := query.Get("after"); after != "" {
		start = len(items)
		if index := slices.IndexFunc(items, func(item T) bool { return id(item) == after }); index >= 0 {
			start = index + 1
		}
	}
	end := min(start+limit, len(items))
	return items[start:end], end < len(items), nil
}

// anthropicListPage 按 before_id、after_id 与 limit 返回一页对象及该方向是否还有对象，items 按创建时间从新到旧排列，未知游标返回空页
func anthropicListPage[T any](query url.Values, items []T, id func(T) string, limits listLimits) ([]T, bool, error) {
	limit, err := listLimit(query, limits)
	if err != nil {
		return nil, false, err
	}
	cursor := func(value string) int { return slices.IndexFunc(items, func(item T) bool { return id(item) == value }) }
	if before := query.Get("before_id"); before != "" {
		end := max(cursor(before), 0)
		start := max(end-limit, 0)
		return items[start:end], start > 0, nil
	}
	start := 0
	if after := query.Get("after_id"); after != "" {
		start = len(items)
		if index := cursor(after); index >= 0 {
			start = index + 1
		}
	}
	end := min(start+limit, len(items))
	return items[start:end], end < len(items), nil
}
