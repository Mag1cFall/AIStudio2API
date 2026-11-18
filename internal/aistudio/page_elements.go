package aistudio

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"math"
	"math/rand/v2"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/Mag1cFall/AIStudio2API/internal/camoufoxnative"
	"github.com/Mag1cFall/AIStudio2API/internal/waa/mozjs"
	"github.com/google/uuid"
)

const (
	// pageElementsSource 是官网页面界面元素日志的 log source
	pageElementsSource = 2712
	// pageElementsDelay 是官网界面元素日志从一批的第一条事件到发出这批的延迟
	pageElementsDelay = 10 * time.Second
	// pageElementsBurst 是虚拟时钟上一串界面操作在真实时间中执行完所需的上限，发出时刻已过的一批等它结束再发出
	pageElementsBurst = 200 * time.Millisecond
	// pageElementsBeaconSize 与 pageElementsBeacons 是页面隐藏时每个 sendBeacon 的事件数与 sendBeacon 个数上限
	pageElementsBeaconSize, pageElementsBeacons = 32, 10
	// pageElementsPath 是界面元素日志上下文中官网新对话页的路由
	pageElementsPath = "/" + pageRoute
	// pageElementsHost 是界面元素日志上下文中的页面主机名
	pageElementsHost = "aistudio.google.com"
	// starterModule 是官网新对话页按需加载、内含各模型入门卡片的前端模块
	starterModule = "AgQvWc"
	// starterDependency 是官网新对话页在 starterModule 之前加载的前端模块
	starterDependency = "XVDWwe"
)

// 官网新对话页界面元素的 VE 编号
const (
	veLoaded   = 319850
	veRun      = 225921
	veCard     = 261405
	veAnswered = 312781
	veRerun    = 228331
	veUpgrade  = 365044
	// veSelector 是运行设置中的模型选择卡片
	veSelector = 274382
	// veSystem 是运行设置中的系统指令卡片
	veSystem = 274527
	// veLevel 是运行设置中的思考档位下拉框
	veLevel = 334163
	// veInsert 是输入框旁插入图片、视频、音频与文件的按钮
	veInsert = 225926
)

// systemDetail 是系统指令卡片在对话页的元素参数
var systemDetail = []any{nil, nil, nil, nil, nil, nil, nil, "CHAT"}

// levelDetail 返回思考档位下拉框取值为目录编号 level 时的元素参数
func levelDetail(level int64) []any {
	return append(make([]any, 17), []any{strconv.FormatInt(level, 10)})
}

// pageTrackedElements 是页面框架渲染时曝光并跟踪可见性的元素
var pageTrackedElements = []int{281710, 281711, 268858, 281712, 281713}

// pageShellElements 是页面框架渲染时在跟踪元素之后曝光的元素
var pageShellElements = []int{305470, 227591, 227417, 317577}

// pageMainElements 是权益与模型目录返回后新对话页在第一个元素与入门卡片之后曝光的元素
var pageMainElements = []int{veInsert, 296303, 282217, veRun, veSelector, veSystem, veLevel, 228153, 228152, 228154, 233242, 233241, 242417, 329280, 249371, 228155}

// pageMainFirst 是权益与模型目录返回后新对话页第一个曝光的元素，入门卡片与它同时渲染时紧跟其后
const pageMainFirst = 233240

// pageTurnElements 是对话每个模型轮次下方的一组按钮，第二个与第四个为 Rerun 按钮，这一轮回答的 veTurnLate 在下一次扫描中单独曝光
var pageTurnElements = []int{228330, veRerun, veTurnLate, veRerun}

// pageThoughtTurnElements 是带思考的模型轮次在回答结束时曝光的一组按钮，最后的正文段更多选项按钮在 Rerun 中随正文段曝光
var pageThoughtTurnElements = []int{228330, veRerun, veRerun, veRerun, veChunkOptions}

// veTurnLate 是模型轮次下方按钮组的第三个元素
const veTurnLate = 269715

// pageRunElements 是回答结束后输入区重新曝光的元素
var pageRunElements = []int{234541, 234542}

// pageFunctionCallElements 是回答为函数调用时在回答结束时曝光的模型轮次按钮，输入区不重新曝光
var pageFunctionCallElements = []int{228330, veRerun, veTurnLate}

// pageFunctionCallBlock 是函数调用块渲染时曝光的函数响应输入区
var pageFunctionCallBlock = []int{273046, 273045}

// pageFunctionResultElements 是在函数调用块中提交函数结果后的回答结束时曝光的两个模型轮次的按钮与输入区
var pageFunctionResultElements = []int{228330, veRerun, veTurnLate, veRerun, veTurnLate, veRerun, 234541, 234542}

// veData 是界面元素事件的附加数据，按 JS 对象整数键的升序编码：126 为事件毫秒时刻，340 为页面事件参数，618 为曝光毫秒时刻，1174 为元素参数
type veData struct {
	at     int64
	page   any
	shown  int64
	detail any
}

// MarshalJSON 按官网页面的键序编码附加数据
func (data veData) MarshalJSON() ([]byte, error) {
	return data.appendJSON(nil), nil
}

// appendJSON 把按官网页面键序编码的附加数据追加到 dst
func (data veData) appendJSON(dst []byte) []byte {
	start := len(dst)
	dst = append(dst, '{')
	key := func(name string) {
		if len(dst) > start+1 {
			dst = append(dst, ',')
		}
		dst = append(dst, `"`+name+`":`...)
	}
	if data.at != 0 {
		key("126")
		dst = append(strconv.AppendInt(append(dst, "[["...), data.at, 10), "]]"...)
	}
	if data.page != nil {
		key("340")
		dst = appendScriptJSON(dst, data.page)
	}
	if data.shown != 0 {
		key("618")
		dst = append(strconv.AppendInt(append(dst, "[["...), data.shown, 10), "]]"...)
	}
	if data.detail != nil {
		key("1174")
		dst = appendScriptJSON(dst, data.detail)
	}
	return append(dst, '}')
}

// appendScriptJSON 把 value 按 scriptJSON 的编码追加到 dst：界面元素事件中的 nil、整数、字符串、数组与附加数据直接追加，其余类型经 scriptJSON 编码
func appendScriptJSON(dst []byte, value any) []byte {
	switch value := value.(type) {
	case nil:
		return append(dst, "null"...)
	case int:
		return strconv.AppendInt(dst, int64(value), 10)
	case int64:
		return strconv.AppendInt(dst, value, 10)
	case string:
		return appendScriptString(dst, value)
	case []any:
		dst = append(dst, '[')
		for index, item := range value {
			if index > 0 {
				dst = append(dst, ',')
			}
			dst = appendScriptJSON(dst, item)
		}
		return append(dst, ']')
	case veData:
		return value.appendJSON(dst)
	default:
		encoded, _ := scriptJSON(value)
		return append(dst, encoded...)
	}
}

