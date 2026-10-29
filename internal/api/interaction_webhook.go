package api

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"time"
)

// interactionWebhookClient 发送 webhook 事件，接收端需在数秒内应答
var interactionWebhookClient = &http.Client{Timeout: 10 * time.Second}

// interactionWebhookWindow 是 webhook 事件投递失败后继续重试的时长
const interactionWebhookWindow = 24 * time.Hour

// interactionWebhookConfig 是请求级 webhook 的地址与随事件返回的用户元数据
type interactionWebhookConfig struct {
	URIs         []string        `json:"uris"`
	UserMetadata json.RawMessage `json:"user_metadata"`
}

// interactionWebhookEvent 是按官方字段顺序编码的 webhook 事件信封
type interactionWebhookEvent struct {
	Type         string                 `json:"type"`
	Version      string                 `json:"version"`
	Timestamp    string                 `json:"timestamp"`
	Data         interactionWebhookData `json:"data"`
	UserMetadata json.RawMessage        `json:"user_metadata,omitempty"`
}

// interactionWebhookData 是事件指向的交互 ID 与失败时的错误
type interactionWebhookData struct {
	ID           string `json:"id"`
	ErrorCode    string `json:"error_code,omitempty"`
	ErrorMessage string `json:"error_message,omitempty"`
}

// interactionWebhookEvents 是交互终态对应的 webhook 事件类型
var interactionWebhookEvents = map[string]string{
	"completed": "interaction.completed", "incomplete": "interaction.completed", "requires_action": "interaction.requires_action",
	"failed": "interaction.failed", "cancelled": "interaction.cancelled",
}

// validate 要求请求为后台交互且地址为 http 或 https URL
func (config interactionWebhookConfig) validate(background bool) error {
	if !background {
		return fmt.Errorf("webhook_config requires background to be true")
	}
	for _, uri := range config.URIs {
		if parsed, err := url.Parse(uri); err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Host == "" {
			return fmt.Errorf("webhook_config.uris must be http or https URLs: %q", uri)
		}
	}
	return nil
}

// sendInteractionWebhook 向每个地址发送交互终态事件，failure 为后台请求写出的 Gemini 错误
func sendInteractionWebhook(config interactionWebhookConfig, id, status string, failure []byte) {
	event := interactionWebhookEvent{Type: interactionWebhookEvents[status], Version: "v1", Timestamp: time.Now().UTC().Format(time.RFC3339), Data: interactionWebhookData{ID: id}}
	var written struct {
		Error struct {
			Message string `json:"message"`
			Status  string `json:"status"`
		} `json:"error"`
	}
	if status == "failed" && json.Unmarshal(failure, &written) == nil {
		event.Data.ErrorCode, event.Data.ErrorMessage = written.Error.Status, written.Error.Message
	}
	if rawJSONConfigured(config.UserMetadata) {
		event.UserMetadata = config.UserMetadata
	}
	body, _ := json.Marshal(event)
	messageID := newID("msg")
	for _, uri := range config.URIs {
		go deliverInteractionWebhook(uri, messageID, body)
	}
}

// deliverInteractionWebhook 以 webhook-id 与 webhook-timestamp 请求头 POST 事件，非 2xx 时指数退避重试，直到成功或超过重试时长
func deliverInteractionWebhook(uri, messageID string, body []byte) {
	deadline := time.Now().Add(interactionWebhookWindow)
	for delay := time.Second; ; delay = min(delay*2, time.Hour) {
		request, _ := http.NewRequest(http.MethodPost, uri, bytes.NewReader(body))
		request.Header.Set("Content-Type", "application/json")
		request.Header.Set("webhook-id", messageID)
		request.Header.Set("webhook-timestamp", strconv.FormatInt(time.Now().Unix(), 10))
		if response, err := interactionWebhookClient.Do(request); err == nil {
			response.Body.Close()
			if response.StatusCode/100 == 2 {
				return
			}
		}
		if time.Now().Add(delay).After(deadline) {
			return
		}
		time.Sleep(delay)
	}
}
