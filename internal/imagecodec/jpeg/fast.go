package jpeg

import (
	"bytes"
	"encoding/binary"
)

// unzigT 把 zigzag 序号映射为按列存放的自然序下标 列*8+行，顺序扫描按此写入系数，IDCT 汇编按列读入
var unzigT = func() (t [blockSize]uint8) {
	for zig, natural := range unzig {
		t[zig] = uint8(natural%8*8 + natural/8)
	}
	return t
}()

// safeMargin 是块解码不必检查可用位所需的到下一个标记的字节数：一个块最多 1985 位，字节填充后不超过 498 字节，另加 8 字节预读
const safeMargin = 512

// transposeQuant 由 zigzag 序的量化表生成按列存放的自然序量化表
func (d *decoder) transposeQuant() {
	for tq := range d.quant {
		for zig, q := range d.quant[tq] {
			d.quantT[tq][unzigT[zig]] = q
		}
	}
}

// tablesReady 判断扫描用到的 Huffman 表都已定义；未定义时由通用循环在首次解码该表时报错
func (d *decoder) tablesReady(nComp int, scan *[maxComponents]scanComponent) bool {
	for i := range nComp {
		if d.huff[dcTable][scan[i].td].nCodes == 0 || d.huff[acTable][scan[i].ta].nCodes == 0 {
			return false
		}
	}
	return true
}

// noFF 判断 v 的 8 个字节中没有 0xff
func noFF(v uint64) bool {
	return (^v-0x0101010101010101)&v&0x8080808080808080 == 0
}

// nextMarker 返回 data 中 from 起第一个其后不是 0x00 的 0xff 的下标，没有时返回 len(data)
func nextMarker(data []byte, from int) int {
	for {
		i := bytes.IndexByte(data[from:], 0xff)
		if i < 0 {
			return len(data)
		}
		from += i
		if from+1 >= len(data) || data[from+1] != 0x00 {
			return from
		}
		from += 2
	}
}

// bitReader 是顺序扫描的 64 位位缓冲：acc 左对齐存放 n 位，其中最后 virt 位是标记或数据结尾之后补的零位，endErr 是 decodeHuffman 读到该处时返回的错误，pos 为下一个要装入的字节
type bitReader struct {
	data   []byte
	pos    int
	acc    uint64
	n      uint
	virt   uint
	endErr error
}

// fill 把位缓冲补到超过 56 位：后 8 字节没有 0xff 时一次装入整字节，否则逐字节处理字节填充；遇到标记或数据结尾时补零位计入 virt，并记下 endErr
func (r *bitReader) fill() {
	if r.pos+8 <= len(r.data) {
		if v := binary.BigEndian.Uint64(r.data[r.pos:]); noFF(v) {
			r.acc |= v >> r.n
			k := (63 - r.n) >> 3
			r.n += k << 3
			r.pos += int(k)
			r.acc &= ^uint64(0) << (64 - r.n)
			return
		}
	}
	for r.n <= 56 {
		if r.pos >= len(r.data) || r.data[r.pos] == 0xff && (r.pos+1 >= len(r.data) || r.data[r.pos+1] != 0x00) {
			if r.endErr == nil {
				r.endErr = errShortHuffmanData
				if r.pos+1 < len(r.data) {
					r.endErr = errMissingFF00
				}
			}
			r.n += 8
			r.virt += 8
			continue
		}
		c := r.data[r.pos]
		r.pos++
		if c == 0xff {
			r.pos++
		}
		r.acc |= uint64(c) << (56 - r.n)
		r.n += 8
	}
}

// fillStuffed 逐字节把位缓冲补到超过 56 位，跳过 0xff 之后的填充字节；调用方保证其间没有标记
func fillStuffed(data []byte, pos int, acc uint64, n uint) (int, uint64, uint) {
	for n <= 56 {
		c := data[pos]
		pos++
		if c == 0xff {
			pos++
		}
		acc |= uint64(c) << (56 - n)
		n += 8
	}
	return pos, acc, n
}

