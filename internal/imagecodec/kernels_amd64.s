#include "textflag.h"

DATA ones<>+0(SB)/8, $0x0101010101010101
DATA ones<>+8(SB)/8, $0x0101010101010101
DATA ones<>+16(SB)/8, $0x0101010101010101
DATA ones<>+24(SB)/8, $0x0101010101010101
GLOBL ones<>(SB), RODATA|NOPTR, $32

// tailmask 前 32 字节为 0xff、后 32 字节为 0，从 tailmask+32-k 读取 32 字节得到前 k 字节有效的掩码
DATA tailmask<>+0(SB)/8, $-1
DATA tailmask<>+8(SB)/8, $-1
DATA tailmask<>+16(SB)/8, $-1
DATA tailmask<>+24(SB)/8, $-1
DATA tailmask<>+32(SB)/8, $0
DATA tailmask<>+40(SB)/8, $0
DATA tailmask<>+48(SB)/8, $0
DATA tailmask<>+56(SB)/8, $0
GLOBL tailmask<>(SB), RODATA|NOPTR, $64

// rgbshuf 在每个 128 位通道内取出 4 个像素的 RGB
DATA rgbshuf<>+0(SB)/8, $0x0908060504020100
DATA rgbshuf<>+8(SB)/8, $0x808080800e0d0c0a
DATA rgbshuf<>+16(SB)/8, $0x0908060504020100
DATA rgbshuf<>+24(SB)/8, $0x808080800e0d0c0a
GLOBL rgbshuf<>(SB), RODATA|NOPTR, $32

DATA k10101<>+0(SB)/8, $0x0001010100010101
DATA k10101<>+8(SB)/8, $0x0001010100010101
DATA k10101<>+16(SB)/8, $0x0001010100010101
DATA k10101<>+24(SB)/8, $0x0001010100010101
GLOBL k10101<>(SB), RODATA|NOPTR, $32

DATA k128<>+0(SB)/8, $0x0000008000000080
DATA k128<>+8(SB)/8, $0x0000008000000080
DATA k128<>+16(SB)/8, $0x0000008000000080
DATA k128<>+24(SB)/8, $0x0000008000000080
GLOBL k128<>(SB), RODATA|NOPTR, $32

DATA k91881<>+0(SB)/8, $0x000166e9000166e9
DATA k91881<>+8(SB)/8, $0x000166e9000166e9
DATA k91881<>+16(SB)/8, $0x000166e9000166e9
DATA k91881<>+24(SB)/8, $0x000166e9000166e9
GLOBL k91881<>(SB), RODATA|NOPTR, $32

DATA k22554<>+0(SB)/8, $0x0000581a0000581a
DATA k22554<>+8(SB)/8, $0x0000581a0000581a
DATA k22554<>+16(SB)/8, $0x0000581a0000581a
DATA k22554<>+24(SB)/8, $0x0000581a0000581a
GLOBL k22554<>(SB), RODATA|NOPTR, $32

DATA k46802<>+0(SB)/8, $0x0000b6d20000b6d2
DATA k46802<>+8(SB)/8, $0x0000b6d20000b6d2
DATA k46802<>+16(SB)/8, $0x0000b6d20000b6d2
DATA k46802<>+24(SB)/8, $0x0000b6d20000b6d2
GLOBL k46802<>(SB), RODATA|NOPTR, $32

DATA k116130<>+0(SB)/8, $0x0001c5a20001c5a2
DATA k116130<>+8(SB)/8, $0x0001c5a20001c5a2
DATA k116130<>+16(SB)/8, $0x0001c5a20001c5a2
DATA k116130<>+24(SB)/8, $0x0001c5a20001c5a2
GLOBL k116130<>(SB), RODATA|NOPTR, $32

DATA kffffff<>+0(SB)/8, $0x00ffffff00ffffff
DATA kffffff<>+8(SB)/8, $0x00ffffff00ffffff
DATA kffffff<>+16(SB)/8, $0x00ffffff00ffffff
DATA kffffff<>+24(SB)/8, $0x00ffffff00ffffff
GLOBL kffffff<>(SB), RODATA|NOPTR, $32

