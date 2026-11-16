package aistudio

import (
	"math/rand/v2"
	"slices"
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

// modelRowHeight 与 panelChrome 是模型面板中一行模型的高度与面板中不放模型行的高度，面板在视口中完整容纳 (视口高度 - panelChrome) / modelRowHeight 行
const (
	modelRowHeight = 141
	panelChrome    = 212
)

// pickedTabs 是从 All 分类选中模型后不跟踪地重新曝光的页签
var pickedTabs = []string{"Featured", "Gemini", "Images", "Video", "Audio", "Live"}

// panelRows 返回模型面板在视口中完整容纳的模型行数
func (log *pageElements) panelRows() int {
	return max(1, int((log.viewport-panelChrome)/modelRowHeight))
}

// featuredTab 是打开模型面板时选中的分类
const featuredTab = 10

// visibleModelRows 是打开模型面板后视口内的模型行数
const visibleModelRows = 5

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

// openSelector 记录在 at 点击模型选择卡片，模型面板随后曝光各分类页签与 Featured 分类中的每行模型
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
		log.panel = slices.Clone(log.tabs)
		log.listing = "10"
		rows := 0
		for _, model := range log.models {
			if !slices.Contains(model.categories, featuredTab) {
				continue
			}
			for _, ve := range rowElements(model) {
				seq := log.shown(ve, shown, rowElementDetail(ve, log.listing, model.name), true)
				if rows < visibleModelRows {
					log.panel = append(log.panel, [2]int{seq, ve})
				}
				if ve == veModelRow {
					log.rows[model.name] = seq
				}
			}
			rows++
		}
	})
}

// openAll 记录在 at 点击模型面板的 All 页签，随后按 ListModels 顺序曝光每个模型的一行（带运行环境的模型一行另带环境标记），9 ms 后各页签进入视口：列表停在页面模型下一行为视口最下面一行的位置，视口中完整的各行与其上只露出行底部的一行的行元素不跟踪可见性
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
		log.listing = modelTabs[0].name
		log.listed = log.listed[:0]
		selected := slices.IndexFunc(log.models, func(item catalogModel) bool { return item.name == log.model })
		log.listedBottom = min(selected+1, len(log.models)-1)
		top := log.listedBottom - log.panelRows()
		for index, model := range log.models {
			var row [][3]int
			for _, ve := range rowElements(model) {
				tracked := selected < 0 || index < top || index > log.listedBottom || index == top && ve != veModelRow
				detail := veData{shown: shown.UnixMilli(), detail: rowElementDetail(ve, log.listing, model.name)}
				var seq int
				if ve == veModelRow && model.environment {
					mark := veData{shown: shown.UnixMilli(), detail: append(make([]any, 22), []any{2, model.name})}
					seq = log.record(shown, func(seq int) []any {
						elements := []any{[]any{ve, nil, nil, []any{1}, nil, 1, detail}, []any{veModelEnvironment, nil, nil, nil, nil, 1, mark}}
						return []any{log.ref(seq), elements, nil, nil, nil, nil, nil, nil, []any{log.ref(seq)}}
					})
					log.exposed[ve], log.exposed[veModelEnvironment] = seq, seq
				} else {
					seq = log.shown(ve, shown, detail.detail, tracked)
				}
				flag := 0
				if tracked {
					flag = 1
				}
				row = append(row, [3]int{seq, ve, flag})
				if ve == veModelRow {
					log.rows[model.name] = seq
				}
			}
			log.listed = append(log.listed, row)
		}
		tabsSeen := shown.Add(9 * time.Millisecond)
		for _, item := range log.tabs {
			log.visible(item[0], item[1], tabsSeen)
		}
	})
}

// listedVisible 返回滚到 All 分类第 index 行后视口中的各行与只露出行底部的一行（没有时为 -1）：该行在 All 分类曝光时视口最下面一行之下时滚到视口底部，不是最后一行且其上有第 panelRows 行时该行只露出行底部，列表滚到顶时视口从第一行开始；否则滚到视口顶部，其上一行在面板页签下
func (log *pageElements) listedVisible(index int) (int, int, int) {
	rows := log.panelRows()
	if index <= log.listedBottom {
		return max(0, index-1), min(len(log.listed)-1, index+rows-1), -1
	}
	if index == len(log.listed)-1 || index < rows {
		return max(0, index-rows+1), index, -1
	}
	return index - rows + 1, index, index - rows
}

// pickModel 记录模型面板视口内跟踪可见性的元素在 seen 进入视口、在 at 点击 model 一行，面板关闭后页面以新模型重新曝光入门卡片、Run 按钮与取值变化的思考档位下拉框：Featured 分类中为各页签与前 5 行，All 分类中为滚到 model 一行后视口中的行（见 listedVisible），从 All 分类选中时先曝光 pickedTabs 各页签
func (log *pageElements) pickModel(seen, at time.Time, model string) {
	log.mu.Lock()
	defer log.mu.Unlock()
	if log.closed {
		return
	}
	log.runPending(seen)
	listed := log.listing == modelTabs[0].name
	if listed {
		index := slices.IndexFunc(log.models, func(item catalogModel) bool { return item.name == model })
		first, last, partial := log.listedVisible(max(index, 0))
		if partial >= 0 {
			first = partial
		}
		for current := first; current <= last; current++ {
			for _, item := range log.listed[current] {
				if item[2] == 1 && (current != partial || item[1] == veModelRow) {
					log.visible(item[0], item[1], seen)
				}
			}
		}
	} else {
		for _, item := range log.panel {
			log.visible(item[0], item[1], seen)
		}
	}
	log.clicked(log.rows[model], veModelRow, veData{detail: rowDetail(log.listing, model)}, at)
	log.model = model
	previous := log.level
	log.level = 0
	if index := slices.IndexFunc(log.models, func(item catalogModel) bool { return item.name == model }); index >= 0 {
		log.level = log.models[index].level
	}
	shown := at.Add(renderDelay())
	log.later(shown, func() {
		if listed {
			for _, name := range pickedTabs {
				log.shown(veModelTab, shown, []any{nil, []any{nil, nil, name}}, false)
			}
		}
		if series := modelSeries(log.allSeries, model); series != nil {
			log.series = series
			for _, card := range series.Cards {
				var applet any
				if card.Applet != "" {
					applet = []any{card.Applet}
				}
				log.shown(veCard, shown, []any{nil, []any{model}, applet, nil, series.ID + ":" + card.Title}, false)
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
