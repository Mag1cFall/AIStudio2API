package aistudio

import (
	"regexp"
	"slices"
	"strings"
)

// clientIdentity 匹配客户端系统提示中行首带归属声明的身份句：You are 加以 agent、assistant 或 AI 结尾的身份短语，8 个字符内接 created by、built on、based on 等归属声明，到句号为止，句号后的空格、Tab 或 CR 保留
var clientIdentity = regexp.MustCompile(`(?im)^([ \t]*)(?:you are|you're)\s+(?:an?\s+)?(?:[\w'-]+[\s,]+){0,8}(?:agent|assistant|ai)\b[^.!?\n<>]{0,8}?\b(?:(?:created|built|made|developed|designed|trained|powered|maintained|published|released|provided)\s+(?:by|on|upon)|based\s+on)\s+[^\n<>]{1,80}?\.([ \t\r]|$)`)

// neutralIdentity 为替换客户端身份句的中性身份句
const neutralIdentity = "You are an AI Agent."

// billingPrefix 为 Claude Code 计费行的开头
const billingPrefix = "x-anthropic-billing-header:"

// rfc2119Keywords 匹配 RFC 2119 keywords 写法，不区分大小写
var rfc2119Keywords = regexp.MustCompile(`(?i)RFC 2119 keywords`)

// clientSystem 逐段改写客户端系统提示，改写后变空的段落不再发送
func clientSystem(system []string) []string {
	var rewritten []string
	for _, text := range system {
		if text = clientSystemText(text); text != "" {
			rewritten = append(rewritten, text)
		}
	}
	return rewritten
}

// clientSystemText 改写一段客户端系统提示：行首带归属声明的身份句换成中性身份句，Claude Code 计费行与含 RFC 2119 keywords 的行整行删除
func clientSystemText(text string) string {
	lines := slices.DeleteFunc(strings.Split(text, "\n"), func(line string) bool {
		return strings.HasPrefix(line, billingPrefix) || rfc2119Keywords.MatchString(line)
	})
	return clientIdentity.ReplaceAllString(strings.Join(lines, "\n"), "${1}"+neutralIdentity+"${2}")
}
