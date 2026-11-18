package api

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
	"unicode"

	"github.com/Mag1cFall/AIStudio2API/internal/aistudio"
)

// TestPublicErrorReason 验证公开错误为英文且不含账户邮箱，上游错误给出上游原始消息，5xx 带官方英文开头，请求记录保留完整原因
func TestPublicErrorReason(t *testing.T) {
	cases := []struct {
		name     string
		err      error
		status   int
		contains string
		prefix   string
	}{
		{
			name: "上游错误",
			err: fmt.Errorf("发送 AI Studio GenerateContent: %w", &aistudio.RPCError{
				Method: "GenerateContent", StatusCode: http.StatusForbidden, Code: 7, Message: "The caller does not have permission",
			}),
			status: http.StatusForbidden, contains: "The caller does not have permission",
		},
		{
			name: "账户都不可调度",
			err: &aistudio.AccountsNotReadyError{
				Reasons: []string{"a@example.com 需要重新登录", "b@example.com 需要重新登录", "c@example.com 服务受限"},
				States:  []aistudio.AccountState{aistudio.AccountAuthRequired, aistudio.AccountAuthRequired, aistudio.AccountRestricted},
			},
			status: http.StatusServiceUnavailable, contains: "sign-in required: 2, service restricted: 1", prefix: "Service unavailable: ",
		},
		{
			name: "账户均在冷却",
			err: &aistudio.AllCoolingError{
				ModelID: "gemini-3.8-flash", Until: time.Now().Add(time.Hour),
				Reason: "每日限额: " + (&aistudio.RPCError{Method: "GenerateContent", StatusCode: http.StatusTooManyRequests, Code: 8, Message: "You've reached your usage limit"}).Error(),
			},
			status: http.StatusTooManyRequests, contains: "gemini-3.8-flash",
		},
		{
			name:   "资源绑定账户",
			err:    fmt.Errorf("读取文件: %w", errors.New("资源 files/abc 已绑定账户 a@example.com")),
			status: http.StatusBadGateway, contains: "files/abc", prefix: "Internal server error: ",
		},
		{
			name:   "参数无效",
			err:    fmt.Errorf("%w: media_resolution 必须是 low、medium 或 high", aistudio.ErrInvalidArgument),
			status: http.StatusBadRequest, contains: "media_resolution",
		},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			recorder := httptest.NewRecorder()
			metadata := &accessLogMetadata{}
			writeOpenAIRequestError(&accessLogResponseWriter{ResponseWriter: recorder, metadata: metadata}, test.err)
			var body struct {
				Error struct {
					Message string `json:"message"`
				} `json:"error"`
			}
			if err := json.Unmarshal(recorder.Body.Bytes(), &body); err != nil {
				t.Fatal(err)
			}
			message := body.Error.Message
			if recorder.Code != test.status || !strings.Contains(message, test.contains) || !strings.HasPrefix(message, test.prefix) {
				t.Fatalf("状态 %d 消息 %q", recorder.Code, message)
			}
			if strings.Contains(message, "@") || strings.Contains(message, "GenerateContent") || strings.ContainsFunc(message, func(r rune) bool { return unicode.Is(unicode.Han, r) }) {
				t.Fatalf("公开错误带邮箱、上游方法或中文: %q", message)
			}
			if metadata.err != test.err.Error() {
				t.Fatalf("请求记录 %q", metadata.err)
			}
		})
	}
}