// FILTER_BODY 以 Y5、Y6、Y7、Y8 中的 x、a、b、c 计算 32 字节的四种过滤结果写到 AX 偏移处，绝对值和累加到 Y0 至 Y4；Y15 为 0
#define FILTER_BODY \
	VPABSB    Y5, Y9; VPSADBW Y15, Y9, Y9; VPADDQ Y9, Y0, Y0; \
	VPSUBB    Y6, Y5, Y9; VMOVDQU Y9, (R8)(AX*1); VPABSB Y9, Y9; VPSADBW Y15, Y9, Y9; VPADDQ Y9, Y1, Y1; \
	VPSUBB    Y7, Y5, Y9; VMOVDQU Y9, (R9)(AX*1); VPABSB Y9, Y9; VPSADBW Y15, Y9, Y9; VPADDQ Y9, Y2, Y2; \
	VPAVGB    Y7, Y6, Y9; VPXOR Y7, Y6, Y10; VPAND ones<>(SB), Y10, Y10; VPSUBB Y10, Y9, Y9; VPSUBB Y9, Y5, Y9; \
	VMOVDQU   Y9, (R10)(AX*1); VPABSB Y9, Y9; VPSADBW Y15, Y9, Y9; VPADDQ Y9, Y3, Y3; \
	VPSUBUSB  Y8, Y7, Y9; VPSUBUSB Y7, Y8, Y10; VPOR Y10, Y9, Y11; \
	VPSUBUSB  Y8, Y6, Y9; VPSUBUSB Y6, Y8, Y12; VPOR Y12, Y9, Y13; \
	VPCMPEQB  Y15, Y10, Y10; VPCMPEQB Y15, Y12, Y12; VPXOR Y12, Y10, Y10; \
	VPADDUSB  Y13, Y11, Y9; VPSUBUSB Y13, Y11, Y12; VPSUBUSB Y11, Y13, Y14; VPOR Y14, Y12, Y12; \
	VPBLENDVB Y10, Y12, Y9, Y9; \
	VPMINUB   Y13, Y11, Y10; VPCMPEQB Y11, Y10, Y10; VPMINUB Y9, Y11, Y12; VPCMPEQB Y11, Y12, Y12; VPAND Y12, Y10, Y10; \
	VPMINUB   Y9, Y13, Y12; VPCMPEQB Y13, Y12, Y12; \
	VPBLENDVB Y12, Y7, Y8, Y9; VPBLENDVB Y10, Y6, Y9, Y9; \
	VPSUBB    Y9, Y5, Y9; VMOVDQU Y9, (R11)(AX*1); VPABSB Y9, Y9; VPSADBW Y15, Y9, Y9; VPADDQ Y9, Y4, Y4

// HSUM 把 Y 寄存器四个 64 位通道的和写入 dst
#define HSUM(y, x, dst) \
	VEXTRACTI128 $1, y, X9; VPADDQ X9, x, x; VPSHUFD $0x4e, x, X9; VPADDQ X9, x, x; VMOVQ x, dst

// func filterRow(cur, prev, sub, up, avg, paeth *byte, n, bpp int, sums *[5]int)
TEXT ·filterRow(SB), NOSPLIT, $0-72
	MOVQ  cur+0(FP), SI
	MOVQ  prev+8(FP), DI
	MOVQ  sub+16(FP), R8
	MOVQ  up+24(FP), R9
	MOVQ  avg+32(FP), R10
	MOVQ  paeth+40(FP), R11
	MOVQ  n+48(FP), CX
	MOVQ  bpp+56(FP), DX
	MOVQ  SI, R12
	SUBQ  DX, R12
	MOVQ  DI, R13
	SUBQ  DX, R13
	VPXOR Y15, Y15, Y15
	VPXOR Y0, Y0, Y0
	VPXOR Y1, Y1, Y1
	VPXOR Y2, Y2, Y2
	VPXOR Y3, Y3, Y3
	VPXOR Y4, Y4, Y4
	XORQ  AX, AX
	MOVQ  CX, DX
	SUBQ  $32, DX

filterLoop:
	CMPQ    AX, DX
	JGT     filterTail
	VMOVDQU (SI)(AX*1), Y5
	VMOVDQU (R12)(AX*1), Y6
	VMOVDQU (DI)(AX*1), Y7
	VMOVDQU (R13)(AX*1), Y8
	FILTER_BODY
	ADDQ    $32, AX
	JMP     filterLoop

