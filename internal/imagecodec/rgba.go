package imagecodec

import (
	"image"
	"image/color"
	"image/draw"
)

// YCbCrToRGBA 把 m 转为不透明的 *image.RGBA，与 draw.Draw 以 draw.Src 画到 *image.RGBA 的结果逐字节相同；没有 SIMD 内核、子采样比不受支持或起点不在原点时由 draw.Draw 转换
func YCbCrToRGBA(m *image.YCbCr) *image.RGBA {
	bounds := m.Bounds()
	dst := image.NewRGBA(bounds)
	shift, vertical, ok := chromaLayout(m.SubsampleRatio)
	if !simd || !ok || m.Rect.Min != (image.Point{}) {
		draw.Draw(dst, bounds, m, bounds.Min, draw.Src)
		return dst
	}
	width := bounds.Dx()
	n := width &^ 7
	for y := range bounds.Dy() {
		row := dst.Pix[y*dst.Stride : y*dst.Stride+4*width]
		ys := m.Y[y*m.YStride:]
		chroma := (y >> vertical) * m.CStride
		if n > 0 {
			rgbaFromYCbCr(&row[0], &ys[0], &m.Cb[chroma], &m.Cr[chroma], n, shift)
		}
		for x := n; x < width; x++ {
			c := chroma + x>>shift
			row[4*x], row[4*x+1], row[4*x+2] = color.YCbCrToRGB(ys[x], m.Cb[c], m.Cr[c])
			row[4*x+3] = 0xff
		}
	}
	return dst
}

// chromaLayout 返回子采样比的色度水平与垂直位移
func chromaLayout(ratio image.YCbCrSubsampleRatio) (shift, vertical int, ok bool) {
	switch ratio {
	case image.YCbCrSubsampleRatio444:
		return 0, 0, true
	case image.YCbCrSubsampleRatio422:
		return 1, 0, true
	case image.YCbCrSubsampleRatio420:
		return 1, 1, true
	case image.YCbCrSubsampleRatio440:
		return 0, 1, true
	case image.YCbCrSubsampleRatio411:
		return 2, 0, true
	case image.YCbCrSubsampleRatio410:
		return 2, 1, true
	}
	return 0, 0, false
}
