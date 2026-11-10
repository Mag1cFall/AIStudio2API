package camoufoxnative

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"time"
)

// GenerateTurn 是在页面界面运行的一轮 GenerateContent：Body 第 ProofStart 到 ProofEnd 字节是 proof 字段，发送时换成页面应用为 Digest 取得的 proof；Digest 为 binding 的 SHA-256 十六进制
type GenerateTurn struct {
	Body       []byte
	ProofStart int
	ProofEnd   int
	Digest     string
}

// chatTab 是运行界面对话的标签页
type chatTab struct {
	contextID string
	page      pageHandle
	// busy 表示标签页正在运行一轮对话
	busy bool
	// contents 是标签页上一轮发出的 GenerateContent 的 contents，页面没有对话或上一轮以上游错误结束时为空
	contents []json.RawMessage
	// resetting 在一轮对话结束后于后台新开对话期间非空，新开结束时关闭
	resetting chan struct{}
	// runHovered 表示后台新开对话后指针停在 Run 按钮上
	runHovered bool
}

// chatAction 是一轮界面对话在标签页上的操作
type chatAction int

const (
	// chatContinue 在页面当前对话后输入提示词并点击 Run
	chatContinue chatAction = iota
	// chatNew 先点 New chat 并丢弃当前对话，再输入提示词并点击 Run
	chatNew
	// chatRerun 点击最后一轮的 Rerun
	chatRerun
)

// generateContents 返回 GenerateContent 正文的 contents
func generateContents(body []byte) ([]json.RawMessage, error) {
	var fields []json.RawMessage
	if err := json.Unmarshal(body, &fields); err != nil || len(fields) < 2 {
		return nil, fmt.Errorf("GenerateContent 正文缺少 contents: %v", err)
	}
	var contents []json.RawMessage
	if err := json.Unmarshal(fields[1], &contents); err != nil {
		return nil, fmt.Errorf("解析 GenerateContent contents: %w", err)
	}
	return contents, nil
}

// contentRole 返回 wire 轮次 [parts, role] 的角色
func contentRole(content json.RawMessage) string {
	var fields []json.RawMessage
	var role string
	if json.Unmarshal(content, &fields) == nil && len(fields) > 1 {
		_ = json.Unmarshal(fields[1], &role)
	}
	return role
}

// contentText 返回 wire 轮次各 Part 的非空文本以空格连接的结果
func contentText(content json.RawMessage) string {
	var fields []json.RawMessage
	var parts [][]json.RawMessage
	if json.Unmarshal(content, &fields) != nil || len(fields) == 0 || json.Unmarshal(fields[0], &parts) != nil {
		return ""
	}
	values := make([]string, 0, len(parts))
	for _, part := range parts {
		var text string
		if len(part) > 1 && json.Unmarshal(part[1], &text) == nil && text != "" {
			values = append(values, text)
		}
	}
	return strings.Join(values, " ")
}

// GeneratePrompt 返回 GenerateContent 正文最后一个轮次的文本，即官网用户在输入框中输入的提示词；最后一个轮次不是带文本的 user 轮次时返回 false
func GeneratePrompt(body []byte) (string, bool) {
	contents, err := generateContents(body)
	if err != nil || len(contents) == 0 || contentRole(contents[len(contents)-1]) != "user" {
		return "", false
	}
	text := contentText(contents[len(contents)-1])
	return text, text != ""
}

// nextChatAction 按标签页上一轮的 contents 判断本轮的操作：contents 相同时 Rerun，在上一轮后追加 model 与 user 两轮时继续当前对话，上一轮 contents 为空时在当前对话后直接输入，其余情况先开新对话
func nextChatAction(previous, contents []json.RawMessage) chatAction {
	if previous == nil {
		return chatContinue
	}
	equal := func(left, right []json.RawMessage) bool {
		if len(left) != len(right) {
			return false
		}
		for index := range left {
			if !bytes.Equal(left[index], right[index]) {
				return false
			}
		}
		return true
	}
	if equal(previous, contents) {
		return chatRerun
	}
	if len(contents) == len(previous)+2 && equal(previous, contents[:len(previous)]) && contentRole(contents[len(previous)]) == "model" {
		return chatContinue
	}
	return chatNew
}

