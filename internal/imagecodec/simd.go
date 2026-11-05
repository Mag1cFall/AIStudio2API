//go:build amd64 || arm64

package imagecodec

// filterRow 计算 cur 相对 prev 的 Sub、Up、Average、Paeth 过滤结果写入对应缓冲，sums 依次为 None、Sub、Up、Average、Paeth 的逐字节绝对值和；cur 与 prev 之前 bpp 字节为 0，各缓冲在 n 之后保留 rowPad 字节
//
//go:noescape
func filterRow(cur, prev, sub, up, avg, paeth *byte, n, bpp int, sums *[5]int)

// rgbFromRGBA 把 n 个 RGBA 像素去掉 alpha 写入 dst，n 为 8 的倍数，dst 在 3n 字节之后保留 8 字节
//
//go:noescape
func rgbFromRGBA(dst, src *byte, n int)

// rgbaFromYCbCr 把 n 个 YCbCr 像素按 color.YCbCrToRGB 转为不透明 RGBA 写入 dst，n 为 8 的倍数，shift 为色度水平位移
//
//go:noescape
func rgbaFromYCbCr(dst, y, cb, cr *byte, n, shift int)

// convertRGBA 把 n 个 RGBA 像素的各分量乘 0x101 转为 float64 写入 dst
//
//go:noescape
func convertRGBA(dst *float64, src *byte, n int)

// convertNRGBA 把 n 个 NRGBA 像素按 x/image/draw 的整数运算预乘 alpha 后转为 float64 写入 dst
//
//go:noescape
func convertNRGBA(dst *float64, src *byte, n int)

// convertGray 把 n 个灰度像素乘 0x101 转为 float64 写入 dst 每个像素的四个分量
//
//go:noescape
func convertGray(dst *float64, src *byte, n int)

// convertYCbCr 把 n 个 YCbCr 像素按 color.YCbCr.RGBA 转为 float64 写入 dst，n 为 8 的倍数，shift 为色度水平位移
//
//go:noescape
func convertYCbCr(dst *float64, y, cb, cr *byte, n, shift int)

// scaleRows 对 rowGroup 行取色结果做水平卷积，按 x/image/draw 的累加顺序与舍入写出 n 列结果；alphaOne 为 1 时 alpha 记为 1
//
//go:noescape
func scaleRows(tmp *float64, tmpStride int, conv *float64, convStride int, sources *source, n int, contribs *contrib, alphaOne int)

// scaleColumn 对 taps 行水平结果做垂直卷积，按 x/image/draw 的 scaleY_RGBA_Src 写出 n 个 RGBA 像素
//
//go:noescape
func scaleColumn(dst *byte, rows **float64, weights *float64, taps, n int, invTotalWeight float64)
