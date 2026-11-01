package app

import (
	"slices"
	"testing"

	"github.com/Mag1cFall/AIStudio2API/internal/config"
)

// TestAPIKeyAccountsKeepRemovedKeyBinding 核对绑定按保存的 key 列表立即生效，已从列表移除而管理服务仍接受的 key 沿用管理服务启动时的绑定
func TestAPIKeyAccountsKeepRemovedKeyBinding(t *testing.T) {
	active := config.Config{ProxyAPIKey: "key-a,key-b", APIKeyAccounts: "key-a:b@example.com"}
	same := apiKeyAccounts("key-a,key-b", "key-a:a@example.com", active)
	if len(same) != 1 || !slices.Equal(same["key-a"], []string{"a@example.com"}) {
		t.Fatalf("key 列表不变时 %v", same)
	}
	rotated := apiKeyAccounts("key-new,key-b", "key-new:b@example.com", active)
	if len(rotated) != 2 || !slices.Equal(rotated["key-a"], []string{"b@example.com"}) || !slices.Equal(rotated["key-new"], []string{"b@example.com"}) {
		t.Fatalf("换 key 后 %v", rotated)
	}
}
