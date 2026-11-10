package aistudio

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"unicode"
)

// ProtocolEvidenceError 保存无法解释的协议位置和原始值
type ProtocolEvidenceError struct {
	Method string
	Path   string
	Detail string
	Raw    json.RawMessage
}

// PromptFeedbackError 表示上游拒绝当前输入且没有返回候选
type PromptFeedbackError struct {
	Reason        string
	Message       string
	SafetyRatings []SafetyRating
	Raw           json.RawMessage
}

// ContentPolicyError 表示上游以内容策略类原因结束生成
type ContentPolicyError struct {
	Reason  string
	Message string
}

// Error 返回上游结束生成的内容策略原因
func (e *ContentPolicyError) Error() string {
	if e.Message != "" {
		return fmt.Sprintf("AI Studio 因内容策略结束生成: %s: %s", strings.ToUpper(e.Reason), e.Message)
	}
	return fmt.Sprintf("AI Studio 因内容策略结束生成: %s", strings.ToUpper(e.Reason))
}

// Error 返回协议证据错误
func (e *ProtocolEvidenceError) Error() string {
	if e.Method == "" {
		return fmt.Sprintf("协议位置 %s: %s", e.Path, e.Detail)
	}
	return fmt.Sprintf("AI Studio %s 协议位置 %s: %s", e.Method, e.Path, e.Detail)
}

// Error 返回上游输入拒绝原因
func (e *PromptFeedbackError) Error() string {
	if e.Message != "" {
		return fmt.Sprintf("AI Studio 拒绝当前输入: %s: %s", e.Reason, e.Message)
	}
	return fmt.Sprintf("AI Studio 拒绝当前输入: %s", e.Reason)
}

// Unwrap 将输入拒绝映射为无效请求
func (e *PromptFeedbackError) Unwrap() error {
	return ErrInvalidArgument
}

// errMissingFinishFrame 表示上游生成流在完成帧之前正常结束
var errMissingFinishFrame = errors.New("流结束前没有完成帧")

// StreamInterruptedError 表示上游生成流在完成帧之前中断或结束
type StreamInterruptedError struct {
	Method string
	Frames int
	Err    error
}

// Error 返回中断前已接收的帧数与读取错误
func (e *StreamInterruptedError) Error() string {
	return fmt.Sprintf("AI Studio %s 上游流中断（已接收 %d 帧）: %v", e.Method, e.Frames, e.Err)
}

// Unwrap 返回读取错误
func (e *StreamInterruptedError) Unwrap() error {
	return e.Err
}

// streamReadRecorder 记录生成流读取时的非 EOF 错误
type streamReadRecorder struct {
	source io.Reader
	err    error
}

func (reader *streamReadRecorder) Read(buffer []byte) (int, error) {
	count, err := reader.source.Read(buffer)
	if err != nil && err != io.EOF {
		reader.err = err
	}
	return count, err
}

// failure 在读取出错时返回流中断错误，否则返回解码错误
func (reader *streamReadRecorder) failure(method string, frames int, decodeErr error) error {
	if reader.err == nil {
		return decodeErr
	}
	return &StreamInterruptedError{Method: method, Frames: frames, Err: reader.err}
}

type sparseJSONReader struct {
	source        *bufio.Reader
	pending       []byte
	hole          [5]byte
	inString      bool
	escaped       bool
	previousToken byte
}

func newSparseJSONReader(source io.Reader) io.Reader {
	return &sparseJSONReader{source: bufio.NewReader(source)}
}

func (r *sparseJSONReader) Read(destination []byte) (int, error) {
	written := 0
	for written < len(destination) {
		if len(r.pending) > 0 {
			count := copy(destination[written:], r.pending)
			r.pending = r.pending[count:]
			written += count
			continue
		}
		if written > 0 && r.source.Buffered() == 0 {
			return written, nil
		}
		value, err := r.source.ReadByte()
		if err != nil {
			if written > 0 {
				return written, nil
			}
			return 0, err
		}
		if r.emptySlot(value) {
			r.pending = append(append(r.hole[:0], "null"...), value)
			continue
		}
		destination[written] = value
		written++
	}
	return written, nil
}

