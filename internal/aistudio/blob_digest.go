package aistudio

import (
	"crypto/sha256"
	"runtime"
	"sync"
	"weak"
)

// blobDigests 按附件的弱指针保存客户端附件内容的 SHA-256，尚未计算时为 nil，附件被回收后条目随之删除
var blobDigests = struct {
	sync.Mutex
	sums map[weak.Pointer[Blob]]*[sha256.Size]byte
}{sums: map[weak.Pointer[Blob]]*[sha256.Size]byte{}}

// NewBlob 返回请求中由客户端提交、之后不再改写的附件，SHA256 第一次计算的摘要随附件保存，账户选择、轮次摘要与上传共用
func NewBlob(mime string, data []byte) *Blob {
	blob := &Blob{MIME: mime, Data: data}
	key := weak.Make(blob)
	blobDigests.Lock()
	blobDigests.sums[key] = nil
	blobDigests.Unlock()
	runtime.AddCleanup(blob, func(key weak.Pointer[Blob]) {
		blobDigests.Lock()
		delete(blobDigests.sums, key)
		blobDigests.Unlock()
	}, key)
	return blob
}

// SHA256 返回附件内容的 SHA-256：NewBlob 创建的附件只在第一次调用时计算，其余附件每次现算
func (blob *Blob) SHA256() [sha256.Size]byte {
	key := weak.Make(blob)
	blobDigests.Lock()
	sum, saved := blobDigests.sums[key]
	blobDigests.Unlock()
	if sum != nil {
		return *sum
	}
	computed := sha256.Sum256(blob.Data)
	if saved {
		blobDigests.Lock()
		blobDigests.sums[key] = &computed
		blobDigests.Unlock()
	}
	return computed
}
