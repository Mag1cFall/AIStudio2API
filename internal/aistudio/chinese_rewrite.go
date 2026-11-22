package aistudio

import (
	_ "embed"
	"slices"
	"strings"
	"sync"

	"github.com/go-ego/gocc"
)

const (
	userMessageRewriteOff         = "off"
	userMessageRewriteTraditional = "traditional"
	userMessageRewriteMartian     = "martian"
)

// userMessageRewriteReplyInstruction 是用户消息改写开启时追加到系统指令的回复语言要求，繁体中文与火星文模式共用
const userMessageRewriteReplyInstruction = "User messages may be converted to Traditional Chinese or Martian Chinese (Huoxingwen) for transmission. Understand their intended meaning and always write your reply in Simplified Chinese, ignoring the converted characters in user messages, unless the user explicitly asks you to reply in another language or script."

//go:embed martian_table.tsv
var martianTableTSV string

// openCCS2T 懒初始化用于简体转繁体的 OpenCC s2t 转换器
var openCCS2T = sync.OnceValues(func() (*gocc.Converter, error) {
	return gocc.New("s2t")
})

// martianTable 懒初始化由自制火星文码表构建的只读字符映射，每行记录同一原字的简繁变体及其唯一替换字
var martianTable = sync.OnceValue(func() map[rune]rune {
	lines := strings.Split(strings.TrimRight(martianTableTSV, "\r\n"), "\n")
	table := make(map[rune]rune, len(lines)*2)
	for _, line := range lines {
		line = strings.TrimRight(line, "\r")
		sources, targetText, ok := strings.Cut(line, "\t")
		if !ok || sources == "" || targetText == "" {
			continue
		}
		target := []rune(targetText)[0]
		for _, r := range sources {
			table[r] = target
		}
	}
	return table
})

// convertMartian 先经 OpenCC s2t 消歧，再按火星文码表逐字替换，表外生僻字保留繁体且不改动 ASCII 与标点
func convertMartian(text string, converter *gocc.Converter) string {
	converted := converter.Convert(text)
	table := martianTable()
	var builder strings.Builder
	for i, r := range converted {
		mapped, ok := table[r]
		if !ok || mapped == r {
			if builder.Len() > 0 {
				builder.WriteRune(r)
			}
			continue
		}
		if builder.Len() == 0 {
			builder.Grow(len(converted))
			builder.WriteString(converted[:i])
		}
		builder.WriteRune(mapped)
	}
	if builder.Len() > 0 {
		return builder.String()
	}
	return converted
}

// chineseRewriteEligible 判断本次生成是否启用用户消息文字改写
func chineseRewriteEligible(request GenerateRequest, entry modelEntry) bool {
	switch request.UserMessageRewrite {
	case userMessageRewriteTraditional, userMessageRewriteMartian:
		return !nonTextGeneration(request, entry)
	default:
		return false
	}
}

// applyChineseRewrite 按配置模式改写用户消息中的中文并追加简体中文回复系统指令，保留模型历史回答、非文本块与调用方入参
func applyChineseRewrite(request GenerateRequest) (GenerateRequest, error) {
	converter, err := openCCS2T()
	if err != nil {
		return request, err
	}
	convert := converter.Convert
	if request.UserMessageRewrite == userMessageRewriteMartian {
		convert = func(text string) string {
			return convertMartian(text, converter)
		}
	}
	var contents []Content
	for index, content := range request.Contents {
		if content.Role != RoleUser {
			continue
		}
		var parts []Part
		for partIndex, part := range content.Parts {
			if part.Text == "" || part.Thought {
				continue
			}
			converted := convert(part.Text)
			if converted == part.Text {
				continue
			}
			if parts == nil {
				parts = slices.Clone(content.Parts)
			}
			parts[partIndex].Text = converted
		}
		if parts != nil {
			if contents == nil {
				contents = slices.Clone(request.Contents)
			}
			contents[index].Parts = parts
		}
	}
	if contents != nil {
		request.Contents = contents
	}
	instruction := userMessageRewriteReplyInstruction
	if len(request.System) > 0 {
		instruction = "\n" + instruction
	}
	request.System = appendSystem(request.System, instruction)
	return request, nil
}
