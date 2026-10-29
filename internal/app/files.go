package app

import (
	"context"

	"github.com/Mag1cFall/AIStudio2API/internal/aistudio"
	"github.com/Mag1cFall/AIStudio2API/internal/api"
)

// UploadFile 上传文件并记录实际账户
func (service *trackedService) UploadFile(ctx context.Context, request aistudio.UploadRequest) (aistudio.FileRef, error) {
	requestCtx, cancel, err := service.observedDataRequestContext(ctx, "")
	if err != nil {
		return aistudio.FileRef{}, err
	}
	defer cancel()
	file, requestErr := service.service.(aistudio.FileService).UploadFile(requestCtx, request)
	api.SetAccessLogError(requestCtx, requestErr)
	return file, requestErr
}

// FileMetadata 返回上传文件的持久元数据
func (service *trackedService) FileMetadata(ctx context.Context, fileID string) (aistudio.FileMetadata, error) {
	requestCtx, cancel, err := service.observedDataRequestContext(ctx, "")
	if err != nil {
		return aistudio.FileMetadata{}, err
	}
	defer cancel()
	metadata, requestErr := service.service.(aistudio.FileService).FileMetadata(requestCtx, fileID)
	api.SetAccessLogError(requestCtx, requestErr)
	return metadata, requestErr
}

// ListFiles 列出上传文件
func (service *trackedService) ListFiles(ctx context.Context) ([]aistudio.FileMetadata, error) {
	requestCtx, cancel, err := service.observedDataRequestContext(ctx, "")
	if err != nil {
		return nil, err
	}
	defer cancel()
	list, requestErr := service.service.(aistudio.FileService).ListFiles(requestCtx)
	api.SetAccessLogError(requestCtx, requestErr)
	return list, requestErr
}

// DeleteFile 删除上传文件并记录结果
func (service *trackedService) DeleteFile(ctx context.Context, fileID string) error {
	requestCtx, cancel, err := service.observedDataRequestContext(ctx, "")
	if err != nil {
		return err
	}
	defer cancel()
	requestErr := service.service.(aistudio.FileService).DeleteFile(requestCtx, fileID)
	api.SetAccessLogError(requestCtx, requestErr)
	return requestErr
}
