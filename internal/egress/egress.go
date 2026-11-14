// Package egress 查询出口的公网 IP，并按本地 IP 库推出出口所在的国家、时区与语言
package egress

import (
	"archive/zip"
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/netip"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/oschwald/maxminddb-golang/v2"
	"golang.org/x/text/language"
)

// Exit 是一个出口的公网 IP 与按 IP 定位的国家、时区和语言
type Exit struct {
	IP       string `json:"ip"`
	Country  string `json:"country"`
	Timezone string `json:"timezone"`
	Locale   string `json:"locale"`
}

// echoURLs 是返回请求方公网 IP 的服务，与 Camoufox 的 public_ip 相同，依次尝试
var echoURLs = []string{
	"https://api.ipify.org",
	"https://checkip.amazonaws.com",
	"https://ipinfo.io/ip",
	"https://icanhazip.com",
	"https://ifconfig.co/ip",
	"https://ipecho.net/plain",
}

// databaseURL 是 Camoufox 默认 IP 库 GeoIP AIO 按地址族发布的压缩包，%s 为 ipv4 或 ipv6
const databaseURL = "https://github.com/daijro/geoip-all-in-one/releases/latest/download/geoip-aio-%s.mmdb.zip"

// databaseMaxAge 是本地 IP 库的更新间隔，与 Camoufox 在库构建超过 8 天时更新一致
const databaseMaxAge = 8 * 24 * time.Hour

// databaseMu 串行化 IP 库文件的替换与查询
var databaseMu sync.Mutex

// downloadRound 是一轮进行中的 IP 库下载：所经的出口、压缩包已写入与总字节（总字节未知时为 0），done 关闭后 err 为结果
type downloadRound struct {
	client   *http.Client
	received atomic.Int64
	total    atomic.Int64
	done     chan struct{}
	err      error
}

// downloads 是各地址族进行中的 IP 库下载与最近一轮下载失败的原因，进程内共用
var downloads = struct {
	sync.Mutex
	rounds  map[string]*downloadRound
	failure error
}{rounds: map[string]*downloadRound{}}

// Download 是正在进行的 IP 库下载的已收与总字节，总字节未知时为 0
type Download struct {
	Received int64 `json:"received"`
	Total    int64 `json:"total"`
}

// Preparation 是 IP 库的准备状态：本地 IPv4 库是否已有、是否正在下载、正在进行的下载与最近一轮下载失败的原因
type Preparation struct {
	Ready     bool      `json:"ready"`
	Preparing bool      `json:"preparing"`
	Download  *Download `json:"download,omitempty"`
	Error     string    `json:"error,omitempty"`
}

// PreparationState 返回 IP 库的准备状态，同时下载多个地址族时合计已收与总字节，收到下载响应前 Download 为 nil
func PreparationState() Preparation {
	downloads.Lock()
	state := Preparation{Preparing: len(downloads.rounds) > 0}
	if downloads.failure != nil {
		state.Error = downloads.failure.Error()
	}
	var download Download
	for _, round := range downloads.rounds {
		download.Received += round.received.Load()
		download.Total += round.total.Load()
	}
	downloads.Unlock()
	if download.Received > 0 || download.Total > 0 {
		state.Download = &download
	}
	if path, err := databasePath("ipv4"); err == nil {
		_, err = os.Stat(path)
		state.Ready = err == nil
	}
	return state
}

// record 是 GeoIP AIO 库中一个地址段的国家与时区
type record struct {
	Country struct {
		ISOCode string `maxminddb:"iso_code"`
	} `maxminddb:"country"`
	Location struct {
		TimeZone string `maxminddb:"time_zone"`
	} `maxminddb:"location"`
}

