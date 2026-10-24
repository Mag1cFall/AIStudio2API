//go:build desktop

package desktop

import (
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"os/exec"

	"golang.org/x/sys/windows"
)

// webview2BootstrapperURL 为微软 WebView2 Evergreen 引导程序的下载地址
const webview2BootstrapperURL = "https://go.microsoft.com/fwlink/p/?LinkId=2124703"

// idOK 为 MessageBox 中“确定”按钮的返回值
const idOK = 1

// installWebView2 询问用户后下载 WebView2 Evergreen 引导程序并按当前用户权限静默安装，安装完成时返回 true
func installWebView2() bool {
	if messageBox("AIStudio2API needs the Microsoft Edge WebView2 Runtime to open its window. Press OK to download and install it from Microsoft; the installation runs silently and takes a moment. Press Cancel to use the default browser instead.", windows.MB_OKCANCEL|windows.MB_ICONINFORMATION) != idOK {
		return false
	}
	if err := runWebView2Bootstrapper(); err != nil {
		slog.Warn("WebView2 运行时安装失败", "error", err)
		messageBox(err.Error(), windows.MB_OK|windows.MB_ICONERROR)
		return false
	}
	return true
}

// runWebView2Bootstrapper 下载 WebView2 Evergreen 引导程序到临时文件并以 /silent /install 运行
func runWebView2Bootstrapper() error {
	response, err := http.Get(webview2BootstrapperURL)
	if err != nil {
		return fmt.Errorf("下载 WebView2 引导程序: %w", err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return fmt.Errorf("下载 WebView2 引导程序返回 HTTP %d", response.StatusCode)
	}
	file, err := os.CreateTemp("", "MicrosoftEdgeWebview2Setup-*.exe")
	if err != nil {
		return err
	}
	defer os.Remove(file.Name())
	_, err = io.Copy(file, response.Body)
	if closeErr := file.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		return fmt.Errorf("保存 WebView2 引导程序: %w", err)
	}
	if err := exec.Command(file.Name(), "/silent", "/install").Run(); err != nil {
		return fmt.Errorf("运行 WebView2 引导程序: %w", err)
	}
	return nil
}

// messageBox 显示标题为 AIStudio2API 的系统消息框并返回用户按下的按钮
func messageBox(text string, style uint32) int32 {
	title, _ := windows.UTF16PtrFromString("AIStudio2API")
	body, _ := windows.UTF16PtrFromString(text)
	choice, _ := windows.MessageBox(0, body, title, style)
	return choice
}
