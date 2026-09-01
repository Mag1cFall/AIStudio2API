package api

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"strings"
	"time"

	"github.com/Mag1cFall/AIStudio2API/internal/aistudio"
	"github.com/gorilla/websocket"
)

const (
	bidiWriteTimeout   = 10 * time.Second
	bidiCloseTimeout   = 5 * time.Second
	bidiSetupTimeout   = 10 * time.Second
	bidiMaxClientFrame = 8 << 20
)

var bidiUpgrader = websocket.Upgrader{
	CheckOrigin: func(*http.Request) bool { return true },
}

type bidiClientMessage struct {
	Type          string                    `json:"type"`
	Text          string                    `json:"text,omitempty"`
	MIMEType      string                    `json:"mime_type,omitempty"`
	Data          []byte                    `json:"data,omitempty"`
	ToolResponses []aistudio.FunctionResult `json:"tool_responses,omitempty"`
}

type bidiClientSetup struct {
	Type             string                         `json:"type"`
	Model            string                         `json:"model"`
	InputModalities  []string                       `json:"input_modalities"`
	OutputModalities []string                       `json:"output_modalities"`
	Tools            []aistudio.FunctionDeclaration `json:"tools"`
	SessionToken     string                         `json:"session_token,omitempty"`
}

type bidiServerMessage struct {
	Type          string                      `json:"type"`
	Model         string                      `json:"model,omitempty"`
	Text          string                      `json:"text,omitempty"`
	MIMEType      string                      `json:"mime_type,omitempty"`
	Data          []byte                      `json:"data,omitempty"`
	Transcription *aistudio.BidiTranscription `json:"transcription,omitempty"`
	ToolCall      *aistudio.FunctionCall      `json:"tool_call,omitempty"`
	ToolCallIDs   []string                    `json:"tool_call_ids,omitempty"`
	SessionToken  string                      `json:"session_token,omitempty"`
	Resumable     bool                        `json:"resumable,omitempty"`
	Raw           json.RawMessage             `json:"raw,omitempty"`
	Error         string                      `json:"error,omitempty"`
	Code          string                      `json:"code,omitempty"`
	Retryable     bool                        `json:"retryable,omitempty"`
}

// Hijack 让 WebSocket upgrade 穿过访问日志响应包装器
func (writer *accessLogResponseWriter) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	connection, readWriter, err := http.NewResponseController(writer.ResponseWriter).Hijack()
	if err == nil && writer.status == 0 {
		writer.status = http.StatusSwitchingProtocols
	}
	return connection, readWriter, err
}

func (s *server) handleGeminiLive(w http.ResponseWriter, r *http.Request) {
	s.serveBidi(w, r, aistudio.BidiModeLive)
}

func (s *server) handleRoboticsStream(w http.ResponseWriter, r *http.Request) {
	s.serveBidi(w, r, aistudio.BidiModeRobotics)
}

func (s *server) serveBidi(w http.ResponseWriter, r *http.Request, mode aistudio.BidiMode) {
	service, ok := s.service.(aistudio.BidiService)
	if !ok {
		writeOpenAIError(w, http.StatusNotImplemented, "unsupported_feature", "bidi service is unavailable")
		return
	}
	connection, err := bidiUpgrader.Upgrade(w, r, nil)
	if err != nil {
		return
	}
	connection.SetReadLimit(bidiMaxClientFrame)
	if err := connection.SetReadDeadline(time.Now().Add(bidiSetupTimeout)); err != nil {
		_ = connection.Close()
		return
	}
	var setup bidiClientSetup
	if err := connection.ReadJSON(&setup); err != nil {
		if errors.Is(err, websocket.ErrReadLimit) {
			_ = connection.WriteControl(
				websocket.CloseMessage,
				websocket.FormatCloseMessage(websocket.CloseMessageTooBig, "message too big"),
				time.Now().Add(bidiWriteTimeout),
			)
		}
		_ = connection.Close()
		return
	}
	request, inputModalities, err := bidiRequestFromSetup(mode, setup)
	if err != nil {
		SetAccessLogError(r.Context(), err)
		_ = writeBidiJSON(connection, bidiServerMessage{Type: "error", Code: "invalid_request", Error: err.Error()})
		_ = connection.Close()
		return
	}
	if err := connection.SetReadDeadline(time.Time{}); err != nil {
		_ = connection.Close()
		return
	}
	startedAt := time.Now()
	ctx, cancel := context.WithCancel(r.Context())
	defer cancel()
	clientMessages := make(chan bidiClientMessage, 8)
	readerDone := make(chan struct{})
	go readBidiClientMessages(ctx, cancel, connection, clientMessages, readerDone)
	session, err := service.OpenBidi(ctx, request)
	if err != nil {
		SetAccessLogError(r.Context(), err)
		_ = writeBidiJSON(connection, bidiErrorMessage(err))
		_ = connection.Close()
		cancel()
		waitBidiRoutine(readerDone)
		return
	}
	clientErrors := make(chan error, 1)
	writerDone := make(chan struct{})
	senderDone := make(chan struct{})
	go s.writeBidiEvents(
		ctx, connection, r, session, startedAt, request.ModelAccessScope != "", clientErrors, writerDone,
	)
	go sendBidiClientMessages(
		ctx, cancel, session, inputModalities, len(request.Tools) > 0, clientMessages, clientErrors, senderDone,
	)
	<-writerDone
	cancel()
	if closeErr := session.Close(); closeErr != nil {
		SetAccessLogError(r.Context(), closeErr)
	}
	_ = connection.Close()
	waitBidiRoutine(readerDone)
	waitBidiRoutine(senderDone)
}

