package camoufoxnative

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
)

// pageDOMHelpers 定义官网页面共用的可见目标与按钮状态判断
const pageDOMHelpers = `
  const visible = element => element.checkVisibility({visibilityProperty: true});
  const uniqueVisible = (selector, label) => {
    const items = [...document.querySelectorAll(selector)].filter(visible);
    if (items.length > 1) throw new Error(label + ' 匹配多个可见目标');
    return items[0];
  };
  const buttonEnabled = button => !button.matches(':disabled') && !button.closest('[aria-disabled="true"]');
`

// promptReadyExpression 检查登录与生成流程使用的同一个可见输入框
const promptReadyExpression = `(() => {` + pageDOMHelpers + `
  return Boolean(uniqueVisible('ms-prompt-box textarea', '提示词输入框'));
})()`

// workerPageReadyExpression 等待输入框出现或页面跳转到登录入口
const workerPageReadyExpression = `(location.hostname === 'accounts.google.com' || ` + promptReadyExpression + `)`

// loggingContextReadyExpression 检查官网页面已收到 GetLoggingContext 响应，此后页面 RPC 才带 x-goog-ext-519733851-bin
const loggingContextReadyExpression = `performance.getEntriesByType('resource').some(entry => entry.name.endsWith('/GetLoggingContext'))`

// normalizePromptNewlines 把 CRLF/CR 统一成 LF
func normalizePromptNewlines(value string) string {
	return strings.ReplaceAll(strings.ReplaceAll(value, "\r\n", "\n"), "\r", "\n")
}

// fillPromptStatements 向当前可见提示框写入参数 prompt 并通知页面表单，textarea 为该输入框，setValue 为 value 的原生 setter，synced 表示页面值与 prompt 相同
const fillPromptStatements = pageDOMHelpers + `
  const textarea = uniqueVisible('ms-prompt-box textarea', '提示词输入框');
  if (!textarea) throw new Error('提示词输入框不存在');
  const setValue = Object.getOwnPropertyDescriptor(HTMLTextAreaElement.prototype, 'value').set;
  setValue.call(textarea, prompt);
  textarea.dispatchEvent(new InputEvent('input', {bubbles: true, inputType: 'insertText', data: prompt}));
  textarea.dispatchEvent(new Event('change', {bubbles: true}));
  const synced = textarea.value === prompt;
`

// fillPromptFunction 向当前可见提示框写入提示词，返回页面值是否相同
const fillPromptFunction = `prompt => {` + fillPromptStatements + `  return synced;
}`

// proofFunction 写入提示词后以保存的官方 WAA service 为 digest 调用官网 snapshot 函数，之后清空输入框并通知页面表单；页面值与提示词不同时不调用 snapshot，返回 null
const proofFunction = `async (page, prompt, digest) => {
  if (!page.service) throw new Error('官方 WAA service 尚未就绪');` + fillPromptStatements + `  try {
    return synced ? await page.snapshot(digest) : null;
  } finally {
    setValue.call(textarea, '');
    textarea.dispatchEvent(new InputEvent('input', {bubbles: true, inputType: 'deleteContent'}));
  }
}`

// fillPrompt 向官网提示框写入按 LF 规范化的提示词，页面值与之不同时返回错误
func fillPrompt(ctx context.Context, client *bidiClient, contextID, prompt string) error {
	prompt = normalizePromptNewlines(prompt)
	result, err := client.callFunction(ctx, contextID, fillPromptFunction, prompt)
	if err != nil {
		return fmt.Errorf("填写提示词: %w", err)
	}
	if result["value"] != true {
		return fmt.Errorf("填写提示词后页面值不同 prompt=%q", prompt)
	}
	return nil
}

