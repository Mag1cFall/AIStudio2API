package api

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"testing"
	"time"

	"github.com/Mag1cFall/AIStudio2API/internal/aistudio"
)

// TestMediaDirectoryLimit 验证媒体目录超过上限时删除最久未使用的文件：启动与保存新文件时回收，读取媒体与续接读回刷新使用时间
func TestMediaDirectoryLimit(t *testing.T) {
	dir := t.TempDir()
	setUsed := func(name string, hours int) {
		t.Helper()
		used := time.Now().Add(time.Duration(hours) * time.Hour)
		if err := os.Chtimes(filepath.Join(dir, name), used, used); err != nil {
			t.Fatal(err)
		}
	}
	name := func(index int) string { return fmt.Sprintf("%064x.png", index) }
	for index := range 4 {
		name := name(index)
		if err := os.WriteFile(filepath.Join(dir, name), make([]byte, 10), 0o644); err != nil {
			t.Fatal(err)
		}
		setUsed(name, index-10)
	}
	handler := NewHandler(nil, Config{MediaDir: dir, MediaLimit: 30})
	assertMedia := func(want ...string) {
		t.Helper()
		entries, err := os.ReadDir(dir)
		if err != nil {
			t.Fatal(err)
		}
		var got []string
		for _, entry := range entries {
			got = append(got, entry.Name())
		}
		slices.Sort(want)
		if !slices.Equal(got, want) {
			t.Fatalf("媒体目录 = %v，期望 %v", got, want)
		}
	}
	assertMedia(name(1), name(2), name(3))

	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/media/"+name(1), nil))
	if recorder.Code != http.StatusOK {
		t.Fatalf("GET /media/%s = %d", name(1), recorder.Code)
	}
	server := &server{config: Config{MediaDir: dir}, media: &mediaStore{dir: dir, limit: 30}}
	output := mediaOutput{base: "http://127.0.0.1:2048", store: server.media}
	first := aistudio.Blob{MIME: "image/png", Data: []byte("first-new!")}
	if err := output.save(first); err != nil {
		t.Fatal(err)
	}
	assertMedia(name(1), name(3), mediaFileName(first))

	setUsed(name(1), -5)
	setUsed(mediaFileName(first), -4)
	link := aistudio.Content{Role: aistudio.RoleAssistant, Parts: []aistudio.Part{{Text: "![media](http://127.0.0.1:2048/media/" + name(3) + ")"}}}
	if err := server.restoreMediaLinks([]aistudio.Content{link}); err != nil {
		t.Fatal(err)
	}
	second := aistudio.Blob{MIME: "image/png", Data: []byte("second-new")}
	if err := output.save(second); err != nil {
		t.Fatal(err)
	}
	assertMedia(name(3), mediaFileName(first), mediaFileName(second))
}
