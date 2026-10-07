package aistudio

import (
	"os"
	"runtime"
	"testing"
)

// TestUserCacheRootWithoutEnvironment 验证目录环境变量缺失时仍解析到同一个用户缓存目录
func TestUserCacheRootWithoutEnvironment(t *testing.T) {
	expected, err := os.UserCacheDir()
	if err != nil {
		t.Skipf("当前环境没有用户缓存目录: %v", err)
	}
	if runtime.GOOS == "windows" {
		t.Setenv("LOCALAPPDATA", "")
	} else {
		if os.Getenv("XDG_CACHE_HOME") != "" {
			t.Skip("XDG_CACHE_HOME 指定了非默认缓存目录")
		}
		t.Setenv("HOME", "")
	}
	actual, err := userCacheRoot()
	if err != nil {
		t.Fatalf("userCacheRoot: %v", err)
	}
	if actual != expected {
		t.Fatalf("userCacheRoot = %q, want %q", actual, expected)
	}
}