// appendScriptString 把 text 按 encoding/json 不转义 HTML 字符时的规则编码为 JSON 字符串追加到 dst
func appendScriptString(dst []byte, text string) []byte {
	const hex = "0123456789abcdef"
	dst = append(dst, '"')
	start := 0
	for index := 0; index < len(text); {
		if b := text[index]; b < utf8.RuneSelf {
			if b >= 0x20 && b != '"' && b != '\\' {
				index++
				continue
			}
			dst = append(dst, text[start:index]...)
			switch b {
			case '"', '\\':
				dst = append(dst, '\\', b)
			case '\b':
				dst = append(dst, '\\', 'b')
			case '\f':
				dst = append(dst, '\\', 'f')
			case '\n':
				dst = append(dst, '\\', 'n')
			case '\r':
				dst = append(dst, '\\', 'r')
			case '\t':
				dst = append(dst, '\\', 't')
			default:
				dst = append(dst, '\\', 'u', '0', '0', hex[b>>4], hex[b&0xf])
			}
			index++
			start = index
			continue
		}
		r, size := utf8.DecodeRuneInString(text[index:])
		if r == utf8.RuneError && size == 1 {
			dst = append(append(dst, text[start:index]...), "\ufffd"...)
			index += size
			start = index
			continue
		}
		if r == '\u2028' || r == '\u2029' {
			dst = append(append(dst, text[start:index]...), '\\', 'u', '2', '0', '2', hex[r&0xf])
			index += size
			start = index
			continue
		}
		index += size
	}
	return append(append(dst, text[start:]...), '"')
}

// starterSeries 是 starterModule 中一个模型系列的入门卡片，models 为官网判断页面模型属于该系列的名单
type starterSeries struct {
	ID     string        `json:"id"`
	Models []string      `json:"models"`
	Cards  []starterCard `json:"cards"`
}

// starterCard 是一张入门卡片，applet 为示例应用 ID、preview 为示例应用的预览图，提示词卡片为空
type starterCard struct {
	Title   string `json:"title"`
	Applet  string `json:"applet,omitempty"`
	Preview string `json:"preview,omitempty"`
}

var (
	// starterSeriesPattern 匹配 starterModule 中一个系列对象的开头
	starterSeriesPattern = regexp.MustCompile(`\{id:"([^"\\]+)",type:"[a-z]+"`)
	// starterListPattern 匹配系列以 includes 判断模型名单的变量，属性名随官网构建变化
	starterListPattern = regexp.MustCompile(`[\w$]+:a=>(?:[^,]*?)([\w$]+)\.includes\(a\.getName\(\)\)`)
	// starterArrayPattern 匹配系列中卡片数组的开头，第一个是提示词卡片数组，属性名随官网构建变化
	starterArrayPattern = regexp.MustCompile(`[,{\s]([\w$]+):\[\s*\{\s*title:`)
	// starterTitlePattern 匹配卡片对象的标题
	starterTitlePattern = regexp.MustCompile(`(?:^\{|,)\s*title:("(?:[^"\\]|\\.)*")`)
	// starterAppletPattern 匹配卡片对象的示例应用 ID
	starterAppletPattern = regexp.MustCompile(`,\s*appletId:("(?:[^"\\]|\\.)*")`)
	// starterPreviewPattern 匹配卡片对象的预览图地址
	starterPreviewPattern = regexp.MustCompile(`,\s*preview:("(?:[^"\\]|\\.)*")`)
)

// parseStarterSeries 从 starterModule 的脚本中读出以 includes 判断模型名单的各模型系列与入门卡片：先列出提示词卡片数组中的卡片，再列出 examples 中的示例应用卡片；卡片引用模块之外示例应用（没有标题）的系列不读出
func parseStarterSeries(script []byte) ([]starterSeries, error) {
	text := string(script)
	starts := starterSeriesPattern.FindAllStringSubmatchIndex(text, -1)
	var result []starterSeries
	for index, start := range starts {
		end := len(text)
		if index+1 < len(starts) {
			end = starts[index+1][0]
		}
		block := text[start[0]:end]
		series := starterSeries{ID: text[start[2]:start[3]]}
		match := starterListPattern.FindStringSubmatch(block)
		if match == nil {
			continue
		}
		definition := regexp.MustCompile(`[,;\s]` + regexp.QuoteMeta(match[1]) + `=(\[[^\]]*\])`).FindStringSubmatch(text)
		if definition == nil || json.Unmarshal([]byte(definition[1]), &series.Models) != nil {
			return nil, fmt.Errorf("入门卡片模块缺少系列 %s 的模型名单 %s", series.ID, match[1])
		}
		var arrays []int
		if prompts := starterArrayPattern.FindStringSubmatchIndex(block); prompts != nil && block[prompts[2]:prompts[3]] != "examples" {
			arrays = append(arrays, prompts[3]+1)
		}
		if position := strings.Index(block, "examples:["); position >= 0 {
			arrays = append(arrays, position+len("examples:"))
		}
		complete := true
		for _, open := range arrays {
			array := block[open:jsSpan(block, open)]
			for _, item := range jsObjects(array) {
				title := starterTitlePattern.FindStringSubmatch(item)
				if title == nil {
					complete = false
					break
				}
				card := starterCard{}
				var err error
				if card.Title, err = strconv.Unquote(title[1]); err != nil {
					return nil, fmt.Errorf("入门卡片模块系列 %s 的卡片标题: %w", series.ID, err)
				}
				if applet := starterAppletPattern.FindStringSubmatch(item); applet != nil {
					if card.Applet, err = strconv.Unquote(applet[1]); err != nil {
						return nil, fmt.Errorf("入门卡片模块系列 %s 的示例应用: %w", series.ID, err)
					}
				}
				if preview := starterPreviewPattern.FindStringSubmatch(item); preview != nil {
					if card.Preview, err = strconv.Unquote(preview[1]); err != nil {
						return nil, fmt.Errorf("入门卡片模块系列 %s 的预览图: %w", series.ID, err)
					}
				}
				series.Cards = append(series.Cards, card)
			}
		}
		if complete && len(series.Cards) > 0 {
			result = append(result, series)
		}
	}
	if len(result) == 0 {
		return nil, fmt.Errorf("入门卡片模块缺少模型系列")
	}
	return result, nil
}

// jsSpan 返回 text 中 start 处的括号与之配对的右括号之后的位置，跳过字符串字面量，括号不配对时返回 len(text)
func jsSpan(text string, start int) int {
	depth := 0
	for index := start; index < len(text); index++ {
		switch character := text[index]; character {
		case '"', '\'', '`':
			for index++; index < len(text) && text[index] != character; index++ {
				if text[index] == '\\' {
					index++
				}
			}
		case '[', '{', '(':
			depth++
		case ']', '}', ')':
			depth--
			if depth == 0 {
				return index + 1
			}
		}
	}
	return len(text)
}

// jsObjects 返回 JS 数组字面量中各个顶层对象字面量的文本
func jsObjects(array string) []string {
	var objects []string
	for index := 1; index < len(array); index++ {
		if array[index] == '{' {
			end := jsSpan(array, index)
			objects = append(objects, array[index:end])
			index = end - 1
		}
	}
	return objects
}

// starterModuleURLs 按官网新对话页的按需加载由文档预载的 exm=_b 模块地址推出 starterDependency 与 starterModule 的地址
func starterModuleURLs(document pageDocument) (dependency, module string, err error) {
	for _, resource := range document.resources {
		if resource.kind != pagePreloadScript || !strings.Contains(resource.url, "/exm=_b/") {
			continue
		}
		slash := strings.LastIndex(resource.url, "/")
		if !strings.HasPrefix(resource.url[slash+1:], "m=") {
			continue
		}
		base := resource.url[:slash+1]
		return base + "m=" + starterDependency, strings.Replace(base, "/exm=_b/", "/exm="+starterDependency+",_b/", 1) + "m=" + starterModule, nil
	}
	return "", "", fmt.Errorf("AI Studio 页面缺少 exm=_b 的预载模块")
}

