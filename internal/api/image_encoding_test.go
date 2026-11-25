package api

import (
	"bytes"
	"image"
	"image/png"
	"testing"

	"github.com/Mag1cFall/AIStudio2API/internal/aistudio"
)

// TestFitImageWebPOutputCompression 核对 OpenAI 的 output_compression 作用于 webp 输出：输出为有损 VP8 WebP，压缩级别越低文件越小
func TestFitImageWebPOutputCompression(t *testing.T) {
	picture := image.NewRGBA(image.Rect(0, 0, 192, 128))
	for y := range 128 {
		for x := range 192 {
			copy(picture.Pix[picture.PixOffset(x, y):], []byte{byte(x * 2), byte(y * 2), byte(x * y), 0xff})
		}
	}
	var source bytes.Buffer
	if err := png.Encode(&source, picture); err != nil {
		t.Fatal(err)
	}
	media := aistudio.Blob{MIME: "image/png", Data: source.Bytes()}
	var sizes []int
	for _, compression := range []int{100, 50} {
		converted, size, err := fitImage(media, image.Point{}, "image/webp", compression)
		if err != nil {
			t.Fatal(err)
		}
		if converted.MIME != "image/webp" || size != picture.Rect.Size() || len(converted.Data) < 16 || string(converted.Data[:4]) != "RIFF" || string(converted.Data[8:16]) != "WEBPVP8 " {
			t.Fatalf("output_compression=%d: %s %v %q", compression, converted.MIME, size, converted.Data[:min(len(converted.Data), 16)])
		}
		sizes = append(sizes, len(converted.Data))
	}
	if sizes[1] >= sizes[0] {
		t.Fatalf("output_compression=50 输出 %d 字节，不小于 output_compression=100 的 %d 字节", sizes[1], sizes[0])
	}
}