// emptySlot 按 value 更新扫描状态，返回 value 之前是否为数组空位
func (r *sparseJSONReader) emptySlot(value byte) bool {
	if r.inString {
		if r.escaped {
			r.escaped = false
		} else if value == '\\' {
			r.escaped = true
		} else if value == '"' {
			r.inString = false
		}
		return false
	}
	if value == '"' {
		r.inString = true
	}
	empty := value == ',' && (r.previousToken == '[' || r.previousToken == ',') || value == ']' && r.previousToken == ','
	if !unicode.IsSpace(rune(value)) {
		r.previousToken = value
	}
	return empty
}

func decodeJSONValue(raw []byte) (json.RawMessage, error) {
	decoder := json.NewDecoder(newSparseJSONReader(bytes.NewReader(raw)))
	decoder.UseNumber()
	var value json.RawMessage
	if err := decoder.Decode(&value); err != nil {
		return nil, err
	}
	var extra json.RawMessage
	if err := decoder.Decode(&extra); err != io.EOF {
		if err == nil {
			return nil, fmt.Errorf("JSON+protobuf 包含多个根值")
		}
		return nil, err
	}
	return value, nil
}

func decodeGenerateItems(source io.Reader, consume func(json.RawMessage) error) error {
	reader := &streamReadRecorder{source: source}
	decoder := json.NewDecoder(newSparseJSONReader(reader))
	decoder.UseNumber()
	index := 0
	fail := func(path string, err error) error {
		return reader.failure("GenerateContent", index, &ProtocolEvidenceError{Method: "GenerateContent", Path: path, Detail: err.Error()})
	}
	rootStart, err := decoder.Token()
	if err != nil {
		return fail("$", err)
	}
	if delimiter, ok := rootStart.(json.Delim); !ok || delimiter != '[' {
		return &ProtocolEvidenceError{Method: "GenerateContent", Path: "$", Detail: "根值不是数组"}
	}
	if !decoder.More() {
		return reader.failure("GenerateContent", index, &ProtocolEvidenceError{Method: "GenerateContent", Path: "$", Detail: "根数组缺少 field 1"})
	}
	fieldStart, err := decoder.Token()
	if err != nil {
		return fail("$[0]", err)
	}
	if delimiter, ok := fieldStart.(json.Delim); !ok || delimiter != '[' {
		return &ProtocolEvidenceError{Method: "GenerateContent", Path: "$[0]", Detail: "field 1 不是 repeated 数组"}
	}
	for decoder.More() {
		var raw json.RawMessage
		if err := decoder.Decode(&raw); err != nil {
			return fail(fmt.Sprintf("$[0][%d]", index), err)
		}
		if err := consume(raw); err != nil {
			return err
		}
		index++
	}
	if token, err := decoder.Token(); err != nil || token != json.Delim(']') {
		if err != nil {
			return fail("$[0]", err)
		}
		return &ProtocolEvidenceError{Method: "GenerateContent", Path: "$[0]", Detail: "field 1 没有正常结束"}
	}
	fieldIndex := 1
	for decoder.More() {
		var raw json.RawMessage
		if err := decoder.Decode(&raw); err != nil {
			return fail(fmt.Sprintf("$[%d]", fieldIndex), err)
		}
		if fieldIndex == 1 {
			if err := trailerError("GenerateContent", raw); err != nil {
				return err
			}
		}
		fieldIndex++
	}
	if token, err := decoder.Token(); err != nil || token != json.Delim(']') {
		if err != nil {
			return fail("$", err)
		}
		return &ProtocolEvidenceError{Method: "GenerateContent", Path: "$", Detail: "根数组没有正常结束"}
	}
	var extra json.RawMessage
	if err := decoder.Decode(&extra); err != io.EOF {
		if err == nil {
			return &ProtocolEvidenceError{Method: "GenerateContent", Path: "$", Detail: "响应后存在第二个根值", Raw: extra}
		}
		return fail("$", err)
	}
	return nil
}

