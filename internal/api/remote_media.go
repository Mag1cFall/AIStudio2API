package api

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"io"
	"mime"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"strings"
	"syscall"
	"time"

	"github.com/Mag1cFall/AIStudio2API/internal/aistudio"
	"golang.org/x/sync/errgroup"
)

// remoteMediaUserAgent 是下载外部媒体时声明的客户端标识
const remoteMediaUserAgent = "AIStudio2API (+https://github.com/Mag1cFall/AIStudio2API)"

// remoteMediaClient 是下载外部媒体的 HTTP 客户端，每次重定向前检查目标主机
var remoteMediaClient = &http.Client{Transport: remoteMediaTransport(), CheckRedirect: func(request *http.Request, via []*http.Request) error {
	if len(via) >= 10 {
		return errors.New("stopped after 10 redirects")
	}
	return checkRemoteMediaHost(request.Context(), request.URL.Hostname())
}}

// remoteMediaHeaderTimeout 是下载外部媒体等待响应头的超时
const remoteMediaHeaderTimeout = 30 * time.Second

// remoteMediaTransport 返回按进程环境变量选择代理并设置响应头超时的 Transport，直连时在建立连接时拒绝实际连接的非公网地址
func remoteMediaTransport() *http.Transport {
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.ResponseHeaderTimeout = remoteMediaHeaderTimeout
	dialer := &net.Dialer{Timeout: 30 * time.Second, KeepAlive: 30 * time.Second}
	direct := *dialer
	direct.Control = func(_, address string, _ syscall.RawConn) error {
		return checkRemoteMediaAddress(netip.MustParseAddrPort(address).Addr())
	}
	transport.DialContext = func(ctx context.Context, network, address string) (net.Conn, error) {
		if remoteMediaProxy(address) {
			return dialer.DialContext(ctx, network, address)
		}
		return direct.DialContext(ctx, network, address)
	}
	return transport
}

// remoteMediaProxy 判断连接地址是否为进程环境变量中 http 或 https 请求的代理
func remoteMediaProxy(address string) bool {
	for _, scheme := range []string{"http", "https"} {
		proxy, _ := http.ProxyFromEnvironment(&http.Request{URL: &url.URL{Scheme: scheme, Host: "remote-media.invalid"}})
		if proxy != nil && net.JoinHostPort(proxy.Hostname(), cmp.Or(proxy.Port(), proxyDefaultPorts[proxy.Scheme])) == address {
			return true
		}
	}
	return false
}

// proxyDefaultPorts 为代理 URL 省略端口时各协议的端口
var proxyDefaultPorts = map[string]string{"http": "80", "https": "443", "socks5": "1080", "socks5h": "1080"}

// remoteMediaConcurrency 是一次请求同时下载的外部媒体 URL 数上限
const remoteMediaConcurrency = 4

// inlineRemoteMedia 把内容中 http(s) URL 形式的文件引用下载为内联媒体，同一 URL 只下载一次，不同 URL 最多 remoteMediaConcurrency 个同时下载，一个失败时取消其余下载，Gemini Files API 的文件 URI 保持文件引用
func inlineRemoteMedia(ctx context.Context, contents []aistudio.Content) error {
	var parts []*aistudio.Part
	// first 是每个 URL 第一次出现的片段，下载时取它声明的 MIME
	var first []*aistudio.Part
	index := make(map[string]int)
	for contentIndex := range contents {
		for partIndex := range contents[contentIndex].Parts {
			part := &contents[contentIndex].Parts[partIndex]
			if part.File == nil || !remoteMediaURL(part.File.ID) || geminiFilesAPIURI(part.File.ID) {
				continue
			}
			parts = append(parts, part)
			if _, ok := index[part.File.ID]; !ok {
				index[part.File.ID] = len(first)
				first = append(first, part)
			}
		}
	}
	blobs := make([]*aistudio.Blob, len(first))
	group, groupCtx := errgroup.WithContext(ctx)
	group.SetLimit(remoteMediaConcurrency)
	for i, part := range first {
		url, declared := part.File.ID, part.File.MIME
		group.Go(func() error {
			var err error
			if blobs[i], err = downloadRemoteMedia(groupCtx, url, declared); err != nil {
				return fmt.Errorf("Error while downloading %s: %w", url, err)
			}
			return nil
		})
	}
	if err := group.Wait(); err != nil {
		return err
	}
	for _, part := range parts {
		blob := blobs[index[part.File.ID]]
		part.DisplayName = cmp.Or(part.DisplayName, part.File.Name)
		part.File, part.InlineData = nil, blob
	}
	return nil
}

// remoteMediaURL 返回值是否为 http 或 https URL
func remoteMediaURL(value string) bool {
	lower := strings.ToLower(value)
	return strings.HasPrefix(lower, "http://") || strings.HasPrefix(lower, "https://")
}

// checkRemoteMediaHost 拒绝解析到本机、私网、链路本地、组播或未指定地址的主机
func checkRemoteMediaHost(ctx context.Context, host string) error {
	addresses, err := net.DefaultResolver.LookupNetIP(ctx, "ip", host)
	if err != nil {
		return err
	}
	for _, address := range addresses {
		if err := checkRemoteMediaAddress(address); err != nil {
			return fmt.Errorf("host %s resolves to %w", host, err)
		}
	}
	return nil
}

// checkRemoteMediaAddress 拒绝本机、私网、链路本地、组播与未指定地址
func checkRemoteMediaAddress(address netip.Addr) error {
	address = address.Unmap()
	if address.IsLoopback() || address.IsPrivate() || address.IsLinkLocalUnicast() || address.IsMulticast() || address.IsUnspecified() {
		return fmt.Errorf("non-public address %s", address)
	}
	return nil
}

// downloadRemoteMedia 在请求 context 内下载外部媒体，MIME 依次取响应 Content-Type、声明值与内容判断
func downloadRemoteMedia(ctx context.Context, url string, declared string) (*aistudio.Blob, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	if err := checkRemoteMediaHost(ctx, request.URL.Hostname()); err != nil {
		return nil, err
	}
	request.Header.Set("User-Agent", remoteMediaUserAgent)
	response, err := remoteMediaClient.Do(request)
	if err != nil {
		return nil, err
	}
	defer response.Body.Close()
	if response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices {
		return nil, fmt.Errorf("HTTP %s", response.Status)
	}
	if response.ContentLength > openAIFileMaxBytes {
		return nil, fmt.Errorf("content exceeds %d bytes", openAIFileMaxBytes)
	}
	data, err := io.ReadAll(io.LimitReader(response.Body, openAIFileMaxBytes+1))
	if err != nil {
		return nil, err
	}
	if int64(len(data)) > openAIFileMaxBytes {
		return nil, fmt.Errorf("content exceeds %d bytes", openAIFileMaxBytes)
	}
	if len(data) == 0 {
		return nil, fmt.Errorf("response body is empty")
	}
	mimeType, _, err := mime.ParseMediaType(response.Header.Get("Content-Type"))
	if err != nil || mimeType == "application/octet-stream" || mimeType == "binary/octet-stream" {
		mimeType = declared
	}
	mimeType, data = normalizeImagePayload(attachmentMIME(mimeType, "", data), data)
	return &aistudio.Blob{MIME: mimeType, Data: data}, nil
}
