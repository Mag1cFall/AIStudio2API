package setup

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"sync"
	"time"

	"github.com/Mag1cFall/AIStudio2API/internal/aistudio"
	"github.com/Mag1cFall/AIStudio2API/internal/api"
	"github.com/Mag1cFall/AIStudio2API/internal/chromeauth"
)

// renewalInterval 是为配对账户推送新会话的间隔，短于会话签发后约 2 小时的有效期
const renewalInterval = 100 * time.Minute

// renewalRetry 是推送失败后的重试间隔
const renewalRetry = 5 * time.Minute

// errRenewalRevoked 表示远程实例已删除该账户或续签凭据失效，之后的续签不会成功
var errRenewalRevoked = errors.New("远程账户已删除或续签凭据失效")

// renewalsPath 是本机保存远程配对账户续签材料的文件
var renewalsPath = filepath.Join("runtime", "remote-renewals.json")

// remoteRenewal 是本机为远程实例上一个配对账户签发新会话所需的材料
type remoteRenewal struct {
	Origin  string                       `json:"origin"`
	Account string                       `json:"account"`
	Token   string                       `json:"token"`
	Source  aistudio.AuthSource          `json:"source"`
	OAuth   aistudio.ChromeOAuthMaterial `json:"oauth"`
	Proxy   string                       `json:"proxy,omitempty"`
}

// readRenewals 读取本机保存的全部续签材料，文件不存在时为空
func readRenewals() ([]remoteRenewal, error) {
	data, err := os.ReadFile(renewalsPath)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("读取续签记录: %w", err)
	}
	var renewals []remoteRenewal
	if err := json.Unmarshal(data, &renewals); err != nil {
		return nil, fmt.Errorf("解析续签记录: %w", err)
	}
	return renewals, nil
}

// saveRenewals 用 added 替换同一实例同一账户的旧记录后写回续签材料
func saveRenewals(added []remoteRenewal) error {
	renewals, err := readRenewals()
	if err != nil {
		return err
	}
	kept := make([]remoteRenewal, 0, len(renewals)+len(added))
	for _, renewal := range renewals {
		if !slices.ContainsFunc(added, func(item remoteRenewal) bool { return item.Origin == renewal.Origin && item.Account == renewal.Account }) {
			kept = append(kept, renewal)
		}
	}
	return writeRenewals(append(kept, added...))
}

// removeRenewal 从续签记录中删除 removed 的实例、账户与续签凭据都相同的记录
func removeRenewal(removed remoteRenewal) error {
	renewals, err := readRenewals()
	if err != nil {
		return err
	}
	return writeRenewals(slices.DeleteFunc(renewals, func(item remoteRenewal) bool {
		return item.Origin == removed.Origin && item.Account == removed.Account && item.Token == removed.Token
	}))
}

// writeRenewals 写回全部续签记录
func writeRenewals(renewals []remoteRenewal) error {
	data, err := json.MarshalIndent(renewals, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(renewalsPath), 0o755); err != nil {
		return fmt.Errorf("创建续签记录目录: %w", err)
	}
	if err := os.WriteFile(renewalsPath, data, 0o600); err != nil {
		return fmt.Errorf("保存续签记录: %w", err)
	}
	return nil
}

// originRenewals 返回本机为 origin 保存的续签材料
func originRenewals(origin string) ([]remoteRenewal, error) {
	renewals, err := readRenewals()
	if err != nil {
		return nil, err
	}
	var selected []remoteRenewal
	for _, renewal := range renewals {
		if renewal.Origin == origin {
			selected = append(selected, renewal)
		}
	}
	if len(selected) == 0 {
		return nil, fmt.Errorf("本机没有 %s 的续签记录，先用 setup --remote 上传 Chrome 导入的账户", origin)
	}
	return selected, nil
}

// renewalState 是一个配对账户的续签材料、下次续签时刻与本机签发后尚未推送成功的会话
type renewalState struct {
	renewal  remoteRenewal
	due      time.Time
	cookies  []aistudio.StateCookie
	mintedAt time.Time
}

