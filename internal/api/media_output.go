package api

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/Mag1cFall/AIStudio2API/internal/aistudio"
)

// mediaOutput 决定 Chat 与 Messages 正文引用生成媒体的方式
type mediaOutput struct {
	// base 是媒体 URL 使用的服务地址，为空时以 data URI 内联
	base  string
	store *mediaStore
}

// mediaStore 是按内容命名保存生成媒体的目录，总量超过 limit 字节时删除最久未使用的文件
type mediaStore struct {
	dir   string
	limit int64
	mu    sync.Mutex
}

// mediaFile 是媒体目录中的一个文件，修改时间为最近一次使用的时间
type mediaFile struct {
	name string
	size int64
	used time.Time
}

// save 写入媒体文件，已存在的文件只刷新使用时间，写入新文件后按上限回收
func (store *mediaStore) save(name string, data []byte) error {
	path := filepath.Join(store.dir, name)
	if _, err := os.Stat(path); err == nil {
		store.touch(name)
		return nil
	}
	if err := os.MkdirAll(store.dir, 0o755); err != nil {
		return err
	}
	if err := os.WriteFile(path, data, 0o644); err != nil {
		return err
	}
	if err := store.trim(); err != nil {
		slog.Warn("媒体目录回收失败", "dir", store.dir, "error", err)
	}
	return nil
}

// touch 把媒体目录中文件的修改时间刷新为当前时间，指向目录外的名称不处理
func (store *mediaStore) touch(name string) {
	if !filepath.IsLocal(name) {
		return
	}
	now := time.Now()
	if err := os.Chtimes(filepath.Join(store.dir, name), now, now); err != nil && !errors.Is(err, fs.ErrNotExist) {
		slog.Warn("刷新媒体文件使用时间失败", "file", name, "error", err)
	}
}

// trim 在目录总量超过上限时按使用时间从旧到新删除文件，直到总量不超过上限
func (store *mediaStore) trim() error {
	store.mu.Lock()
	defer store.mu.Unlock()
	entries, err := os.ReadDir(store.dir)
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	files := make([]mediaFile, 0, len(entries))
	var total int64
	for _, entry := range entries {
		if !entry.Type().IsRegular() {
			continue
		}
		info, err := entry.Info()
		if errors.Is(err, fs.ErrNotExist) {
			continue
		}
		if err != nil {
			return err
		}
		files = append(files, mediaFile{name: entry.Name(), size: info.Size(), used: info.ModTime()})
		total += info.Size()
	}
	slices.SortFunc(files, func(a, b mediaFile) int { return a.used.Compare(b.used) })
	var removeErr error
	for _, file := range files {
		if total <= store.limit {
			break
		}
		if err := os.Remove(filepath.Join(store.dir, file.name)); err != nil && !errors.Is(err, fs.ErrNotExist) {
			removeErr = errors.Join(removeErr, err)
			continue
		}
		total -= file.size
	}
	return removeErr
}

// mediaExtensions 是常见媒体类型的文件扩展名
var mediaExtensions = map[string]string{
	"image/jpeg": ".jpg",
	"image/png":  ".png",
	"image/webp": ".webp",
	"audio/mpeg": ".mp3",
	"audio/wav":  ".wav",
	"video/mp4":  ".mp4",
}

// mediaTypes 是保存的图片文件扩展名对应的媒体类型
var mediaTypes = map[string]string{".jpg": "image/jpeg", ".png": "image/png", ".webp": "image/webp"}

// mediaOutputFor 按当前设置与请求地址返回本次请求的媒体输出方式
func (s *server) mediaOutputFor(r *http.Request) mediaOutput {
	if s.config.MediaLinks == nil {
		return mediaOutput{}
	}
	enabled, base := s.config.MediaLinks()
	if !enabled {
		return mediaOutput{}
	}
	if base == "" {
		base = requestOrigin(r)
	}
	return mediaOutput{base: strings.TrimRight(base, "/"), store: s.media}
}

// save 在 URL 模式下把带内容的媒体写入媒体目录
func (output mediaOutput) save(media aistudio.Blob) error {
	if output.base == "" || len(media.Data) == 0 {
		return nil
	}
	return output.store.save(mediaFileName(media), media.Data)
}

