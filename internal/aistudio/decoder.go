package aistudio

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"slices"
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
	// Usage 是拦截帧中上游计入的输入用量
	Usage *Usage
	Raw   json.RawMessage
}

// InputBlocked 表示上游按输入本身（安全设置、屏蔽词、禁止内容）拦截请求，同一输入重发仍被拦截
func (e *PromptFeedbackError) InputBlocked() bool {
	switch e.Reason {
	case "safety", "blocklist", "prohibited_content":
		return true
	}
	return false
}

// ContentPolicyError 表示上游以内容策略类原因结束生成
type ContentPolicyError struct {
	Reason  string
	Message string
	// Usage 是下游还没有收到事件就结束时上游给出的用量
	Usage *Usage
}

// AbnormalFinishError 表示上游以格式错误的函数调用、意外的工具调用或格式错误的响应结束且这次生成没有任何输出
type AbnormalFinishError struct {
	Reason  string
	Message string
	// Usage 是上游最后给出的用量
	Usage *Usage
}

// Error 返回上游异常结束生成的原因
func (e *AbnormalFinishError) Error() string {
	if e.Message != "" {
		return fmt.Sprintf("AI Studio 异常结束生成且没有输出: %s: %s", strings.ToUpper(e.Reason), e.Message)
	}
	return fmt.Sprintf("AI Studio 异常结束生成且没有输出: %s", strings.ToUpper(e.Reason))
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
		if r.source.Buffered() == 0 {
			if _, err := r.source.Peek(1); err != nil {
				if written > 0 {
					return written, nil
				}
				return 0, err
			}
		}
		chunk, _ := r.source.Peek(min(r.source.Buffered(), len(destination)-written))
		count, hole := r.scan(chunk)
		written += copy(destination[written:], chunk[:count])
		if hole {
			r.pending = append(append(r.hole[:0], "null"...), chunk[count])
			count++
		}
		_, _ = r.source.Discard(count)
	}
	return written, nil
}