// loadStarters 像官网新对话页一样以脚本请求并发载入 starterDependency 与 starterModule，读出页面模型的入门卡片
func (runtime *goWAARuntime) loadStarters(ctx context.Context, dependency, module string) {
	errs := make([]error, 2)
	var group sync.WaitGroup
	for index, target := range []string{dependency, module} {
		group.Go(func() {
			errs[index] = runtime.fetchCached(ctx, target, subresourceHeaders(target, "*/*", "script", ""))
		})
	}
	group.Wait()
	if err := errors.Join(errs...); err != nil {
		slog.Warn("载入官网入门卡片模块失败", "error", err)
		return
	}
	runtime.elements.starters(runtime.pageCache.starters(module))
}

// pageElements 是一次页面载入的官网界面元素日志（2712）：记录页面载入、元素曝光与可见、点击 Run 与 Rerun 和回答结束，每批从第一条事件起 pageElementsDelay 后以 gzip 正文发出
type pageElements struct {
	send   func(clearcutShape, []byte)
	locale string
	zone   *time.Location
	// context 是上下文中权益之前的字段
	context []any
	model   string

	mu     sync.Mutex
	closed bool
	// origin 是第一条事件的微秒时刻，各事件以它与序号引用
	origin int64
	seq    int
	batch  []encodedEvent
	timer  *time.Timer
	// due 是这一批按第一条事件时刻应发出的时刻，之后时刻的事件进入下一批
	due  time.Time
	tier string
	// contextJSON 是带 contextTier 权益的上下文编码后的 JSON 字符串字面量，同一权益的事件共用
	contextJSON []byte
	contextTier string
	// shellAt 是页面框架元素的曝光时刻，shellDone 表示已曝光
	shellAt   time.Time
	shellDone bool
	// mainAt 是主体元素的曝光时刻，权益与模型目录返回前为零值，mainTier 是从主体元素起写入上下文的权益串
	mainAt   time.Time
	mainTier string
	// tracked 是页面框架中跟踪可见性元素的序号与 VE，trackedSeen 表示它们已进入视口
	tracked     [][2]int
	trackedSeen bool
	// series 是页面模型所属的入门卡片系列，cardsAt 非零时入门卡片已到渲染时刻、等待 series
	series  *starterSeries
	cardsAt time.Time
	cards   bool
	// exposed 是各元素最近一次曝光的序号，点击引用它
	exposed map[int]int
	// level 是思考档位下拉框的当前值，页面模型没有思考档位时为 0
	level int64
	// levelChanged 表示上次点击 Run 之后改过思考档位，下一次回答随回答占位再曝光一次下拉框
	levelChanged bool
	// models 是页面 ListModels 中的模型，模型面板按它列出各行
	models []catalogModel
	// recorded 是上一条事件的时刻
	recorded time.Time
	// queued 是以 later 安排、尚未执行的后续事件
	queued []laterAction
	// allSeries 是 starterModule 中全部模型系列，换模型后按新模型取入门卡片
	allSeries []starterSeries
	// tabs 是模型面板各分类页签的曝光序号与 VE
	tabs [][2]int
	// listing 是模型面板当前分类的元素参数，rows 是各模型行最近一次曝光的序号，listed 是当前分类列出的各行，listScroll 是列表的滚动位置
	listing    string
	rows       map[string]int
	listed     []panelRow
	listScroll float64
	// viewport 与 width 是页面视口的高度与宽度
	viewport, width float64
	// thoughtsRendered 表示本页已渲染过思考面板
	thoughtsRendered bool
	// answers 是本页已完成的回答数
	answers int
	// chips 是上次点击 Run 之后输入框中附件的 CountTokens，还没有时为 nil
	chips   *pageChips
	pending *pageTurn
	// scanned 是 VE 记录器上一次扫描曝光元素的时刻，scanQueued 表示节流窗口结束时还有一次扫描
	scanned    time.Time
	scanQueued bool
	// render 在页面框架、主体与入门卡片渲染时请求其中的图片与字体
	render func(part pageRenderPart, images []string)
}

// pageRenderPart 是页面依次渲染的部分
type pageRenderPart int

const (
	// pageRenderShell 是页面框架
	pageRenderShell pageRenderPart = iota
	// pageRenderMain 是新对话页主体
	pageRenderMain
	// pageRenderCards 是入门卡片
	pageRenderCards
	// pageRenderThoughts 是回答中的思考面板
	pageRenderThoughts
)

// rendered 在页面某部分渲染时请求其中的资源；调用方持有 mu
func (log *pageElements) rendered(part pageRenderPart, images []string) {
	if log.render != nil {
		log.render(part, images)
	}
}

// newPageElements 建立页面载入的界面元素日志：window 为浏览器窗口 ID，experiments 为页面 WIZ 数据的实验 ID
func newPageElements(send func(clearcutShape, []byte), locale string, zone *time.Location, experiments []int64, window string, language string, model string) *pageElements {
	return &pageElements{
		send: send, locale: locale, zone: zone, model: model, exposed: map[int]int{}, rows: map[string]int{},
		context: []any{nil, nil, nil, nil, experiments, pageElementsPath, window, nil, language, nil, pageElementsHost, uuid.NewString()},
	}
}

// pageWindowID 返回官网页面存入 sessionStorage 的浏览器窗口 ID
func pageWindowID() string {
	return strings.ToUpper(uuid.NewString())
}

// ref 返回事件引用
func (log *pageElements) ref(seq int) []any {
	return []any{[]any{log.origin, 0, 0}, seq}
}

// record 以下一个序号记录 body 构造的事件，事件时刻为 at（早于上一条事件时取上一条的时刻），第一条事件开始计时发出这一批；调用方持有 mu
func (log *pageElements) record(at time.Time, body func(seq int) []any) int {
	log.runPending(at)
	log.seq++
	if log.contextJSON == nil || log.contextTier != log.tier {
		context := log.context
		if log.tier != "" {
			context = append(slices.Clone(context), nil, nil, log.tier)
		}
		encodedContext, _ := scriptJSON(context)
		literal, _ := scriptJSON(encodedContext)
		log.contextJSON, log.contextTier = []byte(literal), log.tier
	}
	now := at
	if now.Before(log.recorded) {
		now = log.recorded
	}
	log.recorded = now
	if log.timer != nil && !now.Before(log.due) {
		log.timer.Stop()
		events := log.batch
		log.batch, log.timer = nil, nil
		log.sendGzip(clearcutClient(log.locale, nil, 4), events, time.Now())
	}
	_, offset := now.In(log.zone).Zone()
	head := append(strconv.AppendInt([]byte{'['}, now.UnixMilli(), 10), ",null,null,null,null,null,null,"...)
	tail := strconv.AppendInt([]byte(",null,null,null,null,null,null,"), int64(-offset), 10)
	tail = strconv.AppendInt(append(tail, ",null,null,null,null,null,"...), int64(log.seq), 10)
	tail = append(appendScriptString(append(tail, ",null,null,"...), string(appendScriptJSON(nil, body(log.seq)))), ']')
	log.batch = append(log.batch, encodedEvent{head: head, context: log.contextJSON, tail: tail})
	if log.timer == nil {
		log.due = now.Add(pageElementsDelay)
		log.timer = time.AfterFunc(max(time.Until(log.due), pageElementsBurst), log.flush)
	}
	return log.seq
}

// flush 发出积攒的一批事件：持锁取出这一批并记下发出时刻，编码与压缩在锁外进行
func (log *pageElements) flush() {
	log.mu.Lock()
	if log.closed {
		log.mu.Unlock()
		return
	}
	events, now := log.batch, time.Now()
	log.batch, log.timer = nil, nil
	log.mu.Unlock()
	log.sendGzip(clearcutClient(log.locale, nil, 4), events, now)
}