// generateArmFunction 是在页面上为下一次 Run 安装一次性钩子的函数声明：官网高层 proof 函数下一次被调用时记下请求已开始并以本请求的 digest 取 proof，proof 返回后下一个 GenerateContent XHR 发送时把正文换成 head + proof + tail，并在该 XHR 上监听响应，经通道先发出 {sent: true}，再依次发出响应头、正文块与结束标记；参数依次为页面对象、请求 ID、digest、head、tail 与通道
const generateArmFunction = `(page, id, digest, head, tail, emit) => {
  const makerSuite = window.default_MakerSuite;
  const open = XMLHttpRequest.prototype.open;
  const send = XMLHttpRequest.prototype.send;
  const request = {xhr: null, credit: 8, resume: null};
  (page.requests ||= new Map()).set(id, request);
  let loaded = false, failure = "", wake = null;
  const changed = () => {
    wake?.();
    wake = null;
  };
  const next = () => new Promise(resolve => wake = resolve);
  makerSuite[page.key] = function(service) {
    makerSuite[page.key] = page.original;
    request.started = true;
    return page.original.call(this, service, digest).then(proof => {
      XMLHttpRequest.prototype.open = function(method, url) {
        if (String(url).endsWith("` + generateContentPath + `")) {
          XMLHttpRequest.prototype.open = open;
          const xhr = this;
          request.xhr = xhr;
          xhr.send = function() {
            delete xhr.send;
            xhr.addEventListener("readystatechange", changed);
            xhr.addEventListener("progress", changed);
            xhr.addEventListener("load", () => {
              loaded = true;
              changed();
            });
            xhr.addEventListener("error", () => {
              failure = "NetworkError when attempting to fetch resource.";
              changed();
            });
            xhr.addEventListener("abort", () => {
              failure = "AbortError: The operation was aborted.";
              changed();
              request.resume?.();
            });
            emit(JSON.stringify({sent: true}));
            return send.call(xhr, head + JSON.stringify(proof) + tail);
          };
        }
        return open.apply(this, arguments);
      };
      return proof;
    });
  };
  request.release = () => {
    if (request.xhr) return;
    if (makerSuite[page.key] !== page.original) makerSuite[page.key] = page.original;
    if (XMLHttpRequest.prototype.open !== open) XMLHttpRequest.prototype.open = open;
    failure = "AbortError: The operation was aborted.";
    changed();
  };
` + protectedPump + `}`

// Generate 像官网用户一样在标签页界面运行一轮 GenerateContent：按标签页上一轮的对话决定继续、Rerun 或先开新对话，输入框没有焦点时以可信指针点击输入框，写入最后一个 user 轮次文本的开头后点击 Run，页面应用自己取 proof、构造请求头并发出请求，发送时正文换成 turn 的正文；一轮成功完成后在后台新开对话，上游返回错误时下一轮在当前对话中继续；全部标签页都在运行对话时在新标签页中载入官网页面
func (worker *Worker) Generate(ctx context.Context, turn GenerateTurn) (*ProtectedResponse, error) {
	contents, err := generateContents(turn.Body)
	if err != nil {
		return nil, err
	}
	prompt, ok := GeneratePrompt(turn.Body)
	if !ok {
		return nil, errors.New("GenerateContent 最后一个轮次不是带文本的 user 轮次")
	}
	tab, err := worker.acquireChat(ctx)
	if err != nil {
		return nil, err
	}
	response, err := worker.generate(ctx, tab, turn, contents, prompt)
	if err != nil {
		worker.releaseChat(tab, false)
	}
	return response, err
}

