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

// ContextWithStreamActivityObserver 记录上游响应体实际到达的字节，ctx 已有的观察函数照常收到同样的字节
func ContextWithStreamActivityObserver(ctx context.Context, observer func(int)) context.Context {
	if parent, _ := ctx.Value(streamActivityContextKey{}).(func(int)); parent != nil {
		own := observer
		observer = func(count int) {
			parent(count)
			own(count)
		}
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
