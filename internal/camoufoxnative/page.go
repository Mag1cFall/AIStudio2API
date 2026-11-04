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

// runButtonEnabledExpression 检查官网 Run 按钮是否可见且已启用
const runButtonEnabledExpression = `(() => {` + pageDOMHelpers + `
  const button = uniqueVisible('ms-run-button button', '官网 Run 按钮');
  return Boolean(button && buttonEnabled(button));
})()`

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

// pointer 以可信指针移到 selector 与 pattern 匹配的可见元素中心，click 为真时再按下、停顿并抬起，返回是否找到该元素
func (client *bidiClient) pointer(ctx context.Context, contextID, selector, pattern string, click bool) (bool, error) {
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
	actions := []map[string]any{{"type": "pointerMove", "x": target.X, "y": target.Y, "duration": 120}}
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

// dismissKnownOverlays 关闭官网已知启动弹层
func dismissKnownOverlays(ctx context.Context, client *bidiClient, contextID string) error {
	if _, err := client.evaluate(ctx, contextID, dismissOverlaysExpression); err != nil {
		return fmt.Errorf("处理 AI Studio 启动覆盖层: %w", err)
	}
	return nil
}
