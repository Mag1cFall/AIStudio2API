package aistudio

import (
	"encoding/json"
	"math/rand/v2"
	"slices"
	"strconv"
	"strings"
	"time"
)

// 官网运行设置与模型面板中界面元素的 VE 编号
const (
	// veModelTab 是模型面板的分类页签
	veModelTab = 274388
	// veModelRow 是模型面板中的一行模型，veModelBadge、veModelInfo 与 veModelDocs 是该行的标记、说明与文档按钮
	veModelRow   = 274387
	veModelBadge = 358946
	veModelInfo  = 274385
	veModelDocs  = 274386
	// veGrounding 是工具中的 Grounding with Google Search 开关
	veGrounding = 242417
	// veFunctions 是工具中的 Function calling 开关，veFunctionsEdit 是它的编辑按钮
	veFunctions     = 233241
	veFunctionsEdit = 233242
	// veSystemText 是系统指令面板中的输入框
	veSystemText = 324611
	// veModelEnvironment 是模型面板 All 分类中带运行环境的模型一行的环境标记
	veModelEnvironment = 341307
)

// systemPanelElements 是打开系统指令面板时曝光并跟踪可见性的元素
var systemPanelElements = []int{324606, 324609, 324610, veSystemText}

// modelTab 是模型面板的一个分类页签与它在 ListModels 中的分类编号
type modelTab struct {
	name string
	code int64
}

// modelTabs 是模型面板按官网顺序排列的分类页签，All 列出全部模型
var modelTabs = []modelTab{{"All", 0}, {"Featured", 10}, {"Gemini", 17}, {"Live", 18}, {"Images", 15}, {"Video", 14}, {"Audio", 13}, {"Music", 16}, {"Agents", 19}, {"Gemma", 3}}

// 官网模型面板 All 分类与运行设置面板的布局，单位为 CSS 像素
const (
	// panelTop 是模型列表在视口中的上缘，panelBottom 是列表下缘到视口底部的距离，panelGap 是相邻两行的间距
	panelTop    = 211
	panelBottom = 26
	panelGap    = 17
	// rowButtonTop 与 rowButtonSize 是一行中标记、说明与文档按钮相对行上缘的位置与边长
	rowButtonTop  = 20
	rowButtonSize = 28
	// veVisibleSize 是官网 VE 记录器把元素记为可见时它与视口相交部分的最小宽高
	veVisibleSize = 10
)

// categoryCards 是新对话页在页面模型没有入门卡片系列时列出的各分类卡片
var categoryCards = []string{"Featured", "Gemini", "Images", "Video", "Audio", "Live"}

// showCategories 在 at 曝光新对话页的各分类卡片；调用方持有 mu
func (log *pageElements) showCategories(at time.Time) {
	for _, name := range categoryCards {
		log.shown(veModelTab, at, []any{nil, []any{nil, nil, name}}, false)
	}
}

// inView 判断上缘为 top、高为 height 的元素与视口相交的高度达到官网 VE 记录器记为可见的大小
func (log *pageElements) inView(top, height float64) bool {
	return min(top+height, log.viewport)-max(top, 0) >= veVisibleSize
}

// panelRow 是模型面板当前分类列出的一行：model 为 ListModels 中的下标，elements 为该行各元素的曝光序号、VE 与是否跟踪可见性（1 为跟踪）
type panelRow struct {
	model    int
	elements [][3]int
}

// listTops 返回 rows 各行相对列表顶部的上缘与列表总高
func (log *pageElements) listTops(rows []panelRow) ([]float64, float64) {
	tops := make([]float64, len(rows))
	var offset float64
	for index, row := range rows {
		tops[index] = offset
		offset += log.models[row.model].height + panelGap
	}
	return tops, max(offset-panelGap, 0)
}

// listHeight 返回模型列表在视口中的高度
func (log *pageElements) listHeight() float64 {
	return log.viewport - panelTop - panelBottom
}

// clampScroll 返回列表总高为 total 时 scroll 在可滚动范围内的值
func (log *pageElements) clampScroll(scroll, total float64) float64 {
	return max(min(scroll, total-log.listHeight()), 0)
}

// rowsInView 记录 log.listed 中跟踪可见性、在列表滚到 log.listScroll 后进入视口的元素在 seen 可见，按行与行内元素的顺序；调用方持有 mu
func (log *pageElements) rowsInView(seen time.Time) {
	tops, _ := log.listTops(log.listed)
	for index, row := range log.listed {
		model := log.models[row.model]
		for _, item := range row.elements {
			offset, height := listedBox(model, item[1])
			if item[2] == 1 && log.inView(panelTop+tops[index]-log.listScroll+offset, height) {
				log.visible(item[0], item[1], seen)
			}
		}
	}
}