func bidiRequestFromSetup(
	mode aistudio.BidiMode,
	setup bidiClientSetup,
) (aistudio.BidiRequest, map[string]bool, error) {
	if setup.Type != "setup" {
		return aistudio.BidiRequest{}, nil, fmt.Errorf("%w: 首帧 type 必须是 setup", aistudio.ErrInvalidArgument)
	}
	model := strings.TrimSpace(setup.Model)
	if model == "" {
		return aistudio.BidiRequest{}, nil, fmt.Errorf("%w: bidi model 不能为空", aistudio.ErrInvalidArgument)
	}
	input, err := bidiModalitySet(setup.InputModalities)
	if err != nil {
		return aistudio.BidiRequest{}, nil, err
	}
	output, err := bidiModalitySet(setup.OutputModalities)
	if err != nil {
		return aistudio.BidiRequest{}, nil, err
	}
	request := aistudio.BidiRequest{
		Model: model, Mode: mode, Tools: setup.Tools, SessionToken: strings.TrimSpace(setup.SessionToken),
	}
	switch mode {
	case aistudio.BidiModeLive:
		for modality := range input {
			if modality != "text" && modality != "audio" && modality != "image" {
				return aistudio.BidiRequest{}, nil, fmt.Errorf("%w: live input modality %q 不可用", aistudio.ErrInvalidArgument, modality)
			}
		}
		if len(output) != 1 || !output["audio"] {
			return aistudio.BidiRequest{}, nil, fmt.Errorf("%w: live output_modalities 必须是 [audio]", aistudio.ErrInvalidArgument)
		}
		if input["audio"] || input["image"] {
			request.ModelAccessScope = aistudio.ModelAccessKey("bidi-media", model)
		}
	case aistudio.BidiModeRobotics:
		if len(input) != 1 || !input["text"] {
			return aistudio.BidiRequest{}, nil, fmt.Errorf("%w: robotics input_modalities 必须是 [text]", aistudio.ErrInvalidArgument)
		}
		if len(output) != 1 || !output["text"] {
			return aistudio.BidiRequest{}, nil, fmt.Errorf("%w: robotics output_modalities 必须是 [text]", aistudio.ErrInvalidArgument)
		}
		request.ModelAccessScope = aistudio.ModelAccessKey("bidi-media", model)
	default:
		return aistudio.BidiRequest{}, nil, fmt.Errorf("%w: bidi mode %q 无效", aistudio.ErrInvalidArgument, mode)
	}
	if _, _, err := aistudio.EncodeBidiSetupRequest(request, aistudio.RequestContext{}); err != nil {
		return aistudio.BidiRequest{}, nil, fmt.Errorf("%w: %v", aistudio.ErrInvalidArgument, err)
	}
	return request, input, nil
}

func bidiModalitySet(values []string) (map[string]bool, error) {
	if len(values) == 0 {
		return nil, fmt.Errorf("%w: modality 列表不能为空", aistudio.ErrInvalidArgument)
	}
	result := make(map[string]bool, len(values))
	for _, value := range values {
		value = strings.ToLower(strings.TrimSpace(value))
		if value == "" || result[value] {
			return nil, fmt.Errorf("%w: modality %q 无效", aistudio.ErrInvalidArgument, value)
		}
		result[value] = true
	}
	return result, nil
}

func readBidiClientMessages(
	ctx context.Context,
	cancel context.CancelFunc,
	connection *websocket.Conn,
	messages chan<- bidiClientMessage,
	done chan<- struct{},
) {
	defer close(done)
	defer close(messages)
	defer cancel()
	for {
		var message bidiClientMessage
		if err := connection.ReadJSON(&message); err != nil {
			if errors.Is(err, websocket.ErrReadLimit) {
				_ = connection.WriteControl(
					websocket.CloseMessage,
					websocket.FormatCloseMessage(websocket.CloseMessageTooBig, "message too big"),
					time.Now().Add(bidiWriteTimeout),
				)
			}
			return
		}
		select {
		case messages <- message:
		case <-ctx.Done():
			return
		}
	}
}