filterTail:
	CMPQ    AX, CX
	JGE     filterDone
	MOVQ    CX, BX
	SUBQ    AX, BX
	LEAQ    tailmask<>+32(SB), DX
	SUBQ    BX, DX
	VMOVDQU (DX), Y14
	VPAND   (SI)(AX*1), Y14, Y5
	VPAND   (R12)(AX*1), Y14, Y6
	VPAND   (DI)(AX*1), Y14, Y7
	VPAND   (R13)(AX*1), Y14, Y8
	FILTER_BODY

filterDone:
	MOVQ sums+64(FP), BX
	HSUM(Y0, X0, 0(BX))
	HSUM(Y1, X1, 8(BX))
	HSUM(Y2, X2, 16(BX))
	HSUM(Y3, X3, 24(BX))
	HSUM(Y4, X4, 32(BX))
	VZEROUPPER
	RET

// func rgbFromRGBA(dst, src *byte, n int)
TEXT ·rgbFromRGBA(SB), NOSPLIT, $0-24
	MOVQ    dst+0(FP), DI
	MOVQ    src+8(FP), SI
	MOVQ    n+16(FP), CX
	VMOVDQU rgbshuf<>(SB), Y1

rgbLoop:
	VMOVDQU      (SI), Y0
	VPSHUFB      Y1, Y0, Y0
	VMOVDQU      X0, (DI)
	VEXTRACTI128 $1, Y0, X2
	VMOVDQU      X2, 12(DI)
	ADDQ         $32, SI
	ADDQ         $24, DI
	SUBQ         $8, CX
	JNZ          rgbLoop
	VZEROUPPER
	RET

DATA kalpha<>+0(SB)/8, $0xff000000ff000000
DATA kalpha<>+8(SB)/8, $0xff000000ff000000
DATA kalpha<>+16(SB)/8, $0xff000000ff000000
DATA kalpha<>+24(SB)/8, $0xff000000ff000000
GLOBL kalpha<>(SB), RODATA|NOPTR, $32

// YCBCR_RGBA 以 Y1、Y2 中 8 个像素的 Cb、Cr 与 SI 处 8 个 Y 按 color.YCbCrToRGB 计算 8 位 RGB，加 alpha 0xff 写到 DI 处 32 字节并后移；Y15 为 0
#define YCBCR_RGBA \
	VPMOVZXBD (SI), Y0; VPMULLD k10101<>(SB), Y0, Y0; \
	VPSUBD    k128<>(SB), Y1, Y1; VPSUBD k128<>(SB), Y2, Y2; \
	VPMULLD   k91881<>(SB), Y2, Y3; VPADDD Y0, Y3, Y3; \
	VPMULLD   k22554<>(SB), Y1, Y4; VPSUBD Y4, Y0, Y4; VPMULLD k46802<>(SB), Y2, Y5; VPSUBD Y5, Y4, Y4; \
	VPMULLD   k116130<>(SB), Y1, Y5; VPADDD Y0, Y5, Y5; \
	VPMAXSD   Y15, Y3, Y3; VPMINSD kffffff<>(SB), Y3, Y3; VPSRLD $16, Y3, Y3; \
	VPMAXSD   Y15, Y4, Y4; VPMINSD kffffff<>(SB), Y4, Y4; VPSRLD $16, Y4, Y4; \
	VPMAXSD   Y15, Y5, Y5; VPMINSD kffffff<>(SB), Y5, Y5; VPSRLD $16, Y5, Y5; \
	VPSLLD    $8, Y4, Y4; VPSLLD $16, Y5, Y5; VPOR Y4, Y3, Y3; VPOR Y5, Y3, Y3; VPOR kalpha<>(SB), Y3, Y3; \
	VMOVDQU   Y3, (DI); \
	ADDQ      $8, SI; ADDQ $32, DI

// func rgbaFromYCbCr(dst, y, cb, cr *byte, n, shift int)
TEXT ·rgbaFromYCbCr(SB), NOSPLIT, $0-48
	MOVQ  dst+0(FP), DI
	MOVQ  y+8(FP), SI
	MOVQ  cb+16(FP), R8
	MOVQ  cr+24(FP), R9
	MOVQ  n+32(FP), CX
	MOVQ  shift+40(FP), DX
	VPXOR Y15, Y15, Y15
	CMPQ  DX, $1
	JEQ   rgba422
	JGT   rgba411

rgba444:
	VPMOVZXBD (R8), Y1
	VPMOVZXBD (R9), Y2
	ADDQ      $8, R8
	ADDQ      $8, R9
	YCBCR_RGBA
	SUBQ      $8, CX
	JNZ       rgba444
	VZEROUPPER
	RET

