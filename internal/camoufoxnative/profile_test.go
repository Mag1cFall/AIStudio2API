package camoufoxnative

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"testing"
	"time"
)

// profileProcessEnv 让测试二进制作为使用 profile 的进程运行：parent 启动一个 child 后报告就绪，两者运行 30 秒后写出 exited
const profileProcessEnv = "CAMOUFOX_PROFILE_PROCESS"

// TestMain 在设置了 profileProcessEnv 时作为测试用的进程运行，否则运行测试
func TestMain(m *testing.M) {
	switch os.Getenv(profileProcessEnv) {
	case "":
		os.Exit(m.Run())
	case "parent":
		child := exec.Command(os.Args[0])
		child.Env = append(os.Environ(), profileProcessEnv+"=child")
		child.Stdout = os.Stdout
		if err := child.Start(); err != nil {
			os.Exit(1)
		}
		fmt.Println("ready")
	}
	time.Sleep(30 * time.Second)
	fmt.Println("exited")
	os.Exit(0)
}

// TestRemoveStaleProfilesEndsProfileProcesses 确认遗留 profile 删除前，以 -profile 引用它的进程连同子进程被结束
func TestRemoveStaleProfilesEndsProfileProcesses(t *testing.T) {
	root := t.TempDir()
	t.Setenv("TMPDIR", root)
	t.Setenv("TMP", root)
	profile, lock, err := createProfile()
	if err != nil {
		t.Fatal(err)
	}
	if err := lock.Unlock(); err != nil {
		t.Fatal(err)
	}
	command := exec.Command(os.Args[0], "-profile", profile)
	command.Env = append(os.Environ(), profileProcessEnv+"=parent")
	stdout, err := command.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := command.Start(); err != nil {
		t.Fatal(err)
	}
	output := bufio.NewReader(stdout)
	if line, err := output.ReadString('\n'); line != "ready\n" {
		t.Fatalf("使用 profile 的进程没有就绪: %q, %v", line, err)
	}
	removed, err := RemoveStaleProfiles()
	if removed != 1 || err != nil {
		t.Fatalf("RemoveStaleProfiles() = %d, %v，期望删除 1 个 profile", removed, err)
	}
	rest, err := io.ReadAll(output)
	if len(rest) != 0 || err != nil {
		t.Fatalf("使用 profile 的进程或其子进程运行到了结尾: %q, %v", rest, err)
	}
	if err := command.Wait(); err == nil {
		t.Fatal("使用 profile 的进程正常退出，没有被结束")
	}
	if _, err := os.Stat(profile); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("profile 仍然存在: %v", err)
	}
}