// slowSymbol 按 decodeHuffman 的慢路径从位缓冲顶端逐位解码一个符号，avail 为可用的真实位数，不足时返回 endErr
func slowSymbol(h *huffman, acc uint64, avail uint, endErr error) (uint8, uint, error) {
	for i, code := 0, int32(0); i < maxCodeLength; i++ {
		if uint(i) >= avail {
			return 0, 0, endErr
		}
		code |= int32(acc>>(63-i)) & 1
		if code <= h.maxCodes[i] {
			return h.vals[h.valsIndices[i]+code-h.minCodes[i]], uint(i + 1), nil
		}
		code <<= 1
	}
	return 0, 0, FormatError("bad Huffman code")
}

// unread 返回位缓冲中 n-virt 位真实数据里整字节未用部分退回后的读取位置，即 decodeHuffman 按需读字节时停下的位置；字节填充的 0xff 0x00 算一个字节，不越过 start
func (r *bitReader) unread(start int) int {
	pos := r.pos
	for whole := (r.n - r.virt) / 8; whole > 0; whole-- {
		if pos-2 >= start && r.data[pos-1] == 0x00 && r.data[pos-2] == 0xff {
			pos -= 2
		} else {
			pos--
		}
	}
	return pos
}

// blockFast 在 pos 起 safeMargin 字节内没有标记时解码一个块，返回 DC 差值与更新后的 eobRun；AC 系数按列存放写入 blk，eobRun 大于 0 时只解 DC
func (r *bitReader) blockFast(blk *block, dcHuff, acHuff *huffman, eobRun uint16) (int32, uint16, error) {
	data, pos, acc, n := r.data, r.pos, r.acc, r.n
	if n < 32 {
		if v := binary.BigEndian.Uint64(data[pos:]); noFF(v) {
			acc |= v >> n
			k := (63 - n) >> 3
			n += k << 3
			pos += int(k)
			acc &= ^uint64(0) << (64 - n)
		} else {
			pos, acc, n = fillStuffed(data, pos, acc, n)
		}
	}
	var symbol uint8
	var length uint
	if e := dcHuff.fast[acc>>(64-fastBits)]; e != 0 {
		symbol, length = uint8(e>>8), uint(e&0xff)
	} else {
		var err error
		if symbol, length, err = slowSymbol(dcHuff, acc, n, nil); err != nil {
			return 0, 0, err
		}
	}
	acc <<= length
	n -= length
	if symbol > 16 {
		return 0, 0, UnsupportedError("excessive DC component")
	}
	delta := extend(int32(acc>>(64-uint(symbol))), symbol)
	acc <<= symbol
	n -= uint(symbol)

	if eobRun > 0 {
		eobRun--
	} else {
		fastAC, fast, coef := &acHuff.fastAC, &acHuff.fast, blk[:]
		for k := uint(1); k < blockSize; k++ {
			if n < 32 {
				if v := binary.BigEndian.Uint64(data[pos:]); noFF(v) {
					acc |= v >> n
					k8 := (63 - n) >> 3
					n += k8 << 3
					pos += int(k8)
					acc &= ^uint64(0) << (64 - n)
				} else {
					pos, acc, n = fillStuffed(data, pos, acc, n)
				}
			}
			peek := acc >> (64 - fastBits)
			if f := fastAC[peek]; f != 0 {
				k += uint(f>>12) & 0x0f
				if k >= blockSize {
					length = uint(f>>6) & 63
					acc <<= length
					n -= length
					break
				}
				acc <<= uint(f) & 63
				n -= uint(f) & 63
				coef[unzigT[k]&63] = int32(f) >> 16
				continue
			}
			if e := fast[peek]; e != 0 {
				symbol, length = uint8(e>>8), uint(e&0xff)
			} else {
				var err error
				if symbol, length, err = slowSymbol(acHuff, acc, n, nil); err != nil {
					return 0, 0, err
				}
			}
			acc <<= length
			n -= length
			run, size := uint(symbol>>4), uint(symbol&0x0f)
			if size != 0 {
				k += run
				if k >= blockSize {
					break
				}
				coef[unzigT[k]&63] = extend(int32(acc>>(64-size)), uint8(size))
				acc <<= size
				n -= size
				continue
			}
			if run != 0x0f {
				eobRun = uint16(1) << run
				if run != 0 {
					eobRun |= uint16(acc >> (64 - run))
					acc <<= run
					n -= run
				}
				eobRun--
				break
			}
			k += 0x0f
		}
	}
	r.pos, r.acc, r.n = pos, acc, n
	return delta, eobRun, nil
}

