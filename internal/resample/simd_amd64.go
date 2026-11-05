package resample

import "golang.org/x/sys/cpu"

// simd 表示 CPU 支持 AVX，可运行本包的汇编内核
var simd = cpu.X86.HasAVX
