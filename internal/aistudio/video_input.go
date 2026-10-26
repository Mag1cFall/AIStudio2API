package aistudio

import (
	"fmt"
	"math"
	"regexp"
	"strconv"
	"strings"
)

var videoOffsetPattern = regexp.MustCompile(`^([0-9]+)(?:\.([0-9]{1,9}))?s$`)

// videoOffset parses the protobuf Duration representation used for video offsets.
func videoOffset(value string) (int64, int64, error) {
	match := videoOffsetPattern.FindStringSubmatch(value)
	if match == nil {
		return 0, 0, fmt.Errorf("video offset must be a nonnegative duration in seconds: %q", value)
	}
	seconds, err := strconv.ParseInt(match[1], 10, 64)
	if err != nil {
		return 0, 0, fmt.Errorf("video offset: %w", err)
	}
	var nanos int64
	if match[2] != "" {
		nanos, _ = strconv.ParseInt(match[2]+strings.Repeat("0", 9-len(match[2])), 10, 64)
	}
	return seconds, nanos, nil
}

// videoPartOptions validates and encodes the metadata shared by video media parts.
func videoPartOptions(part Part) ([]any, int64, error) {
	var metadata []any
	if video := part.VideoMetadata; video != nil {
		var startSeconds, startNanos int64
		if video.StartOffset != "" {
			seconds, nanos, err := videoOffset(video.StartOffset)
			if err != nil {
				return nil, 0, err
			}
			startSeconds, startNanos = seconds, nanos
			metadata = []any{videoDurationWire(seconds, nanos)}
		}
		if video.EndOffset != "" {
			seconds, nanos, err := videoOffset(video.EndOffset)
			if err != nil {
				return nil, 0, err
			}
			if seconds < startSeconds || seconds == startSeconds && nanos <= startNanos {
				return nil, 0, fmt.Errorf("video end offset must be greater than its start offset")
			}
			for len(metadata) < 2 {
				metadata = append(metadata, nil)
			}
			metadata[1] = videoDurationWire(seconds, nanos)
		}
		if video.FPS != nil {
			fps := *video.FPS
			if math.IsNaN(fps) || math.IsInf(fps, 0) || fps <= 0 || fps > 24 {
				return nil, 0, fmt.Errorf("video fps must be greater than 0 and at most 24")
			}
			for len(metadata) < 3 {
				metadata = append(metadata, nil)
			}
			metadata[2] = fps
		}
	}
	var processing int64
	switch part.MediaProcessing {
	case "", "MEDIA_PROCESSING_UNSPECIFIED":
	case "STATIC":
		processing = 1
	case "AGENTIC":
		if len(metadata) != 0 {
			return nil, 0, fmt.Errorf("agentic video processing cannot use static clipping or fps")
		}
		processing = 2
	default:
		return nil, 0, fmt.Errorf("unknown video mediaProcessing %q", part.MediaProcessing)
	}
	if len(metadata) != 0 || processing != 0 {
		mime := ""
		switch {
		case part.InlineData != nil:
			mime = part.InlineData.MIME
		case part.ExternalMedia != nil:
			mime = part.ExternalMedia.MIME
		case part.File != nil:
			mime = part.File.MIME
		case part.Upload != nil:
			mime = part.Upload.MIME
		default:
			return nil, 0, fmt.Errorf("video metadata requires a video media part")
		}
		if !strings.HasPrefix(mime, "video/") && !(mime == "" && part.File != nil) {
			return nil, 0, fmt.Errorf("video metadata requires a video MIME type")
		}
	}
	return metadata, processing, nil
}

// videoDurationWire encodes seconds and optional nanoseconds without losing zero.
func videoDurationWire(seconds, nanos int64) []any {
	if nanos == 0 {
		return []any{seconds}
	}
	return []any{seconds, nanos}
}

// encodeVideoPart attaches input-video options and the existing thought signature.
func encodeVideoPart(wire []any, part Part) ([]any, error) {
	metadata, processing, err := videoPartOptions(part)
	if err != nil {
		return nil, err
	}
	if len(metadata) > 0 {
		for len(wire) < 16 {
			wire = append(wire, nil)
		}
		wire[15] = metadata
	}
	if processing != 0 {
		for len(wire) < 40 {
			wire = append(wire, nil)
		}
		wire[39] = processing
	}
	return setPartThoughtSignature(wire, part.ThoughtSignature), nil
}

// validateVideoInputs checks requested agentic processing against the live model.
func validateVideoInputs(contents []Content, model Model) error {
	for _, content := range contents {
		for _, part := range content.Parts {
			if _, _, err := videoPartOptions(part); err != nil {
				return err
			}
			if part.MediaProcessing == "AGENTIC" && !model.Capabilities["agentic_video"] {
				return fmt.Errorf("model %s does not support agentic video processing", model.ID)
			}
		}
	}
	return nil
}
