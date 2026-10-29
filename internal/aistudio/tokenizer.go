package aistudio

import (
	"bytes"
	_ "embed"
	"hash/maphash"
	"sync"

	sentencepiece "github.com/Mag1cFall/go-sentencepiece"
	"github.com/klauspost/compress/zstd"
)

//go:embed gemma3.model.zst
var geminiTokenizerModel []byte

// geminiTokenizer 在首次本地计数时解析内嵌模型
var geminiTokenizer = sync.OnceValue(loadGeminiTokenizer)

func loadGeminiTokenizer() *sentencepiece.Processor {
	decoder, _ := zstd.NewReader(nil)
	model, err := decoder.DecodeAll(geminiTokenizerModel, nil)
	decoder.Close()
	if err != nil {
		panic(err)
	}
	processor, err := sentencepiece.NewProcessor(bytes.NewReader(model))
	if err != nil {
		panic(err)
	}
	return processor
}

// tokenCountCacheMinBytes 是缓存本地 Token 数的最短文本字节数
const tokenCountCacheMinBytes = 1024

// tokenCountCacheLimit 是本地 Token 数缓存的条目上限，达到上限时清空
const tokenCountCacheLimit = 4096

// tokenCountCache 按文本的 maphash 缓存长文本的本地 Token 数
var tokenCountCache = struct {
	sync.Mutex
	seed   maphash.Seed
	counts map[uint64]int64
}{seed: maphash.MakeSeed(), counts: map[uint64]int64{}}

func localTextTokens(value string) int64 {
	if len(value) < tokenCountCacheMinBytes {
		return int64(len(geminiTokenizer().Encode(value)))
	}
	key := maphash.String(tokenCountCache.seed, value)
	tokenCountCache.Lock()
	count, ok := tokenCountCache.counts[key]
	tokenCountCache.Unlock()
	if ok {
		return count
	}
	count = int64(len(geminiTokenizer().Encode(value)))
	tokenCountCache.Lock()
	if len(tokenCountCache.counts) >= tokenCountCacheLimit {
		clear(tokenCountCache.counts)
	}
	tokenCountCache.counts[key] = count
	tokenCountCache.Unlock()
	return count
}
