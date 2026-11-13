package aistudio

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"slices"
	"strings"

	"github.com/google/uuid"
)

// AssistantStreamDecoder 把写代码助手的步骤快照解码为增量事件
type AssistantStreamDecoder struct {
	// Functions 是请求声明的函数名，不为空时正文中的调用代码块解码为函数调用
	Functions []string
	// Reopen 以同一任务重新打开任务流
	Reopen    func() (io.ReadCloser, error)
	emitted   map[int64][]string
	usage     map[int64]Usage
	searched  map[int64]bool
	grounding *GroundingMetadata
	// searches 是已完成的 search_web 的查询与摘要，供下一轮补回对话历史
	searches []string
	// held 是扣下的正文：可能的代码块围栏开头，或尚未结束的调用候选代码块
	held string
	// block 表示 held 以调用候选代码块开头，other 表示正处在其他代码块内
	block bool
	other bool
	// called 表示已输出函数调用，malformed 表示 function_calls 代码块无法解码，invalidTool 表示声明了客户端函数时模型调用了被禁止的工作区内置工具
	called      bool
	malformed   bool
	invalidTool bool
	lastStep    int64
	wroteText   bool
	finished    bool
	// answered 表示最近解码的步骤是已完成、含正文且没有工具动作的模型步骤，即本轮回复结束
	answered bool
	// stopped 表示不再读取本任务后续步骤：函数调用之后模型继续输出正文、上游后续步骤失败，或模型调用了被禁止的工作区工具、交回的提问缺少调用块
	stopped bool
	// failed 表示上游任务以失败步骤结束，不再需要取消
	failed bool
}

// DecodeAssistantStream 解码 StreamCodeAssistantOfflineGeneration 响应：field 1 为消息列表，其后可带 google.rpc 状态；本轮回复结束、函数调用之后模型继续输出正文或上游后续步骤失败时不再读取上游，消息列表在回复完成前正常结束时经 Reopen 重新打开一次任务流继续读取，仍未完成而已解出完整函数调用或写出正文时以已有内容结束本轮
func DecodeAssistantStream(source io.Reader, decoder *AssistantStreamDecoder, emit func(Event) error) error {
	err := decodeAssistantMessages(source, decoder, emit)
	if err == nil && !decoder.finished && !decoder.stopped && decoder.Reopen != nil {
		var reopened io.ReadCloser
		if reopened, err = decoder.Reopen(); err == nil {
			err = errors.Join(decodeAssistantMessages(reopened, decoder, emit), reopened.Close())
		}
	}
	if err != nil {
		return err
	}
	events, err := decoder.End()
	if err != nil {
		return err
	}
	for _, event := range events {
		if err := emit(event); err != nil {
			return err
		}
	}
	return nil
}

// decodeAssistantMessages 读取一条任务流的消息列表与流尾状态
func decodeAssistantMessages(source io.Reader, decoder *AssistantStreamDecoder, emit func(Event) error) error {
	reader := &streamReadRecorder{source: source}
	stream := json.NewDecoder(newSparseJSONReader(reader))
	stream.UseNumber()
	index := 0
	fail := func(path string, detail string) error {
		return reader.failure(assistantStreamMethod, index, &ProtocolEvidenceError{Method: assistantStreamMethod, Path: path, Detail: detail})
	}
	if token, err := stream.Token(); err != nil || token != json.Delim('[') {
		return fail("$", "根值不是数组")
	}
	if !stream.More() {
		return fail("$", "根数组缺少消息列表")
	}
	if token, err := stream.Token(); err != nil || token != json.Delim('[') {
		return fail("$[0]", "消息列表不是数组")
	}
	for ; !decoder.answered && !decoder.stopped && stream.More(); index++ {
		var raw json.RawMessage
		if err := stream.Decode(&raw); err != nil {
			return fail(fmt.Sprintf("$[0][%d]", index), err.Error())
		}
		events, err := decoder.Decode(raw, fmt.Sprintf("$[0][%d]", index))
		if err != nil {
			return err
		}
		for _, event := range events {
			if err := emit(event); err != nil {
				return err
			}
		}
	}
	if !decoder.answered && !decoder.stopped {
		if token, err := stream.Token(); err != nil || token != json.Delim(']') {
			return fail("$[0]", "消息列表没有正常结束")
		}
		for stream.More() {
			var raw json.RawMessage
			if err := stream.Decode(&raw); err != nil {
				return fail("$", err.Error())
			}
			if err := trailerError(assistantStreamMethod, raw); err != nil {
				return err
			}
		}
	}
	return reader.failure(assistantStreamMethod, index, nil)
}

