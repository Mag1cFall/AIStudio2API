package aistudio

import (
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"math/rand/v2"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
)

const (
	// pageElementsSource 是官网页面界面元素日志的 log source
	pageElementsSource = 2712
	// pageElementsDelay 是官网界面元素日志从一批的第一条事件到发出这批的延迟
	pageElementsDelay = 10 * time.Second
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
)

// pageTrackedElements 是页面框架渲染时曝光并跟踪可见性的元素
var pageTrackedElements = []int{281710, 281711, 268858, 281712, 281713}

// pageShellElements 是页面框架渲染时在跟踪元素之后曝光的元素
var pageShellElements = []int{305470, 227591, 227417, 317577}

// pageMainElements 是权益与模型目录返回后新对话页在第一个元素与入门卡片之后曝光的元素
var pageMainElements = []int{225926, 296303, 282217, veRun, 274382, 274527, 334163, 228153, 228152, 228154, 233242, 233241, 242417, 329280, 249371, 228155}

// pageMainFirst 是权益与模型目录返回后新对话页第一个曝光的元素，入门卡片与它同时渲染时紧跟其后
const pageMainFirst = 233240

// pageTurnElements 是对话每个模型轮次下方的一组按钮，第二个与第四个为 Rerun 按钮
var pageTurnElements = []int{228330, veRerun, 269715, veRerun}

// pageRunElements 是回答结束后输入区重新曝光的元素
var pageRunElements = []int{234541, 234542}

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

// starterCard 是一张入门卡片，applet 为示例应用 ID，提示词卡片为空
type starterCard struct {
	Title  string `json:"title"`
	Applet string `json:"applet,omitempty"`
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
	send   func([]byte)
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
	batch  []any
	timer  *time.Timer
	tier   string
	// shellAt 是页面框架元素的曝光时刻，shellDone 表示已曝光
	shellAt   time.Time
	shellDone bool
	// mainAt 是主体元素的曝光时刻，权益与模型目录返回前为零值，mainTier 是从主体元素起写入上下文的权益串
	mainAt   time.Time
	mainTier string
	// tracked 是跟踪可见性元素的序号与 VE
	tracked [][2]int
	// series 是页面模型所属系列，cardsAt 非零时入门卡片已到渲染时刻、等待 series
	series  *starterSeries
	cardsAt time.Time
	cards   bool
	// run 是最近一次曝光的 Run 按钮序号，rerun 是最后一个模型轮次的 Rerun 按钮序号
	run   int
	rerun int
	// answers 是本页已完成的回答数
	answers int
	pending *pageTurn
}

