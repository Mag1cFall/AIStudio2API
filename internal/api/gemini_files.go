package api

import (
	"bytes"
	"cmp"
	"context"
	"crypto/rand"
	"encoding/base32"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"mime/multipart"
	"net/http"
	"net/url"
	"os"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/Mag1cFall/AIStudio2API/internal/aistudio"
)

// geminiFilesURIPrefix 为 Files API 文件 URI 的前缀
const geminiFilesURIPrefix = "https://generativelanguage.googleapis.com/v1beta/files/"

// geminiUploadTTL 为可续传上传会话在两次请求之间的保留时长
const geminiUploadTTL = time.Hour

// geminiFileNames 把 Drive 文件 ID 编码为 Files API 资源 ID 允许的小写字母与数字
var geminiFileNames = base32.StdEncoding.WithPadding(base32.NoPadding)

// geminiFileMetadata 是创建文件请求携带的文件元数据
type geminiFileMetadata struct {
	File struct {
		Name        string `json:"name"`
		DisplayName string `json:"displayName"`
		MIMEType    string `json:"mimeType"`
	} `json:"file"`
}

// geminiFileAliasPattern 是客户端可指定的文件 ID：小写字母、数字与连字符，不以连字符开头或结尾，最长 40 个字符
var geminiFileAliasPattern = regexp.MustCompile(`^[a-z0-9]([a-z0-9-]{0,38}[a-z0-9])?$`)

// alias 校验并返回元数据中客户端指定的文件 ID，未指定时为空
func (metadata geminiFileMetadata) alias() (string, error) {
	name := strings.TrimPrefix(strings.TrimSpace(metadata.File.Name), "files/")
	if name == "" {
		return "", nil
	}
	if !geminiFileAliasPattern.MatchString(name) {
		return "", fmt.Errorf("file.name %q must contain 1 to 40 lowercase letters, digits or dashes and cannot begin or end with a dash", metadata.File.Name)
	}
	return name, nil
}

// geminiFileIDs 列出全部上传文件并返回公开文件 ID 对应的 Drive 文件 ID
func geminiFileIDs(ctx context.Context, service aistudio.FileService) (map[string]string, error) {
	files, err := service.ListFiles(ctx)
	if err != nil {
		return nil, err
	}
	ids := make(map[string]string, len(files))
	for _, file := range files {
		ids[geminiPublicFileID(file)] = file.ID
	}
	return ids, nil
}

// geminiFileDriveID 解析 files/ 资源名：本服务签发的 ID 直接解码，客户端指定的 ID 从文件列表查找
func geminiFileDriveID(ctx context.Context, service aistudio.FileService, name string) (string, bool, error) {
	if driveID, ok := geminiDriveID(name); ok {
		return driveID, true, nil
	}
	ids, err := geminiFileIDs(ctx, service)
	if err != nil {
		return "", false, err
	}
	driveID, ok := ids[strings.TrimPrefix(name, "files/")]
	return driveID, ok, nil
}

// resolveGeminiFileNames 把引用客户端指定文件 ID 的 Files API URI 换成 Drive 文件 ID
func resolveGeminiFileNames(ctx context.Context, service aistudio.Service, contents []aistudio.Content) error {
	files, ok := service.(aistudio.FileService)
	if !ok {
		return nil
	}
	var ids map[string]string
	for contentIndex := range contents {
		for partIndex := range contents[contentIndex].Parts {
			file := contents[contentIndex].Parts[partIndex].File
			if file == nil || !geminiFilesAPIURI(file.ID) {
				continue
			}
			if ids == nil {
				var err error
				if ids, err = geminiFileIDs(ctx, files); err != nil {
					return err
				}
			}
			_, name, _ := strings.Cut(file.ID, "/files/")
			if driveID, found := ids[name]; found {
				file.ID = driveID
			}
		}
	}
	return nil
}

// geminiUploadSession 是一次可续传上传的元数据与已接收的分块
type geminiUploadSession struct {
	mu       sync.Mutex
	alias    string
	name     string
	mime     string
	received int64
	buffer   *os.File
	expires  time.Time
	timer    *time.Timer
	done     bool
}

