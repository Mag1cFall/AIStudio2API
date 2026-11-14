package aistudio

import (
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"math/rand/v2"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/Mag1cFall/AIStudio2API/internal/camoufoxnative"
	"github.com/google/uuid"
)

const (
	// pageElementsSource 是官网页面界面元素日志的 log source
	pageElementsSource = 2712
	// pageElementsDelay 是官网界面元素日志从一批的第一条事件到发出这批的延迟
	pageElementsDelay = 10 * time.Second
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

// pageTurnElements 是对话每个模型轮次下方的一组按钮，第二个与第四个为 Rerun 按钮
var pageTurnElements = []int{228330, veRerun, veTurnLate, veRerun}

// pageThoughtTurnElements 是带思考的模型轮次在回答结束时曝光的一组按钮，veTurnLate 约 1 s 后单独曝光
var pageThoughtTurnElements = []int{228330, veRerun, veRerun, veRerun, veChunkOptions}

// veTurnLate 是模型轮次下方按钮组的第三个元素
const veTurnLate = 269715

// pageRunElements 是回答结束后输入区重新曝光的元素
var pageRunElements = []int{234541, 234542}

// pageFunctionCallElements 是回答为函数调用时在回答结束时曝光的模型轮次按钮与函数调用块中的函数响应输入区，输入区不重新曝光
var pageFunctionCallElements = []int{228330, veRerun, veTurnLate, 273046, 273045}

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
	var buffer bytes.Buffer
	buffer.WriteByte('{')
	write := func(key string, value any) error {
		if buffer.Len() > 1 {
			buffer.WriteByte(',')
		}
		encoded, err := scriptJSON(value)
		buffer.WriteString(`"` + key + `":` + encoded)
		return err
	}
	if data.at != 0 {
		if err := write("126", []any{[]any{data.at}}); err != nil {
			return nil, err
		}
	}
	if data.page != nil {
		if err := write("340", data.page); err != nil {
			return nil, err
		}
	}
	if data.shown != 0 {
		if err := write("618", []any{[]any{data.shown}}); err != nil {
			return nil, err
		}
	}
	if data.detail != nil {
		if err := write("1174", data.detail); err != nil {
			return nil, err
		}
	}
	buffer.WriteByte('}')
	return buffer.Bytes(), nil
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
	// starterListPattern 匹配系列以 includes 判断模型名单的变量
	starterListPattern = regexp.MustCompile(`Ek:a=>(?:[^,]*?)([\w$]+)\.includes\(a\.getName\(\)\)`)
	// starterTitlePattern 匹配卡片对象的标题
	starterTitlePattern = regexp.MustCompile(`(?:^\{|,)\s*title:("(?:[^"\\]|\\.)*")`)
	// starterAppletPattern 匹配卡片对象的示例应用 ID
	starterAppletPattern = regexp.MustCompile(`,\s*appletId:("(?:[^"\\]|\\.)*")`)
	// starterPreviewPattern 匹配卡片对象的预览图地址
	starterPreviewPattern = regexp.MustCompile(`,\s*preview:("(?:[^"\\]|\\.)*")`)
)

