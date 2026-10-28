package aistudio

import (
	"encoding/json"
	"fmt"
	"time"
)

func validateTranscriptionConfig(config *TranscriptionConfig, model Model) error {
	if config == nil {
		return nil
	}
	checks := []struct {
		requested  bool
		capability string
		setting    string
	}{
		{config.WordTimestamps, "transcription_word_timestamps", "word timestamps"},
		{config.SpeakerLabels, "transcription_speaker_labels", "speaker labels"},
		{len(config.LanguageCodes) > 0, "transcription_language_codes", "language codes"},
		{len(config.CustomVocabulary) > 0, "transcription_custom_vocabulary", "custom vocabulary"},
		{config.SmartTranscription, "transcription_smart", "smart transcription"},
	}
	for _, check := range checks {
		if check.requested && !model.Capabilities[check.capability] {
			return fmt.Errorf("模型 %s 不支持 %s", model.ID, check.setting)
		}
	}
	return nil
}

// buildTranscriptionConfig 返回 Gemini API audioTranscriptionConfig
func buildTranscriptionConfig(config TranscriptionConfig) map[string]any {
	encoded := map[string]any{}
	if config.WordTimestamps {
		encoded["wordTimestamp"] = true
	}
	if config.SpeakerLabels {
		encoded["diarization"] = true
	}
	if len(config.LanguageCodes) > 0 {
		encoded["languageCodes"] = config.LanguageCodes
	}
	if len(config.CustomVocabulary) > 0 {
		encoded["customVocabulary"] = config.CustomVocabulary
	}
	if len(config.AdaptationPhrases) > 0 {
		encoded["adaptationPhrases"] = config.AdaptationPhrases
	}
	if len(config.LanguageHints) > 0 {
		encoded["languageHints"] = map[string]any{"languageCodes": config.LanguageHints}
	}
	if config.LanguageAuto {
		encoded["languageAuto"] = map[string]any{}
	}
	if config.SmartTranscription {
		encoded["mode"] = "SMART"
	}
	return encoded
}

func encodeTranscriptionConfig(config *TranscriptionConfig) ([]any, error) {
	if config == nil {
		return nil, nil
	}
	if config.SmartTranscription && (config.WordTimestamps || config.SpeakerLabels) {
		return nil, fmt.Errorf("smart transcription 不能同时启用 word timestamps 或 speaker labels")
	}
	var wire []any
	if config.LanguageAuto {
		wire = setWireField(wire, 1, []any{})
	}
	if len(config.LanguageHints) > 0 {
		wire = setWireField(wire, 2, []any{append([]string(nil), config.LanguageHints...)})
	}
	if len(config.AdaptationPhrases) > 0 {
		wire = setWireField(wire, 3, append([]string(nil), config.AdaptationPhrases...))
	}
	if config.WordTimestamps {
		wire = setWireField(wire, 4, int64(1))
	}
	if config.SpeakerLabels {
		wire = setWireField(wire, 5, int64(1))
	}
	if config.WordTimestamps && len(wire) < 6 {
		wire = setWireField(wire, 5, nil)
	}
	if len(config.CustomVocabulary) > 0 {
		wire = setWireField(wire, 6, append([]string(nil), config.CustomVocabulary...))
	}
	if len(config.LanguageCodes) > 0 {
		wire = setWireField(wire, 7, append([]string(nil), config.LanguageCodes...))
	}
	if config.SmartTranscription {
		wire = setWireField(wire, 8, int64(2))
	}
	return wire, nil
}

func decodeTranscriptMetadata(raw json.RawMessage, path string, evidence json.RawMessage) (TranscriptMetadata, error) {
	values, err := rawArray(raw, path, evidence)
	if err != nil {
		return TranscriptMetadata{}, withMethod(err, "GenerateContent")
	}
	metadata := TranscriptMetadata{}
	if textRaw := rawAt(values, 0); !isJSONNull(textRaw) {
		metadata.Text, err = rawString(textRaw, path+"[0]", evidence)
		if err != nil {
			return TranscriptMetadata{}, withMethod(err, "GenerateContent")
		}
	}
	if speakerRaw := rawAt(values, 1); !isJSONNull(speakerRaw) {
		metadata.Speaker, err = rawString(speakerRaw, path+"[1]", evidence)
		if err != nil {
			return TranscriptMetadata{}, withMethod(err, "GenerateContent")
		}
	}
	wordsRaw := rawAt(values, 2)
	if isJSONNull(wordsRaw) {
		return metadata, nil
	}
	words, err := rawArray(wordsRaw, path+"[2]", evidence)
	if err != nil {
		return TranscriptMetadata{}, withMethod(err, "GenerateContent")
	}
	metadata.Words = make([]TranscriptWord, 0, len(words))
	for index, wordRaw := range words {
		word, err := decodeTranscriptWord(wordRaw, fmt.Sprintf("%s[2][%d]", path, index), evidence)
		if err != nil {
			return TranscriptMetadata{}, err
		}
		metadata.Words = append(metadata.Words, word)
	}
	return metadata, nil
}

// decodeTranscriptWord 解码 AudioTranscription.WordInfo：word、start_offset、end_offset
func decodeTranscriptWord(raw json.RawMessage, path string, evidence json.RawMessage) (TranscriptWord, error) {
	values, err := rawArray(raw, path, evidence)
	if err != nil {
		return TranscriptWord{}, withMethod(err, "GenerateContent")
	}
	word := TranscriptWord{}
	if textRaw := rawAt(values, 0); !isJSONNull(textRaw) {
		if word.Word, err = rawString(textRaw, path+"[0]", evidence); err != nil {
			return TranscriptWord{}, withMethod(err, "GenerateContent")
		}
	}
	if startRaw := rawAt(values, 1); !isJSONNull(startRaw) {
		word.Start, err = decodeTranscriptDuration(startRaw, path+"[1]", evidence)
		if err != nil {
			return TranscriptWord{}, err
		}
	}
	if endRaw := rawAt(values, 2); !isJSONNull(endRaw) {
		word.End, err = decodeTranscriptDuration(endRaw, path+"[2]", evidence)
		if err != nil {
			return TranscriptWord{}, err
		}
	}
	return word, nil
}

// parseTranscriptDuration 解析 Gemini API Duration 字符串，如 "0.800s"
func parseTranscriptDuration(value string) TranscriptDuration {
	duration, err := time.ParseDuration(value)
	if err != nil {
		return TranscriptDuration{}
	}
	return TranscriptDuration{Seconds: int64(duration / time.Second), Nanos: int64(duration % time.Second)}
}

func decodeTranscriptDuration(raw json.RawMessage, path string, evidence json.RawMessage) (TranscriptDuration, error) {
	values, err := rawArray(raw, path, evidence)
	if err != nil {
		return TranscriptDuration{}, withMethod(err, "GenerateContent")
	}
	duration := TranscriptDuration{}
	if secondsRaw := rawAt(values, 0); !isJSONNull(secondsRaw) {
		duration.Seconds, err = rawInt64(secondsRaw, path+"[0]", evidence)
		if err != nil {
			return TranscriptDuration{}, withMethod(err, "GenerateContent")
		}
	}
	if nanosRaw := rawAt(values, 1); !isJSONNull(nanosRaw) {
		duration.Nanos, err = rawInt64(nanosRaw, path+"[1]", evidence)
		if err != nil {
			return TranscriptDuration{}, withMethod(err, "GenerateContent")
		}
	}
	return duration, nil
}
