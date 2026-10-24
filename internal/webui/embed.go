// Package webui 提供内嵌管理端静态资源
package webui

import (
	"embed"
	"io/fs"
	"net/http"
	"path"
	"strings"

	"github.com/vearutop/statigz"
)

//go:embed all:dist
var embedded embed.FS

// Handler 返回管理端静态文件处理器，gzip 构建产物直接返回给接受 gzip 的客户端，其余客户端收到解压后的内容
func Handler() http.Handler {
	index, err := fs.ReadFile(embedded, "dist/index.html")
	if err != nil {
		panic(err)
	}
	return &spaHandler{
		static: statigz.FileServer(embedded, statigz.FSPrefix("dist")),
		index:  index,
	}
}

type spaHandler struct {
	static *statigz.Server
	index  []byte
}

// ServeHTTP 服务静态资源并为普通页面路径返回 SPA 入口
func (handler *spaHandler) ServeHTTP(writer http.ResponseWriter, request *http.Request) {
	requestPath := path.Clean(request.URL.Path)
	if reservedPath(requestPath) {
		http.NotFound(writer, request)
		return
	}
	if request.Method != http.MethodGet && request.Method != http.MethodHead {
		http.NotFound(writer, request)
		return
	}

	if handler.static.Found(request) {
		handler.static.ServeHTTP(writer, request)
		return
	}
	if request.Method == http.MethodHead {
		http.NotFound(writer, request)
		return
	}

	writer.Header().Set("Content-Type", "text/html; charset=utf-8")
	writer.WriteHeader(http.StatusOK)
	_, _ = writer.Write(handler.index)
}

// reservedPath 判断路径是否属于服务 API
func reservedPath(requestPath string) bool {
	for _, prefix := range []string{"/api", "/v1", "/v1beta", "/health"} {
		if requestPath == prefix || strings.HasPrefix(requestPath, prefix+"/") {
			return true
		}
	}
	return false
}
