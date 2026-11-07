package api

import (
	"encoding/json"
	"net/http"
	"strings"

	"github.com/Mag1cFall/AIStudio2API/internal/aistudio"
)

// videoTokensRequest 是试用页统计视频附件 token 数的请求，file 为 Files API 文件名或 URI，videoMetadata 为视频设置
type videoTokensRequest struct {
	Model         string `json:"model"`
	File          string `json:"file"`
	VideoMetadata *struct {
		StartOffset string   `json:"startOffset"`
		EndOffset   string   `json:"endOffset"`
		FPS         *float64 `json:"fps"`
	} `json:"videoMetadata"`
}

// handleVideoTokens 与官网视频附件一样以 StreamExtractVideoFrames 统计已上传视频按视频设置抽帧后的 token 数
func (s *server) handleVideoTokens(w http.ResponseWriter, r *http.Request) {
	files, ok := s.service.(aistudio.FileService)
	frames, framesOK := s.service.(aistudio.VideoFrameService)
	if !ok || !framesOK {
		writeAdminError(w, http.StatusBadRequest, "failed_precondition", "video token count is unavailable")
		return
	}
	var request videoTokensRequest
	if err := json.NewDecoder(r.Body).Decode(&request); err != nil || strings.TrimSpace(request.Model) == "" || strings.TrimSpace(request.File) == "" {
		writeAdminError(w, http.StatusBadRequest, "invalid_argument", "model and file are required")
		return
	}
	name := request.File
	if _, id, found := strings.Cut(name, "/files/"); found {
		name = "files/" + id
	}
	driveID, found, err := geminiFileDriveID(r.Context(), files, name)
	if err != nil {
		writeAdminUpstreamError(w, err)
		return
	}
	if !found {
		writeAdminError(w, http.StatusNotFound, "not_found", "file "+request.File+" was not found")
		return
	}
	frameRequest := aistudio.VideoFramesRequest{Model: request.Model, FileID: driveID}
	if video := request.VideoMetadata; video != nil {
		frameRequest.VideoMetadata = &aistudio.VideoMetadata{StartOffset: video.StartOffset, EndOffset: video.EndOffset, FPS: video.FPS}
	}
	tokens, err := frames.VideoFrameTokens(r.Context(), frameRequest)
	if err != nil {
		writeAdminUpstreamError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"totalTokens": tokens})
}
