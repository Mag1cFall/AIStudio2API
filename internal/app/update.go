package app

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"golang.org/x/mod/semver"

	"github.com/Mag1cFall/AIStudio2API/internal/api"
)

// releaseRepo 为 GitHub 仓库网页根地址，在线更新的跳转、Atom 订阅、SHA256SUMS 与发行包下载均基于此地址
const releaseRepo = "https://github.com/Mag1cFall/AIStudio2API"

// releaseCheckInterval 为成功读取最新发行版本后的缓存时长
const releaseCheckInterval = time.Hour

// releaseRetryInterval 为检查失败且上游未给出恢复时刻时的自动重查间隔
const releaseRetryInterval = 5 * time.Minute

// releaseCheckTimeout 是读取一次最新发行版本的总时限
const releaseCheckTimeout = 10 * time.Second

// githubRelease 为 GitHub 最新正式发行版本中用到的字段
type githubRelease struct {
	TagName     string
	Body        string
	HTMLURL     string
	PublishedAt time.Time
}

// updateConflictError 表示当前状态不能安装更新
type updateConflictError string

func (err updateConflictError) Error() string { return string(err) }

// HTTPStatus 返回更新冲突的 HTTP 状态码
func (updateConflictError) HTTPStatus() int { return http.StatusConflict }

// updateHTTPError 表示访问 GitHub 返回了非预期 HTTP 状态
type updateHTTPError struct {
	status      int
	rateLimited bool
	retryAt     time.Time
}

func (err *updateHTTPError) Error() string {
	if err.rateLimited {
		return fmt.Sprintf("GitHub 访问频率受限（HTTP %d）", err.status)
	}
	if text := http.StatusText(err.status); text != "" {
		return fmt.Sprintf("HTTP %d %s", err.status, text)
	}
	return fmt.Sprintf("HTTP %d", err.status)
}

// selfUpdater 读取最新发行版本并替换当前可执行文件
type selfUpdater struct {
	version    string
	mode       string
	releaseURL string
	executable string
	proxy      func() string
	requests   *requestRegistry
	restart    func()
	now        func() time.Time
	mu         sync.Mutex
	release    *githubRelease
	checked    time.Time
	checkErr   error
	errorKind  string
	retryAt    time.Time
	updating   bool
	progress   api.UpdateProgress
}

// newSelfUpdater 按构建版本与运行环境创建替换 executable 的更新器
func newSelfUpdater(version, executable string, proxy func() string, requests *requestRegistry, restart func()) *selfUpdater {
	return &selfUpdater{
		version: version, mode: updateMode(version), releaseURL: releaseRepo, executable: executable,
		proxy: proxy, requests: requests, restart: restart, now: time.Now,
	}
}

func (u *selfUpdater) currentTime() time.Time {
	if u.now != nil {
		return u.now()
	}
	return time.Now()
}

func (u *selfUpdater) repoURL() string {
	return strings.TrimRight(u.releaseURL, "/")
}

// updateMode 返回更新方式：managed 由分发方更新，binary 可原地替换，container 随镜像更新，development 为源码构建
func updateMode(version string) string {
	if managedBy != "" {
		return "managed"
	}
	if !semver.IsValid(version) || semver.Prerelease(version) != "" || semver.Build(version) != "" {
		return "development"
	}
	for _, marker := range []string{"/.dockerenv", "/run/.containerenv"} {
		if _, err := os.Stat(marker); err == nil {
			return "container"
		}
	}
	return platformUpdateMode()
}

