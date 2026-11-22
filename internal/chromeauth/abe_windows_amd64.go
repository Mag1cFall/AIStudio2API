//go:build windows && amd64

package chromeauth

import (
	"bytes"
	_ "embed"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"
	"unicode/utf16"
	"unsafe"

	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/registry"
)

// Supported 表示本平台能从 Chrome 导入账户
const Supported = true

const (
	appBoundPrefix      = "APPB"
	abeInputEnv         = "AISTUDIO2API_ABE_INPUT"
	abeOutputEnv        = "AISTUDIO2API_ABE_OUTPUT"
	abeHelperDLLName    = "abe_helper_amd64.dll"
	abeKeyFileName      = "abe_key.bin"
	abePollInterval     = 100 * time.Millisecond
	abeInjectionTimeout = 30 * time.Second
)

const (
	createSuspended     = 0x00000004
	startfUseShowWindow = 0x00000001
	swHide              = 0
	memCommit           = 0x00001000
	memReserve          = 0x00002000
	pageReadwrite       = 0x04
)

var (
	kernel32                   = windows.NewLazySystemDLL("kernel32.dll")
	procVirtualAllocEx         = kernel32.NewProc("VirtualAllocEx")
	procWriteProcessMemory     = kernel32.NewProc("WriteProcessMemory")
	procCreateRemoteThread     = kernel32.NewProc("CreateRemoteThread")
	procLoadLibraryW           = kernel32.NewProc("LoadLibraryW")
	versionDLL                 = windows.NewLazySystemDLL("version.dll")
	procGetFileVersionInfoSize = versionDLL.NewProc("GetFileVersionInfoSizeW")
	procGetFileVersionInfo     = versionDLL.NewProc("GetFileVersionInfoW")
	procVerQueryValue          = versionDLL.NewProc("VerQueryValueW")
)

//go:embed native/abe_helper_amd64.bin
var abeHelperDLL []byte

// retrieveV20Key 读取 Chrome App-Bound v20 主密钥
func retrieveV20Key(chromeRoot string) ([]byte, error) {
	encrypted, err := readAppBoundCiphertext(chromeRoot)
	if err != nil {
		return nil, err
	}
	chromePath, err := findChromeExecutable()
	if err != nil {
		return nil, err
	}
	return decryptAppBoundCiphertext(chromePath, encrypted)
}

// readAppBoundCiphertext 从 Local State 提取 APPB 密文
func readAppBoundCiphertext(chromeRoot string) ([]byte, error) {
	data, err := os.ReadFile(filepath.Join(chromeRoot, "Local State"))
	if err != nil {
		return nil, fmt.Errorf("读取 Chrome Local State: %w", err)
	}
	var state struct {
		OSCrypt struct {
			AppBoundEncryptedKey string `json:"app_bound_encrypted_key"`
		} `json:"os_crypt"`
	}
	if err := json.Unmarshal(data, &state); err != nil {
		return nil, fmt.Errorf("解析 Chrome Local State: %w", err)
	}
	raw := strings.TrimSpace(state.OSCrypt.AppBoundEncryptedKey)
	if raw == "" {
		return nil, fmt.Errorf("Chrome Local State 缺少 app_bound_encrypted_key")
	}
	decoded, err := base64.StdEncoding.DecodeString(raw)
	if err != nil {
		return nil, fmt.Errorf("解析 app_bound_encrypted_key: %w", err)
	}
	if len(decoded) <= len(appBoundPrefix) || string(decoded[:len(appBoundPrefix)]) != appBoundPrefix {
		return nil, fmt.Errorf("app_bound_encrypted_key 缺少 APPB 前缀")
	}
	return decoded[len(appBoundPrefix):], nil
}

// findChromeExecutable 定位官方稳定版 Google Chrome 可执行文件
func findChromeExecutable() (string, error) {
	return findChromeExecutableWith(chromeExecutableFromRegistry, chromeExecutableFallbacks())
}

// findChromeExecutableWith 依次从 HKCU、HKLM 注册表与常见安装目录查找官方 Google Chrome 可执行文件
func findChromeExecutableWith(fromRegistry func(registry.Key) (string, error), fallbacks []string) (string, error) {
	for _, root := range []registry.Key{registry.CURRENT_USER, registry.LOCAL_MACHINE} {
		path, err := fromRegistry(root)
		if err == nil && isGoogleChromeExecutable(path) {
			return path, nil
		}
	}
	for _, path := range fallbacks {
		if isGoogleChromeExecutable(path) {
			return path, nil
		}
	}
	return "", fmt.Errorf("找不到官方 Google Chrome chrome.exe")
}

