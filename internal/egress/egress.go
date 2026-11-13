// Package egress 查询出口的公网 IP，并按本地 IP 库推出出口所在的国家、时区与语言
package egress

import (
	"archive/zip"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/netip"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
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

// databaseMu 串行化 IP 库的下载、替换与查询
var databaseMu sync.Mutex

// record 是 GeoIP AIO 库中一个地址段的国家与时区
type record struct {
	Country struct {
		ISOCode string `maxminddb:"iso_code"`
	} `maxminddb:"country"`
	Location struct {
		TimeZone string `maxminddb:"time_zone"`
	} `maxminddb:"location"`
}

// Lookup 经 proxy 取得出口公网 IP，再按本地 IP 库推出国家、时区与该国最常用的语言；proxy 为空时直连，IP 库缺失或过期时经同一出口下载
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
	databaseMu.Lock()
	defer databaseMu.Unlock()
	path, err := database(ctx, client, family)
	if err != nil {
		return Exit{}, err
	}
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

// database 返回本地 IP 库路径，文件缺失或超过 databaseMaxAge 时重新下载
func database(ctx context.Context, client *http.Client, family string) (string, error) {
	root, err := filepath.Abs(filepath.Join("runtime", "geoip"))
	if err != nil {
		return "", err
	}
	path := filepath.Join(root, "geoip-aio-"+family+".mmdb")
	if info, err := os.Stat(path); err == nil && time.Since(info.ModTime()) < databaseMaxAge {
		return path, nil
	}
	if err := os.MkdirAll(root, 0o755); err != nil {
		return "", fmt.Errorf("创建 IP 库目录: %w", err)
	}
	if err := download(ctx, client, fmt.Sprintf(databaseURL, family), path); err != nil {
		return "", fmt.Errorf("下载 IP 库: %w", err)
	}
	return path, nil
}

// download 下载 IP 库压缩包，解出其中的 mmdb 文件后替换 path
func download(ctx context.Context, client *http.Client, address string, path string) error {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, address, nil)
	if err != nil {
		return err
	}
	request.Header.Set("User-Agent", "AIStudio2API")
	response, err := client.Do(request)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return fmt.Errorf("%s 返回 HTTP %d", address, response.StatusCode)
	}
	archive, err := os.CreateTemp(filepath.Dir(path), "geoip-*.zip")
	if err != nil {
		return err
	}
	defer os.Remove(archive.Name())
	defer archive.Close()
	size, err := io.Copy(archive, response.Body)
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
		return os.Rename(staged.Name(), path)
	}
	return fmt.Errorf("%s 中没有 mmdb 文件", address)
}
