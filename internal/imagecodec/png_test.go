package imagecodec

import (
	"bytes"
	"compress/zlib"
	"encoding/binary"
	"image"
	"image/color"
	"image/png"
	"io"
	"math/rand/v2"
	"testing"
)

// testImages 返回覆盖快速路径各类型、宽度余数与五种过滤选择的确定性图片
func testImages() map[string]image.Image {
	images := map[string]image.Image{}
	random := rand.New(rand.NewPCG(1, 2))
	sizes := []image.Point{{1, 1}, {7, 3}, {8, 2}, {33, 17}, {65, 9}, {129, 40}, {257, 31}}
	for _, size := range sizes {
		rect := image.Rectangle{Max: size}
		name := func(kind string) string { return kind + "-" + rect.String() }
		rgba, nrgba, translucent, gray := image.NewRGBA(rect), image.NewNRGBA(rect), image.NewNRGBA(rect), image.NewGray(rect)
		for y := range size.Y {
			for x := range size.X {
				c := testColor(random, x, y)
				rgba.Set(x, y, c)
				nrgba.Set(x, y, c)
				c.A = uint8(random.IntN(256))
				translucent.SetNRGBA(x, y, c)
				gray.SetGray(x, y, color.Gray{Y: c.G})
			}
		}
		images[name("rgba")], images[name("nrgba")], images[name("nrgba-alpha")], images[name("gray")] = rgba, nrgba, translucent, gray
	}
	return images
}

// testColor 按区域生成噪声、平坦、横向与纵向渐变像素，使各行选择不同的过滤类型
func testColor(random *rand.Rand, x, y int) color.NRGBA {
	switch y % 5 {
	case 0:
		return color.NRGBA{uint8(random.IntN(256)), uint8(random.IntN(256)), uint8(random.IntN(256)), 0xff}
	case 1:
		return color.NRGBA{40, 90, 200, 0xff}
	case 2:
		return color.NRGBA{uint8(x * 3), uint8(x * 5), uint8(x * 7), 0xff}
	case 3:
		return color.NRGBA{uint8(x*2 + y*3), uint8(x + y*5), uint8(x*7 + y), 0xff}
	}
	return color.NRGBA{uint8(y * 9), uint8(y*4 + random.IntN(3)), uint8(y * 2), 0xff}
}

func TestEncodePNGMatchesImagePNG(t *testing.T) {
	filters := map[byte]int{}
	for name, img := range testImages() {
		var want, got bytes.Buffer
		if err := png.Encode(&want, img); err != nil {
			t.Fatal(err)
		}
		if err := EncodePNG(&got, img); err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if !bytes.Equal(got.Bytes(), want.Bytes()) {
			t.Errorf("%s: EncodePNG 输出 %d 字节，与 png.Encode 的 %d 字节不同", name, got.Len(), want.Len())
			continue
		}
		for _, f := range pngRowFilters(t, got.Bytes()) {
			filters[f]++
		}
	}
	for f := byte(filterNone); f <= filterPaeth; f++ {
		if filters[f] == 0 {
			t.Errorf("测试图片没有选到过滤类型 %d：%v", f, filters)
		}
	}
}

// pngRowFilters 解出 PNG 每行的过滤类型字节
func pngRowFilters(t *testing.T, data []byte) []byte {
	t.Helper()
	width, height := binary.BigEndian.Uint32(data[16:20]), binary.BigEndian.Uint32(data[20:24])
	bits := map[[2]byte]int{{8, 0}: 8, {8, 2}: 24, {8, 6}: 32}[[2]byte{data[24], data[25]}]
	var idat []byte
	for offset := 8; offset < len(data); {
		length := int(binary.BigEndian.Uint32(data[offset:]))
		if string(data[offset+4:offset+8]) == "IDAT" {
			idat = append(idat, data[offset+8:offset+8+length]...)
		}
		offset += 12 + length
	}
	reader, err := zlib.NewReader(bytes.NewReader(idat))
	if err != nil {
		t.Fatal(err)
	}
	raw, err := io.ReadAll(reader)
	if err != nil {
		t.Fatal(err)
	}
	stride := 1 + int(width)*bits/8
	filters := make([]byte, height)
	for y := range filters {
		filters[y] = raw[y*stride]
	}
	return filters
}

