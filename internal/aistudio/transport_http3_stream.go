package aistudio

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"
	"sync/atomic"

	fhttp "github.com/Mag1cFall/fhttp"
	quic "github.com/Mag1cFall/uquic"
	"github.com/Mag1cFall/uquic/quicvarint"
)

// h3RequestFields 按 Firefox 的 HTTP/3 请求头生成 QPACK 字段：伪头 method、scheme、authority、path，其余头按 Firefox 顺序并去掉逐跳头，altUsed 时写入 Alt-Used
func h3RequestFields(request *fhttp.Request, altUsed bool) []qpackField {
	authority := request.Host
	if authority == "" {
		authority = request.URL.Host
	}
	fields := []qpackField{
		{":method", request.Method},
		{":scheme", request.URL.Scheme},
		{":authority", authority},
		{":path", request.URL.RequestURI()},
	}
	header := request.Header.Clone()
	delete(header, fhttp.HeaderOrderKey)
	if altUsed && header.Get("Sec-Fetch-Dest") != "image" {
		header.Set("Alt-Used", request.URL.Hostname())
	}
	if length := request.ContentLength; length > 0 || (length == 0 && (request.Method == "POST" || request.Method == "PUT" || request.Method == "PATCH")) {
		header.Set("Content-Length", strconv.FormatInt(length, 10))
	}
	for _, name := range orderedHeaderNames(header, request.URL.Host) {
		if _, excluded := h3ExcludedRequestHeaders[name]; excluded {
			continue
		}
		values := header.Values(name)
		if len(values) == 0 {
			continue
		}
		separator := ", "
		if name == "cookie" {
			separator = "; "
		}
		fields = append(fields, qpackField{name, strings.Join(values, separator)})
	}
	return fields
}

// roundTrip 在本连接上发送请求并返回响应头已到达的响应，opened 表示请求流已经打开；0-RTT 连接上只有安全方法不等握手完成，0-RTT 被拒绝的请求在握手完成后重发一次
func (conn *http3Conn) roundTrip(request *fhttp.Request, fields []qpackField) (*fhttp.Response, bool, error) {
	if !firefoxSafeMethods[request.Method] {
		if err := conn.awaitReady(request.Context()); err != nil {
			return nil, false, err
		}
	}
	response, opened, err := conn.exchange(request, fields)
	if errors.Is(err, quic.Err0RTTRejected) {
		if err := conn.awaitReady(request.Context()); err != nil {
			return nil, false, err
		}
		return conn.exchange(request, fields)
	}
	return response, opened, err
}

// exchange 打开请求流写出请求并读取响应头，0-RTT 被拒绝的请求流随 0-RTT 数据一起作废，不再重置
func (conn *http3Conn) exchange(request *fhttp.Request, fields []qpackField) (*fhttp.Response, bool, error) {
	ctx := request.Context()
	stream, err := conn.quic.OpenStreamSync(ctx)
	if err != nil {
		return nil, false, err
	}
	id := int64(stream.StreamID())
	conn.mu.Lock()
	block := conn.encoder.encode(fields, id)
	err = conn.flushEncoderLocked()
	conn.mu.Unlock()
	if err == nil {
		_, err = stream.Write(appendH3Frame(nil, h3FrameHeaders, block))
	}
	if err != nil {
		if !errors.Is(err, quic.Err0RTTRejected) {
			stream.CancelWrite(h3RequestCancelled)
			stream.CancelRead(h3RequestCancelled)
		}
		return nil, true, err
	}
	conn.decoderMu.Lock()
	body := &http3Body{conn: conn, decoder: conn.decoder, stream: stream, reader: bufio.NewReader(stream)}
	conn.decoderMu.Unlock()
	body.stopWatch = context.AfterFunc(ctx, body.abort)
	if request.Body != nil && request.Body != fhttp.NoBody {
		go body.writeRequest(request.Body)
	} else {
		_ = stream.Close()
	}
	response, err := body.readResponse(ctx, request)
	if err != nil {
		body.stopWatch()
		if !errors.Is(err, quic.Err0RTTRejected) {
			body.abort()
		}
		if ctx.Err() != nil {
			err = ctx.Err()
		}
		return nil, true, err
	}
	return response, true, nil
}

// http3Body 是请求流上的响应体，按帧读取 DATA，结尾的 HEADERS 写入 trailer
type http3Body struct {
	conn      *http3Conn
	decoder   *qpackDecoder
	stream    *quic.Stream
	reader    *bufio.Reader
	response  *fhttp.Response
	remaining uint64
	done      bool
	aborted   atomic.Bool
	stopWatch func() bool
}