// blockCareful 解码一个块，每次取位前检查真实可用位，在数据结尾与标记处得到与 decodeHuffman、receiveExtend 相同的错误；返回值同 blockFast
func (r *bitReader) blockCareful(blk *block, dcHuff, acHuff *huffman, eobRun uint16) (int32, uint16, error) {
	if r.n < 32 {
		r.fill()
	}
	var symbol uint8
	var length uint
	if e := dcHuff.fast[r.acc>>(64-fastBits)]; r.n-r.virt >= 8 && e != 0 {
		symbol, length = uint8(e>>8), uint(e&0xff)
		if length > r.n-r.virt {
			return 0, 0, r.endErr
		}
	} else {
		var err error
		if symbol, length, err = slowSymbol(dcHuff, r.acc, r.n-r.virt, r.endErr); err != nil {
			return 0, 0, err
		}
	}
	r.acc <<= length
	r.n -= length
	if symbol > 16 {
		return 0, 0, UnsupportedError("excessive DC component")
	}
	size := uint(symbol)
	if size > r.n-r.virt {
		return 0, 0, r.endErr
	}
	delta := extend(int32(r.acc>>(64-size)), symbol)
	r.acc <<= size
	r.n -= size

	if eobRun > 0 {
		return delta, eobRun - 1, nil
	}
	for k := uint(1); k < blockSize; k++ {
		if r.n < 32 {
			r.fill()
		}
		avail := r.n - r.virt
		peek := r.acc >> (64 - fastBits)
		if f := acHuff.fastAC[peek]; f != 0 && avail >= 8 {
			k += uint(f>>12) & 0x0f
			if k >= blockSize {
				length = uint(f>>6) & 63
				if length > avail {
					return 0, 0, r.endErr
				}
				r.acc <<= length
				r.n -= length
				break
			}
			length = uint(f) & 63
			if length > avail {
				return 0, 0, r.endErr
			}
			r.acc <<= length
			r.n -= length
			blk[unzigT[k]&63] = int32(f) >> 16
			continue
		}
		if e := acHuff.fast[peek]; avail >= 8 && e != 0 {
			symbol, length = uint8(e>>8), uint(e&0xff)
			if length > avail {
				return 0, 0, r.endErr
			}
		} else {
			var err error
			if symbol, length, err = slowSymbol(acHuff, r.acc, avail, r.endErr); err != nil {
				return 0, 0, err
			}
		}
		r.acc <<= length
		r.n -= length
		run, size := uint(symbol>>4), uint(symbol&0x0f)
		if size != 0 {
			k += run
			if k >= blockSize {
				break
			}
			if size > r.n-r.virt {
				return 0, 0, r.endErr
			}
			blk[unzigT[k]&63] = extend(int32(r.acc>>(64-size)), uint8(size))
			r.acc <<= size
			r.n -= size
			continue
		}
		if run != 0x0f {
			eobRun = uint16(1) << run
			if run != 0 {
				if run > r.n-r.virt {
					return 0, 0, r.endErr
				}
				eobRun |= uint16(r.acc >> (64 - run))
				r.acc <<= run
				r.n -= run
			}
			return delta, eobRun - 1, nil
		}
		k += 0x0f
	}
	return delta, eobRun, nil
}