// sendGzip 以官网页面 CompressionStream 压缩的 gzip 正文发出 events，客户端信息为 client，now 为发出时刻
func (log *pageElements) sendGzip(client []any, events []encodedEvent, now time.Time) {
	body := elementsPayloads.Get().(*bytes.Buffer)
	body.Reset()
	writeElementsPayload(body, client, events, now)
	compressed := mozjs.Gzip(body.Bytes())
	elementsPayloads.Put(body)
	log.send(clearcutGzip, compressed)
}

// elementsPayloads 是压缩界面元素日志批次前复用的正文缓冲区
var elementsPayloads = sync.Pool{New: func() any { return new(bytes.Buffer) }}

// writeElementsPayload 把客户端信息为 client、发出时刻为 now 的一批事件按官网上报正文写入 writer
func writeElementsPayload(writer io.Writer, client []any, events []encodedEvent, now time.Time) {
	head, _ := scriptJSON([]any{client, pageElementsSource})
	_, _ = writer.Write([]byte(head[:len(head)-1] + ",["))
	for index, event := range events {
		if index > 0 {
			_, _ = writer.Write([]byte{','})
		}
		_, _ = writer.Write(event.head)
		_, _ = writer.Write(event.context)
		_, _ = writer.Write(event.tail)
	}
	_, _ = writer.Write([]byte("]," + strconv.FormatInt(now.UnixMilli(), 10) + "]"))
}

// encodedEvent 是一条已编码为 JSON 的界面元素事件：载荷之前的字段、上下文载荷与载荷之后的字段
type encodedEvent struct {
	head, context, tail []byte
}

// pageEvent 记录页面事件
func (log *pageElements) pageEvent(ve int, at time.Time, page any) int {
	return log.record(at, func(seq int) []any {
		element := append([]any{ve, nil, 1}, make([]any, 11)...)
		element = append(element, []any{veData{at: at.UnixMilli(), page: page}})
		return []any{log.ref(seq), nil, nil, element, nil, nil, nil, nil, []any{}}
	})
}

// shown 记录元素曝光并记下它的序号，tracked 为跟踪可见性的元素
func (log *pageElements) shown(ve int, at time.Time, detail any, tracked bool) int {
	seq := log.record(at, func(seq int) []any {
		element := []any{ve, veData{shown: at.UnixMilli(), detail: detail}}
		if tracked {
			element = []any{ve, nil, nil, nil, nil, 1, veData{shown: at.UnixMilli(), detail: detail}}
		}
		return []any{log.ref(seq), []any{element}, nil, nil, nil, nil, nil, nil, []any{log.ref(seq)}}
	})
	log.exposed[ve] = seq
	log.settleScan(at)
	if !log.scanQueued && !at.Before(log.scanned.Add(veThrottle)) {
		log.scanned = at
	}
	return seq
}

// veThrottle 是官网 VE 记录器扫描曝光元素的节流间隔：页面内容变化（对话区滚动、载入）后立即扫描，此后这段时间内的变化合并到窗口结束时再扫描一次
const veThrottle = 500 * time.Millisecond

// scan 返回 at 时刻页面内容变化后 VE 记录器扫描的时刻：节流窗口外即为 at，窗口内为窗口结束时；调用方持有 mu
func (log *pageElements) scan(at time.Time) time.Time {
	log.settleScan(at)
	if end := log.scanned.Add(veThrottle); at.Before(end) {
		log.scanQueued = true
		return end
	}
	log.scanned = at
	return at
}

// settleScan 把 at 之前已到窗口结束时刻的扫描记为上一次扫描；调用方持有 mu
func (log *pageElements) settleScan(at time.Time) {
	if end := log.scanned.Add(veThrottle); log.scanQueued && !at.Before(end) {
		log.scanned, log.scanQueued = end, false
	}
}

// scanFrame 返回扫描时刻 scan 之后在其后的动画帧中完成扫描、曝光新元素的时刻（15–44 ms 后），同一次扫描的元素时刻相同
func scanFrame(scan time.Time) time.Time {
	return scan.Add(time.Duration(15+scan.UnixMilli()%30) * time.Millisecond)
}

// visible 记录跟踪元素进入视口
func (log *pageElements) visible(target int, ve int, at time.Time) int {
	return log.record(at, func(seq int) []any {
		state := []any{[]any{[]any{[]any{nil, []any{0, ve}, log.ref(target)}, 1, nil, at.UnixMilli() * 1000}}}
		return []any{log.ref(seq), nil, log.ref(target), nil, nil, nil, state, nil, []any{log.ref(target)}}
	})
}

// clicked 记录对 target 处元素的点击
func (log *pageElements) clicked(target int, ve int, detail any, at time.Time) int {
	return log.record(at, func(seq int) []any {
		element := []any{ve}
		if detail != nil {
			element = append(element, detail)
		}
		action := append([]any{ve, 0, 3}, make([]any, 10)...)
		action = append(action, []any{element}, []any{veData{at: at.UnixMilli()}})
		return []any{log.ref(seq), nil, log.ref(target), action, nil, nil, nil, nil, []any{log.ref(target)}}
	})
}

// laterAction 是以 later 安排在 at 时刻执行的后续事件
type laterAction struct {
	at     time.Time
	action func()
}

// later 安排在 at 时刻持有 mu 执行 action，at 之后时刻的事件记录前先执行它，日志关闭后不再执行；调用方持有 mu
func (log *pageElements) later(at time.Time, action func()) {
	log.queued = append(log.queued, laterAction{at, action})
	time.AfterFunc(time.Until(at), func() {
		log.mu.Lock()
		defer log.mu.Unlock()
		if !log.closed {
			log.runPending(at)
		}
	})
}

// runPending 按时刻先后执行 at 及之前以 later 安排的后续事件；调用方持有 mu
func (log *pageElements) runPending(at time.Time) {
	for {
		index := -1
		for current, item := range log.queued {
			if !item.at.After(at) && (index < 0 || item.at.Before(log.queued[index].at)) {
				index = current
			}
		}
		if index < 0 {
			return
		}
		item := log.queued[index]
		log.queued = slices.Delete(log.queued, index, index+1)
		item.action()
	}
}

// after 在 at 时刻持有 mu 执行 action，日志关闭后不再执行
func (log *pageElements) after(at time.Time, action func()) {
	time.AfterFunc(time.Until(at), func() {
		log.mu.Lock()
		defer log.mu.Unlock()
		if !log.closed {
			action()
		}
	})
}

// modelDetail 返回带模型名的元素参数
func modelDetail(model string) []any {
	return []any{nil, []any{model}}
}

// load 按官网页面应用启动的顺序安排页面载入事件与页面框架元素的曝光：interactive 为导航计时的 domInteractive 时刻
func (log *pageElements) load(interactive time.Time) {
	loaded := interactive.Add(time.Duration(60+rand.IntN(140)) * time.Millisecond)
	log.mu.Lock()
	log.shellAt = loaded.Add(time.Duration(60+rand.IntN(220)) * time.Millisecond)
	log.mu.Unlock()
	log.after(loaded, func() {
		log.origin = loaded.UnixMicro()
		log.pageEvent(veLoaded, loaded, []any{nil, nil, nil, nil, nil, nil, nil, nil, nil, []any{3, 2}})
		log.after(log.shellAt, log.showShell)
	})
}