rgba422:
	VMOVD      (R8), X1
	VMOVD      (R9), X2
	VPUNPCKLBW X1, X1, X1
	VPUNPCKLBW X2, X2, X2
	VPMOVZXBD  X1, Y1
	VPMOVZXBD  X2, Y2
	ADDQ       $4, R8
	ADDQ       $4, R9
	YCBCR_RGBA
	SUBQ       $8, CX
	JNZ        rgba422
	VZEROUPPER
	RET

rgba411:
	MOVWLZX    (R8), AX
	MOVWLZX    (R9), BX
	VMOVD      AX, X1
	VMOVD      BX, X2
	VPUNPCKLBW X1, X1, X1
	VPUNPCKLBW X2, X2, X2
	VPUNPCKLWD X1, X1, X1
	VPUNPCKLWD X2, X2, X2
	VPMOVZXBD  X1, Y1
	VPMOVZXBD  X2, Y2
	ADDQ       $2, R8
	ADDQ       $2, R9
	YCBCR_RGBA
	SUBQ       $8, CX
	JNZ        rgba411
	VZEROUPPER
	RET

DATA k257<>+0(SB)/8, $0x0000010100000101
DATA k257<>+8(SB)/8, $0x0000010100000101
DATA k257<>+16(SB)/8, $0x0000010100000101
DATA k257<>+24(SB)/8, $0x0000010100000101
GLOBL k257<>(SB), RODATA|NOPTR, $32

DATA k65535i<>+0(SB)/8, $0x0000ffff0000ffff
DATA k65535i<>+8(SB)/8, $0x0000ffff0000ffff
DATA k65535i<>+16(SB)/8, $0x0000ffff0000ffff
DATA k65535i<>+24(SB)/8, $0x0000ffff0000ffff
GLOBL k65535i<>(SB), RODATA|NOPTR, $32

DATA one<>+0(SB)/8, $0x3ff0000000000000
GLOBL one<>(SB), RODATA|NOPTR, $8

DATA half<>+0(SB)/8, $0x3fe0000000000000
GLOBL half<>(SB), RODATA|NOPTR, $8

DATA f65535<>+0(SB)/8, $0x40efffe000000000
GLOBL f65535<>(SB), RODATA|NOPTR, $8

DATA f255<>+0(SB)/8, $0x406fe00000000000
GLOBL f255<>(SB), RODATA|NOPTR, $8

// alphalane 只在第 3 个 64 位通道置符号位，作为 VBLENDVPD 的 alpha 选择掩码
DATA alphalane<>+0(SB)/8, $0
DATA alphalane<>+8(SB)/8, $0
DATA alphalane<>+16(SB)/8, $0
DATA alphalane<>+24(SB)/8, $0x8000000000000000
GLOBL alphalane<>(SB), RODATA|NOPTR, $32

// func convertRGBA(dst *float64, src *byte, n int)
TEXT ·convertRGBA(SB), NOSPLIT, $0-24
	MOVQ dst+0(FP), DI
	MOVQ src+8(FP), SI
	MOVQ n+16(FP), CX

rgbaPair:
	CMPQ         CX, $2
	JLT          rgbaSingle
	VPMOVZXBD    (SI), Y0
	VPMULLD      k257<>(SB), Y0, Y0
	VCVTDQ2PD    X0, Y1
	VEXTRACTI128 $1, Y0, X2
	VCVTDQ2PD    X2, Y2
	VMOVUPD      Y1, (DI)
	VMOVUPD      Y2, 32(DI)
	ADDQ         $8, SI
	ADDQ         $64, DI
	SUBQ         $2, CX
	JMP          rgbaPair

rgbaSingle:
	TESTQ     CX, CX
	JZ        rgbaDone
	VPMOVZXBD (SI), X0
	VPMULLD   k257<>(SB), X0, X0
	VCVTDQ2PD X0, Y1
	VMOVUPD   Y1, (DI)

rgbaDone:
	VZEROUPPER
	RET

// func convertNRGBA(dst *float64, src *byte, n int)
TEXT ·convertNRGBA(SB), NOSPLIT, $0-24
	MOVQ         dst+0(FP), DI
	MOVQ         src+8(FP), SI
	MOVQ         n+16(FP), CX
	VBROADCASTSD f255<>(SB), Y5
	TESTQ        CX, CX
	JZ           nrgbaDone

