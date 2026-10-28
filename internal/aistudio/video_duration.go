package aistudio

import (
	"encoding/binary"
	"time"
)

// mp4MoovLimit 是读取 moov box 的最大字节数
const mp4MoovLimit = 64 << 20

// mp4Duration 从写入的 ISO BMFF 字节流中读取 moov/mvhd 的时长，跳过其余顶层 box，流不是 ISO BMFF 时停止解析
type mp4Duration struct {
	header   []byte
	skip     int64
	moov     []byte
	moovLeft int64
	boxes    int
	done     bool
	duration time.Duration
}

// Write 按顶层 box 消费字节流，始终接受全部数据
func (scan *mp4Duration) Write(data []byte) (int, error) {
	written := len(data)
	for len(data) > 0 && !scan.done {
		switch {
		case scan.moovLeft > 0:
			take := min(int64(len(data)), scan.moovLeft)
			scan.moov, scan.moovLeft, data = append(scan.moov, data[:take]...), scan.moovLeft-take, data[take:]
			if scan.moovLeft == 0 {
				scan.duration, scan.done = mvhdDuration(scan.moov), true
			}
		case scan.skip > 0:
			take := min(int64(len(data)), scan.skip)
			scan.skip, data = scan.skip-take, data[take:]
		default:
			need := 8
			if len(scan.header) >= 8 && binary.BigEndian.Uint32(scan.header) == 1 {
				need = 16
			}
			take := min(need-len(scan.header), len(data))
			scan.header, data = append(scan.header, data[:take]...), data[take:]
			if len(scan.header) == need && (need == 16 || binary.BigEndian.Uint32(scan.header) != 1) {
				scan.openBox()
			}
		}
	}
	return written, nil
}

// openBox 按已读取的 box 头决定读取 moov 内容或跳过 box 内容
func (scan *mp4Duration) openBox() {
	header := scan.header
	scan.header = nil
	kind := string(header[4:8])
	size := int64(binary.BigEndian.Uint32(header))
	if len(header) == 16 {
		size = int64(binary.BigEndian.Uint64(header[8:]))
	}
	scan.boxes++
	body := size - int64(len(header))
	if scan.boxes == 1 && kind != "ftyp" || size == 0 || body < 0 || kind == "moov" && body > mp4MoovLimit {
		scan.done = true
		return
	}
	if kind == "moov" {
		scan.moovLeft = body
		return
	}
	scan.skip = body
}

// mvhdDuration 在 moov 内容中查找 mvhd 并换算时长，找不到时为 0
func mvhdDuration(moov []byte) time.Duration {
	for len(moov) >= 8 {
		size := int(binary.BigEndian.Uint32(moov))
		if size < 8 || size > len(moov) {
			return 0
		}
		if string(moov[4:8]) == "mvhd" && size >= 32 {
			body := moov[8:size]
			if body[0] == 1 && len(body) >= 32 {
				return boxDuration(uint64(binary.BigEndian.Uint32(body[20:])), binary.BigEndian.Uint64(body[24:]))
			}
			return boxDuration(uint64(binary.BigEndian.Uint32(body[12:])), uint64(binary.BigEndian.Uint32(body[16:])))
		}
		moov = moov[size:]
	}
	return 0
}

// boxDuration 把 timescale 单位的时长换算为 time.Duration
func boxDuration(timescale uint64, units uint64) time.Duration {
	if timescale == 0 {
		return 0
	}
	return time.Duration(float64(units) / float64(timescale) * float64(time.Second))
}
