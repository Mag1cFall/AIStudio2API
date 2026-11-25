package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"testing/synctest"
	"time"

	"github.com/Mag1cFall/AIStudio2API/internal/aistudio"
)

type delayedStreamFailureService struct{ aistudio.Service }

func (delayedStreamFailureService) Models(context.Context) ([]aistudio.Model, error) {
	return nil, nil
}

func (delayedStreamFailureService) Generate(ctx context.Context, _ aistudio.GenerateRequest) (<-chan aistudio.Event, error) {
	events := make(chan aistudio.Event)
	go func() {
		defer close(events)
		time.Sleep(2 * streamHeartbeatInterval)
		select {
		case events <- aistudio.Event{Kind: aistudio.EventError, Err: &aistudio.RPCError{StatusCode: 429, Message: "rate limit"}}:
		case <-ctx.Done():
		}
	}()
	return events, nil
}

func TestStreamingHandlersReturnDelayedHTTPError(t *testing.T) {
	for _, tc := range []struct{ path, body string }{
		{"/v1/images/generations", `{"model":"gemini-3.8-flash","stream":true,"prompt":"hello"}`},
		{"/v1/chat/completions", `{"model":"gemini-3.8-flash","stream":true,"messages":[{"role":"user","content":"hello"}]}`},
		{"/v1/messages", `{"model":"gemini-3.8-flash","stream":true,"max_tokens":10,"messages":[{"role":"user","content":"hello"}]}`},
		{"/v1/responses", `{"model":"gemini-3.8-flash","stream":true,"input":"hello"}`},
		{"/v1beta/models/gemini-3.8-flash:streamGenerateContent?alt=sse", `{"contents":[{"role":"user","parts":[{"text":"hello"}]}]}`},
	} {
		t.Run(tc.path, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				recorder := httptest.NewRecorder()
				request := httptest.NewRequest(http.MethodPost, tc.path, strings.NewReader(tc.body))
				request.Header.Set("Content-Type", "application/json")
				NewHandler(delayedStreamFailureService{}, Config{}).ServeHTTP(recorder, request)
				if recorder.Code != 429 || recorder.Result().Header.Get("Content-Type") != "application/json" || !json.Valid(recorder.Body.Bytes()) {
					t.Fatalf("status=%d headers=%v body=%s", recorder.Code, recorder.Result().Header, recorder.Body.String())
				}
			})
		})
	}
}

func TestAwaitStreamStartDelayedError(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		events := make(chan aistudio.Event)
		want := errors.New("delayed upstream failure")
		go func() {
			time.Sleep(2 * streamHeartbeatInterval)
			events <- aistudio.Event{Kind: aistudio.EventError, Err: want}
			close(events)
		}()
		replay, usage, err := awaitStreamStart(t.Context(), events)
		if !errors.Is(err, want) || replay != nil || usage != nil {
			t.Fatalf("replay=%v usage=%v err=%v", replay, usage, err)
		}
	})
}

func TestAwaitStreamStartReplayAndCancel(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		ctx, cancel := context.WithCancel(t.Context())
		defer cancel()
		events := make(chan aistudio.Event, 1)
		usage := &aistudio.Usage{InputTokens: 12}
		events <- aistudio.Event{Kind: aistudio.EventText, Part: aistudio.Part{Text: "hello"}, FrameUsage: usage}
		replay, gotUsage, err := awaitStreamStart(ctx, events)
		if err != nil || gotUsage != usage {
			t.Fatalf("usage=%v err=%v", gotUsage, err)
		}
		if first := <-replay; first.Text != "hello" {
			t.Fatalf("first=%+v", first)
		}
		synctest.Wait()
		cancel()
		if _, ok := <-replay; ok {
			t.Fatal("replay must close on cancellation even if upstream stays open")
		}
	})
}

