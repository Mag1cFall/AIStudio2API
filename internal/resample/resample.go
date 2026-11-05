// Package resample 以 Kaiser 窗 sinc 多相滤波转换 16 位 PCM 的采样率
package resample

import (
	"encoding/binary"
	"math"
)

const (
	// halfTaps 为上采样时滤波器每侧的抽头数，下采样时按采样率比放大
	halfTaps = 128
	// bandwidth 为截止频率相对较低一侧奈奎斯特频率的比例
	bandwidth = 0.975
	// beta 为 Kaiser 窗的形状参数
	beta = 12
	// phases 为插值型系数表的相位数，也是精确相位滤波允许的最大化简输出采样率
	phases = 1024
	// blockOutputs 为每次交给内核的输出数下限
	blockOutputs = 1024
)

// kaiserNorm 为 Kaiser 窗中心值的倒数
var kaiserNorm = 1 / besselI0(beta)

// PCM 把 16 位小端交错 PCM 从 from Hz 转换为 to Hz，输出 ceil(帧数×to/from) 帧，第 n 帧取输入时刻 n×from/to 处的值
func PCM(data []byte, channels, from, to int) []byte {
	g := gcd(from, to)
	up, down := to/g, from/g
	taps, cutoff := 2*halfTaps, bandwidth
	if up < down {
		taps = (2*halfTaps*down/up + 7) &^ 7
		cutoff = bandwidth * float64(up) / float64(down)
	}
	var f filter
	if up <= phases {
		f = newExact(up, down, taps, cutoff)
	} else {
		f = newInterpolated(up, down, taps, cutoff)
	}
	frames := len(data) / 2 / channels
	outFrames := (frames*to + from - 1) / from
	lead := taps/2 - 1
	x := make([]float32, lead+frames+taps/2)
	y := make([]float32, f.block())
	out := make([]byte, outFrames*channels*2)
	for c := range channels {
		for i := range frames {
			x[lead+i] = float32(int16(binary.LittleEndian.Uint16(data[(i*channels+c)*2:])))
		}
		for first := 0; first < outFrames; first += len(y) {
			block := y[:min(len(y), outFrames-first)]
			f.run(block, x, first)
			for i, v := range block {
				sample := int16(math.Round(max(math.MinInt16, min(math.MaxInt16, float64(v)))))
				binary.LittleEndian.PutUint16(out[((first+i)*channels+c)*2:], uint16(sample))
			}
		}
	}
	return out
}

// filter 计算一段连续输出，x 为前补 taps/2-1 个、后补 taps/2 个零的单声道输入
type filter interface {
	// block 返回 run 每次计算的输出数
	block() int
	// run 计算从第 first 个输出起的 len(y) 个输出，first 为 block 的整数倍
	run(y, x []float32, first int)
}

// exact 为化简输出采样率不超过 phases 时的多相滤波，每块含整数个周期，各块的系数行与相对输入起点相同
type exact struct {
	taps, advance int
	coef          []float32
	rows, starts  []int32
}

// newExact 为 up 个相位各生成一行系数，并排出一块输出的系数行与相对输入起点
func newExact(up, down, taps int, cutoff float64) *exact {
	periods := (blockOutputs + up - 1) / up
	rows := make([]int32, periods*up)
	starts := make([]int32, periods*up)
	for i := range rows {
		t := i % up
		rows[i] = int32(t * down % up)
		starts[i] = int32(i/up*down + t*down/up)
	}
	return &exact{taps: taps, advance: periods * down, coef: coefficients(up, up, taps, cutoff), rows: rows, starts: starts}
}

func (f *exact) block() int { return len(f.rows) }

func (f *exact) run(y, x []float32, first int) {
	base := first / len(f.rows) * f.advance
	dots(y, f.coef, x[base:], f.rows[:len(y)], f.starts[:len(y)], f.taps)
}

// interpolated 为化简输出采样率超过 phases 时的多相滤波，每个输出取相邻两个相位的点积按相位小数线性插值
type interpolated struct {
	taps, up, down int
	coef           []float32
	rows, starts   []int32
	fracs, a, b    []float32
}

