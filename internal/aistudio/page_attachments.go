package aistudio

import (
	"context"
	"sync"
)

// pageUploader 是在服务上传附件前后发出官网页面伴随请求的账户页面
type pageUploader interface {
	// beforeUpload 在账户尚未确认首次上传素材提示时像官网一样发送 UpdateUserPreferences，返回页面载入时取得、仍在有效期内的 access token，没有时为空
	beforeUpload(ctx context.Context) string
	// uploaded 在服务上传完附件后像官网输入框显示附件一样经 drive.count 统计各附件的 token，并在页面发出带这些附件的 GenerateContent 后经 drive.read 像对话中的附件一样读取文件
	uploaded(ctx context.Context, files []FileRef, drive pageDrive)
}

// pageDrive 是页面附件伴随的请求：count 发送 CountTokens 正文并返回响应正文，read 读取 Drive 文件的元数据与缩略图
type pageDrive struct {
	count func(ctx context.Context, body []byte) ([]byte, error)
	read  func(ctx context.Context, fileID string)
}

// pageWorkers 是各账户的 WAA Worker，键为账户 ID
var pageWorkers sync.Map

// pageOf 返回账户 WAA Worker 当前的页面，没有 Worker 时为 nil
func pageOf(accountID string) workerRuntime {
	worker, ok := pageWorkers.Load(accountID)
	if !ok {
		return nil
	}
	return worker.(*NativeWorker).current()
}
