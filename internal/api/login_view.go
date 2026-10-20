package api

import (
	"net"
	"net/http"
	"sync"

	"github.com/Mag1cFall/AIStudio2API/internal/camoufoxnative"
	"github.com/gorilla/websocket"
)

// loginViewUpgrader 升级管理页登录窗口连接，只接受同源页面
var loginViewUpgrader = websocket.Upgrader{}

// loginViews 让带同一 ID 的登录请求与登录窗口连接取得同一个 LiveView
type loginViews struct {
	mu    sync.Mutex
	items map[string]*loginViewEntry
}

// loginViewEntry 记录 LiveView 及正在使用它的登录请求与连接数
type loginViewEntry struct {
	view  *camoufoxnative.LiveView
	users int
}

// acquire 返回 ID 对应的 LiveView，release 后无人使用时删除
func (views *loginViews) acquire(id string) (*camoufoxnative.LiveView, func()) {
	views.mu.Lock()
	defer views.mu.Unlock()
	if views.items == nil {
		views.items = make(map[string]*loginViewEntry)
	}
	entry := views.items[id]
	if entry == nil {
		entry = &loginViewEntry{view: camoufoxnative.NewLiveView()}
		views.items[id] = entry
	}
	entry.users++
	return entry.view, func() {
		views.mu.Lock()
		defer views.mu.Unlock()
		entry.users--
		if entry.users == 0 {
			delete(views.items, id)
		}
	}
}

// handleLoginView 把管理页登录窗口连接接到同 ID 的 LiveView，连接期间推送画面并转发输入
func (s *server) handleLoginView(w http.ResponseWriter, r *http.Request) {
	view, release := s.loginViews.acquire(r.PathValue("view"))
	defer release()
	connection, err := loginViewUpgrader.Upgrade(w, r, nil)
	if err != nil {
		return
	}
	_ = view.Serve(r.Context(), connection)
}

// loginView 返回登录请求使用的 LiveView；请求没有登录窗口 ID 或使用本机可见窗口时返回 nil
func (s *server) loginView(r *http.Request, id string) (*camoufoxnative.LiveView, func()) {
	if id == "" || !liveLogin(r, camoufoxnative.HasDisplay()) {
		return nil, func() {}
	}
	return s.loginViews.acquire(id)
}

// liveLogin 判断登录是否显示在管理页登录窗口：服务器没有图形界面，或请求者看不到服务器屏幕
func liveLogin(r *http.Request, display bool) bool {
	return !display || remoteRequest(r)
}

// remoteRequest 判断管理请求是否来自其他电脑：对端不是回环地址，或经反向代理转发
func remoteRequest(r *http.Request) bool {
	if r.Header.Get("Forwarded") != "" || r.Header.Get("X-Forwarded-For") != "" {
		return true
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	return err != nil || !net.ParseIP(host).IsLoopback()
}