nrgbaLoop:
	VPMOVZXBD (SI), X0
	VPSHUFD   $0xff, X0, X1
	VPMULLD   k257<>(SB), X1, X1
	VPMULLD   X1, X0, X0
	VCVTDQ2PD X0, Y2
	VDIVPD    Y5, Y2, Y2
	VROUNDPD  $3, Y2, Y2
	VCVTDQ2PD X1, Y3
	VBLENDPD  $8, Y3, Y2, Y2
	VMOVUPD   Y2, (DI)
	ADDQ      $4, SI
	ADDQ      $32, DI
	DECQ      CX
	JNZ       nrgbaLoop

nrgbaDone:
	VZEROUPPER
	RET

// func convertGray(dst *float64, src *byte, n int)
TEXT ·convertGray(SB), NOSPLIT, $0-24
	MOVQ  dst+0(FP), DI
	MOVQ  src+8(FP), SI
	MOVQ  n+16(FP), CX
	TESTQ CX, CX
	JZ    grayDone

grayLoop:
	MOVBLZX      (SI), AX
	IMULL        $0x101, AX
	VCVTSI2SDL   AX, X0, X0
	VBROADCASTSD X0, Y0
	VMOVUPD      Y0, (DI)
	INCQ         SI
	ADDQ         $32, DI
	DECQ         CX
	JNZ          grayLoop

grayDone:
	VZEROUPPER
	RET

// YCBCR_RGB 以 Y1、Y2 中的 Cb、Cr 与 SI 处的 8 个 Y 计算 color.YCbCr.RGBA 的 R、G、B 到 Y3、Y4、Y5；Y15 为 0
#define YCBCR_RGB \
	VPMOVZXBD (SI), Y0; VPMULLD k10101<>(SB), Y0, Y0; \
	VPSUBD    k128<>(SB), Y1, Y1; VPSUBD k128<>(SB), Y2, Y2; \
	VPMULLD   k91881<>(SB), Y2, Y3; VPADDD Y0, Y3, Y3; \
	VPMULLD   k22554<>(SB), Y1, Y4; VPSUBD Y4, Y0, Y4; VPMULLD k46802<>(SB), Y2, Y5; VPSUBD Y5, Y4, Y4; \
	VPMULLD   k116130<>(SB), Y1, Y5; VPADDD Y0, Y5, Y5; \
	VPMAXSD   Y15, Y3, Y3; VPMINSD kffffff<>(SB), Y3, Y3; VPSRLD $8, Y3, Y3; \
	VPMAXSD   Y15, Y4, Y4; VPMINSD kffffff<>(SB), Y4, Y4; VPSRLD $8, Y4, Y4; \
	VPMAXSD   Y15, Y5, Y5; VPMINSD kffffff<>(SB), Y5, Y5; VPSRLD $8, Y5, Y5

// RGB_FLOAT 把 Y3、Y4、Y5 中 8 个像素的 R、G、B 与 alpha 0xffff 转为 float64 写入 DI 处 256 字节
#define RGB_FLOAT \
	VMOVDQU      k65535i<>(SB), Y1; \
	VPUNPCKLDQ   Y4, Y3, Y7; VPUNPCKHDQ Y4, Y3, Y8; VPUNPCKLDQ Y1, Y5, Y9; VPUNPCKHDQ Y1, Y5, Y10; \
	VPUNPCKLQDQ  Y9, Y7, Y11; VPUNPCKHQDQ Y9, Y7, Y12; VPUNPCKLQDQ Y10, Y8, Y13; VPUNPCKHQDQ Y10, Y8, Y14; \
	VCVTDQ2PD    X11, Y0; VMOVUPD Y0, (DI); VCVTDQ2PD X12, Y0; VMOVUPD Y0, 32(DI); \
	VCVTDQ2PD    X13, Y0; VMOVUPD Y0, 64(DI); VCVTDQ2PD X14, Y0; VMOVUPD Y0, 96(DI); \
	VEXTRACTI128 $1, Y11, X0; VCVTDQ2PD X0, Y0; VMOVUPD Y0, 128(DI); \
	VEXTRACTI128 $1, Y12, X0; VCVTDQ2PD X0, Y0; VMOVUPD Y0, 160(DI); \
	VEXTRACTI128 $1, Y13, X0; VCVTDQ2PD X0, Y0; VMOVUPD Y0, 192(DI); \
	VEXTRACTI128 $1, Y14, X0; VCVTDQ2PD X0, Y0; VMOVUPD Y0, 224(DI); \
	ADDQ         $8, SI; ADDQ $256, DI