// UpdateStatus 返回当前版本与最新发行版本，refresh 为 true、成功缓存过期或到达失败重查时刻时重新读取
func (u *selfUpdater) UpdateStatus(ctx context.Context, refresh bool) (api.UpdateStatus, error) {
	now := u.currentTime()
	u.mu.Lock()
	stale := false
	if u.mode != "managed" {
		switch {
		case refresh || u.checked.IsZero():
			stale = true
		case u.checkErr != nil:
			if !u.retryAt.IsZero() {
				stale = !now.Before(u.retryAt)
			} else {
				stale = now.Sub(u.checked) >= releaseRetryInterval
			}
		default:
			stale = now.Sub(u.checked) >= releaseCheckInterval
		}
	}
	u.mu.Unlock()
	if stale {
		release, kind, retryAt, err := u.fetchRelease(ctx)
		if ctx.Err() != nil {
			return api.UpdateStatus{}, ctx.Err()
		}
		if err != nil {
			if kind == "rate_limited" && !retryAt.IsZero() {
				u.requests.log("service", "WARN", "检查更新失败", "error", err, "until", retryAt.Format(time.RFC3339))
			} else {
				u.requests.log("service", "WARN", "检查更新失败", "error", err)
			}
		}
		u.mu.Lock()
		u.checked, u.checkErr, u.errorKind, u.retryAt = u.currentTime(), err, kind, retryAt
		if err == nil {
			u.release = release
		}
		u.mu.Unlock()
	}
	u.mu.Lock()
	defer u.mu.Unlock()
	return u.statusLocked(), nil
}

// ApplyUpdate 下载最新发行包，校验 SHA256SUMS 后替换当前可执行文件并重启管理进程
func (u *selfUpdater) ApplyUpdate(ctx context.Context) (api.UpdateStatus, error) {
	u.mu.Lock()
	if u.mode == "managed" {
		u.mu.Unlock()
		return api.UpdateStatus{}, updateConflictError("在线更新由 " + managedBy + " 管理")
	}
	if u.mode != "binary" {
		u.mu.Unlock()
		return api.UpdateStatus{}, updateConflictError("当前运行方式不支持在线更新")
	}
	if u.updating {
		u.mu.Unlock()
		return api.UpdateStatus{}, updateConflictError("更新正在进行")
	}
	u.updating = true
	u.progress = api.UpdateProgress{Stage: "download"}
	u.mu.Unlock()
	release, _, _, err := u.fetchRelease(ctx)
	if err == nil && semver.Compare(release.TagName, u.version) <= 0 {
		err = updateConflictError("当前已是最新版本 " + u.version)
	}
	if err == nil {
		u.requests.logStage("service", "INFO", api.LogStage{Name: "在线更新", Step: 1, Steps: 2}, "下载发行包", "version", release.TagName)
		err = u.install(ctx, release)
	}
	u.mu.Lock()
	defer u.mu.Unlock()
	if err != nil {
		u.updating = false
		u.requests.logStage("service", "ERROR", api.LogStage{Name: "在线更新", State: "failed"}, "在线更新失败", "error", err)
		return api.UpdateStatus{}, err
	}
	u.release, u.checked, u.checkErr, u.errorKind, u.retryAt = release, u.currentTime(), nil, "", time.Time{}
	u.progress.Stage = "restart"
	u.requests.logStage("service", "INFO", api.LogStage{Name: "在线更新", State: "completed"}, "在线更新完成", "version", release.TagName)
	status := u.statusLocked()
	u.restart()
	return status, nil
}

// statusLocked 在持有锁时生成更新状态
func (u *selfUpdater) statusLocked() api.UpdateStatus {
	status := api.UpdateStatus{Current: u.version, Mode: u.mode, ManagedBy: managedBy, CheckedAt: u.checked, Updating: u.updating}
	status.LogDir, _ = filepath.Abs(filepath.Join("runtime", "logs"))
	if u.updating {
		progress := u.progress
		status.Progress = &progress
	}
	if u.checkErr != nil {
		status.Error = u.checkErr.Error()
		status.ErrorKind = u.errorKind
		status.RetryAt = u.retryAt
	}
	if release := u.release; release != nil {
		status.Latest, status.Notes, status.URL, status.PublishedAt = release.TagName, release.Body, release.HTMLURL, release.PublishedAt
		status.Available = semver.Compare(release.TagName, u.version) > 0
	} else if u.mode != "managed" {
		status.URL = u.repoURL() + "/releases/latest"
	}
	return status
}

