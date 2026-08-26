package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"runtime"
	"strings"
	"syscall"
	"time"

	"github.com/Mag1cFall/AIStudio2API/internal/aistudio"
	"github.com/Mag1cFall/AIStudio2API/internal/api"
	"github.com/Mag1cFall/AIStudio2API/internal/config"
	"github.com/Mag1cFall/AIStudio2API/internal/webui"
)

// commandOptions 保存只影响本次启动的命令行选项
type commandOptions struct {
	openUI bool
}

// main 执行单二进制命令入口
func main() {
	if err := runCommand(os.Args[1:]); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return
		}
		slog.Error("AIStudio2API 启动失败", "error", err)
		os.Exit(1)
	}
}

// runCommand 分派首次配置与默认服务
func runCommand(args []string) error {
	cfg, err := config.Load(".env")
	if err != nil {
		return err
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	if len(args) != 0 && args[0] == "setup" {
		return runSetup(ctx, cfg, args[1:])
	}
	options, err := parseFlags(args, &cfg)
	if err != nil {
		return err
	}
	service, admin, closeRuntime, err := newRuntime(ctx, cfg)
	if err != nil {
		return err
	}
	return errors.Join(runServer(ctx, cfg, options, service, admin), closeRuntime())
}

// parseFlags 使用命令行参数覆盖本次启动配置
func parseFlags(args []string, cfg *config.Config) (commandOptions, error) {
	flags := flag.NewFlagSet("aistudio2api", flag.ContinueOnError)
	flags.Usage = func() {
		fmt.Fprintln(flags.Output(), "首次配置: aistudio2api setup")
		fmt.Fprintln(flags.Output(), "日常启动: aistudio2api [参数]")
		flags.PrintDefaults()
	}
	authStates := flags.String("auth", cfg.AuthStates, "账户状态文件、目录或逗号分隔的多个路径")
	listenAddr := flags.String("listen", cfg.ListenAddr, "服务监听地址")
	proxy := flags.String("proxy", cfg.Proxy, "本次启动使用的 HTTP、HTTPS 或 SOCKS5 代理")
	openUI := flags.Bool("open-ui", len(args) == 0, "启动后打开管理界面")
	if err := flags.Parse(args); err != nil {
		return commandOptions{}, err
	}
	if flags.NArg() != 0 {
		return commandOptions{}, fmt.Errorf("未知参数 %q", flags.Arg(0))
	}

	cfg.AuthStates = strings.TrimSpace(*authStates)
	cfg.ListenAddr = strings.TrimSpace(*listenAddr)
	cfg.Proxy = strings.TrimSpace(*proxy)
	if err := cfg.Validate(); err != nil {
		return commandOptions{}, err
	}
	return commandOptions{openUI: *openUI}, nil
}

// runServer 管理 HTTP 监听与优雅退出
func runServer(ctx context.Context, cfg config.Config, options commandOptions, service aistudio.Service, admin *runtimeAdmin) error {
	admin.requests.log("service", "INFO", fmt.Sprintf("应用启动 | 4/4 | 监听 HTTP | 地址=%s", cfg.ListenAddr))
	listener, err := net.Listen("tcp", cfg.ListenAddr)
	if err != nil {
		return fmt.Errorf("监听 %s: %w", cfg.ListenAddr, err)
	}
	apiHandler := api.NewHandler(service, api.Config{APIKey: cfg.ProxyAPIKey, Admin: admin})
	server := &http.Server{
		Handler:           rootHandler(apiHandler),
		ReadHeaderTimeout: 10 * time.Second,
		IdleTimeout:       2 * time.Minute,
	}
	serveError := make(chan error, 1)
	go func() {
		serveError <- server.Serve(listener)
	}()

	address := browserAddress(listener.Addr().String())
	admin.requests.log("service", "INFO", "管理服务就绪 | 地址=http://"+address)
	if options.openUI {
		if err := openBrowser("http://" + address); err != nil {
			_ = server.Close()
			<-serveError
			return err
		}
		admin.requests.log("service", "INFO", "管理页面已打开 | 地址=http://"+address)
	}

	select {
	case err := <-serveError:
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return err
	case <-ctx.Done():
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err := server.Shutdown(shutdownCtx); err != nil {
			return fmt.Errorf("关闭 HTTP 服务: %w", err)
		}
		if err := <-serveError; err != nil && !errors.Is(err, http.ErrServerClosed) {
			return err
		}
		return nil
	}
}

// rootHandler 将公开 API 与内嵌管理端挂载到同一服务
func rootHandler(apiHandler http.Handler) http.Handler {
	root := http.NewServeMux()
	root.Handle("/health", apiHandler)
	root.Handle("/api/", apiHandler)
	root.Handle("/v1/", apiHandler)
	root.Handle("/v1beta/", apiHandler)
	root.Handle("/", webui.Handler())
	return root
}

// browserAddress 将通配监听地址转换为本机可访问地址
func browserAddress(address string) string {
	host, port, err := net.SplitHostPort(address)
	if err != nil {
		return address
	}
	if host == "" || host == "0.0.0.0" || host == "::" {
		host = "127.0.0.1"
	}
	return net.JoinHostPort(host, port)
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