// Decode 解码一条消息 [null, null, [步骤...], trajectory ID]：步骤 field 1 为对话轮、6 为错误（状态为 3 时不终止任务）、10 为工具动作、12 为序号、13 为状态（1 完成、2 进行中、4 失败）、16 为用量
func (d *AssistantStreamDecoder) Decode(raw json.RawMessage, path string) ([]Event, error) {
	if d.emitted == nil {
		d.emitted = make(map[int64][]string)
		d.usage = make(map[int64]Usage)
		d.searched = make(map[int64]bool)
	}
	message, err := rawArray(raw, path, raw)
	if err != nil {
		return nil, withMethod(err, assistantStreamMethod)
	}
	stepsRaw := rawAt(message, 2)
	if isJSONNull(stepsRaw) {
		return nil, nil
	}
	steps, err := rawArray(stepsRaw, path+"[2]", raw)
	if err != nil {
		return nil, withMethod(err, assistantStreamMethod)
	}
	var events []Event
	for stepIndex, stepRaw := range steps {
		stepPath := fmt.Sprintf("%s[2][%d]", path, stepIndex)
		stepEvents, err := d.decodeStep(stepRaw, stepPath, raw)
		if err != nil {
			return nil, err
		}
		events = append(events, stepEvents...)
		if d.stopped {
			break
		}
	}
	return events, nil
}