// fetchRelease 经 releases/latest 跳转与 releases.atom 读取最新正式发行版本，总时限 releaseCheckTimeout，不调用 api.github.com
func (u *selfUpdater) fetchRelease(ctx context.Context) (*githubRelease, string, time.Time, error) {
	fetchCtx, cancel := context.WithTimeout(ctx, releaseCheckTimeout)
	defer cancel()
	transport, err := u.newTransport()
	if err != nil {
		return nil, "", u.currentTime().Add(releaseRetryInterval).UTC(), fmt.Errorf("读取最新发行版本: %w", err)
	}
	defer transport.CloseIdleConnections()
	client := &http.Client{Transport: transport}
	noRedirect := &http.Client{
		Transport: transport,
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}

	tag, htmlURL, err := u.fetchLatestTag(fetchCtx, noRedirect)
	var notes string
	var publishedAt time.Time
	if err == nil {
		notes, publishedAt, err = u.fetchReleaseNotes(fetchCtx, client, tag)
	}
	if err != nil {
		now := u.currentTime()
		retryAt := now.Add(releaseRetryInterval).UTC()
		if errors.Is(fetchCtx.Err(), context.DeadlineExceeded) {
			err = fmt.Errorf("GitHub 在 %d 秒内没有响应", int(releaseCheckTimeout/time.Second))
			return nil, "", retryAt, fmt.Errorf("读取最新发行版本: %w", err)
		}
		var httpErr *updateHTTPError
		if errors.As(err, &httpErr) && httpErr.rateLimited {
			if !httpErr.retryAt.IsZero() && httpErr.retryAt.After(now) {
				retryAt = httpErr.retryAt
			}
			return nil, "rate_limited", retryAt, fmt.Errorf("读取最新发行版本: %w", err)
		}
		return nil, "", retryAt, fmt.Errorf("读取最新发行版本: %w", err)
	}
	return &githubRelease{TagName: tag, Body: notes, HTMLURL: htmlURL, PublishedAt: publishedAt}, "", time.Time{}, nil
}

// fetchLatestTag 请求 releases/latest 跳转并从 Location 提取最新正式版版本号与页面地址
func (u *selfUpdater) fetchLatestTag(ctx context.Context, client *http.Client) (string, string, error) {
	address := u.repoURL() + "/releases/latest"
	response, err := u.doRequest(ctx, client, http.MethodHead, address, "text/html")
	if err != nil {
		return "", "", err
	}
	if response.StatusCode == http.StatusMethodNotAllowed {
		response.Body.Close()
		response, err = u.doRequest(ctx, client, http.MethodGet, address, "text/html")
		if err != nil {
			return "", "", err
		}
	}
	defer response.Body.Close()
	if response.StatusCode >= 300 && response.StatusCode < 400 {
		location := strings.TrimSpace(response.Header.Get("Location"))
		resolved, parseErr := response.Request.URL.Parse(location)
		if location == "" || parseErr != nil {
			return "", "", errors.New("最新发行版本跳转缺少版本号")
		}
		tag := tagFromReleasePath(resolved.Path)
		if tag == "" {
			return "", "", errors.New("最新发行版本跳转缺少版本号")
		}
		return tag, resolved.String(), nil
	}
	return "", "", readUpdateHTTPError(response, u.currentTime())
}

// fetchReleaseNotes 从 releases.atom 中精确匹配 tag 条目，原样返回 GitHub 渲染后的 HTML 说明与发布时间
func (u *selfUpdater) fetchReleaseNotes(ctx context.Context, client *http.Client, tag string) (string, time.Time, error) {
	response, err := u.doRequest(ctx, client, http.MethodGet, u.repoURL()+"/releases.atom", "application/atom+xml, application/xml;q=0.9")
	if err != nil {
		return "", time.Time{}, err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return "", time.Time{}, readUpdateHTTPError(response, u.currentTime())
	}
	body, err := io.ReadAll(io.LimitReader(response.Body, 4<<20))
	if err != nil {
		return "", time.Time{}, err
	}
	return parseReleaseAtom(body, tag)
}