func sendBidiClientMessages(
	ctx context.Context,
	cancel context.CancelFunc,
	session *aistudio.BidiSession,
	inputModalities map[string]bool,
	toolsEnabled bool,
	messages <-chan bidiClientMessage,
	errors chan<- error,
	done chan<- struct{},
) {
	defer close(done)
	for {
		select {
		case <-ctx.Done():
			return
		case message, ok := <-messages:
			if !ok {
				return
			}
			var err error
			switch message.Type {
			case "text":
				if !inputModalities["text"] {
					err = fmt.Errorf("%w: text 未在 setup input_modalities 中声明", aistudio.ErrInvalidArgument)
				} else {
					err = session.SendText(ctx, message.Text)
				}
			case "audio":
				if !inputModalities["audio"] {
					err = fmt.Errorf("%w: audio 未在 setup input_modalities 中声明", aistudio.ErrInvalidArgument)
				} else if message.MIMEType == "" {
					message.MIMEType = "audio/pcm"
				}
				if err == nil {
					err = session.SendMedia(ctx, message.MIMEType, message.Data)
				}
			case "image":
				if !inputModalities["image"] {
					err = fmt.Errorf("%w: image 未在 setup input_modalities 中声明", aistudio.ErrInvalidArgument)
				} else if message.MIMEType == "" {
					message.MIMEType = "image/jpeg"
				}
				if err == nil {
					err = session.SendMedia(ctx, message.MIMEType, message.Data)
				}
			case "media_end":
				if !inputModalities["audio"] && !inputModalities["image"] {
					err = fmt.Errorf("%w: media_end 未在 setup input_modalities 中声明", aistudio.ErrInvalidArgument)
				} else {
					err = session.SendMediaEnd(ctx)
				}
			case "tool_response":
				if !toolsEnabled {
					err = fmt.Errorf("%w: tool_response 的 setup 未声明 tools", aistudio.ErrInvalidArgument)
				} else {
					err = session.SendToolResponses(ctx, message.ToolResponses)
				}
			case "close":
				err = session.Close()
				cancel()
				return
			default:
				err = fmt.Errorf("%w: unsupported bidi message type %q", aistudio.ErrInvalidArgument, message.Type)
			}
			if err != nil {
				select {
				case errors <- err:
				case <-ctx.Done():
				}
				return
			}
		}
	}
}

func (s *server) writeBidiEvents(
	ctx context.Context,
	connection *websocket.Conn,
	r *http.Request,
	session *aistudio.BidiSession,
	startedAt time.Time,
	mediaScoped bool,
	clientErrors <-chan error,
	done chan<- struct{},
) {
	defer close(done)
	defer connection.Close()
	if err := writeBidiJSON(connection, bidiServerMessage{Type: "session_opened", Model: session.Model()}); err != nil {
		return
	}
	first := true
	for {
		select {
		case err := <-clientErrors:
			SetAccessLogError(r.Context(), err)
			_ = writeBidiJSON(connection, bidiErrorMessage(err))
			return
		case event, ok := <-session.Events():
			if !ok {
				return
			}
			if first {
				SetAccessLogFirstEvent(r.Context(), time.Since(startedAt))
				first = false
			}
			message := mapBidiServerEvent(event)
			if event.Err != nil {
				SetAccessLogError(r.Context(), event.Err)
				message = bidiErrorMessage(event.Err)
				message.Raw = event.Raw
			}
			if err := writeBidiJSON(connection, message); err != nil {
				return
			}
			if event.Kind == aistudio.BidiEventError || event.Kind == aistudio.BidiEventClosed {
				return
			}
		case <-ctx.Done():
			return
		}
	}
}

func bidiErrorMessage(err error) bidiServerMessage {
	message := bidiServerMessage{Type: "error"}
	if err != nil {
		message.Error = err.Error()
	}
	if errors.Is(err, aistudio.ErrInvalidArgument) {
		message.Code = "invalid_request"
	}
	return message
}

func writeBidiJSON(connection *websocket.Conn, value any) error {
	if err := connection.SetWriteDeadline(time.Now().Add(bidiWriteTimeout)); err != nil {
		return err
	}
	return connection.WriteJSON(value)
}

func waitBidiRoutine(done <-chan struct{}) {
	timer := time.NewTimer(bidiCloseTimeout)
	defer timer.Stop()
	select {
	case <-done:
	case <-timer.C:
	}
}

func mapBidiServerEvent(event aistudio.BidiEvent) bidiServerMessage {
	message := bidiServerMessage{
		Type: string(event.Kind), Text: event.Text, Transcription: event.Transcription,
		ToolCall: event.ToolCall, SessionToken: event.SessionToken, Resumable: event.Resumable,
		ToolCallIDs: event.ToolCallIDs,
		Raw:         event.Raw,
	}
	if event.Media != nil {
		message.MIMEType = event.Media.MIME
		message.Data = event.Media.Data
	}
	if event.Err != nil {
		message.Error = event.Err.Error()
	}
	return message
}
