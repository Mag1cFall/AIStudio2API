package api

import (
	"context"
	"crypto/rand"
	"net/http"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/Mag1cFall/AIStudio2API/internal/aistudio"
	"github.com/Mag1cFall/AIStudio2API/internal/chromeauth"
)

// AccountTask 是在服务端运行的添加或重新登录账户任务，管理页关闭或刷新后继续运行，结束后保留到用户关闭
type AccountTask struct {
	ID        string            `json:"id"`
	Kind      string            `json:"kind"`
	View      string            `json:"view,omitempty"`
	State     string            `json:"state"`
	Items     []AccountTaskItem `json:"items"`
	StartedAt time.Time         `json:"started_at"`
}

// AccountTaskItem 是任务中的一个账户，Label 为邮箱，浏览器登录完成前为空
type AccountTaskItem struct {
	Label string `json:"label"`
	State string `json:"state"`
	Code  string `json:"code,omitempty"`
	Error string `json:"error,omitempty"`
}

// chromeImportRequest 是从本机 Chrome 导入的账号与共用的账户环境
type chromeImportRequest struct {
	Accounts []struct {
		ID    string `json:"id"`
		Email string `json:"email"`
	} `json:"accounts"`
	Proxy    string `json:"proxy"`
	Locale   string `json:"locale"`
	Timezone string `json:"timezone"`
}

// cookieImportItem 是 Cookie 导入中的一个账户：识别出的邮箱、Cookie 文本或浏览器调试地址与会话序号
type cookieImportItem struct {
	Email    string `json:"email"`
	Cookies  string `json:"cookies"`
	CDP      string `json:"cdp"`
	AuthUser int    `json:"auth_user"`
}

// cookieImportRequest 是一次导入的 Cookie 账户与共用的账户环境，语言与时区为空时按出口位置填写
type cookieImportRequest struct {
	Items    []cookieImportItem `json:"items"`
	Proxy    string             `json:"proxy"`
	Locale   string             `json:"locale"`
	Timezone string             `json:"timezone"`
}

// accountTasks 保存账户任务，任务列表变化时推送给管理事件流
type accountTasks struct {
	mu          sync.Mutex
	tasks       []*accountTaskEntry
	subscribers map[chan []AccountTask]struct{}
}

// accountTaskEntry 是登记中的任务与它的取消函数
type accountTaskEntry struct {
	task   AccountTask
	cancel context.CancelFunc
}

// accountTaskUpdate 在锁内修改任务进度并推送任务列表，任务已被关闭时不修改
type accountTaskUpdate func(func(*AccountTask))

// clone 返回不与登记中的任务共用账户列表的副本
func (task AccountTask) clone() AccountTask {
	task.Items = slices.Clone(task.Items)
	return task
}

// start 登记任务并在不随请求取消的 context 中运行 run，run 返回后有账户失败时任务为 failed，否则为 done
func (tasks *accountTasks) start(r *http.Request, task AccountTask, run func(context.Context, accountTaskUpdate)) AccountTask {
	ctx, cancel := context.WithCancel(context.WithoutCancel(r.Context()))
	task.ID = rand.Text()
	task.State = "running"
	task.StartedAt = time.Now().UTC()
	entry := &accountTaskEntry{task: task.clone(), cancel: cancel}
	tasks.mu.Lock()
	tasks.tasks = append(tasks.tasks, entry)
	tasks.publishLocked()
	tasks.mu.Unlock()
	update := func(change func(*AccountTask)) {
		tasks.mu.Lock()
		defer tasks.mu.Unlock()
		if slices.Contains(tasks.tasks, entry) {
			change(&entry.task)
			tasks.publishLocked()
		}
	}
	go func() {
		defer cancel()
		run(ctx, update)
		update(func(task *AccountTask) {
			task.State = "done"
			if slices.ContainsFunc(task.Items, func(item AccountTaskItem) bool { return item.State == "failed" }) {
				task.State = "failed"
			}
		})
	}()
	return task
}

// list 按开始顺序返回全部任务
func (tasks *accountTasks) list() []AccountTask {
	tasks.mu.Lock()
	defer tasks.mu.Unlock()
	return tasks.snapshotLocked()
}

