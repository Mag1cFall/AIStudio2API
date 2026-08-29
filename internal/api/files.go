package api

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"mime"
	"mime/multipart"
	"net/http"
	"strconv"
	"strings"

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

type openAIFilePurposeResult struct {
	value string
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
	Filename  string `json:"filename"`
	Purpose   string `json:"purpose"`
	Status    string `json:"status"`
}

func (s *server) handleOpenAIFileUpload(w http.ResponseWriter, r *http.Request) {
	service, ok := s.service.(aistudio.FileService)
	if !ok {
		writeOpenAIError(w, http.StatusBadRequest, "invalid_request", "file upload is unavailable")
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, openAIFileMaxBytes+openAIFileRequestOverhead)
	reader, err := r.MultipartReader()
	if err != nil {
		writeOpenAIFileParseError(w, err)
		return
	}
	purpose, file, filename, contentType, err := readOpenAIFileParts(reader)
	if err != nil {
		writeOpenAIFileParseError(w, err)
		return
	}
	defer file.Close()
	mimeType, stream, err := multipartStreamMIME(file, contentType)
	if err != nil {
		writeOpenAIError(w, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}
	request := aistudio.UploadRequest{
		Name: filename, MIME: mimeType, Purpose: purpose, Size: -1, MaxSize: openAIFileMaxBytes, Reader: stream,
	}
	var ref aistudio.FileRef
	if purpose != "" {
		ref, err = service.UploadFile(r.Context(), request)
	} else {
		ref, err = uploadOpenAIFileBeforePurpose(r.Context(), service, reader, request)
	}
	if err != nil {
		if !shouldWriteRequestError(r, err) {
			return
		}
		var maxBytes *http.MaxBytesError
		if errors.As(err, &maxBytes) {
			writeOpenAIFileParseError(w, err)
			return
		}
		writeOpenAIError(w, statusFromError(err), openAIErrorCode(err), err.Error())
		return
	}
	metadata, err := service.FileMetadata(r.Context(), ref.ID)
	if err != nil {
		if !shouldWriteRequestError(r, err) {
			return
		}
		writeOpenAIError(w, statusFromError(err), openAIErrorCode(err), err.Error())
		return
	}
	writeJSON(w, http.StatusOK, openAIFileResponse(metadata))
}

func readOpenAIFileParts(reader *multipart.Reader) (string, *multipart.Part, string, string, error) {
	purpose := ""
	for {
		part, err := reader.NextPart()
		if errors.Is(err, io.EOF) {
			return "", nil, "", "", errors.New("file is required")
		}
		if err != nil {
			return "", nil, "", "", err
		}
		if part.FormName() == "file" && part.FileName() != "" {
			filename := strings.TrimSpace(part.FileName())
			if filename == "" {
				_ = part.Close()
				return "", nil, "", "", errors.New("filename is required")
			}
			return purpose, part, filename, part.Header.Get("Content-Type"), nil
		}
		value, err := readOpenAIFileField(part)
		_ = part.Close()
		if err != nil {
			return "", nil, "", "", err
		}
		if part.FormName() == "purpose" && purpose == "" {
			purpose = strings.TrimSpace(value)
		}
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

func uploadOpenAIFileBeforePurpose(
	ctx context.Context,
	service aistudio.FileService,
	reader *multipart.Reader,
	request aistudio.UploadRequest,
) (aistudio.FileRef, error) {
	uploadCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	consumed := make(chan error, 1)
	request.Reader = &completionReader{reader: request.Reader, done: consumed}
	purpose := make(chan openAIFilePurposeResult, 1)
	request.ResolvePurpose = func(resolveCtx context.Context) (string, error) {
		select {
		case result := <-purpose:
			return result.value, result.err
		case <-resolveCtx.Done():
			return "", resolveCtx.Err()
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
	value, parseErr := readOpenAIFilePurpose(reader)
	purpose <- openAIFilePurposeResult{value: value, err: parseErr}
	result := <-done
	if parseErr != nil {
		return result.ref, parseErr
	}
	return result.ref, result.err
}

func readOpenAIFilePurpose(reader *multipart.Reader) (string, error) {
	for {
		part, err := reader.NextPart()
		if errors.Is(err, io.EOF) {
			return "", fmt.Errorf("%w: purpose is required", aistudio.ErrInvalidArgument)
		}
		if err != nil {
			if errors.Is(err, context.Canceled) {
				return "", err
			}
			return "", fmt.Errorf("%w: %w", aistudio.ErrInvalidArgument, err)
		}
		value, readErr := readOpenAIFileField(part)
		_ = part.Close()
		if readErr != nil {
			if errors.Is(readErr, context.Canceled) {
				return "", readErr
			}
			return "", fmt.Errorf("%w: %w", aistudio.ErrInvalidArgument, readErr)
		}
		if part.FormName() == "purpose" {
			value = strings.TrimSpace(value)
			if value == "" {
				return "", fmt.Errorf("%w: purpose is required", aistudio.ErrInvalidArgument)
			}
			return value, nil
		}
	}
}

func (s *server) handleOpenAIFileGet(w http.ResponseWriter, r *http.Request) {
	service, ok := s.service.(aistudio.FileService)
	if !ok {
		writeOpenAIError(w, http.StatusBadRequest, "invalid_request", "file metadata is unavailable")
		return
	}
	fileID := strings.TrimSpace(r.PathValue("file"))
	metadata, err := service.FileMetadata(r.Context(), fileID)
	if err != nil && !shouldWriteRequestError(r, err) {
		return
	}
	if errors.Is(err, aistudio.ErrResourceNotFound) {
		writeOpenAIError(w, http.StatusNotFound, "file_not_found", "file not found")
		return
	}
	if err != nil {
		writeOpenAIError(w, statusFromError(err), openAIErrorCode(err), err.Error())
		return
	}
	writeJSON(w, http.StatusOK, openAIFileResponse(metadata))
}

// handleOpenAIFileContent 返回上传文件内容
func (s *server) handleOpenAIFileContent(w http.ResponseWriter, r *http.Request) {
	service, ok := s.service.(aistudio.FileService)
	if !ok {
		writeOpenAIError(w, http.StatusBadRequest, "invalid_request", "file content is unavailable")
		return
	}
	fileID := strings.TrimSpace(r.PathValue("file"))
	metadata, err := service.FileMetadata(r.Context(), fileID)
	if err != nil && !shouldWriteRequestError(r, err) {
		return
	}
	if errors.Is(err, aistudio.ErrResourceNotFound) {
		writeOpenAIError(w, http.StatusNotFound, "file_not_found", "file not found")
		return
	}
	if err != nil {
		writeOpenAIError(w, statusFromError(err), openAIErrorCode(err), err.Error())
		return
	}
	media, err := service.DownloadFile(r.Context(), fileID)
	if err != nil && !shouldWriteRequestError(r, err) {
		return
	}
	if errors.Is(err, aistudio.ErrResourceNotFound) {
		writeOpenAIError(w, http.StatusNotFound, "file_not_found", "file not found")
		return
	}
	if err != nil {
		writeOpenAIError(w, statusFromError(err), openAIErrorCode(err), err.Error())
		return
	}
	mimeType := strings.TrimSpace(media.MIME)
	if mimeType == "" {
		mimeType = metadata.MIME
	}
	filename := strings.TrimSpace(media.Name)
	if filename == "" {
		filename = metadata.Name
	}
	size := media.Size
	if size < 0 {
		size = metadata.Size
	}
	w.Header().Set("Content-Type", mimeType)
	w.Header().Set("Content-Disposition", mime.FormatMediaType("attachment", map[string]string{"filename": filename}))
	if size >= 0 {
		w.Header().Set("Content-Length", strconv.FormatInt(size, 10))
	}
	w.WriteHeader(http.StatusOK)
	_, copyErr := io.Copy(w, media.Body)
	closeErr := media.Body.Close()
	if requestErr := errors.Join(copyErr, closeErr); requestErr != nil {
		SetAccessLogError(r.Context(), requestErr)
	}
}

// handleOpenAIFileDelete 删除上传文件
func (s *server) handleOpenAIFileDelete(w http.ResponseWriter, r *http.Request) {
	service, ok := s.service.(aistudio.FileService)
	if !ok {
		writeOpenAIError(w, http.StatusBadRequest, "invalid_request", "file deletion is unavailable")
		return
	}
	fileID := strings.TrimSpace(r.PathValue("file"))
	err := service.DeleteFile(r.Context(), fileID)
	if err != nil && !shouldWriteRequestError(r, err) {
		return
	}
	if errors.Is(err, aistudio.ErrResourceNotFound) {
		writeOpenAIError(w, http.StatusNotFound, "file_not_found", "file not found")
		return
	}
	if err != nil {
		writeOpenAIError(w, statusFromError(err), openAIErrorCode(err), err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"id": fileID, "object": "file", "deleted": true})
}

func multipartStreamMIME(file io.Reader, value string) (string, io.Reader, error) {
	probe := make([]byte, 512)
	count, readErr := io.ReadFull(file, probe)
	if readErr != nil && !errors.Is(readErr, io.EOF) && !errors.Is(readErr, io.ErrUnexpectedEOF) {
		return "", nil, readErr
	}
	if count == 0 {
		return "", nil, errors.New("file must not be empty")
	}
	stream := io.MultiReader(bytes.NewReader(probe[:count]), file)
	value = strings.TrimSpace(value)
	if value != "" {
		mediaType, _, err := mime.ParseMediaType(value)
		if err != nil {
			return "", nil, err
		}
		if mediaType != "application/octet-stream" {
			return mediaType, stream, nil
		}
	}
	return http.DetectContentType(probe[:count]), stream, nil
}

func multipartFileMIME(file io.ReadSeeker, value string) (string, error) {
	value = strings.TrimSpace(value)
	if value != "" {
		mediaType, _, err := mime.ParseMediaType(value)
		if err != nil {
			return "", err
		}
		if mediaType != "application/octet-stream" {
			return mediaType, nil
		}
	}
	probe := make([]byte, 512)
	count, err := file.Read(probe)
	if err != nil && !errors.Is(err, io.EOF) {
		return "", err
	}
	if _, err := file.Seek(0, io.SeekStart); err != nil {
		return "", err
	}
	return http.DetectContentType(probe[:count]), nil
}

func openAIFileResponse(metadata aistudio.FileMetadata) openAIFileObject {
	return openAIFileObject{
		ID: metadata.ID, Object: "file", Bytes: metadata.Size, CreatedAt: metadata.CreatedAt.Unix(),
		Filename: metadata.Name, Purpose: metadata.Purpose, Status: "processed",
	}
}

func writeOpenAIFileParseError(w http.ResponseWriter, err error) {
	var maxBytes *http.MaxBytesError
	if errors.As(err, &maxBytes) {
		writeOpenAIError(w, http.StatusRequestEntityTooLarge, "file_too_large", "file exceeds 512 MB")
		return
	}
	writeOpenAIError(w, http.StatusBadRequest, "invalid_request", err.Error())
}
