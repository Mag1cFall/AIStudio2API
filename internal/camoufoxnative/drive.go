package camoufoxnative

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"net/http"
	"strings"
	"time"
)

// accessTokenPath 为官网读取 Drive 授权 token 的 RPC 路径
const accessTokenPath = "/$rpc/google.internal.alkali.applications.makersuite.v1.MakerSuiteService/GenerateAccessToken"

// driveConsentTimeout 为一次 Drive 授权流程的最长耗时
const driveConsentTimeout = 3 * time.Minute

// driveAccessButtonExpression 返回 History 页未被遮挡的 Allow Drive access 按钮
const driveAccessButtonExpression = `[...document.querySelectorAll('button[aria-label="Allow Drive access"]')].find(button => {
  const box = button.getBoundingClientRect();
  return button.contains(document.elementFromPoint(box.left + box.width / 2, box.top + box.height / 2));
}) || null`

// driveApproveExpression 返回 OAuth 同意页的 Allow 按钮
const driveApproveExpression = `document.querySelector('#submit_approve_access')`

// authorizeDrive 在官网确认账户已授权 Google Drive，未授权时完成官网 OAuth 同意
func (session *loginSession) authorizeDrive(ctx context.Context, email string) error {
	ctx, cancel := context.WithTimeout(ctx, driveConsentTimeout)
	defer cancel()
	status, err := session.waitAccessTokenStatus(ctx)
	if err != nil {
		return err
	}
	if status == http.StatusOK {
		return nil
	}
	if status != http.StatusUnauthorized {
		return fmt.Errorf("GenerateAccessToken 返回 HTTP %d", status)
	}
	if err := session.navigate(ctx, aiStudioOrigin+"/library"); err != nil {
		return err
	}
	known, err := session.topLevelContexts(ctx)
	if err != nil {
		return err
	}
	popup := ""
	for popup == "" {
		if err := session.clickWhenPresent(ctx, session.contextID, driveAccessButtonExpression); err != nil {
			return fmt.Errorf("打开 Drive 授权: %w", err)
		}
		attemptCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
		popup, err = session.waitPopup(attemptCtx, known)
		cancel()
		if err != nil && ctx.Err() != nil {
			return err
		}
	}
	if err := session.completeConsent(ctx, popup, email); err != nil {
		return err
	}
	session.client.accessTokenStatus = 0
	if err := session.navigate(ctx, aiStudioOrigin+"/prompts/new_chat"); err != nil {
		return err
	}
	status, err = session.waitAccessTokenStatus(ctx)
	if err != nil {
		return err
	}
	if status != http.StatusOK {
		return fmt.Errorf("Drive 授权后 GenerateAccessToken 返回 HTTP %d", status)
	}
	return nil
}

// waitAccessTokenStatus 等待官网页面完成一次 GenerateAccessToken
func (session *loginSession) waitAccessTokenStatus(ctx context.Context) (int, error) {
	for session.client.accessTokenStatus == 0 {
		if _, err := session.client.evaluate(ctx, session.contextID, "0"); err != nil && !retryablePageEvaluation(err) {
			return 0, fmt.Errorf("等待 GenerateAccessToken: %w", err)
		}
		if err := waitContext(ctx, 200*time.Millisecond); err != nil {
			return 0, fmt.Errorf("等待 GenerateAccessToken: %w", err)
		}
	}
	return session.client.accessTokenStatus, nil
}

// navigate 在登录 tab 打开官网页面
func (session *loginSession) navigate(ctx context.Context, pageURL string) error {
	if _, err := session.client.command(ctx, "browsingContext.navigate", map[string]any{
		"context": session.contextID,
		"url":     pageURL,
		"wait":    "interactive",
	}); err != nil && !strings.Contains(err.Error(), "NS_ERROR_ABORT") {
		return fmt.Errorf("导航 %s: %w", pageURL, err)
	}
	return nil
}

// topLevelContexts 返回当前全部顶层 tab 与其地址
func (session *loginSession) topLevelContexts(ctx context.Context) (map[string]string, error) {
	tree, err := session.client.command(ctx, "browsingContext.getTree", map[string]any{"maxDepth": 0})
	if err != nil {
		return nil, fmt.Errorf("读取浏览器 tab: %w", err)
	}
	contexts := make(map[string]string)
	items, _ := tree["contexts"].([]any)
	for _, item := range items {
		value, _ := item.(map[string]any)
		id, _ := value["context"].(string)
		address, _ := value["url"].(string)
		if id != "" {
			contexts[id] = address
		}
	}
	return contexts, nil
}

