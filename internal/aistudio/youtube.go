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
	videoID := ""
	switch host {
	case "youtu.be":
		path := strings.Trim(parsed.Path, "/")
		if path != "" {
			videoID = strings.Split(path, "/")[0]
		}
	case "youtube.com", "www.youtube.com", "m.youtube.com":
		path := strings.Trim(parsed.Path, "/")
		if path == "watch" {
			videoID = parsed.Query().Get("v")
		} else {
			segments := strings.Split(path, "/")
			if len(segments) >= 2 && (segments[0] == "shorts" || segments[0] == "live" || segments[0] == "embed") {
				videoID = segments[1]
			}
		}
	default:
		return nil, false
	}
	videoID = strings.TrimSpace(videoID)
	if videoID == "" {
		return nil, false
	}
	return &ExternalMedia{MIME: "video/*", URL: "https://www.youtube.com/watch?v=" + url.QueryEscape(videoID)}, true
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
