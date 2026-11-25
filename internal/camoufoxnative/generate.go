package camoufoxnative

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/Mag1cFall/AIStudio2API/internal/waa/prooftime"
	"log/slog"
	"net/http"
	"strings"
	"time"
	"unicode/utf16"
)

// GenerateTurn 是在页面界面运行的一轮 GenerateContent：Body 第 ProofStart 到 ProofEnd 字节是 proof 字段，发送时换成页面应用为 Digest 取得的 proof；Digest 为 binding 的 SHA-256 十六进制，Result 是最后一个 user 轮次为函数结果时官网用户在函数调用块中输入的函数结果，其余为空
type GenerateTurn struct {
	Body       []byte
	ProofStart int
	ProofEnd   int
	Digest     string
	Result     string
	// Step 在界面运行进入每个界面步骤与页面发出 GenerateContent 时调用，为空时不报告
	Step func(GenerateStep)
}

// GenerateStep 是界面运行一轮 GenerateContent 所在的界面步骤，取值为步骤说明
type GenerateStep string

const (
	// StepAcquireTab 等待空闲的界面对话标签页
	StepAcquireTab GenerateStep = "取得标签页"
	// StepLockInput 等待其他请求的界面操作结束
	StepLockInput GenerateStep = "等待其他请求的界面操作"
	// StepSelectTab 选中标签页并等上一轮生成结束
	StepSelectTab GenerateStep = "选中标签页"
	// StepNewChat 点 New chat 并丢弃当前对话
	StepNewChat GenerateStep = "新开对话"
	// StepPastePrompt 点击输入框并粘贴提示词
	StepPastePrompt GenerateStep = "粘贴提示词"
	// StepWaitRunnable 等官网 Run 按钮可点
	StepWaitRunnable GenerateStep = "等待 Run 可点"
	// StepClickRun 点击 Run
	StepClickRun GenerateStep = "点击 Run"
	// StepClickRerun 点击最后一轮的 Rerun
	StepClickRerun GenerateStep = "点击 Rerun"
	// StepPasteResult 在函数调用块中点击函数响应输入框并粘贴函数结果
	StepPasteResult GenerateStep = "粘贴函数结果"
	// StepClickSubmit 点击函数调用块的提交按钮
	StepClickSubmit GenerateStep = "点击提交"
	// StepWaitSent 点击后等页面应用发出 GenerateContent
	StepWaitSent GenerateStep = "等待页面发出请求"
	// StepSent 页面已发出 GenerateContent，此后等待响应头
	StepSent GenerateStep = "页面已发出请求"
)

// 界面运行各步骤的等待上限
const (
	// chatAcquireTimeout 是取得界面对话标签页并提高其刷新帧率的上限，与新标签页载入官网页面时等输入框出现的上限相同
	chatAcquireTimeout = 2 * time.Minute
	// inputLockTimeout 是等待其他请求界面操作结束的上限，与重新载入页面期间持有界面输入时等输入框出现的上限相同
	inputLockTimeout = 2 * time.Minute
	// turnInputTimeout 是一轮界面输入从选中标签页到点击结束的上限，覆盖其中等上一轮生成结束、新开对话各步的上限之和与页面忙碌时的等待
	turnInputTimeout = time.Minute
	// pageSendTimeout 是页面进入运行后发出 GenerateContent 的上限，与载入页面时等官网 WAA service 与 WAA 程序图片请求的上限相同
	pageSendTimeout = 30 * time.Second
)

// report 报告界面运行进入 step
func (turn GenerateTurn) report(step GenerateStep) {
	if turn.Step != nil {
		turn.Step(step)
	}
}

// stepTimeout 返回界面运行在 step 等满 limit 的错误
func stepTimeout(step GenerateStep, limit time.Duration) error {
	return fmt.Errorf("界面运行停在%s，%s 内没有完成", step, limit)
}