func (d *AssistantStreamDecoder) decodeStep(raw json.RawMessage, path string, evidence json.RawMessage) ([]Event, error) {
	d.answered = false
	step, err := rawArray(raw, path, evidence)
	if err != nil {
		return nil, withMethod(err, assistantStreamMethod)
	}
	number, err := rawInt64(rawAt(step, 11), path+"[11]", evidence)
	if err != nil {
		return nil, withMethod(err, assistantStreamMethod)
	}
	status, err := rawInt64(rawAt(step, 12), path+"[12]", evidence)
	if err != nil {
		return nil, withMethod(err, assistantStreamMethod)
	}
	if failure := rawAt(step, 5); !isJSONNull(failure) {
		failed, err := decodeAssistantFailure(failure, path+"[5]", evidence)
		if err != nil {
			return nil, err
		}
		if status == 3 {
			return nil, nil
		}
		d.failed = true
		if d.called {
			d.stopped = true
			return nil, nil
		}
		return nil, failed
	}
	if usageRaw := rawAt(step, 15); !isJSONNull(usageRaw) && status == 1 {
		usage, err := decodeAssistantUsage(usageRaw, path+"[15]", evidence)
		if err != nil {
			return nil, err
		}
		d.usage[number] = usage
	}
	var events []Event
	invalidAction := false
	missingCallQuestion := false
	if actionRaw := rawAt(step, 9); !isJSONNull(actionRaw) && status == 1 && !d.searched[number] {
		action, err := rawArray(actionRaw, path+"[9]", evidence)
		if err != nil {
			return nil, withMethod(err, assistantStreamMethod)
		}
		if err := d.decodeSearch(action, path+"[9]", evidence); err != nil {
			return nil, err
		}
		var isQuestion bool
		if events, isQuestion, err = d.decodeQuestion(action, path+"[9]", evidence); err != nil {
			return nil, err
		}
		invalidAction = !isQuestion && isJSONNull(rawAt(action, 18)) && len(events) == 0
		missingCallQuestion = isQuestion && len(events) == 0
		d.searched[number] = true
	}
	contentsRaw := rawAt(step, 0)
	if !isJSONNull(contentsRaw) {
		contents, err := rawArray(contentsRaw, path+"[0]", evidence)
		if err != nil {
			return nil, withMethod(err, assistantStreamMethod)
		}
		partOffset := 0
		for contentIndex, contentRaw := range contents {
			contentPath := fmt.Sprintf("%s[0][%d]", path, contentIndex)
			content, err := rawArray(contentRaw, contentPath, evidence)
			if err != nil {
				return nil, withMethod(err, assistantStreamMethod)
			}
			role, err := rawString(rawAt(content, 1), contentPath+"[1]", evidence)
			if err != nil {
				return nil, withMethod(err, assistantStreamMethod)
			}
			if role != "model" {
				continue
			}
			parts, err := rawArray(rawAt(content, 0), contentPath+"[0]", evidence)
			if err != nil {
				return nil, withMethod(err, assistantStreamMethod)
			}
			for partIndex, partRaw := range parts {
				partEvents, err := d.decodePart(number, partOffset+partIndex, partRaw, fmt.Sprintf("%s[0][%d]", contentPath, partIndex), evidence)
				if err != nil {
					return nil, err
				}
				events = append(events, partEvents...)
			}
			partOffset += len(parts)
			if status == 1 {
				d.finished = true
			}
		}
		d.answered = status == 1 && isJSONNull(rawAt(step, 9)) && d.wroteText && d.lastStep == number
	}
	if invalidAction && len(d.Functions) > 0 && !d.called {
		d.invalidTool = true
		d.stopped = true
	} else if missingCallQuestion && len(d.Functions) > 0 && !d.called && !d.block {
		d.malformed = true
		d.stopped = true
	}
	return events, nil
}

// decodeAssistantFailure 解码错误步骤 field 6：field 1 为错误文字，field 2 为详情（1 消息、2 调试信息、3 内部错误详情、4 通用额度、5 每用户额度）；带额度标记时为 HTTP 429 与协议错误码 8，否则为 HTTP 502
func decodeAssistantFailure(raw json.RawMessage, path string, evidence json.RawMessage) (*RPCError, error) {
	values, err := rawArray(raw, path, evidence)
	if err != nil {
		return nil, withMethod(err, assistantStreamMethod)
	}
	message, err := rawString(rawAt(values, 0), path+"[0]", evidence)
	if err != nil {
		return nil, withMethod(err, assistantStreamMethod)
	}
	failure := &RPCError{Method: assistantStreamMethod, StatusCode: http.StatusBadGateway, Message: "生成步骤失败: " + message}
	detailRaw := rawAt(values, 1)
	if isJSONNull(detailRaw) {
		return failure, nil
	}
	detail, err := rawArray(detailRaw, path+"[1]", evidence)
	if err != nil {
		return nil, withMethod(err, assistantStreamMethod)
	}
	for index := range 3 {
		if text := rawAt(detail, index); !isJSONNull(text) {
			value, err := rawString(text, fmt.Sprintf("%s[1][%d]", path, index), evidence)
			if err != nil {
				return nil, withMethod(err, assistantStreamMethod)
			}
			if value != "" && value != message {
				failure.Message += "；" + value
			}
		}
	}
	for _, index := range []int{3, 4} {
		if flag := rawAt(detail, index); !isJSONNull(flag) {
			quota, err := rawBool(flag, fmt.Sprintf("%s[1][%d]", path, index), evidence)
			if err != nil {
				return nil, withMethod(err, assistantStreamMethod)
			}
			if quota {
				failure.StatusCode, failure.Code = http.StatusTooManyRequests, 8
				if index == 4 {
					failure.PerUserQuota = true
				}
			}
		}
	}
	return failure, nil
}