// remove 取消进行中的任务或关闭已结束的任务，任务不存在时返回 false
func (tasks *accountTasks) remove(id string) bool {
	tasks.mu.Lock()
	defer tasks.mu.Unlock()
	index := slices.IndexFunc(tasks.tasks, func(entry *accountTaskEntry) bool { return entry.task.ID == id })
	if index < 0 {
		return false
	}
	tasks.tasks[index].cancel()
	tasks.tasks = slices.Delete(tasks.tasks, index, index+1)
	tasks.publishLocked()
	return true
}

// subscribe 返回任务列表的变化，先送出当前列表，读取不及时时只保留最新列表
func (tasks *accountTasks) subscribe(ctx context.Context) <-chan []AccountTask {
	updates := make(chan []AccountTask, 1)
	tasks.mu.Lock()
	if tasks.subscribers == nil {
		tasks.subscribers = make(map[chan []AccountTask]struct{})
	}
	tasks.subscribers[updates] = struct{}{}
	updates <- tasks.snapshotLocked()
	tasks.mu.Unlock()
	context.AfterFunc(ctx, func() {
		tasks.mu.Lock()
		delete(tasks.subscribers, updates)
		tasks.mu.Unlock()
	})
	return updates
}

// snapshotLocked 返回任务列表的副本
func (tasks *accountTasks) snapshotLocked() []AccountTask {
	list := make([]AccountTask, len(tasks.tasks))
	for index, entry := range tasks.tasks {
		list[index] = entry.task.clone()
	}
	return list
}

// publishLocked 把最新任务列表放入每个订阅，替换尚未读取的旧列表
func (tasks *accountTasks) publishLocked() {
	list := tasks.snapshotLocked()
	for updates := range tasks.subscribers {
		select {
		case <-updates:
		default:
		}
		updates <- list
	}
}

// runAccountTaskItems 依次处理任务中的各个账户，单个账户失败时记录原因后继续下一个，任务被取消后停止
func runAccountTaskItems(ctx context.Context, update accountTaskUpdate, count int, run func(int) (AdminAccount, error)) {
	for index := 0; index < count && ctx.Err() == nil; index++ {
		update(func(task *AccountTask) { task.Items[index].State = "running" })
		account, err := run(index)
		update(func(task *AccountTask) {
			item := &task.Items[index]
			if err != nil {
				item.State, item.Code, item.Error = "failed", codeFromError(err, ""), err.Error()
				return
			}
			item.State, item.Label = "done", account.Label
		})
	}
}

// startAccountTask 登记并启动账户任务，返回 202 与任务
func (s *server) startAccountTask(w http.ResponseWriter, r *http.Request, task AccountTask, run func(context.Context, accountTaskUpdate)) {
	writeJSON(w, http.StatusAccepted, map[string]AccountTask{"task": s.accountTasks.start(r, task, run)})
}

// handleAccountTasks 返回账户任务
func (s *server) handleAccountTasks(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string][]AccountTask{"tasks": s.accountTasks.list()})
}

