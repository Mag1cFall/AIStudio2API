package aistudio

import (
	"bytes"
	"encoding/json"
	"strings"
	"time"

	"github.com/Mag1cFall/AIStudio2API/internal/waa"
)

// pageSettings 是 SpiderMonkey 页面运行设置中由界面操作改过的状态
type pageSettings struct {
	// system 与 tools 是上次写入页面的系统指令与工具声明的 GenerateContent 编码
	system, tools json.RawMessage
	// groundingOff 表示已关闭 Grounding with Google Search，functions 表示已打开 Function calling
	groundingOff, functions bool
}

// 官网用户在运行设置中两次操作之间的间隔，从上一次抬起或粘贴到指针移向下一处
const (
	// selectorPause 是点击模型选择卡片到指针移到模型面板中的一行
	selectorPause = 2900 * time.Millisecond
	// pickedPause 是点击模型行或关闭面板后到下一次操作
	pickedPause = 3900 * time.Millisecond
	// levelPause 是打开思考档位下拉框到点击选项
	levelPause = 1900 * time.Millisecond
	// systemPause 是点击系统指令卡片到点击面板中的输入框
	systemPause = 2900 * time.Millisecond
	// closePause 是粘贴系统指令或函数声明到点击关闭或保存
	closePause = 1450 * time.Millisecond
	// togglePause 是工具中相邻两次点击开关或按钮的间隔
	togglePause = 1900 * time.Millisecond
	// dialogPause 是点击函数声明的编辑按钮到点击对话框中的输入框
	dialogPause = 5900 * time.Millisecond
)

// settingSteps 返回点击 Run 之前像官网用户一样把页面运行设置改成 body 所需的各组可信界面操作：请求模型在 ListModels 中且与页面模型不同时在模型面板中换模型（不在 Featured 分类时先点 All 页签），思考档位与页面不同时在下拉框中选中，系统指令与工具声明与上次写入页面的不同时在面板中写入；各步点击时记录官网界面元素事件并更新页面状态，放不进空闲区间的组不做，页面状态不变
func (runtime *goWAARuntime) settingSteps(body []byte) [][]waa.SettingStep {
	elements := runtime.elements
	if elements == nil {
		return nil
	}
	var fields []json.RawMessage
	if json.Unmarshal(body, &fields) != nil || len(fields) < 2 {
		return nil
	}
	var model string
	_ = json.Unmarshal(fields[0], &model)
	var groups [][]waa.SettingStep
	wait := func() time.Duration {
		if len(groups) == 0 {
			return 0
		}
		return pickedPause
	}
	if model != "" && model != elements.pageModel() && elements.listedModel(model) {
		hold := time.Duration((runtime.rhythm.Press + runtime.rhythm.Hold) * float64(time.Millisecond))
		group := []waa.SettingStep{{Wait: wait(), To: "selector", Clicked: elements.openSelector}}
		if !elements.featuredModel(model) {
			group = append(group, waa.SettingStep{Wait: selectorPause, To: "all", Clicked: elements.openAll})
		}
		groups = append(groups, append(group, waa.SettingStep{Wait: selectorPause, To: "row", Clicked: func(at time.Time) { elements.pickModel(at.Add(-hold), at, model) }}))
	}
	if level := bodyThinkingLevel(fields); level != 0 && level != elements.pageLevel(model) {
		groups = append(groups, []waa.SettingStep{
			{Wait: wait(), To: "level"},
			{Wait: levelPause, To: "option", Clicked: func(at time.Time) { elements.changeLevel(at, level) }},
		})
	}
	if system := rawField(fields, 5); !bytes.Equal(system, runtime.settings.system) {
		if text := systemText(system); text != "" {
			groups = append(groups, []waa.SettingStep{
				{Wait: wait(), To: "system", Clicked: elements.openSystem},
				{Wait: systemPause, To: "instructions", Text: text, Clicked: elements.editSystem},
				{Wait: closePause, To: "close", Clicked: func(time.Time) { runtime.settings.system = system }},
			})
		}
	}
	if tools := rawField(fields, 6); !bytes.Equal(tools, runtime.settings.tools) && bytes.Contains(tools, []byte(`"`)) {
		var group []waa.SettingStep
		pause := wait()
		if !runtime.settings.groundingOff {
			group = append(group, waa.SettingStep{Wait: pause, To: "grounding", Clicked: func(at time.Time) {
				elements.toggle(at, veGrounding)
				runtime.settings.groundingOff = true
			}})
			pause = togglePause
		}
		if !runtime.settings.functions {
			group = append(group, waa.SettingStep{Wait: pause, To: "functions", Clicked: func(at time.Time) {
				elements.toggle(at, veFunctions)
				runtime.settings.functions = true
			}})
			pause = togglePause
		}
		groups = append(groups, append(group,
			waa.SettingStep{Wait: pause, To: "edit", Clicked: func(at time.Time) { elements.toggle(at, veFunctionsEdit) }},
			waa.SettingStep{Wait: dialogPause, To: "declarations", Text: string(tools)},
			waa.SettingStep{Wait: closePause, To: "save", Clicked: func(time.Time) { runtime.settings.tools = tools }},
		))
	}
	return groups
}

// rawField 返回 GenerateContent 正文第 index 个字段，缺少或为 null 时为 nil
func rawField(fields []json.RawMessage, index int) json.RawMessage {
	if index >= len(fields) || string(fields[index]) == "null" {
		return nil
	}
	return fields[index]
}

// bodyThinkingLevel 返回 GenerateContent 正文思考配置中的档位在 ListModels 中的编号，没有档位时为 0
func bodyThinkingLevel(fields []json.RawMessage) int64 {
	var config []json.RawMessage
	var thinking []json.RawMessage
	var level int64
	if json.Unmarshal(rawField(fields, 3), &config) != nil || len(config) <= 16 || json.Unmarshal(config[16], &thinking) != nil || len(thinking) <= 3 || json.Unmarshal(thinking[3], &level) != nil {
		return 0
	}
	if level == 6 {
		return 5
	}
	return level
}

// systemText 返回 GenerateContent 系统指令各文本 Part 以换行连接的文本
func systemText(system json.RawMessage) string {
	var instruction []json.RawMessage
	var parts [][]json.RawMessage
	if json.Unmarshal(system, &instruction) != nil || len(instruction) == 0 || json.Unmarshal(instruction[0], &parts) != nil {
		return ""
	}
	texts := make([]string, 0, len(parts))
	for _, part := range parts {
		var text string
		if len(part) > 1 && json.Unmarshal(part[1], &text) == nil {
			texts = append(texts, text)
		}
	}
	return strings.Join(texts, "\n")
}