// Lookup 经 proxy 取得出口公网 IP，再按本地 IP 库推出国家、时区与该国最常用的语言；proxy 为空时直连，IP 库缺失时等进程内共用的一轮下载结束，这一轮经其他出口失败时经 proxy 再下载，IP 库过期时立即用现有的库查询并在后台更新
func Lookup(ctx context.Context, proxy string) (Exit, error) {
	client, err := newClient(proxy)
	if err != nil {
		return Exit{}, err
	}
	address, err := publicIP(ctx, client)
	if err != nil {
		return Exit{}, err
	}
	return locate(ctx, client, address)
}

// Prefetch 在本地 IPv4 库缺失或过期时经 proxy 开始后台下载，proxy 为空时直连
func Prefetch(proxy string) {
	client, err := newClient(proxy)
	if err != nil {
		slog.Warn("下载 IP 库失败", "error", err)
		return
	}
	path, err := databasePath("ipv4")
	if err != nil {
		slog.Warn("下载 IP 库失败", "error", err)
		return
	}
	if !databaseFresh(path) {
		startDownload(client, "ipv4", path)
	}
}

// Environment 返回账户的语言与时区：两项都给出时原样返回，否则经 proxy 查询出口，未给出的项取出口所在地的语言与时区
func Environment(ctx context.Context, proxy string, locale string, timezone string) (Exit, error) {
	if locale != "" && timezone != "" {
		return Exit{Locale: locale, Timezone: timezone}, nil
	}
	exit, err := Lookup(ctx, proxy)
	if err != nil {
		return Exit{}, err
	}
	if locale != "" {
		exit.Locale = locale
	}
	if timezone != "" {
		exit.Timezone = timezone
	}
	return exit, nil
}

// newClient 返回经 proxy 访问外网的 HTTP 客户端，proxy 为空时直连
func newClient(proxy string) (*http.Client, error) {
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.Proxy = nil
	if proxy = strings.TrimSpace(proxy); proxy != "" {
		proxyURL, err := url.Parse(proxy)
		if err != nil {
			return nil, fmt.Errorf("解析出口代理: %w", err)
		}
		transport.Proxy = http.ProxyURL(proxyURL)
	}
	return &http.Client{Transport: transport}, nil
}

// publicIP 依次请求 echoURLs，返回第一个有效的公网 IP
func publicIP(ctx context.Context, client *http.Client) (netip.Addr, error) {
	var errs []error
	for _, address := range echoURLs {
		ip, err := echo(ctx, client, address)
		if err == nil {
			return ip, nil
		}
		if ctx.Err() != nil {
			return netip.Addr{}, ctx.Err()
		}
		errs = append(errs, err)
	}
	return netip.Addr{}, fmt.Errorf("查询出口 IP 失败: %w", errors.Join(errs...))
}

// echo 请求一个 IP 回显服务并解析返回的地址
func echo(ctx context.Context, client *http.Client, address string) (netip.Addr, error) {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, address, nil)
	if err != nil {
		return netip.Addr{}, err
	}
	response, err := client.Do(request)
	if err != nil {
		return netip.Addr{}, err
	}
	defer response.Body.Close()
	body, err := io.ReadAll(io.LimitReader(response.Body, 256))
	if err != nil {
		return netip.Addr{}, err
	}
	if response.StatusCode != http.StatusOK {
		return netip.Addr{}, fmt.Errorf("%s 返回 HTTP %d", address, response.StatusCode)
	}
	ip, err := netip.ParseAddr(strings.TrimSpace(string(body)))
	if err != nil {
		return netip.Addr{}, fmt.Errorf("%s 返回的不是 IP: %q", address, strings.TrimSpace(string(body)))
	}
	return ip.Unmap(), nil
}