// chatTab 是运行界面对话的标签页
type chatTab struct {
	contextID string
	page      pageHandle
	// busy 表示标签页正在运行一轮对话
	busy bool
	// contents 是标签页上一轮成功完成的 GenerateContent 的 contents，页面没有对话或上一轮未成功完成时为空
	contents []json.RawMessage
	// resetting 表示一轮对话结束后正在后台新开对话，或页面正在原标签页重新载入
	resetting bool
	// runHovered 表示后台新开对话后指针停在 Run 按钮上
	runHovered bool
	// unsent 表示输入框或函数响应输入框中留有已粘贴、页面没有发出的输入，下一轮全选后粘贴，原本要 Rerun 时先开新对话
	unsent bool
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

// lastUserParts 返回 GenerateContent 正文最后一个轮次是 user 轮次时它的各 Part，否则为 nil
func lastUserParts(body []byte) [][]json.RawMessage {
	contents, err := generateContents(body)
	if err != nil || len(contents) == 0 || contentRole(contents[len(contents)-1]) != "user" {
		return nil
	}
	var turn []json.RawMessage
	var parts [][]json.RawMessage
	if json.Unmarshal(contents[len(contents)-1], &turn) != nil || len(turn) == 0 || json.Unmarshal(turn[0], &parts) != nil {
		return nil
	}
	return parts
}

// GenerateAttachments 返回 GenerateContent 正文最后一个 user 轮次中内联数据、Drive 文件与外部媒体 Part 的个数，即官网用户在输入框中加入的附件数
func GenerateAttachments(body []byte) int {
	count := 0
	for _, part := range lastUserParts(body) {
		for _, index := range []int{2, 5, 6} {
			if len(part) > index && !bytes.Equal(part[index], []byte("null")) {
				count++
				break
			}
		}
	}
	return count
}

// GenerateFiles 返回 GenerateContent 正文最后一个 user 轮次中各 Drive 文件 Part 的文件 ID，顺序与正文相同
func GenerateFiles(body []byte) []string {
	var files []string
	for _, part := range lastUserParts(body) {
		var file []string
		if len(part) > 5 && json.Unmarshal(part[5], &file) == nil && len(file) > 0 {
			files = append(files, file[0])
		}
	}
	return files
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

// StartsNewChat 返回页面上一次发出的对话 previous 之后要发出 contents 时是否先开新对话
func StartsNewChat(previous, contents []json.RawMessage) bool {
	return nextChatAction(previous, contents) == chatNew
}

// generateArmFunction 是在页面上为下一次 Run 安装一次性钩子的函数声明
// 参数依次为页面对象、请求 ID、digest、head、tail 与通道；页面发送的正文换成 head + proof + tail
// 通道先发出 {sent: true, proof, refresh, compute}，再依次发出响应头、正文块与结束标记
// refresh 与 compute 是等待官网 WAA 初始化与重建和生成 proof 的毫秒数
// request.release 之后返回的 proof 不改写页面的请求；request.cancel 之后的下一次可信点击在捕获阶段整次拦下
const generateArmFunction = `(page, id, digest, head, tail, emit) => {
  const makerSuite = window.default_MakerSuite;
  const open = XMLHttpRequest.prototype.open;
  const send = XMLHttpRequest.prototype.send;
  const request = {xhr: null, credit: 8, resume: null};
  (page.requests ||= new Map()).set(id, request);
  let loaded = false, failure = "", wake = null;
  const guarded = ["pointerdown", "mousedown", "pointerup", "mouseup", "click"];
  let canceled = false, blocking = false;
  const guard = event => {
    if (!event.isTrusted) return;
    if (event.type === "pointerdown") blocking = canceled;
    if (blocking) {
      event.preventDefault();
      event.stopImmediatePropagation();
    }
    if (event.type === "click") unguard();
  };
  const unguard = () => guarded.forEach(type => removeEventListener(type, guard, true));
  guarded.forEach(type => addEventListener(type, guard, true));
  request.cancel = () => canceled = true;
  const changed = () => {
    wake?.();
    wake = null;
  };
  const next = () => new Promise(resolve => wake = resolve);
  makerSuite[page.key] = function(service) {
    makerSuite[page.key] = page.original;
    request.started = true;
    const timing = (` + proofTimingFunction + `)(service);
    return page.original.call(this, service, digest).then(proof => {
      if (failure) return proof;
      const {refresh, compute} = timing();
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
            emit(JSON.stringify({sent: true, proof, refresh, compute}));
            return send.call(xhr, head + JSON.stringify(proof) + tail);
          };
        }
        return open.apply(this, arguments);
      };
      return proof;
    });
  };
  request.release = () => {
    unguard();
    if (request.xhr) return;
    if (makerSuite[page.key] !== page.original) makerSuite[page.key] = page.original;
    if (XMLHttpRequest.prototype.open !== open) XMLHttpRequest.prototype.open = open;
    failure = "AbortError: The operation was aborted.";
    changed();
  };
` + protectedPump + `}`

// generateCancelFunction 是让页面拦下界面运行还没有按下的点击的函数声明，参数为页面对象与请求 ID
const generateCancelFunction = `(page, id) => page.requests?.get(id)?.cancel()`

// Generate 在标签页界面运行一轮 GenerateContent
// 页面应用自己取 proof、构造请求头并发出请求，发送时正文换成 turn 的正文
// 最后一个 user 轮次是函数结果时，在最后一轮是其所回应函数调用的标签页中运行
// 一轮成功完成后在后台新开对话，回答为函数调用时留在当前对话；上游返回错误时下一轮在当前对话中继续
// 全部标签页都在运行对话时在新标签页中载入官网页面
func (worker *Worker) Generate(ctx context.Context, turn GenerateTurn) (*ProtectedResponse, error) {
	contents, err := generateContents(turn.Body)
	if err != nil {
		return nil, err
	}
	prompt, ok := GeneratePrompt(turn.Body)
	if !ok && turn.Result == "" {
		return nil, errors.New("GenerateContent 最后一个轮次不是带文本或函数结果的 user 轮次")
	}
	lockStarted := time.Now()
	turn.report(StepAcquireTab)
	acquireCtx, cancel := context.WithTimeout(ctx, chatAcquireTimeout)
	defer cancel()
	var tab *chatTab
	if turn.Result != "" {
		tab, err = worker.acquireFunctionChat(contents)
	} else {
		tab, err = worker.acquireChat(acquireCtx)
	}
	var unboost func()
	if err == nil {
		if unboost, err = worker.boostFrames(acquireCtx); err != nil {
			worker.releaseChat(tab, false)
		}
	}
	if err != nil {
		if ctx.Err() == nil && acquireCtx.Err() != nil {
			err = fmt.Errorf("%w: %w", stepTimeout(StepAcquireTab, chatAcquireTimeout), err)
		}
		return nil, err
	}
	prooftime.Observe(ctx, prooftime.Lock, time.Since(lockStarted))
	response, err := worker.generate(ctx, tab, unboost, turn, contents, prompt)
	if err != nil {
		worker.releaseChat(tab, false)
	}
	return response, err
}

// generate 在已取得并提高了刷新帧率的标签页上运行一轮对话并等待响应头，界面输入结束或失败时调用 unboost
func (worker *Worker) generate(ctx context.Context, tab *chatTab, unboost func(), turn GenerateTurn, contents []json.RawMessage, prompt string) (*ProtectedResponse, error) {
	client := worker.client
	requestID := rand.Text()
	channel, err := client.openChannel(requestID)
	if err != nil {
		unboost()
		return nil, fmt.Errorf("界面运行 GenerateContent: %w", err)
	}
	inputStarted := time.Now()
	turn.report(StepLockInput)
	if err := worker.lockInputWithin(ctx, inputLockTimeout); err != nil {
		unboost()
		client.closeChannel(requestID)
		if ctx.Err() == nil {
			err = stepTimeout(StepLockInput, inputLockTimeout)
		}
		return nil, err
	}
	locked := time.Now()
	prooftime.Observe(ctx, prooftime.Lock, locked.Sub(inputStarted))
	turned := make(chan error, 1)
	go func() {
		defer worker.inputMu.Unlock()
		turnCtx, cancel := context.WithTimeoutCause(ctx, turnInputTimeout, errTurnInputTimeout)
		current := StepSelectTab
		err := worker.startTurn(turnCtx, tab, turn, contents, prompt, requestID, func(step GenerateStep) {
			current = step
			turn.report(step)
		})
		if err != nil && context.Cause(turnCtx) == errTurnInputTimeout {
			err = fmt.Errorf("%w: %w", stepTimeout(current, turnInputTimeout), err)
		}
		cancel()
		turned <- err
	}()
	sent, err := worker.waitSent(ctx, tab, turn, requestID, channel, turned)
	unboost()
	if err != nil {
		prooftime.Observe(ctx, prooftime.Interaction, time.Since(locked))
		return nil, worker.abandonProtectedRequest(ctx, tab.contextID, tab.page, requestID, err)
	}
	turn.report(StepSent)
	sent.observe(ctx, locked)
	worker.mu.Lock()
	tab.contents = nil
	tab.unsent = false
	worker.mu.Unlock()
	status, header, err := readResponseHead(ctx, channel)
	if err != nil {
		client.closeChannel(requestID)
		return nil, errors.Join(err, worker.stopTurn(tab))
	}
	bodyCtx, cancelBody := context.WithCancel(ctx)
	return &ProtectedResponse{
		StatusCode: status,
		Header:     header,
		Proof:      sent.Proof,
		Body: &protectedResponseBody{
			ctx: bodyCtx, cancel: cancelBody, worker: worker, contextID: tab.contextID, page: tab.page, requestID: requestID, channel: channel,
			stop: func() error { return worker.stopTurn(tab) },
			release: func(completed, call bool) {
				ok := completed && status == http.StatusOK
				if ok {
					worker.mu.Lock()
					tab.contents = contents
					worker.mu.Unlock()
				}
				worker.releaseChat(tab, ok && !call)
			},
		},
	}, nil
}

// errTurnInputTimeout 是一轮界面输入超过 turnInputTimeout 时输入上下文的结束原因
var errTurnInputTimeout = errors.New("界面输入超时")

// startTurn 在标签页上关闭页面启动后出现的已知弹层，完成本轮的界面输入（输入框没有焦点时先点击输入框；函数结果在最后一轮的函数调用块中输入并提交），安装一次性钩子后点击 Run 或 Rerun，指针仍停在 Run 上时原地点击，进入每个界面步骤时调用 step；调用方持有 inputMu
func (worker *Worker) startTurn(ctx context.Context, tab *chatTab, turn GenerateTurn, contents []json.RawMessage, prompt string, requestID string, step func(GenerateStep)) error {
	client := worker.client
	// input 是界面操作使用的上下文：ctx 被取消只在两次操作之间生效，页面不会停在确认弹窗等操作中途；ctx 的期限照常约束每个操作
	input := context.WithoutCancel(ctx)
	if deadline, ok := ctx.Deadline(); ok {
		var cancel context.CancelFunc
		input, cancel = context.WithDeadline(input, deadline)
		defer cancel()
	}
	step(StepSelectTab)
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
	if tab.unsent && action == chatRerun {
		action = chatNew
	}
	worker.mu.Unlock()
	if turn.Result != "" {
		return worker.submitResult(ctx, input, tab, turn, requestID, step)
	}
	if action == chatNew {
		hovered = false
		if err := ctx.Err(); err != nil {
			return err
		}
		step(StepNewChat)
		if err := worker.newChat(input, tab); err != nil {
			return err
		}
	}
	if action != chatRerun {
		if err := ctx.Err(); err != nil {
			return err
		}
		step(StepPastePrompt)
		focused, err := client.evaluate(input, tab.contextID, promptFocusedExpression)
		if err != nil {
			return fmt.Errorf("检查官网输入框焦点: %w", err)
		}
		if focused["value"] != true {
			hovered = false
			if err := worker.click(input, tab.contextID, promptBoxSelector, "", "官网输入框", pointerMoveDuration); err != nil {
				return err
			}
			if err := ctx.Err(); err != nil {
				return err
			}
		}
		worker.mu.Lock()
		unsent := tab.unsent
		tab.unsent = true
		worker.mu.Unlock()
		if unsent {
			if err := pressKeys(input, client, tab.contextID, selectAllKeyActions); err != nil {
				return fmt.Errorf("全选官网输入框中未发出的输入: %w", err)
			}
		}
		if err := pastePrompt(input, client, tab.contextID, promptHead(prompt)); err != nil {
			return err
		}
		step(StepWaitRunnable)
		if err := worker.waitRunnable(ctx, tab); err != nil {
			return fmt.Errorf("等待官网 Run 按钮启用: %w", err)
		}
	}
	if err := worker.arm(ctx, tab, turn, requestID); err != nil {
		return err
	}
	if action == chatRerun {
		step(StepClickRerun)
		if _, err := client.pointer(input, tab.contextID, lastTurnSelector, "", false, pointerMoveDuration); err != nil {
			return fmt.Errorf("指针移到最后一轮: %w", err)
		}
		if err := client.waitFor(ctx, tab.contextID, `Boolean(document.querySelector('`+rerunButtonSelector+`'))`, 5*time.Second); err != nil {
			return fmt.Errorf("等待官网 Rerun 按钮: %w", err)
		}
		return worker.clickArmed(ctx, tab, requestID, rerunButtonSelector, "官网 Rerun 按钮", pointerMoveDuration)
	}
	move := pointerMoveDuration
	if hovered {
		move = 0
	}
	step(StepClickRun)
	return worker.clickArmed(ctx, tab, requestID, runButtonSelector, "官网 Run 按钮", move)
}

// clickArmed 以可信指针用 move 毫秒移到已安装一次性钩子的标签页上 selector 匹配的可见按钮并点击，name 为错误中的按钮名；ctx 在点击前结束时不点击，点击期间结束时让页面拦下还没有按下的这次点击，两种情况都返回 ctx 的错误
func (worker *Worker) clickArmed(ctx context.Context, tab *chatTab, requestID, selector, name string, move int) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	var cancelErr error
	canceled := make(chan struct{})
	stop := context.AfterFunc(ctx, func() {
		defer close(canceled)
		cancelCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		if _, err := worker.client.callFunction(cancelCtx, tab.contextID, generateCancelFunction, tab.page, requestID); err != nil {
			cancelErr = fmt.Errorf("让页面拦下点击%s: %w", name, err)
		}
	})
	err := worker.click(context.WithoutCancel(ctx), tab.contextID, selector, "", name, move)
	if !stop() {
		<-canceled
		return errors.Join(err, ctx.Err(), cancelErr)
	}
	return err
}

// arm 在标签页上为下一次 Run 或提交安装 generateArmFunction 一次性钩子
func (worker *Worker) arm(ctx context.Context, tab *chatTab, turn GenerateTurn, requestID string) error {
	head, tail := string(turn.Body[:turn.ProofStart]), string(turn.Body[turn.ProofEnd:])
	if _, err := worker.client.callFunction(ctx, tab.contextID, generateArmFunction, tab.page, requestID, turn.Digest, head, tail, bidiChannelArgument(requestID)); err != nil {
		return fmt.Errorf("安装界面运行钩子: %w", err)
	}
	return nil
}

// selectAllKeyActions 是在有焦点的输入框按 Ctrl+A 全选的可信键盘动作
var selectAllKeyActions = []map[string]any{
	{"type": "keyDown", "value": "\uE009"}, {"type": "pause", "duration": 25},
	{"type": "keyDown", "value": "a"}, {"type": "pause", "duration": 25}, {"type": "pause", "duration": 70},
	{"type": "keyUp", "value": "a"}, {"type": "pause", "duration": 25},
	{"type": "keyUp", "value": "\uE009"},
}

// promptFillLimit 是界面运行写入官网输入框的最多 UTF-16 码元数，与官网把更长的粘贴转为文本附件的长度相同；页面发出时正文整体换成服务的正文
const promptFillLimit = 25000

// promptHead 返回界面运行写入官网输入框的提示词开头，最多 promptFillLimit 个 UTF-16 码元，不拆开代理对
func promptHead(prompt string) string {
	units := 0
	for index, char := range prompt {
		units += utf16.RuneLen(char)
		if units > promptFillLimit {
			return prompt[:index]
		}
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

// errInputEnded 表示本轮界面输入已经结束
var errInputEnded = errors.New("界面输入已结束")

// waitSent 等页面应用发出本轮 GenerateContent 并返回发出标记，turned 送出本轮界面输入的结果；页面在界面输入结束前发出时立即返回发出标记，没有提前发出时等界面输入结束后报告 StepWaitSent 并继续等待，输入失败时返回它的错误
func (worker *Worker) waitSent(ctx context.Context, tab *chatTab, turn GenerateTurn, requestID string, channel *bidiChannel, turned <-chan error) (protectedMessage, error) {
	inputCtx, cancel := context.WithCancelCause(ctx)
	defer cancel(nil)
	var inputErr error
	ended := make(chan struct{})
	go func() {
		inputErr = <-turned
		close(ended)
		cancel(errInputEnded)
	}()
	message, err := readProtectedMessage(inputCtx, channel)
	if err == nil {
		sent, err := sentProof(message)
		if err != nil {
			<-ended
			return protectedMessage{}, fmt.Errorf("等待页面应用发出 GenerateContent: %w", err)
		}
		return sent, nil
	}
	<-ended
	if inputErr != nil {
		return protectedMessage{}, inputErr
	}
	if context.Cause(inputCtx) != errInputEnded {
		return protectedMessage{}, fmt.Errorf("等待页面应用发出 GenerateContent: %w", err)
	}
	turn.report(StepWaitSent)
	sent, err := worker.waitStarted(ctx, tab, requestID, channel)
	if err != nil {
		return protectedMessage{}, fmt.Errorf("等待页面应用发出 GenerateContent: %w", err)
	}
	return sent, nil
}

// waitStarted 在界面输入结束后等页面应用发出本轮 GenerateContent 并返回发出标记；页面忙于处理时随之等待，开始运行后最多等 pageSendTimeout，点击后页面空闲时持续没有开始运行或弹出对话框时以页面给出的原因失败
func (worker *Worker) waitStarted(ctx context.Context, tab *chatTab, requestID string, channel *bidiChannel) (protectedMessage, error) {
	for checks := 1; ; checks++ {
		pollCtx, cancel := context.WithTimeout(ctx, pagePollInterval)
		message, err := readProtectedMessage(pollCtx, channel)
		cancel()
		if err != nil && (ctx.Err() != nil || !errors.Is(err, context.DeadlineExceeded)) {
			return protectedMessage{}, err
		}
		if err == nil {
			return sentProof(message)
		}
		state, err := readRunState(ctx, worker.client, tab.contextID, tab.page, requestID)
		if err != nil {
			return protectedMessage{}, err
		}
		if state.Started || state.Running {
			sendCtx, cancel := context.WithTimeout(ctx, pageSendTimeout)
			message, err := readProtectedMessage(sendCtx, channel)
			cancel()
			if err != nil && ctx.Err() == nil && errors.Is(err, context.DeadlineExceeded) {
				return protectedMessage{}, fmt.Errorf("%w: 页面已开始运行，官网取 proof 的函数已调用=%t", stepTimeout(StepWaitSent, pageSendTimeout), state.Started)
			}
			if err != nil {
				return protectedMessage{}, err
			}
			return sentProof(message)
		}
		if len(state.Dialogs) > 0 || checks == runSettleChecks {
			return protectedMessage{}, fmt.Errorf("点击 Run 后页面没有开始运行: %s", state.reason())
		}
	}
}

// sentProof 返回通道上第一条消息表示的发送结果，页面已发出 GenerateContent 时返回带 proof 与取 proof 分项耗时的发出标记
func sentProof(message protectedMessage) (protectedMessage, error) {
	if message.Error != "" {
		return protectedMessage{}, errors.New(message.Error)
	}
	if !message.Sent {
		return protectedMessage{}, errors.New("页面应用没有发出 GenerateContent")
	}
	return message, nil
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
	tab.unsent = false
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

// acquireChat 取得一个空闲的界面对话标签页并标为忙碌；没有空闲标签页时等待在后台新开对话的标签页，全部标签页都在运行对话时在后台新建标签页并载入官网页面，同时等已有标签页空出与新标签页载入完成，先到哪个用哪个，新开失败且没有标签页空出时返回失败原因，ctx 结束时返回它的错误；载入期间其他标签页照常接收输入
func (worker *Worker) acquireChat(ctx context.Context) (*chatTab, error) {
	worker.mu.Lock()
	defer worker.mu.Unlock()
	waited := false
	for {
		if worker.closed {
			return nil, errors.New("Camoufox runtime 已关闭")
		}
		resetting := false
		var fallback *chatTab
		for _, tab := range worker.chats {
			if !tab.busy {
				if tab.contents == nil && !tab.unsent {
					tab.busy = true
					worker.useTab(tab.contextID)
					return tab, nil
				}
				if fallback == nil {
					fallback = tab
				}
				continue
			}
			resetting = resetting || tab.resetting
		}
		if fallback != nil {
			fallback.busy = true
			worker.useTab(fallback.contextID)
			return fallback, nil
		}
		if !resetting && !worker.chatOpening {
			if waited && worker.chatOpenErr != nil {
				return nil, worker.chatOpenErr
			}
			worker.openChat()
		}
		if worker.chatsChanged == nil {
			worker.chatsChanged = make(chan struct{})
		}
		changed := worker.chatsChanged
		worker.mu.Unlock()
		select {
		case <-changed:
		case <-ctx.Done():
			worker.mu.Lock()
			return nil, ctx.Err()
		}
		worker.mu.Lock()
		waited = true
	}
}

// notifyChats 唤醒等待界面对话标签页空出或新开结束的请求；调用方持有 mu
func (worker *Worker) notifyChats() {
	if worker.chatsChanged != nil {
		close(worker.chatsChanged)
		worker.chatsChanged = nil
	}
}

// openChat 在后台新建标签页并载入官网页面，结束后把它作为空闲标签页加入或记下失败原因，并唤醒等待标签页的请求；期间发生重新载入时关闭该旧标签页；调用方持有 mu
func (worker *Worker) openChat() {
	worker.chatOpening, worker.chatOpenErr = true, nil
	generation := worker.reloads
	options := worker.options
	options.StartupProgress = nil
	go func() {
		tab, err := worker.loadChat(context.Background(), options)
		worker.mu.Lock()
		closed := worker.closed
		stale := worker.reloads != generation
		if stale {
			worker.chatOpenErr = nil
		} else {
			worker.chatOpenErr = err
		}
		worker.chatOpening = false
		if err == nil && !closed && !stale {
			worker.chats = append(worker.chats, tab)
		} else if err == nil && worker.selected == tab.contextID {
			worker.selected = ""
		}
		worker.notifyChats()
		worker.mu.Unlock()
		if err == nil && !closed && stale {
			if closeErr := worker.closeTab(tab.contextID); closeErr != nil {
				slog.Warn("关闭重新载入前新开的 Camoufox 标签页失败", "context", tab.contextID, "error", closeErr)
			}
		}
	}()
}

// loadChat 新建标签页、选中后在其中载入官网页面，返回空闲的界面对话标签页
func (worker *Worker) loadChat(ctx context.Context, options Options) (*chatTab, error) {
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
	return &chatTab{contextID: contextID, page: loaded.page}, nil
}

// releaseChat 结束标签页上的一轮对话；completed 表示本轮成功完成，标签页未退役时在后台新开对话，下一轮在空白对话中直接输入
func (worker *Worker) releaseChat(tab *chatTab, completed bool) {
	worker.mu.Lock()
	retired := worker.tabs[tab.contextID] != nil && worker.tabs[tab.contextID].retired
	reset := completed && !worker.closed && !retired
	if reset {
		tab.resetting = true
	} else {
		tab.busy = false
		worker.notifyChats()
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
	tab.busy, tab.resetting = false, false
	worker.notifyChats()
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
	worker.mu.Lock()
	retired := worker.closed || (worker.tabs[tab.contextID] != nil && worker.tabs[tab.contextID].retired)
	worker.mu.Unlock()
	if retired {
		return nil
	}
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
