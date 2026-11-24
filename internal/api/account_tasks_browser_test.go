package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

// TestBrowserCookieCacheSharesSingleBiDiSession 验证同一批导入多选同一 Firefox 地址时只建一次 BiDi 会话，并发读取不触发单会话冲突
func TestBrowserCookieCacheSharesSingleBiDiSession(t *testing.T) {
	upgrader := websocket.Upgrader{}
	var active atomic.Int32
	var sessions atomic.Int32
	mux := http.NewServeMux()
	mux.HandleFunc("/json/version", http.NotFound)
	mux.HandleFunc("/session", func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer conn.Close()
		for {
			var msg struct {
				ID     int    `json:"id"`
				Method string `json:"method"`
			}
			if err := conn.ReadJSON(&msg); err != nil {
				return
			}
			switch msg.Method {
			case "session.new":
				if active.Add(1) > 1 {
					active.Add(-1)
					_ = conn.WriteJSON(map[string]any{"id": msg.ID, "error": "session not created", "message": "Maximum number of active sessions"})
					return
				}
				sessions.Add(1)
				time.Sleep(50 * time.Millisecond)
				_ = conn.WriteJSON(map[string]any{"id": msg.ID, "result": map[string]any{"sessionId": "s1", "capabilities": map[string]any{"browserName": "firefox"}}})
			case "storage.getCookies":
				_ = conn.WriteJSON(map[string]any{"id": msg.ID, "result": map[string]any{"cookies": []map[string]any{
					{"name": "SID", "value": map[string]string{"type": "string", "value": "v"}, "domain": ".google.com", "path": "/"},
				}}})
			case "session.end":
				active.Add(-1)
				_ = conn.WriteJSON(map[string]any{"id": msg.ID, "result": map[string]any{}})
				return
			}
		}
	})
	server := httptest.NewServer(mux)
	t.Cleanup(server.Close)
	address := strings.TrimPrefix(server.URL, "http://")

	cache := newBrowserCookieCache(context.Background())
	var group sync.WaitGroup
	results := make([]string, 4)
	errs := make([]error, 4)
	for i := range results {
		group.Go(func() {
			results[i], errs[i] = cache.read(context.Background(), address)
		})
	}
	group.Wait()
	for i := range results {
		if errs[i] != nil {
			t.Fatalf("第 %d 项读取失败: %v", i, errs[i])
		}
		var cookies []map[string]any
		if err := json.Unmarshal([]byte(results[i]), &cookies); err != nil || len(cookies) != 1 {
			t.Fatalf("第 %d 项 Cookie 无效: %q (%v)", i, results[i], err)
		}
	}
	if got := sessions.Load(); got != 1 {
		t.Fatalf("同一地址并发导入应只建立 1 次 BiDi 会话，实际 %d 次", got)
	}
}
