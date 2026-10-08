package api

import (
	"bytes"
	"crypto/md5"
	"encoding/binary"
	"fmt"
	"math"
	"mime"
	"strconv"

	"github.com/mewkiz/flac"
	"github.com/mewkiz/flac/frame"
	"github.com/mewkiz/flac/meta"
	"github.com/oov/audio/resampler"
	"github.com/thesyncim/gopus"
	"github.com/thesyncim/gopus/container/ogg"
	aacpcm "github.com/tphakala/go-aac/pcm"
	mp3 "github.com/tphakala/go-mp3"
	mp3pcm "github.com/tphakala/go-mp3/pcm"
	"github.com/zaf/g711"

	"github.com/Mag1cFall/AIStudio2API/internal/aistudio"
)

// audioPCM 表示 16-bit 小端交错 PCM 及其采样率与声道数
type audioPCM struct {
	Data       []byte
	SampleRate int
	Channels   int
}

// audioOutput 表示目标编码、采样率与码率，采样率为零时沿用源音频，码率为零时使用编码器默认值
type audioOutput struct {
	Format     string
	SampleRate int
	BitRate    int
}

// resamplerQuality 为 Speex 重采样器的最高质量档
const resamplerQuality = 10

// mp3SampleRates 为 MPEG-1 Layer III 编码器接受的输入采样率
var mp3SampleRates = []int{32000, 44100, 48000}

// mp3BitRates 为 MPEG-1 Layer III 的 CBR 码率
var mp3BitRates = []int{32000, 40000, 48000, 56000, 64000, 80000, 96000, 112000, 128000, 160000, 192000, 224000, 256000, 320000}

// aacSampleRates 为 AAC-LC 编码器接受的输入采样率
var aacSampleRates = []int{44100, 48000}

// opusSampleRates 为 Opus 编码器接受的输入采样率
var opusSampleRates = []int{8000, 12000, 16000, 24000, 48000}

// mediaPCM 解析上游 audio/l16、PCM16 WAV 或解码 MP3 音频
func mediaPCM(media aistudio.Media) (audioPCM, error) {
	baseType, parameters, err := mime.ParseMediaType(media.MIME)
	if err != nil {
		return audioPCM{}, fmt.Errorf("AI Studio returned invalid audio MIME %q", media.MIME)
	}
	if baseType == "audio/mpeg" {
		data, info, err := mp3pcm.DecodeInterleaved(bytes.NewReader(media.Data))
		if err != nil {
			return audioPCM{}, fmt.Errorf("AI Studio returned undecodable MP3 audio: %w", err)
		}
		return audioPCM{Data: data, SampleRate: info.SampleRate, Channels: info.Channels}, nil
	}
	if baseType == "audio/wav" || baseType == "audio/x-wav" {
		if media, err = wavPCM(media); err != nil {
			return audioPCM{}, err
		}
		baseType, parameters, _ = mime.ParseMediaType(media.MIME)
	}
	if baseType != "audio/l16" {
		return audioPCM{}, fmt.Errorf("AI Studio returned %s, which is not PCM audio", media.MIME)
	}
	rate, err := strconv.Atoi(parameters["rate"])
	if err != nil || rate <= 0 {
		return audioPCM{}, fmt.Errorf("AI Studio audio MIME is missing a valid rate")
	}
	channels := 1
	if value := parameters["channels"]; value != "" {
		if channels, err = strconv.Atoi(value); err != nil || channels <= 0 {
			return audioPCM{}, fmt.Errorf("AI Studio audio MIME has invalid channels")
		}
	}
	return audioPCM{Data: media.Data, SampleRate: rate, Channels: channels}, nil
}

