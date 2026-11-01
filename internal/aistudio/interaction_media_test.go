package aistudio

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
)

// TestDecodeInteractionMediaMIME 核对各类媒体 delta 按官网枚举表取 MIME，枚举缺省或不在表中时取该类的默认 MIME
func TestDecodeInteractionMediaMIME(t *testing.T) {
	data := []byte("\x00\x00\x00\x18ftypmp42\x00\x00\x00\x00mp42isom")
	for _, tc := range []struct {
		field int
		code  any
		mime  string
	}{
		{4, nil, "video/mp4"},
		{4, 1, "video/mp4"},
		{4, 7, "video/webm"},
		{4, 42, "video/mp4"},
		{1, nil, "image/jpeg"},
		{1, 1, "image/png"},
		{1, 3, "image/webp"},
		{2, nil, "audio/wav"},
		{2, 11, "audio/opus"},
		{3, nil, "application/pdf"},
	} {
		content := make([]any, tc.field+1)
		content[tc.field] = []any{tc.code, data}
		delta := make([]any, 11)
		delta[10] = []any{0, content}
		final := make([]any, 20)
		final[19] = []any{[]any{nil, 3}}
		wire, err := json.Marshal([]any{[]any{delta, final}, []any{0}})
		if err != nil {
			t.Fatal(err)
		}
		var media *Blob
		err = DecodeInteractionStream("CreateInteractionStream", strings.NewReader(string(wire)), func(event Event) error {
			if event.Kind == EventMedia {
				media = event.InlineData
			}
			return nil
		})
		if err != nil {
			t.Fatalf("field %d code %v: %v", tc.field, tc.code, err)
		}
		if media == nil || media.MIME != tc.mime || !bytes.Equal(media.Data, data) {
			t.Fatalf("field %d code %v: media=%+v", tc.field, tc.code, media)
		}
	}
}