// geminiUploads 保存进行中的可续传上传会话
type geminiUploads struct {
	mu       sync.Mutex
	sessions map[string]*geminiUploadSession
}

func newGeminiUploads() *geminiUploads {
	return &geminiUploads{sessions: make(map[string]*geminiUploadSession)}
}

// start 登记新的上传会话并返回会话 ID，会话闲置超过保留时长后删除
func (uploads *geminiUploads) start(session *geminiUploadSession) string {
	id := rand.Text()
	session.expires = time.Now().Add(geminiUploadTTL)
	session.timer = time.AfterFunc(geminiUploadTTL, func() {
		session.mu.Lock()
		defer session.mu.Unlock()
		if !session.done && !time.Now().Before(session.expires) {
			uploads.discard(id, session)
		}
	})
	uploads.mu.Lock()
	uploads.sessions[id] = session
	uploads.mu.Unlock()
	return id
}

func (uploads *geminiUploads) get(id string) *geminiUploadSession {
	uploads.mu.Lock()
	defer uploads.mu.Unlock()
	return uploads.sessions[id]
}

// discard 结束会话并删除已缓存的分块，调用方持有会话锁
func (uploads *geminiUploads) discard(id string, session *geminiUploadSession) {
	uploads.mu.Lock()
	if uploads.sessions[id] == session {
		delete(uploads.sessions, id)
	}
	uploads.mu.Unlock()
	session.done = true
	session.timer.Stop()
	if session.buffer != nil {
		_ = session.buffer.Close()
		_ = os.Remove(session.buffer.Name())
		session.buffer = nil
	}
}

// geminiFileID 返回 Drive 文件对应的 Files API 资源 ID
func geminiFileID(driveID string) string {
	return strings.ToLower(geminiFileNames.EncodeToString([]byte(driveID)))
}

// geminiDriveID 解析本服务签发的 Files API 资源 ID 或 files/ 资源名，签发的 ID 长于客户端可指定的文件 ID
func geminiDriveID(name string) (string, bool) {
	name = strings.TrimPrefix(name, "files/")
	if geminiFileAliasPattern.MatchString(name) {
		return "", false
	}
	decoded, err := geminiFileNames.DecodeString(strings.ToUpper(name))
	if err != nil || len(decoded) == 0 {
		return "", false
	}
	for _, char := range decoded {
		if !(char >= 'a' && char <= 'z' || char >= 'A' && char <= 'Z' || char >= '0' && char <= '9' || char == '-' || char == '_') {
			return "", false
		}
	}
	return string(decoded), true
}

// geminiUploadedFileID 返回本服务签发的 Files API 文件 URI 对应的 Drive 文件 ID
func geminiUploadedFileID(uri string) (string, bool) {
	if !geminiFilesAPIURI(uri) {
		return "", false
	}
	parsed, _ := url.Parse(uri)
	_, name, _ := strings.Cut(parsed.Path, "/files/")
	return geminiDriveID(name)
}

// geminiPublicFileID 返回文件的公开 ID：客户端指定的 ID 或由 Drive 文件 ID 编码的 ID
func geminiPublicFileID(metadata aistudio.FileMetadata) string {
	return cmp.Or(metadata.Alias, geminiFileID(metadata.ID))
}

// geminiFileObject 构造 Files API 的 File 资源
func geminiFileObject(metadata aistudio.FileMetadata) map[string]any {
	id := geminiPublicFileID(metadata)
	created := metadata.CreatedAt.UTC().Format(time.RFC3339Nano)
	object := map[string]any{
		"name": "files/" + id, "displayName": metadata.Name, "mimeType": metadata.MIME,
		"sizeBytes": strconv.FormatInt(metadata.Size, 10), "createTime": created, "updateTime": created,
		"uri": geminiFilesURIPrefix + id, "state": "ACTIVE", "source": "UPLOADED",
	}
	if metadata.SHA256 != "" {
		object["sha256Hash"] = metadata.SHA256
	}
	if metadata.VideoDuration > 0 {
		object["videoMetadata"] = map[string]any{"videoDuration": strconv.FormatFloat(metadata.VideoDuration.Seconds(), 'f', -1, 64) + "s"}
	}
	return object
}