// listedBox 返回 All 分类中 model 一行的 ve 元素相对行上缘的位置与高度
func listedBox(model catalogModel, ve int) (float64, float64) {
	switch ve {
	case veModelRow:
		return 0, model.height
	case veModelEnvironment:
		return model.height - agentMarkBottom, agentMarkHeight
	}
	return rowButtonTop, rowButtonSize
}

// settingsTops 是页面模型卡片高 settingsCardHeight 时运行设置面板中各元素在视口中的上缘与高度
var settingsTops = map[int][2]float64{
	veSelector: {60, settingsCardHeight}, veSystem: {167, 81}, veLevel: {308, 24}, 228153: {417, 32}, 228152: {422.1, 21.7}, 228154: {461, 21.7},
	veFunctionsEdit: {494.7, 32}, veFunctions: {499.8, 21.7}, veGrounding: {547.3, 21.7}, 329280: {589.7, 21.7}, 249371: {631.4, 21.7}, 228155: {694, 36},
}

// settingsCardHeight 是两行说明的模型卡片高度
const settingsCardHeight = 99

// settingsInView 判断运行设置面板中的 ve 元素在页面载入时位于视口内，模型卡片按页面模型的说明行数增高，其后各元素随之下移；不在运行设置面板中的元素都在视口内
func (log *pageElements) settingsInView(ve int) bool {
	box, ok := settingsTops[ve]
	if !ok {
		return true
	}
	card := float64(settingsCardHeight)
	if index := slices.IndexFunc(log.models, func(item catalogModel) bool { return item.name == log.model }); index >= 0 {
		card = log.models[index].card
	}
	if ve == veSelector {
		return log.inView(box[0], card)
	}
	return log.inView(box[0]+card-settingsCardHeight, box[1])
}

// featuredTab 是打开模型面板时选中的分类
const featuredTab = 10

// renderDelay 返回官网界面元素日志在一次点击后记录新渲染元素的间隔
func renderDelay() time.Duration {
	return time.Duration(913+rand.IntN(19)) * time.Millisecond
}

// rowDetail 返回模型面板中 tab 分类下 model 一行的元素参数
func rowDetail(tab string, model string) []any {
	return []any{nil, []any{nil, nil, tab, model}}
}

// featuredModel 判断 model 在页面 ListModels 中属于模型面板打开时的分类
func (log *pageElements) featuredModel(model string) bool {
	log.mu.Lock()
	defer log.mu.Unlock()
	index := slices.IndexFunc(log.models, func(item catalogModel) bool { return item.name == model })
	return index >= 0 && slices.Contains(log.models[index].categories, featuredTab)
}

// listedModel 判断 model 在页面 ListModels 中，模型面板的 All 分类列出它
func (log *pageElements) listedModel(model string) bool {
	log.mu.Lock()
	defer log.mu.Unlock()
	return slices.ContainsFunc(log.models, func(item catalogModel) bool { return item.name == model })
}

// pageLevel 返回页面换成 model 之后思考档位下拉框的值：model 与页面模型相同或不在 ListModels 中时为当前值，否则为 model 的默认档位
func (log *pageElements) pageLevel(model string) int64 {
	log.mu.Lock()
	defer log.mu.Unlock()
	if model == log.model {
		return log.level
	}
	index := slices.IndexFunc(log.models, func(item catalogModel) bool { return item.name == model })
	if index < 0 {
		return log.level
	}
	return log.models[index].level
}

// rowElements 返回模型面板中 model 一行的元素：行、标记、说明，有文档链接时再加文档按钮
func rowElements(model catalogModel) []int {
	if model.docs {
		return []int{veModelRow, veModelBadge, veModelInfo, veModelDocs}
	}
	return []int{veModelRow, veModelBadge, veModelInfo}
}

// rowElementDetail 返回模型面板中 tab 分类下 model 一行中 ve 元素的参数
func rowElementDetail(ve int, tab string, model string) []any {
	if ve == veModelBadge {
		return []any{nil, []any{nil, nil, tab, model, nil, 1}}
	}
	return rowDetail(tab, model)
}