// renewRemote 在前台为每个配对账户各自立即以 push 签发并推送新会话，之后在会话签发 100 分钟后推送下一个，推送失败时从这次推送结束起 5 分钟后重试，远程实例已删除账户或续签凭据失效时停止该账户并删除其记录；各账户互不等待，直到 ctx 结束或没有账户
func renewRemote(ctx context.Context, renewals []remoteRenewal, output io.Writer, push func(context.Context, *renewalState) error) error {
	fmt.Fprintln(output, "保持此窗口打开，本机按时为这些账户续签")
	ctx, cancel := context.WithCancelCause(ctx)
	defer cancel(nil)
	// records 串行化各账户的输出与续签记录写入
	var records sync.Mutex
	var group sync.WaitGroup
	for _, renewal := range renewals {
		group.Go(func() {
			state := renewalState{renewal: renewal}
			for {
				err := push(ctx, &state)
				now := time.Now()
				records.Lock()
				switch {
				case ctx.Err() != nil:
					records.Unlock()
					return
				case errors.Is(err, errRenewalRevoked):
					fmt.Fprintf(output, "%s 账户 %s 停止续签: %v\n", now.Format(time.DateTime), state.renewal.Account, err)
					if err := removeRenewal(state.renewal); err != nil {
						cancel(err)
					}
					records.Unlock()
					return
				case err != nil:
					state.due = now.Add(renewalRetry)
					fmt.Fprintf(output, "%s 账户 %s 续签失败，5 分钟后重试: %v\n", now.Format(time.DateTime), state.renewal.Account, err)
				default:
					state.due = state.mintedAt.Add(renewalInterval)
					fmt.Fprintf(output, "%s 账户 %s 已续签，下次续签 %s\n", now.Format(time.DateTime), state.renewal.Account, state.due.Format(time.TimeOnly))
				}
				records.Unlock()
				select {
				case <-ctx.Done():
					return
				case <-time.After(time.Until(state.due)):
				}
			}
		})
	}
	group.Wait()
	if err := context.Cause(ctx); !errors.Is(err, context.Canceled) {
		return err
	}
	return nil
}

// pushTo 返回经 client 推送续签的函数
func pushTo(client *http.Client) func(context.Context, *renewalState) error {
	return func(ctx context.Context, state *renewalState) error { return pushRenewal(ctx, client, state) }
}

// pushRenewal 凭续签凭据把会话 Cookie 写入远程实例；state 中没有签发不到 renewalInterval、尚未推送成功的会话时先以本机 Chrome 来源与 OAuth 材料签发新会话
func pushRenewal(ctx context.Context, client *http.Client, state *renewalState) error {
	renewal := state.renewal
	if state.cookies == nil || time.Since(state.mintedAt) >= renewalInterval {
		cookies, _, err := chromeauth.Refresh(ctx, renewal.Source, renewal.OAuth, renewal.Proxy)
		if err != nil {
			return err
		}
		state.cookies, state.mintedAt = cookies, time.Now()
	}
	payload, err := json.Marshal(api.PairingRenewalInput{Cookies: state.cookies})
	if err != nil {
		return fmt.Errorf("编码 Cookie: %w", err)
	}
	address := renewal.Origin + "/api/pairing/accounts/" + url.PathEscape(renewal.Account) + "/cookies"
	request, err := http.NewRequestWithContext(ctx, http.MethodPut, address, bytes.NewReader(payload))
	if err != nil {
		return err
	}
	request.Header.Set("Authorization", "Bearer "+renewal.Token)
	request.Header.Set("Content-Type", "application/json")
	response, err := client.Do(request)
	if err != nil {
		return fmt.Errorf("连接远程实例: %w", err)
	}
	defer response.Body.Close()
	if response.StatusCode == http.StatusOK {
		state.cookies = nil
		return nil
	}
	var result struct {
		Error struct {
			Code    string `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}
	failure := fmt.Errorf("远程实例续签失败: HTTP %d", response.StatusCode)
	if json.NewDecoder(response.Body).Decode(&result) == nil && result.Error.Message != "" {
		failure = fmt.Errorf("远程实例续签失败: HTTP %d: %s", response.StatusCode, result.Error.Message)
	}
	if result.Error.Code == "pairing_renewal_invalid" || result.Error.Code == "account_not_found" {
		return fmt.Errorf("%w: %w", errRenewalRevoked, failure)
	}
	return failure
}