// showShell 曝光页面框架元素，主体元素的渲染时刻已定时随后安排主体元素；调用方持有 mu
func (log *pageElements) showShell() {
	for _, ve := range pageTrackedElements {
		log.tracked = append(log.tracked, [2]int{log.shown(ve, log.shellAt, nil, true), ve})
	}
	for _, ve := range pageShellElements {
		log.shown(ve, log.shellAt, nil, false)
	}
	log.shellDone = true
	log.rendered(pageRenderShell, nil)
	seen := log.shellAt.Add(time.Duration(405+rand.IntN(10)) * time.Millisecond)
	log.later(seen, func() { log.showTracked(seen) })
	if !log.mainAt.IsZero() {
		log.after(log.mainAt, log.showMain)
	}
}

// showTracked 记录页面框架中跟踪可见性的元素在 at 进入视口，每页一次：官网 60 Hz 页面在框架曝光约 0.41 s 后，主体元素先渲染时在其后 4–6 ms；调用方持有 mu
func (log *pageElements) showTracked(at time.Time) {
	if log.trackedSeen {
		return
	}
	log.trackedSeen = true
	for _, item := range log.tracked {
		log.visible(item[0], item[1], at)
	}
}

// catalog 在权益与模型目录返回（at）后定下新对话页主体元素的渲染时刻，不早于页面框架：tier 为官网权益串，免费账户为空，models 为页面 ListModels 中各模型的名称与默认思考档位，思考档位取页面模型的默认档位
func (log *pageElements) catalog(at time.Time, tier string, models []catalogModel) {
	log.mu.Lock()
	defer log.mu.Unlock()
	if index := slices.IndexFunc(models, func(model catalogModel) bool { return model.name == log.model }); index >= 0 {
		log.level = models[index].level
	}
	log.models = models
	log.mainTier = tier
	log.mainAt = at.Add(time.Duration(390+rand.IntN(20)) * time.Millisecond)
	if log.mainAt.Before(log.shellAt) {
		log.mainAt = log.shellAt
	}
	if log.shellDone {
		log.after(log.mainAt, log.showMain)
	}
}

// showMain 曝光新对话页主体元素（免费账户先曝光侧栏的升级卡片），入门卡片已载入时有一半机会随第一个元素同时曝光，再安排跟踪元素的可见与其余入门卡片；调用方持有 mu
func (log *pageElements) showMain() {
	main := log.mainAt
	log.tier = log.mainTier
	if log.tier == "" {
		log.shown(veUpgrade, main, nil, false)
	}
	log.shown(pageMainFirst, main, nil, false)
	log.rendered(pageRenderMain, nil)
	if log.series != nil && rand.IntN(2) == 0 {
		log.showCards(main)
	}
	log.showMainElements(main)
	visible := main.Add(time.Duration(4+rand.IntN(3)) * time.Millisecond)
	log.after(visible, func() {
		log.showTracked(visible)
		if log.cards {
			return
		}
		cards := main.Add(time.Duration(260+rand.IntN(750)) * time.Millisecond)
		log.after(cards, func() {
			log.cardsAt = cards
			if log.series != nil {
				log.showCards(cards)
			}
		})
	})
}

// showMainElements 在 at 曝光新对话页输入区与运行设置面板的元素，运行设置面板中在视口之外的元素跟踪可见性；调用方持有 mu
func (log *pageElements) showMainElements(at time.Time) {
	for _, ve := range pageMainElements {
		var detail any
		switch ve {
		case veRun:
			detail = modelDetail(log.model)
		case veSystem:
			detail = systemDetail
		case veLevel:
			if log.level == 0 {
				continue
			}
			detail = levelDetail(log.level)
		}
		log.shown(ve, at, detail, !log.settingsInView(ve))
	}
}

// newChat 记录点 New chat 并确认后页面在 at 切换到新对话页：同一时刻曝光主体元素，页面模型没有入门卡片系列时先曝光各分类卡片，有时 0.40–0.45 s 后曝光入门卡片；之后的第一次回答按页面第一次回答记录
func (log *pageElements) newChat(at time.Time) {
	log.mu.Lock()
	defer log.mu.Unlock()
	if !log.closed {
		log.later(at, func() { log.showNewChat(at) })
	}
}

// showNewChat 在 at 曝光新对话页，见 newChat；调用方持有 mu
func (log *pageElements) showNewChat(at time.Time) {
	log.answers = 0
	log.cards = false
	log.shown(pageMainFirst, at, nil, false)
	if log.series == nil || log.paidNotice() {
		log.showZeroState(at)
	}
	log.showMainElements(at)
	if log.series != nil && !log.paidNotice() {
		cards := at.Add(time.Duration(400+rand.IntN(50)) * time.Millisecond)
		log.later(cards, func() { log.showCards(cards) })
	}
}

// vePaidNotice 是免费账户的页面模型需要付费时新对话页在入门卡片处显示的付费提示
const vePaidNotice = 296304

// paidNotice 判断新对话页显示付费提示：免费账户的页面模型在 ListModels 中标为需要付费；调用方持有 mu
func (log *pageElements) paidNotice() bool {
	if log.tier != "" {
		return false
	}
	index := slices.IndexFunc(log.models, func(item catalogModel) bool { return item.name == log.model })
	return index >= 0 && log.models[index].paid
}

// showZeroState 在 at 曝光页面模型没有入门卡片时新对话页的内容：付费提示或各分类卡片；调用方持有 mu
func (log *pageElements) showZeroState(at time.Time) {
	if log.paidNotice() {
		log.shown(vePaidNotice, at, nil, false)
		return
	}
	log.showCategories(at)
}

// starters 设置 starterModule 的模型系列，入门卡片已到渲染时刻时随即曝光
func (log *pageElements) starters(series []starterSeries) {
	log.mu.Lock()
	defer log.mu.Unlock()
	log.allSeries = series
	log.series = modelSeries(series, log.model)
	if !log.closed && log.series != nil && !log.cardsAt.IsZero() {
		log.showCards(time.Now())
	}
}

// modelSeries 返回以 includes 判断模型名单包含 model 的第一个系列，没有时为 nil
func modelSeries(series []starterSeries, model string) *starterSeries {
	for index := range series {
		if slices.Contains(series[index].Models, model) {
			return &series[index]
		}
	}
	return nil
}

// showCards 曝光页面模型所属系列的入门卡片，每页一次；调用方持有 mu
func (log *pageElements) showCards(at time.Time) {
	if log.cards {
		return
	}
	log.cards = true
	var previews []string
	for _, card := range log.series.Cards {
		if card.Preview != "" {
			previews = append(previews, card.Preview)
		}
	}
	log.rendered(pageRenderCards, previews)
	for _, card := range log.series.Cards {
		var applet any
		if card.Applet != "" {
			applet = []any{card.Applet}
		}
		log.shown(veCard, at, []any{nil, []any{log.model}, applet, nil, log.series.ID + ":" + card.Title}, false)
	}
}

// press 在 at 时刻记录一次点击 Run（rerun 时为 Rerun），随后发出的 GenerateContent 由 turn 取走
func (log *pageElements) press(at time.Time, rerun bool) {
	log.mu.Lock()
	defer log.mu.Unlock()
	if log.closed {
		return
	}
	log.runPending(at)
	if rerun {
		log.clicked(log.exposed[veRerun], veRerun, nil, at)
	} else {
		log.clicked(log.exposed[veRun], veRun, veData{detail: modelDetail(log.model)}, at)
	}
	log.pending = &pageTurn{log: log, rerun: rerun, pressed: at}
}

// submit 记录在函数调用块中提交函数结果：提交按钮没有点击事件，随后的回答按提交函数结果的回答记录
func (log *pageElements) submit() {
	log.mu.Lock()
	defer log.mu.Unlock()
	if !log.closed {
		log.pending = &pageTurn{log: log, result: true, pressed: time.Now()}
	}
}

