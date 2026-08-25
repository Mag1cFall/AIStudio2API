package aistudio

import (
	"net/url"
	"regexp"
	"strings"
)

var youtubeURLPattern = regexp.MustCompile(`https?://[^\s<>"']+`)

// ExternalMediaForURL 返回 AI Studio 可直接读取的外部媒体
func ExternalMediaForURL(raw string) (*ExternalMedia, bool) {
	raw = strings.TrimRight(strings.TrimSpace(raw), ".,;:!?)]}")
	parsed, err := url.Parse(raw)
	if err != nil || parsed.Scheme != "http" && parsed.Scheme != "https" {
		return nil, false
	}
	host := strings.ToLower(parsed.Hostname())
	switch host {
	case "youtu.be":
		if strings.Trim(parsed.Path, "/") == "" {
			return nil, false
		}
	case "youtube.com", "www.youtube.com", "m.youtube.com":
		path := strings.Trim(parsed.Path, "/")
		valid := path == "watch" && parsed.Query().Get("v") != ""
		valid = valid || strings.HasPrefix(path, "shorts/") || strings.HasPrefix(path, "live/") || strings.HasPrefix(path, "embed/")
		if !valid {
			return nil, false
		}
	default:
		return nil, false
	}
	return &ExternalMedia{MIME: "video/*", URL: raw}, true
}

func attachYouTubeMedia(content Content) Content {
	if content.Role != RoleUser {
		return content
	}
	existing := make(map[string]struct{})
	for _, part := range content.Parts {
		if part.ExternalMedia != nil {
			existing[part.ExternalMedia.URL] = struct{}{}
		}
	}
	parts := make([]Part, 0, len(content.Parts)+1)
	for _, part := range content.Parts {
		if part.Text != "" {
			for _, raw := range youtubeURLPattern.FindAllString(part.Text, -1) {
				media, ok := ExternalMediaForURL(raw)
				if !ok {
					continue
				}
				if _, duplicate := existing[media.URL]; !duplicate {
					existing[media.URL] = struct{}{}
					parts = append(parts, Part{ExternalMedia: media})
				}
				part.Text = strings.ReplaceAll(part.Text, raw, "")
			}
			part.Text = strings.TrimSpace(part.Text)
			if part.Text == "" {
				continue
			}
		}
		parts = append(parts, part)
	}
	content.Parts = parts
	return content
}
