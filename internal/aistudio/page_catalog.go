package aistudio

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"

	"github.com/Mag1cFall/AIStudio2API/internal/camoufoxnative"
)

// PageCatalogProvider 返回账户会话页面载入时目录 RPC 的响应：reload 为 false 时取最近一次载入，账户没有载入过页面时先载入；reload 为 true 时重新载入页面
type PageCatalogProvider interface {
	PageCatalog(ctx context.Context, accountID string, reload bool) (camoufoxnative.PageCatalog, error)
}

// loadPageCatalog 以账户会话页面载入时的响应更新条款状态、权益与模型目录
func (c *Client) loadPageCatalog(ctx context.Context, accountID string, reload bool) (BenefitTier, []Model, error) {
	page, err := c.pages.PageCatalog(ctx, accountID, reload)
	if err != nil {
		return BenefitTierFree, nil, err
	}
	return c.ApplyPageCatalog(ctx, accountID, page)
}

// ApplyPageCatalog 以账户会话页面载入时的响应更新条款状态、权益与模型目录，账户尚未同意条款时按官网条款对话框发送 AcceptTerms，本进程同意过的账户不再发送
func (c *Client) ApplyPageCatalog(ctx context.Context, accountID string, page camoufoxnative.PageCatalog) (BenefitTier, []Model, error) {
	if page.Logging == nil || page.Tier == nil || page.Models == nil {
		return BenefitTierFree, nil, errors.New("会话页面载入没有取得 GetLoggingContext、GetAiStudioBenefitTier 与 ListModels 的响应")
	}
	if err := c.acceptTerms(ctx, accountID, page.Logging); err != nil {
		return BenefitTierFree, nil, err
	}
	tier, err := decodeBenefitTier(page.Tier)
	if err != nil {
		return BenefitTierFree, nil, err
	}
	catalog, err := parseModelCatalog(bytes.NewReader(page.Models))
	if err != nil {
		return BenefitTierFree, nil, err
	}
	catalog.bundle = page.Bundle
	c.setBenefitTier(accountID, tier)
	c.setModelCatalog(accountID, catalog)
	return tier, cloneModels(catalog.models), nil
}

// acceptTerms 按页面载入时的 GetLoggingContext 响应在账户尚未同意条款时发送 AcceptTerms，并记下账户已同意
func (c *Client) acceptTerms(ctx context.Context, accountID string, logging []byte) error {
	c.termsMu.Lock()
	_, accepted := c.termsAccepted[accountID]
	c.termsMu.Unlock()
	if accepted {
		return nil
	}
	body, err := acceptTermsBody(logging)
	if err != nil {
		return err
	}
	if body != nil {
		response, err := c.do(ctx, "AcceptTerms", accountID, "", body, false)
		if err != nil {
			return err
		}
		_, err = io.Copy(io.Discard, response.Body)
		response.Body.Close()
		if err != nil {
			return fmt.Errorf("读取 AcceptTerms: %w", err)
		}
	}
	c.termsMu.Lock()
	c.termsAccepted[accountID] = struct{}{}
	c.termsMu.Unlock()
	return nil
}
