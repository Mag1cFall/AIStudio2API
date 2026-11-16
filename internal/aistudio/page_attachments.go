package aistudio

import (
	"context"
	"sync"
)

// pageUploader 是在服务上传附件前后发出官网页面伴随请求的账户页面
type pageUploader interface {
	// driveDenied 判断页面载入时申请 Drive access token 被拒，这样的页面插入附件时不再申请令牌与应用文件夹，附件内联
	driveDenied(ctx context.Context) bool
	// beforeUpload 像官网插入文件一样记录两次点击插入按钮，账户尚未确认首次上传素材提示时记录确认框并发送 UpdateUserPreferences，rerun 为真且本页面点过 Run 时页面按 Rerun 运行、不插入文件；返回页面载入时取得、仍在有效期内的 access token，没有时为空
	beforeUpload(ctx context.Context, rerun bool) string
	// inlined 在附件内联时像官网输入框显示附件一样经 count 为每个附件发出只含该文件内联数据的 CountTokens，rerun 为真且本页面点过 Run 时附件已在对话中、不发
	inlined(ctx context.Context, rerun bool, files []inlineFile, count func(context.Context, []byte) ([]byte, error))
	// uploaded 在服务上传完附件后像官网输入框显示附件一样经 drive.count 统计各附件的 token，并在页面发出带这些附件的 GenerateContent 后经 drive.read 像对话中的附件一样读取文件
	uploaded(ctx context.Context, files []FileRef, drive pageDrive)
}

// inlineFile 是内联发送的一个附件：name 为文件名，blob 为内联数据
type inlineFile struct {
	name string
	blob *Blob
}

// pageDrive 是页面附件伴随的请求：count 发送 CountTokens 正文并返回响应正文，read 读取 Drive 文件的元数据与缩略图
type pageDrive struct {
	count func(ctx context.Context, body []byte) ([]byte, error)
	read  func(ctx context.Context, fileID string)
}

// pageIssuedKey 是上下文中标记 MakerSuite RPC 由账户页面发出的键
type pageIssuedKey struct{}

// issuedByPage 返回其中的 MakerSuite RPC 像官网页面自己发出的附件伴随请求一样由账户页面发出的上下文
func issuedByPage(ctx context.Context) context.Context {
	return context.WithValue(ctx, pageIssuedKey{}, true)
}

// pageWorkers 是各账户的 WAA Worker，键为账户 ID
var pageWorkers sync.Map

// pageWorker 返回账户的 WAA Worker，没有时为 nil
func pageWorker(accountID string) *NativeWorker {
	worker, ok := pageWorkers.Load(accountID)
	if !ok {
		return nil
	}
	return worker.(*NativeWorker)
}

// pageLoginState 返回账户请求所用的登录状态：账户有 WAA Worker 时为页面当前的 Cookie，与页面发出的生成请求一致，没有时为磁盘状态
func pageLoginState(ctx context.Context, account *Account) (StorageState, error) {
	if worker := pageWorker(account.ID); worker != nil {
		return worker.BrowserStorageState(ctx)
	}
	return LoadStorageState(account.StoragePath)
}
