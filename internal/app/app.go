package app

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/Mag1cFall/AIStudio2API/internal/api"
	"github.com/Mag1cFall/AIStudio2API/internal/config"
	"github.com/Mag1cFall/AIStudio2API/internal/promptdb"
	"github.com/Mag1cFall/AIStudio2API/internal/requestdb"
	"github.com/Mag1cFall/AIStudio2API/internal/setup"
	"github.com/Mag1cFall/AIStudio2API/internal/webui"
)

// commandOptions 保存只影响本次启动的命令行选项
type commandOptions struct {
	openUI    bool
	overrides dataConfigOverrides
}

// Run 执行单二进制命令入口
func Run(args []string) int {
	if err := prepareShell(args); err != nil {
		fmt.Fprintln(os.Stderr, "AIStudio2API 启动失败:", err)
		return 1
	}
	logs := &logOutput{dir: filepath.Join("runtime", "logs")}
	slog.SetDefault(slog.New(slog.NewJSONHandler(logs, nil)))
	executable, executableErr := os.Executable()
	restart, err := runCommand(args, logs, executable)
	if errors.Is(err, flag.ErrHelp) {
		return 0
	}
	if err != nil {
		slog.Error("AIStudio2API 启动失败", "error", err)
		return 1
	}
	if restart {
		if err := errors.Join(executableErr, relaunch(executable, args)); err != nil {
			slog.Error("AIStudio2API 重新启动失败", "error", err)
			return 1
		}
	}
	return 0
}

// runCommand 分派首次配置与默认服务，返回值 restart 表示在线更新后需要重新启动
func runCommand(args []string, logs *logOutput, executable string) (restart bool, err error) {
	cfg, err := config.Load(".env")
	if err != nil {
		return false, err
	}
	if err := logs.set(cfg.LogFile, cfg.LogMaxTotalMB); err != nil {
		return false, err
	}
	defer logs.set(false, 0)
	signals, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	ctx, cancel := context.WithCancel(signals)
	defer cancel()

	if len(args) != 0 && args[0] == "setup" {
		return false, setup.Run(ctx, cfg, args[1:])
	}
	options, err := parseFlags(args, &cfg)
	if err != nil {
		return false, err
	}
	shell := newShell(options.openUI)
	ready := make(chan serviceReady, 1)
	shellCtx, stopShell := context.WithCancel(ctx)
	defer stopShell()
	var restarting atomic.Bool
	result := make(chan error, 1)
	go func() {
		started, err := runService(ctx, cancel, cfg, options, logs, executable, &restarting, ready)
		result <- err
		if started {
			stopShell()
		}
	}()
	shellErr := shell.run(shellCtx, func() (serviceReady, error) {
		select {
		case service := <-ready:
			return service, service.err
		case <-shellCtx.Done():
			return serviceReady{}, shellCtx.Err()
		}
	})
	cancel()
	serviceErr := <-result
	if errors.Is(serviceErr, context.Canceled) {
		serviceErr = nil
	}
	return restarting.Load(), errors.Join(serviceErr, shellErr)
}

// serviceReady 为管理服务的启动结果：管理页面地址与服务日志，或启动失败的原因
type serviceReady struct {
	url string
	log func(level, message string)
	err error
}

// runService 装配运行时、打开请求账本并运行管理监听；就绪或启动失败时向 ready 发送一次结果，started 表示服务曾经就绪
func runService(ctx context.Context, cancel func(), cfg config.Config, options commandOptions, logs *logOutput, executable string, restarting *atomic.Bool, ready chan<- serviceReady) (started bool, err error) {
	defer func() {
		if !started && err != nil {
			ready <- serviceReady{err: err}
		}
	}()
	manager, err := newRuntimeManager(ctx, ".env", cfg, options.overrides)
	if err != nil {
		return false, err
	}
	logService := func(level, message string) {
		manager.requests.log("service", level, message)
	}
	ledger, err := requestdb.Open(filepath.Join("runtime", "requests.db"), logService)
	if err != nil {
		return false, errors.Join(fmt.Errorf("打开请求账本: %w", err), manager.Close())
	}
	ledger.SetBodyCapture(cfg.RequestBodyLog)
	manager.ledger = ledger
	prompts, err := promptdb.Open(filepath.Join("runtime", "prompts.db"))
	if err != nil {
		return false, errors.Join(fmt.Errorf("打开对话库: %w", err), manager.Close(), ledger.Close())
	}
	manager.prompts = prompts
	manager.logs = logs
	updater := newSelfUpdater(buildVersion(), executable, manager.egressProxy, logService, updateRestart(cancel, restarting, logService))
	started, err = runServer(ctx, cfg, manager, updater, func(address string) {
		ready <- serviceReady{url: address, log: logService}
	})
	if started {
		cancel()
	}
	return started, errors.Join(err, manager.Close(), ledger.Close(), prompts.Close())
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
	options := commandOptions{openUI: *openUI}
	flags.Visit(func(value *flag.Flag) {
		switch value.Name {
		case "auth":
			override := cfg.AuthStates
			options.overrides.authStates = &override
		case "proxy":
			override := cfg.Proxy
			options.overrides.proxy = &override
		}
	})
	return options, nil
}

