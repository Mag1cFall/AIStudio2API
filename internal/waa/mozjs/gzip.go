package mozjs

/*
#include <stddef.h>
#include <stdint.h>
size_t mz_gzip_bound(size_t len);
size_t mz_gzip(const uint8_t* input, size_t len, uint8_t* out, size_t cap);
*/
import "C"

import (
	"bytes"
	"sync"
	"unsafe"
)

// gzipBuffers 是 Gzip 复用的输出缓冲区
var gzipBuffers sync.Pool

// Gzip 返回 Windows 版 Firefox CompressionStream("gzip") 对 data 输出的字节：以 Firefox 所用的 zlib-rs 按其默认级别与调用顺序压缩，gzip 头的操作系统字节取 Windows 版写入的 10
func Gzip(data []byte) []byte {
	bound := int(C.mz_gzip_bound(C.size_t(len(data))))
	buffer, _ := gzipBuffers.Get().(*[]byte)
	if buffer == nil || cap(*buffer) < bound {
		created := make([]byte, bound)
		buffer = &created
	}
	defer gzipBuffers.Put(buffer)
	out := (*buffer)[:bound]
	var input *C.uint8_t
	if len(data) > 0 {
		input = (*C.uint8_t)(unsafe.Pointer(&data[0]))
	}
	written := C.mz_gzip(input, C.size_t(len(data)), (*C.uint8_t)(unsafe.Pointer(&out[0])), C.size_t(len(out)))
	out[9] = 10
	return bytes.Clone(out[:written])
}