// func convertYCbCr(dst *float64, y, cb, cr *byte, n, shift int)
TEXT ·convertYCbCr(SB), NOSPLIT, $0-48
	MOVQ  dst+0(FP), DI
	MOVQ  y+8(FP), SI
	MOVQ  cb+16(FP), R8
	MOVQ  cr+24(FP), R9
	MOVQ  n+32(FP), CX
	MOVQ  shift+40(FP), DX
	VPXOR Y15, Y15, Y15
	CMPQ  DX, $1
	JEQ   conv422
	JGT   conv411

conv444:
	VPMOVZXBD (R8), Y1
	VPMOVZXBD (R9), Y2
	ADDQ      $8, R8
	ADDQ      $8, R9
	YCBCR_RGB
	RGB_FLOAT
	SUBQ      $8, CX
	JNZ       conv444
	VZEROUPPER
	RET

conv422:
	VMOVD      (R8), X1
	VMOVD      (R9), X2
	VPUNPCKLBW X1, X1, X1
	VPUNPCKLBW X2, X2, X2
	VPMOVZXBD  X1, Y1
	VPMOVZXBD  X2, Y2
	ADDQ       $4, R8
	ADDQ       $4, R9
	YCBCR_RGB
	RGB_FLOAT
	SUBQ       $8, CX
	JNZ        conv422
	VZEROUPPER
	RET

conv411:
	MOVWLZX    (R8), AX
	MOVWLZX    (R9), BX
	VMOVD      AX, X1
	VMOVD      BX, X2
	VPUNPCKLBW X1, X1, X1
	VPUNPCKLBW X2, X2, X2
	VPUNPCKLWD X1, X1, X1
	VPUNPCKLWD X2, X2, X2
	VPMOVZXBD  X1, Y1
	VPMOVZXBD  X2, Y2
	ADDQ       $2, R8
	ADDQ       $2, R9
	YCBCR_RGB
	RGB_FLOAT
	SUBQ       $8, CX
	JNZ        conv411
	VZEROUPPER
	RET

// func scaleRows(tmp *float64, tmpStride int, conv *float64, convStride int, sources *source, n int, contribs *contrib, alphaOne int)
TEXT ·scaleRows(SB), NOSPLIT, $0-64
	MOVQ         tmp+0(FP), DI
	MOVQ         tmpStride+8(FP), R8
	MOVQ         conv+16(FP), SI
	MOVQ         convStride+24(FP), R9
	MOVQ         sources+32(FP), R10
	MOVQ         n+40(FP), CX
	MOVQ         contribs+48(FP), R11
	LEAQ         (SI)(R9*1), R13
	LEAQ         (R13)(R9*1), R12
	LEAQ         (R12)(R9*1), BX
	VBROADCASTSD one<>(SB), Y13
	VPXOR        Y14, Y14, Y14
	CMPQ         alphaOne+56(FP), $0
	JEQ          rowsColumn
	VMOVDQU      alphalane<>(SB), Y14

rowsColumn:
	MOVLQSX 0(R10), AX
	MOVLQSX 4(R10), DX
	VXORPD  Y0, Y0, Y0
	VXORPD  Y1, Y1, Y1
	VXORPD  Y2, Y2, Y2
	VXORPD  Y3, Y3, Y3
	CMPQ    AX, DX
	JGE     rowsStore
	SHLQ    $4, AX
	SHLQ    $4, DX

rowsTap:
	MOVLQSX      (R11)(AX*1), R9
	SHLQ         $5, R9
	VBROADCASTSD 8(R11)(AX*1), Y4
	VMULPD       (SI)(R9*1), Y4, Y5
	VADDPD       Y5, Y0, Y0
	VMULPD       (R13)(R9*1), Y4, Y6
	VADDPD       Y6, Y1, Y1
	VMULPD       (R12)(R9*1), Y4, Y7
	VADDPD       Y7, Y2, Y2
	VMULPD       (BX)(R9*1), Y4, Y8
	VADDPD       Y8, Y3, Y3
	ADDQ         $16, AX
	CMPQ         AX, DX
	JLT          rowsTap

