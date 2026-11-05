package imagecodec

import (
	"bytes"
	"fmt"
	"image"
	"image/draw"
	"math/rand/v2"
	"testing"
)

func TestYCbCrToRGBAMatchesDraw(t *testing.T) {
	for _, size := range []image.Point{{1, 1}, {7, 3}, {8, 8}, {9, 5}, {33, 17}, {130, 67}, {257, 31}} {
		random := rand.New(rand.NewPCG(uint64(size.X), uint64(size.Y)))
		for _, ratio := range []image.YCbCrSubsampleRatio{
			image.YCbCrSubsampleRatio444, image.YCbCrSubsampleRatio422, image.YCbCrSubsampleRatio420,
			image.YCbCrSubsampleRatio440, image.YCbCrSubsampleRatio411, image.YCbCrSubsampleRatio410,
		} {
			m := image.NewYCbCr(image.Rectangle{Max: size}, ratio)
			for i := range m.Y {
				m.Y[i] = uint8(random.IntN(256))
			}
			for i := range m.Cb {
				m.Cb[i], m.Cr[i] = uint8(random.IntN(256)), uint8(random.IntN(256))
			}
			t.Run(fmt.Sprintf("%v-%v", ratio, size), func(t *testing.T) {
				want := image.NewRGBA(m.Bounds())
				draw.Draw(want, want.Bounds(), m, m.Bounds().Min, draw.Src)
				got := YCbCrToRGBA(m)
				if got.Rect != want.Rect || !bytes.Equal(got.Pix, want.Pix) {
					for i := range got.Pix {
						if got.Pix[i] != want.Pix[i] {
							t.Fatalf("第 %d 个像素的分量 %d 为 %d，期望 %d", i/4, i%4, got.Pix[i], want.Pix[i])
						}
					}
					t.Fatalf("矩形为 %v，期望 %v", got.Rect, want.Rect)
				}
			})
		}
	}
}
