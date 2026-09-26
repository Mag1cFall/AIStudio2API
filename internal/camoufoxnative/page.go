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

// normalizePromptNewlines 把 CRLF/CR 统一成 LF，官网 textarea 会把写入值规范化为 LF
func normalizePromptNewlines(value string) string {
	return strings.ReplaceAll(strings.ReplaceAll(value, "\r\n", "\n"), "\r", "\n")
}

// fillPromptExpression 向当前可见提示框写入文本并通知页面表单
func fillPromptExpression(prompt string) string {
	encoded, _ := json.Marshal(prompt)
	return fmt.Sprintf(`(() => {`+pageDOMHelpers+`
  const textarea = uniqueVisible('ms-prompt-box textarea', '提示词输入框');
  if (!textarea) throw new Error('提示词输入框不存在');
  const setter = Object.getOwnPropertyDescriptor(HTMLTextAreaElement.prototype, 'value').set;
  setter.call(textarea, %s);
  textarea.dispatchEvent(new InputEvent('input', {bubbles: true, inputType: 'insertText', data: %s}));
  textarea.dispatchEvent(new Event('change', {bubbles: true}));
  return textarea.value;
})()`, encoded, encoded)
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