// generate 在已取得的标签页上运行一轮对话并等待响应头
func (worker *Worker) generate(ctx context.Context, tab *chatTab, turn GenerateTurn, contents []json.RawMessage, prompt string) (*ProtectedResponse, error) {
	client := worker.client
	requestID := rand.Text()
	channel, err := client.openChannel(requestID)
	if err != nil {
		return nil, fmt.Errorf("界面运行 GenerateContent: %w", err)
	}
	unboost, err := worker.boostFrames(ctx)
	if err != nil {
		client.closeChannel(requestID)
		return nil, err
	}
	if err := worker.startTurn(ctx, tab, turn, contents, prompt, requestID); err != nil {
		unboost()
		return nil, worker.abandonProtectedRequest(ctx, tab.contextID, tab.page, requestID, err)
	}
	err = worker.waitSent(ctx, tab, requestID, channel)
	unboost()
	if err != nil {
		return nil, worker.abandonProtectedRequest(ctx, tab.contextID, tab.page, requestID, fmt.Errorf("等待页面应用发出 GenerateContent: %w", err))
	}
	worker.mu.Lock()
	tab.contents = contents
	worker.mu.Unlock()
	status, header, err := readResponseHead(ctx, channel)
	if err != nil {
		client.closeChannel(requestID)
		return nil, errors.Join(err, worker.stopTurn(tab))
	}
	if status != http.StatusOK {
		worker.mu.Lock()
		tab.contents = nil
		worker.mu.Unlock()
	}
	bodyCtx, cancelBody := context.WithCancel(ctx)
	return &ProtectedResponse{
		StatusCode: status,
		Header:     header,
		Body: &protectedResponseBody{
			ctx: bodyCtx, cancel: cancelBody, worker: worker, contextID: tab.contextID, page: tab.page, requestID: requestID, channel: channel,
			stop: func() error { return worker.stopTurn(tab) }, release: func(done bool) { worker.releaseChat(tab, done && status == http.StatusOK) },
		},
	}, nil
}

// startTurn 在标签页上关闭页面启动后出现的已知弹层，完成本轮的界面输入（输入框没有焦点时先点击输入框），安装一次性钩子后点击 Run 或 Rerun，指针仍停在 Run 上时原地点击
func (worker *Worker) startTurn(ctx context.Context, tab *chatTab, turn GenerateTurn, contents []json.RawMessage, prompt string, requestID string) error {
	client := worker.client
	worker.lockInput()
	defer worker.inputMu.Unlock()
	// input 是界面操作使用的上下文，请求取消只在两次操作之间生效，页面不会停在确认弹窗等操作中途
	input := context.WithoutCancel(ctx)
	if err := worker.selectChat(input, tab); err != nil {
		return err
	}
	if err := client.waitFor(ctx, tab.contextID, `!`+generationRunningExpression, 15*time.Second); err != nil {
		return fmt.Errorf("等待上一轮生成结束: %w", err)
	}
	if _, err := dismissKnownOverlays(input, client, tab.contextID); err != nil {
		return err
	}
	worker.mu.Lock()
	action := nextChatAction(tab.contents, contents)
	hovered := tab.runHovered
	tab.runHovered = false
	worker.mu.Unlock()
	if action == chatNew {
		hovered = false
		if err := worker.newChat(input, tab); err != nil {
			return err
		}
		if err := ctx.Err(); err != nil {
			return err
		}
	}
	if action != chatRerun {
		focused, err := client.evaluate(input, tab.contextID, promptFocusedExpression)
		if err != nil {
			return fmt.Errorf("检查官网输入框焦点: %w", err)
		}
		if focused["value"] != true {
			hovered = false
			if err := worker.click(input, tab.contextID, promptBoxSelector, "", "官网输入框", pointerMoveDuration); err != nil {
				return err
			}
		}
		if err := fillPrompt(input, client, tab.contextID, promptHead(prompt)); err != nil {
			return err
		}
		if err := worker.waitRunnable(ctx, tab); err != nil {
			return fmt.Errorf("等待官网 Run 按钮启用: %w", err)
		}
	}
	head, tail := string(turn.Body[:turn.ProofStart]), string(turn.Body[turn.ProofEnd:])
	if _, err := client.callFunction(ctx, tab.contextID, generateArmFunction, tab.page, requestID, turn.Digest, head, tail, bidiChannelArgument(requestID)); err != nil {
		return fmt.Errorf("安装界面运行钩子: %w", err)
	}
	if action == chatRerun {
		if _, err := client.pointer(input, tab.contextID, lastTurnSelector, "", false, pointerMoveDuration); err != nil {
			return fmt.Errorf("指针移到最后一轮: %w", err)
		}
		if err := client.waitFor(ctx, tab.contextID, `Boolean(document.querySelector('`+rerunButtonSelector+`'))`, 5*time.Second); err != nil {
			return fmt.Errorf("等待官网 Rerun 按钮: %w", err)
		}
		return worker.click(input, tab.contextID, rerunButtonSelector, "", "官网 Rerun 按钮", pointerMoveDuration)
	}
	move := pointerMoveDuration
	if hovered {
		move = 0
	}
	return worker.click(input, tab.contextID, runButtonSelector, "", "官网 Run 按钮", move)
}