// decodePart 返回模型 part 文本相对上一次快照新增的部分
func (d *AssistantStreamDecoder) decodePart(step int64, index int, raw json.RawMessage, path string, evidence json.RawMessage) ([]Event, error) {
	part, err := rawArray(raw, path, evidence)
	if err != nil {
		return nil, withMethod(err, assistantStreamMethod)
	}
	textRaw := rawAt(part, 1)
	if isJSONNull(textRaw) {
		return nil, nil
	}
	text, err := rawString(textRaw, path+"[1]", evidence)
	if err != nil {
		return nil, withMethod(err, assistantStreamMethod)
	}
	thought := false
	if thoughtRaw := rawAt(part, 12); !isJSONNull(thoughtRaw) {
		thought, err = rawBool(thoughtRaw, path+"[12]", evidence)
		if err != nil {
			return nil, withMethod(err, assistantStreamMethod)
		}
	}
	emitted := d.emitted[step]
	for len(emitted) <= index {
		emitted = append(emitted, "")
	}
	previous := emitted[index]
	if !strings.HasPrefix(text, previous) {
		return nil, &ProtocolEvidenceError{Method: assistantStreamMethod, Path: path + "[1]", Detail: "步骤正文不是上一次快照的延续", Raw: cloneRaw(raw)}
	}
	emitted[index] = text
	d.emitted[step] = emitted
	delta := text[len(previous):]
	if delta == "" {
		return nil, nil
	}
	if thought {
		return []Event{{Kind: EventReasoning, Text: delta, Thought: true}}, nil
	}
	if d.wroteText && previous == "" && step != d.lastStep {
		delta = "\n\n" + delta
	}
	d.wroteText = true
	d.lastStep = step
	return d.text(delta), nil
}

// text 返回正文增量的事件；声明了函数时扣下调用候选代码块，代码块结束后解码，函数调用之后的正文与模型自写的 function_results 代码块不输出
func (d *AssistantStreamDecoder) text(delta string) []Event {
	if len(d.Functions) == 0 {
		return []Event{{Kind: EventText, Text: delta}}
	}
	if d.called {
		d.stopped = d.stopped || strings.TrimSpace(delta) != ""
		return nil
	}
	d.held += delta
	var events []Event
	emit := func(text string) {
		if text != "" {
			events = append(events, Event{Kind: EventText, Text: text})
		}
	}
	for !d.called {
		switch {
		case d.other:
			end := strings.Index(d.held, "\n"+assistantFence)
			if end < 0 {
				keep := min(len(d.held), len(assistantFence))
				emit(d.held[:len(d.held)-keep])
				d.held = d.held[len(d.held)-keep:]
				return events
			}
			end += 1 + len(assistantFence)
			emit(d.held[:end])
			d.held = d.held[end:]
			d.other = false
		case d.block:
			opening := strings.IndexByte(d.held, '\n')
			end := strings.Index(d.held[opening:], "\n"+assistantFence)
			if end < 0 {
				return events
			}
			end += opening + 1 + len(assistantFence)
			block := d.held[:end]
			d.held = d.held[end:]
			d.block = false
			events = append(events, d.callBlock(block)...)
		default:
			start := strings.Index(d.held, assistantFence)
			if start < 0 {
				keep := len(d.held) - len(strings.TrimRight(d.held, "`"))
				prefix := d.held[:len(d.held)-keep]
				if d.malformed && strings.TrimSpace(prefix) == "" {
					return events
				}
				emit(prefix)
				d.held = d.held[len(d.held)-keep:]
				return events
			}
			prefix := d.held[:start]
			if !d.malformed || strings.TrimSpace(prefix) != "" {
				emit(prefix)
			}
			d.held = d.held[start:]
			newline := strings.IndexByte(d.held, '\n')
			if newline < 0 {
				return events
			}
			if assistantCallFences[strings.TrimSpace(d.held[len(assistantFence):newline])] {
				d.block = true
				continue
			}
			emit(d.held[:newline+1])
			d.held = d.held[newline+1:]
			d.other = true
		}
	}
	d.stopped = strings.TrimSpace(d.held) != ""
	d.held = ""
	return events
}

