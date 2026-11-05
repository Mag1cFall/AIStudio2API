// Package libwebp 经 cgo 调用 libwebp 编码有损 WebP
package libwebp

/*
#cgo LDFLAGS: -lwebp -lsharpyuv
#cgo linux LDFLAGS: -lm
#include <webp/encode.h>

// encode 以默认预设与 quality 编码 RGBX 或 RGBA 像素，成功时 out 为 libwebp 分配的输出，失败时返回 0 并在 code 中给出 WebPEncodingError
static size_t encode(const uint8_t *pix, int width, int height, int stride, int alpha, float quality, uint8_t **out, int *code) {
	WebPConfig config;
	WebPPicture picture;
	WebPMemoryWriter writer;
	if (!WebPConfigPreset(&config, WEBP_PRESET_DEFAULT, quality) || !WebPPictureInit(&picture)) {
		*code = VP8_ENC_ERROR_INVALID_CONFIGURATION;
		return 0;
	}
	picture.width = width;
	picture.height = height;
	if (!(alpha ? WebPPictureImportRGBA(&picture, pix, stride) : WebPPictureImportRGBX(&picture, pix, stride))) {
		*code = picture.error_code;
		WebPPictureFree(&picture);
		return 0;
	}
	WebPMemoryWriterInit(&writer);
	picture.writer = WebPMemoryWrite;
	picture.custom_ptr = &writer;
	int ok = WebPEncode(&config, &picture);
	*code = picture.error_code;
	WebPPictureFree(&picture);
	if (!ok) {
		WebPMemoryWriterClear(&writer);
		return 0;
	}
	*out = writer.mem;
	return writer.size;
}
*/
import "C"

import (
	"fmt"
	"image"
	"image/draw"
	"io"
	"unsafe"

	"github.com/Mag1cFall/AIStudio2API/internal/imagecodec"
)

// Encode 以 libwebp 的默认预设与 quality（0 到 100）把 m 编码为有损 WebP 写入 w，不透明图片不带 alpha 平面
func Encode(w io.Writer, m image.Image, quality int) error {
	pix, stride, opaque := nrgbaPixels(m)
	bounds := m.Bounds()
	alpha := C.int(1)
	if opaque {
		alpha = 0
	}
	var out *C.uint8_t
	var code C.int
	size := C.encode((*C.uint8_t)(unsafe.Pointer(unsafe.SliceData(pix))), C.int(bounds.Dx()), C.int(bounds.Dy()), C.int(stride), alpha, C.float(quality), &out, &code)
	if size == 0 {
		return fmt.Errorf("libwebp: encoding error %d", int(code))
	}
	defer C.WebPFree(unsafe.Pointer(out))
	_, err := w.Write(unsafe.Slice((*byte)(unsafe.Pointer(out)), int(size)))
	return err
}

// nrgbaPixels 返回 m 的非预乘 RGBA 像素、行跨度与是否不透明，不透明的 RGBA 直接使用其像素，YCbCr 经 imagecodec.YCbCrToRGBA 转换
func nrgbaPixels(m image.Image) ([]byte, int, bool) {
	switch m := m.(type) {
	case *image.NRGBA:
		return m.Pix, m.Stride, m.Opaque()
	case *image.RGBA:
		if m.Opaque() {
			return m.Pix, m.Stride, true
		}
	case *image.YCbCr:
		converted := imagecodec.YCbCrToRGBA(m)
		return converted.Pix, converted.Stride, true
	}
	converted := image.NewNRGBA(m.Bounds())
	draw.Draw(converted, converted.Bounds(), m, m.Bounds().Min, draw.Src)
	return converted.Pix, converted.Stride, converted.Opaque()
}
