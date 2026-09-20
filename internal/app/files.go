package app

import (
	"context"
	"fmt"

	"github.com/Mag1cFall/AIStudio2API/internal/aistudio"
	"github.com/Mag1cFall/AIStudio2API/internal/api"
)

// UploadFile uploads a file and tracks the actual account used.
func (service *trackedService) UploadFile(ctx context.Context, request aistudio.UploadRequest) (aistudio.FileRef, error) {
	requestCtx, cancel, err := service.observedDataRequestContext(ctx, "")
	if err != nil {
		return aistudio.FileRef{}, err
	}
	defer cancel()

	files, ok := service.service.(aistudio.FileService)
	if !ok {
		return aistudio.FileRef{}, fmt.Errorf("file service is unavailable")
	}

	file, requestErr := files.UploadFile(requestCtx, request)
	api.SetAccessLogError(requestCtx, requestErr)

	return file, requestErr
}

// FileMetadata returns persistent metadata for an uploaded file.
func (service *trackedService) FileMetadata(ctx context.Context, fileID string) (aistudio.FileMetadata, error) {
	requestCtx, cancel, err := service.observedDataRequestContext(ctx, "")
	if err != nil {
		return aistudio.FileMetadata{}, err
	}
	defer cancel()

	files, ok := service.service.(aistudio.FileService)
	if !ok {
		return aistudio.FileMetadata{}, fmt.Errorf("file service is unavailable")
	}

	metadata, requestErr := files.FileMetadata(requestCtx, fileID)
	api.SetAccessLogError(requestCtx, requestErr)

	return metadata, requestErr
}

// DeleteFile deletes an uploaded file and logs the outcome.
func (service *trackedService) DeleteFile(ctx context.Context, fileID string) error {
	requestCtx, cancel, err := service.observedDataRequestContext(ctx, "")
	if err != nil {
		return err
	}
	defer cancel()

	files, ok := service.service.(aistudio.FileService)
	if !ok {
		return fmt.Errorf("file service is unavailable")
	}

	requestErr := files.DeleteFile(requestCtx, fileID)
	api.SetAccessLogError(requestCtx, requestErr)

	return requestErr
}