func TestAwaitStreamStartClosedOrCanceled(t *testing.T) {
	events := make(chan aistudio.Event)
	close(events)
	if _, _, err := awaitStreamStart(t.Context(), events); !errors.Is(err, errIncompleteStream) {
		t.Fatalf("err=%v", err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, _, err := awaitStreamStart(ctx, make(chan aistudio.Event)); !errors.Is(err, context.Canceled) {
		t.Fatalf("err=%v", err)
	}
}

func TestAwaitStreamStartReplaysAllEvents(t *testing.T) {
	events := make(chan aistudio.Event, 3)
	events <- aistudio.Event{Kind: aistudio.EventText, Part: aistudio.Part{Text: "first"}}
	events <- aistudio.Event{Kind: aistudio.EventText, Part: aistudio.Part{Text: "second"}}
	events <- aistudio.Event{Kind: aistudio.EventFinish, FinishReason: "stop"}
	close(events)
	replay, _, err := awaitStreamStart(t.Context(), events)
	if err != nil {
		t.Fatal(err)
	}
	result, err := consumeEvents(t.Context(), replay)
	if err != nil || result.text.String() != "firstsecond" || !result.finished {
		t.Fatalf("text=%q finished=%v err=%v", result.text.String(), result.finished, err)
	}
}

type delayedTranscriptionFailure struct{ emit bool }

func (service delayedTranscriptionFailure) Transcribe(_ context.Context, request aistudio.TranscriptionRequest) (aistudio.TranscriptionResult, error) {
	if service.emit {
		if err := request.Emit(aistudio.Event{Kind: aistudio.EventText, Part: aistudio.Part{Text: "hello"}}); err != nil {
			return aistudio.TranscriptionResult{}, err
		}
	}
	time.Sleep(2 * streamHeartbeatInterval)
	return aistudio.TranscriptionResult{}, &aistudio.RPCError{StatusCode: 429, Message: "rate limit"}
}

func TestTranscriptionErrorBeforeAndAfterStreamStart(t *testing.T) {
	for _, emit := range []bool{false, true} {
		synctest.Test(t, func(t *testing.T) {
			recorder := httptest.NewRecorder()
			request := httptest.NewRequest(http.MethodPost, "/v1/audio/transcriptions", nil)
			streamTranscription(recorder, request, delayedTranscriptionFailure{emit: emit}, aistudio.TranscriptionRequest{}, "json")
			if !emit {
				if recorder.Code != 429 || recorder.Result().Header.Get("Content-Type") != "application/json" || !json.Valid(recorder.Body.Bytes()) {
					t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
				}
			} else if recorder.Code != 200 || recorder.Result().Header.Get("Content-Type") != "text/event-stream" || !strings.Contains(recorder.Body.String(), "rate_limit_exceeded") || strings.Contains(recorder.Body.String(), "transcript.text.done") {
				t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
			}
		})
	}
}

func TestStreamKeepaliveWaitsForOpen(t *testing.T) {
	for _, frame := range []string{sseHeartbeat, anthropicHeartbeat, geminiHeartbeat} {
		synctest.Test(t, func(t *testing.T) {
			recorder := httptest.NewRecorder()
			stream := keepStreamAlive(recorder, frame)
			defer stream.stop()
			time.Sleep(2 * streamHeartbeatInterval)
			if recorder.Flushed || recorder.Body.Len() != 0 || stream.opened {
				t.Fatal("heartbeat committed an unopened stream")
			}
			if err := stream.open(); err != nil {
				t.Fatal(err)
			}
			time.Sleep(streamHeartbeatInterval)
			synctest.Wait()
			if !stream.stop() {
				t.Fatal("opened stream not recorded")
			}
			if recorder.Result().Header.Get("Content-Type") != "text/event-stream" || recorder.Body.String() != frame {
				t.Fatalf("unexpected response: %v %q", recorder.Header(), recorder.Body.String())
			}
			time.Sleep(2 * streamHeartbeatInterval)
			if recorder.Body.String() != frame {
				t.Fatal("heartbeat continued after stop")
			}
		})
	}
}