// parseStarterSeries 从 starterModule 的脚本中读出以 includes 判断模型名单的各模型系列与入门卡片：先列出 yl 中的提示词卡片，再列出 examples 中的示例应用卡片；卡片引用模块之外示例应用（没有标题）的系列不读出
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
		complete := true
		for _, field := range []string{"yl:[", "examples:["} {
			position := strings.Index(block, field)
			if position < 0 {
				continue
			}
			array := block[position+len(field)-1 : jsSpan(block, position+len(field)-1)]
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
	tier   string
	// contextJSON 是带 contextTier 权益的上下文编码后的 JSON 字符串字面量，同一权益的事件共用
	contextJSON []byte
	contextTier string
	// shellAt 是页面框架元素的曝光时刻，shellDone 表示已曝光
	shellAt   time.Time
	shellDone bool
	// mainAt 是主体元素的曝光时刻，权益与模型目录返回前为零值，mainTier 是从主体元素起写入上下文的权益串
	mainAt   time.Time
	mainTier string
	// tracked 是跟踪可见性元素的序号与 VE
	tracked [][2]int
	// series 是页面模型所属的入门卡片系列，cardsAt 非零时入门卡片已到渲染时刻、等待 series
	series  *starterSeries
	cardsAt time.Time
	cards   bool
	// exposed 是各元素最近一次曝光的序号，点击引用它
	exposed map[int]int
	// level 是思考档位下拉框的当前值，页面模型没有思考档位时为 0
	level int64
	// thoughtsRendered 表示本页已渲染过思考面板
	thoughtsRendered bool
	// answers 是本页已完成的回答数
	answers int
	// chips 是上次点击 Run 之后输入框中附件的 CountTokens 总数
	chips   int
	pending *pageTurn
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
		send: send, locale: locale, zone: zone, model: model, exposed: map[int]int{},
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

// record 以下一个序号记录 body 构造的事件，第一条事件开始计时发出这一批；调用方持有 mu
func (log *pageElements) record(body func(seq int) []any) int {
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
	encodedBody, _ := scriptJSON(body(log.seq))
	event := clearcutEvent(24, time.Now(), log.zone, "")
	event[20] = log.seq
	event[23] = encodedBody
	head, _ := scriptJSON(event[:7])
	tail, _ := scriptJSON(event[8:])
	log.batch = append(log.batch, encodedEvent{head: []byte(head[:len(head)-1] + ","), context: log.contextJSON, tail: []byte("," + tail[1:])})
	if log.timer == nil {
		log.timer = time.AfterFunc(pageElementsDelay, log.flush)
	}
	return log.seq
}

// flush 发出积攒的一批事件
func (log *pageElements) flush() {
	log.mu.Lock()
	defer log.mu.Unlock()
	if log.closed {
		return
	}
	log.sendGzip(clearcutClient(log.locale, nil, 4), log.batch)
	log.batch, log.timer = nil, nil
}

// sendGzip 以 gzip 正文发出 events，客户端信息为 client
func (log *pageElements) sendGzip(client []any, events []encodedEvent) {
	var body bytes.Buffer
	writer := pageElementsWriters.Get().(*gzip.Writer)
	defer pageElementsWriters.Put(writer)
	writer.Reset(&body)
	writer.OS = 10
	writeElementsPayload(writer, client, events)
	_ = writer.Close()
	log.send(clearcutGzip, body.Bytes())
}

// writeElementsPayload 把客户端信息为 client 的一批事件按官网上报正文写入 writer
func writeElementsPayload(writer io.Writer, client []any, events []encodedEvent) {
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
	_, _ = writer.Write([]byte("]," + strconv.FormatInt(time.Now().UnixMilli(), 10) + "]"))
}

// encodedEvent 是一条已编码为 JSON 的界面元素事件：载荷之前的字段、上下文载荷与载荷之后的字段
type encodedEvent struct {
	head, context, tail []byte
}

// pageElementsWriters 是发出界面元素日志批次时复用的 gzip 写入器
var pageElementsWriters = sync.Pool{New: func() any {
	writer, _ := gzip.NewWriterLevel(nil, gzip.DefaultCompression)
	return writer
}}

// pageEvent 记录页面事件
func (log *pageElements) pageEvent(ve int, at time.Time, page any) int {
	return log.record(func(seq int) []any {
		element := append([]any{ve, nil, 1}, make([]any, 11)...)
		element = append(element, []any{veData{at: at.UnixMilli(), page: page}})
		return []any{log.ref(seq), nil, nil, element, nil, nil, nil, nil, []any{}}
	})
}

// shown 记录元素曝光并记下它的序号，tracked 为跟踪可见性的元素
func (log *pageElements) shown(ve int, at time.Time, detail any, tracked bool) int {
	seq := log.record(func(seq int) []any {
		element := []any{ve, veData{shown: at.UnixMilli(), detail: detail}}
		if tracked {
			element = []any{ve, nil, nil, nil, nil, 1, veData{shown: at.UnixMilli(), detail: detail}}
		}
		return []any{log.ref(seq), []any{element}, nil, nil, nil, nil, nil, nil, []any{log.ref(seq)}}
	})
	log.exposed[ve] = seq
	return seq
}

// visible 记录跟踪元素进入视口
func (log *pageElements) visible(target int, ve int, at time.Time) int {
	return log.record(func(seq int) []any {
		state := []any{[]any{[]any{[]any{nil, []any{0, ve}, log.ref(target)}, 1, nil, at.UnixMilli() * 1000}}}
		return []any{log.ref(seq), nil, log.ref(target), nil, nil, nil, state, nil, []any{log.ref(target)}}
	})
}

// clicked 记录对 target 处元素的点击
func (log *pageElements) clicked(target int, ve int, detail any, at time.Time) int {
	return log.record(func(seq int) []any {
		element := []any{ve}
		if detail != nil {
			element = append(element, detail)
		}
		action := append([]any{ve, 0, 3}, make([]any, 10)...)
		action = append(action, []any{element}, []any{veData{at: at.UnixMilli()}})
		return []any{log.ref(seq), nil, log.ref(target), action, nil, nil, nil, nil, []any{log.ref(target)}}
	})
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
	if !log.mainAt.IsZero() {
		log.after(log.mainAt, log.showMain)
	}
}

// catalog 在权益与模型目录返回（at）后定下新对话页主体元素的渲染时刻，不早于页面框架：tier 为官网权益串，免费账户为空，models 为页面 ListModels 中各模型的名称与默认思考档位，思考档位取页面模型的默认档位
func (log *pageElements) catalog(at time.Time, tier string, models []catalogModel) {
	log.mu.Lock()
	defer log.mu.Unlock()
	if index := slices.IndexFunc(models, func(model catalogModel) bool { return model.name == log.model }); index >= 0 {
		log.level = models[index].level
	}
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
		log.shown(ve, main, detail, false)
	}
	visible := main.Add(time.Duration(4+rand.IntN(3)) * time.Millisecond)
	log.after(visible, func() {
		for _, item := range log.tracked {
			log.visible(item[0], item[1], visible)
		}
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

// starters 设置 starterModule 的模型系列，入门卡片已到渲染时刻时随即曝光
func (log *pageElements) starters(series []starterSeries) {
	log.mu.Lock()
	defer log.mu.Unlock()
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
	if rerun {
		log.clicked(log.exposed[veRerun], veRerun, nil, at)
	} else {
		log.clicked(log.exposed[veRun], veRun, veData{detail: modelDetail(log.model)}, at)
	}
	log.pending = &pageTurn{log: log, rerun: rerun}
}

// submit 记录在函数调用块中提交函数结果：提交按钮没有点击事件，随后的回答按提交函数结果的回答记录
func (log *pageElements) submit() {
	log.mu.Lock()
	defer log.mu.Unlock()
	if !log.closed {
		log.pending = &pageTurn{log: log, result: true}
	}
}

// pageTurn 是一次点击后页面发出的 GenerateContent 在界面上的回答
type pageTurn struct {
	log   *pageElements
	rerun bool
	model string
	// turns 是回答后对话中的模型轮次数
	turns int
	// first 表示这是本页第一次回答
	first bool
	// counting 表示回答伴随一次 CountTokens，counted 为它完成的时刻，prompted 为它统计的输入框 token 数
	counting bool
	counted  time.Time
	prompted int
	// chips 是这一轮输入框中附件的 CountTokens 总数
	chips  int
	closed time.Time
	// attachments 是最后一个 user 轮次的附件数，每个附件在对话中是单独一段
	attachments int
	// thoughts 表示回答中出现了思考 Part
	thoughts bool
	// result 表示这是提交函数结果后的回答，call 表示回答中出现了函数调用 Part
	result, call bool
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
	var fields []json.RawMessage
	var contents [][]json.RawMessage
	turn.model = log.model
	if json.Unmarshal(body, &fields) == nil && len(fields) > 1 {
		_ = json.Unmarshal(fields[0], &turn.model)
		_ = json.Unmarshal(fields[1], &contents)
	}
	turn.turns = 1
	for _, content := range contents {
		var role string
		if len(content) > 1 && json.Unmarshal(content[1], &role) == nil && role == "model" {
			turn.turns++
		}
	}
	turn.first = log.answers == 0
	turn.chips, log.chips = log.chips, 0
	turn.counting = counting
	turn.attachments = camoufoxnative.GenerateAttachments(body)
	return turn
}

// started 在 GenerateContent 响应头到达（at）时曝光回答占位，与 Run 按钮在指针下换成 Stop 同时渲染，Rerun 没有这一步
func (turn *pageTurn) started(at time.Time) {
	if turn.rerun {
		return
	}
	log := turn.log
	log.after(at, func() {
		if turn.first {
			log.shown(313896, at, nil, false)
		}
		chunks := 2 + turn.attachments
		if turn.result {
			chunks = 1
		}
		for range chunks {
			log.shown(veChunkOptions, at, nil, false)
		}
		log.shown(243616, at, nil, true)
		if turn.first {
			log.shown(veScrollbar, at, nil, false)
		}
		log.shown(250044, at, modelDetail(turn.model), false)
	})
}

// thought 在回答的第一个思考 Part 到达（at）时曝光思考面板，带附件的对话随后长出对话区滚动条；Rerun 没有这一步
func (turn *pageTurn) thought(at time.Time) {
	if turn.rerun {
		return
	}
	log := turn.log
	log.after(at, func() {
		turn.thoughts = true
		log.shown(veThoughts, at, append(make([]any, 18), []any{1, 2}), false)
		if !log.thoughtsRendered {
			log.thoughtsRendered = true
			log.rendered(pageRenderThoughts, nil)
		}
	})
	if turn.attachments > 0 {
		scrollbar := at.Add(time.Duration(980+rand.IntN(30)) * time.Millisecond)
		log.after(scrollbar, func() { log.shown(veScrollbar, scrollbar, nil, false) })
	}
}

// functionCall 在回答的第一个函数调用 Part 到达（at）时记下回答是函数调用
func (turn *pageTurn) functionCall(at time.Time) {
	turn.log.after(at, func() { turn.call = true })
}

// done 在响应正文关闭（at）后安排各模型轮次的按钮与输入区的曝光，点击 Run 的回答再记录回答结束；函数调用与提交函数结果后的回答按官网各自的一组元素曝光
func (turn *pageTurn) done(at time.Time) {
	log := turn.log
	shown := at.Add(time.Duration(50+rand.IntN(20)) * time.Millisecond)
	log.after(shown, func() {
		log.answers++
		if turn.call || turn.result {
			elements := pageFunctionCallElements
			if turn.result {
				elements = pageFunctionResultElements
			}
			for _, ve := range elements {
				log.shown(ve, shown, nil, false)
			}
			log.shown(veRun, shown, modelDetail(turn.model), false)
			return
		}
		groups := turn.turns
		if turn.thoughts {
			groups--
		}
		for range groups {
			for _, ve := range pageTurnElements {
				log.shown(ve, shown, nil, false)
			}
		}
		if turn.thoughts {
			for _, ve := range pageThoughtTurnElements {
				log.shown(ve, shown, nil, false)
			}
		}
		for _, ve := range pageRunElements {
			log.shown(ve, shown, nil, false)
		}
		if !turn.rerun {
			log.shown(veRun, shown, modelDetail(turn.model), false)
		}
		if turn.thoughts {
			feedback := shown.Add(time.Duration(980+rand.IntN(30)) * time.Millisecond)
			log.after(feedback, func() { log.shown(veTurnLate, feedback, nil, false) })
		}
	})
	if turn.rerun || turn.result {
		return
	}
	log.mu.Lock()
	turn.closed = at
	ready := !turn.counting || !turn.counted.IsZero()
	log.mu.Unlock()
	if ready {
		turn.answered()
	}
}

// count 记录伴随的 CountTokens 在 at 时刻完成、统计出 tokens 个 token，正文已关闭时随即安排回答结束
func (turn *pageTurn) count(at time.Time, tokens int) {
	log := turn.log
	log.mu.Lock()
	turn.counted = at
	turn.prompted = tokens
	ready := !turn.closed.IsZero()
	log.mu.Unlock()
	if ready {
		turn.answered()
	}
}

// usage 记下响应最后一帧 usage 中的输入、可见输出与总 token 数
func (turn *pageTurn) usage(input, output, total int) {
	turn.log.mu.Lock()
	turn.input, turn.output, turn.total = input, output, total
	turn.log.mu.Unlock()
}

// chipCounted 记下输入框中一个附件的 CountTokens 统计出 tokens 个 token
func (log *pageElements) chipCounted(tokens int) {
	log.mu.Lock()
	log.chips += tokens
	log.mu.Unlock()
}

// answered 在正文关闭后 44–49 ms 与 CountTokens 完成后 4 ms 的较晚者记录回答结束，参数为页面 token 统计在这一轮增加的输入与输出 token 数：输入为伴随 CountTokens 统计的输入框与各附件的 token 数之和，输出为可见输出，显示了思考面板时再加思考
func (turn *pageTurn) answered() {
	at := turn.closed.Add(time.Duration(44+rand.IntN(6)) * time.Millisecond)
	if counted := turn.counted.Add(4 * time.Millisecond); counted.After(at) {
		at = counted
	}
	log := turn.log
	log.after(at, func() {
		output := turn.output
		if turn.thoughts {
			output = turn.total - turn.input
		}
		log.pageEvent(veAnswered, at, []any{nil, nil, nil, nil, nil, nil, nil, nil, []any{turn.prompted + turn.chips, output, nil, turn.model}})
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
	for beacons := 0; len(log.batch) > 0 && beacons < pageElementsBeacons; beacons++ {
		size := min(len(log.batch), pageElementsBeaconSize)
		var body bytes.Buffer
		writeElementsPayload(&body, client, log.batch[:size])
		log.send(clearcutBeacon, body.Bytes())
		log.batch = log.batch[size:]
	}
	if len(log.batch) > 0 {
		log.sendGzip(client, log.batch)
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

// catalogModel 是页面 ListModels 中一个模型的名称与默认思考档位
type catalogModel struct {
	name string
	// level 是默认思考档位的目录编号，没有思考档位时为 0
	level int64
}

// parseCatalogModels 按目录顺序从 ListModels 响应读出各模型的名称与默认思考档位
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
