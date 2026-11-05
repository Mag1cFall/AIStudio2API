package api

import (
	"bytes"
	"image"
	"image/draw"
	"image/jpeg"
	"image/png"
	"testing"

	"github.com/Mag1cFall/AIStudio2API/internal/aistudio"
)

// TestFitImageJPEGToPNGIsRGB8 核对 JPEG 源不缩放输出 PNG 时为 8 位 RGB，像素与 image/jpeg 解码后经 draw.Draw 以 draw.Src 转为 RGBA 的结果相同
func TestFitImageJPEGToPNGIsRGB8(t *testing.T) {
	picture := image.NewRGBA(image.Rect(0, 0, 97, 61))
	for y := range 61 {
		for x := range 97 {
			copy(picture.Pix[picture.PixOffset(x, y):], []byte{byte(x * 5), byte(y * 3), byte(x*y + 7), 0xff})
		}
	}
	var source bytes.Buffer
	if err := jpeg.Encode(&source, picture, &jpeg.Options{Quality: 90}); err != nil {
		t.Fatal(err)
	}
	decoded, err := jpeg.Decode(bytes.NewReader(source.Bytes()))
	if err != nil {
		t.Fatal(err)
	}
	want := image.NewRGBA(decoded.Bounds())
	draw.Draw(want, want.Bounds(), decoded, decoded.Bounds().Min, draw.Src)
	converted, size, err := fitImage(aistudio.Blob{MIME: "image/jpeg", Data: source.Bytes()}, image.Point{}, "image/png", 100)
	if err != nil {
		t.Fatal(err)
	}
	if converted.MIME != "image/png" || size != want.Rect.Size() || len(converted.Data) < 26 || converted.Data[24] != 8 || converted.Data[25] != 2 {
		t.Fatalf("输出 %s %v，IHDR 位深与颜色类型 %v，期望 8 位 RGB", converted.MIME, size, converted.Data[24:min(len(converted.Data), 26)])
	}
	got, err := png.Decode(bytes.NewReader(converted.Data))
	if err != nil {
		t.Fatal(err)
	}
	if rgba, ok := got.(*image.RGBA); !ok || !bytes.Equal(rgba.Pix, want.Pix) {
		t.Fatalf("PNG 解出 %T，像素与 draw.Draw 转换的 RGBA 不同", got)
	}
}

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
