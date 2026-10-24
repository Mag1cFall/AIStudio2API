package webui

import (
	_ "embed"
	"net/http"
)

//go:embed preview.html
var previewPage []byte

// PreviewHandler 返回预览来源的处理器，只提供把父页面发来的 HTML 以 blob 文档打开的 /shim.html
func PreviewHandler() http.Handler {
	return http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.URL.Path != "/shim.html" || (request.Method != http.MethodGet && request.Method != http.MethodHead) {
			http.NotFound(writer, request)
			return
		}
		header := writer.Header()
		header.Set("Content-Type", "text/html; charset=utf-8")
		header.Set("Cache-Control", "no-store")
		header.Set("X-Content-Type-Options", "nosniff")
		header.Set("Referrer-Policy", "no-referrer")
		_, _ = writer.Write(previewPage)
	})
}