// promptFillLimit 是界面运行写入官网输入框的最多字符数，与官网把更长的粘贴转为文本附件的长度相同；页面发出时正文整体换成服务的正文
const promptFillLimit = 25000

// promptHead 返回界面运行写入官网输入框的提示词开头，最多 promptFillLimit 个字符
func promptHead(prompt string) string {
	count := 0
	for index := range prompt {
		if count == promptFillLimit {
			return prompt[:index]
		}
		count++
	}
	return prompt
}

// waitRunnable 等页面处理完输入后官网 Run 按钮可点；期间弹出的已知弹层在此关闭，页面忙于处理时随之等待，页面空闲时 Run 按钮持续不可点时以页面给出的原因失败
func (worker *Worker) waitRunnable(ctx context.Context, tab *chatTab) error {
	for checks := 1; ; checks++ {
		state, err := readRunState(ctx, worker.client, tab.contextID, tab.page, "")
		if err != nil {
			return err
		}
		if state.Enabled {
			return nil
		}
		if len(state.Dialogs) > 0 || state.Cover != "" {
			clicked, err := dismissKnownOverlays(ctx, worker.client, tab.contextID)
			if err != nil {
				return err
			}
			if clicked > 0 {
				checks = 0
			}
		}
		if checks == runSettleChecks {
			return fmt.Errorf("页面空闲后仍不可点: %s", state.reason())
		}
		if err := waitContext(ctx, pagePollInterval); err != nil {
			return err
		}
	}
}

// waitSent 等页面应用发出本轮 GenerateContent；页面忙于处理或已开始运行时随之等待，点击后页面空闲时持续没有开始运行或弹出对话框时以页面给出的原因失败
func (worker *Worker) waitSent(ctx context.Context, tab *chatTab, requestID string, channel *bidiChannel) error {
	for checks := 1; ; checks++ {
		pollCtx, cancel := context.WithTimeout(ctx, pagePollInterval)
		message, err := readProtectedMessage(pollCtx, channel)
		cancel()
		if err != nil && (ctx.Err() != nil || !errors.Is(err, context.DeadlineExceeded)) {
			return err
		}
		if err == nil {
			return sentError(message)
		}
		state, err := readRunState(ctx, worker.client, tab.contextID, tab.page, requestID)
		if err != nil {
			return err
		}
		if state.Started || state.Running {
			message, err := readProtectedMessage(ctx, channel)
			if err != nil {
				return err
			}
			return sentError(message)
		}
		if len(state.Dialogs) > 0 || checks == runSettleChecks {
			return fmt.Errorf("点击 Run 后页面没有开始运行: %s", state.reason())
		}
	}
}

// sentError 返回通道上第一条消息表示的发送结果，页面已发出 GenerateContent 时为 nil
func sentError(message protectedMessage) error {
	if message.Error != "" {
		return errors.New(message.Error)
	}
	if !message.Sent {
		return errors.New("页面应用没有发出 GenerateContent")
	}
	return nil
}