// handleGeminiFileUpload 按 X-Goog-Upload-Protocol 或 uploadType 开始可续传上传或完成 multipart 上传
func (s *server) handleGeminiFileUpload(w http.ResponseWriter, r *http.Request) {
	service, ok := s.service.(aistudio.FileService)
	if !ok {
		writeGeminiError(w, http.StatusBadRequest, "FAILED_PRECONDITION", "file upload is unavailable")
		return
	}
	protocol := strings.ToLower(strings.TrimSpace(r.Header.Get("X-Goog-Upload-Protocol")))
	if protocol == "" {
		protocol = strings.ToLower(r.URL.Query().Get("uploadType"))
	}
	switch protocol {
	case "resumable":
		s.startGeminiUpload(w, r)
	case "multipart":
		uploadGeminiMultipart(w, r, service)
	default:
		writeGeminiError(w, http.StatusBadRequest, "INVALID_ARGUMENT", "X-Goog-Upload-Protocol must be resumable or multipart")
	}
}

// startGeminiUpload 创建可续传上传会话，响应头 X-Goog-Upload-URL 为后续分块的上传地址
func (s *server) startGeminiUpload(w http.ResponseWriter, r *http.Request) {
	if command := strings.ToLower(strings.TrimSpace(r.Header.Get("X-Goog-Upload-Command"))); command != "start" {
		writeGeminiError(w, http.StatusBadRequest, "INVALID_ARGUMENT", "X-Goog-Upload-Command must be start")
		return
	}
	metadata, err := readGeminiFileMetadata(r.Body)
	if err != nil {
		writeGeminiError(w, http.StatusBadRequest, "INVALID_ARGUMENT", err.Error())
		return
	}
	alias, ok := checkGeminiFileAlias(w, metadata)
	if !ok {
		return
	}
	session := &geminiUploadSession{
		alias: alias, name: geminiUploadName(metadata, "", r),
		mime: firstMediaType(r.Header.Get("X-Goog-Upload-Header-Content-Type"), metadata.File.MIMEType),
	}
	id := s.uploads.start(session)
	w.Header().Set("X-Goog-Upload-URL", requestOrigin(r)+"/upload/v1beta/files?upload_id="+url.QueryEscape(id)+"&upload_protocol=resumable")
	w.Header().Set("X-Goog-Upload-Status", "active")
	w.WriteHeader(http.StatusOK)
}

// geminiUploadHandler 让携带 upload_id 的分块请求凭上传会话鉴权，其余上传请求交给公开路由
func (s *server) geminiUploadHandler(public http.Handler) http.Handler {
	chunks := bodyLimitMiddleware(http.HandlerFunc(s.handleGeminiUploadChunk))
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Has("upload_id") {
			chunks.ServeHTTP(w, r)
			return
		}
		public.ServeHTTP(w, r)
	})
}