// trailerError 解码流式响应列表之后的 google.rpc 状态，code 不为 0 时返回上游错误
func trailerError(method string, raw json.RawMessage) error {
	status, err := rawArray(raw, "$[1]", raw)
	if err != nil || len(status) == 0 {
		return &ProtocolEvidenceError{Method: method, Path: "$[1]", Detail: "流尾状态无效", Raw: cloneRaw(raw)}
	}
	code, err := rawInt64(status[0], "$[1][0]", raw)
	if err != nil {
		return withMethod(err, method)
	}
	if code == 0 {
		return nil
	}
	statusCode := http.StatusBadGateway
	if mapped, ok := interactionStatusHTTP[code]; ok {
		statusCode = mapped
	}
	return DecodeRPCError(method, statusCode, raw)
}

func rawArray(raw json.RawMessage, path string, evidence json.RawMessage) ([]json.RawMessage, error) {
	var values []json.RawMessage
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	if err := decoder.Decode(&values); err != nil {
		return nil, &ProtocolEvidenceError{Path: path, Detail: "期望数组", Raw: cloneEvidence(raw, evidence)}
	}
	return values, nil
}

func rawString(raw json.RawMessage, path string, evidence json.RawMessage) (string, error) {
	var value string
	if err := json.Unmarshal(raw, &value); err != nil {
		return "", &ProtocolEvidenceError{Path: path, Detail: "期望字符串", Raw: cloneEvidence(raw, evidence)}
	}
	return value, nil
}

func rawInt64(raw json.RawMessage, path string, evidence json.RawMessage) (int64, error) {
	var number json.Number
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	if err := decoder.Decode(&number); err != nil {
		return 0, &ProtocolEvidenceError{Path: path, Detail: "期望整数", Raw: cloneEvidence(raw, evidence)}
	}
	value, err := strconv.ParseInt(number.String(), 10, 64)
	if err != nil {
		return 0, &ProtocolEvidenceError{Path: path, Detail: "期望整数", Raw: cloneEvidence(raw, evidence)}
	}
	return value, nil
}

func rawFloat64(raw json.RawMessage, path string, evidence json.RawMessage) (float64, error) {
	var number json.Number
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	if err := decoder.Decode(&number); err != nil {
		return 0, &ProtocolEvidenceError{Path: path, Detail: "期望数字", Raw: cloneEvidence(raw, evidence)}
	}
	value, err := strconv.ParseFloat(number.String(), 64)
	if err != nil {
		return 0, &ProtocolEvidenceError{Path: path, Detail: "期望数字", Raw: cloneEvidence(raw, evidence)}
	}
	return value, nil
}

func rawBool(raw json.RawMessage, path string, evidence json.RawMessage) (bool, error) {
	if bytes.Equal(bytes.TrimSpace(raw), []byte("1")) {
		return true, nil
	}
	if bytes.Equal(bytes.TrimSpace(raw), []byte("0")) {
		return false, nil
	}
	var value bool
	if err := json.Unmarshal(raw, &value); err != nil {
		return false, &ProtocolEvidenceError{Path: path, Detail: "期望布尔值", Raw: cloneEvidence(raw, evidence)}
	}
	return value, nil
}

func rawAt(values []json.RawMessage, index int) json.RawMessage {
	if index >= len(values) {
		return nil
	}
	return values[index]
}

func isJSONNull(raw json.RawMessage) bool {
	trimmed := bytes.TrimSpace(raw)
	return len(trimmed) == 0 || bytes.Equal(trimmed, []byte("null"))
}

func cloneEvidence(primary json.RawMessage, fallback json.RawMessage) json.RawMessage {
	if len(primary) > 0 {
		return append(json.RawMessage(nil), primary...)
	}
	return append(json.RawMessage(nil), fallback...)
}
