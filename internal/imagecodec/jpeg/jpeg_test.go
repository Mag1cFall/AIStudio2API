package jpeg

import (
	"bytes"
	"fmt"
	"image"
	"image/color"
	stdjpeg "image/jpeg"
	"maps"
	"math/rand/v2"
	"slices"
	"testing"
)

// referenceStore 按 reconstructBlock 的 Go 实现反量化、IDCT、加 128 截取，得到 8x8 输出字节
func referenceStore(coef *block, quant *block) [64]byte {
	b := *coef
	for zig := range blockSize {
		b[unzig[zig]] *= quant[zig]
	}
	idct(&b)
	var out [64]byte
	for i, c := range b {
		out[i] = uint8(max(0, min(255, c+128)))
	}
	return out
}

func TestIDCTStoreMatchesGo(t *testing.T) {
	if !simd {
		t.Skip("本机没有 SIMD 内核")
	}
	random := rand.New(rand.NewPCG(5, 6))
	for iteration := range 200000 {
		var coef, quant block
		limit := int64(1) << (iteration % 32)
		for i := range coef {
			if random.IntN(3) == 0 || i == 0 {
				coef[i] = int32(random.Int64N(2*limit) - limit)
			}
			quant[i] = random.Int32N(1 << (iteration % 17))
		}
		if iteration%4 == 0 {
			for i := range quant {
				quant[i] = 1 + random.Int32N(255)
			}
			for i := range coef {
				coef[i] = random.Int32N(4096) - 2048
			}
		}
		want := referenceStore(&coef, &quant)
		var coefT, quantT block
		for zig := range blockSize {
			coefT[unzigT[zig]] = coef[unzig[zig]]
			quantT[unzigT[zig]] = quant[zig]
		}
		const stride = 13
		dst := make([]byte, 7*stride+8)
		idctStore(&dst[0], stride, &coefT, &quantT)
		for y := range 8 {
			for x := range 8 {
				if got := dst[y*stride+x]; got != want[y*8+x] {
					t.Fatalf("第 %d 次：(%d,%d) 为 %d，期望 %d", iteration, x, y, got, want[y*8+x])
				}
			}
		}
		if coefT != (block{}) {
			t.Fatalf("第 %d 次：系数块没有清零", iteration)
		}
	}
}

// encodedSamples 返回 image/jpeg 编码的彩色与灰度样本，覆盖块与 MCU 的尺寸余数、各质量与超过 safeMargin 的熵编码段
func encodedSamples(t *testing.T) map[string][]byte {
	files := map[string][]byte{}
	random := rand.New(rand.NewPCG(7, 8))
	for _, size := range []image.Point{{1, 1}, {7, 9}, {16, 16}, {33, 17}, {130, 67}, {257, 255}, {640, 480}} {
		rgba := image.NewRGBA(image.Rectangle{Max: size})
		gray := image.NewGray(image.Rectangle{Max: size})
		for y := range size.Y {
			for x := range size.X {
				c := color.RGBA{uint8(x*3 + random.IntN(40)), uint8(y*5 + random.IntN(40)), uint8(x*y + random.IntN(40)), 255}
				rgba.SetRGBA(x, y, c)
				gray.SetGray(x, y, color.Gray{Y: c.G})
			}
		}
		for _, quality := range []int{1, 50, 90, 100} {
			for name, m := range map[string]image.Image{"rgba": rgba, "gray": gray} {
				var out bytes.Buffer
				if err := stdjpeg.Encode(&out, m, &stdjpeg.Options{Quality: quality}); err != nil {
					t.Fatal(err)
				}
				files[fmt.Sprintf("%s-%v-q%d", name, size, quality)] = out.Bytes()
			}
		}
	}
	return files
}

