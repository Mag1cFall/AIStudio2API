package api

import (
	"net/http"

	"github.com/Mag1cFall/AIStudio2API/internal/camoufoxnative"
)

// handleAccountLiveView 把管理页远程操作窗口接到账户的常驻 Camoufox Worker，
// 连接期间转发画面与输入，断开后由服务清理挂载与账户调度占用并回写 Cookie
func (s *server) handleAccountLiveView(w http.ResponseWriter, r *http.Request) {
	accountID := r.PathValue("id")
	view := camoufoxnative.NewLiveView()
	cleanup, err := s.config.Admin.AttachAccountLiveView(r.Context(), accountID, view)
	if err != nil {
		writeAdminError(w, http.StatusConflict, "live_view_unavailable", err.Error())
		return
	}
	defer cleanup()
	connection, err := loginViewUpgrader.Upgrade(w, r, nil)
	if err != nil {
		return
	}
	_ = view.Serve(r.Context(), connection)
}