// runStateFunction 是等页面执行完已排队的任务后读取官网 Run 按钮状态的函数声明，页面忙于处理时随之等待；参数为 Worker 页面对象与界面运行的请求 ID，返回 runState 的 JSON
const runStateFunction = `async (page, id) => {` + pageDOMHelpers + `
  await new Promise(resolve => setTimeout(resolve));
  const describe = node => (node?.getAttribute('aria-describedby') || '').split(' ').map(name => document.getElementById(name)?.textContent.trim()).filter(Boolean).join(' ');
  const texts = selector => [...document.querySelectorAll(selector)].filter(visible).map(node => node.innerText.trim()).filter(Boolean);
  const button = uniqueVisible('ms-run-button button', '官网 Run 按钮');
  const box = document.querySelector('ms-prompt-box .prompt-box-container');
  let cover = '';
  if (button) {
    const rect = button.getBoundingClientRect();
    const top = document.elementFromPoint(rect.left + rect.width / 2, rect.top + rect.height / 2);
    if (top && !button.contains(top)) cover = (top.innerText || top.getAttribute('aria-label') || top.tagName).trim();
  }
  return JSON.stringify({
    found: Boolean(button), enabled: Boolean(button && buttonEnabled(button)) && !cover, running: Boolean(button?.querySelector('.spin')),
    started: Boolean(page.requests?.get(id)?.started),
    tooltip: describe(button), boxTooltip: box?.classList.contains('disabled') ? describe(box) : '',
    dialogs: texts('mat-dialog-container'), notices: texts('mat-snack-bar-container'), cover,
    prompt: document.querySelector('ms-prompt-box textarea')?.value.length ?? -1
  });
}`

// runState 是页面空闲时官网 Run 按钮与页面提示的状态
type runState struct {
	// Found 与 Enabled 表示 Run 按钮可见与可点，按钮中心被其他元素遮挡时不可点
	Found   bool `json:"found"`
	Enabled bool `json:"enabled"`
	// Running 表示 Run 按钮已切换为 Stop
	Running bool `json:"running"`
	// Started 表示页面应用已为本次请求调用高层 proof 函数
	Started bool `json:"started"`
	// Tooltip 与 BoxTooltip 是 Run 按钮与停用的输入框的悬停提示
	Tooltip    string   `json:"tooltip"`
	BoxTooltip string   `json:"boxTooltip"`
	Dialogs    []string `json:"dialogs"`
	Notices    []string `json:"notices"`
	// Cover 是遮挡 Run 按钮中心的元素文字
	Cover string `json:"cover"`
	// Prompt 是输入框文本的 UTF-16 长度，没有输入框时为 -1
	Prompt int `json:"prompt"`
}

// readRunState 读取页面空闲时官网 Run 按钮的状态，id 为界面运行的请求 ID
func readRunState(ctx context.Context, client *bidiClient, contextID string, page pageHandle, id string) (runState, error) {
	var state runState
	value, err := client.callFunctionString(ctx, contextID, runStateFunction, page, id)
	if err != nil {
		return state, fmt.Errorf("读取官网 Run 按钮状态: %w", err)
	}
	if err := json.Unmarshal([]byte(value), &state); err != nil {
		return state, fmt.Errorf("解析官网 Run 按钮状态: %w", err)
	}
	return state, nil
}

// reason 返回官网页面给出的 Run 按钮不可点或点击后没有运行的原因
func (state runState) reason() string {
	var parts []string
	if !state.Found {
		parts = append(parts, "Run 按钮不存在")
	}
	for _, dialog := range state.Dialogs {
		parts = append(parts, fmt.Sprintf("对话框 %q", dialog))
	}
	if state.Cover != "" {
		parts = append(parts, fmt.Sprintf("遮挡 Run 按钮的元素 %q", state.Cover))
	}
	if state.BoxTooltip != "" {
		parts = append(parts, fmt.Sprintf("输入框提示 %q", state.BoxTooltip))
	}
	if state.Tooltip != "" {
		parts = append(parts, fmt.Sprintf("Run 按钮提示 %q", state.Tooltip))
	}
	for _, notice := range state.Notices {
		parts = append(parts, fmt.Sprintf("提示条 %q", notice))
	}
	parts = append(parts, fmt.Sprintf("输入框 %d 字", state.Prompt))
	return strings.Join(parts, " | ")
}

// runSettleChecks 是页面空闲时 Run 按钮持续不可点、点击后仍没有开始运行时判定失败所需的连续检查次数，页面空闲时约 3 秒
const runSettleChecks = 40

// generationRunningExpression 检查官网 Run 按钮已切换为 Stop（按钮内出现 .spin）
const generationRunningExpression = `(() => {` + pageDOMHelpers + `
  const button = uniqueVisible('ms-run-button button', '官网 Run 按钮');
  return Boolean(button && button.querySelector('.spin'));
})()`

// promptFocusedExpression 检查输入框已获得焦点，官网新开对话后焦点在输入框中
const promptFocusedExpression = `Boolean(document.activeElement && document.activeElement.matches('` + promptBoxSelector + `'))`

// chatEmptyExpression 检查页面没有对话轮次
const chatEmptyExpression = `document.querySelectorAll('ms-chat-turn').length === 0`