// handleDeleteAccountTask 取消进行中的账户任务或关闭已结束的任务
func (s *server) handleDeleteAccountTask(w http.ResponseWriter, r *http.Request) {
	if !s.accountTasks.remove(r.PathValue("id")) {
		writeAdminError(w, http.StatusNotFound, "task_not_found", "Account task not found")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// handleCreateAccount 在账户任务中以浏览器登录添加账户，登录显示在管理页登录窗口时任务带登录窗口 ID
func (s *server) handleCreateAccount(w http.ResponseWriter, r *http.Request) {
	var input struct {
		AccountCreateInput
		View string `json:"view"`
	}
	if err := decodeJSON(r, &input); err != nil {
		writeAdminError(w, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}
	view, release := s.loginView(r, input.View)
	task := AccountTask{Kind: "login", Items: []AccountTaskItem{{State: "pending"}}}
	if view != nil {
		task.View = input.View
	}
	s.startAccountTask(w, r, task, func(ctx context.Context, update accountTaskUpdate) {
		defer release()
		runAccountTaskItems(ctx, update, 1, func(int) (AdminAccount, error) {
			return s.config.Admin.CreateAccount(ctx, input.AccountCreateInput, view)
		})
	})
}

// handleLoginAccount 在账户任务中打开浏览器重新登录账户
func (s *server) handleLoginAccount(w http.ResponseWriter, r *http.Request) {
	var input struct {
		View string `json:"view"`
	}
	if r.ContentLength != 0 {
		if err := decodeJSON(r, &input); err != nil {
			writeAdminError(w, http.StatusBadRequest, "invalid_request", err.Error())
			return
		}
	}
	id := r.PathValue("id")
	view, release := s.loginView(r, input.View)
	task := AccountTask{Kind: "relogin", Items: []AccountTaskItem{{Label: id, State: "pending"}}}
	if view != nil {
		task.View = input.View
	}
	s.startAccountTask(w, r, task, func(ctx context.Context, update accountTaskUpdate) {
		defer release()
		runAccountTaskItems(ctx, update, 1, func(int) (AdminAccount, error) {
			return s.config.Admin.LoginAccount(ctx, id, view)
		})
	})
}

// handleImportChromeAccounts 在账户任务中逐个导入选中的本机 Chrome 账号
func (s *server) handleImportChromeAccounts(w http.ResponseWriter, r *http.Request) {
	var input chromeImportRequest
	if err := decodeJSON(r, &input); err != nil {
		writeAdminError(w, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}
	task := AccountTask{Kind: "chrome", Items: make([]AccountTaskItem, len(input.Accounts))}
	for index, account := range input.Accounts {
		task.Items[index] = AccountTaskItem{Label: account.Email, State: "pending"}
	}
	s.startAccountTask(w, r, task, func(ctx context.Context, update accountTaskUpdate) {
		runAccountTaskItems(ctx, update, len(input.Accounts), func(index int) (AdminAccount, error) {
			accounts, err := s.config.Admin.ImportChromeAccounts(ctx, ChromeImportInput{
				AccountIDs: []string{input.Accounts[index].ID},
				Proxy:      input.Proxy, Locale: input.Locale, Timezone: input.Timezone,
			})
			if err != nil {
				return AdminAccount{}, err
			}
			return accounts[0], nil
		})
	})
}

// handleImportCookieAccounts 在账户任务中逐个导入 Cookie 文本或调试浏览器中的账户
func (s *server) handleImportCookieAccounts(w http.ResponseWriter, r *http.Request) {
	var input cookieImportRequest
	if err := decodeJSON(r, &input); err != nil {
		writeAdminError(w, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}
	task := AccountTask{Kind: "cookies", Items: make([]AccountTaskItem, len(input.Items))}
	for index, item := range input.Items {
		task.Items[index] = AccountTaskItem{Label: item.Email, State: "pending"}
		if strings.TrimSpace(item.CDP) != "" {
			task.Kind = "browser"
		}
	}
	s.startAccountTask(w, r, task, func(ctx context.Context, update accountTaskUpdate) {
		runAccountTaskItems(ctx, update, len(input.Items), func(index int) (AdminAccount, error) {
			return s.importCookieAccount(ctx, input.Items[index], input.Proxy, input.Locale, input.Timezone)
		})
	})
}

// importCookieAccount 读取调试浏览器的 Cookie 或解析 Cookie 文本，导入第 auth_user 个会话登录的账户
func (s *server) importCookieAccount(ctx context.Context, item cookieImportItem, proxy string, locale string, timezone string) (AdminAccount, error) {
	cookies := item.Cookies
	if address := strings.TrimSpace(item.CDP); address != "" {
		browser, err := chromeauth.ReadCDPBrowser(ctx, address)
		if err != nil {
			return AdminAccount{}, err
		}
		cookies = browser.Cookies
	}
	state, err := aistudio.ParseCookieText(cookies)
	if err != nil {
		return AdminAccount{}, err
	}
	return s.config.Admin.ImportAccountState(ctx, AccountStateInput{
		Proxy: proxy, Locale: locale, Timezone: timezone, AuthUser: item.AuthUser, StorageState: state,
	})
}