// handleGeminiUploadChunk 处理可续传上传的 upload、finalize、query 与 cancel 命令，finalize 时上传全部内容
func (s *server) handleGeminiUploadChunk(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost && r.Method != http.MethodPut {
		w.Header().Set("Allow", "POST, PUT")
		writeGeminiError(w, http.StatusMethodNotAllowed, "INVALID_ARGUMENT", "upload commands require POST or PUT")
		return
	}
	id := r.URL.Query().Get("upload_id")
	session := s.uploads.get(id)
	if session == nil {
		writeGeminiUploadError(w, http.StatusNotFound, "NOT_FOUND", "upload session was not found", "final")
		return
	}
	markAccessLogAuthorized(r.Context())
	session.mu.Lock()
	defer session.mu.Unlock()
	if session.done {
		writeGeminiUploadError(w, http.StatusNotFound, "NOT_FOUND", "upload session was not found", "final")
		return
	}
	session.expires = time.Now().Add(geminiUploadTTL)
	session.timer.Reset(geminiUploadTTL)
	commands := make(map[string]bool)
	for _, command := range strings.Split(strings.ToLower(r.Header.Get("X-Goog-Upload-Command")), ",") {
		commands[strings.TrimSpace(command)] = true
	}
	switch {
	case commands["cancel"]:
		s.uploads.discard(id, session)
		w.Header().Set("X-Goog-Upload-Status", "cancelled")
		w.WriteHeader(http.StatusOK)
		return
	case commands["query"]:
		w.Header().Set("X-Goog-Upload-Status", "active")
		w.Header().Set("X-Goog-Upload-Size-Received", strconv.FormatInt(session.received, 10))
		w.WriteHeader(http.StatusOK)
		return
	case !commands["upload"] && !commands["finalize"]:
		writeGeminiUploadError(w, http.StatusBadRequest, "INVALID_ARGUMENT", "X-Goog-Upload-Command must contain upload, finalize, query or cancel", "active")
		return
	}
	if offset := strings.TrimSpace(r.Header.Get("X-Goog-Upload-Offset")); commands["upload"] && offset != "" && offset != strconv.FormatInt(session.received, 10) {
		writeGeminiUploadError(w, http.StatusBadRequest, "INVALID_ARGUMENT", fmt.Sprintf("X-Goog-Upload-Offset %s does not match the %d bytes received", offset, session.received), "active")
		return
	}
	var content io.Reader = r.Body
	if !commands["upload"] {
		content = http.NoBody
	}
	if !commands["finalize"] || session.buffer != nil {
		if err := bufferGeminiUpload(session, content); err != nil {
			s.uploads.discard(id, session)
			writeGeminiUploadFailure(w, err, "final")
			return
		}
		if !commands["finalize"] {
			w.Header().Set("X-Goog-Upload-Status", "active")
			w.Header().Set("X-Goog-Upload-Size-Received", strconv.FormatInt(session.received, 10))
			w.WriteHeader(http.StatusOK)
			return
		}
		if _, err := session.buffer.Seek(0, io.SeekStart); err != nil {
			s.uploads.discard(id, session)
			writeGeminiUploadError(w, http.StatusInternalServerError, "INTERNAL", err.Error(), "final")
			return
		}
		content = session.buffer
	}
	metadata, err := uploadGeminiFile(r.Context(), s.service.(aistudio.FileService), session.alias, session.name, session.mime, content)
	s.uploads.discard(id, session)
	if err != nil {
		if shouldWriteRequestError(r, err) {
			writeGeminiUploadFailure(w, err, "final")
		}
		return
	}
	w.Header().Set("X-Goog-Upload-Status", "final")
	writeJSON(w, http.StatusOK, officialOrder("gemini", map[string]any{"file": geminiFileObject(metadata)}))
}

// bufferGeminiUpload 把分块追加到会话的临时文件
func bufferGeminiUpload(session *geminiUploadSession, content io.Reader) error {
	if session.buffer == nil {
		buffer, err := os.CreateTemp("", "aistudio2api-upload-*")
		if err != nil {
			return err
		}
		session.buffer = buffer
	}
	written, err := io.Copy(session.buffer, io.LimitReader(content, openAIFileMaxBytes-session.received+1))
	session.received += written
	if err != nil {
		return err
	}
	if session.received > openAIFileMaxBytes {
		return &http.MaxBytesError{Limit: openAIFileMaxBytes}
	}
	return nil
}

