package camoufoxnative

import (
	"archive/zip"
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

const camoufoxRelease = "158.0.1-beta.41"

// downloadStallTimeout 是下载连续收不到数据即判定中断的时长
const downloadStallTimeout = 30 * time.Second

// errDownloadStalled 表示下载连续 downloadStallTimeout 没有收到数据
var errDownloadStalled = fmt.Errorf("连续 %d 秒没有收到数据", int(downloadStallTimeout/time.Second))

// Download 是正在进行的 Camoufox 下载的已收与总字节，总字节未知时为 0
type Download struct {
	Received int64 `json:"received"`
	Total    int64 `json:"total"`
}

// downloadState 是正在进行的下载：已收与总字节、本次开始时已有的字节与开始时刻；写入时累计已收字节并推迟停滞判定
type downloadState struct {
	received atomic.Int64
	total    int64
	resumed  int64
	started  time.Time
	stall    *time.Timer
}

func (state *downloadState) Write(data []byte) (int, error) {
	state.received.Add(int64(len(data)))
	state.stall.Reset(downloadStallTimeout)
	return len(data), nil
}

// remaining 按本次下载的平均速度估计剩余时间，无法估计时返回 0
func (state *downloadState) remaining() time.Duration {
	received := state.received.Load()
	elapsed := time.Since(state.started)
	if state.total <= 0 || received <= state.resumed || elapsed <= 0 {
		return 0
	}
	return time.Duration(float64(state.total-received) / float64(received-state.resumed) * float64(elapsed))
}

// activeDownload 是正在进行的 Camoufox 下载，没有下载时为 nil
var activeDownload atomic.Pointer[downloadState]

// DownloadProgress 返回正在进行的 Camoufox 下载，没有下载时 ok 为 false
func DownloadProgress() (Download, bool) {
	state := activeDownload.Load()
	if state == nil {
		return Download{}, false
	}
	return Download{Received: state.received.Load(), Total: state.total}, true
}

// Preparation 是 Camoufox 的准备状态：是否就绪、是否正在下载或解压、正在进行的下载与最近一次准备失败的原因
type Preparation struct {
	Ready     bool      `json:"ready"`
	Preparing bool      `json:"preparing"`
	Download  *Download `json:"download,omitempty"`
	Error     string    `json:"error,omitempty"`
}

// PreparingError 表示 Camoufox 正在准备，Download 为正在进行的下载，连接与解压期间为 nil；Remaining 为估计的剩余下载时间，无法估计时为 0
type PreparingError struct {
	Download  *Download
	Remaining time.Duration
}

func (err *PreparingError) Error() string {
	if err.Download == nil || err.Download.Total <= 0 {
		return "正在准备 Camoufox"
	}
	return fmt.Sprintf("正在下载 Camoufox %d/%d MB", err.Download.Received/1e6, err.Download.Total/1e6)
}

// preparationRound 是一轮后台准备，done 关闭后 path 与 err 为结果
type preparationRound struct {
	done chan struct{}
	path string
	err  error
}

// preparation 是进程内共用的 Camoufox 准备：已就绪的路径、进行中的一轮与最近一轮失败的原因
var preparation struct {
	mu      sync.Mutex
	path    string
	round   *preparationRound
	failure error
}

// PrepareInBackground 在 Camoufox 未就绪且没有进行中的准备时开始一轮后台准备，本机没有同版本 Camoufox 时经 proxy 下载
func PrepareInBackground(proxy string) {
	startPreparation(proxy)
}

// startPreparation 返回进行中的一轮准备，没有时开始一轮，已就绪时返回 nil
func startPreparation(proxy string) *preparationRound {
	preparation.mu.Lock()
	defer preparation.mu.Unlock()
	if preparation.path != "" || preparation.round != nil {
		return preparation.round
	}
	round := &preparationRound{done: make(chan struct{})}
	preparation.round, preparation.failure = round, nil
	go func() {
		round.path, round.err = FindExecutable(context.Background(), proxy)
		if round.err != nil {
			slog.Error("Camoufox 准备失败", "error", round.err)
		}
		preparation.mu.Lock()
		preparation.round, preparation.path, preparation.failure = nil, round.path, round.err
		preparation.mu.Unlock()
		close(round.done)
	}()
	return round
}

// Prepare 返回同版本 Camoufox 的路径，未就绪时开始或加入进程内共用的后台准备并等到这一轮结束；ctx 结束只停止等待，不中止准备
func Prepare(ctx context.Context, proxy string) (string, error) {
	round := startPreparation(proxy)
	if round == nil {
		preparation.mu.Lock()
		defer preparation.mu.Unlock()
		return preparation.path, nil
	}
	select {
	case <-round.done:
		return round.path, round.err
	case <-ctx.Done():
		return "", ctx.Err()
	}
}

// ReadyPath 返回已就绪的 Camoufox 路径，正在准备时返回 *PreparingError，最近一轮准备失败时返回失败原因
func ReadyPath() (string, error) {
	preparation.mu.Lock()
	defer preparation.mu.Unlock()
	if preparation.path != "" {
		return preparation.path, nil
	}
	if preparation.round == nil && preparation.failure != nil {
		return "", preparation.failure
	}
	err := &PreparingError{}
	if state := activeDownload.Load(); state != nil {
		err.Download = &Download{Received: state.received.Load(), Total: state.total}
		err.Remaining = state.remaining()
	}
	return "", err
}

// PreparationState 返回 Camoufox 的准备状态，没有准备过时按本机是否已有同版本 Camoufox 报告就绪
func PreparationState() Preparation {
	preparation.mu.Lock()
	state := Preparation{Ready: preparation.path != "", Preparing: preparation.round != nil}
	if preparation.failure != nil {
		state.Error = preparation.failure.Error()
	}
	preparation.mu.Unlock()
	if !state.Ready && !state.Preparing {
		state.Ready = Installed()
	}
	if download, ok := DownloadProgress(); ok {
		state.Download = &download
	}
	return state
}

// installCamoufox 经出口代理下载当前协议传输已对齐的 Camoufox 版本
func installCamoufox(ctx context.Context, executableName string, proxy string) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	asset, err := camoufoxAssetName()
	if err != nil {
		return "", err
	}
	root, err := camoufoxInstallRoot()
	if err != nil {
		return "", err
	}
	if err := os.MkdirAll(filepath.Dir(root), 0o755); err != nil {
		return "", fmt.Errorf("创建 Camoufox 目录: %w", err)
	}
	archivePath := filepath.Join(filepath.Dir(root), asset+".part")
	address := fmt.Sprintf("https://github.com/daijro/camoufox/releases/download/v%s/%s", camoufoxRelease, asset)
	if err := downloadCamoufoxArchive(ctx, address, archivePath, proxy); err != nil {
		if ctx.Err() != nil {
			return "", err
		}
		return "", fmt.Errorf("%w；在 .env 的 PROXY 设置能访问 GitHub 的代理后重试，或手动下载 %s 解压到 %s", err, address, root)
	}
	staging, err := os.MkdirTemp(filepath.Dir(root), ".camoufox-stage-*")
	if err != nil {
		return "", fmt.Errorf("创建 Camoufox 临时目录: %w", err)
	}
	defer os.RemoveAll(staging)
	if err := extractCamoufoxArchive(ctx, archivePath, staging); err != nil {
		if ctx.Err() == nil {
			os.Remove(archivePath)
		}
		return "", err
	}
	stagedExecutable := filepath.Join(staging, executableName)
	if runtime.GOOS != "windows" {
		if err := os.Chmod(stagedExecutable, 0o755); err != nil {
			return "", fmt.Errorf("设置 Camoufox 执行权限: %w", err)
		}
	}
	if _, err := validateCamoufoxExecutable(stagedExecutable); err != nil {
		return "", fmt.Errorf("校验 Camoufox 临时目录: %w", err)
	}
	if err := ctx.Err(); err != nil {
		return "", err
	}
	if err := os.RemoveAll(root); err != nil {
		return "", fmt.Errorf("清理旧 Camoufox 目录: %w", err)
	}
	if err := os.Rename(staging, root); err != nil {
		return "", fmt.Errorf("发布 Camoufox 目录: %w", err)
	}
	os.Remove(archivePath)
	executable := filepath.Join(root, executableName)
	slog.Info("Camoufox 已就绪", "path", executable)
	return executable, nil
}

