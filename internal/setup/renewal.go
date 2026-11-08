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
	"time"

	"github.com/Mag1cFall/AIStudio2API/internal/aistudio"
	"github.com/Mag1cFall/AIStudio2API/internal/api"
	"github.com/Mag1cFall/AIStudio2API/internal/chromeauth"
)

// renewalInterval 是为配对账户推送新会话的间隔，短于会话签发后约 2 小时的有效期
const renewalInterval = 100 * time.Minute

// renewalRetry 是推送失败后的重试间隔
const renewalRetry = 5 * time.Minute

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
	data, err := json.MarshalIndent(append(kept, added...), "", "  ")
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

// renewRemote 在前台为配对账户立即签发并推送新会话，之后每 100 分钟一次，失败 5 分钟后重试，直到 ctx 结束
func renewRemote(ctx context.Context, client *http.Client, renewals []remoteRenewal, output io.Writer) error {
	fmt.Fprintln(output, "保持此窗口打开，本机按时为这些账户续签")
	due := make([]time.Time, len(renewals))
	for {
		next := time.Time{}
		for index, renewal := range renewals {
			now := time.Now()
			if !now.Before(due[index]) {
				if err := pushRenewal(ctx, client, renewal); err != nil {
					if ctx.Err() != nil {
						return nil
					}
					due[index] = now.Add(renewalRetry)
					fmt.Fprintf(output, "%s 账户 %s 续签失败，5 分钟后重试: %v\n", now.Format(time.DateTime), renewal.Account, err)
				} else {
					due[index] = now.Add(renewalInterval)
					fmt.Fprintf(output, "%s 账户 %s 已续签，下次续签 %s\n", now.Format(time.DateTime), renewal.Account, due[index].Format(time.TimeOnly))
				}
			}
			if next.IsZero() || due[index].Before(next) {
				next = due[index]
			}
		}
		select {
		case <-ctx.Done():
			return nil
		case <-time.After(time.Until(next)):
		}
	}
}

// pushRenewal 以本机 Chrome 来源与 OAuth 材料签发新会话，凭续签凭据写入远程实例
func pushRenewal(ctx context.Context, client *http.Client, renewal remoteRenewal) error {
	cookies, _, err := chromeauth.Refresh(ctx, renewal.Source, renewal.OAuth, renewal.Proxy)
	if err != nil {
		return err
	}
	payload, err := json.Marshal(api.PairingRenewalInput{Cookies: cookies})
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
		return nil
	}
	var result struct {
		Error struct {
			Message string `json:"message"`
		} `json:"error"`
	}
	if json.NewDecoder(response.Body).Decode(&result) == nil && result.Error.Message != "" {
		return fmt.Errorf("远程实例续签失败: HTTP %d: %s", response.StatusCode, result.Error.Message)
	}
	return fmt.Errorf("远程实例续签失败: HTTP %d", response.StatusCode)
}