// waitPopup 等待官网打开的 OAuth 弹窗
func (session *loginSession) waitPopup(ctx context.Context, known map[string]string) (string, error) {
	for {
		contexts, err := session.topLevelContexts(ctx)
		if err != nil {
			return "", err
		}
		for id := range contexts {
			if _, ok := known[id]; !ok {
				return id, nil
			}
		}
		if err := waitContext(ctx, 200*time.Millisecond); err != nil {
			return "", fmt.Errorf("等待 Drive 授权弹窗: %w", err)
		}
	}
}

// completeConsent 在 OAuth 弹窗中选择账户并同意授权，直到弹窗关闭
func (session *loginSession) completeConsent(ctx context.Context, popup string, email string) error {
	accountExpression := consentAccountExpression(email)
	lastURL := ""
	for {
		contexts, err := session.topLevelContexts(ctx)
		if err != nil {
			return err
		}
		pageURL, open := contexts[popup]
		if !open {
			return nil
		}
		lastURL = pageURL
		step, err := session.client.evaluateString(ctx, popup, fmt.Sprintf(
			`(() => (%s) ? 'approve' : (%s) ? 'account' : '')()`, driveApproveExpression, accountExpression,
		))
		switch {
		case err != nil && !retryablePageEvaluation(err):
			return fmt.Errorf("读取 Drive 授权弹窗: %w", err)
		case step == "approve":
			err = session.click(ctx, popup, driveApproveExpression)
		case step == "account":
			err = session.click(ctx, popup, accountExpression)
		}
		if err != nil && !retryablePageEvaluation(err) && !errors.Is(err, errElementMissing) {
			return fmt.Errorf("操作 Drive 授权弹窗: %w", err)
		}
		if err := waitContext(ctx, 500*time.Millisecond); err != nil {
			return fmt.Errorf("Drive 授权未完成，弹窗停在 %s: %w", lastURL, err)
		}
	}
}

// consentAccountExpression 返回账户选择页中目标账户的条目
func consentAccountExpression(email string) string {
	encoded, _ := json.Marshal(strings.ToLower(strings.TrimSpace(email)))
	return fmt.Sprintf(`((email) => {
  const items = [...document.querySelectorAll('[data-email]')];
  return items.find(item => (item.getAttribute('data-email') || '').toLowerCase() === email) || (items.length === 1 ? items[0] : null);
})(%s)`, encoded)
}

// errElementMissing 表示点击目标暂未出现在页面
var errElementMissing = errors.New("页面元素不存在")

// clickWhenPresent 等待元素出现后点击
func (session *loginSession) clickWhenPresent(ctx context.Context, contextID string, expression string) error {
	for {
		err := session.click(ctx, contextID, expression)
		if err == nil {
			return nil
		}
		if !errors.Is(err, errElementMissing) && !retryablePageEvaluation(err) {
			return err
		}
		if err := waitContext(ctx, 300*time.Millisecond); err != nil {
			return err
		}
	}
}

// click 以真实指针事件点击表达式返回的元素中心
func (session *loginSession) click(ctx context.Context, contextID string, expression string) error {
	encoded, err := session.client.evaluateString(ctx, contextID, fmt.Sprintf(`(() => {
  const element = %s;
  if (!element) return '';
  element.scrollIntoView({block: 'center', inline: 'center'});
  const box = element.getBoundingClientRect();
  return JSON.stringify([box.left + box.width / 2, box.top + box.height / 2]);
})()`, expression))
	if err != nil {
		return err
	}
	if encoded == "" {
		return errElementMissing
	}
	var point [2]float64
	if err := json.Unmarshal([]byte(encoded), &point); err != nil {
		return fmt.Errorf("解析元素位置: %w", err)
	}
	x, y := int(math.Round(point[0])), int(math.Round(point[1]))
	_, err = session.client.command(ctx, "input.performActions", map[string]any{
		"context": contextID,
		"actions": []map[string]any{{
			"type":       "pointer",
			"id":         "mouse",
			"parameters": map[string]any{"pointerType": "mouse"},
			"actions": []map[string]any{
				{"type": "pointerMove", "x": x, "y": y},
				{"type": "pointerDown", "button": 0},
				{"type": "pointerUp", "button": 0},
			},
		}},
	})
	return err
}
