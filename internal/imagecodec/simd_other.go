//go:build !amd64 && !arm64

package imagecodec

// simd 表示本平台没有本包的汇编内核
const simd = false

func filterRow(cur, prev, sub, up, avg, paeth *byte, n, bpp int, sums *[5]int) {}

func rgbFromRGBA(dst, src *byte, n int) {}

func rgbaFromYCbCr(dst, y, cb, cr *byte, n, shift int) {}

func convertRGBA(dst *float64, src *byte, n int) {}

func convertNRGBA(dst *float64, src *byte, n int) {}

func convertGray(dst *float64, src *byte, n int) {}

func convertYCbCr(dst *float64, y, cb, cr *byte, n, shift int) {}

func scaleRows(tmp *float64, tmpStride int, conv *float64, convStride int, sources *source, n int, contribs *contrib, alphaOne int) {
}

func scaleColumn(dst *byte, rows **float64, weights *float64, taps, n int, invTotalWeight float64) {}
