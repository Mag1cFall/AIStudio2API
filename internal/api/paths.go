package api

import (
	"cmp"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"

	"github.com/Mag1cFall/AIStudio2API/internal/aistudio"
)

// pathRootsQuery 是路径选择器请求文件系统根时的 path 取值
const pathRootsQuery = "roots"

// pathEntry 是路径选择器中的一个目录或账户状态文件
type pathEntry struct {
	Name string `json:"name"`
	Path string `json:"path"`
	Dir  bool   `json:"dir"`
}

// pathListing 是路径选择器当前位置的内容，Path 为空表示文件系统根列表
type pathListing struct {
	Path    string      `json:"path"`
	Parent  string      `json:"parent"`
	Entries []pathEntry `json:"entries"`
}

// handlePaths 列出服务所在机器上 path 或其最近的已存在上级目录下不以点开头的目录与账户状态文件，不带 path 时从工作目录开始，上级目录都不存在时返回文件系统根
func (s *server) handlePaths(w http.ResponseWriter, r *http.Request) {
	path := r.URL.Query().Get("path")
	if path == pathRootsQuery {
		writeJSON(w, http.StatusOK, pathListing{Entries: pathRoots()})
		return
	}
	absolute, err := filepath.Abs(cmp.Or(path, "."))
	if err != nil {
		writeAdminError(w, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}
	for {
		if info, err := os.Stat(absolute); err == nil && info.IsDir() {
			break
		}
		parent := filepath.Dir(absolute)
		if parent == absolute {
			writeJSON(w, http.StatusOK, pathListing{Entries: pathRoots()})
			return
		}
		absolute = parent
	}
	items, err := os.ReadDir(absolute)
	if err != nil {
		writeAdminError(w, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}
	entries := make([]pathEntry, 0, len(items))
	for _, item := range items {
		if strings.HasPrefix(item.Name(), ".") {
			continue
		}
		if item.IsDir() || item.Name() == aistudio.StorageStateName {
			entries = append(entries, pathEntry{Name: item.Name(), Path: filepath.Join(absolute, item.Name()), Dir: item.IsDir()})
		}
	}
	slices.SortFunc(entries, func(a, b pathEntry) int {
		if a.Dir != b.Dir {
			if a.Dir {
				return -1
			}
			return 1
		}
		return cmp.Compare(strings.ToLower(a.Name), strings.ToLower(b.Name))
	})
	parent := filepath.Dir(absolute)
	if parent == absolute {
		parent = pathRootsQuery
	}
	writeJSON(w, http.StatusOK, pathListing{Path: absolute, Parent: parent, Entries: entries})
}

// pathRoots 返回文件系统根：Windows 为存在的盘符，其余系统为 /
func pathRoots() []pathEntry {
	if runtime.GOOS != "windows" {
		return []pathEntry{{Name: "/", Path: "/", Dir: true}}
	}
	roots := make([]pathEntry, 0, 4)
	for letter := 'A'; letter <= 'Z'; letter++ {
		root := string(letter) + `:\`
		if _, err := os.Stat(root); err == nil {
			roots = append(roots, pathEntry{Name: root, Path: root, Dir: true})
		}
	}
	return roots
}