// pageTurn 是一次点击后页面发出的 GenerateContent 在界面上的回答
type pageTurn struct {
	log   *pageElements
	rerun bool
	// pressed 是点击 Run、Rerun 或提交函数结果的时刻
	pressed time.Time
	model   string
	// turns 是回答后对话中的模型轮次数
	turns int
	// first 表示这是本页第一次回答
	first bool
	// counting 表示回答伴随一次 CountTokens，counted 为它完成的时刻，prompted 为它统计的输入框 token 数
	counting bool
	counted  time.Time
	prompted int
	// chips 是这一轮输入框中附件的 CountTokens，没有附件时为 nil
	chips  *pageChips
	closed time.Time
	// attachments 是最后一个 user 轮次的附件数，每个附件在对话中是单独一段
	attachments int
	// thoughts 表示回答中出现了思考 Part
	thoughts bool
	// stopped 表示 Rerun 换成 Stop 的按钮已安排曝光，回答结束时 Run 按钮随之重新曝光
	stopped bool
	// result 表示这是提交函数结果后的回答，call 表示回答中出现了函数调用 Part
	result, call bool
	// block 是函数调用块的输入区等待曝光的扫描时刻，与回答结束组同一次扫描时按页面顺序排在模型轮次按钮之后
	block time.Time
	// tracked 是 Rerun 中跟踪可见性的正文段等待曝光的扫描时刻
	tracked time.Time
	// height 是点击 Run 后对话各轮与回答占位不含回答正文的估算高度，answer 是已到达的回答正文，scrolled 表示这一轮已安排对话区滚动条曝光
	height   float64
	answer   string
	scrolled bool
	// input、output 与 total 是响应最后一帧 usage 中的输入、可见输出与总 token 数
	input, output, total int
}

// turn 取走等待中的点击并按 GenerateContent 正文建立回答，没有点击时返回 nil；counting 表示请求伴随 CountTokens
func (log *pageElements) turn(body []byte, counting bool) *pageTurn {
	log.mu.Lock()
	defer log.mu.Unlock()
	turn := log.pending
	log.pending = nil
	if turn == nil {
		return nil
	}
	var contents []json.RawMessage
	turn.model = log.model
	if fields, ok := wireArray(body); ok && len(fields) > 1 {
		_ = json.Unmarshal(fields[0], &turn.model)
		contents, _ = wireArray(fields[1])
	}
	turn.turns = 1
	turn.height = chatTurn
	for _, raw := range contents {
		content, _ := wireArray(raw)
		var role string
		if len(content) > 1 && json.Unmarshal(content[1], &role) == nil && role == "model" {
			turn.turns++
			turn.height += chatThoughts
		}
		turn.height += chatTurn + float64(log.chatLines(wireText(content)))*chatLine
	}
	turn.scrolled = !turn.rerun && !turn.result && turn.height > log.viewport-chatChrome
	turn.first = log.answers == 0
	turn.chips, log.chips = log.chips, nil
	if turn.chips != nil {
		turn.chips.turn = turn
	}
	turn.counting = counting
	turn.attachments = camoufoxnative.GenerateAttachments(body)
	return turn
}

// changed 安排页面内容在 at 时刻变化（对话区重新布局并滚动）：VE 记录器按节流扫描，show 在这次扫描完成的时刻曝光新渲染的元素，show 为 nil 时只推进节流；调用方持有 mu
func (log *pageElements) changed(at time.Time, show func(shown time.Time)) {
	log.later(at, func() {
		scan := log.scan(at)
		if show == nil {
			return
		}
		shown := scanFrame(scan)
		log.later(shown, func() { show(shown) })
	})
}

// started 在点击 Run 或提交函数结果后 80–150 ms 页面渲染对话轮次时安排回答占位的曝光：用户轮次与模型占位的各段与换成 Stop 的按钮，提交函数结果只多模型占位一段；跟踪可见性的模型占位在其后一帧渲染，扫描已排在节流窗口结束时与其余元素一同曝光（排在 Stop 按钮之前），否则在窗口结束时单独曝光。Rerun 在原处重新生成，对话区不滚动，换成 Stop 的按钮在回答中第一次扫描时曝光
func (turn *pageTurn) started() {
	if turn.rerun {
		return
	}
	log := turn.log
	log.mu.Lock()
	defer log.mu.Unlock()
	render := turn.pressed.Add(time.Duration(80+rand.IntN(70)) * time.Millisecond)
	var placeholder time.Time
	together := false
	log.changed(render, func(shown time.Time) {
		together = placeholder.Equal(shown)
		if turn.first {
			log.shown(313896, shown, nil, false)
		}
		chunks := 2 + turn.attachments
		if turn.result {
			chunks = 1
		}
		for range chunks {
			log.shown(veChunkOptions, shown, nil, false)
		}
		if together {
			log.shown(243616, shown, nil, true)
		}
		if turn.scrolled {
			log.shown(veScrollbar, shown, nil, false)
		}
		log.shown(250044, shown, modelDetail(turn.model), false)
		if log.levelChanged {
			log.shown(veLevel, shown, levelDetail(log.level), false)
			log.levelChanged = false
		}
	})
	tracked := render.Add(25 * time.Millisecond)
	log.later(tracked, func() {
		placeholder = scanFrame(log.scan(tracked))
		log.later(placeholder, func() {
			if !together {
				log.shown(243616, placeholder, nil, true)
			}
		})
	})
}

// thought 在回答的第一个思考 Part 到达（at）时渲染思考面板，带附件的对话约 1 s 后长出对话区滚动条
func (turn *pageTurn) thought(at time.Time) {
	log := turn.log
	log.mu.Lock()
	defer log.mu.Unlock()
	turn.thoughts = true
	turn.change(at, func(shown time.Time) {
		log.shown(veThoughts, shown, append(make([]any, 18), []any{1, 2}), false)
		if !log.thoughtsRendered {
			log.thoughtsRendered = true
			log.rendered(pageRenderThoughts, nil)
		}
	})
	if turn.attachments > 0 && !turn.rerun && !turn.scrolled {
		turn.scrolled = true
		scrollbar := at.Add(time.Duration(980+rand.IntN(30)) * time.Millisecond)
		log.changed(scrollbar, func(shown time.Time) { log.shown(veScrollbar, shown, nil, false) })
	}
}

// answerText 在 Rerun 的思考之后第一个回答正文 Part 到达（at）时渲染正文段，曝光它的更多选项按钮，跟踪可见性的正文段在节流窗口结束时曝光，这次扫描同时曝光回答结束的一组元素时排在输入区之后
func (turn *pageTurn) answerText(at time.Time) {
	log := turn.log
	log.mu.Lock()
	defer log.mu.Unlock()
	turn.change(at, func(shown time.Time) { log.shown(veChunkOptions, shown, nil, false) })
	text := at.Add(10 * time.Millisecond)
	log.later(text, func() {
		shown := scanFrame(log.scan(text))
		turn.tracked = shown
		log.later(shown, func() {
			log.later(shown, func() {
				if turn.tracked.Equal(shown) {
					turn.tracked = time.Time{}
					log.shown(243616, shown, nil, true)
				}
			})
		})
	})
}

// functionCall 在回答的第一个函数调用 Part 到达（at）时渲染函数调用块，在其后的扫描中曝光其中的函数响应输入区
func (turn *pageTurn) functionCall(at time.Time) {
	log := turn.log
	log.mu.Lock()
	defer log.mu.Unlock()
	turn.call = true
	turn.change(at, func(shown time.Time) {
		turn.block = shown
		log.later(shown, func() {
			if turn.block.Equal(shown) {
				turn.block = time.Time{}
				turn.showBlock(shown)
			}
		})
	})
}

