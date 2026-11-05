package imagecodec

import (
	"image"
	"math"

	"golang.org/x/image/draw"
)

// source 与 contrib 的内存布局与 x/image/draw 相同，汇编按此布局读取
type source struct {
	i, j               int32
	invTotalWeight     float64
	invTotalWeightFFFF float64
}

type contrib struct {
	coord  int32
	weight float64
}

// distrib 是 x/image/draw 的 CatmullRom 权重分布
type distrib struct {
	sources  []source
	contribs []contrib
}

// rowGroup 是水平一遍同时处理的源行数
const rowGroup = 4

// pixelConverter 把源图第 y 行转为每像素 [4]float64 写入 dst，取值与 x/image/draw 对该类型源图的取色相同
type pixelConverter func(dst []float64, y int)

// Scale 以 CatmullRom 把 src 的 sr 区域缩放为 size 大小的 RGBA 图片，结果与 draw.CatmullRom.Scale(dst, dst.Bounds(), src, sr, draw.Src, nil) 逐像素相同；没有 SIMD 内核时与其余源图类型由该函数缩放
func Scale(src image.Image, sr image.Rectangle, size image.Point) *image.RGBA {
	dst := image.NewRGBA(image.Rectangle{Max: size})
	if !simd || size.X <= 0 || size.Y <= 0 || sr.Empty() || !sr.In(src.Bounds()) {
		draw.CatmullRom.Scale(dst, dst.Bounds(), src, sr, draw.Src, nil)
		return dst
	}
	dw, dh, sw, sh := size.X, size.Y, sr.Dx(), sr.Dy()
	convert, lead, alphaOne, ok := newPixelConverter(src, sr.Min.X, sw)
	if !ok {
		draw.CatmullRom.Scale(dst, dst.Bounds(), src, sr, draw.Src, nil)
		return dst
	}
	horizontal := newDistrib(int32(dw), int32(sw))
	vertical := newDistrib(int32(dh), int32(sh))
	// 计算第 dy 行所需水平结果时会提前算出同组其余行，环形缓冲至少容纳该行与之后各行最低所需行之间的全部行再加 rowGroup-1 行
	need, lowest := 0, math.MaxInt
	for dy := len(vertical.sources) - 1; dy >= 0; dy-- {
		s := vertical.sources[dy]
		if s.j == s.i {
			continue
		}
		lowest = min(lowest, int(vertical.contribs[s.i].coord))
		need = max(need, int(vertical.contribs[s.j-1].coord)+1-lowest)
	}
	ring := (need + 2*(rowGroup-1)) / rowGroup * rowGroup
	tmp := make([]float64, ring*dw*4)
	convFloats := (lead + sw) * 4
	conv := make([]float64, rowGroup*convFloats)
	alpha := 0
	if alphaOne {
		alpha = 1
	}
	rows := make([]*float64, 0, need)
	weights := make([]float64, 0, need)
	next := 0
	for dy, s := range vertical.sources {
		taps := vertical.contribs[s.i:s.j]
		if len(taps) == 0 {
			continue
		}
		for last := int(taps[len(taps)-1].coord); next <= last; next += rowGroup {
			for r := range rowGroup {
				if y := next + r; y < sh {
					convert(conv[r*convFloats:(r+1)*convFloats], sr.Min.Y+y)
				}
			}
			scaleRows(&tmp[next%ring*dw*4], dw*32, &conv[lead*4], convFloats*8, &horizontal.sources[0], dw, &horizontal.contribs[0], alpha)
		}
		rows, weights = rows[:0], weights[:0]
		for _, c := range taps {
			rows = append(rows, &tmp[int(c.coord)%ring*dw*4])
			weights = append(weights, c.weight)
		}
		scaleColumn(&dst.Pix[dy*dst.Stride], &rows[0], &weights[0], len(taps), dw, s.invTotalWeight)
	}
	return dst
}