// uploadGeminiMultipart 处理元数据与文件内容位于同一 multipart 请求体的上传
func uploadGeminiMultipart(w http.ResponseWriter, r *http.Request, service aistudio.FileService) {
	mediaType, parameters, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if err != nil || !strings.HasPrefix(mediaType, "multipart/") || parameters["boundary"] == "" {
		writeGeminiError(w, http.StatusBadRequest, "INVALID_ARGUMENT", "multipart upload requires a multipart Content-Type with boundary")
		return
	}
	reader := multipart.NewReader(r.Body, parameters["boundary"])
	var metadata geminiFileMetadata
	metadataRead := false
	for {
		part, err := reader.NextPart()
		if errors.Is(err, io.EOF) {
			writeGeminiError(w, http.StatusBadRequest, "INVALID_ARGUMENT", "multipart upload requires file content")
			return
		}
		if err != nil {
			writeGeminiUploadFailure(w, fmt.Errorf("%w: %w", aistudio.ErrInvalidArgument, err), "")
			return
		}
		partType, _, _ := mime.ParseMediaType(part.Header.Get("Content-Type"))
		if !metadataRead && part.FileName() == "" && (partType == "application/json" || part.FormName() == "metadata") {
			metadata, err = readGeminiFileMetadata(part)
			_ = part.Close()
			if err != nil {
				writeGeminiError(w, http.StatusBadRequest, "INVALID_ARGUMENT", err.Error())
				return
			}
			metadataRead = true
			continue
		}
		alias, ok := checkGeminiFileAlias(w, metadata)
		if !ok {
			return
		}
		file, err := uploadGeminiFile(r.Context(), service, alias, geminiUploadName(metadata, part.FileName(), r),
			firstMediaType(part.Header.Get("Content-Type"), r.Header.Get("X-Goog-Upload-Header-Content-Type"), metadata.File.MIMEType), part)
		_ = part.Close()
		if err != nil {
			if shouldWriteRequestError(r, err) {
				writeGeminiUploadFailure(w, err, "")
			}
			return
		}
		w.Header().Set("X-Goog-Upload-Status", "final")
		writeJSON(w, http.StatusOK, officialOrder("gemini", map[string]any{"file": geminiFileObject(file)}))
		return
	}
}

// checkGeminiFileAlias 校验客户端指定的文件 ID 格式，无效时写出错误并返回 false
func checkGeminiFileAlias(w http.ResponseWriter, metadata geminiFileMetadata) (string, bool) {
	alias, err := metadata.alias()
	if err != nil {
		writeGeminiError(w, http.StatusBadRequest, "INVALID_ARGUMENT", err.Error())
		return "", false
	}
	return alias, true
}

// uploadGeminiFile 上传文件内容并返回文件元数据，未声明具体类型时按内容识别，alias 为客户端指定的文件 ID
func uploadGeminiFile(ctx context.Context, service aistudio.FileService, alias string, name string, mimeType string, content io.Reader) (aistudio.FileMetadata, error) {
	mediaType, stream, err := multipartStreamMIME(content, mimeType, name)
	if err != nil {
		var maxBytes *http.MaxBytesError
		if errors.As(err, &maxBytes) {
			return aistudio.FileMetadata{}, err
		}
		return aistudio.FileMetadata{}, fmt.Errorf("%w: %w", aistudio.ErrInvalidArgument, err)
	}
	ref, err := service.UploadFile(ctx, aistudio.UploadRequest{
		Alias: alias, Name: name, MIME: mediaType, Purpose: defaultFilePurpose, Size: -1, MaxSize: openAIFileMaxBytes, Reader: stream,
	})
	if err != nil {
		return aistudio.FileMetadata{}, err
	}
	return service.FileMetadata(ctx, ref.ID)
}

// readGeminiFileMetadata 读取创建文件请求的元数据，请求体为空时返回空元数据
func readGeminiFileMetadata(body io.Reader) (geminiFileMetadata, error) {
	var metadata geminiFileMetadata
	raw, err := io.ReadAll(io.LimitReader(body, openAIFileFieldMaxBytes+1))
	if err != nil {
		return metadata, err
	}
	if int64(len(raw)) > openAIFileFieldMaxBytes {
		return metadata, errors.New("file metadata exceeds 64 KB")
	}
	if len(bytes.TrimSpace(raw)) == 0 {
		return metadata, nil
	}
	raw, _, err = geminiCamelKeys(doubleQuotedJSON(raw))
	if err == nil {
		err = json.Unmarshal(raw, &metadata)
	}
	if err != nil {
		return metadata, fmt.Errorf("file metadata: %w", err)
	}
	return metadata, nil
}

// doubleQuotedJSON 把 Google API 请求体接受的单引号字符串改写为 JSON 双引号字符串
func doubleQuotedJSON(raw []byte) []byte {
	if !bytes.ContainsRune(raw, '\'') {
		return raw
	}
	converted := make([]byte, 0, len(raw)+8)
	var quote byte
	for index := 0; index < len(raw); index++ {
		char := raw[index]
		switch {
		case quote == 0:
			if char == '\'' || char == '"' {
				quote = char
				char = '"'
			}
			converted = append(converted, char)
		case char == '\\' && index+1 < len(raw):
			index++
			if quote == '\'' && raw[index] == '\'' {
				converted = append(converted, '\'')
			} else {
				converted = append(converted, char, raw[index])
			}
		case char == quote:
			quote = 0
			converted = append(converted, '"')
		case char == '"':
			converted = append(converted, '\\', '"')
		default:
			converted = append(converted, char)
		}
	}
	return converted
}

