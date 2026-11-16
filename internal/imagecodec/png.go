// Package imagecodec 提供生成图片输出的 PNG 编码与 CatmullRom 缩放，热点循环由 amd64 AVX2 与 arm64 NEON 汇编实现，PNG 的过滤与解压后数据和 image/png 逐字节相同，缩放与 x/image/draw 逐像素相同
package imagecodec

import (
	"bufio"
	"bytes"
	"encoding/binary"
	"hash/adler32"
	"hash/crc32"
	"image"
	"image/png"
	"io"
	"runtime"
	"sync"

	"github.com/klauspost/compress/flate"
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

// EncodePNG 把 m 编码为 PNG，各行过滤与解压后的数据与 png.Encode 逐字节相同，压缩分段并行；没有 SIMD 内核时与快速路径之外的图片类型由 png.Encode 编码
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

// stripBytes 是并行压缩时每段过滤后数据的目标字节数
const stripBytes = 256 << 10

// dictionaryBytes 是每段压缩前以上一段末尾数据预置的 deflate 字典长度
const dictionaryBytes = 32 << 10

// deflaters 复用默认级别的 deflate 写入器
var deflaters = sync.Pool{New: func() any {
	w, _ := flate.NewWriter(nil, flate.DefaultCompression)
	return w
}}

// strip 是一段行压缩后的 deflate 数据、过滤后数据的 Adler-32 与长度
type strip struct {
	deflate []byte
	adler   uint32
	size    int64
	err     error
}

// writeIDATs 按 stripBytes 把行分段，各段并行转换、过滤并以默认级别 deflate 压缩，段间以上一段末尾 32 KiB 作字典并以同步刷新衔接，按顺序写入 32 KiB 的 IDAT 块；解压后的数据与 image/png 逐字节相同
func (e *pngWriter) writeIDATs(source pngSource, n, height int) error {
	rows := max(1, stripBytes/(n+1))
	count := (height + rows - 1) / rows
	results := make([]chan strip, count)
	for i := range results {
		results[i] = make(chan strip, 1)
	}
	workers := make(chan struct{}, runtime.GOMAXPROCS(0))
	go func() {
		for i := range count {
			workers <- struct{}{}
			go func() {
				first, last := i*rows, min(height, (i+1)*rows)
				results[i] <- compressStrip(source, n, first, last, max(0, first-(dictionaryBytes+n)/(n+1)), last == height)
				<-workers
			}()
		}
	}()
	bw := bufio.NewWriterSize(e, 1<<15)
	_, _ = bw.Write([]byte{0x78, 0x9c})
	var adler uint32 = 1
	var err error
	for _, result := range results {
		part := <-result
		if err != nil {
			continue
		}
		if err = part.err; err != nil {
			continue
		}
		adler = adler32Combine(adler, part.adler, part.size)
		_, err = bw.Write(part.deflate)
	}
	if err != nil {
		return err
	}
	var trailer [4]byte
	binary.BigEndian.PutUint32(trailer[:], adler)
	_, _ = bw.Write(trailer[:])
	return bw.Flush()
}

// compressStrip 过滤 [first, last) 行并以 deflate 压缩，[dictionary, first) 行过滤后的末尾 32 KiB 作字典；final 为真时写出结束块，否则以同步刷新结束
func compressStrip(source pngSource, n, first, last, dictionary int, final bool) strip {
	filter := newRowFilter(n, source.bpp)
	if dictionary > 0 {
		source.row(dictionary-1, filter.prev())
	}
	history := make([]byte, 0, (first-dictionary)*(n+1))
	for y := dictionary; y < first; y++ {
		history = append(history, filter.next(source, y)...)
	}
	history = history[max(0, len(history)-dictionaryBytes):]
	var compressed bytes.Buffer
	zw := deflaters.Get().(*flate.Writer)
	defer deflaters.Put(zw)
	zw.ResetDict(&compressed, history)
	checksum := adler32.New()
	var size int64
	for y := first; y < last; y++ {
		line := filter.next(source, y)
		_, _ = zw.Write(line)
		_, _ = checksum.Write(line)
		size += int64(len(line))
	}
	var err error
	if final {
		err = zw.Close()
	} else {
		err = zw.Flush()
	}
	return strip{deflate: compressed.Bytes(), adler: checksum.Sum32(), size: size, err: err}
}

// rowFilter 逐行转换并选择 image/png 所选的过滤类型
type rowFilter struct {
	n, bpp    int
	lines     [6][]byte
	cur, last int
	sums      [5]int
}

// newRowFilter 返回行长 n、每像素 bpp 字节的行过滤器，上一行初始为全 0
func newRowFilter(n, bpp int) *rowFilter {
	stride := rowPad + n + rowPad
	buffer := make([]byte, 6*stride)
	filter := &rowFilter{n: n, bpp: bpp, cur: 0, last: 5}
	// lines[i][0] 是过滤类型字节，其后是行数据；lines[0] 与 lines[5] 轮流存当前行与上一行的原始字节，类型字节与其前的保留区保持为 0
	for i := range filter.lines {
		filter.lines[i] = buffer[i*stride+rowPad-1 : (i+1)*stride]
	}
	for f := filterSub; f <= filterPaeth; f++ {
		filter.lines[f][0] = byte(f)
	}
	return filter
}

// prev 返回上一行原始字节的缓冲，供从中间行开始时写入
func (filter *rowFilter) prev() []byte {
	return filter.lines[filter.last][1 : 1+filter.n]
}

// next 转换并过滤第 y 行，返回带过滤类型字节的行数据，结果在下一次调用前有效
func (filter *rowFilter) next(source pngSource, y int) []byte {
	lines, n := &filter.lines, filter.n
	source.row(y, lines[filter.cur][1:])
	filterRow(&lines[filter.cur][1], &lines[filter.last][1], &lines[filterSub][1], &lines[filterUp][1], &lines[filterAverage][1], &lines[filterPaeth][1], n, filter.bpp, &filter.sums)
	best, f := filter.sums[filterUp], filterUp
	for _, candidate := range [...]int{filterPaeth, filterNone, filterSub, filterAverage} {
		if filter.sums[candidate] < best {
			best, f = filter.sums[candidate], candidate
		}
	}
	line := lines[f]
	if f == filterNone {
		line = lines[filter.cur]
	}
	filter.cur, filter.last = filter.last, filter.cur
	return line[:1+n]
}

// adler32Combine 返回两段数据相接后的 Adler-32，second 为后一段的校验和、size 为后一段长度，与 zlib 的 adler32_combine 相同
func adler32Combine(first, second uint32, size int64) uint32 {
	const base = 65521
	rem := uint32(size % base)
	sum1 := first & 0xffff
	sum2 := rem * sum1 % base
	sum1 += second&0xffff + base - 1
	sum2 += first>>16 + second>>16 + base - rem
	if sum1 >= base {
		sum1 -= base
	}
	if sum1 >= base {
		sum1 -= base
	}
	if sum2 >= base<<1 {
		sum2 -= base << 1
	}
	if sum2 >= base {
		sum2 -= base
	}
	return sum1 | sum2<<16
}
