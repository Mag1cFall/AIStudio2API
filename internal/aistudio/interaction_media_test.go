package aistudio

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"errors"
	"strings"
	"testing"
)

func TestDecodeInteractionMedia(t *testing.T) {
	// Minimal ftyp box with an MP4 major brand.
	mp4 := []byte("\x00\x00\x00\x18ftypmp42\x00\x00\x00\x00mp42isom")
	encoded := base64.StdEncoding.EncodeToString(mp4)
	for _, tc := range []struct {
		name     string
		wire     string
		index    int
		wantMIME string
		wantErr  bool
	}{
		{"omitted video enum", `[null,"` + encoded + `"]`, 4, "video/mp4", false},
		{"numeric video enum", `[1,"` + encoded + `"]`, 4, "video/mp4", false},
		{"numeric image enum", `[1,"aW1hZ2U="]`, 1, "image/png", false},
		{"missing image enum", `[null,"` + encoded + `"]`, 1, "", true},
		{"unknown video enum", `[9,"` + encoded + `"]`, 4, "", true},
		{"string MIME", `["video/mp4","` + encoded + `"]`, 4, "", true},
		{"bad base64", `[null,"***"]`, 4, "", true},
		{"wrong content", `[null,"aW1hZ2U="]`, 4, "", true},
		{"empty content", `[null,""]`, 4, "", true},
		{"short media", `[null]`, 4, "", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			mimes := interactionVideoMIME
			if tc.index == 1 {
				mimes = interactionImageMIME
			}
			blob, err := decodeInteractionMedia("CreateInteractionStream", json.RawMessage(tc.wire), tc.index, mimes, json.RawMessage(tc.wire))
			if tc.wantErr {
				var evidence *ProtocolEvidenceError
				if !errors.As(err, &evidence) {
					t.Fatalf("expected protocol error, got %v", err)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if blob.MIME != tc.wantMIME {
				t.Fatalf("MIME = %q", blob.MIME)
			}
			if tc.index == 4 && !bytes.Equal(blob.Data, mp4) {
				t.Fatal("video bytes changed")
			}
		})
	}
}

func TestDecodeInteractionStreamVideoWithoutMIME(t *testing.T) {
	mp4 := []byte("\x00\x00\x00\x18ftypmp42\x00\x00\x00\x00mp42isom")
	content := make([]any, 5)
	content[4] = []any{nil, base64.StdEncoding.EncodeToString(mp4)}
	delta := make([]any, 11)
	delta[10] = []any{0, content}
	final := make([]any, 20)
	final[19] = []any{[]any{nil, 3}}
	wire, err := json.Marshal([]any{[]any{delta, final}, []any{0}})
	if err != nil {
		t.Fatal(err)
	}
	var events []Event
	err = DecodeInteractionStream("CreateInteractionStream", strings.NewReader(string(wire)), func(event Event) error {
		events = append(events, event)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 2 || events[0].Kind != EventMedia || events[1].Kind != EventFinish {
		t.Fatalf("unexpected events: %#v", events)
	}
	if events[0].InlineData.MIME != "video/mp4" || !bytes.Equal(events[0].InlineData.Data, mp4) {
		t.Fatal("video corrupted")
	}
}