// openSelector 记录在 at 点击模型选择卡片，模型面板随后曝光各分类页签与 Featured 分类中的每行模型，面板展开中曝光的元素都跟踪可见性；Featured 列表把页面模型一行滚到列表中央
func (log *pageElements) openSelector(at time.Time) {
	log.mu.Lock()
	defer log.mu.Unlock()
	if log.closed {
		return
	}
	log.runPending(at)
	log.clicked(log.exposed[veSelector], veSelector, nil, at)
	shown := at.Add(renderDelay())
	log.later(shown, func() {
		log.tabs = log.tabs[:0]
		for _, tab := range modelTabs {
			log.tabs = append(log.tabs, [2]int{log.shown(veModelTab, shown, []any{nil, []any{nil, nil, tab.name}}, true), veModelTab})
		}
		log.listing = strconv.Itoa(featuredTab)
		log.listed = log.listed[:0]
		for index, model := range log.models {
			if !slices.Contains(model.categories, featuredTab) {
				continue
			}
			row := panelRow{model: index}
			for _, ve := range rowElements(model) {
				seq := log.shown(ve, shown, rowElementDetail(ve, log.listing, model.name), true)
				row.elements = append(row.elements, [3]int{seq, ve, 1})
				if ve == veModelRow {
					log.rows[model.name] = seq
				}
			}
			log.listed = append(log.listed, row)
		}
		tops, total := log.listTops(log.listed)
		log.listScroll = 0
		if selected := slices.IndexFunc(log.listed, func(row panelRow) bool { return log.models[row.model].name == log.model }); selected >= 0 {
			log.listScroll = log.clampScroll(tops[selected]+log.models[log.listed[selected].model].height/2-log.listHeight()/2, total)
		}
	})
}

// openAll 记录在 at 点击模型面板的 All 页签，随后按 ListModels 顺序曝光每个模型的一行（带运行环境的模型一行另带环境标记），9 ms 后各页签进入视口：Firefox 的滚动锚定让 Featured 列表中第一个露出的行留在原处，曝光时不在视口内的元素跟踪可见性
func (log *pageElements) openAll(at time.Time) {
	log.mu.Lock()
	defer log.mu.Unlock()
	if log.closed {
		return
	}
	log.runPending(at)
	if len(log.tabs) > 0 {
		log.clicked(log.tabs[0][0], veModelTab, veData{detail: []any{nil, []any{nil, nil, modelTabs[0].name}}}, at)
	}
	shown := at.Add(renderDelay())
	log.later(shown, func() {
		previous, _ := log.listTops(log.listed)
		anchor := -1
		for index, row := range log.listed {
			if panelTop+previous[index]-log.listScroll+log.models[row.model].height > panelTop {
				anchor = index
				break
			}
		}
		all := make([]panelRow, len(log.models))
		for index := range log.models {
			all[index] = panelRow{model: index}
		}
		tops, total := log.listTops(all)
		if anchor >= 0 {
			log.listScroll += tops[log.listed[anchor].model] - previous[anchor]
		}
		log.listScroll = log.clampScroll(log.listScroll, total)
		log.listing = modelTabs[0].name
		for index, model := range log.models {
			for _, ve := range rowElements(model) {
				offset, height := listedBox(model, ve)
				tracked := !log.inView(panelTop+tops[index]-log.listScroll+offset, height)
				detail := veData{shown: shown.UnixMilli(), detail: rowElementDetail(ve, log.listing, model.name)}
				var seq int
				if ve == veModelRow && model.environment {
					mark := veData{shown: shown.UnixMilli(), detail: append(make([]any, 22), []any{2, model.name})}
					seq = log.record(shown, func(seq int) []any {
						elements := []any{[]any{ve, nil, nil, []any{1}, nil, 1, detail}, []any{veModelEnvironment, nil, nil, nil, nil, 1, mark}}
						return []any{log.ref(seq), elements, nil, nil, nil, nil, nil, nil, []any{log.ref(seq)}}
					})
					log.exposed[ve], log.exposed[veModelEnvironment] = seq, seq
					tracked = true
				} else {
					seq = log.shown(ve, shown, detail.detail, tracked)
				}
				flag := 0
				if tracked {
					flag = 1
				}
				all[index].elements = append(all[index].elements, [3]int{seq, ve, flag})
				if ve == veModelRow {
					log.rows[model.name] = seq
				}
			}
		}
		log.listed = all
		tabsSeen := shown.Add(9 * time.Millisecond)
		for _, item := range log.tabs {
			log.visible(item[0], item[1], tabsSeen)
		}
	})
}

