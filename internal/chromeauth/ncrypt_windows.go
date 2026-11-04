//go:build windows

package chromeauth

import (
	"crypto/sha256"
	"fmt"
	"runtime"
	"sync"
	"unsafe"

	"golang.org/x/sys/windows"
)

const ncryptSilentFlag = 0x40

var (
	ncryptDLL                 = windows.NewLazySystemDLL("ncrypt.dll")
	ncryptOpenStorageProvider = ncryptDLL.NewProc("NCryptOpenStorageProvider")
	ncryptImportKey           = ncryptDLL.NewProc("NCryptImportKey")
	ncryptExportKey           = ncryptDLL.NewProc("NCryptExportKey")
	ncryptSignHash            = ncryptDLL.NewProc("NCryptSignHash")
	ncryptFreeObject          = ncryptDLL.NewProc("NCryptFreeObject")
)

// ncryptMu 串行化对 Microsoft Platform Crypto Provider 的调用，TPM 同时执行多条命令时返回 NTE_DEVICE_NOT_READY 或 TPM_RC_CANCELED
var ncryptMu sync.Mutex

type ncryptDeviceKey struct {
	provider uintptr
	key      uintptr
}

func openDeviceBindingKey(wrappedKey []byte) (deviceBindingKey, error) {
	ncryptMu.Lock()
	defer ncryptMu.Unlock()
	providerName, err := windows.UTF16PtrFromString("Microsoft Platform Crypto Provider")
	if err != nil {
		return nil, fmt.Errorf("编码 NCrypt Provider 名称: %w", err)
	}
	var provider uintptr
	status, _, _ := ncryptOpenStorageProvider.Call(
		uintptr(unsafe.Pointer(&provider)), uintptr(unsafe.Pointer(providerName)), 0,
	)
	if uint32(status) != 0 {
		return nil, fmt.Errorf("NCryptOpenStorageProvider 返回 %d", int32(status))
	}
	key := &ncryptDeviceKey{provider: provider}
	blobType, err := windows.UTF16PtrFromString("OpaqueKeyBlob")
	if err != nil {
		key.free()
		return nil, fmt.Errorf("编码 NCrypt Blob 类型: %w", err)
	}
	status, _, _ = ncryptImportKey.Call(
		key.provider,
		0,
		uintptr(unsafe.Pointer(blobType)),
		0,
		uintptr(unsafe.Pointer(&key.key)),
		bytePointer(wrappedKey),
		uintptr(len(wrappedKey)),
		ncryptSilentFlag,
	)
	runtime.KeepAlive(wrappedKey)
	if uint32(status) != 0 {
		key.free()
		return nil, fmt.Errorf("NCryptImportKey 返回 %d", int32(status))
	}
	return key, nil
}

func (key *ncryptDeviceKey) PublicKey() ([]byte, error) {
	ncryptMu.Lock()
	defer ncryptMu.Unlock()
	blobType, err := windows.UTF16PtrFromString("ECCPUBLICBLOB")
	if err != nil {
		return nil, fmt.Errorf("编码 NCrypt 公钥类型: %w", err)
	}
	var size uint32
	status, _, _ := ncryptExportKey.Call(
		key.key, 0, uintptr(unsafe.Pointer(blobType)), 0, 0, 0,
		uintptr(unsafe.Pointer(&size)), 0,
	)
	if uint32(status) != 0 {
		return nil, fmt.Errorf("NCryptExportKey 查询返回 %d", int32(status))
	}
	output := make([]byte, size)
	status, _, _ = ncryptExportKey.Call(
		key.key, 0, uintptr(unsafe.Pointer(blobType)), 0, bytePointer(output), uintptr(len(output)),
		uintptr(unsafe.Pointer(&size)), 0,
	)
	runtime.KeepAlive(output)
	if uint32(status) != 0 {
		return nil, fmt.Errorf("NCryptExportKey 返回 %d", int32(status))
	}
	return parsePublicKeyBlob(output[:size])
}

func (key *ncryptDeviceKey) SignSHA256(value []byte) ([]byte, error) {
	ncryptMu.Lock()
	defer ncryptMu.Unlock()
	digest := sha256.Sum256(value)
	var size uint32
	status, _, _ := ncryptSignHash.Call(
		key.key, 0, uintptr(unsafe.Pointer(&digest[0])), uintptr(len(digest)),
		0, 0, uintptr(unsafe.Pointer(&size)), ncryptSilentFlag,
	)
	if uint32(status) != 0 {
		return nil, fmt.Errorf("NCryptSignHash 查询返回 %d", int32(status))
	}
	output := make([]byte, size)
	status, _, _ = ncryptSignHash.Call(
		key.key, 0, uintptr(unsafe.Pointer(&digest[0])), uintptr(len(digest)),
		bytePointer(output), uintptr(len(output)), uintptr(unsafe.Pointer(&size)), ncryptSilentFlag,
	)
	runtime.KeepAlive(output)
	if uint32(status) != 0 {
		return nil, fmt.Errorf("NCryptSignHash 返回 %d", int32(status))
	}
	return output[:size], nil
}

func (key *ncryptDeviceKey) Close() {
	ncryptMu.Lock()
	defer ncryptMu.Unlock()
	key.free()
}

// free 释放密钥与 Provider 句柄，调用方持有 ncryptMu
func (key *ncryptDeviceKey) free() {
	if key.key != 0 {
		ncryptFreeObject.Call(key.key)
		key.key = 0
	}
	if key.provider != 0 {
		ncryptFreeObject.Call(key.provider)
		key.provider = 0
	}
}

func bytePointer(value []byte) uintptr {
	if len(value) == 0 {
		return 0
	}
	return uintptr(unsafe.Pointer(&value[0]))
}
