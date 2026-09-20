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
	"os/exec"
	"os/signal"
	"runtime"
	"strings"
	"syscall"
	"time"

	"github.com/Mag1cFall/AIStudio2API/internal/api"
	"github.com/Mag1cFall/AIStudio2API/internal/config"
	"github.com/Mag1cFall/AIStudio2API/internal/setup"
	"github.com/Mag1cFall/AIStudio2API/internal/webui"
)

// commandOptions holds command-line options that only affect the current run.
type commandOptions struct {
	openUI    bool
	overrides dataConfigOverrides
}

// Run executes the single-binary command entry point.
func Run(args []string) int {
	slog.SetDefault(slog.New(slog.NewJSONHandler(os.Stderr, nil)))

	err := runCommand(args)
	if errors.Is(err, flag.ErrHelp) {
		return 0
	}
	if err != nil {
		slog.Error("AIStudio2API failed to start", "error", err)
		return 1
	}

	return 0
}

// runCommand dispatches setup or default server workflows.
func runCommand(args []string) error {
	cfg, err := config.Load(".env")
	if err != nil {
		return err
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	if len(args) != 0 && args[0] == "setup" {
		return setup.Run(ctx, cfg, args[1:])
	}

	options, err := parseFlags(args, &cfg)
	if err != nil {
		return err
	}

	manager, err := newRuntimeManager(ctx, ".env", cfg, options.overrides)
	if err != nil {
		return err
	}

	return errors.Join(runServer(ctx, cfg, options, manager), manager.Close())
}

// parseFlags parses CLI flags and overrides the current run configuration.
func parseFlags(args []string, cfg *config.Config) (commandOptions, error) {
	flags := flag.NewFlagSet("aistudio2api", flag.ContinueOnError)
	flags.Usage = func() {
		fmt.Fprintln(flags.Output(), "Initial setup: aistudio2api setup")
		fmt.Fprintln(flags.Output(), "Standard run:  aistudio2api [flags]")
		flags.PrintDefaults()
	}

	authStates := flags.String("auth", cfg.AuthStates, "Account state file, directory, or comma-separated paths")
	listenAddr := flags.String("listen", cfg.ListenAddr, "Server listen address")
	proxy := flags.String("proxy", cfg.Proxy, "HTTP, HTTPS, or SOCKS5 proxy for this run")
	openUI := flags.Bool("open-ui", len(args) == 0, "Open web UI after launch")

	if err := flags.Parse(args); err != nil {
		return commandOptions{}, err
	}
	if flags.NArg() != 0 {
		return commandOptions{}, fmt.Errorf("unknown argument %q", flags.Arg(0))
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

// runServer manages HTTP listening and graceful shutdown.
func runServer(ctx context.Context, cfg config.Config, options commandOptions, manager *runtimeManager) error {
	manager.requests.log("service", "INFO", fmt.Sprintf("Admin listener started | address=%s", cfg.ListenAddr))

	listener, err := net.Listen("tcp", cfg.ListenAddr)
	if err != nil {
		return fmt.Errorf("listen on %s: %w", cfg.ListenAddr, err)
	}

	apiHandler := api.NewHandler(manager, api.Config{APIKey: cfg.ProxyAPIKey, Admin: manager})
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
	manager.requests.log("service", "INFO", "Admin service ready | address=http://"+address)

	if options.openUI {
		if err := openBrowser("http://" + address); err != nil {
			manager.requests.log("service", "WARN", "Failed to open admin UI | "+err.Error())
		} else {
			manager.requests.log("service", "INFO", "Admin UI opened | address=http://"+address)
		}
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
			return fmt.Errorf("shutdown HTTP server: %w", err)
		}

		if err := <-serveError; err != nil && !errors.Is(err, http.ErrServerClosed) {
			return err
		}
		return nil
	}
}

// rootHandler mounts the public API and internal web UI onto the same handler.
func rootHandler(apiHandler http.Handler) http.Handler {
	root := http.NewServeMux()
	root.Handle("/health", apiHandler)
	root.Handle("/api/", apiHandler)
	root.Handle("/v1/", apiHandler)
	root.Handle("/v1beta/", apiHandler)
	root.Handle("/", webui.Handler())

	return root
}

// browserAddress converts wildcard listen addresses into a local accessible address.
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

// openBrowser opens the administration UI using the system's default browser command.
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
		return fmt.Errorf("open admin UI: %w", err)
	}

	if err := command.Process.Release(); err != nil {
		return fmt.Errorf("release browser process: %w", err)
	}

	return nil
}
