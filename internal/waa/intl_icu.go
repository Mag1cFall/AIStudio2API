package waa

import (
	"context"
	"encoding/binary"
	"fmt"
	"strings"
	"sync"
	"unicode/utf16"

	"github.com/tetratelabs/wazero"
	"github.com/tetratelabs/wazero/api"
	"github.com/tetratelabs/wazero/imports/wasi_snapshot_preview1"
)

// intlICU 是 Firefox 152 的 ICU 与 mozilla::intl 组件编译成的 WebAssembly 模块，进程内各 Realm 共享
type intlICU struct {
	mu     sync.Mutex
	camou  [3]string
	module api.Module
	input  api.Function
	output api.Function
	entry  api.Function
}

// readIntlGzip 解压 intl 目录中的 gzip 二进制文件
func readIntlGzip(name string) ([]byte, error) {
	raw, err := intlFiles.ReadFile("intl/" + name + ".gz")
	if err != nil {
		return nil, err
	}
	return gunzip(raw)
}

// loadIntlICU 编译 ICU 模块并载入 Firefox 的 ICU 数据
var loadIntlICU = sync.OnceValues(func() (*intlICU, error) {
	ctx := context.Background()
	code, err := readIntlGzip("icu/intl.wasm")
	if err != nil {
		return nil, fmt.Errorf("读取 ICU 模块: %w", err)
	}
	data, err := readIntlGzip("icu/icudt78l.dat")
	if err != nil {
		return nil, fmt.Errorf("读取 ICU 数据: %w", err)
	}
	runtime := wazero.NewRuntime(ctx)
	wasi_snapshot_preview1.MustInstantiate(ctx, runtime)
	module, err := runtime.InstantiateWithConfig(ctx, code, wazero.NewModuleConfig().WithStartFunctions("_initialize"))
	if err != nil {
		return nil, fmt.Errorf("实例化 ICU 模块: %w", err)
	}
	results, err := module.ExportedFunction("intl_alloc").Call(ctx, uint64(len(data)))
	if err != nil {
		return nil, fmt.Errorf("分配 ICU 数据内存: %w", err)
	}
	if !module.Memory().Write(uint32(results[0]), data) {
		return nil, fmt.Errorf("写入 ICU 数据越界")
	}
	status, err := module.ExportedFunction("intl_init").Call(ctx, results[0])
	if err != nil || int32(status[0]) > 0 {
		return nil, fmt.Errorf("初始化 ICU 数据: %d %v", int32(status[0]), err)
	}
	return &intlICU{
		module: module,
		input:  module.ExportedFunction("intl_input"),
		output: module.ExportedFunction("intl_output"),
		entry:  module.ExportedFunction("intl_call"),
	}, nil
})

// camouLocale 返回 Camoufox 按账户语言写入 MaskConfig 的 locale:language、locale:region 与 navigator.language
func camouLocale(locale string) [3]string {
	language, region, found := strings.Cut(locale, "-")
	if !found {
		return [3]string{strings.ToLower(language), "", locale}
	}
	return [3]string{strings.ToLower(language), strings.ToUpper(region), locale}
}

// invoke 在账户的 Camoufox 区域设置下以操作码和参数调用 ICU 模块，返回结果字符串，失败时返回负数错误码
func (icu *intlICU) invoke(camou [3]string, op int, args []string) ([]string, int32, error) {
	icu.mu.Lock()
	defer icu.mu.Unlock()
	if icu.camou != camou {
		if _, _, err := icu.call(0, camou[:]); err != nil {
			return nil, 0, err
		}
		icu.camou = camou
	}
	return icu.call(op, args)
}

// call 以操作码和参数调用 ICU 模块，调用方持有锁
func (icu *intlICU) call(op int, args []string) ([]string, int32, error) {
	units := make([]uint16, 0, 64)
	for _, arg := range args {
		encoded := utf16.Encode([]rune(arg))
		units = append(units, uint16(len(encoded)), uint16(len(encoded)>>16))
		units = append(units, encoded...)
	}
	raw := make([]byte, len(units)*2)
	for index, unit := range units {
		binary.LittleEndian.PutUint16(raw[index*2:], unit)
	}
	ctx := context.Background()
	pointer, err := icu.input.Call(ctx, uint64(len(units)))
	if err != nil {
		return nil, 0, err
	}
	memory := icu.module.Memory()
	memory.Write(uint32(pointer[0]), raw)
	length, err := icu.entry.Call(ctx, uint64(op))
	if err != nil {
		return nil, 0, err
	}
	if code := int32(length[0]); code < 0 {
		return nil, code, nil
	}
	pointer, err = icu.output.Call(ctx)
	if err != nil {
		return nil, 0, err
	}
	view, _ := memory.Read(uint32(pointer[0]), uint32(length[0])*2)
	output := make([]uint16, len(view)/2)
	for index := range output {
		output[index] = binary.LittleEndian.Uint16(view[index*2:])
	}
	var results []string
	for index := 0; index+2 <= len(output); {
		size := int(output[index]) | int(output[index+1])<<16
		results = append(results, string(utf16.Decode(output[index+2:index+2+size])))
		index += 2 + size
	}
	return results, 0, nil
}

// intlAvailable 是 SpiderMonkey 按 ICU 可用区域设置、去掉书写系统的语言地区组合与 en-GB 构成的可用区域设置集合
var intlAvailable = sync.OnceValues(func() (map[string]bool, error) {
	icu, err := loadIntlICU()
	if err != nil {
		return nil, err
	}
	names, _, err := icu.invoke([3]string{}, 15, nil)
	if err != nil {
		return nil, err
	}
	available := map[string]bool{"en-GB": true}
	for _, name := range names {
		parts := strings.Split(name, "_")
		available[strings.Join(parts, "-")] = true
		if len(parts) == 3 && len(parts[1]) == 4 {
			available[parts[0]+"-"+parts[2]] = true
		}
	}
	return available, nil
})
