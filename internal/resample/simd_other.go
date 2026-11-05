//go:build !amd64 && !arm64

package resample

// simd 表示本平台没有本包的汇编内核
const simd = false

func dotsSIMD(y, coef, x *float32, rows, starts *int32, n, taps int) {}
