//go:build !desktop

package app

import (
	"context"
	"fmt"
	"os/exec"
	"runtime"
)

// prepareShell 为命令行版的启动准备，命令行版使用当前工作目录
func prepareShell([]string) error {
	return nil
}

// uiShell 为命令行版的管理界面宿主，服务就绪后可用系统浏览器打开管理页面
type uiShell struct {
	open bool
}

// newShell 创建命令行版的管理界面宿主，open 为 true 时打开系统浏览器
func newShell(open bool) *uiShell {
	return &uiShell{open: open}
}

// run 等待 start 返回管理服务就绪，需要时用系统浏览器打开管理页面，等到 ctx 结束；启动失败的原因由服务返回
func (s *uiShell) run(ctx context.Context, start func() (serviceReady, error)) error {
	service, err := start()
	if err != nil {
		return nil
	}
	pageURL, log := service.url, service.log
	if s.open {
		if err := openBrowser(pageURL); err != nil {
			log("WARN", "管理页面打开失败 | "+err.Error())
		} else {
			log("INFO", "管理页面已打开 | 地址="+pageURL)
		}
	}
	<-ctx.Done()
	return nil
}

// openBrowser 使用当前平台的系统命令打开管理界面
func openBrowser(url string) error {
	var command *exec.Cmd
	switch runtime.GOOS {
	case "windows":
		command = exec.Command("rundll32", "url.dll,FileProtocolHandler", url)
	case "darwin":
		command = exec.Command("open", url)
	default:
		command = exec.Command("xdg-open", url)
	}
	if err := command.Start(); err != nil {
		return fmt.Errorf("打开管理界面: %w", err)
	}
	if err := command.Process.Release(); err != nil {
		return fmt.Errorf("释放管理界面启动进程: %w", err)
	}
	return nil
}