// newChat 点击顶栏 New chat，在确认弹窗中丢弃当前对话，等页面清空并显示临时对话标识，临时对话中打开的新对话仍是临时对话
func (worker *Worker) newChat(ctx context.Context, tab *chatTab) error {
	client := worker.client
	if err := worker.click(ctx, tab.contextID, newChatButtonSelector, "", "官网 New chat 按钮", pointerMoveDuration); err != nil {
		return err
	}
	discard := fmt.Sprintf(`[...document.querySelectorAll('button')].some(node => node.checkVisibility({visibilityProperty: true}) && new RegExp(%q, 'i').test(node.innerText.trim()))`, discardButtonPattern)
	if err := client.waitFor(ctx, tab.contextID, discard, 5*time.Second); err != nil {
		return fmt.Errorf("等待官网新对话确认: %w", err)
	}
	if err := worker.click(ctx, tab.contextID, "button", discardButtonPattern, "官网 Discard and continue 按钮", pointerMoveDuration); err != nil {
		return err
	}
	if err := client.waitFor(ctx, tab.contextID, chatEmptyExpression, 10*time.Second); err != nil {
		return fmt.Errorf("等待官网新对话: %w", err)
	}
	if err := client.waitFor(ctx, tab.contextID, temporaryChatExpression, 10*time.Second); err != nil {
		return fmt.Errorf("等待官网临时对话: %w", err)
	}
	worker.mu.Lock()
	tab.contents = nil
	worker.mu.Unlock()
	return nil
}

// stopTurn 像官网用户一样点击标签页上的 Stop 中止本轮生成，生成已结束时不操作
func (worker *Worker) stopTurn(tab *chatTab) error {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	worker.lockInput()
	defer worker.inputMu.Unlock()
	unboost, err := worker.boostFrames(ctx)
	if err != nil {
		return err
	}
	defer unboost()
	if err := worker.selectChat(ctx, tab); err != nil {
		return err
	}
	running, err := worker.client.evaluateBool(ctx, tab.contextID, generationRunningExpression)
	if err != nil || !running {
		return err
	}
	return worker.click(ctx, tab.contextID, runButtonSelector, "", "官网 Stop 按钮", pointerMoveDuration)
}

// click 以可信指针用 move 毫秒移到 contextID 标签页上 selector 与 pattern 匹配的可见元素并点击，name 为错误中的元素名
func (worker *Worker) click(ctx context.Context, contextID, selector, pattern, name string, move int) error {
	found, err := worker.client.pointer(ctx, contextID, selector, pattern, true, move)
	if err != nil {
		return fmt.Errorf("点击%s: %w", name, err)
	}
	if !found {
		return fmt.Errorf("%s不存在", name)
	}
	return nil
}

