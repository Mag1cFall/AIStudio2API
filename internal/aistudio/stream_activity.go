package aistudio

import (
	"context"
	"io"
)

type streamActivityContextKey struct{}

type streamActivityReader struct {
	source io.Reader
	notify func(int)
}

// ContextWithStreamActivityObserver 记录上游响应体实际到达的字节
func ContextWithStreamActivityObserver(ctx context.Context, observer func(int)) context.Context {
	if observer == nil {
		return ctx
	}
	return context.WithValue(ctx, streamActivityContextKey{}, observer)
}

func observeStreamActivity(ctx context.Context, source io.Reader) io.Reader {
	observer, _ := ctx.Value(streamActivityContextKey{}).(func(int))
	if observer == nil {
		return source
	}
	return &streamActivityReader{source: source, notify: observer}
}

func (reader *streamActivityReader) Read(buffer []byte) (int, error) {
	count, err := reader.source.Read(buffer)
	if count > 0 {
		reader.notify(count)
	}
	return count, err
}
