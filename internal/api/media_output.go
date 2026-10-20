package api

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/Mag1cFall/AIStudio2API/internal/aistudio"
)

// mediaOutput 决定 Chat 与 Messages 正文引用生成媒体的方式
type mediaOutput struct {
	// base 是媒体 URL 使用的服务地址，为空时以 data URI 内联
	base string
	dir  string
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
	return mediaOutput{base: strings.TrimRight(base, "/"), dir: s.config.MediaDir}
}

// save 在 URL 模式下把带内容的媒体写入媒体目录
func (output mediaOutput) save(media aistudio.Media) error {
	if output.base == "" || len(media.Data) == 0 {
		return nil
	}
	path := filepath.Join(output.dir, mediaFileName(media))
	if _, err := os.Stat(path); err == nil {
		return nil
	}
	if err := os.MkdirAll(output.dir, 0o755); err != nil {
		return err
	}
	return os.WriteFile(path, media.Data, 0o644)
}

// saveEvents 保存事件中的全部媒体
func (output mediaOutput) saveEvents(events []aistudio.Event) error {
	for _, event := range events {
		if event.Kind == aistudio.EventMedia && event.Media != nil {
			if err := output.save(*event.Media); err != nil {
				return err
			}
		}
	}
	return nil
}

// markdown 把媒体写成 Markdown 图片或链接
func (output mediaOutput) markdown(media aistudio.Media) string {
	url := media.URL
	if len(media.Data) > 0 {
		if output.base != "" {
			url = output.base + "/media/" + mediaFileName(media)
		} else {
			url = "data:" + media.MIME + ";base64," + base64.StdEncoding.EncodeToString(media.Data)
		}
	}
	label := media.Name
	if label == "" {
		label = "media"
	}
	if strings.HasPrefix(media.MIME, "image/") {
		return fmt.Sprintf("![%s](%s)", label, url)
	}
	return fmt.Sprintf("[%s](%s)", label, url)
}

// mediaFileName 返回按媒体内容命名的文件名
func mediaFileName(media aistudio.Media) string {
	sum := sha256.Sum256(media.Data)
	extension := ".bin"
	mimeType, _, _ := strings.Cut(media.MIME, ";")
	if value, ok := mediaExtensions[strings.ToLower(strings.TrimSpace(mimeType))]; ok {
		extension = value
	}
	return hex.EncodeToString(sum[:]) + extension
}

// handleMedia 返回 URL 模式下保存的生成媒体
func (s *server) handleMedia(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
	http.ServeFileFS(w, r, os.DirFS(s.config.MediaDir), r.PathValue("file"))
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
		data, err := os.ReadFile(filepath.Join(s.config.MediaDir, text[match[2]:match[3]]))
		if errors.Is(err, fs.ErrNotExist) {
			continue
		}
		if err != nil {
			return nil, err
		}
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
