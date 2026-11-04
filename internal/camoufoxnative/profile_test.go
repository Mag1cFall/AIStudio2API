//go:build !windows

package camoufoxnative

import (
	"os"
	"path/filepath"
	"testing"
)

// TestIsProfileBrowser 覆盖遗留进程只有在自成进程组且以该 profile 启动时才算匹配
func TestIsProfileBrowser(t *testing.T) {
	profile := "/tmp/aistudio-camoufox-123"
	cases := []struct {
		name string
		line string
		want bool
	}{
		{name: "match", line: " 61513 camoufox --remote-debugging-port=0 -no-remote -headless -profile /tmp/aistudio-camoufox-123\n", want: true},
		{name: "other profile", line: "61513 camoufox -headless -profile /tmp/aistudio-camoufox-999", want: false},
		{name: "profile prefix only", line: "61513 camoufox -profile /tmp/aistudio-camoufox-1234", want: false},
		{name: "not group leader", line: "61600 camoufox -profile /tmp/aistudio-camoufox-123", want: false},
		{name: "no args", line: "61513", want: false},
		{name: "empty", line: "", want: false},
	}
	for _, item := range cases {
		t.Run(item.name, func(t *testing.T) {
			if got := isProfileBrowser(item.line, 61513, profile); got != item.want {
				t.Fatalf("isProfileBrowser() = %v, want %v", got, item.want)
			}
		})
	}
}

// TestRemoveStaleProfilesWithRecordedProcess 覆盖记录了已不存在进程的遗留 profile 被删除且不影响其他进程
func TestRemoveStaleProfilesWithRecordedProcess(t *testing.T) {
	root := t.TempDir()
	t.Setenv("TMPDIR", root)
	profile := filepath.Join(root, profilePrefix+"stale")
	if err := os.Mkdir(profile, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(profile, profileLockName), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := recordBrowserProcess(profile, 2147483646); err != nil {
		t.Fatal(err)
	}
	removed, err := RemoveStaleProfiles()
	if err != nil || removed != 1 {
		t.Fatalf("RemoveStaleProfiles() = %d, %v, want 1, nil", removed, err)
	}
	if _, err := os.Stat(profile); !os.IsNotExist(err) {
		t.Fatalf("profile still exists: %v", err)
	}
}

// TestTerminateStaleBrowserIgnoresBadRecord 覆盖缺失、非数字和系统进程号的记录不触发任何结束操作
func TestTerminateStaleBrowserIgnoresBadRecord(t *testing.T) {
	for _, content := range []string{"", "abc", "0", "1", "-5"} {
		profile := t.TempDir()
		if err := os.WriteFile(filepath.Join(profile, profilePIDName), []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
		if err := terminateStaleBrowser(profile); err != nil {
			t.Fatalf("content %q: %v", content, err)
		}
	}
	if err := terminateStaleBrowser(t.TempDir()); err != nil {
		t.Fatal(err)
	}
}
