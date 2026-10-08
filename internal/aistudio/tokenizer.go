package aistudio

import (
	"bytes"
	"compress/gzip"
	_ "embed"
	"sync"

	sentencepiece "github.com/eliben/go-sentencepiece"
)

//go:embed gemma3.model.gz
var geminiTokenizerModel []byte

// geminiTokenizer 在首次本地计数时解析内嵌模型
var geminiTokenizer = sync.OnceValue(loadGeminiTokenizer)

func loadGeminiTokenizer() *sentencepiece.Processor {
	model, err := gzip.NewReader(bytes.NewReader(geminiTokenizerModel))
	if err != nil {
		panic(err)
	}
	processor, err := sentencepiece.NewProcessor(model)
	if err != nil {
		panic(err)
	}
	return processor
}

func localTextTokens(value string) int64 {
	return int64(len(geminiTokenizer().Encode(value)))
}