// newInterpolated 生成 phases+1 个等距相位的系数行，最后一行是下一个输入采样的第 0 相位
func newInterpolated(up, down, taps int, cutoff float64) *interpolated {
	return &interpolated{
		taps: taps, up: up, down: down, coef: coefficients(phases, phases+1, taps, cutoff),
		rows: make([]int32, blockOutputs), starts: make([]int32, blockOutputs),
		fracs: make([]float32, blockOutputs), a: make([]float32, blockOutputs), b: make([]float32, blockOutputs),
	}
}

func (f *interpolated) block() int { return blockOutputs }

func (f *interpolated) run(y, x []float32, first int) {
	n := len(y)
	for i := range n {
		position := (first + i) * f.down
		phase := position % f.up * phases
		f.rows[i], f.starts[i] = int32(phase/f.up), int32(position/f.up)
		f.fracs[i] = float32(float64(phase%f.up) / float64(f.up))
	}
	a, b := f.a[:n], f.b[:n]
	dots(a, f.coef, x, f.rows[:n], f.starts[:n], f.taps)
	dots(b, f.coef[f.taps:], x, f.rows[:n], f.starts[:n], f.taps)
	for i := range y {
		y[i] = a[i] + float32(f.fracs[i]*(b[i]-a[i]))
	}
}

// coefficients 返回 rows 行、每行 taps 个系数：第 p 行为输出时刻落在窗口第 taps/2-1 个输入之后 p/phases 个输入采样时各抽头的权重；各行都是偶函数原型在步长 1/phases 的网格上的采样，原型只算非负一侧
func coefficients(phases, rows, taps int, cutoff float64) []float32 {
	half := taps / 2
	prototype := make([]float32, phases*half+1)
	for m := range prototype {
		prototype[m] = float32(windowedSinc(float64(m)/float64(phases), float64(half), cutoff))
	}
	coef := make([]float32, rows*taps)
	for p := range rows {
		for j := range taps {
			m := p + phases*(half-1-j)
			coef[p*taps+j] = prototype[max(m, -m)]
		}
	}
	return coef
}

// windowedSinc 返回截止频率为 cutoff（相对输入奈奎斯特频率）、半宽为 half 个输入采样的 Kaiser 窗 sinc 在 t 处的值
func windowedSinc(t, half, cutoff float64) float64 {
	if t == 0 {
		return cutoff
	}
	r := t / half
	x := math.Pi * cutoff * t
	return cutoff * math.Sin(x) / x * besselI0(beta*math.Sqrt(1-r*r)) * kaiserNorm
}

// besselI0 返回第一类零阶修正贝塞尔函数在 x 处的值，幂级数求和到项小于和的 1e-17
func besselI0(x float64) float64 {
	sum, term := 1.0, 1.0
	for k := 1.0; term > sum*1e-17; k++ {
		term *= x * x / (4 * k * k)
		sum += term
	}
	return sum
}

// gcd 返回 a 与 b 的最大公约数
func gcd(a, b int) int {
	for b != 0 {
		a, b = b, a%b
	}
	return a
}

// dots 依次计算 y[i] 为 coef 第 rows[i] 行与 x[starts[i]:] 前 taps 个元素的点积，taps 为 8 的倍数
func dots(y, coef, x []float32, rows, starts []int32, taps int) {
	n := 0
	if simd && len(y) >= 4 {
		n = len(y) &^ 3
		dotsSIMD(&y[0], &coef[0], &x[0], &rows[0], &starts[0], n, taps)
	}
	for i := n; i < len(y); i++ {
		r, s := int(rows[i])*taps, int(starts[i])
		y[i] = dot(coef[r:r+taps], x[s:s+taps])
	}
}

// dot 返回 h 与 x 的点积：乘积按下标模 8 分 8 路累加，第 l 路先与第 l+4 路相加，再两两相加，与 dotsSIMD 逐位相同
func dot(h, x []float32) float32 {
	var a [8]float32
	for j := 0; j < len(h); j += 8 {
		hj, xj := h[j:j+8:j+8], x[j:j+8:j+8]
		for l := range a {
			a[l] += float32(hj[l] * xj[l])
		}
	}
	return (a[0] + a[4] + (a[1] + a[5])) + (a[2] + a[6] + (a[3] + a[7]))
}