// scan 按字节更新扫描状态，返回第一个前面是数组空位的字节的位置；没有空位时返回 len(chunk) 与 false；字符串内容按 bytes.IndexByte 跳到下一个引号或反斜杠
func (r *sparseJSONReader) scan(chunk []byte) (int, bool) {
	quote, slash := -1, -1
	next := func(cached *int, value byte, from int) int {
		if *cached < from {
			*cached = len(chunk)
			if index := bytes.IndexByte(chunk[from:], value); index >= 0 {
				*cached = from + index
			}
		}
		return *cached
	}
	for position := 0; position < len(chunk); position++ {
		if r.inString {
			if r.escaped {
				r.escaped = false
				continue
			}
			position = min(next(&quote, '"', position), next(&slash, '\\', position))
			if position == len(chunk) {
				break
			}
			if chunk[position] == '\\' {
				r.escaped = true
			} else {
				r.inString = false
			}
			continue
		}
		if r.emptySlot(chunk[position]) {
			return position, true
		}
	}
	return len(chunk), false
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

// decodeGenerateItems 依次把 GenerateContent 流 field 1 中的帧交给 consume：补齐空位后单遍切出各值并逐个校验，切分或校验不接受时从流开头按标准解码重放
func decodeGenerateItems(source io.Reader, consume func(json.RawMessage) error) error {
	reader := &streamReadRecorder{source: source}
	scanner := &frameScanner{source: newSparseJSONReader(reader)}
	frames, err := scanner.decode(consume)
	if err != errFrameReplay {
		return err
	}
	if scanner.err != nil {
		reader.source, reader.err = failedReader{scanner.err}, nil
	}
	replay := &replayReader{data: slices.Concat(append(scanner.done, scanner.filled)...), ends: scanner.ends}
	return decodeGenerateItemsStandard(io.MultiReader(replay, scanner.source), reader, frames, consume)
}

// errFrameReplay 表示单遍切分不接受当前输入，需要按标准解码重放
var errFrameReplay = errors.New("按标准解码重放")

// failedReader 每次读取都返回 err，重放读到原流结束或出错的位置时再次给出同一结果
type failedReader struct{ err error }

func (reader failedReader) Read([]byte) (int, error) {
	return 0, reader.err
}

// replayReader 按单遍切分时每次读取的边界重放已读到的字节，标准解码读到的各块与直接读原流时相同
type replayReader struct {
	data []byte
	ends []int
	pos  int
}

func (reader *replayReader) Read(buffer []byte) (int, error) {
	if len(reader.ends) == 0 {
		return 0, io.EOF
	}
	count := copy(buffer, reader.data[reader.pos:reader.ends[0]])
	reader.pos += count
	if reader.pos == reader.ends[0] {
		reader.ends = reader.ends[1:]
	}
	return count, nil
}

// frameScanner 从补齐空位后的 GenerateContent 流中切出根数组内的各值：filled 从 mark 起是尚未切完的字节，之前读到的字节依次存入 done，ends 保存每次读取结束时的流内位置，供重放使用
type frameScanner struct {
	source io.Reader
	done   [][]byte
	filled []byte
	ends   []int
	read   int
	mark   int
	pos    int
	err    error
}

// fill 再读一块数据，返回尚未切完的字节在 filled 中前移的距离；流已结束或读取出错时返回 false
func (s *frameScanner) fill() (int, bool) {
	shift := 0
	for s.err == nil {
		if cap(s.filled)-len(s.filled) < 16<<10 {
			shift += s.mark
			kept := s.filled[s.mark:]
			if s.mark > 0 {
				s.done = append(s.done, s.filled[:s.mark:s.mark])
			}
			s.filled = append(make([]byte, 0, max(64<<10, 2*len(kept))), kept...)
			s.read, s.pos, s.mark = s.read+s.mark, s.pos-s.mark, 0
		}
		count, err := s.source.Read(s.filled[len(s.filled):cap(s.filled)])
		s.filled = s.filled[:len(s.filled)+count]
		s.err = err
		if count > 0 {
			s.ends = append(s.ends, s.read+len(s.filled))
			return shift, true
		}
	}
	return shift, false
}

// peek 跳过空白返回下一个字节，流已结束或读取出错时返回 false
func (s *frameScanner) peek() (byte, bool) {
	for {
		for ; s.pos < len(s.filled); s.pos++ {
			if char := s.filled[s.pos]; char != ' ' && char != '\t' && char != '\n' && char != '\r' {
				return char, true
			}
		}
		s.mark = s.pos
		if _, ok := s.fill(); !ok {
			return 0, false
		}
	}
}

// value 返回从当前位置开始的一个值的字节并前移到其后，字符串内容按 stringEnd 跳过；值为空或不完整时返回 false
func (s *frameScanner) value() ([]byte, bool) {
	s.mark = s.pos
	depth, position := 0, s.pos
	for {
		for position < len(s.filled) {
			switch s.filled[position] {
			case '"':
				from := position + 1
				end, closed := stringEnd(s.filled, from)
				for !closed {
					from = len(s.filled)
					for from > position+1 && s.filled[from-1] == '\\' {
						from--
					}
					shift, ok := s.fill()
					if !ok {
						return nil, false
					}
					position, from = position-shift, from-shift
					end, closed = stringEnd(s.filled, from)
				}
				position = end + 1
				if depth == 0 {
					return s.cut(position), true
				}
			case '[', '{':
				depth++
				position++
			case ']', '}':
				if depth == 0 {
					return s.cut(position), position > s.mark
				}
				depth--
				position++
				if depth == 0 {
					return s.cut(position), true
				}
			case ',', ' ', '\t', '\n', '\r':
				if depth == 0 {
					return s.cut(position), position > s.mark
				}
				position++
			default:
				position++
			}
		}
		shift, ok := s.fill()
		if !ok {
			return nil, false
		}
		position -= shift
	}
}

// cut 返回从 mark 到 end 的值并前移到 end
func (s *frameScanner) cut(end int) []byte {
	s.pos = end
	return s.filled[s.mark:end:end]
}

// decode 依次交出 field 1 的各帧，核对 field 2 的流尾状态与根数组结束，返回已交出的帧数；切分或校验不接受时返回 errFrameReplay
func (s *frameScanner) decode(consume func(json.RawMessage) error) (int, error) {
	frames := 0
	for range 2 {
		if char, ok := s.peek(); !ok || char != '[' {
			return frames, errFrameReplay
		}
		s.pos++
	}
	if char, ok := s.peek(); !ok {
		return frames, errFrameReplay
	} else if char == ']' {
		s.pos++
	} else {
		for {
			raw, ok := s.value()
			if !ok || !json.Valid(raw) {
				return frames, errFrameReplay
			}
			if err := consume(raw); err != nil {
				return frames, err
			}
			frames++
			char, ok := s.peek()
			if !ok || char != ',' && char != ']' {
				return frames, errFrameReplay
			}
			s.pos++
			if char == ']' {
				break
			}
		}
	}
	for field := 1; ; field++ {
		char, ok := s.peek()
		if !ok || char != ',' && char != ']' {
			return frames, errFrameReplay
		}
		s.pos++
		if char == ']' {
			break
		}
		if _, ok := s.peek(); !ok {
			return frames, errFrameReplay
		}
		raw, ok := s.value()
		if !ok || !json.Valid(raw) {
			return frames, errFrameReplay
		}
		if field == 1 {
			if err := trailerError("GenerateContent", raw); err != nil {
				return frames, err
			}
		}
	}
	if _, ok := s.peek(); ok || s.err != io.EOF {
		return frames, errFrameReplay
	}
	return frames, nil
}

// decodeGenerateItemsStandard 以标准 JSON 解码逐个读取补齐空位后的 GenerateContent 流，前 skip 帧已经交出，不再交给 consume
func decodeGenerateItemsStandard(source io.Reader, reader *streamReadRecorder, skip int, consume func(json.RawMessage) error) error {
	decoder := json.NewDecoder(source)
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
		if index >= skip {
			if err := consume(raw); err != nil {
				return err
			}
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
	if err := json.Unmarshal(raw, &values); err != nil {
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

// splitArray 返回与 rawArray 相同的元素与错误，raw 须为合法 JSON：按顶层逗号切分数组，元素与 raw 共用字节且容量截止到元素末尾
func splitArray(raw json.RawMessage, path string, evidence json.RawMessage) ([]json.RawMessage, error) {
	trimmed := bytes.TrimSpace(raw)
	if bytes.Equal(trimmed, []byte("null")) {
		return nil, nil
	}
	if len(trimmed) == 0 || trimmed[0] != '[' {
		return nil, &ProtocolEvidenceError{Path: path, Detail: "期望数组", Raw: cloneEvidence(raw, evidence)}
	}
	values := []json.RawMessage{}
	depth, start := 0, 1
	for position := 1; position < len(trimmed); position++ {
		switch trimmed[position] {
		case '"':
			closing, ok := stringEnd(trimmed, position+1)
			if !ok {
				return nil, &ProtocolEvidenceError{Path: path, Detail: "期望数组", Raw: cloneEvidence(raw, evidence)}
			}
			position = closing
		case '[', '{':
			depth++
		case ']', '}':
			if depth > 0 {
				depth--
				continue
			}
			if element := bytes.TrimSpace(trimmed[start:position]); len(element) > 0 {
				values = append(values, element[:len(element):len(element)])
			}
			return values, nil
		case ',':
			if depth == 0 {
				element := bytes.TrimSpace(trimmed[start:position])
				values = append(values, element[:len(element):len(element)])
				start = position + 1
			}
		}
	}
	return nil, &ProtocolEvidenceError{Path: path, Detail: "期望数组", Raw: cloneEvidence(raw, evidence)}
}

// canonicalInt64 解析不带符号以外修饰、没有前导零、不超过 18 位的 JSON 整数字面量，其余写法返回 false
func canonicalInt64(raw []byte) (int64, bool) {
	digits := raw
	if len(digits) > 0 && digits[0] == '-' {
		digits = digits[1:]
	}
	if len(digits) == 0 || len(digits) > 18 || len(digits) > 1 && digits[0] == '0' {
		return 0, false
	}
	value := int64(0)
	for _, digit := range digits {
		if digit < '0' || digit > '9' {
			return 0, false
		}
		value = value*10 + int64(digit-'0')
	}
	if len(digits) < len(raw) {
		value = -value
	}
	return value, true
}

func rawInt64(raw json.RawMessage, path string, evidence json.RawMessage) (int64, error) {
	if value, ok := canonicalInt64(bytes.TrimSpace(raw)); ok {
		return value, nil
	}
	var number json.Number
	if err := json.Unmarshal(raw, &number); err != nil {
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
	if err := json.Unmarshal(raw, &number); err != nil {
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