// newPixelConverter 返回源图从 x0 起 n 列的取色函数，转换结果从 dst 的第 lead 个像素起对应 x0，alphaOne 表示 x/image/draw 对该类型把水平结果的 alpha 记为 1
func newPixelConverter(src image.Image, x0, n int) (convert pixelConverter, lead int, alphaOne, ok bool) {
	switch src := src.(type) {
	case *image.RGBA:
		return func(dst []float64, y int) {
			convertRGBA(&dst[0], &src.Pix[src.PixOffset(x0, y)], n)
		}, 0, false, true
	case *image.NRGBA:
		return func(dst []float64, y int) {
			convertNRGBA(&dst[0], &src.Pix[src.PixOffset(x0, y)], n)
		}, 0, false, true
	case *image.Gray:
		return func(dst []float64, y int) {
			convertGray(&dst[0], &src.Pix[src.PixOffset(x0, y)], n)
		}, 0, true, true
	case *image.YCbCr:
		shift, vertical, ok := chromaLayout(src.SubsampleRatio)
		if !ok || src.Rect.Min != (image.Point{}) {
			return nil, 0, false, false
		}
		// 从 4 的倍数列开始转换，使色度下标与像素对齐
		start := x0 &^ 3
		lead = x0 - start
		total := lead + n
		count := total &^ 7
		// x/image/draw 只对 4:4:4、4:2:2、4:2:0、4:4:0 有专用路径并把 alpha 记为 1，4:1:1 与 4:1:0 经 At 取色，alpha 为 0xffff 的加权结果
		return func(dst []float64, y int) {
			if count > 0 {
				chroma := (y>>vertical)*src.CStride + start>>shift
				convertYCbCr(&dst[0], &src.Y[y*src.YStride+start], &src.Cb[chroma], &src.Cr[chroma], count, shift)
			}
			for x := count; x < total; x++ {
				r, g, b, a := src.YCbCrAt(start+x, y).RGBA()
				dst[4*x], dst[4*x+1], dst[4*x+2], dst[4*x+3] = float64(r), float64(g), float64(b), float64(a)
			}
		}, lead, shift < 2, true
	}
	return nil, 0, false, false
}

// newDistrib 是 x/image/draw 的 newDistrib 对 CatmullRom 的副本，浮点运算与其逐步相同
func newDistrib(dw, sw int32) distrib {
	scale := float64(sw) / float64(dw)
	halfWidth, kernelArgScale := 2.0, 1.0
	if scale > 1 {
		halfWidth *= scale
		kernelArgScale = 1 / scale
	}
	n, sources := int32(0), make([]source, dw)
	for x := range sources {
		center := float64((float64(x)+0.5)*scale) - 0.5
		i := int32(math.Floor(center - halfWidth))
		if i < 0 {
			i = 0
		}
		j := int32(math.Ceil(center + halfWidth))
		if j > sw {
			j = sw
			if j < i {
				j = i
			}
		}
		sources[x] = source{i: i, j: j, invTotalWeight: center}
		n += j - i
	}
	contribs := make([]contrib, 0, n)
	for k, b := range sources {
		totalWeight := 0.0
		l := int32(len(contribs))
		for coord := b.i; coord < b.j; coord++ {
			t := (b.invTotalWeight - float64(coord)) * kernelArgScale
			if t < 0 {
				t = -t
			}
			if t >= 2 {
				continue
			}
			weight := catmullRom(t)
			if weight == 0 {
				continue
			}
			totalWeight += weight
			contribs = append(contribs, contrib{coord, weight})
		}
		totalWeight = 1 / totalWeight
		sources[k] = source{
			i:                  l,
			j:                  int32(len(contribs)),
			invTotalWeight:     totalWeight,
			invTotalWeightFFFF: totalWeight / 0xffff,
		}
	}
	return distrib{sources, contribs}
}

// catmullRom 是 draw.CatmullRom.At 的副本
func catmullRom(t float64) float64 {
	if t < 1 {
		return float64((float64(1.5*t)-2.5)*t*t) + 1
	}
	return float64((float64(float64(float64(-0.5*t)+2.5)*t)-4)*t) + 2
}
