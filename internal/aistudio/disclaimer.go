package aistudio

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"time"
)

// bidiDisclaimer 是 UserPreferences 中一类首次使用确认的字段序号与 field mask 名称
type bidiDisclaimer struct {
	index int
	mask  string
}

// bidiDisclaimers 是官网 Live 与 Robotics 首次运行时确认对话框写入的偏好字段
var bidiDisclaimers = map[BidiMode]bidiDisclaimer{
	BidiModeLive:     {index: 2, mask: "live_api_disclaimer_ack"},
	BidiModeRobotics: {index: 3, mask: "robotics_model_disclaimer_ack"},
}

// EnsureDisclaimerAcknowledged 读取账户的官网用户偏好，账户尚未确认该模式的首次使用提示时与官网对话框的 Acknowledge 一样发送 UpdateUserPreferences
func (c *Client) EnsureDisclaimerAcknowledged(ctx context.Context, accountID string, mode BidiMode) error {
	disclaimer, ok := bidiDisclaimers[mode]
	if !ok {
		return nil
	}
	key := strings.TrimSpace(accountID) + " " + disclaimer.mask
	c.termsMu.Lock()
	_, acknowledged := c.disclaimers[key]
	c.termsMu.Unlock()
	if acknowledged {
		return nil
	}
	raw, err := c.readRPC(ctx, "GetUserPreferences", accountID, []byte("[]"))
	if err != nil {
		return err
	}
	value, err := decodeJSONValue(raw)
	if err != nil {
		return withMethod(err, "GetUserPreferences")
	}
	root, err := rawArray(value, "$", value)
	if err != nil {
		return withMethod(err, "GetUserPreferences")
	}
	if isJSONNull(rawAt(root, disclaimer.index)) {
		body, err := json.Marshal(disclaimerBody(disclaimer, time.Now()))
		if err != nil {
			return err
		}
		if _, err := c.readRPC(ctx, "UpdateUserPreferences", accountID, body); err != nil {
			return err
		}
	}
	c.termsMu.Lock()
	c.disclaimers[key] = struct{}{}
	c.termsMu.Unlock()
	return nil
}

// disclaimerBody 返回官网确认对话框的 UpdateUserPreferences 请求：偏好字段为确认时刻的毫秒精度 Timestamp，field mask 只含该字段
func disclaimerBody(disclaimer bidiDisclaimer, now time.Time) []any {
	preferences := make([]any, disclaimer.index+1)
	preferences[disclaimer.index] = []any{[]any{now.Unix(), int64(now.Nanosecond()) / int64(time.Millisecond) * int64(time.Millisecond)}}
	return []any{preferences, []any{[]any{disclaimer.mask}}}
}

// readRPC 发送一次非流式 MakerSuite RPC 并读出完整响应正文
func (c *Client) readRPC(ctx context.Context, method string, accountID string, body []byte) ([]byte, error) {
	response, err := c.do(ctx, method, accountID, "", body, false)
	if err != nil {
		return nil, err
	}
	defer response.Body.Close()
	raw, err := io.ReadAll(response.Body)
	if err != nil {
		return nil, fmt.Errorf("读取 %s: %w", method, err)
	}
	return raw, nil
}