// geminiUploadName 依次取 displayName、文件段名称与 X-Goog-Upload-File-Name 作为文件名
func geminiUploadName(metadata geminiFileMetadata, filename string, r *http.Request) string {
	for _, name := range []string{metadata.File.DisplayName, filename, r.Header.Get("X-Goog-Upload-File-Name")} {
		if name = strings.TrimSpace(name); name != "" {
			return name
		}
	}
	return "file"
}

// firstMediaType 返回第一个具体的媒体类型声明
func firstMediaType(values ...string) string {
	for _, value := range values {
		if mediaType := declaredMediaType(value); mediaType != "" {
			return mediaType
		}
	}
	return ""
}

// geminiUploadFailure 返回上传失败的 HTTP 状态与状态名，超过文件大小上限时为 413
func geminiUploadFailure(err error) (int, string) {
	var maxBytes *http.MaxBytesError
	if errors.As(err, &maxBytes) || statusFromError(err) == http.StatusRequestEntityTooLarge {
		return http.StatusRequestEntityTooLarge, "INVALID_ARGUMENT"
	}
	return statusFromError(err), geminiErrorStatus(err)
}

// writeGeminiUploadError 写入带 X-Goog-Upload-Status 的上传错误
func writeGeminiUploadError(w http.ResponseWriter, status int, statusName string, message string, uploadStatus string) {
	w.Header().Set("X-Goog-Upload-Status", uploadStatus)
	writeGeminiError(w, status, statusName, message)
}

// writeGeminiUploadFailure 按上传失败原因写入错误，uploadStatus 非空时同时写入 X-Goog-Upload-Status
func writeGeminiUploadFailure(w http.ResponseWriter, err error, uploadStatus string) {
	status, statusName := geminiUploadFailure(err)
	if uploadStatus != "" {
		w.Header().Set("X-Goog-Upload-Status", uploadStatus)
	}
	writeGeminiError(w, status, statusName, err.Error())
}

// requestOrigin 返回客户端访问本服务使用的协议与主机
func requestOrigin(r *http.Request) string {
	scheme := "http"
	if r.TLS != nil {
		scheme = "https"
	}
	if forwarded := strings.TrimSpace(r.Header.Get("X-Forwarded-Proto")); forwarded != "" {
		scheme = forwarded
	}
	return scheme + "://" + r.Host
}

// handleGeminiFileGet 返回上传文件的 File 资源，files/{name}:download 返回文件内容
func (s *server) handleGeminiFileGet(w http.ResponseWriter, r *http.Request) {
	service, ok := s.service.(aistudio.FileService)
	if !ok {
		writeGeminiError(w, http.StatusBadRequest, "FAILED_PRECONDITION", "file metadata is unavailable")
		return
	}
	if name, download := strings.CutSuffix(r.PathValue("file"), ":download"); download {
		s.handleGeminiFileDownload(w, r, service, name)
		return
	}
	driveID, ok, err := geminiFileDriveID(r.Context(), service, r.PathValue("file"))
	if err != nil {
		writeGeminiFileError(w, r, err)
		return
	}
	if !ok {
		writeGeminiFileNotFound(w, r.PathValue("file"))
		return
	}
	metadata, err := service.FileMetadata(r.Context(), driveID)
	if err != nil {
		writeGeminiFileError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, officialOrder("gemini", geminiFileObject(metadata)))
}

// handleGeminiFileURIGet 处理官方 Python SDK 对非 https 文件 uri 拼出的 files/<uri> 路径，取 uri 中最后一个 files/ 之后的文件名
func (s *server) handleGeminiFileURIGet(w http.ResponseWriter, r *http.Request) {
	uri := r.PathValue("uri")
	index := strings.LastIndex(uri, "/files/")
	if index < 0 {
		writeGeminiFileNotFound(w, uri)
		return
	}
	r.SetPathValue("file", uri[index+len("/files/"):])
	s.handleGeminiFileGet(w, r)
}