// runServer 管理 HTTP 监听，就绪后以管理页面地址调用 onReady，运行到 ctx 结束或监听出错；started 表示监听曾经就绪
func runServer(ctx context.Context, cfg config.Config, manager *runtimeManager, updater api.UpdateService, onReady func(address string)) (started bool, err error) {
	manager.requests.log("service", "INFO", fmt.Sprintf("管理监听启动 | 地址=%s", cfg.ListenAddr))
	listener, err := net.Listen("tcp", cfg.ListenAddr)
	if err != nil {
		return false, fmt.Errorf("监听 %s: %w", cfg.ListenAddr, err)
	}
	apiHandler := api.NewHandler(manager, api.Config{
		APIKey: cfg.ProxyAPIKey, Admin: manager, Ledger: manager.ledger, Prompts: manager.prompts, Updates: updater,
		AdminAuthEnabled: cfg.AdminAuthEnabled, AdminUsername: cfg.AdminUsername, AdminPassword: cfg.AdminPassword,
		AdminSessionPath: filepath.Join("runtime", "admin-sessions.json"),
		MediaDir:         filepath.Join("runtime", "media"), MediaLinks: manager.mediaLinks,
	})
	server := &http.Server{
		Handler:           rootHandler(apiHandler),
		ReadHeaderTimeout: 10 * time.Second,
		IdleTimeout:       2 * time.Minute,
	}
	serveError := make(chan error, 1)
	go func() {
		serveError <- server.Serve(listener)
	}()

	address := "http://" + browserAddress(listener.Addr().String())
	manager.requests.log("service", "INFO", "管理服务就绪 | 地址="+address)
	onReady(address)
	if cfg.AutoStart {
		go autoStartService(ctx, manager)
	}

	select {
	case err := <-serveError:
		if errors.Is(err, http.ErrServerClosed) {
			return true, nil
		}
		return true, err
	case <-ctx.Done():
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err := server.Shutdown(shutdownCtx); err != nil {
			server.Close()
			slog.Warn("管理监听关闭超时，断开剩余连接", "error", err)
		}
		if err := <-serveError; err != nil && !errors.Is(err, http.ErrServerClosed) {
			return true, err
		}
		return true, nil
	}
}

// autoStartService 在管理服务就绪后按配置启动生成服务
func autoStartService(ctx context.Context, manager *runtimeManager) {
	manager.requests.log("service", "INFO", "自动启动生成服务")
	if _, err := manager.StartService(ctx); err != nil && ctx.Err() == nil {
		manager.requests.log("service", "ERROR", "自动启动生成服务失败 | 错误="+err.Error())
	}
}

// rootHandler 将公开 API 与内嵌管理端挂载到同一服务
func rootHandler(apiHandler http.Handler) http.Handler {
	root := http.NewServeMux()
	root.Handle("/health", apiHandler)
	root.Handle("/api/", apiHandler)
	root.Handle("/v1/", apiHandler)
	root.Handle("/v1beta/", apiHandler)
	root.Handle("/upload/", apiHandler)
	root.Handle("/media/", apiHandler)
	root.Handle("/", webui.Handler())
	return securityHeaders(root)
}

// securityHeaders 禁止页面被其他站点嵌入并关闭 MIME 嗅探与 Referer
func securityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		header := w.Header()
		header.Set("X-Frame-Options", "DENY")
		header.Set("Content-Security-Policy", "frame-ancestors 'none'")
		header.Set("X-Content-Type-Options", "nosniff")
		header.Set("Referrer-Policy", "no-referrer")
		next.ServeHTTP(w, r)
	})
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