// callBlock 解码一个调用候选代码块：调用解码为函数调用，function_results 代码块丢弃，无法解码的 function_calls 代码块记为格式错误的函数调用，其余代码块按正文输出
func (d *AssistantStreamDecoder) callBlock(block string) []Event {
	opening := strings.IndexByte(block, '\n')
	info := strings.TrimSpace(block[len(assistantFence):opening])
	body := block[opening+1 : len(block)-len(assistantFence)]
	if info == "function_results" {
		return nil
	}
	if calls, ok := assistantCalls(body, info == "function_calls", d.Functions); ok {
		d.called = true
		return calls
	}
	if info == "function_calls" {
		d.malformed = true
		return nil
	}
	return []Event{{Kind: EventText, Text: block}}
}

// assistantCalls 把代码块中的第一个 JSON 值解码为函数调用事件：无法解码时修正字符串中的不合法转义与原始控制字符后再解码，值为调用数组或单个调用对象，anyName 为假时调用的函数必须已声明，不是调用时返回 false
func assistantCalls(body string, anyName bool, functions []string) ([]Event, bool) {
	var value json.RawMessage
	if json.NewDecoder(strings.NewReader(body)).Decode(&value) != nil && json.NewDecoder(strings.NewReader(repairJSONStrings(body))).Decode(&value) != nil {
		return nil, false
	}
	var calls []assistantCall
	if json.Unmarshal(value, &calls) != nil {
		var call assistantCall
		if json.Unmarshal(value, &call) != nil {
			return nil, false
		}
		calls = []assistantCall{call}
	}
	if len(calls) == 0 {
		return nil, false
	}
	events := make([]Event, 0, len(calls))
	for _, call := range calls {
		if call.Name == "" || !anyName && !slices.Contains(functions, call.Name) {
			return nil, false
		}
		arguments := call.Arguments
		if isJSONNull(arguments) {
			arguments = json.RawMessage(`{}`)
		}
		events = append(events, Event{Kind: EventToolCall, FunctionCall: &FunctionCall{ID: uuid.NewString(), Name: call.Name, Arguments: arguments}})
	}
	return events, true
}

// repairJSONStrings 修正 JSON 字符串中模型写出的内容并去掉 } 或 ] 之前的尾随逗号：不合法的转义（如按 Python 写出的 \x27、Windows 路径中 \u 之后不是十六进制的部分）写为字面反斜杠，原始控制字符（如多行代码中的换行）写为转义
func repairJSONStrings(body string) string {
	var repaired strings.Builder
	quoted := false
	for index := 0; index < len(body); index++ {
		char := body[index]
		switch {
		case quoted && char == '\\' && validJSONEscape(body[index+1:]):
			repaired.WriteString(body[index : index+2])
			index++
		case quoted && char == '\\':
			repaired.WriteString(`\\`)
		case quoted && char < 0x20:
			fmt.Fprintf(&repaired, `\u%04x`, char)
		case !quoted && char == ',' && nextNonSpaceCloses(body[index+1:]):
			continue
		default:
			if char == '"' {
				quoted = !quoted
			}
			repaired.WriteByte(char)
		}
	}
	return repaired.String()
}

// nextNonSpaceCloses 表示 rest 在跳过空白后紧跟 } 或 ]
func nextNonSpaceCloses(rest string) bool {
	for index := 0; index < len(rest); index++ {
		switch rest[index] {
		case ' ', '\t', '\n', '\r':
			continue
		case '}', ']':
			return true
		default:
			return false
		}
	}
	return false
}

