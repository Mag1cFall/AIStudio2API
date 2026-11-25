package aistudio

import "testing"

// TestAttachYouTubeMediaPreservesText 验证普通文本与非空白字符在附加 YouTube 媒体时不被截断为空
func TestAttachYouTubeMediaPreservesText(t *testing.T) {
	content := Content{
		Role: RoleUser,
		Parts: []Part{
			{Text: "  normal text with space  "},
			{Text: "https://www.youtube.com/watch?v=dQw4w9WgXcQ"},
		},
	}
	attached := attachYouTubeMedia(content)
	if len(attached.Parts) != 2 {
		t.Fatalf("expected 2 parts (1 text + 1 external media), got %d: %#v", len(attached.Parts), attached.Parts)
	}
	if attached.Parts[0].Text != "  normal text with space  " {
		t.Fatalf("unexpected text: %q", attached.Parts[0].Text)
	}
	if attached.Parts[1].ExternalMedia == nil {
		t.Fatalf("expected external media part")
	}
	for _, text := range []string{"  https://example.com/article  ", "  https://youtube.com/playlist?list=abc  "} {
		got := attachYouTubeMedia(Content{Role: RoleUser, Parts: []Part{{Text: text}}})
		if len(got.Parts) != 1 || got.Parts[0].Text != text {
			t.Errorf("ordinary text changed: %#v", got)
		}
	}
}

// TestAttachYouTubeMediaVideoIDs 验证文本中只有 11 位视频 ID 的链接转为视频，链接在汉字与全角标点处结束，其余链接保留为文本
func TestAttachYouTubeMediaVideoIDs(t *testing.T) {
	for _, test := range []struct{ text, media, rest string }{
		{"用一句话总结https://youtu.be/jNQXAC9IVRw这个视频", "https://www.youtube.com/watch?v=jNQXAC9IVRw", "用一句话总结这个视频"},
		{"看看 https://www.youtube.com/watch?v=jNQXAC9IVRw，讲了什么", "https://www.youtube.com/watch?v=jNQXAC9IVRw", "看看 ，讲了什么"},
		{"Embed players with `https://www.youtube.com/embed/${videoId}`.", "", "Embed players with `https://www.youtube.com/embed/${videoId}`."},
		{"Template: https://www.youtube.com/watch?v=VIDEO_ID", "", "Template: https://www.youtube.com/watch?v=VIDEO_ID"},
		{"See https://youtu.be/abc", "", "See https://youtu.be/abc"},
	} {
		got := attachYouTubeMedia(Content{Role: RoleUser, Parts: []Part{{Text: test.text}}})
		var media, rest string
		for _, part := range got.Parts {
			if part.ExternalMedia != nil {
				media = part.ExternalMedia.URL
			}
			rest += part.Text
		}
		if media != test.media || rest != test.rest {
			t.Errorf("%q: media=%q text=%q", test.text, media, rest)
		}
	}
}
