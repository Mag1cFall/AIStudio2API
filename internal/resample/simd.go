//go:build amd64 || arm64

package resample

// dotsSIMD 以每组 4 个输出计算 dots 的前 n 个结果，n 为 4 的倍数，taps 为 8 的倍数
//
//go:noescape
func dotsSIMD(y, coef, x *float32, rows, starts *int32, n, taps int)
