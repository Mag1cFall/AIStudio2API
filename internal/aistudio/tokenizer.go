package aistudio

import (
	"bytes"
	_ "embed"

	sentencepiece "github.com/eliben/go-sentencepiece"
)

//go:embed gemma3.model
var geminiTokenizerModel []byte

var geminiTokenizer = loadGeminiTokenizer()

func loadGeminiTokenizer() *sentencepiece.Processor {
	processor, err := sentencepiece.NewProcessor(bytes.NewReader(geminiTokenizerModel))
	if err != nil {
		panic(err)
	}
	return processor
}

func localTextTokens(value string) int64 {
	return int64(len(geminiTokenizer.Encode(value)))
}
