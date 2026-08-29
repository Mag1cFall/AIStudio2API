package app

import (
	"context"
	"fmt"

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
	files, ok := service.service.(aistudio.FileService)
	if !ok {
		return aistudio.FileRef{}, fmt.Errorf("file service 不可用")
	}
	file, requestErr := files.UploadFile(requestCtx, request)
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
	files, ok := service.service.(aistudio.FileService)
	if !ok {
		return aistudio.FileMetadata{}, fmt.Errorf("file service 不可用")
	}
	metadata, requestErr := files.FileMetadata(requestCtx, fileID)
	api.SetAccessLogError(requestCtx, requestErr)
	return metadata, requestErr
}

// DeleteFile 删除上传文件并记录结果
func (service *trackedService) DeleteFile(ctx context.Context, fileID string) error {
	requestCtx, cancel, err := service.observedDataRequestContext(ctx, "")
	if err != nil {
		return err
	}
	defer cancel()
	files, ok := service.service.(aistudio.FileService)
	if !ok {
		return fmt.Errorf("file service 不可用")
	}
	requestErr := files.DeleteFile(requestCtx, fileID)
	api.SetAccessLogError(requestCtx, requestErr)
	return requestErr
}