// validJSONEscape 表示反斜杠之后的 rest 以 JSON 合法转义开头，\u 之后须为 4 位十六进制
func validJSONEscape(rest string) bool {
	if strings.HasPrefix(rest, "u") {
		return len(rest) >= 5 && strings.Trim(rest[1:5], "0123456789abcdefABCDEF") == ""
	}
	return rest != "" && strings.IndexByte(`"\/bfnrt`, rest[0]) >= 0
}

// decodeQuestion 解码 ask_question 工具动作 field 22 [标题, null, 说明, [[参数名, 值]...]]：没有输出函数调用时把各问题与选项输出为正文，交回函数调用的提问不输出
func (d *AssistantStreamDecoder) decodeQuestion(action []json.RawMessage, path string, evidence json.RawMessage) ([]Event, bool, error) {
	toolRaw := rawAt(action, 21)
	if isJSONNull(toolRaw) || d.called {
		return nil, false, nil
	}
	tool, err := rawArray(toolRaw, path+"[21]", evidence)
	if err != nil {
		return nil, false, withMethod(err, assistantStreamMethod)
	}
	pairs, err := rawArray(rawAt(tool, 3), path+"[21][3]", evidence)
	if err != nil {
		return nil, false, withMethod(err, assistantStreamMethod)
	}
	arguments := map[string]string{}
	for index, pairRaw := range pairs {
		pairPath := fmt.Sprintf("%s[21][3][%d]", path, index)
		pair, err := rawArray(pairRaw, pairPath, evidence)
		if err != nil {
			return nil, false, withMethod(err, assistantStreamMethod)
		}
		name, err := rawString(rawAt(pair, 0), pairPath+"[0]", evidence)
		if err != nil {
			return nil, false, withMethod(err, assistantStreamMethod)
		}
		if arguments[name], err = rawString(rawAt(pair, 1), pairPath+"[1]", evidence); err != nil {
			return nil, false, withMethod(err, assistantStreamMethod)
		}
	}
	if arguments["tag"] != assistantQuestionTool {
		return nil, false, nil
	}
	var questions []struct {
		Question string   `json:"question"`
		Options  []string `json:"options"`
	}
	if err := json.Unmarshal([]byte(arguments["questions"]), &questions); err != nil {
		return nil, true, &ProtocolEvidenceError{Method: assistantStreamMethod, Path: path + "[21][3]", Detail: "ask_question 的 questions 不是问题列表: " + err.Error(), Raw: cloneRaw(evidence)}
	}
	var text strings.Builder
	for _, question := range questions {
		if question.Question == assistantCallQuestion {
			continue
		}
		if d.wroteText || text.Len() > 0 {
			text.WriteString("\n\n")
		}
		text.WriteString(question.Question)
		for _, option := range question.Options {
			text.WriteString("\n- " + option)
		}
	}
	if text.Len() == 0 {
		return nil, true, nil
	}
	d.wroteText = true
	return []Event{{Kind: EventText, Text: text.String()}}, true, nil
}

