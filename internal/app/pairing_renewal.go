package app

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"github.com/Mag1cFall/AIStudio2API/internal/aistudio"
	"github.com/Mag1cFall/AIStudio2API/internal/api"
)

// pairingRenewalName 是账户目录中保存配对续签凭据摘要与最近续签时间的文件
const pairingRenewalName = "pairing-renewal.json"

// pairingRenewal 是远程配对账户的续签凭据摘要与最近一次续签时间
type pairingRenewal struct {
	TokenSHA256 string    `json:"token_sha256"`
	RenewedAt   time.Time `json:"renewed_at,omitzero"`
}

// pairingTokenDigest 返回续签凭据的 SHA-256 十六进制摘要
func pairingTokenDigest(token string) string {
	digest := sha256.Sum256([]byte(token))
	return hex.EncodeToString(digest[:])
}

// readPairingRenewal 读取账户目录中的配对续签记录
func readPairingRenewal(directory string) (pairingRenewal, error) {
	var renewal pairingRenewal
	data, err := os.ReadFile(filepath.Join(directory, pairingRenewalName))
	if err != nil {
		return renewal, err
	}
	return renewal, json.Unmarshal(data, &renewal)
}

// writePairingRenewal 写入账户目录中的配对续签记录
func writePairingRenewal(directory string, renewal pairingRenewal) error {
	data, err := json.Marshal(renewal)
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(directory, pairingRenewalName), data, 0o600)
}

// pairingRenewedAt 返回账户目录记录的最近一次配对续签时间，没有续签过时为 nil
func pairingRenewedAt(directory string) *time.Time {
	renewal, err := readPairingRenewal(directory)
	if err != nil || renewal.RenewedAt.IsZero() {
		return nil
	}
	return &renewal.RenewedAt
}

// savePairingRenewal 为配对上传的账户保存续签凭据摘要
func (admin *runtimeAdmin) savePairingRenewal(accountID string, token string) error {
	account, err := admin.pool.Account(accountID)
	if err != nil {
		return err
	}
	return writePairingRenewal(account.Directory, pairingRenewal{TokenSHA256: pairingTokenDigest(token)})
}

// RenewAccountCookies 凭配对上传时签发的续签凭据把原电脑签发的新会话 Cookie 写入账户，账户随后回到可调度状态
func (admin *runtimeAdmin) RenewAccountCookies(ctx context.Context, accountID string, token string, cookies []aistudio.StateCookie) (api.AdminAccount, error) {
	account, err := admin.pool.Account(accountID)
	if err != nil {
		return api.AdminAccount{}, accountOperationError(err)
	}
	renewal, err := readPairingRenewal(account.Directory)
	if err != nil || subtle.ConstantTimeCompare([]byte(pairingTokenDigest(token)), []byte(renewal.TokenSHA256)) != 1 {
		return api.AdminAccount{}, &adminOperationError{
			status: http.StatusUnauthorized, code: "pairing_renewal_invalid", message: "Pairing renewal token is invalid",
		}
	}
	lease, err := admin.pool.AcquireAccount(ctx, account.ID)
	if err != nil {
		return api.AdminAccount{}, accountOperationError(err)
	}
	state := lease.Account().StorageState
	state.Cookies = cookies
	if _, err := aistudio.NewSigner().Authorization(state); err != nil {
		return api.AdminAccount{}, errors.Join(invalidAccount(fmt.Errorf("认证状态无法用于 AI Studio: %w", err)), lease.Release())
	}
	if err := admin.workers.Reset(account.ID); err != nil {
		return api.AdminAccount{}, errors.Join(err, lease.Release())
	}
	if err := lease.SaveStorageState(state); err != nil {
		return api.AdminAccount{}, errors.Join(err, lease.Release())
	}
	if err := admin.service.changeModels(func() error { return admin.pool.MarkReady(account.ID) }); err != nil {
		return api.AdminAccount{}, errors.Join(err, lease.Release())
	}
	renewal.RenewedAt = time.Now().UTC()
	if err := writePairingRenewal(account.Directory, renewal); err != nil {
		return api.AdminAccount{}, errors.Join(err, lease.Release())
	}
	if err := lease.Release(); err != nil {
		return api.AdminAccount{}, err
	}
	admin.requests.log(account.Config.Label, "INFO", "账户由配对电脑续签")
	return admin.account(account.ID)
}