// handleGeminiFileDownload 返回文件内容
func (s *server) handleGeminiFileDownload(w http.ResponseWriter, r *http.Request, service aistudio.FileService, name string) {
	driveID, ok, err := geminiFileDriveID(r.Context(), service, name)
	if err != nil {
		writeGeminiFileError(w, r, err)
		return
	}
	if !ok {
		writeGeminiFileNotFound(w, name)
		return
	}
	media, err := service.DownloadFile(r.Context(), driveID)
	if err != nil {
		writeGeminiFileError(w, r, err)
		return
	}
	w.Header().Set("Content-Type", cmp.Or(strings.TrimSpace(media.MIME), "application/octet-stream"))
	if media.Size >= 0 {
		w.Header().Set("Content-Length", strconv.FormatInt(media.Size, 10))
	}
	w.WriteHeader(http.StatusOK)
	_, copyErr := io.Copy(w, media.Body)
	closeErr := media.Body.Close()
	if requestErr := errors.Join(copyErr, closeErr); requestErr != nil {
		SetAccessLogError(r.Context(), requestErr)
	}
}

// handleGeminiFileDelete 删除上传文件并返回空对象
func (s *server) handleGeminiFileDelete(w http.ResponseWriter, r *http.Request) {
	service, ok := s.service.(aistudio.FileService)
	if !ok {
		writeGeminiError(w, http.StatusBadRequest, "FAILED_PRECONDITION", "file deletion is unavailable")
		return
	}
	driveID, ok, err := geminiFileDriveID(r.Context(), service, r.PathValue("file"))
	if err != nil {
		writeGeminiFileError(w, r, err)
		return
	}
	if !ok {
		writeGeminiFileNotFound(w, r.PathValue("file"))
		return
	}
	if err := service.DeleteFile(r.Context(), driveID); err != nil {
		writeGeminiFileError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{})
}

// handleGeminiFileList 按创建时间从新到旧分页列出上传文件，pageToken 为下一页的起始位置
func (s *server) handleGeminiFileList(w http.ResponseWriter, r *http.Request) {
	service, ok := s.service.(aistudio.FileService)
	if !ok {
		writeGeminiError(w, http.StatusBadRequest, "FAILED_PRECONDITION", "file listing is unavailable")
		return
	}
	files, err := service.ListFiles(r.Context())
	if err != nil {
		if shouldWriteRequestError(r, err) {
			writeGeminiRequestError(w, err)
		}
		return
	}
	page, next, err := geminiPage(r.URL.Query(), files, 10, 100)
	if err != nil {
		writeGeminiError(w, http.StatusBadRequest, "INVALID_ARGUMENT", err.Error())
		return
	}
	objects := make([]map[string]any, 0, len(page))
	for _, file := range page {
		objects = append(objects, geminiFileObject(file))
	}
	body := map[string]any{"files": objects}
	if next != "" {
		body["nextPageToken"] = next
	}
	writeJSON(w, http.StatusOK, officialOrder("gemini", body))
}

// firstQueryValue 返回第一个非空查询参数，查询参数接受驼峰与下划线两种字段名
func firstQueryValue(query url.Values, names ...string) string {
	for _, name := range names {
		if value := query.Get(name); value != "" {
			return value
		}
	}
	return ""
}

// writeGeminiFileError 写入文件查询与删除的错误，文件不存在时返回 404
func writeGeminiFileError(w http.ResponseWriter, r *http.Request, err error) {
	if !shouldWriteRequestError(r, err) {
		return
	}
	if errors.Is(err, aistudio.ErrResourceNotFound) {
		writeGeminiFileNotFound(w, r.PathValue("file"))
		return
	}
	writeGeminiRequestError(w, err)
}

// writeGeminiFileNotFound 返回文件不存在的 404
func writeGeminiFileNotFound(w http.ResponseWriter, name string) {
	writeGeminiError(w, http.StatusNotFound, "NOT_FOUND", fmt.Sprintf("file %q was not found", strings.TrimPrefix(name, "files/")))
}