// sameDecode 比较两次解码的结果：同为错误时比较错误文本，否则比较图片类型、尺寸、行跨度与全部平面
func sameDecode(want image.Image, wantErr error, got image.Image, gotErr error) string {
	if (wantErr != nil) != (gotErr != nil) {
		return fmt.Sprintf("image/jpeg 错误 %v，分支错误 %v", wantErr, gotErr)
	}
	if wantErr != nil {
		if wantErr.Error() != gotErr.Error() {
			return fmt.Sprintf("错误不同：%q 与 %q", wantErr, gotErr)
		}
		return ""
	}
	switch w := want.(type) {
	case *image.YCbCr:
		g, ok := got.(*image.YCbCr)
		if !ok || w.Rect != g.Rect || w.SubsampleRatio != g.SubsampleRatio || w.YStride != g.YStride || w.CStride != g.CStride ||
			!bytes.Equal(w.Y, g.Y) || !bytes.Equal(w.Cb, g.Cb) || !bytes.Equal(w.Cr, g.Cr) {
			return "YCbCr 平面不同"
		}
	case *image.Gray:
		g, ok := got.(*image.Gray)
		if !ok || w.Rect != g.Rect || w.Stride != g.Stride || !bytes.Equal(w.Pix, g.Pix) {
			return "灰度平面不同"
		}
	case *image.RGBA:
		g, ok := got.(*image.RGBA)
		if !ok || w.Rect != g.Rect || !bytes.Equal(w.Pix, g.Pix) {
			return "RGBA 像素不同"
		}
	case *image.CMYK:
		g, ok := got.(*image.CMYK)
		if !ok || w.Rect != g.Rect || !bytes.Equal(w.Pix, g.Pix) {
			return "CMYK 像素不同"
		}
	default:
		return fmt.Sprintf("未比较的类型 %T", want)
	}
	return ""
}

// decodeStd 以 image/jpeg 解码，panic 视为错误
func decodeStd(data []byte) (m image.Image, err error) {
	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("panic: %v", r)
		}
	}()
	return stdjpeg.Decode(bytes.NewReader(data))
}

// decodeFork 以本包解码，panic 视为错误
func decodeFork(data []byte) (m image.Image, err error) {
	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("panic: %v", r)
		}
	}()
	return Decode(data)
}

// corrupt 按 round 对 data 的副本做截断、随机改字节、插入 RST 标记或把字节改成 0xff 开头的标记之一
func corrupt(random *rand.Rand, data []byte, round int) []byte {
	out := bytes.Clone(data)
	switch round % 4 {
	case 0:
		out = out[:random.IntN(len(out))]
	case 1:
		for range 1 + random.IntN(4) {
			out[random.IntN(len(out))] = uint8(random.IntN(256))
		}
	case 2:
		at := len(out)/4 + random.IntN(len(out)*3/4)
		marker := []byte{0xff, uint8(0xd0 + random.IntN(10))}
		out = append(out[:at:at], append(marker, out[at:]...)...)
	case 3:
		at := random.IntN(len(out))
		out[at] = 0xff
		if at+1 < len(out) {
			out[at+1] = uint8(random.IntN(256))
		}
	}
	return out
}

func TestDecodeMatchesImageJPEG(t *testing.T) {
	for name, data := range encodedSamples(t) {
		want, wantErr := decodeStd(data)
		got, gotErr := decodeFork(data)
		if diff := sameDecode(want, wantErr, got, gotErr); diff != "" {
			t.Errorf("%s: %s", name, diff)
		}
	}
}

func TestDecodeCorruptMatchesImageJPEG(t *testing.T) {
	random := rand.New(rand.NewPCG(9, 10))
	samples := encodedSamples(t)
	for _, name := range slices.Sorted(maps.Keys(samples)) {
		data := samples[name]
		for round := range 40 {
			input := corrupt(random, data, round)
			want, wantErr := decodeStd(input)
			got, gotErr := decodeFork(input)
			if diff := sameDecode(want, wantErr, got, gotErr); diff != "" {
				t.Errorf("%s 第 %d 轮：%s", name, round, diff)
			}
		}
	}
}