func (body *http3Body) writeRequest(source io.ReadCloser) {
	defer source.Close()
	buffer := make([]byte, h3RequestBodyChunk)
	for {
		read, err := source.Read(buffer)
		if read > 0 {
			if _, werr := body.stream.Write(appendH3Frame(nil, h3FrameData, buffer[:read])); werr != nil {
				return
			}
		}
		if err == io.EOF {
			_ = body.stream.Close()
			return
		}
		if err != nil {
			body.stream.CancelWrite(h3RequestCancelled)
			return
		}
	}
}

func (body *http3Body) readResponse(ctx context.Context, request *fhttp.Request) (*fhttp.Response, error) {
	for {
		kind, payload, err := readH3Frame(body.reader)
		if err != nil {
			return nil, err
		}
		if kind != h3FrameHeaders {
			if kind == h3FrameData {
				return nil, fmt.Errorf("HTTP/3 响应在 HEADERS 之前收到 DATA")
			}
			continue
		}
		fields, err := body.decoder.decode(ctx, payload, int64(body.stream.StreamID()))
		if err != nil {
			return nil, err
		}
		_ = body.conn.flushDecoder()
		header := make(fhttp.Header, len(fields))
		status := 0
		for _, field := range fields {
			if field.name == ":status" {
				status, _ = strconv.Atoi(field.value)
				continue
			}
			header.Add(field.name, field.value)
		}
		if status < 100 || status > 599 {
			return nil, fmt.Errorf("HTTP/3 响应状态 %d 无效", status)
		}
		if status < 200 {
			continue
		}
		response := &fhttp.Response{
			Status:        strconv.Itoa(status) + " " + fhttp.StatusText(status),
			StatusCode:    status,
			Proto:         "HTTP/3.0",
			ProtoMajor:    3,
			Header:        header,
			ContentLength: -1,
			Trailer:       fhttp.Header{},
			Request:       request,
			Body:          body,
		}
		if length, err := strconv.ParseInt(header.Get("Content-Length"), 10, 64); err == nil {
			response.ContentLength = length
		}
		body.response = response
		if request.Method != "HEAD" {
			response.Body = fhttp.DecompressBody(response)
		}
		return response, nil
	}
}

func (body *http3Body) Read(buffer []byte) (int, error) {
	for {
		if body.done {
			return 0, io.EOF
		}
		if body.remaining > 0 {
			read, err := body.reader.Read(buffer[:min(uint64(len(buffer)), body.remaining)])
			body.remaining -= uint64(read)
			if err == io.EOF {
				err = nil
				if body.remaining > 0 {
					err = io.ErrUnexpectedEOF
				}
			}
			return read, body.readError(err)
		}
		kind, err := quicvarint.Read(body.reader)
		if err == io.EOF {
			body.done = true
			body.stopWatch()
			return 0, io.EOF
		}
		if err != nil {
			return 0, body.readError(err)
		}
		length, err := quicvarint.Read(body.reader)
		if err != nil {
			return 0, body.readError(err)
		}
		switch kind {
		case h3FrameData:
			body.remaining = length
		case h3FrameHeaders:
			payload := make([]byte, length)
			if _, err := io.ReadFull(body.reader, payload); err != nil {
				return 0, body.readError(err)
			}
			fields, err := body.decoder.decode(context.Background(), payload, int64(body.stream.StreamID()))
			if err != nil {
				return 0, err
			}
			_ = body.conn.flushDecoder()
			for _, field := range fields {
				body.response.Trailer.Add(field.name, field.value)
			}
		default:
			if _, err := body.reader.Discard(int(length)); err != nil {
				return 0, body.readError(err)
			}
		}
	}
}

func (body *http3Body) readError(err error) error {
	if err != nil && body.aborted.Load() {
		if ctxErr := body.response.Request.Context().Err(); ctxErr != nil {
			return ctxErr
		}
		return errors.New("HTTP/3 响应体已关闭")
	}
	return err
}

func (body *http3Body) Close() error {
	body.stopWatch()
	if !body.done {
		body.abort()
	}
	return nil
}

// abort 按 Firefox 取消请求：以 H3_REQUEST_CANCELLED 重置请求流并在解码器流写出 Stream Cancellation
func (body *http3Body) abort() {
	if !body.aborted.CompareAndSwap(false, true) {
		return
	}
	body.stream.CancelWrite(h3RequestCancelled)
	body.stream.CancelRead(h3RequestCancelled)
	body.decoder.cancel(int64(body.stream.StreamID()))
	_ = body.conn.flushDecoder()
}