// downloadCamoufoxArchive 经出口代理把发行包写入 archivePath，文件已有部分内容时从末尾续传
func downloadCamoufoxArchive(ctx context.Context, address string, archivePath string, proxy string) error {
	transport := http.DefaultTransport.(*http.Transport).Clone()
	egress := "直连"
	if proxy != "" {
		proxyURL, err := url.Parse(proxy)
		if err != nil {
			return fmt.Errorf("解析 Camoufox 下载代理: %w", err)
		}
		transport.Proxy = http.ProxyURL(proxyURL)
		egress = proxyURL.Redacted()
	}
	var offset int64
	if info, err := os.Stat(archivePath); err == nil {
		offset = info.Size()
	}
	slog.Info("正在下载 Camoufox", "version", camoufoxRelease, "platform", runtime.GOOS+"/"+runtime.GOARCH, "egress", egress)
	ctx, cancel := context.WithCancelCause(ctx)
	defer cancel(nil)
	stall := time.AfterFunc(downloadStallTimeout, func() { cancel(errDownloadStalled) })
	defer stall.Stop()
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, address, nil)
	if err != nil {
		return err
	}
	request.Header.Set("User-Agent", "AIStudio2API")
	if offset > 0 {
		request.Header.Set("Range", fmt.Sprintf("bytes=%d-", offset))
	}
	response, err := (&http.Client{Transport: transport}).Do(request)
	if err != nil {
		return fmt.Errorf("下载 Camoufox: %w", stallCause(ctx, err))
	}
	defer response.Body.Close()
	flags := os.O_CREATE | os.O_WRONLY
	switch response.StatusCode {
	case http.StatusOK:
		offset = 0
		flags |= os.O_TRUNC
	case http.StatusPartialContent:
		flags |= os.O_APPEND
	case http.StatusRequestedRangeNotSatisfiable:
		return nil
	default:
		return fmt.Errorf("下载 Camoufox: HTTP %d", response.StatusCode)
	}
	state := &downloadState{resumed: offset, started: time.Now(), stall: stall}
	state.received.Store(offset)
	if response.ContentLength > 0 {
		state.total = offset + response.ContentLength
		slog.Info("Camoufox 下载已开始", "size_mib", state.total/(1024*1024), "resume_mib", offset/(1024*1024))
	}
	archive, err := os.OpenFile(archivePath, flags, 0o644)
	if err != nil {
		return fmt.Errorf("创建 Camoufox 下载文件: %w", err)
	}
	activeDownload.Store(state)
	defer activeDownload.Store(nil)
	_, copyErr := io.Copy(io.MultiWriter(archive, state), contextReader{ctx: ctx, reader: response.Body})
	if closeErr := archive.Close(); copyErr != nil || closeErr != nil {
		return fmt.Errorf("保存 Camoufox: %w", firstError(stallCause(ctx, copyErr), closeErr))
	}
	return nil
}

