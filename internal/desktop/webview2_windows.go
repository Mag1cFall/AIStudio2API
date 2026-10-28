//go:build desktop

package desktop

import (
	"context"
	"errors"
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

// webview2Cancelled 为用户在引导程序窗口中取消安装时引导程序的退出码
const webview2Cancelled = 0x80040810

// idOK 为 MessageBox 中“确定”按钮的返回值
const idOK = 1

// errWebView2Cancelled 表示用户在引导程序窗口中取消了安装
var errWebView2Cancelled = errors.New("用户取消了 WebView2 运行时安装")

// bootstrapper 为后台下载 WebView2 引导程序的结果
type bootstrapper struct {
	path string
	err  error
}

// installWebView2 显示询问框的同时在后台下载 WebView2 Evergreen 引导程序，用户确定后按当前用户权限安装，安装完成时返回 true
func installWebView2() bool {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	downloaded := make(chan bootstrapper, 1)
	go func() {
		path, err := downloadWebView2Bootstrapper(ctx)
		downloaded <- bootstrapper{path, err}
	}()
	accepted := messageBox("AIStudio2API needs the Microsoft Edge WebView2 Runtime to open its window. Press OK to download and install it from Microsoft; the Microsoft installer shows its progress. Press Cancel to use the default browser instead.", windows.MB_OKCANCEL|windows.MB_ICONINFORMATION) == idOK
	if !accepted {
		cancel()
	}
	result := <-downloaded
	if accepted && result.err != nil {
		slog.Warn("后台下载 WebView2 引导程序失败，重新下载", "error", result.err)
		result.path, result.err = downloadWebView2Bootstrapper(ctx)
	}
	if result.err == nil {
		defer os.Remove(result.path)
	}
	if !accepted {
		return false
	}
	err := result.err
	if err == nil {
		err = runWebView2Bootstrapper(result.path)
	}
	if err == nil {
		return true
	}
	if errors.Is(err, errWebView2Cancelled) {
		slog.Info("用户取消了 WebView2 运行时安装")
		return false
	}
	slog.Warn("WebView2 运行时安装失败", "error", err)
	messageBox("The Microsoft Edge WebView2 Runtime could not be installed. AIStudio2API will open its management page in the default browser and ask again the next time it starts.", windows.MB_OK|windows.MB_ICONERROR)
	return false
}

// downloadWebView2Bootstrapper 下载 WebView2 Evergreen 引导程序到临时文件并返回其路径
func downloadWebView2Bootstrapper(ctx context.Context) (string, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, webview2BootstrapperURL, nil)
	if err != nil {
		return "", err
	}
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		return "", fmt.Errorf("下载 WebView2 引导程序: %w", err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return "", fmt.Errorf("下载 WebView2 引导程序返回 HTTP %d", response.StatusCode)
	}
	file, err := os.CreateTemp("", "MicrosoftEdgeWebview2Setup-*.exe")
	if err != nil {
		return "", err
	}
	_, err = io.Copy(file, response.Body)
	if closeErr := file.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		os.Remove(file.Name())
		return "", fmt.Errorf("保存 WebView2 引导程序: %w", err)
	}
	return file.Name(), nil
}

// runWebView2Bootstrapper 以 /install 运行引导程序，用户在引导程序窗口中取消安装时返回 errWebView2Cancelled
func runWebView2Bootstrapper(path string) error {
	err := exec.Command(path, "/install").Run()
	var exit *exec.ExitError
	if errors.As(err, &exit) && uint32(exit.ExitCode()) == webview2Cancelled {
		return errWebView2Cancelled
	}
	if err != nil {
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
