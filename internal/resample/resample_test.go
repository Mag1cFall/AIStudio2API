package resample

import (
	"encoding/binary"
	"math"
	"math/rand/v2"
	"testing"
)

// TestDotsMatchesGo 检查汇编内核在各种抽头数、输出数、系数行与输入起点下与 dot 逐位相同
func TestDotsMatchesGo(t *testing.T) {
	if !simd {
		t.Skip("本平台没有汇编内核")
	}
	random := rand.New(rand.NewPCG(1, 2))
	for _, taps := range []int{8, 16, 256, 384, 1536} {
		for _, n := range []int{4, 8, 12, 1024, 1027} {
			coef := make([]float32, 9*taps)
			for i := range coef {
				coef[i] = random.Float32()*2 - 1
			}
			x := make([]float32, 3*taps+n)
			for i := range x {
				x[i] = float32(random.IntN(65536) - 32768)
			}
			rows, starts := make([]int32, n), make([]int32, n)
			for i := range rows {
				rows[i] = int32(random.IntN(9))
				starts[i] = int32(random.IntN(len(x) - taps + 1))
			}
			got := make([]float32, n)
			dots(got, coef, x, rows, starts, taps)
			for i := range got {
				r, s := int(rows[i])*taps, int(starts[i])
				if want := dot(coef[r:r+taps], x[s:s+taps]); math.Float32bits(got[i]) != math.Float32bits(want) {
					t.Fatalf("taps=%d n=%d 第 %d 个输出为 %v，Go 实现为 %v", taps, n, i, got[i], want)
				}
			}
		}
	}
}

// sinePCM 返回 1 s 的 16 位立体声 PCM，左右声道分别为振幅 30000 的 left 与 right Hz 正弦
func sinePCM(rate int, left, right float64) []byte {
	data := make([]byte, rate*4)
	for i := range rate {
		for c, freq := range []float64{left, right} {
			value := math.Round(30000 * math.Sin(2*math.Pi*freq*float64(i)/float64(rate)))
			binary.LittleEndian.PutUint16(data[(i*2+c)*2:], uint16(int16(value)))
		}
	}
	return data
}

// TestPCMSine 检查各采样率比下的输出帧数，以及左右声道正弦与理想重采样的误差只剩 16 位量化噪声：理想输出为同一正弦在新采样时刻的值
func TestPCMSine(t *testing.T) {
	for _, rates := range [][2]int{
		{24000, 8000}, {24000, 16000}, {24000, 22050}, {24000, 32000}, {24000, 44100}, {24000, 48000},
		{16000, 24000}, {48000, 44100}, {24000, 44101},
	} {
		from, to := rates[0], rates[1]
		out := PCM(sinePCM(from, 1000, 3000), 2, from, to)
		frames := (from*to + from - 1) / from
		if len(out) != frames*4 {
			t.Fatalf("%d→%d Hz 输出 %d 字节，应为 %d 帧", from, to, len(out), frames)
		}
		for c, freq := range []float64{1000, 3000} {
			var signal, noise float64
			for n := to / 50; n < frames-to/50; n++ {
				ideal := 30000 * math.Sin(2*math.Pi*freq*float64(n)/float64(to))
				got := float64(int16(binary.LittleEndian.Uint16(out[(n*2+c)*2:])))
				signal += ideal * ideal
				noise += (got - ideal) * (got - ideal)
			}
			if snr := 10 * math.Log10(signal/noise); snr < 90 {
				t.Errorf("%d→%d Hz 声道 %d 的 %v Hz 正弦信噪比 %.1f dB，低于 90 dB", from, to, c, freq, snr)
			}
		}
	}
}

// TestPCMRejectsAliases 检查下采样滤除高于新奈奎斯特频率的成分：24 kHz 的 8.5 kHz 与 10 kHz 正弦转为 16 kHz 后不高于 16 位量化噪声
func TestPCMRejectsAliases(t *testing.T) {
	out := PCM(sinePCM(24000, 8500, 10000), 2, 24000, 16000)
	for c, freq := range []float64{8500, 10000} {
		var power float64
		for n := 320; n < len(out)/4-320; n++ {
			value := float64(int16(binary.LittleEndian.Uint16(out[(n*2+c)*2:])))
			power += value * value
		}
		power /= float64(len(out)/4 - 640)
		if level := 10 * math.Log10(power/(30000*30000/2)); level > -90 {
			t.Errorf("%v Hz 正弦转为 16 kHz 后电平 %.1f dB，高于 -90 dB", freq, level)
		}
	}
}