// pickModel 记录模型面板中 model 一行滚入列表后跟踪可见性的元素在 seen 进入视口、在 at 点击这一行，面板关闭后页面以新模型重新曝光入门卡片（没有入门卡片系列时为各分类卡片，免费账户换成需要付费的模型时为付费提示）、Run 按钮与取值变化的思考档位下拉框：这一行在列表上方时滚到列表顶部、在下方时滚到列表底部；Featured 分类中各页签先进入视口
func (log *pageElements) pickModel(seen, at time.Time, model string) {
	log.mu.Lock()
	defer log.mu.Unlock()
	if log.closed {
		return
	}
	log.runPending(seen)
	if log.listing != modelTabs[0].name {
		for _, item := range log.tabs {
			log.visible(item[0], item[1], seen)
		}
	}
	if target := slices.IndexFunc(log.listed, func(row panelRow) bool { return log.models[row.model].name == model }); target >= 0 {
		tops, _ := log.listTops(log.listed)
		height := log.models[log.listed[target].model].height
		switch top := panelTop + tops[target] - log.listScroll; {
		case top < panelTop:
			log.listScroll = tops[target]
		case top+height > panelTop+log.listHeight():
			log.listScroll = tops[target] + height - log.listHeight()
		}
	}
	log.rowsInView(seen)
	log.clicked(log.rows[model], veModelRow, veData{detail: rowDetail(log.listing, model)}, at)
	log.model = model
	previous := log.level
	log.level = 0
	if index := slices.IndexFunc(log.models, func(item catalogModel) bool { return item.name == model }); index >= 0 {
		log.level = log.models[index].level
	}
	shown := at.Add(renderDelay())
	log.later(shown, func() {
		log.series = modelSeries(log.allSeries, model)
		if log.series == nil || log.paidNotice() {
			log.showZeroState(shown)
		} else {
			for _, card := range log.series.Cards {
				var applet any
				if card.Applet != "" {
					applet = []any{card.Applet}
				}
				log.shown(veCard, shown, []any{nil, []any{model}, applet, nil, log.series.ID + ":" + card.Title}, false)
			}
		}
		log.shown(veRun, shown, modelDetail(model), false)
		if log.level != 0 && log.level != previous {
			log.shown(veLevel, shown, levelDetail(log.level), false)
		}
	})
}

// changeLevel 记录在 at 从思考档位下拉框选中目录编号 level 的档位
func (log *pageElements) changeLevel(at time.Time, level int64) {
	log.mu.Lock()
	defer log.mu.Unlock()
	if log.closed {
		return
	}
	log.runPending(at)
	log.record(at, func(seq int) []any {
		action := append([]any{veLevel, nil, 3}, make([]any, 10)...)
		action = append(action, []any{[]any{veLevel, veData{detail: levelDetail(level)}}}, []any{veData{at: at.UnixMilli()}})
		return []any{log.ref(seq), nil, nil, action, nil, nil, nil, nil, []any{}}
	})
	log.level = level
	log.levelChanged = true
}

// openSystem 记录在 at 点击系统指令卡片，系统指令面板随后曝光
func (log *pageElements) openSystem(at time.Time) {
	log.mu.Lock()
	defer log.mu.Unlock()
	if log.closed {
		return
	}
	log.runPending(at)
	log.clicked(log.exposed[veSystem], veSystem, veData{detail: systemDetail}, at)
	shown := at.Add(renderDelay())
	log.later(shown, func() {
		for _, ve := range systemPanelElements {
			log.shown(ve, shown, nil, true)
		}
	})
}

// editSystem 记录在 at 点击系统指令输入框，面板中的元素随后进入视口
func (log *pageElements) editSystem(at time.Time) {
	log.mu.Lock()
	defer log.mu.Unlock()
	if log.closed {
		return
	}
	log.runPending(at)
	log.clicked(log.exposed[veSystemText], veSystemText, nil, at)
	seen := at.Add(renderDelay())
	log.later(seen, func() {
		for _, ve := range systemPanelElements {
			log.visible(log.exposed[ve], ve, seen)
		}
	})
}

// toggle 记录在 at 点击工具中 ve 处的开关或按钮
func (log *pageElements) toggle(at time.Time, ve int) {
	log.mu.Lock()
	defer log.mu.Unlock()
	if !log.closed {
		log.runPending(at)
		log.clicked(log.exposed[ve], ve, nil, at)
	}
}