rowsStore:
	VBROADCASTSD 16(R10), Y4
	VMULPD       Y4, Y0, Y0
	VMULPD       Y4, Y1, Y1
	VMULPD       Y4, Y2, Y2
	VMULPD       Y4, Y3, Y3
	VBLENDVPD    Y14, Y13, Y0, Y0
	VBLENDVPD    Y14, Y13, Y1, Y1
	VBLENDVPD    Y14, Y13, Y2, Y2
	VBLENDVPD    Y14, Y13, Y3, Y3
	VMOVUPD      Y0, (DI)
	VMOVUPD      Y1, (DI)(R8*1)
	LEAQ         (DI)(R8*2), R9
	VMOVUPD      Y2, (R9)
	VMOVUPD      Y3, (R9)(R8*1)
	ADDQ         $32, DI
	ADDQ         $24, R10
	DECQ         CX
	JNZ          rowsColumn
	VZEROUPPER
	RET

// COLUMN_PIXEL 按 scaleY_RGBA_Src 把累加结果 acc 夹到 alpha 以内、乘 invTotalWeight 后经 ftou 取高 8 位，四个分量的 int32 写到 out
#define COLUMN_PIXEL(acc, out) \
	VPERMPD     $0xff, acc, Y9; VCMPPD $0x1e, Y9, acc, Y10; VBLENDVPD Y10, Y9, acc, acc; \
	VMULPD      Y15, acc, acc; VMULPD Y14, acc, acc; VADDPD Y13, acc, acc; \
	VCVTTPD2DQY acc, out; VPMAXSD X11, out, out; VPMINSD X12, out, out; VPSRLD $8, out, out

// func scaleColumn(dst *byte, rows **float64, weights *float64, taps, n int, invTotalWeight float64)
TEXT ·scaleColumn(SB), NOSPLIT, $0-48
	MOVQ         dst+0(FP), DI
	MOVQ         rows+8(FP), SI
	MOVQ         weights+16(FP), R8
	MOVQ         taps+24(FP), R9
	MOVQ         n+32(FP), CX
	VBROADCASTSD invTotalWeight+40(FP), Y15
	VBROADCASTSD f65535<>(SB), Y14
	VBROADCASTSD half<>(SB), Y13
	VMOVDQU      k65535i<>(SB), X12
	VPXOR        X11, X11, X11
	XORQ         R10, R10

columnQuad:
	CMPQ   CX, $4
	JLT    columnSingle
	VXORPD Y0, Y0, Y0
	VXORPD Y1, Y1, Y1
	VXORPD Y2, Y2, Y2
	VXORPD Y3, Y3, Y3
	XORQ   AX, AX

columnQuadTap:
	MOVQ         (SI)(AX*8), BX
	VBROADCASTSD (R8)(AX*8), Y4
	ADDQ         R10, BX
	VMULPD       (BX), Y4, Y5
	VADDPD       Y5, Y0, Y0
	VMULPD       32(BX), Y4, Y6
	VADDPD       Y6, Y1, Y1
	VMULPD       64(BX), Y4, Y7
	VADDPD       Y7, Y2, Y2
	VMULPD       96(BX), Y4, Y8
	VADDPD       Y8, Y3, Y3
	INCQ         AX
	CMPQ         AX, R9
	JLT          columnQuadTap
	COLUMN_PIXEL(Y0, X0)
	COLUMN_PIXEL(Y1, X1)
	COLUMN_PIXEL(Y2, X2)
	COLUMN_PIXEL(Y3, X3)
	VPACKUSDW    X1, X0, X0
	VPACKUSDW    X3, X2, X2
	VPACKUSWB    X2, X0, X0
	VMOVDQU      X0, (DI)
	ADDQ         $16, DI
	ADDQ         $128, R10
	SUBQ         $4, CX
	JMP          columnQuad

columnSingle:
	TESTQ  CX, CX
	JZ     columnDone
	VXORPD Y0, Y0, Y0
	XORQ   AX, AX

columnSingleTap:
	MOVQ         (SI)(AX*8), BX
	VBROADCASTSD (R8)(AX*8), Y4
	VMULPD       (BX)(R10*1), Y4, Y5
	VADDPD       Y5, Y0, Y0
	INCQ         AX
	CMPQ         AX, R9
	JLT          columnSingleTap
	COLUMN_PIXEL(Y0, X0)
	VPACKUSDW    X0, X0, X0
	VPACKUSWB    X0, X0, X0
	VMOVD        X0, (DI)
	ADDQ         $4, DI
	ADDQ         $32, R10
	DECQ         CX
	JMP          columnSingle

columnDone:
	VZEROUPPER
	RET
