// Package webui 提供内嵌管理端静态资源
package webui

import (
	"embed"
	"io/fs"
	"net/http"
	"path"
	"strings"
)

//go:embed dist
var embedded embed.FS

// Files 返回管理端构建产物文件系统
func Files() fs.FS {
	dist, err := fs.Sub(embedded, "dist")
	if err != nil {
		panic(err)
	}
	return dist
}

// Handler 返回管理端静态文件处理器
func Handler() http.Handler {
	files := Files()
	index, err := fs.ReadFile(files, "index.html")
	if err != nil {
		panic(err)
	}
	return &spaHandler{
		files:  files,
		static: http.FileServer(http.FS(files)),
		index:  index,
	}
}

type spaHandler struct {
	files  fs.FS
	static http.Handler
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

	name := strings.TrimPrefix(requestPath, "/")
	if name == "." {
		name = "index.html"
	}
	if _, err := fs.Stat(handler.files, name); err == nil {
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