// encodeAudio 把 PCM 重采样到目标或编码器支持的采样率后编码，返回编码数据与编码时的 PCM 参数
func encodeAudio(audio audioPCM, output audioOutput) ([]byte, audioPCM, error) {
	rate := output.SampleRate
	if rate == 0 {
		rate = audio.SampleRate
	}
	switch output.Format {
	case "mp3":
		rate = supportedSampleRate(rate, mp3SampleRates)
	case "aac":
		rate = supportedSampleRate(rate, aacSampleRates)
	case "opus":
		rate = supportedSampleRate(rate, opusSampleRates)
	}
	audio = resamplePCM(audio, rate)
	var data []byte
	var err error
	switch output.Format {
	case "pcm":
		data = audio.Data
	case "mp3":
		data, err = encodeMP3(audio, output.BitRate)
	case "aac":
		data, err = encodeAAC(audio, output.BitRate)
	case "opus":
		data, err = encodeOggOpus(audio, output.BitRate)
	case "flac":
		data, err = encodeFLAC(audio)
	case "alaw":
		data = g711.EncodeAlaw(audio.Data)
	case "mulaw":
		data = g711.EncodeUlaw(audio.Data)
	}
	return data, audio, err
}

// supportedSampleRate 返回不低于目标的最小支持采样率，目标高于全部取值时返回最高采样率
func supportedSampleRate(rate int, rates []int) int {
	for _, value := range rates {
		if value >= rate {
			return value
		}
	}
	return rates[len(rates)-1]
}

// resamplePCM 用 Speex 重采样器逐声道转换采样率，起止的滤波延迟已补偿
func resamplePCM(audio audioPCM, rate int) audioPCM {
	if rate == audio.SampleRate {
		return audio
	}
	frames := len(audio.Data) / 2 / audio.Channels
	outFrames := (frames*rate + audio.SampleRate - 1) / audio.SampleRate
	converter := resampler.NewWithSkipZeros(audio.Channels, audio.SampleRate, rate, resamplerQuality)
	input := make([]float64, frames+converter.InputLatency())
	output := make([]float64, outFrames)
	data := make([]byte, outFrames*audio.Channels*2)
	for channel := range audio.Channels {
		for frame := range frames {
			input[frame] = float64(int16(binary.LittleEndian.Uint16(audio.Data[(frame*audio.Channels+channel)*2:])))
		}
		_, written := converter.ProcessFloat64(channel, input, output)
		for frame, value := range output[:written] {
			sample := int16(math.Round(max(math.MinInt16, min(math.MaxInt16, value))))
			binary.LittleEndian.PutUint16(data[(frame*audio.Channels+channel)*2:], uint16(sample))
		}
		outFrames = min(outFrames, written)
	}
	return audioPCM{Data: data[:outFrames*audio.Channels*2], SampleRate: rate, Channels: audio.Channels}
}

// mp3DefaultBitRate 为未指定码率时的 MP3 码率
const mp3DefaultBitRate = 192000

// encodeMP3 以最接近目标码率的 CBR 码率编码 MPEG-1 Layer III，码率为零时使用 mp3DefaultBitRate，码率搜索使用快速模式
func encodeMP3(audio audioPCM, bitRate int) ([]byte, error) {
	if bitRate == 0 {
		bitRate = mp3DefaultBitRate
	}
	nearest := mp3BitRates[0]
	for _, value := range mp3BitRates {
		if math.Abs(float64(value-bitRate)) < math.Abs(float64(nearest-bitRate)) {
			nearest = value
		}
	}
	bitRate = nearest
	encoder, err := mp3.NewEncoder(mp3.EncoderConfig{SampleRate: audio.SampleRate, Channels: audio.Channels, Bitrate: bitRate, RateControl: mp3.RateControlFast})
	if err != nil {
		return nil, err
	}
	frames := len(audio.Data) / 2 / audio.Channels
	buffers := make([][]float32, audio.Channels)
	planar := make([][]float32, audio.Channels)
	for channel := range buffers {
		buffers[channel] = make([]float32, mp3.FrameSize)
	}
	var output []byte
	for start := 0; start < frames; start += mp3.FrameSize {
		count := min(mp3.FrameSize, frames-start)
		for channel := range planar {
			planar[channel] = buffers[channel][:count]
			for index := range count {
				planar[channel][index] = float32(int16(binary.LittleEndian.Uint16(audio.Data[((start+index)*audio.Channels+channel)*2:]))) / 32768
			}
		}
		if output, err = encoder.EncodeFrame(output, planar); err != nil {
			return nil, err
		}
	}
	return encoder.EncodeFrame(output, nil)
}

