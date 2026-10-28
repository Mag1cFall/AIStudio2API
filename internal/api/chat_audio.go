package api

import (
	"cmp"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/Mag1cFall/AIStudio2API/internal/aistudio"
)

// chatAudioConfig 是 Chat 请求 audio 参数的声音与输出格式
type chatAudioConfig struct {
	Voice  json.RawMessage `json:"voice"`
	Format string          `json:"format"`
}

// chatAudioFormats 是 Chat audio.format 对应的语音编码格式
var chatAudioFormats = map[string]string{"wav": "wav", "mp3": "mp3", "flac": "flac", "opus": "opus", "aac": "aac", "pcm16": "pcm"}

// chatAudioTTL 是 message.audio.expires_at 距生成时间的时长
const chatAudioTTL = time.Hour

// audioOutput 返回音频输出的编码格式与声音：modalities 含 audio，或未设置 modalities 而设置了 audio 时输出音频，格式默认为 wav
func (request chatRequest) audioOutput() (string, string, error) {
	requested := slices.Contains(request.Modalities, "audio") || len(request.Modalities) == 0 && request.Audio != nil
	if !requested {
		return "", "", nil
	}
	if request.Audio == nil {
		return "wav", "", nil
	}
	format, ok := chatAudioFormats[cmp.Or(strings.ToLower(strings.TrimSpace(request.Audio.Format)), "wav")]
	if !ok {
		return "", "", fmt.Errorf("audio.format must be wav, mp3, flac, opus, aac or pcm16")
	}
	var voice string
	if rawJSONConfigured(request.Audio.Voice) && json.Unmarshal(request.Audio.Voice, &voice) != nil {
		var custom struct {
			ID string `json:"id"`
		}
		if err := json.Unmarshal(request.Audio.Voice, &custom); err != nil {
			return "", "", fmt.Errorf("audio.voice must be a voice name or an object with id")
		}
		voice = custom.ID
	}
	return format, voice, nil
}

// chatAudioMessage 编码 message.audio：音频按 format 编码，正文作为 transcript
func chatAudioMessage(id string, media []aistudio.Blob, format string, transcript string) (map[string]any, error) {
	joined, err := joinedAudio(media)
	if err != nil {
		return nil, err
	}
	data, _, err := encodeSpeechResponse(joined, format)
	if err != nil {
		return nil, err
	}
	return map[string]any{
		"id": id, "data": base64.StdEncoding.EncodeToString(data),
		"expires_at": time.Now().Add(chatAudioTTL).Unix(), "transcript": transcript,
	}, nil
}

// takeChatAudio 从结果中移出音频事件并返回 message.audio，结果没有音频时返回 nil
func takeChatAudio(result *generationResult, format string) (map[string]any, error) {
	var media []aistudio.Blob
	events := slices.DeleteFunc(slices.Clone(result.events), func(event aistudio.Event) bool {
		if audioEvent(event) {
			media = append(media, *event.InlineData)
			return true
		}
		return false
	})
	if len(media) == 0 {
		return nil, nil
	}
	result.events = events
	return chatAudioMessage(newID("audio"), media, format, result.text.String())
}
