//go:build amd64 || arm64

package jpeg

// idctStore 把按列存放的系数块 coef 按同样存放的量化表 quant 反量化，做与 idct 逐位相同的二维 IDCT，加 128 截到 [0,255] 后按行写入 dst 起的 8 行（行跨度 stride），并把 coef 清零
//
//go:noescape
func idctStore(dst *byte, stride int, coef, quant *block)
