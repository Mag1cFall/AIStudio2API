package app

import (
	"slices"
	"testing"
)

// TestAPIKeyAccountsBinding 核对绑定按最新保存的 key 列表立即生效，已移除的 key 不再保留绑定
func TestAPIKeyAccountsBinding(t *testing.T) {
	same := apiKeyAccounts("key-a,key-b", "key-a:a@example.com")
	if len(same) != 1 || !slices.Equal(same["key-a"], []string{"a@example.com"}) {
		t.Fatalf("key 列表不变时 %v", same)
	}
	rotated := apiKeyAccounts("key-new,key-b", "key-a:a@example.com,key-new:b@example.com")
	if _, kept := rotated["key-a"]; kept || len(rotated) != 1 || !slices.Equal(rotated["key-new"], []string{"b@example.com"}) {
		t.Fatalf("换 key 后 %v", rotated)
	}
}
