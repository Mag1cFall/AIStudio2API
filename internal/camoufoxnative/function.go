package camoufoxnative

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
)

const (
	// functionResultInputSelector 是最后一轮函数调用块中的函数响应输入框
	functionResultInputSelector = "ms-chat-turn:last-of-type ms-function-call-chunk input"
	// functionResultSubmitSelector 是最后一轮函数调用块中提交函数响应的按钮
	functionResultSubmitSelector = "ms-chat-turn:last-of-type ms-function-call-chunk button.ms-button-primary"
)

// FunctionCallMarker 是 GenerateContent 响应流中函数调用 Part 的开头：十个空字段后为带函数名的 functionCall
var FunctionCallMarker = []byte(`[null,null,null,null,null,null,null,null,null,null,["`)

// functionTab 返回最后一轮是 contents 所回应的函数调用、输入框中没有未发出输入的空闲标签页，即 contents 在其上一轮的对话后追加了 model 与 user 两轮，没有时为 nil；调用方持有 mu
func (worker *Worker) functionTab(contents []json.RawMessage) *chatTab {
	for _, tab := range worker.chats {
		if !tab.busy && !tab.unsent && tab.contents != nil && nextChatAction(tab.contents, contents) == chatContinue {
			return tab
		}
	}
	return nil
}

// ContinuesFunctionCall 判断函数结果正文 body 是否回应某个空闲标签页最后一轮的函数调用，即官网用户能在该标签页的函数调用块中提交函数结果
func (worker *Worker) ContinuesFunctionCall(body []byte) bool {
	contents, err := generateContents(body)
	if err != nil {
		return false
	}
	worker.mu.Lock()
	defer worker.mu.Unlock()
	return worker.functionTab(contents) != nil
}

// acquireFunctionChat 取得最后一轮是 contents 所回应的函数调用的空闲标签页并标为忙碌
func (worker *Worker) acquireFunctionChat(contents []json.RawMessage) (*chatTab, error) {
	worker.mu.Lock()
	defer worker.mu.Unlock()
	tab := worker.functionTab(contents)
	if tab == nil {
		return nil, errors.New("没有最后一轮是本请求回应的函数调用的标签页")
	}
	tab.busy = true
	worker.useTab(tab.contextID)
	return tab, nil
}

// submitResult 像官网用户一样在最后一轮的函数调用块中以可信指针点击函数响应输入框、以 Ctrl+V 粘贴 turn 的函数结果，随后安装一次性钩子并点击提交
func (worker *Worker) submitResult(ctx, input context.Context, tab *chatTab, turn GenerateTurn, requestID string) error {
	if err := worker.click(input, tab.contextID, functionResultInputSelector, "", "官网函数响应输入框", pointerMoveDuration); err != nil {
		return err
	}
	worker.mu.Lock()
	tab.unsent = true
	worker.mu.Unlock()
	if err := pasteText(input, worker.client, tab.contextID, turn.Result, false); err != nil {
		return fmt.Errorf("粘贴函数结果: %w", err)
	}
	if err := worker.arm(ctx, tab, turn, requestID); err != nil {
		return err
	}
	return worker.click(input, tab.contextID, functionResultSubmitSelector, "", "官网函数响应提交按钮", pointerMoveDuration)
}