// decodeSequential 解码顺序扫描：解出的符号、错误、EOB run 与扫描结束后的字节位置都与 processSOS 的通用循环相同
func (d *decoder) decodeSequential(nComp int, scan *[maxComponents]scanComponent, mxx, myy int) error {
	d.transposeQuant()
	var planes [maxComponents][]byte
	var strides [maxComponents]int
	if d.nComp == 1 {
		planes[0], strides[0] = d.img1.Pix, d.img1.Stride
	} else {
		planes[0], strides[0] = d.img3.Y, d.img3.YStride
		planes[1], strides[1] = d.img3.Cb, d.img3.CStride
		planes[2], strides[2] = d.img3.Cr, d.img3.CStride
		planes[3], strides[3] = d.blackPix, d.blackStride
	}
	r := bitReader{data: d.bytes.buf[:d.bytes.j], pos: d.bytes.i}
	start := r.pos
	limit := nextMarker(r.data, start)
	var (
		dc         [maxComponents]int32
		blk        block
		bx, by     int
		blockCount int
	)
	eobRun := d.eobRun
	mcu, expectedRST := 0, uint8(rst0Marker)
	for my := 0; my < myy; my++ {
		for mx := 0; mx < mxx; mx++ {
			for i := 0; i < nComp; i++ {
				compIndex := scan[i].compIndex
				hi := d.comp[compIndex].h
				vi := d.comp[compIndex].v
				dcHuff := &d.huff[dcTable][scan[i].td]
				acHuff := &d.huff[acTable][scan[i].ta]
				quant := &d.quantT[d.comp[compIndex].tq]
				for j := 0; j < hi*vi; j++ {
					if nComp != 1 {
						bx = hi*mx + j%hi
						by = vi*my + j/hi
					} else {
						q := mxx * hi
						bx = blockCount % q
						by = blockCount / q
						blockCount++
						if bx*8 >= d.width || by*8 >= d.height {
							continue
						}
					}
					var delta int32
					var err error
					if r.pos+safeMargin <= limit {
						delta, eobRun, err = r.blockFast(&blk, dcHuff, acHuff, eobRun)
					} else {
						delta, eobRun, err = r.blockCareful(&blk, dcHuff, acHuff, eobRun)
					}
					if err != nil {
						return err
					}
					dc[compIndex] += delta
					blk[0] = dc[compIndex]
					if simd && !d.flex {
						stride := strides[compIndex]
						offset := 8 * (by*stride + bx)
						dst := planes[compIndex][offset : offset+7*stride+8]
						idctStore(&dst[0], stride, &blk, quant)
						continue
					}
					var natural block
					for k := range natural {
						natural[k] = blk[k%8*8+k/8]
					}
					blk = block{}
					if err := d.reconstructBlock(&natural, bx, by, int(compIndex)); err != nil {
						return err
					}
				}
			}
			mcu++
			if d.ri > 0 && mcu%d.ri == 0 && mcu < mxx*myy {
				d.bytes.i, d.bytes.nUnreadable, d.bits = r.unread(start), 0, bits{}
				if err := d.readFull(d.tmp[:2]); err != nil {
					return err
				} else if d.tmp[0] != 0xff || d.tmp[1] != expectedRST {
					if err := d.findRST(expectedRST); err != nil {
						return err
					}
				}
				expectedRST++
				if expectedRST == rst7Marker+1 {
					expectedRST = rst0Marker
				}
				r = bitReader{data: r.data, pos: d.bytes.i}
				start = r.pos
				limit = nextMarker(r.data, start)
				dc = [maxComponents]int32{}
				eobRun = 0
			}
		}
	}
	d.bytes.i, d.bytes.nUnreadable, d.bits, d.eobRun = r.unread(start), 0, bits{}, eobRun
	return nil
}