// isGoogleChromeExecutable 通过 Windows PE 版本信息核对可执行文件为官方 Google Chrome
func isGoogleChromeExecutable(path string) bool {
	if fileInfo, err := os.Stat(path); err != nil || fileInfo.IsDir() {
		return false
	}
	pathUTF16, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return false
	}
	var handle uint32
	size, _, _ := procGetFileVersionInfoSize.Call(
		uintptr(unsafe.Pointer(pathUTF16)),
		uintptr(unsafe.Pointer(&handle)),
	)
	if size == 0 {
		return false
	}
	block := make([]byte, size)
	ok, _, _ := procGetFileVersionInfo.Call(
		uintptr(unsafe.Pointer(pathUTF16)),
		0,
		size,
		uintptr(unsafe.Pointer(&block[0])),
	)
	if ok == 0 {
		return false
	}
	for _, langCodePage := range versionTranslations(block) {
		company := strings.TrimSpace(versionStringValue(block, fmt.Sprintf(`\StringFileInfo\%s\CompanyName`, langCodePage)))
		product := strings.TrimSpace(versionStringValue(block, fmt.Sprintf(`\StringFileInfo\%s\ProductName`, langCodePage)))
		if (strings.EqualFold(company, "Google LLC") || strings.EqualFold(company, "Google Inc.")) &&
			strings.EqualFold(product, "Google Chrome") {
			return true
		}
	}
	return false
}

// versionTranslations 读取版本资源中的语言与代码页列表，缺失时回退到英文 Unicode 表
func versionTranslations(block []byte) []string {
	subBlock, err := windows.UTF16PtrFromString(`\VarFileInfo\Translation`)
	if err != nil {
		return []string{"040904b0"}
	}
	var ptr uintptr
	var length uint32
	ok, _, _ := procVerQueryValue.Call(
		uintptr(unsafe.Pointer(&block[0])),
		uintptr(unsafe.Pointer(subBlock)),
		uintptr(unsafe.Pointer(&ptr)),
		uintptr(unsafe.Pointer(&length)),
	)
	defer runtime.KeepAlive(block)
	base := uintptr(unsafe.Pointer(&block[0]))
	if ok == 0 || ptr < base || length < 4 {
		return []string{"040904b0"}
	}
	offset := int(ptr - base)
	size := int(length)
	if offset < 0 || offset+size > len(block) {
		return []string{"040904b0"}
	}
	count := size / 4
	raw := block[offset : offset+count*4]
	translations := make([]string, 0, count)
	for i := 0; i < count; i++ {
		lang := binary.LittleEndian.Uint16(raw[i*4 : i*4+2])
		codePage := binary.LittleEndian.Uint16(raw[i*4+2 : i*4+4])
		translations = append(translations, fmt.Sprintf("%04x%04x", lang, codePage))
	}
	return translations
}

// versionStringValue 从 PE 版本资源块中读取指定字符串项
func versionStringValue(block []byte, path string) string {
	subBlock, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return ""
	}
	var ptr uintptr
	var length uint32
	ok, _, _ := procVerQueryValue.Call(
		uintptr(unsafe.Pointer(&block[0])),
		uintptr(unsafe.Pointer(subBlock)),
		uintptr(unsafe.Pointer(&ptr)),
		uintptr(unsafe.Pointer(&length)),
	)
	defer runtime.KeepAlive(block)
	base := uintptr(unsafe.Pointer(&block[0]))
	if ok == 0 || ptr < base || length == 0 {
		return ""
	}
	offset := int(ptr - base)
	byteLen := int(length) * 2
	if offset < 0 || offset+byteLen > len(block) {
		return ""
	}
	units := make([]uint16, int(length))
	raw := block[offset : offset+byteLen]
	for i := range units {
		units[i] = binary.LittleEndian.Uint16(raw[i*2 : i*2+2])
	}
	return windows.UTF16ToString(units)
}