// download 读取 SHA256SUMS 取得目标发行包摘要，经 releases/download 直链下载并校验
func (u *selfUpdater) download(ctx context.Context, release *githubRelease, name string) ([]byte, error) {
	transport, err := u.newTransport()
	if err != nil {
		return nil, fmt.Errorf("下载 %s: %w", name, err)
	}
	defer transport.CloseIdleConnections()
	client := &http.Client{Transport: transport}

	expected, err := u.fetchDigest(ctx, client, release.TagName, name)
	if err != nil {
		return nil, err
	}
	assetURL := fmt.Sprintf("%s/releases/download/%s/%s", u.repoURL(), url.PathEscape(release.TagName), url.PathEscape(name))
	response, err := u.doRequest(ctx, client, http.MethodGet, assetURL, "application/octet-stream")
	if err != nil {
		return nil, fmt.Errorf("下载 %s: %w", name, err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("下载 %s: %w", name, readUpdateHTTPError(response, u.currentTime()))
	}
	u.mu.Lock()
	u.progress = api.UpdateProgress{Stage: "download", Total: max(response.ContentLength, 0)}
	u.mu.Unlock()
	var archive bytes.Buffer
	archive.Grow(int(max(response.ContentLength, 0)))
	if _, err := io.Copy(&archive, io.TeeReader(response.Body, downloadCounter{u})); err != nil {
		return nil, fmt.Errorf("下载 %s: %w", name, err)
	}
	u.setStage("verify")
	if sum := sha256.Sum256(archive.Bytes()); hex.EncodeToString(sum[:]) != expected {
		return nil, fmt.Errorf("发行包 %s 的 SHA-256 与发行版本记录不一致", name)
	}
	return archive.Bytes(), nil
}

// fetchDigest 从随 Release 上传的 SHA256SUMS 中读取目标发行包的小写 SHA-256 摘要
func (u *selfUpdater) fetchDigest(ctx context.Context, client *http.Client, tag, name string) (string, error) {
	sumsURL := fmt.Sprintf("%s/releases/download/%s/SHA256SUMS", u.repoURL(), url.PathEscape(tag))
	response, err := u.doRequest(ctx, client, http.MethodGet, sumsURL, "text/plain, */*;q=0.8")
	if err != nil {
		return "", fmt.Errorf("读取校验摘要: %w", err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return "", fmt.Errorf("读取校验摘要: %w", readUpdateHTTPError(response, u.currentTime()))
	}
	body, err := io.ReadAll(io.LimitReader(response.Body, 256<<10))
	if err != nil {
		return "", fmt.Errorf("读取校验摘要: %w", err)
	}
	digest, found := parseSHA256Sums(body, name)
	if !found {
		return "", fmt.Errorf("发行版本 %s 没有 %s", tag, name)
	}
	if digest == "" {
		return "", fmt.Errorf("发行包 %s 缺少 SHA-256 摘要", name)
	}
	return digest, nil
}

// setStage 记录在线更新进入的阶段
func (u *selfUpdater) setStage(stage string) {
	u.mu.Lock()
	u.progress.Stage = stage
	u.mu.Unlock()
}

// downloadCounter 把流经的字节数计入在线更新的已下载字节数
type downloadCounter struct{ updater *selfUpdater }

// Write 累加 p 的字节数
func (c downloadCounter) Write(p []byte) (int, error) {
	c.updater.mu.Lock()
	c.updater.progress.Downloaded += int64(len(p))
	c.updater.mu.Unlock()
	return len(p), nil
}

// newTransport 创建走管理进程出口代理的 HTTP Transport
func (u *selfUpdater) newTransport() (*http.Transport, error) {
	transport := http.DefaultTransport.(*http.Transport).Clone()
	if proxy := u.proxy(); proxy != "" {
		proxyURL, err := url.Parse(proxy)
		if err != nil {
			return nil, fmt.Errorf("解析代理: %w", err)
		}
		transport.Proxy = http.ProxyURL(proxyURL)
	}
	return transport, nil
}

func (u *selfUpdater) doRequest(ctx context.Context, client *http.Client, method, address, accept string) (*http.Response, error) {
	request, err := http.NewRequestWithContext(ctx, method, address, nil)
	if err != nil {
		return nil, err
	}
	request.Header.Set("Accept", accept)
	request.Header.Set("User-Agent", "AIStudio2API")
	return client.Do(request)
}

// readUpdateHTTPError 把非预期 HTTP 响应转为结构化错误：识别限流与恢复时刻，不把原始 HTML 或 JSON 暴露到错误文本
func readUpdateHTTPError(response *http.Response, now time.Time) *updateHTTPError {
	body, _ := io.ReadAll(io.LimitReader(response.Body, 4096))
	rateLimited := response.StatusCode == http.StatusTooManyRequests ||
		(response.StatusCode == http.StatusForbidden && (strings.TrimSpace(response.Header.Get("X-RateLimit-Remaining")) == "0" ||
			strings.TrimSpace(response.Header.Get("Retry-After")) != "" ||
			strings.Contains(strings.ToLower(string(body)), "rate limit")))
	return &updateHTTPError{
		status:      response.StatusCode,
		rateLimited: rateLimited,
		retryAt:     parseRetryAt(response.Header, now),
	}
}

// parseRetryAt 优先从 Retry-After、其次从 X-RateLimit-Reset 解析未来恢复时刻（UTC）
func parseRetryAt(header http.Header, now time.Time) time.Time {
	if value := strings.TrimSpace(header.Get("Retry-After")); value != "" {
		if seconds, err := strconv.ParseInt(value, 10, 64); err == nil && seconds > 0 {
			return now.Add(time.Duration(seconds) * time.Second).UTC()
		}
		if parsed, err := http.ParseTime(value); err == nil && parsed.After(now) {
			return parsed.UTC()
		}
	}
	if value := strings.TrimSpace(header.Get("X-RateLimit-Reset")); value != "" {
		if unix, err := strconv.ParseInt(value, 10, 64); err == nil && unix > 0 {
			if parsed := time.Unix(unix, 0).UTC(); parsed.After(now) {
				return parsed
			}
		}
	}
	return time.Time{}
}

// tagFromReleasePath 从 /.../releases/tag/<tag> 路径中取出 tag
func tagFromReleasePath(rawPath string) string {
	const marker = "/releases/tag/"
	index := strings.Index(rawPath, marker)
	if index < 0 {
		return ""
	}
	rest := strings.Trim(rawPath[index+len(marker):], "/")
	if rest == "" || strings.Contains(rest, "/") {
		return ""
	}
	tag, err := url.PathUnescape(rest)
	if err != nil {
		return rest
	}
	return strings.TrimSpace(tag)
}

type atomFeed struct {
	Entries []atomEntry `xml:"entry"`
}

type atomEntry struct {
	ID      string     `xml:"id"`
	Updated string     `xml:"updated"`
	Links   []atomLink `xml:"link"`
	Content string     `xml:"content"`
}

type atomLink struct {
	Href string `xml:"href,attr"`
}

// parseReleaseAtom 在 releases.atom 中精确匹配 tag 对应的条目，原样返回 GitHub 渲染后的 HTML 说明与发布时间
func parseReleaseAtom(data []byte, tag string) (string, time.Time, error) {
	var feed atomFeed
	if err := xml.Unmarshal(data, &feed); err != nil {
		return "", time.Time{}, err
	}
	for _, entry := range feed.Entries {
		if !atomEntryMatchesTag(entry, tag) {
			continue
		}
		var publishedAt time.Time
		if ts := strings.TrimSpace(entry.Updated); ts != "" {
			if parsed, err := time.Parse(time.RFC3339, ts); err == nil {
				publishedAt = parsed.UTC()
			}
		}
		return strings.TrimSpace(entry.Content), publishedAt, nil
	}
	return "", time.Time{}, nil
}

func atomEntryMatchesTag(entry atomEntry, tag string) bool {
	for _, link := range entry.Links {
		if parsed, err := url.Parse(strings.TrimSpace(link.Href)); err == nil && tagFromReleasePath(parsed.Path) == tag {
			return true
		}
	}
	return strings.HasSuffix(strings.TrimSpace(entry.ID), "/"+tag)
}

// parseSHA256Sums 解析 sha256sum 输出格式（<64位十六进制>  <文件名>），返回 name 对应的小写 SHA-256 及是否找到该文件
func parseSHA256Sums(data []byte, name string) (digest string, found bool) {
	for line := range strings.SplitSeq(string(data), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) < 2 {
			continue
		}
		file := strings.TrimSpace(line[len(fields[0]):])
		file = strings.TrimPrefix(file, "*")
		file = strings.TrimPrefix(file, "./")
		if file != name && path.Base(file) != name {
			continue
		}
		sum := strings.ToLower(strings.TrimSpace(fields[0]))
		if len(sum) == 64 {
			if _, err := hex.DecodeString(sum); err == nil {
				return sum, true
			}
		}
		return "", true
	}
	return "", false
}

// extractExecutable 从 zip 或 tar.gz 发行包中取出可执行文件
func extractExecutable(archive []byte, name, binary string) ([]byte, error) {
	if strings.HasSuffix(name, ".zip") {
		reader, err := zip.NewReader(bytes.NewReader(archive), int64(len(archive)))
		if err != nil {
			return nil, fmt.Errorf("读取发行包: %w", err)
		}
		for _, file := range reader.File {
			if path.Base(file.Name) != binary {
				continue
			}
			opened, err := file.Open()
			if err != nil {
				return nil, fmt.Errorf("读取发行包: %w", err)
			}
			defer opened.Close()
			return io.ReadAll(opened)
		}
		return nil, fmt.Errorf("发行包中没有 %s", binary)
	}
	compressed, err := gzip.NewReader(bytes.NewReader(archive))
	if err != nil {
		return nil, fmt.Errorf("读取发行包: %w", err)
	}
	reader := tar.NewReader(compressed)
	for {
		header, err := reader.Next()
		if errors.Is(err, io.EOF) {
			return nil, fmt.Errorf("发行包中没有 %s", binary)
		}
		if err != nil {
			return nil, fmt.Errorf("读取发行包: %w", err)
		}
		if header.Typeflag == tar.TypeReg && path.Base(header.Name) == binary {
			return io.ReadAll(reader)
		}
	}
}

// removeReplacedExecutables 删除在线更新替换后留在可执行文件旁的旧版本：Wails 更新器改名的 <文件名>.old.<时间> 与 selfupdate 隐藏的 .<文件名>.old
func removeReplacedExecutables(executable string) error {
	directory, name := filepath.Split(executable)
	files, err := filepath.Glob(filepath.Join(directory, name+".old.*"))
	if err != nil {
		return err
	}
	hidden, err := filepath.Glob(filepath.Join(directory, "."+name+".old"))
	if err != nil {
		return err
	}
	var errs []error
	for _, file := range append(files, hidden...) {
		if err := os.Remove(file); err != nil && !errors.Is(err, fs.ErrNotExist) {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

// relaunch 以原参数启动替换后的可执行文件：Windows 上新进程沿用当前控制台，其他系统原地替换进程
func relaunch(executable string, args []string) error {
	args = append(slices.Clone(args), "-open-ui=false")
	if runtime.GOOS == "windows" {
		command := exec.Command(executable, args...)
		command.Stdin, command.Stdout, command.Stderr = os.Stdin, os.Stdout, os.Stderr
		return command.Start()
	}
	return syscall.Exec(executable, append([]string{executable}, args...), os.Environ())
}