// showBlock 曝光函数调用块的输入区；调用方持有 mu
func (turn *pageTurn) showBlock(shown time.Time) {
	for _, ve := range pageFunctionCallBlock {
		turn.log.shown(ve, shown, nil, false)
	}
}

// grew 记录点击 Run 的回答在 at 时刻又到达回答正文 text：对话内容按官网对话区排版估算第一次超出视口时对话区长出滚动条，在这次内容变化后的扫描中曝光两次（官网 60 Hz 现场的长回答在流式到超出视口时同一次扫描曝光两个 273488）；回答占位时已超出视口的这一轮不再曝光
func (turn *pageTurn) grew(at time.Time, text string) {
	log := turn.log
	log.mu.Lock()
	defer log.mu.Unlock()
	if turn.rerun || turn.result || turn.scrolled {
		return
	}
	turn.answer += text
	height := turn.height + float64(log.chatLines(turn.answer))*chatLine
	if turn.thoughts {
		height += chatThoughts
	}
	if height <= log.viewport-chatChrome {
		return
	}
	turn.scrolled = true
	turn.change(at, func(shown time.Time) {
		log.shown(veScrollbar, shown, nil, false)
		log.shown(veScrollbar, shown, nil, false)
	})
}

// 官网对话区的排版，单位为 CSS 像素，由 2026-11-16 三个视口（1600×775、1680×917、1920×947）每秒 60 帧会话现场中 273488 曝光时的对话内容推出：chatLine 是正文行高，chatTurn 是一轮正文之外的高度，chatThoughts 是模型轮次收起的思考面板，chatChrome 是视口中顶栏与输入框占去的高度，chatColumn 是对话列的最大宽度，chatSides 是视口宽度中导航栏与运行设置面板，chatPadding 是一轮正文两侧的内边距，chatFont 是正文字号
const (
	chatLine     = 20
	chatTurn     = 30
	chatThoughts = 40
	chatChrome   = 182
	chatColumn   = 1000
	chatSides    = 600
	chatPadding  = 48
	chatFont     = 14
)

// chatLines 返回 text 在对话区按正文字号与行宽折行的行数，每个换行另起一行；CJK 字符按全角宽度计
func (log *pageElements) chatLines(text string) int {
	if text == "" {
		return 0
	}
	column := min(chatColumn, log.width-chatSides) - chatPadding
	lines := 0
	for line := range strings.SplitSeq(text, "\n") {
		width := 0.0
		for _, char := range line {
			if char >= 0x2E80 {
				width += chatFont
			} else {
				width += interWidth(string(char)) * chatFont / 12
			}
		}
		lines += max(1, int(math.Ceil(width/column)))
	}
	return lines
}

// wireText 返回 GenerateContent 正文中一个对话轮次的正文，不含思考
func wireText(content []json.RawMessage) string {
	if len(content) == 0 {
		return ""
	}
	parts, _ := wireArray(content[0])
	var text strings.Builder
	for _, raw := range parts {
		part, _ := wireArray(raw)
		var value string
		if len(part) < 2 || json.Unmarshal(part[1], &value) != nil || len(part) > 12 && string(part[12]) == "1" {
			continue
		}
		text.WriteString(value)
	}
	return text.String()
}

// streamed 记录回答在 at 时刻又到达一段正文，对话区随之滚动
func (turn *pageTurn) streamed(at time.Time) {
	turn.log.mu.Lock()
	turn.change(at, nil)
	turn.log.mu.Unlock()
}

// change 安排回答中的页面内容在 at 时刻变化，Rerun 换成 Stop 的按钮在点击后的第一次扫描中随其余元素之后曝光；调用方持有 mu
func (turn *pageTurn) change(at time.Time, show func(shown time.Time)) {
	if !turn.rerun || turn.stopped {
		turn.log.changed(at, show)
		return
	}
	turn.stopped = true
	turn.log.changed(at, func(shown time.Time) {
		if show != nil {
			show(shown)
		}
		turn.log.shown(250044, shown, modelDetail(turn.model), false)
	})
}

// done 在响应正文关闭（at）后 50–59 ms（回答结束的页面事件之后）渲染各模型轮次的按钮与输入区并在其后的扫描中曝光，点击 Run 的回答再记录回答结束；函数调用与提交函数结果后的回答按官网各自的一组元素曝光，Rerun 的 Run 按钮只在换成 Stop 的按钮曝光过时重新曝光，点击 Run 的带思考的回答与同一次扫描中等待曝光的 Rerun 正文段在输入区之后曝光跟踪可见性的正文段，这一轮模型轮次的 veTurnLate 紧随其后渲染、在下一次扫描中曝光
func (turn *pageTurn) done(at time.Time) {
	log := turn.log
	log.mu.Lock()
	log.changed(at.Add(time.Duration(50+rand.IntN(10))*time.Millisecond), func(shown time.Time) {
		log.answers++
		if turn.call || turn.result {
			elements := pageFunctionCallElements
			if turn.result {
				elements = pageFunctionResultElements
			}
			block := turn.block.Equal(shown)
			turn.block = time.Time{}
			for _, ve := range elements {
				log.shown(ve, shown, nil, false)
			}
			if block {
				turn.showBlock(shown)
			}
			log.shown(veRun, shown, modelDetail(turn.model), false)
			return
		}
		text := turn.thoughts && (!turn.rerun || turn.tracked.Equal(shown))
		if text {
			turn.tracked = time.Time{}
		}
		for group := 1; group < turn.turns; group++ {
			for _, ve := range pageTurnElements {
				log.shown(ve, shown, nil, false)
			}
		}
		if turn.thoughts {
			elements := pageThoughtTurnElements
			if turn.rerun {
				elements = elements[:len(elements)-1]
			}
			for _, ve := range elements {
				log.shown(ve, shown, nil, false)
			}
		} else {
			for _, ve := range pageTurnElements {
				if ve != veTurnLate {
					log.shown(ve, shown, nil, false)
				}
			}
		}
		for _, ve := range pageRunElements {
			log.shown(ve, shown, nil, false)
		}
		if text {
			log.shown(243616, shown, nil, true)
		}
		if !turn.rerun || turn.stopped {
			log.shown(veRun, shown, modelDetail(turn.model), false)
		}
		log.changed(shown.Add(10*time.Millisecond), func(late time.Time) { log.shown(veTurnLate, late, nil, false) })
	})
	if turn.rerun || turn.result {
		log.mu.Unlock()
		return
	}
	turn.closed = at
	ready := turn.ready()
	log.mu.Unlock()
	if ready {
		turn.answered()
	}
}

// count 记录伴随的 CountTokens 在 at 时刻完成、统计出 tokens 个 token，正文已关闭且附件的 CountTokens 都已返回时随即安排回答结束
func (turn *pageTurn) count(at time.Time, tokens int) {
	log := turn.log
	log.mu.Lock()
	turn.counted = at
	turn.prompted = tokens
	ready := turn.ready()
	log.mu.Unlock()
	if ready {
		turn.answered()
	}
}

// ready 判断回答结束所需的统计都已到齐：正文已关闭，伴随的 CountTokens 与输入框中附件的 CountTokens 都已返回；调用方持有 mu
func (turn *pageTurn) ready() bool {
	return !turn.closed.IsZero() && (!turn.counting || !turn.counted.IsZero()) && (turn.chips == nil || turn.chips.pending == 0)
}