// decodeSearch 把完成的 search_web 工具动作（field 19：查询、域名、摘要、[标题, 链接] 列表）并入搜索来源，查询与摘要记入 searches
func (d *AssistantStreamDecoder) decodeSearch(action []json.RawMessage, path string, evidence json.RawMessage) error {
	searchRaw := rawAt(action, 18)
	if isJSONNull(searchRaw) {
		return nil
	}
	search, err := rawArray(searchRaw, path+"[18]", evidence)
	if err != nil {
		return withMethod(err, assistantStreamMethod)
	}
	query, err := rawString(rawAt(search, 0), path+"[18][0]", evidence)
	if err != nil {
		return withMethod(err, assistantStreamMethod)
	}
	if d.grounding == nil {
		d.grounding = &GroundingMetadata{}
	}
	d.grounding.WebSearchQueries = append(d.grounding.WebSearchQueries, query)
	if summaryRaw := rawAt(search, 2); !isJSONNull(summaryRaw) {
		summary, err := rawString(summaryRaw, path+"[18][2]", evidence)
		if err != nil {
			return withMethod(err, assistantStreamMethod)
		}
		d.searches = append(d.searches, fmt.Sprintf(assistantSearchTranscript, query, summary))
	}
	if resultsRaw := rawAt(search, 3); !isJSONNull(resultsRaw) {
		results, err := rawArray(resultsRaw, path+"[18][3]", evidence)
		if err != nil {
			return withMethod(err, assistantStreamMethod)
		}
		for index, resultRaw := range results {
			resultPath := fmt.Sprintf("%s[18][3][%d]", path, index)
			result, err := rawArray(resultRaw, resultPath, evidence)
			if err != nil {
				return withMethod(err, assistantStreamMethod)
			}
			title, err := rawString(rawAt(result, 0), resultPath+"[0]", evidence)
			if err != nil {
				return withMethod(err, assistantStreamMethod)
			}
			uri, err := rawString(rawAt(result, 1), resultPath+"[1]", evidence)
			if err != nil {
				return withMethod(err, assistantStreamMethod)
			}
			d.grounding.Chunks = append(d.grounding.Chunks, GroundingChunk{Source: "web", URI: uri, Title: title})
		}
	}
	return nil
}

// recorded 返回记录的搜索来源与各步骤用量之和
func (d *AssistantStreamDecoder) recorded() []Event {
	var events []Event
	if d.grounding != nil {
		events = append(events, Event{Kind: EventGrounding, Grounding: d.grounding})
	}
	if len(d.usage) > 0 {
		var total Usage
		for _, usage := range d.usage {
			total.InputTokens += usage.InputTokens
			total.CachedTokens += usage.CachedTokens
			total.OutputTokens += usage.OutputTokens
			total.TotalTokens += usage.TotalTokens
		}
		events = append(events, Event{Kind: EventUsage, Usage: &total})
	}
	return events
}

// End 在本轮回复结束、函数调用之后模型继续输出正文或流结束时返回扣下的正文或函数调用、搜索来源、各步骤用量之和与完成事件
func (d *AssistantStreamDecoder) End() ([]Event, error) {
	if !d.finished && !d.stopped && !d.called && !d.wroteText {
		return nil, &ProtocolEvidenceError{Method: assistantStreamMethod, Path: "$", Detail: "流结束前没有完成的模型回复"}
	}
	var events []Event
	switch {
	case d.block:
		events = d.callBlock(d.held + "\n" + assistantFence)
	case !d.called && d.held != "" && (!(d.malformed || d.invalidTool) || strings.TrimSpace(d.held) != ""):
		events = []Event{{Kind: EventText, Text: d.held}}
	}
	events = append(events, d.recorded()...)
	finish := decodeFinishReason(1)
	if !d.called && (d.malformed || d.invalidTool) {
		finish = decodeFinishReason(10)
	}
	return append(events, Event{Kind: EventFinish, FinishReason: finish}), nil
}

// decodeAssistantUsage 解码步骤用量 [未命中缓存的输入, 输出, 缓存命中]
func decodeAssistantUsage(raw json.RawMessage, path string, evidence json.RawMessage) (Usage, error) {
	values, err := rawArray(raw, path, evidence)
	if err != nil {
		return Usage{}, withMethod(err, assistantStreamMethod)
	}
	numbers := make([]int64, 3)
	for index := range numbers {
		if value := rawAt(values, index); !isJSONNull(value) {
			numbers[index], err = rawInt64(value, fmt.Sprintf("%s[%d]", path, index), evidence)
			if err != nil {
				return Usage{}, withMethod(err, assistantStreamMethod)
			}
		}
	}
	input := numbers[0] + numbers[2]
	return Usage{InputTokens: input, CachedTokens: numbers[2], OutputTokens: numbers[1], TotalTokens: input + numbers[1]}, nil
}
