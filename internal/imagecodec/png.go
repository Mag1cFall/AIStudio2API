// Package imagecodec 提供生成图片输出的 PNG 编码与 CatmullRom 缩放，热点循环由 amd64 AVX2 与 arm64 NEON 汇编实现，PNG 与 image/png 逐字节相同，缩放与 x/image/draw 逐像素相同
package imagecodec

import (
	"bufio"
	"compress/zlib"
	"encoding/binary"
	"hash/crc32"
	"image"
	"image/png"
	"io"
)

// rowPad 是行缓冲数据前后的保留字节数，汇编按整向量读取行首左邻并越过行尾写入
const rowPad = 64

// PNG 行过滤类型
const (
	filterNone = iota
	filterSub
	filterUp
	filterAverage
	filterPaeth
)

// pngSource 是快速路径支持的图片类型的行字节来源
type pngSource struct {
	bitDepth, colorType, bpp int
	// row 把第 y 行的 PNG 原始字节写入 dst
	row func(y int, dst []byte)
}

// EncodePNG 把 m 编码为 PNG，输出与 png.Encode 逐字节相同；没有 SIMD 内核时与快速路径之外的图片类型由 png.Encode 编码
func EncodePNG(w io.Writer, m image.Image) error {
	source, ok := newPNGSource(m)
	if !simd || !ok {
		return png.Encode(w, m)
	}
	bounds := m.Bounds()
	width, height := bounds.Dx(), bounds.Dy()
	e := &pngWriter{w: w}
	_, e.err = io.WriteString(w, "\x89PNG\r\n\x1a\n")
	var ihdr [13]byte
	binary.BigEndian.PutUint32(ihdr[0:4], uint32(width))
	binary.BigEndian.PutUint32(ihdr[4:8], uint32(height))
	ihdr[8], ihdr[9] = byte(source.bitDepth), byte(source.colorType)
	e.writeChunk(ihdr[:], "IHDR")
	if e.err == nil {
		e.err = e.writeIDATs(source, width*source.bpp, height)
	}
	e.writeChunk(nil, "IEND")
	return e.err
}

// newPNGSource 按 png.Encode 的颜色类型选择返回快速路径的行来源
func newPNGSource(m image.Image) (pngSource, bool) {
	bounds := m.Bounds()
	if bounds.Empty() {
		return pngSource{}, false
	}
	width := bounds.Dx()
	switch m := m.(type) {
	case *image.Gray:
		return pngSource{bitDepth: 8, colorType: 0, bpp: 1, row: func(y int, dst []byte) {
			offset := y * m.Stride
			copy(dst, m.Pix[offset:offset+width])
		}}, true
	case *image.RGBA:
		if !m.Opaque() {
			return pngSource{}, false
		}
		return pngSource{bitDepth: 8, colorType: 2, bpp: 3, row: func(y int, dst []byte) {
			rgbRow(dst, m.Pix[y*m.Stride:y*m.Stride+width*4])
		}}, true
	case *image.NRGBA:
		if !m.Opaque() {
			return pngSource{bitDepth: 8, colorType: 6, bpp: 4, row: func(y int, dst []byte) {
				offset := y * m.Stride
				copy(dst, m.Pix[offset:offset+width*4])
			}}, true
		}
		return pngSource{bitDepth: 8, colorType: 2, bpp: 3, row: func(y int, dst []byte) {
			rgbRow(dst, m.Pix[y*m.Stride:y*m.Stride+width*4])
		}}, true
	}
	return pngSource{}, false
}

// rgbRow 把 RGBA 或 NRGBA 行去掉 alpha 写入 dst
func rgbRow(dst, src []byte) {
	n := len(src) / 4 &^ 7
	if n > 0 {
		rgbFromRGBA(&dst[0], &src[0], n)
	}
	for i := n; i < len(src)/4; i++ {
		dst[3*i], dst[3*i+1], dst[3*i+2] = src[4*i], src[4*i+1], src[4*i+2]
	}
}

// pngWriter 写出 PNG 数据块，作为 io.Writer 时每次写入一个 IDAT 块
type pngWriter struct {
	w      io.Writer
	err    error
	header [8]byte
	footer [4]byte
}

// writeChunk 写出类型为 name、数据为 b 的数据块
func (e *pngWriter) writeChunk(b []byte, name string) {
	if e.err != nil {
		return
	}
	binary.BigEndian.PutUint32(e.header[:4], uint32(len(b)))
	copy(e.header[4:], name)
	crc := crc32.NewIEEE()
	crc.Write(e.header[4:8])
	crc.Write(b)
	binary.BigEndian.PutUint32(e.footer[:], crc.Sum32())
	if _, e.err = e.w.Write(e.header[:]); e.err != nil {
		return
	}
	if _, e.err = e.w.Write(b); e.err != nil {
		return
	}
	_, e.err = e.w.Write(e.footer[:])
}

func (e *pngWriter) Write(b []byte) (int, error) {
	e.writeChunk(b, "IDAT")
	if e.err != nil {
		return 0, e.err
	}
	return len(b), nil
}

// writeIDATs 逐行转换、过滤并以默认级别 zlib 压缩写入 32 KiB 的 IDAT 块，与 image/png 的写法一致
func (e *pngWriter) writeIDATs(source pngSource, n, height int) error {
	stride := rowPad + n + rowPad
	buffer := make([]byte, 6*stride)
	// lines[i][0] 是过滤类型字节，其后是行数据；lines[0] 与 lines[5] 轮流存当前行与上一行的原始字节，类型字节与其前的保留区保持为 0
	var lines [6][]byte
	for i := range lines {
		lines[i] = buffer[i*stride+rowPad-1 : (i+1)*stride]
	}
	for f := filterSub; f <= filterPaeth; f++ {
		lines[f][0] = byte(f)
	}
	cur, prev := 0, 5
	bw := bufio.NewWriterSize(e, 1<<15)
	zw, err := zlib.NewWriterLevel(bw, zlib.DefaultCompression)
	if err != nil {
		return err
	}
	var sums [5]int
	for y := range height {
		source.row(y, lines[cur][1:])
		filterRow(&lines[cur][1], &lines[prev][1], &lines[filterSub][1], &lines[filterUp][1], &lines[filterAverage][1], &lines[filterPaeth][1], n, source.bpp, &sums)
		best, f := sums[filterUp], filterUp
		for _, candidate := range [...]int{filterPaeth, filterNone, filterSub, filterAverage} {
			if sums[candidate] < best {
				best, f = sums[candidate], candidate
			}
		}
		line := lines[f]
		if f == filterNone {
			line = lines[cur]
		}
		if _, err := zw.Write(line[:1+n]); err != nil {
			return err
		}
		cur, prev = prev, cur
	}
	if err := zw.Close(); err != nil {
		return err
	}
	return bw.Flush()
}