// usage 记下响应最后一帧 usage 中的输入、可见输出与总 token 数
func (turn *pageTurn) usage(input, output, total int) {
	turn.log.mu.Lock()
	turn.input, turn.output, turn.total = input, output, total
	turn.log.mu.Unlock()
}

// pageChips 是一次点击 Run 之前输入框中附件的 CountTokens：pending 为尚未返回的个数，tokens 为已统计出的 token 数之和，counted 为最近一个返回的时刻，turn 为点击 Run 后带这些附件的回答；由 mu 保护
type pageChips struct {
	pending int
	tokens  int
	counted time.Time
	turn    *pageTurn
}

// chipCounting 记下输入框中一个附件发出了 CountTokens，返回它返回时以统计出的 token 数（失败为 0）调用的函数，带这个附件的回答在等它时随即安排回答结束
func (log *pageElements) chipCounting() func(tokens int) {
	log.mu.Lock()
	defer log.mu.Unlock()
	if log.chips == nil {
		log.chips = &pageChips{}
	}
	chips := log.chips
	chips.pending++
	return func(tokens int) {
		log.mu.Lock()
		chips.pending--
		chips.tokens += tokens
		chips.counted = time.Now()
		turn := chips.turn
		ready := turn != nil && turn.ready()
		log.mu.Unlock()
		if ready {
			turn.answered()
		}
	}
}

// answered 在正文关闭后 44–49 ms、伴随的 CountTokens 与附件的 CountTokens 完成后 4 ms 中最晚的时刻记录回答结束，参数为页面 token 统计在这一轮增加的输入与输出 token 数：输入为伴随 CountTokens 统计的输入框与各附件的 token 数之和，输出为可见输出，显示了思考面板时再加思考
func (turn *pageTurn) answered() {
	at := turn.closed.Add(time.Duration(44+rand.IntN(6)) * time.Millisecond)
	if counted := turn.counted.Add(4 * time.Millisecond); counted.After(at) {
		at = counted
	}
	input := turn.prompted
	if turn.chips != nil {
		input += turn.chips.tokens
		if counted := turn.chips.counted.Add(4 * time.Millisecond); counted.After(at) {
			at = counted
		}
	}
	log := turn.log
	log.after(at, func() {
		output := turn.output
		if turn.thoughts {
			output = turn.total - turn.input
		}
		log.pageEvent(veAnswered, at, []any{nil, nil, nil, nil, nil, nil, nil, nil, []any{input, output, nil, turn.model}})
	})
}

// close 像官网页面隐藏或卸载时一样发出尚未发出的事件并停止日志：每 pageElementsBeaconSize 条一个明文 sendBeacon，至多 pageElementsBeacons 个，其余以 gzip 正文发出，客户端信息标记为页面隐藏时发出
func (log *pageElements) close() {
	log.mu.Lock()
	defer log.mu.Unlock()
	if log.closed {
		return
	}
	log.closed = true
	if log.timer != nil {
		log.timer.Stop()
	}
	client := clearcutClient(log.locale, nil, 3)
	client[10].([]any)[9] = []any{3, 1}
	now := time.Now()
	for beacons := 0; len(log.batch) > 0 && beacons < pageElementsBeacons; beacons++ {
		size := min(len(log.batch), pageElementsBeaconSize)
		var body bytes.Buffer
		writeElementsPayload(&body, client, log.batch[:size], now)
		log.send(clearcutBeacon, body.Bytes())
		log.batch = log.batch[size:]
	}
	if len(log.batch) > 0 {
		log.sendGzip(client, log.batch, now)
	}
	log.batch = nil
}

// 官网对话区中界面元素的 VE 编号
const (
	// veThoughts 是回答中的思考面板
	veThoughts = 334158
	// veScrollbar 是对话区的滚动条
	veScrollbar = 273488
	// veChunkOptions 是对话中每段内容的更多选项按钮
	veChunkOptions = 228332
)

// catalogModel 是页面 ListModels 中一个模型的名称、默认思考档位、在模型面板中的分类与排版
type catalogModel struct {
	name string
	// level 是默认思考档位的目录编号，没有思考档位时为 0
	level int64
	// categories 是模型所在的模型面板分类编号
	categories []int64
	// docs 表示模型有文档链接，模型面板中该行多一个文档按钮
	docs bool
	// environment 表示模型带能力码 65（运行环境），模型面板 All 分类中该行带环境标记
	environment bool
	// height 是模型面板中该行的高度，card 是运行设置中该模型卡片的高度
	height, card float64
	// paid 表示模型在免费账户上需要付费（ListModels 下标 77 为 2）
	paid bool
}

// parseCatalogModels 按目录顺序从 ListModels 响应读出各模型的名称、默认思考档位与模型面板中的排版
func parseCatalogModels(body []byte) []catalogModel {
	raw, err := io.ReadAll(newSparseJSONReader(bytes.NewReader(body)))
	if err != nil {
		return nil
	}
	var root []json.RawMessage
	var rows [][]json.RawMessage
	if json.Unmarshal(raw, &root) != nil || len(root) == 0 || json.Unmarshal(root[0], &rows) != nil {
		return nil
	}
	models := make([]catalogModel, 0, len(rows))
	for _, row := range rows {
		var model catalogModel
		if len(row) == 0 || json.Unmarshal(row[0], &model.name) != nil {
			continue
		}
		var thinking []json.RawMessage
		if len(row) > 71 && json.Unmarshal(row[71], &thinking) == nil && len(thinking) > 5 {
			_ = json.Unmarshal(thinking[5], &model.level)
		}
		if len(row) > 74 {
			_ = json.Unmarshal(row[74], &model.categories)
		}
		var capabilities []int64
		if len(row) > 64 && json.Unmarshal(row[64], &capabilities) == nil {
			model.environment = slices.Contains(capabilities, 65)
		}
		var details []json.RawMessage
		var docs string
		if len(row) > 38 && json.Unmarshal(row[38], &details) == nil && len(details) > 15 && json.Unmarshal(details[15], &docs) == nil {
			model.docs = docs != ""
		}
		var billing int
		if len(row) > 77 && json.Unmarshal(row[77], &billing) == nil {
			model.paid = billing == 2
		}
		model.height, model.card = modelLayout(details, model.environment)
		models = append(models, model)
	}
	return models
}

// pageModel 返回页面当前的模型
func (log *pageElements) pageModel() string {
	log.mu.Lock()
	defer log.mu.Unlock()
	return log.model
}

// lastRecorded 返回页面上一条已记录事件的时刻
func (log *pageElements) lastRecorded() time.Time {
	log.mu.Lock()
	defer log.mu.Unlock()
	return log.recorded
}

// veDisclaimer 是首次上传素材提示确认框中的确认按钮
const veDisclaimer = 265442

// insertClicked 记录点击输入框旁的插入按钮：官网插入文件时先点它打开菜单，再点菜单中的上传文件，两次都记为该按钮的点击
func (log *pageElements) insertClicked(at time.Time) {
	log.mu.Lock()
	defer log.mu.Unlock()
	if !log.closed {
		log.clicked(log.exposed[veInsert], veInsert, nil, at)
	}
}

// disclaimer 记录选好文件后弹出的首次上传素材提示确认框在 shown 显示、在 confirmed 点击确认
func (log *pageElements) disclaimer(shown, confirmed time.Time) {
	log.mu.Lock()
	defer log.mu.Unlock()
	if !log.closed {
		log.clicked(log.shown(veDisclaimer, shown, nil, false), veDisclaimer, nil, confirmed)
	}
}
