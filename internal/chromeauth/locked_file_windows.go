//go:build windows

package chromeauth

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"unsafe"

	"golang.org/x/sys/windows"
)

var (
	procGetFinalPathNameByHandle = kernel32.NewProc("GetFinalPathNameByHandleW")
	procGetFileType              = kernel32.NewProc("GetFileType")
	procSetFilePointerEx         = kernel32.NewProc("SetFilePointerEx")
	procGetFileSizeEx            = kernel32.NewProc("GetFileSizeEx")
	errBrowserRunning            = errors.New("browser is running and locking database")
)

func isSharingViolation(err error) bool {
	if err == nil {
		return false
	}
	var errno syscall.Errno
	if errors.As(err, &errno) && (errno == 32 || errno == windows.ERROR_SHARING_VIOLATION || errno == windows.ERROR_LOCK_VIOLATION) {
		return true
	}
	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "used by another process") || strings.Contains(msg, "sharing violation") || strings.Contains(msg, "lock violation")
}

// browserExeName 返回浏览器对应的进程可执行文件名
func browserExeName(browser string) string {
	switch strings.ToLower(strings.TrimSpace(browser)) {
	case BrowserBrave:
		return "brave.exe"
	case BrowserEdge:
		return "msedge.exe"
	case BrowserChrome:
		return "chrome.exe"
	default:
		return ""
	}
}

// findProcessesByName 查找指定名称的所有进程 PID
func findProcessesByName(exeName string) ([]uint32, error) {
	snap, err := windows.CreateToolhelp32Snapshot(windows.TH32CS_SNAPPROCESS, 0)
	if err != nil {
		return nil, err
	}
	defer windows.CloseHandle(snap)

	var entry windows.ProcessEntry32
	entry.Size = uint32(unsafe.Sizeof(entry))
	if err := windows.Process32First(snap, &entry); err != nil {
		return nil, err
	}

	var pids []uint32
	exeLower := strings.ToLower(strings.TrimSpace(exeName))
	for {
		name := windows.UTF16ToString(entry.ExeFile[:])
		if strings.ToLower(name) == exeLower {
			pids = append(pids, entry.ProcessID)
		}
		if err := windows.Process32Next(snap, &entry); err != nil {
			break
		}
	}
	return pids, nil
}

func getFinalPath(h windows.Handle) (string, error) {
	buf := make([]uint16, 1024)
	ret, _, err := procGetFinalPathNameByHandle.Call(
		uintptr(h),
		uintptr(unsafe.Pointer(&buf[0])),
		uintptr(len(buf)),
		0,
	)
	if ret == 0 {
		return "", err
	}
	return windows.UTF16ToString(buf[:ret]), nil
}

// readLockedFileByHandleDuplication 在目标进程中扫描打开了 targetPath 的 Handle，并通过 DuplicateHandle 突破文件锁定读取数据
func readLockedFileByHandleDuplication(pid uint32, targetPath string) ([]byte, error) {
	hProc, err := windows.OpenProcess(windows.PROCESS_DUP_HANDLE|windows.PROCESS_QUERY_INFORMATION, false, pid)
	if err != nil {
		return nil, fmt.Errorf("OpenProcess(%d): %w", pid, err)
	}
	defer windows.CloseHandle(hProc)

	currentProc := windows.CurrentProcess()
	targetPathLower := strings.ToLower(filepath.Clean(targetPath))

	for h := uintptr(4); h < 0xFFFF; h += 4 {
		var dupHandle windows.Handle
		err := windows.DuplicateHandle(
			hProc,
			windows.Handle(h),
			currentProc,
			&dupHandle,
			windows.GENERIC_READ,
			false,
			0,
		)
		if err != nil {
			err = windows.DuplicateHandle(
				hProc,
				windows.Handle(h),
				currentProc,
				&dupHandle,
				0,
				false,
				windows.DUPLICATE_SAME_ACCESS,
			)
			if err != nil {
				continue
			}
		}

		ft, _, _ := procGetFileType.Call(uintptr(dupHandle))
		if ft != 1 { // 1 = FILE_TYPE_DISK
			windows.CloseHandle(dupHandle)
			continue
		}

		path, err := getFinalPath(dupHandle)
		if err != nil {
			windows.CloseHandle(dupHandle)
			continue
		}

		normPath := strings.ToLower(strings.TrimPrefix(path, `\\?\`))
		if strings.HasSuffix(normPath, targetPathLower) || normPath == targetPathLower {
			var size int64
			ret, _, err := procGetFileSizeEx.Call(uintptr(dupHandle), uintptr(unsafe.Pointer(&size)))
			if ret == 0 {
				windows.CloseHandle(dupHandle)
				return nil, fmt.Errorf("GetFileSizeEx: %w", err)
			}
			data := make([]byte, size)
			var bytesRead uint32

			var newPos int64
			procSetFilePointerEx.Call(uintptr(dupHandle), 0, uintptr(unsafe.Pointer(&newPos)), 0)

			err = windows.ReadFile(dupHandle, data, &bytesRead, nil)
			windows.CloseHandle(dupHandle)
			if err != nil {
				return nil, fmt.Errorf("ReadFile via duplicated handle: %w", err)
			}
			return data[:bytesRead], nil
		}
		windows.CloseHandle(dupHandle)
	}
	return nil, fmt.Errorf("handle not found in process %d", pid)
}

// copyLockedFile 尝试复制文件；若发生 Sharing Violation 锁定，自动通过 Handle Duplication 突破锁定提取数据
func copyLockedFile(src string, dst string, exeNames ...string) error {
	err := copyFile(src, dst)
	if err == nil {
		return nil
	}
	if !isSharingViolation(err) {
		return err
	}

	if len(exeNames) == 0 || (len(exeNames) == 1 && exeNames[0] == "") {
		exeNames = []string{"brave.exe", "chrome.exe", "msedge.exe"}
	}

	for _, exe := range exeNames {
		if exe == "" {
			continue
		}
		pids, pErr := findProcessesByName(exe)
		if pErr != nil || len(pids) == 0 {
			continue
		}
		for _, pid := range pids {
			data, dErr := readLockedFileByHandleDuplication(pid, src)
			if dErr == nil && len(data) > 0 {
				return os.WriteFile(dst, data, 0o600)
			}
		}
	}
	return err
}