// interGlyphs 是官网 12px Inter 中 ASCII 可见字符的宽度，单位为 Firefox 的 1/60 像素，下标为字符码减 32
var interGlyphs = [95]uint16{
	203, 207, 335, 456, 462, 707, 464, 216, 263, 263, 361, 476, 207, 331, 207, 259,
	454, 293, 439, 445, 465, 427, 446, 407, 445, 446, 207, 217, 476, 476, 476, 368,
	695, 497, 471, 526, 520, 433, 425, 537, 535, 193, 411, 484, 407, 650, 542, 551,
	460, 551, 463, 462, 465, 536, 497, 709, 491, 489, 453, 263, 259, 263, 339, 328,
	232, 404, 441, 411, 441, 420, 266, 442, 426, 174, 174, 395, 174, 631, 425, 432,
	441, 441, 271, 380, 236, 426, 405, 589, 393, 405, 398, 307, 239, 307, 476,
}

// interSymbols 是官网模型说明中出现的其余字符在 12px Inter 中的宽度，单位为 1/60 像素
var interSymbols = map[rune]uint16{'•': 405, '—': 720, '–': 360, '’': 188, '…': 622, 'é': 420}

// interWidth 返回 text 以 12px Inter 排版的宽度，单位为像素，未列出的字符按小写字母的平均宽度计
func interWidth(text string) float64 {
	var total int
	for _, char := range text {
		switch width, ok := interSymbols[char]; {
		case char >= 32 && char < 127:
			total += int(interGlyphs[char-32])
		case ok:
			total += int(width)
		default:
			total += 420
		}
	}
	return float64(total) / 60
}

// interLines 返回 text 在宽 width 的行框中以 12px Inter 按空格折行的行数
func interLines(text string, width float64) int {
	lines, line := 1, 0.0
	space := interWidth(" ")
	for index, word := range strings.Split(text, " ") {
		size := interWidth(word)
		switch {
		case index == 0:
			line = size
		case line+space+size > width:
			lines++
			line = size
		default:
			line += space + size
		}
	}
	return lines
}

// 官网模型面板一行与运行设置模型卡片的排版，单位为 CSS 像素
const (
	// rowHeader 是一行的标题区高度，rowPadding 是详情列表下方的留白，rowItemGap 是详情各项的间距，rowLine 是详情的行高，rowText 是详情文字的行宽
	rowHeader  = 64
	rowPadding = 12
	rowItemGap = 8
	rowLine    = 16
	rowText    = 454
	// agentRow 是带运行环境的模型一行在详情列表之后的所选模型与价格两栏，agentMarkBottom 与 agentMarkHeight 是其中环境标记距行下缘的距离与高度
	agentRow        = 8 + 47 + 8 + 16
	agentMarkBottom = 58
	agentMarkHeight = 22
	// cardBase 是模型卡片中说明之外的高度，cardLine 与 cardText 是说明的行高与行宽
	cardBase = 63
	cardLine = 18
	cardText = 227
)

// modelLayout 按 ListModels 中模型的说明字段（下标 38）返回模型面板中该行的高度与运行设置中模型卡片的高度：一行的详情依次为别名、说明、每档价格或价格说明（带链接时后接 Learn more）、发布日期，各占折行后的行数；带运行环境的模型不列价格，改为所选模型与价格两栏
func modelLayout(details []json.RawMessage, environment bool) (float64, float64) {
	field := func(index int) string {
		var text string
		if len(details) > index {
			_ = json.Unmarshal(details[index], &text)
		}
		return text
	}
	var items []int
	if alias := field(14); alias != "" {
		items = append(items, interLines(alias, rowText))
	}
	description := field(9)
	if description != "" {
		items = append(items, interLines(description, rowText))
	}
	var pricing []json.RawMessage
	if !environment && len(details) > 0 && json.Unmarshal(details[0], &pricing) == nil {
		var note, link string
		if len(pricing) > 4 && json.Unmarshal(pricing[3], &note) == nil && note != "" {
			if json.Unmarshal(pricing[4], &link) == nil && link != "" {
				note += " Learn more"
			}
			items = append(items, interLines(note, rowText))
		} else {
			tiers := 0
			for _, side := range pricing[:min(len(pricing), 2)] {
				var groups [][]json.RawMessage
				if json.Unmarshal(side, &groups) == nil && len(groups) > 0 {
					tiers = max(tiers, len(groups[0]))
				}
			}
			for range tiers {
				items = append(items, 1)
			}
		}
	}
	var released []string
	if len(details) > 19 && json.Unmarshal(details[19], &released) == nil && len(released) > 0 {
		items = append(items, 1)
	}
	height := float64(rowHeader + rowPadding)
	for index, lines := range items {
		height += float64(lines * rowLine)
		if index > 0 {
			height += rowItemGap
		}
	}
	if environment {
		height += agentRow
	}
	card := float64(cardBase)
	if description != "" {
		card += float64(interLines(description, cardText) * cardLine)
	}
	return height, card
}