// saveEvents 保存事件中的全部媒体
func (output mediaOutput) saveEvents(events []aistudio.Event) error {
	for _, event := range events {
		if event.Kind == aistudio.EventMedia && event.InlineData != nil {
			if err := output.save(*event.InlineData); err != nil {
				return err
			}
		}
	}
	return nil
}

// markdown 把媒体 Part 写成 Markdown 图片或链接，内联媒体在 URL 模式下指向媒体目录
func (output mediaOutput) markdown(part aistudio.Part) string {
	var mimeType, url string
	switch {
	case part.InlineData != nil:
		mimeType, url = part.InlineData.MIME, "data:"+part.InlineData.MIME+";base64,"+base64.StdEncoding.EncodeToString(part.InlineData.Data)
		if output.base != "" {
			url = output.base + "/media/" + mediaFileName(*part.InlineData)
		}
	case part.ExternalMedia != nil:
		mimeType, url = part.ExternalMedia.MIME, part.ExternalMedia.URL
	default:
		return ""
	}
	if strings.HasPrefix(mimeType, "image/") {
		return fmt.Sprintf("![media](%s)", url)
	}
	return fmt.Sprintf("[media](%s)", url)
}

// mediaFileName 返回按媒体内容命名的文件名
func mediaFileName(media aistudio.Blob) string {
	sum := sha256.Sum256(media.Data)
	extension := ".bin"
	mimeType, _, _ := strings.Cut(media.MIME, ";")
	if value, ok := mediaExtensions[strings.ToLower(strings.TrimSpace(mimeType))]; ok {
		extension = value
	}
	return hex.EncodeToString(sum[:]) + extension
}

// handleMedia 返回 URL 模式下保存的生成媒体并刷新它的使用时间
func (s *server) handleMedia(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
	http.ServeFileFS(w, r, os.DirFS(s.config.MediaDir), r.PathValue("file"))
	s.media.touch(r.PathValue("file"))
}

// mediaLinkPattern 匹配正文中指向本服务媒体目录的 Markdown 图片
var mediaLinkPattern = regexp.MustCompile(`!\[[^\]]*\]\(https?://[^\s)]+/media/([0-9a-f]{64}(\.(?:jpg|png|webp)))\)`)

// restoreMediaLinks 把 assistant 正文中指向本服务媒体目录的图片恢复为图片输入
func (s *server) restoreMediaLinks(contents []aistudio.Content) error {
	if s.config.MediaDir == "" {
		return nil
	}
	for index := range contents {
		if contents[index].Role != aistudio.RoleAssistant {
			continue
		}
		parts := make([]aistudio.Part, 0, len(contents[index].Parts))
		for _, part := range contents[index].Parts {
			restored, err := s.mediaLinkParts(part)
			if err != nil {
				return err
			}
			parts = append(parts, restored...)
		}
		contents[index].Parts = parts
	}
	return nil
}

// mediaLinkParts 把文本 Part 按其中已保存的媒体链接拆成文本与图片
func (s *server) mediaLinkParts(part aistudio.Part) ([]aistudio.Part, error) {
	if part.Text == "" || part.Thought {
		return []aistudio.Part{part}, nil
	}
	text := part.Text
	parts := make([]aistudio.Part, 0)
	position := 0
	for _, match := range mediaLinkPattern.FindAllStringSubmatchIndex(text, -1) {
		name := text[match[2]:match[3]]
		data, err := os.ReadFile(filepath.Join(s.config.MediaDir, name))
		if errors.Is(err, fs.ErrNotExist) {
			continue
		}
		if err != nil {
			return nil, err
		}
		s.media.touch(name)
		if match[0] > position {
			parts = append(parts, aistudio.Part{Text: text[position:match[0]]})
		}
		parts = append(parts, aistudio.Part{InlineData: &aistudio.Blob{MIME: mediaTypes[text[match[4]:match[5]]], Data: data}})
		position = match[1]
	}
	if position == 0 {
		return []aistudio.Part{part}, nil
	}
	if position < len(text) {
		parts = append(parts, aistudio.Part{Text: text[position:]})
	}
	return parts, nil
}