// locate 在本地 IP 库中查出 address 的国家与时区，按国家取 CLDR 最可能的语言
func locate(ctx context.Context, client *http.Client, address netip.Addr) (Exit, error) {
	family := "ipv4"
	if address.Is6() {
		family = "ipv6"
	}
	path, err := database(ctx, client, family)
	if err != nil {
		return Exit{}, err
	}
	databaseMu.Lock()
	defer databaseMu.Unlock()
	reader, err := maxminddb.Open(path)
	if err != nil {
		return Exit{}, fmt.Errorf("打开 IP 库: %w", err)
	}
	defer reader.Close()
	var found record
	if err := reader.Lookup(address).Decode(&found); err != nil {
		return Exit{}, fmt.Errorf("查询 IP 库: %w", err)
	}
	if found.Country.ISOCode == "" || found.Location.TimeZone == "" {
		return Exit{}, fmt.Errorf("IP 库中没有 %s 的国家或时区", address)
	}
	region, err := language.ParseRegion(found.Country.ISOCode)
	if err != nil {
		return Exit{}, fmt.Errorf("IP 库中 %s 的国家代码无效: %w", address, err)
	}
	likely, _ := language.Compose(language.Und, region)
	base, _ := likely.Base()
	locale, _ := language.Compose(base, region)
	return Exit{IP: address.String(), Country: region.String(), Timezone: found.Location.TimeZone, Locale: locale.String()}, nil
}

// databasePath 返回地址族的本地 IP 库路径
func databasePath(family string) (string, error) {
	root, err := filepath.Abs(filepath.Join("runtime", "geoip"))
	if err != nil {
		return "", err
	}
	return filepath.Join(root, "geoip-aio-"+family+".mmdb"), nil
}

// databaseFresh 表示 path 处的 IP 库存在且未超过 databaseMaxAge
func databaseFresh(path string) bool {
	info, err := os.Stat(path)
	return err == nil && time.Since(info.ModTime()) < databaseMaxAge
}

// database 返回本地 IP 库路径：库缺失时等这一轮下载结束，这一轮经其他出口失败时经 client 再下载一轮，ctx 结束只停止等待；库已过期时照常返回并在后台更新
func database(ctx context.Context, client *http.Client, family string) (string, error) {
	path, err := databasePath(family)
	if err != nil {
		return "", err
	}
	if databaseFresh(path) {
		return path, nil
	}
	for {
		round := startDownload(client, family, path)
		if _, err := os.Stat(path); err == nil {
			return path, nil
		}
		select {
		case <-round.done:
			if round.err == nil || round.client == client {
				return path, round.err
			}
		case <-ctx.Done():
			return "", ctx.Err()
		}
	}
}

// startDownload 返回地址族进行中的一轮下载，没有时开始一轮并清除上一轮的失败原因；下载不随调用方取消，失败写入日志
func startDownload(client *http.Client, family string, path string) *downloadRound {
	downloads.Lock()
	defer downloads.Unlock()
	if round := downloads.rounds[family]; round != nil {
		return round
	}
	round := &downloadRound{client: client, done: make(chan struct{})}
	downloads.rounds[family], downloads.failure = round, nil
	go func() {
		if err := download(round, fmt.Sprintf(databaseURL, family), path); err != nil {
			round.err = fmt.Errorf("下载 IP 库: %w", err)
			slog.Warn("下载 IP 库失败", "error", err)
		}
		downloads.Lock()
		delete(downloads.rounds, family)
		downloads.failure = round.err
		downloads.Unlock()
		close(round.done)
	}()
	return round
}

// download 下载 IP 库压缩包，连接中途断开且文件比这次连接前更长时从断点续传，续传时 IP 库已发布新版本则从头下载，下载完成后解出其中的 mmdb 文件替换 path
func download(round *downloadRound, address string, path string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("创建 IP 库目录: %w", err)
	}
	archive, err := os.CreateTemp(filepath.Dir(path), "geoip-*.zip")
	if err != nil {
		return err
	}
	defer os.Remove(archive.Name())
	defer archive.Close()
	var etag string
	for {
		progressed, err := downloadRange(round, address, archive, &etag)
		if err == nil {
			break
		}
		if !progressed {
			return err
		}
	}
	size, err := archive.Seek(0, io.SeekEnd)
	if err != nil {
		return err
	}
	files, err := zip.NewReader(archive, size)
	if err != nil {
		return err
	}
	for _, file := range files.File {
		if !strings.HasSuffix(file.Name, ".mmdb") {
			continue
		}
		source, err := file.Open()
		if err != nil {
			return err
		}
		defer source.Close()
		staged, err := os.CreateTemp(filepath.Dir(path), "geoip-*.mmdb")
		if err != nil {
			return err
		}
		defer os.Remove(staged.Name())
		if _, err := io.Copy(staged, source); err != nil {
			staged.Close()
			return err
		}
		if err := staged.Close(); err != nil {
			return err
		}
		databaseMu.Lock()
		defer databaseMu.Unlock()
		return os.Rename(staged.Name(), path)
	}
	return fmt.Errorf("%s 中没有 mmdb 文件", address)
}