// filterReference 按 image/png 的 filter 计算全部过滤结果与五种绝对值和
func filterReference(cur, prev []byte, bpp int) (out [5][]byte, sums [5]int) {
	n := len(cur)
	for f := range out {
		out[f] = make([]byte, n)
	}
	abs8 := func(d uint8) int {
		if d < 128 {
			return int(d)
		}
		return 256 - int(d)
	}
	for i := range n {
		var a, c uint8
		if i >= bpp {
			a, c = cur[i-bpp], prev[i-bpp]
		}
		b := prev[i]
		out[filterNone][i] = cur[i]
		out[filterSub][i] = cur[i] - a
		out[filterUp][i] = cur[i] - b
		out[filterAverage][i] = cur[i] - uint8((int(a)+int(b))/2)
		out[filterPaeth][i] = cur[i] - paethReference(a, b, c)
		for f := range out {
			sums[f] += abs8(out[f][i])
		}
	}
	return out, sums
}

// paethReference 是 image/png 的 paeth
func paethReference(a, b, c uint8) uint8 {
	abs := func(x int) int {
		if x < 0 {
			return -x
		}
		return x
	}
	pc := int(c)
	pa := int(b) - pc
	pb := int(a) - pc
	pc = abs(pa + pb)
	pa = abs(pa)
	pb = abs(pb)
	if pa <= pb && pa <= pc {
		return a
	} else if pb <= pc {
		return b
	}
	return c
}

// runFilterRow 在行前保留 bpp 个 0 字节、行后保留 rowPad 字节的缓冲中调用 filterRow
func runFilterRow(cur, prev []byte, bpp int) (out [5][]byte, sums [5]int) {
	n := len(cur)
	lead := max(bpp, rowPad)
	line := func(data []byte) []byte {
		buffer := make([]byte, lead+n+rowPad)
		copy(buffer[lead:], data)
		return buffer[lead:]
	}
	c, p := line(cur), line(prev)
	for f := filterSub; f <= filterPaeth; f++ {
		out[f] = line(nil)
	}
	filterRow(&c[0], &p[0], &out[filterSub][0], &out[filterUp][0], &out[filterAverage][0], &out[filterPaeth][0], n, bpp, &sums)
	out[filterNone] = c
	for f := range out {
		out[f] = out[f][:n]
	}
	return out, sums
}

func TestFilterRowMatchesReference(t *testing.T) {
	if !simd {
		t.Skip("本机没有 SIMD 内核")
	}
	random := rand.New(rand.NewPCG(3, 4))
	for _, bpp := range []int{1, 2, 3, 4, 6, 8} {
		for n := bpp; n <= 300; n += bpp {
			cur, prev := make([]byte, n), make([]byte, n)
			for i := range cur {
				cur[i], prev[i] = uint8(random.IntN(256)), uint8(random.IntN(256))
			}
			wantOut, wantSums := filterReference(cur, prev, bpp)
			gotOut, gotSums := runFilterRow(cur, prev, bpp)
			if gotSums != wantSums {
				t.Fatalf("bpp=%d n=%d：绝对值和 %v，期望 %v", bpp, n, gotSums, wantSums)
			}
			for f := range wantOut {
				if !bytes.Equal(gotOut[f], wantOut[f]) {
					t.Fatalf("bpp=%d n=%d：过滤类型 %d 的结果不同", bpp, n, f)
				}
			}
		}
	}
}

func TestFilterRowPaethAllInputs(t *testing.T) {
	if !simd {
		t.Skip("本机没有 SIMD 内核")
	}
	// 以 bpp 为行长一半构造行，后半每个字节的左邻、上方与左上互不相关，覆盖全部 a、b、c 组合
	const half = 1 << 16
	cur, prev := make([]byte, 2*half), make([]byte, 2*half)
	for a := range 256 {
		for i := range half {
			b, c := i>>8, i&0xff
			cur[i], prev[half+i], prev[i] = uint8(a), uint8(b), uint8(c)
			cur[half+i] = uint8(a*7 + b*3 + c)
		}
		gotOut, _ := runFilterRow(cur, prev, half)
		for i := range half {
			b, c := i>>8, i&0xff
			if want := cur[half+i] - paethReference(uint8(a), uint8(b), uint8(c)); gotOut[filterPaeth][half+i] != want {
				t.Fatalf("a=%d b=%d c=%d：Paeth 结果 %d，期望 %d", a, b, c, gotOut[filterPaeth][half+i], want)
			}
		}
	}
}
