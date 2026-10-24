package camoufoxnative

import (
	"context"
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

// normalizePromptNewlines 把 CRLF/CR 统一成 LF，官网 textarea 会把写入值规范化为 LF
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

// proofFunction 写入提示词后以保存的官方 WAA service 为 digest 调用官网 snapshot 函数，之后清空输入框；页面值与提示词不同时不调用 snapshot，返回 null
const proofFunction = `async (page, prompt, digest) => {
  if (!page.service) throw new Error('官方 WAA service 尚未就绪');` + fillPromptStatements + `  try {
    return synced ? await page.snapshot(digest) : null;
  } finally {
    setValue.call(textarea, '');
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

// submitPromptExpression 点击官网当前可见且启用的提交按钮
const submitPromptExpression = `(() => {` + pageDOMHelpers + `
  const button = uniqueVisible('ms-run-button button', '官网 Run 按钮');
  if (!button) throw new Error('官网 Run 按钮不存在');
  if (!buttonEnabled(button)) throw new Error('官网 Run 按钮已禁用');
  button.click();
  return true;
})()`

// stopGenerationExpression 点击官网运行中的 Stop 按钮，页面按用户停止的流程中止自己的 GenerateContent
const stopGenerationExpression = `(() => {` + pageDOMHelpers + `
  const button = uniqueVisible('ms-run-button button', '官网 Stop 按钮');
  if (!button) throw new Error('官网 Stop 按钮不存在');
  if (!buttonEnabled(button)) throw new Error('官网 Stop 按钮已禁用');
  button.click();
  return true;
})()`

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