// temporaryChatExpression 检查页面显示临时对话标识
const temporaryChatExpression = `[...document.querySelectorAll('ms-incognito-mode-indicator')].some(node => node.checkVisibility({visibilityProperty: true}))`

const (
	// promptBoxSelector 是提示词输入框
	promptBoxSelector = "ms-prompt-box textarea"
	// runButtonSelector 是生成前显示 Run、生成中显示 Stop 的按钮
	runButtonSelector = "ms-run-button button"
	// newChatButtonSelector 是顶栏的 New chat 按钮
	newChatButtonSelector = `ms-playground-toolbar button[aria-label="New chat"]`
	// discardButtonPattern 是 New chat 确认弹窗中丢弃当前对话的按钮文字
	discardButtonPattern = "^Discard and continue$"
	// lastTurnSelector 是最后一个对话轮次，指针悬停其上时显示 Rerun 按钮
	lastTurnSelector = "ms-chat-turn:last-of-type"
	// rerunButtonSelector 是最后一个对话轮次的 Rerun 按钮
	rerunButtonSelector = `ms-chat-turn:last-of-type button[aria-label^="Rerun" i]`
)

// pointerTargetFunction 把 selector 匹配且文字或 aria-label 匹配 pattern 的第一个可见元素滚动到视口内，返回其中心坐标 JSON，没有时返回空字符串
const pointerTargetFunction = `(selector, pattern) => {
  const expression = new RegExp(pattern, 'i');
  const item = [...document.querySelectorAll(selector)].find(node => node.checkVisibility({visibilityProperty: true}) && expression.test((node.innerText || node.getAttribute('aria-label') || '').trim()));
  if (!item) return '';
  item.scrollIntoView({block: 'nearest', inline: 'nearest'});
  const box = item.getBoundingClientRect();
  return JSON.stringify({x: Math.round(box.left + box.width / 2), y: Math.round(box.top + box.height / 2)});
}`

// pointerMoveDuration 是可信指针从别处移到目标元素所用的毫秒数
const pointerMoveDuration = 120

// pointer 以可信指针用 move 毫秒移到 selector 与 pattern 匹配的可见元素中心，click 为真时再按下、停顿并抬起，返回是否找到该元素
func (client *bidiClient) pointer(ctx context.Context, contextID, selector, pattern string, click bool, move int) (bool, error) {
	located, err := client.callFunctionString(ctx, contextID, pointerTargetFunction, selector, pattern)
	if err != nil || located == "" {
		return false, err
	}
	var target struct {
		X int `json:"x"`
		Y int `json:"y"`
	}
	if err := json.Unmarshal([]byte(located), &target); err != nil {
		return false, err
	}
	actions := []map[string]any{{"type": "pointerMove", "x": target.X, "y": target.Y, "duration": move}}
	if click {
		actions = append(actions, map[string]any{"type": "pointerDown", "button": 0}, map[string]any{"type": "pause", "duration": 60}, map[string]any{"type": "pointerUp", "button": 0})
	}
	_, err = client.command(ctx, "input.performActions", map[string]any{
		"context": contextID,
		"actions": []map[string]any{{"type": "pointer", "id": "mouse", "parameters": map[string]any{"pointerType": "mouse"}, "actions": actions}},
	})
	return true, err
}

// dismissOverlaysExpression 关闭官网已知且可交互的启动弹层
const dismissOverlaysExpression = `(() => {` + pageDOMHelpers + `
  const selectors = [
    'ms-g1-welcome-dialog button[aria-label="Close dialog"]',
    'button[aria-label="Close guided tour"]',
    '#glue-cookie-notification-bar-1 .glue-cookie-notification-bar__reject'
  ];
  let clicked = 0;
  for (const selector of selectors) {
    const button = uniqueVisible(selector, '启动弹层按钮');
    if (button && buttonEnabled(button)) {
      button.click();
      clicked++;
    }
  }
  return clicked;
})()`

// dismissKnownOverlays 关闭官网已知启动弹层，返回点击的按钮数
func dismissKnownOverlays(ctx context.Context, client *bidiClient, contextID string) (int, error) {
	clicked, err := client.evaluate(ctx, contextID, dismissOverlaysExpression)
	if err != nil {
		return 0, fmt.Errorf("处理 AI Studio 启动覆盖层: %w", err)
	}
	count, _ := number(clicked["value"])
	return int(count), nil
}
