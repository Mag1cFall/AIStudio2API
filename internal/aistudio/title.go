package aistudio

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
)

// TitleRequest 是试用页按提示词与回答生成页头标题的请求，Model 为本次运行的模型
type TitleRequest struct {
	Model  string
	Prompt string
	Answer string
}

// TitleService 生成试用页页头标题
type TitleService interface {
	GenerateTitle(context.Context, TitleRequest) (string, error)
}

// TitleProtectedTransport 原子完成 GenerateTitle fresh WAA proof、field 3 写入和同 context 发送
type TitleProtectedTransport interface {
	DoProtectedTitle(context.Context, TitleRequest, RPCRequest) (*RPCResponse, error)
}

// GenerateTitle 以可调用本次运行模型的账户生成页头标题
func (s *PooledService) GenerateTitle(ctx context.Context, request TitleRequest) (string, error) {
	request.Model = strings.TrimPrefix(strings.TrimSpace(request.Model), "models/")
	lease, owned, err := resolveAccountLease(ctx, s.pool, AccountSelection{ModelID: request.Model})
	if err != nil {
		return "", err
	}
	title, err := s.client.GenerateTitle(ContextWithAccountLease(ctx, lease), lease.Account().ID, request)
	if owned {
		err = errors.Join(err, lease.Release())
	}
	return title, err
}

// GenerateTitle 按官网试用页发送 GenerateTitle：正文为提示词、回答与 WAA proof，没有回答时回答位为 null
func (c *Client) GenerateTitle(ctx context.Context, accountID string, request TitleRequest) (string, error) {
	var answer any
	if request.Answer != "" {
		answer = request.Answer
	}
	body, _ := json.Marshal([]any{request.Prompt, answer, nil})
	rpc := newRPCRequest("GenerateTitle", accountID, "", body, false)
	c.applyBenefitTier(rpc.Method, accountID, rpc.Header)
	response, err := c.protected.(TitleProtectedTransport).DoProtectedTitle(ctx, request, rpc)
	if err != nil {
		return "", fmt.Errorf("发送 AI Studio GenerateTitle: %w", err)
	}
	response, err = validateRPCResponse("GenerateTitle", response)
	if err != nil {
		return "", err
	}
	defer response.Body.Close()
	var title []string
	if err := json.NewDecoder(response.Body).Decode(&title); err != nil {
		return "", fmt.Errorf("解析 AI Studio GenerateTitle 响应: %w", err)
	}
	if len(title) == 0 {
		return "", fmt.Errorf("AI Studio GenerateTitle 响应没有标题")
	}
	return title[0], nil
}
