//go:build !amd64 && !arm64

package jpeg

// simd 表示本平台没有本包的汇编内核
const simd = false

func idctStore(dst *byte, stride int, coef, quant *block) {}
