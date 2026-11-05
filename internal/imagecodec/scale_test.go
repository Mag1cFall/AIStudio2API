package imagecodec

import (
	"bytes"
	"fmt"
	"image"
	"image/color"
	"math/rand/v2"
	"testing"

	"golang.org/x/image/draw"
)

// scaleSources 返回 Scale 快速路径支持的各类源图，像素为确定性的噪声与渐变
func scaleSources(size image.Point) map[string]image.Image {
	random := rand.New(rand.NewPCG(uint64(size.X), uint64(size.Y)))
	rect := image.Rectangle{Max: size}
	rgba, nrgba, gray := image.NewRGBA(rect), image.NewNRGBA(rect), image.NewGray(rect)
	for y := range size.Y {
		for x := range size.X {
			c := testColor(random, x, y)
			rgba.Set(x, y, c)
			c.A = uint8(random.IntN(256))
			if (x+y)%7 == 0 {
				c.A = 0xff
			}
			nrgba.SetNRGBA(x, y, c)
			gray.SetGray(x, y, color.Gray{Y: c.R})
		}
	}
	sources := map[string]image.Image{"rgba": rgba, "nrgba": nrgba, "gray": gray}
	for _, ratio := range []image.YCbCrSubsampleRatio{
		image.YCbCrSubsampleRatio444, image.YCbCrSubsampleRatio422, image.YCbCrSubsampleRatio420,
		image.YCbCrSubsampleRatio440, image.YCbCrSubsampleRatio411, image.YCbCrSubsampleRatio410,
	} {
		ycc := image.NewYCbCr(rect, ratio)
		for i := range ycc.Y {
			ycc.Y[i] = testColor(random, i%ycc.YStride, i/ycc.YStride).B
		}
		for i := range ycc.Cb {
			ycc.Cb[i], ycc.Cr[i] = uint8(random.IntN(256)), uint8(random.IntN(256))
		}
		sources["ycbcr-"+ratio.String()] = ycc
	}
	return sources
}

func TestScaleMatchesCatmullRom(t *testing.T) {
	cases := []struct {
		source image.Point
		crop   image.Rectangle
		size   image.Point
	}{
		{image.Pt(256, 256), image.Rect(0, 0, 256, 256), image.Pt(64, 64)},
		{image.Pt(203, 117), image.Rect(0, 0, 203, 117), image.Pt(55, 31)},
		{image.Pt(300, 200), image.Rect(37, 11, 289, 197), image.Pt(101, 67)},
		{image.Pt(97, 61), image.Rect(3, 5, 94, 58), image.Pt(83, 50)},
		{image.Pt(64, 48), image.Rect(0, 0, 64, 48), image.Pt(64, 48)},
		{image.Pt(40, 30), image.Rect(1, 2, 39, 29), image.Pt(77, 55)},
		{image.Pt(512, 9), image.Rect(5, 0, 509, 9), image.Pt(126, 3)},
		{image.Pt(31, 400), image.Rect(0, 13, 31, 391), image.Pt(7, 95)},
		{image.Pt(130, 130), image.Rect(2, 2, 130, 130), image.Pt(1, 1)},
	}
	for _, c := range cases {
		for name, src := range scaleSources(c.source) {
			t.Run(fmt.Sprintf("%s-%v-%v-%v", name, c.source, c.crop, c.size), func(t *testing.T) {
				want := image.NewRGBA(image.Rectangle{Max: c.size})
				draw.CatmullRom.Scale(want, want.Bounds(), src, c.crop, draw.Src, nil)
				got := Scale(src, c.crop, c.size)
				if !bytes.Equal(got.Pix, want.Pix) {
					for i := range got.Pix {
						if got.Pix[i] != want.Pix[i] {
							t.Fatalf("第 %d 个像素的分量 %d 为 %d，期望 %d", i/4, i%4, got.Pix[i], want.Pix[i])
						}
					}
				}
			})
		}
	}
}

func TestConvertNRGBAAllInputs(t *testing.T) {
	if !simd {
		t.Skip("本机没有 SIMD 内核")
	}
	pix := make([]byte, 4*256*256)
	for i := range 256 * 256 {
		r, a := i>>8, i&0xff
		pix[4*i], pix[4*i+1], pix[4*i+2], pix[4*i+3] = uint8(r), uint8(255-r), uint8(r/2), uint8(a)
	}
	dst := make([]float64, 4*256*256)
	convertNRGBA(&dst[0], &pix[0], 256*256)
	for i := range 256 * 256 {
		pau := uint32(pix[4*i+3]) * 0x101
		want := [4]float64{
			float64(uint32(pix[4*i]) * pau / 0xff), float64(uint32(pix[4*i+1]) * pau / 0xff),
			float64(uint32(pix[4*i+2]) * pau / 0xff), float64(pau),
		}
		if got := [4]float64(dst[4*i : 4*i+4]); got != want {
			t.Fatalf("像素 %v：%v，期望 %v", pix[4*i:4*i+4], got, want)
		}
	}
}

func TestConvertYCbCrAllInputs(t *testing.T) {
	if !simd {
		t.Skip("本机没有 SIMD 内核")
	}
	ys := make([]byte, 256)
	for i := range ys {
		ys[i] = uint8(i)
	}
	cbs, crs := make([]byte, 256), make([]byte, 256)
	dst := make([]float64, 4*256)
	for cb := range 256 {
		for cr := range 256 {
			for i := range cbs {
				cbs[i], crs[i] = uint8(cb), uint8(cr)
			}
			convertYCbCr(&dst[0], &ys[0], &cbs[0], &crs[0], 256, 0)
			for y := range 256 {
				r, g, b, a := color.YCbCr{Y: uint8(y), Cb: uint8(cb), Cr: uint8(cr)}.RGBA()
				if got, want := [4]float64(dst[4*y:4*y+4]), [4]float64{float64(r), float64(g), float64(b), float64(a)}; got != want {
					t.Fatalf("Y=%d Cb=%d Cr=%d：%v，期望 %v", y, cb, cr, got, want)
				}
			}
		}
	}
}
