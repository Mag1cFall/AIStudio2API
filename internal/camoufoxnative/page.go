package camoufoxnative

import (
	"context"
	"encoding/json"
	"fmt"
)

// pageDOMHelpers defines shared visibility and button state checks for official web pages.
const pageDOMHelpers = `
  const visible = element => element.checkVisibility({visibilityProperty: true});
  const uniqueVisible = (selector, label) => {
    const items = [...document.querySelectorAll(selector)].filter(visible);
    if (items.length > 1) throw new Error(label + ' matched multiple visible elements');
    return items[0];
  };
  const buttonEnabled = button => !button.matches(':disabled') && !button.closest('[aria-disabled="true"]');
`

// promptReadyExpression checks the visible input box used across login and generation flows.
const promptReadyExpression = `(() => {` + pageDOMHelpers + `
  return Boolean(uniqueVisible('ms-prompt-box textarea', 'prompt textarea'));
})()`

// workerPageReadyExpression waits for the prompt textarea to appear or page redirects to login.
const workerPageReadyExpression = `(location.hostname === 'accounts.google.com' || ` + promptReadyExpression + `)`

// fillPromptExpression writes text to the currently visible prompt box and dispatches input events.
func fillPromptExpression(prompt string) string {
	encoded, _ := json.Marshal(prompt)
	return fmt.Sprintf(`(() => {`+pageDOMHelpers+`
  const textarea = uniqueVisible('ms-prompt-box textarea', 'prompt textarea');
  if (!textarea) throw new Error('prompt textarea does not exist');
  const setter = Object.getOwnPropertyDescriptor(HTMLTextAreaElement.prototype, 'value').set;
  setter.call(textarea, %s);
  textarea.dispatchEvent(new InputEvent('input', {bubbles: true, inputType: 'insertText', data: %s}));
  textarea.dispatchEvent(new Event('change', {bubbles: true}));
  return textarea.value;
})()`, encoded, encoded)
}

// submitPromptExpression clicks the currently visible and enabled run button.
const submitPromptExpression = `(() => {` + pageDOMHelpers + `
  const button = uniqueVisible('ms-run-button button', 'official Run button');
  if (!button) throw new Error('official Run button does not exist');
  if (!buttonEnabled(button)) throw new Error('official Run button is disabled');
  button.click();
  return true;
})()`

// dismissOverlaysExpression dismisses known and interactive startup overlays.
const dismissOverlaysExpression = `(() => {` + pageDOMHelpers + `
  const selectors = [
    'ms-g1-welcome-dialog button[aria-label="Close dialog"]',
    'button[aria-label="Close guided tour"]',
    '#glue-cookie-notification-bar-1 .glue-cookie-notification-bar__reject'
  ];
  let clicked = 0;
  for (const selector of selectors) {
    const button = uniqueVisible(selector, 'startup overlay button');
    if (button && buttonEnabled(button)) {
      button.click();
      clicked++;
    }
  }
  return clicked;
})()`

// dismissKnownOverlays dismisses known startup overlays.
func dismissKnownOverlays(ctx context.Context, client *bidiClient, contextID string) error {
	if _, err := client.evaluate(ctx, contextID, dismissOverlaysExpression); err != nil {
		return fmt.Errorf("handling AI Studio startup overlays: %w", err)
	}
	return nil
}