// chromeExecutableFromRegistry 读取 Windows App Paths
func chromeExecutableFromRegistry(root registry.Key) (string, error) {
	key, err := registry.OpenKey(root, `SOFTWARE\Microsoft\Windows\CurrentVersion\App Paths\chrome.exe`, registry.QUERY_VALUE)
	if err != nil {
		return "", err
	}
	defer key.Close()
	value, _, err := key.GetStringValue("")
	if err != nil {
		return "", err
	}
	value = strings.Trim(strings.TrimSpace(value), `"`)
	if value == "" {
		return "", fmt.Errorf("Chrome App Paths 为空")
	}
	if fileInfo, err := os.Stat(value); err != nil || fileInfo.IsDir() {
		return "", fmt.Errorf("Chrome App Paths 不可用")
	}
	return value, nil
}

// chromeExecutableFallbacks 返回常见安装位置
func chromeExecutableFallbacks() []string {
	paths := make([]string, 0, 3)
	if programFiles := strings.TrimSpace(os.Getenv("ProgramFiles")); programFiles != "" {
		paths = append(paths, filepath.Join(programFiles, "Google", "Chrome", "Application", "chrome.exe"))
	}
	if programFilesX86 := strings.TrimSpace(os.Getenv("ProgramFiles(x86)")); programFilesX86 != "" {
		paths = append(paths, filepath.Join(programFilesX86, "Google", "Chrome", "Application", "chrome.exe"))
	}
	if localAppData := strings.TrimSpace(os.Getenv("LOCALAPPDATA")); localAppData != "" {
		paths = append(paths, filepath.Join(localAppData, "Google", "Chrome", "Application", "chrome.exe"))
	}
	return paths
}

// decryptAppBoundCiphertext 在独立 Chrome 进程内解密主密钥
func decryptAppBoundCiphertext(chromePath string, encrypted []byte) ([]byte, error) {
	tempDir, err := os.MkdirTemp("", "aistudio2api-abe-*")
	if err != nil {
		return nil, fmt.Errorf("创建 ABE 临时目录: %w", err)
	}
	defer os.RemoveAll(tempDir)

	helperPath := filepath.Join(tempDir, abeHelperDLLName)
	outputPath := filepath.Join(tempDir, abeKeyFileName)
	if err := os.WriteFile(helperPath, abeHelperDLL, 0o600); err != nil {
		return nil, fmt.Errorf("写入 ABE helper: %w", err)
	}

	process, thread, err := startHiddenChrome(chromePath, filepath.Join(tempDir, "profile"), []string{
		abeInputEnv + "=" + base64.StdEncoding.EncodeToString(encrypted),
		abeOutputEnv + "=" + outputPath,
	})
	if err != nil {
		return nil, err
	}
	job, err := createKillOnCloseJob(process)
	if err != nil {
		_ = windows.TerminateProcess(process, 0)
		windows.CloseHandle(thread)
		windows.CloseHandle(process)
		return nil, err
	}
	defer closeChromeProcess(job, process, thread)

	if _, err := windows.ResumeThread(thread); err != nil {
		return nil, fmt.Errorf("启动临时 Chrome: %w", err)
	}
	if err := injectDLL(process, helperPath); err != nil {
		return nil, err
	}
	deadline := time.Now().Add(abeInjectionTimeout)
	for time.Now().Before(deadline) {
		data, err := os.ReadFile(outputPath)
		if err == nil {
			if bytes.HasPrefix(data, []byte("ERR ")) || len(data) != 32 {
				return nil, fmt.Errorf("Chrome ABE helper 返回异常: %s", strings.TrimSpace(string(data)))
			}
			return data, nil
		}
		time.Sleep(abePollInterval)
	}
	return nil, fmt.Errorf("Chrome ABE helper 超时")
}

// createKillOnCloseJob 将临时 Chrome 进程树绑定到独立 Job
func createKillOnCloseJob(process windows.Handle) (windows.Handle, error) {
	job, err := windows.CreateJobObject(nil, nil)
	if err != nil {
		return 0, fmt.Errorf("创建 Chrome Job: %w", err)
	}
	info := windows.JOBOBJECT_EXTENDED_LIMIT_INFORMATION{}
	info.BasicLimitInformation.LimitFlags = windows.JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE
	if _, err := windows.SetInformationJobObject(
		job,
		windows.JobObjectExtendedLimitInformation,
		uintptr(unsafe.Pointer(&info)),
		uint32(unsafe.Sizeof(info)),
	); err != nil {
		windows.CloseHandle(job)
		return 0, fmt.Errorf("配置 Chrome Job: %w", err)
	}
	if err := windows.AssignProcessToJobObject(job, process); err != nil {
		windows.CloseHandle(job)
		return 0, fmt.Errorf("加入 Chrome Job: %w", err)
	}
	return job, nil
}