// acquireChat 取得一个空闲的界面对话标签页并标为忙碌；没有空闲标签页时等待在后台新开对话的标签页，全部标签页都在运行对话时新建标签页、选中后在其中载入官网页面，载入期间其他标签页照常接收输入
func (worker *Worker) acquireChat(ctx context.Context) (*chatTab, error) {
	for {
		worker.mu.Lock()
		if worker.closed {
			worker.mu.Unlock()
			return nil, errors.New("Camoufox runtime 已关闭")
		}
		var resetting chan struct{}
		for _, tab := range worker.chats {
			if !tab.busy {
				tab.busy = true
				worker.useTab(tab.contextID)
				worker.mu.Unlock()
				return tab, nil
			}
			if tab.resetting != nil {
				resetting = tab.resetting
			}
		}
		if resetting == nil {
			break
		}
		worker.mu.Unlock()
		select {
		case <-resetting:
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	options := worker.options
	worker.mu.Unlock()
	options.StartupProgress = nil
	contextID, err := worker.createTab(ctx)
	if err != nil {
		return nil, err
	}
	worker.lockInput()
	err = worker.selectTab(ctx, contextID)
	worker.inputMu.Unlock()
	if err != nil {
		return nil, errors.Join(err, worker.closeTab(contextID))
	}
	loaded, err := worker.openPage(ctx, options, contextID)
	if err == nil && options.Headless && !options.liveView {
		worker.lockInput()
		worker.mu.Lock()
		selected := worker.selected == contextID
		worker.mu.Unlock()
		if selected {
			err = worker.stopRendering(ctx)
		}
		worker.inputMu.Unlock()
	}
	if err != nil {
		return nil, errors.Join(err, worker.closeTab(contextID))
	}
	tab := &chatTab{contextID: contextID, page: loaded.page, busy: true}
	worker.mu.Lock()
	defer worker.mu.Unlock()
	if worker.closed {
		return nil, errors.New("Camoufox runtime 已关闭")
	}
	worker.chats = append(worker.chats, tab)
	worker.useTab(contextID)
	return tab, nil
}

// releaseChat 结束标签页上的一轮对话；completed 表示本轮成功完成，此时在后台新开对话，下一轮在空白对话中直接输入
func (worker *Worker) releaseChat(tab *chatTab, completed bool) {
	worker.mu.Lock()
	reset := completed && !worker.closed
	if reset {
		tab.resetting = make(chan struct{})
	} else {
		tab.busy = false
	}
	worker.mu.Unlock()
	if !reset {
		worker.finishRequest(tab.contextID)
		return
	}
	go worker.resetChat(tab)
}

// resetChat 像官网用户开始新话题一样在标签页上点 New chat 并丢弃当前对话，结束后标签页变为空闲
func (worker *Worker) resetChat(tab *chatTab) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	err := worker.clearChat(ctx, tab)
	cancel()
	worker.mu.Lock()
	closed := worker.closed
	tab.busy = false
	close(tab.resetting)
	tab.resetting = nil
	worker.mu.Unlock()
	worker.finishRequest(tab.contextID)
	if err != nil && !closed {
		slog.Warn("Camoufox 标签页新开对话失败", "context", tab.contextID, "error", err)
	}
}

// clearChat 在标签页上等上一轮生成结束、关闭已知弹层后点 New chat，随后把指针移到 Run 按钮上
func (worker *Worker) clearChat(ctx context.Context, tab *chatTab) error {
	unboost, err := worker.boostFrames(ctx)
	if err != nil {
		return err
	}
	defer unboost()
	worker.lockInputIdle()
	defer worker.inputMu.Unlock()
	if err := worker.selectChat(ctx, tab); err != nil {
		return err
	}
	if err := worker.client.waitFor(ctx, tab.contextID, `!`+generationRunningExpression, 15*time.Second); err != nil {
		return fmt.Errorf("等待上一轮生成结束: %w", err)
	}
	if _, err := dismissKnownOverlays(ctx, worker.client, tab.contextID); err != nil {
		return err
	}
	if err := worker.newChat(ctx, tab); err != nil {
		return err
	}
	if _, err := worker.client.pointer(ctx, tab.contextID, runButtonSelector, "", false, pointerMoveDuration); err != nil {
		return fmt.Errorf("指针移到官网 Run 按钮: %w", err)
	}
	worker.mu.Lock()
	tab.runHovered = true
	worker.mu.Unlock()
	return nil
}

// selectChat 选中标签页；调用方持有 inputMu 并已提高帧率，界面操作结束时停止选中标签页的绘制
func (worker *Worker) selectChat(ctx context.Context, tab *chatTab) error {
	worker.mu.Lock()
	selected := worker.selected
	worker.mu.Unlock()
	if selected == tab.contextID {
		return nil
	}
	if _, err := worker.chromeEvaluate(ctx, fmt.Sprintf(selectTabExpression, tab.contextID)); err != nil {
		return fmt.Errorf("选中标签页: %w", err)
	}
	if err := worker.client.waitFor(ctx, tab.contextID, `document.visibilityState === 'visible'`, 10*time.Second); err != nil {
		return fmt.Errorf("等待标签页可见: %w", err)
	}
	worker.mu.Lock()
	worker.selected = tab.contextID
	worker.mu.Unlock()
	return nil
}