// stallCause 在下载因连续收不到数据而中止时返回 errDownloadStalled，其余情况原样返回 err
func stallCause(ctx context.Context, err error) error {
	if err != nil && errors.Is(context.Cause(ctx), errDownloadStalled) {
		return errDownloadStalled
	}
	return err
}

func camoufoxInstallRoot() (string, error) {
	root, err := filepath.Abs(filepath.Join("runtime", "camoufox"))
	if err != nil {
		return "", fmt.Errorf("定位 Camoufox 目录: %w", err)
	}
	return root, nil
}

func camoufoxAssetName() (string, error) {
	platform := map[string]string{"windows": "win", "linux": "lin", "darwin": "mac"}[runtime.GOOS]
	architecture := map[string]string{"amd64": "x86_64", "386": "i686", "arm64": "arm64"}[runtime.GOARCH]
	if platform == "" || architecture == "" || runtime.GOOS == "darwin" && runtime.GOARCH == "386" {
		return "", fmt.Errorf("Camoufox 没有 %s/%s 发行包", runtime.GOOS, runtime.GOARCH)
	}
	return fmt.Sprintf("camoufox-%s-%s.%s.zip", camoufoxRelease, platform, architecture), nil
}

func extractCamoufoxArchive(ctx context.Context, archivePath string, destination string) error {
	archive, err := zip.OpenReader(archivePath)
	if err != nil {
		return fmt.Errorf("打开 Camoufox 压缩包: %w", err)
	}
	defer archive.Close()
	for _, entry := range archive.File {
		if err := ctx.Err(); err != nil {
			return err
		}
		target := filepath.Join(destination, filepath.FromSlash(entry.Name))
		relative, err := filepath.Rel(destination, target)
		if err != nil || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
			return fmt.Errorf("Camoufox 压缩包包含无效路径 %q", entry.Name)
		}
		if entry.FileInfo().IsDir() {
			if err := os.MkdirAll(target, entry.Mode()); err != nil {
				return err
			}
			continue
		}
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			return err
		}
		source, err := entry.Open()
		if err != nil {
			return err
		}
		targetFile, err := os.OpenFile(target, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, entry.Mode())
		if err != nil {
			source.Close()
			return err
		}
		_, copyErr := io.Copy(targetFile, contextReader{ctx: ctx, reader: source})
		closeTargetErr := targetFile.Close()
		closeSourceErr := source.Close()
		if copyErr != nil || closeTargetErr != nil || closeSourceErr != nil {
			return fmt.Errorf("解压 Camoufox %s: %w", entry.Name, firstError(copyErr, closeTargetErr, closeSourceErr))
		}
	}
	return nil
}

// contextReader 在复制过程中传播装配取消
type contextReader struct {
	ctx    context.Context
	reader io.Reader
}

// Read 在每个数据块前检查装配取消
func (reader contextReader) Read(buffer []byte) (int, error) {
	if err := reader.ctx.Err(); err != nil {
		return 0, err
	}
	return reader.reader.Read(buffer)
}

func firstError(values ...error) error {
	for _, err := range values {
		if err != nil {
			return err
		}
	}
	return nil
}