// closeChromeProcess 终止临时 Chrome 进程树并释放句柄
func closeChromeProcess(job windows.Handle, process windows.Handle, thread windows.Handle) {
	_ = windows.TerminateJobObject(job, 0)
	_, _ = windows.WaitForSingleObject(process, uint32((5 * time.Second).Milliseconds()))
	windows.CloseHandle(job)
	windows.CloseHandle(thread)
	windows.CloseHandle(process)
}

// startHiddenChrome 创建隐藏的独立临时 Chrome，env 追加在本进程环境变量之后
func startHiddenChrome(chromePath string, profileDir string, env []string) (windows.Handle, windows.Handle, error) {
	commandLine := strings.Join([]string{
		quoteWindowsArg(chromePath),
		"--user-data-dir=" + quoteWindowsArg(profileDir),
		"--no-first-run",
		"--no-default-browser-check",
		"--disable-background-networking",
		"--disable-component-update",
		"--disable-default-apps",
		"--disable-extensions",
		"--disable-sync",
		"--disable-gpu",
		"--headless=new",
		"about:blank",
	}, " ")
	commandLineUTF16, err := windows.UTF16PtrFromString(commandLine)
	if err != nil {
		return 0, 0, fmt.Errorf("编码 Chrome 命令行: %w", err)
	}
	chromePathUTF16, err := windows.UTF16PtrFromString(chromePath)
	if err != nil {
		return 0, 0, fmt.Errorf("编码 Chrome 路径: %w", err)
	}
	startupInfo := windows.StartupInfo{
		Flags:      startfUseShowWindow,
		ShowWindow: swHide,
	}
	var environment []uint16
	for _, entry := range append(os.Environ(), env...) {
		environment = append(append(environment, utf16.Encode([]rune(entry))...), 0)
	}
	environment = append(environment, 0)
	var processInfo windows.ProcessInformation
	err = windows.CreateProcess(
		chromePathUTF16,
		commandLineUTF16,
		nil,
		nil,
		false,
		createSuspended|windows.CREATE_UNICODE_ENVIRONMENT,
		&environment[0],
		nil,
		&startupInfo,
		&processInfo,
	)
	if err != nil {
		return 0, 0, fmt.Errorf("启动独立临时 Chrome: %w", err)
	}
	return processInfo.Process, processInfo.Thread, nil
}

// injectDLL 通过 LoadLibraryW 载入 helper
func injectDLL(process windows.Handle, dllPath string) error {
	encodedPath, err := windows.UTF16FromString(dllPath)
	if err != nil {
		return fmt.Errorf("编码 ABE helper 路径: %w", err)
	}
	size := uintptr(len(encodedPath) * 2)
	remotePath, _, callErr := procVirtualAllocEx.Call(
		uintptr(process), 0, size, memCommit|memReserve, pageReadwrite,
	)
	if remotePath == 0 {
		return fmt.Errorf("VirtualAllocEx 失败: %w", callErr)
	}
	var written uintptr
	ok, _, callErr := procWriteProcessMemory.Call(
		uintptr(process),
		remotePath,
		uintptr(unsafe.Pointer(&encodedPath[0])),
		size,
		uintptr(unsafe.Pointer(&written)),
	)
	if ok == 0 || written != size {
		return fmt.Errorf("WriteProcessMemory 失败: %w", callErr)
	}
	thread, _, callErr := procCreateRemoteThread.Call(
		uintptr(process),
		0,
		0,
		procLoadLibraryW.Addr(),
		remotePath,
		0,
		0,
	)
	if thread == 0 {
		return fmt.Errorf("CreateRemoteThread 失败: %w", callErr)
	}
	threadHandle := windows.Handle(thread)
	defer windows.CloseHandle(threadHandle)
	if _, err := windows.WaitForSingleObject(threadHandle, uint32((10 * time.Second).Milliseconds())); err != nil {
		return fmt.Errorf("等待 ABE helper 注入: %w", err)
	}
	return nil
}

// quoteWindowsArg 包裹 Windows 命令行参数
func quoteWindowsArg(value string) string {
	return `"` + strings.ReplaceAll(value, `"`, `\"`) + `"`
}