// downloadStallTimeout 是 IP 库下载连续收不到数据时中止这次连接的时间
const downloadStallTimeout = 30 * time.Second

// errDownloadStalled 表示 IP 库下载连续 downloadStallTimeout 收不到数据
var errDownloadStalled = errors.New("连续 30 秒收不到 IP 库数据")

// errDatabaseReleased 表示续传时 IP 库已发布新版本
var errDatabaseReleased = errors.New("续传时 IP 库已发布新版本")

// downloadRange 经 round 的出口从 archive 已写入内容的末尾续传压缩包并更新 round 的已收与总字节，etag 记录所下版本的 ETag，续传得到的版本不同或范围超出新版本时清空 archive；返回文件是否比这次连接前更长或已清空待从头下载
func downloadRange(round *downloadRound, address string, archive *os.File, etag *string) (bool, error) {
	offset, err := archive.Seek(0, io.SeekEnd)
	if err != nil {
		return false, err
	}
	ctx, cancel := context.WithCancelCause(context.Background())
	defer cancel(nil)
	stall := time.AfterFunc(downloadStallTimeout, func() { cancel(errDownloadStalled) })
	defer stall.Stop()
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, address, nil)
	if err != nil {
		return false, err
	}
	request.Header.Set("User-Agent", "AIStudio2API")
	if offset > 0 {
		request.Header.Set("Range", fmt.Sprintf("bytes=%d-", offset))
	}
	response, err := round.client.Do(request)
	if err != nil {
		return false, stallCause(ctx, err)
	}
	defer response.Body.Close()
	start := offset
	switch response.StatusCode {
	case http.StatusOK:
		if err := archive.Truncate(0); err != nil {
			return false, err
		}
		if start, err = archive.Seek(0, io.SeekStart); err != nil {
			return false, err
		}
		*etag = response.Header.Get("ETag")
	case http.StatusPartialContent:
		if response.Header.Get("ETag") == *etag {
			break
		}
		fallthrough
	case http.StatusRequestedRangeNotSatisfiable:
		if err := archive.Truncate(0); err != nil {
			return false, err
		}
		return true, errDatabaseReleased
	default:
		return false, fmt.Errorf("%s 返回 HTTP %d", address, response.StatusCode)
	}
	round.received.Store(start)
	if response.ContentLength >= 0 {
		round.total.Store(start + response.ContentLength)
	}
	received, err := io.Copy(archive, stallReader{reader: response.Body, stall: stall, received: &round.received})
	return start+received > offset, stallCause(ctx, err)
}

// stallReader 每读到数据就累计 received 并重新开始 downloadStallTimeout 计时
type stallReader struct {
	reader   io.Reader
	stall    *time.Timer
	received *atomic.Int64
}

func (reader stallReader) Read(buffer []byte) (int, error) {
	n, err := reader.reader.Read(buffer)
	if n > 0 {
		reader.received.Add(int64(n))
		reader.stall.Reset(downloadStallTimeout)
	}
	return n, err
}

// stallCause 在下载因连续收不到数据而中止时返回 errDownloadStalled，其余情况原样返回 err
func stallCause(ctx context.Context, err error) error {
	if err != nil && errors.Is(context.Cause(ctx), errDownloadStalled) {
		return errDownloadStalled
	}
	return err
}