// encodeAAC 编码 ADTS 封装的 AAC-LC，码率为零时使用编码器默认值
func encodeAAC(audio audioPCM, bitRate int) ([]byte, error) {
	var output bytes.Buffer
	err := aacpcm.EncodeInterleaved(&output, aacpcm.Config{SampleRate: audio.SampleRate, BitDepth: 16, Channels: audio.Channels, Bitrate: bitRate}, audio.Data)
	return output.Bytes(), err
}

// encodeOggOpus 以 20 ms 帧编码 Ogg Opus，pre-skip 为编码器前瞻，末页 granule 截去补齐的静音
func encodeOggOpus(audio audioPCM, bitRate int) ([]byte, error) {
	encoder, err := gopus.NewEncoder(gopus.EncoderConfig{SampleRate: audio.SampleRate, Channels: audio.Channels, Application: gopus.ApplicationAudio})
	if err != nil {
		return nil, err
	}
	if bitRate > 0 {
		if err := encoder.SetBitrate(bitRate); err != nil {
			return nil, err
		}
	}
	scale := 48000 / audio.SampleRate
	preSkip := encoder.Lookahead() * scale
	var output bytes.Buffer
	writer, err := ogg.NewWriterWithConfig(&output, ogg.WriterConfig{
		SampleRate: uint32(audio.SampleRate), Channels: uint8(audio.Channels), PreSkip: uint16(preSkip), StreamCount: 1, CoupledCount: uint8(audio.Channels - 1),
	})
	if err != nil {
		return nil, err
	}
	samples := make([]int16, len(audio.Data)/2)
	_ = binary.Read(bytes.NewReader(audio.Data), binary.LittleEndian, samples)
	frameSize := audio.SampleRate / 50
	end := preSkip + len(samples)/audio.Channels*scale
	frame := make([]int16, frameSize*audio.Channels)
	packet := make([]byte, 4000)
	for start, granule := 0, 0; granule < end; start += len(frame) {
		clear(frame)
		if start < len(samples) {
			copy(frame, samples[start:])
		}
		count, err := encoder.EncodeInt16(frame, packet)
		if err != nil {
			return nil, err
		}
		duration := min(frameSize*scale, end-granule)
		if err := writer.WritePacket(packet[:count], duration); err != nil {
			return nil, err
		}
		granule += duration
	}
	if err := writer.Close(); err != nil {
		return nil, err
	}
	return output.Bytes(), nil
}

// encodeFLAC 以 4096 样本定长块编码 16-bit FLAC，STREAMINFO 写入总样本数与 PCM 的 MD5
func encodeFLAC(audio audioPCM) ([]byte, error) {
	const blockSize = 4096
	frames := len(audio.Data) / 2 / audio.Channels
	var output bytes.Buffer
	encoder, err := flac.NewEncoder(&output, &meta.StreamInfo{
		BlockSizeMin: blockSize, BlockSizeMax: blockSize, SampleRate: uint32(audio.SampleRate),
		NChannels: uint8(audio.Channels), BitsPerSample: 16, NSamples: uint64(frames), MD5sum: md5.Sum(audio.Data),
	})
	if err != nil {
		return nil, err
	}
	for start := 0; start < frames; start += blockSize {
		count := min(blockSize, frames-start)
		subframes := make([]*frame.Subframe, audio.Channels)
		for channel := range subframes {
			samples := make([]int32, count)
			for index := range samples {
				samples[index] = int32(int16(binary.LittleEndian.Uint16(audio.Data[((start+index)*audio.Channels+channel)*2:])))
			}
			subframes[channel] = &frame.Subframe{SubHeader: frame.SubHeader{Pred: frame.PredVerbatim}, NSamples: count, Samples: samples}
		}
		if err := encoder.WriteFrame(&frame.Frame{Header: frame.Header{
			HasFixedBlockSize: true, BlockSize: uint16(count), SampleRate: uint32(audio.SampleRate),
			Channels: frame.Channels(audio.Channels - 1), BitsPerSample: 16,
		}, Subframes: subframes}); err != nil {
			return nil, err
		}
	}
	if err := encoder.Close(); err != nil {
		return nil, err
	}
	return output.Bytes(), nil
}

// audioContentTypes 为 OpenAI Speech 中需要转码的 response_format 对应的 Content-Type
var audioContentTypes = map[string]string{"mp3": "audio/mpeg", "opus": "audio/opus", "aac": "audio/aac", "flac": "audio/flac"}