// newPageElements 建立页面载入的界面元素日志：window 为浏览器窗口 ID，experiments 为页面 WIZ 数据的实验 ID
func newPageElements(send func([]byte), locale string, zone *time.Location, experiments []int64, window string, language string, model string) *pageElements {
	return &pageElements{
		send: send, locale: locale, zone: zone, model: model,
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
	now := time.Now()
	context := log.context
	if log.tier != "" {
		context = append(slices.Clone(context), nil, nil, log.tier)
	}
	encodedContext, _ := scriptJSON(context)
	encodedBody, _ := scriptJSON(body(log.seq))
	event := clearcutEvent(24, now, log.zone, encodedContext)
	event[20] = log.seq
	event[23] = encodedBody
	log.batch = append(log.batch, event)
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
	now := time.Now()
	encoded, _ := scriptJSON([]any{clearcutClient(log.locale, nil, 4), pageElementsSource, log.batch, now.UnixMilli()})
	var body bytes.Buffer
	writer, _ := gzip.NewWriterLevel(&body, gzip.DefaultCompression)
	writer.OS = 10
	_, _ = writer.Write([]byte(encoded))
	_ = writer.Close()
	log.batch, log.timer = nil, nil
	log.send(body.Bytes())
}

// pageEvent 记录页面事件
func (log *pageElements) pageEvent(ve int, at time.Time, page any) int {
	return log.record(func(seq int) []any {
		element := append([]any{ve, nil, 1}, make([]any, 11)...)
		element = append(element, []any{veData{at: at.UnixMilli(), page: page}})
		return []any{log.ref(seq), nil, nil, element, nil, nil, nil, nil, []any{}}
	})
}

// shown 记录元素曝光，tracked 为跟踪可见性的元素
func (log *pageElements) shown(ve int, at time.Time, detail any, tracked bool) int {
	return log.record(func(seq int) []any {
		element := []any{ve, veData{shown: at.UnixMilli(), detail: detail}}
		if tracked {
			element = []any{ve, nil, nil, nil, nil, 1, veData{shown: at.UnixMilli()}}
		}
		return []any{log.ref(seq), []any{element}, nil, nil, nil, nil, nil, nil, []any{log.ref(seq)}}
	})
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
	if !log.mainAt.IsZero() {
		log.after(log.mainAt, log.showMain)
	}
}

// catalog 在权益与模型目录返回（at）后定下新对话页主体元素的渲染时刻，不早于页面框架：tier 为官网权益串，免费账户为空
func (log *pageElements) catalog(at time.Time, tier string) {
	log.mu.Lock()
	defer log.mu.Unlock()
	log.mainTier = tier
	log.mainAt = at.Add(time.Duration(390+rand.IntN(20)) * time.Millisecond)
	if log.mainAt.Before(log.shellAt) {
		log.mainAt = log.shellAt
	}
	if log.shellDone {
		log.after(log.mainAt, log.showMain)
	}
}

// showMain 曝光新对话页主体元素，入门卡片已载入时有一半机会随第一个元素同时曝光，再安排跟踪元素的可见与其余入门卡片；调用方持有 mu
func (log *pageElements) showMain() {
	main := log.mainAt
	log.tier = log.mainTier
	log.shown(pageMainFirst, main, nil, false)
	if log.series != nil && rand.IntN(2) == 0 {
		log.showCards(main)
	}
	for _, ve := range pageMainElements {
		var detail any
		switch ve {
		case veRun:
			detail = modelDetail(log.model)
		case 274527:
			detail = []any{nil, nil, nil, nil, nil, nil, nil, "CHAT"}
		case 334163:
			detail = append(make([]any, 17), []any{"2"})
		}
		seq := log.shown(ve, main, detail, false)
		if ve == veRun {
			log.run = seq
		}
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
	for _, item := range series {
		if slices.Contains(item.Models, log.model) {
			log.series = &item
			break
		}
	}
	if !log.closed && log.series != nil && !log.cardsAt.IsZero() {
		log.showCards(time.Now())
	}
}

// showCards 曝光页面模型所属系列的入门卡片，每页一次；调用方持有 mu
func (log *pageElements) showCards(at time.Time) {
	if log.cards {
		return
	}
	log.cards = true
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
		log.clicked(log.rerun, veRerun, nil, at)
	} else {
		log.clicked(log.run, veRun, veData{detail: modelDetail(log.model)}, at)
	}
	log.pending = &pageTurn{log: log, rerun: rerun}
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
	// counting 表示回答伴随一次 CountTokens，counted 为它完成的时刻
	counting bool
	counted  time.Time
	closed   time.Time
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
	turn.counting = counting
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
		log.shown(228332, at, nil, false)
		log.shown(228332, at, nil, false)
		log.shown(243616, at, nil, true)
		if turn.first {
			log.shown(273488, at, nil, false)
		}
		log.shown(250044, at, modelDetail(turn.model), false)
	})
}

// done 在响应正文关闭（at）后安排各模型轮次的按钮与输入区的曝光，点击 Run 的回答再记录回答结束
func (turn *pageTurn) done(at time.Time) {
	log := turn.log
	shown := at.Add(time.Duration(50+rand.IntN(20)) * time.Millisecond)
	log.after(shown, func() {
		log.answers++
		for range turn.turns {
			for _, ve := range pageTurnElements {
				seq := log.shown(ve, shown, nil, false)
				if ve == veRerun {
					log.rerun = seq
				}
			}
		}
		for _, ve := range pageRunElements {
			log.shown(ve, shown, nil, false)
		}
		if !turn.rerun {
			log.run = log.shown(veRun, shown, modelDetail(turn.model), false)
		}
	})
	if turn.rerun {
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

// count 记录伴随的 CountTokens 在 at 时刻完成，正文已关闭时随即安排回答结束
func (turn *pageTurn) count(at time.Time) {
	log := turn.log
	log.mu.Lock()
	turn.counted = at
	ready := !turn.closed.IsZero()
	log.mu.Unlock()
	if ready {
		turn.answered()
	}
}

// answered 在正文关闭后 44–49 ms 与 CountTokens 完成后 4 ms 的较晚者记录回答结束
func (turn *pageTurn) answered() {
	at := turn.closed.Add(time.Duration(44+rand.IntN(6)) * time.Millisecond)
	if counted := turn.counted.Add(4 * time.Millisecond); counted.After(at) {
		at = counted
	}
	turn.log.after(at, func() {
		turn.log.pageEvent(veAnswered, at, []any{nil, nil, nil, nil, nil, nil, nil, nil, []any{8, 1, nil, turn.model}})
	})
}

// close 停止尚未发出的界面元素日志
func (log *pageElements) close() {
	log.mu.Lock()
	defer log.mu.Unlock()
	log.closed = true
	if log.timer != nil {
		log.timer.Stop()
	}
}
