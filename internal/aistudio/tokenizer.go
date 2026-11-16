package aistudio

import (
	"bytes"
	"container/list"
	_ "embed"
	"hash/maphash"
	"strings"
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

// tokenCountCacheLimit 是本地 Token 数缓存的条目上限，达到上限时淘汰最久未用的条目
const tokenCountCacheLimit = 4096

// tokenCountEntry 是本地 Token 数缓存的一条记录
type tokenCountEntry struct {
	key   uint64
	count int64
}

// tokenCountCache 按文本的 maphash 缓存长文本的本地 Token 数，order 从最近使用到最久未用排列
var tokenCountCache = struct {
	sync.Mutex
	seed    maphash.Seed
	entries map[uint64]*list.Element
	order   list.List
}{seed: maphash.MakeSeed(), entries: map[uint64]*list.Element{}}

func localTextTokens(value string) int64 {
	if len(value) < tokenCountCacheMinBytes {
		return segmentedTokens(value)
	}
	key := maphash.String(tokenCountCache.seed, value)
	tokenCountCache.Lock()
	if element, ok := tokenCountCache.entries[key]; ok {
		tokenCountCache.order.MoveToFront(element)
		count := element.Value.(tokenCountEntry).count
		tokenCountCache.Unlock()
		return count
	}
	tokenCountCache.Unlock()
	count := segmentedTokens(value)
	tokenCountCache.Lock()
	defer tokenCountCache.Unlock()
	if element, ok := tokenCountCache.entries[key]; ok {
		tokenCountCache.order.MoveToFront(element)
		return count
	}
	if tokenCountCache.order.Len() >= tokenCountCacheLimit {
		oldest := tokenCountCache.order.Back()
		delete(tokenCountCache.entries, oldest.Value.(tokenCountEntry).key)
		tokenCountCache.order.Remove(oldest)
	}
	tokenCountCache.entries[key] = tokenCountCache.order.PushFront(tokenCountEntry{key: key, count: count})
	return count
}

// tokenSegmentLimit 是分段 Token 数缓存每一代的条目上限
const tokenSegmentLimit = 1 << 16

// tokenSegments 缓存文本分段的 Token 数：current 满时整体转为 previous，previous 中命中的分段移回 current
var tokenSegments = struct {
	sync.Mutex
	current, previous map[string]int32
}{current: map[string]int32{}}

// textSegments 在每个跟在非空格字符之后的空格处切分文本，"> </" 与原文 “▁” 之后的空格不切；Gemma 3 词表中只有 ">▁</" 一个片段在非空白字符之后含空格，空格归一化为 “▁” 后与原文的 “▁” 相连，各段分词的 Token 数之和与整段分词相同
func textSegments(text string) []string {
	var segments []string
	start := 0
	for i := 1; i < len(text); i++ {
		if text[i] == ' ' && text[i-1] != ' ' && !(text[i-1] == '>' && strings.HasPrefix(text[i+1:], "</")) && !strings.HasSuffix(text[:i], "▁") {
			segments = append(segments, text[start:i])
			start = i
		}
	}
	if start < len(text) {
		segments = append(segments, text[start:])
	}
	return segments
}

// rememberSegment 在持有 tokenSegments 锁时以分段的独立副本为键记下 Token 数，当代满 tokenSegmentLimit 条时先轮换
func rememberSegment(segment string, count int32) {
	if len(tokenSegments.current) >= tokenSegmentLimit {
		tokenSegments.previous, tokenSegments.current = tokenSegments.current, make(map[string]int32, tokenSegmentLimit)
	}
	tokenSegments.current[strings.Clone(segment)] = count
}

// segmentedTokens 返回 text 各段 Token 数之和，分段 Token 数取自 tokenSegments，缺少的分段在锁外分词后写回
func segmentedTokens(text string) int64 {
	segments := textSegments(text)
	counts := make([]int32, len(segments))
	var missing []int
	tokenSegments.Lock()
	for index, segment := range segments {
		if count, ok := tokenSegments.current[segment]; ok {
			counts[index] = count
		} else if count, ok := tokenSegments.previous[segment]; ok {
			counts[index] = count
			rememberSegment(segment, count)
		} else {
			missing = append(missing, index)
		}
	}
	tokenSegments.Unlock()
	encoded := make(map[string]int32, len(missing))
	for _, index := range missing {
		count, ok := encoded[segments[index]]
		if !ok {
			count = int32(len(geminiTokenizer().Encode(segments[index])))
			encoded[segments[index]] = count
		}
		counts[index] = count
	}
	if len(encoded) > 0 {
		tokenSegments.Lock()
		for segment, count := range encoded {
			rememberSegment(segment, count)
		}
		tokenSegments.Unlock()
	}
	var total int64
	for _, count := range counts {
		total += int64(count)
	}
	return total
}
