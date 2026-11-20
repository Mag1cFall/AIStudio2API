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

// nearestDir 返回 path 或其最近的已存在上级目录的绝对路径，path 为空时从工作目录开始，上级目录都不存在时返回空串
func nearestDir(path string) (string, error) {
	absolute, err := filepath.Abs(cmp.Or(path, "."))
	if err != nil {
		return "", err
	}
	for {
		if info, err := os.Stat(absolute); err == nil && info.IsDir() {
			return absolute, nil
		}
		parent := filepath.Dir(absolute)
		if parent == absolute {
			return "", nil
		}
		absolute = parent
	}
}

// handlePaths 列出服务所在机器上 path 或其最近的已存在上级目录下不以点开头的目录与账户状态文件，不带 path 时从工作目录开始，上级目录都不存在时返回文件系统根
func (s *server) handlePaths(w http.ResponseWriter, r *http.Request) {
	path := r.URL.Query().Get("path")
	if path == pathRootsQuery {
		writeJSON(w, http.StatusOK, pathListing{Entries: pathRoots()})
		return
	}
	absolute, err := nearestDir(path)
	if err != nil {
		writeAdminError(w, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}
	if absolute == "" {
		writeJSON(w, http.StatusOK, pathListing{Entries: pathRoots()})
		return
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

// handlePickPath 在桌面版以系统对话框选择账户目录，从 path 或其最近的已存在上级目录开始，上级目录都不存在时从系统默认位置开始，返回选中的路径，用户取消时 path 为空串；命令行版返回 404
func (s *server) handlePickPath(w http.ResponseWriter, r *http.Request) {
	if s.config.PickPath == nil {
		writeAdminError(w, http.StatusNotFound, "not_found", "System file dialog is available in the desktop app only")
		return
	}
	start, err := nearestDir(r.URL.Query().Get("path"))
	if err != nil {
		writeAdminError(w, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}
	path, err := s.config.PickPath(start)
	if err != nil {
		writeAdminError(w, http.StatusInternalServerError, "internal_error", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"path": path})
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
