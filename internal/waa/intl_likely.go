package waa

import (
	"encoding/base64"
	"fmt"
	"strings"
	"sync"
)

// likelySubtagsData 是 Firefox ICU 的似然子标签数据（langInfo/likely）
type likelySubtagsData struct {
	Trie            string            `json:"trie"`
	LSRs            [][3]string       `json:"lsrs"`
	LanguageAliases map[string]string `json:"languageAliases"`
	RegionAliases   map[string]string `json:"regionAliases"`
	trie            []byte
	undState        bytesTrieState
	undZzzzState    bytesTrieState
	defaultIndex    int
}

// likelySubtags 读取似然子标签数据并缓存 ICU LikelySubtags 构造时求得的 trie 状态
var likelySubtags = sync.OnceValues(func() (*likelySubtagsData, error) {
	var data likelySubtagsData
	if err := readIntlFile("likely", &data); err != nil {
		return nil, fmt.Errorf("读取似然子标签数据: %w", err)
	}
	trie, err := base64.StdEncoding.DecodeString(data.Trie)
	if err != nil {
		return nil, err
	}
	data.trie = trie
	iterator := newBytesTrie(trie)
	iterator.next('*')
	data.undState = iterator.state()
	iterator.next('*')
	data.undZzzzState = iterator.state()
	iterator.next('*')
	data.defaultIndex = iterator.value()
	return &data, nil
})

// bytesTrieState 是 ICU BytesTrie 的位置与线性匹配剩余长度
type bytesTrieState struct {
	pos       int
	remaining int
}

// bytesTrie 是 ICU BytesTrie 的只读遍历器
type bytesTrie struct {
	data      []byte
	pos       int
	remaining int
}

// BytesTrie 的节点编码常量
const (
	trieMaxBranchLinear   = 5
	trieMinLinearMatch    = 0x10
	trieMinValueLead      = 0x20
	trieMinOneByteValue   = 0x10
	trieMinTwoByteValue   = 0x51
	trieMinThreeByteValue = 0x6c
	trieFourByteValue     = 0x7e
	trieMinTwoByteDelta   = 0xc0
	trieMinThreeByteDelta = 0xf0
	trieFourByteDelta     = 0xfe
)

// BytesTrie 的匹配结果
const (
	trieNoMatch = iota
	trieNoValue
	trieFinalValue
	trieIntermediateValue
)

// newBytesTrie 从 trie 开头开始遍历
func newBytesTrie(data []byte) *bytesTrie {
	return &bytesTrie{data: data, remaining: -1}
}

// state 返回当前遍历状态
func (trie *bytesTrie) state() bytesTrieState {
	return bytesTrieState{pos: trie.pos, remaining: trie.remaining}
}

// reset 恢复遍历状态
func (trie *bytesTrie) reset(state bytesTrieState) *bytesTrie {
	trie.pos, trie.remaining = state.pos, state.remaining
	return trie
}

// readValue 读取 lead 字节已右移一位的紧凑整数
func (trie *bytesTrie) readValue(pos, lead int) int {
	data := trie.data
	switch {
	case lead < trieMinTwoByteValue:
		return lead - trieMinOneByteValue
	case lead < trieMinThreeByteValue:
		return (lead-trieMinTwoByteValue)<<8 | int(data[pos])
	case lead < trieFourByteValue:
		return (lead-trieMinThreeByteValue)<<16 | int(data[pos])<<8 | int(data[pos+1])
	case lead == trieFourByteValue:
		return int(data[pos])<<16 | int(data[pos+1])<<8 | int(data[pos+2])
	}
	return int(int32(uint32(data[pos])<<24 | uint32(data[pos+1])<<16 | uint32(data[pos+2])<<8 | uint32(data[pos+3])))
}

// value 返回当前位置的值
func (trie *bytesTrie) value() int {
	return trie.readValue(trie.pos+1, int(trie.data[trie.pos])>>1)
}

// skipTrieValue 跳过 lead 字节之后的值字节
func skipTrieValue(pos, lead int) int {
	if lead >= trieMinTwoByteValue<<1 {
		switch {
		case lead < trieMinThreeByteValue<<1:
			pos++
		case lead < trieFourByteValue<<1:
			pos += 2
		default:
			pos += 3 + (lead>>1)&1
		}
	}
	return pos
}

// skipDelta 跳过跳转偏移
func (trie *bytesTrie) skipDelta(pos int) int {
	delta := int(trie.data[pos])
	pos++
	if delta >= trieMinTwoByteDelta {
		switch {
		case delta < trieMinThreeByteDelta:
			pos++
		case delta < trieFourByteDelta:
			pos += 2
		default:
			pos += 3 + delta&1
		}
	}
	return pos
}

// jumpByDelta 读取跳转偏移并跳转
func (trie *bytesTrie) jumpByDelta(pos int) int {
	data := trie.data
	delta := int(data[pos])
	pos++
	switch {
	case delta < trieMinTwoByteDelta:
	case delta < trieMinThreeByteDelta:
		delta = (delta-trieMinTwoByteDelta)<<8 | int(data[pos])
		pos++
	case delta < trieFourByteDelta:
		delta = (delta-trieMinThreeByteDelta)<<16 | int(data[pos])<<8 | int(data[pos+1])
		pos += 2
	case delta == trieFourByteDelta:
		delta = int(data[pos])<<16 | int(data[pos+1])<<8 | int(data[pos+2])
		pos += 3
	default:
		delta = int(int32(uint32(data[pos])<<24 | uint32(data[pos+1])<<16 | uint32(data[pos+2])<<8 | uint32(data[pos+3])))
		pos += 4
	}
	return pos + delta
}

// trieValueResult 返回值节点对应的匹配结果
func trieValueResult(node int) int {
	return trieIntermediateValue - node&1
}

// stop 结束遍历
func (trie *bytesTrie) stop() int {
	trie.pos = -1
	return trieNoMatch
}

// next 匹配一个字节
func (trie *bytesTrie) next(in int) int {
	pos := trie.pos
	if pos < 0 {
		return trieNoMatch
	}
	if length := trie.remaining; length >= 0 {
		if in != int(trie.data[pos]) {
			return trie.stop()
		}
		pos++
		length--
		trie.remaining, trie.pos = length, pos
		if length < 0 && int(trie.data[pos]) >= trieMinValueLead {
			return trieValueResult(int(trie.data[pos]))
		}
		return trieNoValue
	}
	data := trie.data
	for {
		node := int(data[pos])
		pos++
		switch {
		case node < trieMinLinearMatch:
			return trie.branchNext(pos, node, in)
		case node < trieMinValueLead:
			length := node - trieMinLinearMatch
			if in != int(data[pos]) {
				return trie.stop()
			}
			pos++
			length--
			trie.remaining, trie.pos = length, pos
			if length < 0 && int(data[pos]) >= trieMinValueLead {
				return trieValueResult(int(data[pos]))
			}
			return trieNoValue
		case node&1 != 0:
			return trie.stop()
		default:
			pos = skipTrieValue(pos, node)
		}
	}
}

// branchNext 在分支节点上匹配一个字节
func (trie *bytesTrie) branchNext(pos, length, in int) int {
	data := trie.data
	if length == 0 {
		length = int(data[pos])
		pos++
	}
	length++
	for length > trieMaxBranchLinear {
		if in < int(data[pos]) {
			length >>= 1
			pos = trie.jumpByDelta(pos + 1)
		} else {
			length -= length >> 1
			pos = trie.skipDelta(pos + 1)
		}
	}
	for length > 1 {
		if in == int(data[pos]) {
			pos++
			node := int(data[pos])
			result := trieFinalValue
			if node&1 == 0 {
				pos++
				delta := trie.readValue(pos, node>>1)
				switch lead := node >> 1; {
				case lead < trieMinTwoByteValue:
				case lead < trieMinThreeByteValue:
					pos++
				case lead < trieFourByteValue:
					pos += 2
				case lead == trieFourByteValue:
					pos += 3
				default:
					pos += 4
				}
				pos += delta
				node = int(data[pos])
				result = trieNoValue
				if node >= trieMinValueLead {
					result = trieValueResult(node)
				}
			}
			trie.pos, trie.remaining = pos, -1
			return result
		}
		length--
		pos = skipTrieValue(pos+2, int(data[pos+1]))
	}
	if in != int(data[pos]) {
		return trie.stop()
	}
	pos++
	trie.pos, trie.remaining = pos, -1
	if node := int(data[pos]); node >= trieMinValueLead {
		return trieValueResult(node)
	}
	return trieNoValue
}

// likelyNext 按 LikelySubtags::trieNext 匹配一个子标签，空子标签匹配 *；无匹配为 -1，无值为 0，SKIP_SCRIPT 为 1
func likelyNext(trie *bytesTrie, subtag string) int {
	var result int
	if subtag == "" {
		result = trie.next('*')
	} else {
		for index := 0; index < len(subtag); index++ {
			if index+1 < len(subtag) {
				if trie.next(int(subtag[index]))&1 == 0 {
					return -1
				}
				continue
			}
			result = trie.next(int(subtag[index]) | 0x80)
		}
	}
	switch result {
	case trieNoValue:
		return 0
	case trieIntermediateValue:
		return 1
	case trieFinalValue:
		return trie.value()
	}
	return -1
}

// likelyMacroregions 是 ICU LikelySubtags 硬编码的宏区域
var likelyMacroregions = map[string]bool{
	"001": true, "002": true, "003": true, "005": true, "009": true, "011": true, "013": true, "014": true, "015": true,
	"017": true, "018": true, "019": true, "021": true, "029": true, "030": true, "034": true, "035": true, "039": true,
	"053": true, "054": true, "057": true, "061": true, "142": true, "143": true, "145": true, "150": true, "151": true,
	"154": true, "155": true, "202": true, "419": true, "EU": true, "EZ": true, "QO": true, "UN": true,
}

// likelyMaximize 按 ICU LikelySubtags::maximize 补全语言、书写系统与地区，未匹配且 returnInputIfUnmatch 时返回三个空串
func likelyMaximize(language, script, region string, returnInputIfUnmatch bool) [3]string {
	data, err := likelySubtags()
	if err != nil {
		panic(err)
	}
	if language == "und" {
		language = ""
	}
	if script == "Zzzz" {
		script = ""
	}
	if region == "ZZ" {
		region = ""
	}
	if script != "" && region != "" && language != "" {
		return [3]string{language, script, region}
	}
	retainLanguage, retainScript, retainRegion := false, false, false
	iterator := newBytesTrie(data.trie)
	var state bytesTrieState
	hasState := false
	value := likelyNext(iterator, language)
	matchLanguage := value >= 0
	matchScript := false
	if value >= 0 {
		retainLanguage = language != ""
		state, hasState = iterator.state(), true
	} else {
		retainLanguage = true
		iterator.reset(data.undState)
	}
	if value >= 0 && script != "" {
		matchScript = true
	}
	if value > 0 {
		if value == 1 {
			value = 0
		}
		retainScript = script != ""
	} else {
		value = likelyNext(iterator, script)
		if value >= 0 {
			retainScript = script != ""
			state, hasState = iterator.state(), true
		} else {
			retainScript = true
			if !hasState {
				iterator.reset(data.undZzzzState)
			} else {
				iterator.reset(state)
				value = likelyNext(iterator, "")
				state = iterator.state()
			}
		}
	}
	matchRegion := false
	if value > 0 {
		retainRegion = region != ""
	} else {
		value = likelyNext(iterator, region)
		if value >= 0 {
			if region != "" && !likelyMacroregions[region] {
				retainRegion = true
				matchRegion = true
			}
		} else {
			retainRegion = true
			if !hasState {
				value = data.defaultIndex
			} else {
				iterator.reset(state)
				value = likelyNext(iterator, "")
				if value < 0 {
					retainLanguage = language != ""
					retainScript = script != ""
					retainRegion = region != ""
					iterator.reset(data.undState)
					likelyNext(iterator, "")
					empty := iterator.state()
					value = likelyNext(iterator, region)
					if value < 0 {
						iterator.reset(empty)
						value = likelyNext(iterator, "")
					}
				}
			}
		}
	}
	if returnInputIfUnmatch && !(matchLanguage || matchScript || matchRegion && language == "") {
		return [3]string{}
	}
	if language == "" {
		language = "und"
	}
	matched := data.LSRs[value]
	if !retainLanguage {
		language = matched[0]
	}
	if !retainScript {
		script = matched[1]
	}
	if !retainRegion {
		region = matched[2]
	}
	return [3]string{language, script, region}
}

// likelyAdd 按 uloc_addLikelySubtags 补全语言、书写系统与地区
func likelyAdd(language, script, region string) [3]string {
	data, _ := likelySubtags()
	if alias, ok := data.LanguageAliases[language]; ok {
		language = alias
	}
	if alias, ok := data.RegionAliases[region]; ok {
		region = alias
	}
	maximized := likelyMaximize(language, script, region, true)
	if maximized == [3]string{} {
		return [3]string{language, script, region}
	}
	return maximized
}

// likelyMinimize 按 uloc_minimizeSubtags（优先地区）去除似然子标签
func likelyMinimize(language, script, region string) [3]string {
	maximized := likelyMaximize(language, script, region, true)
	if maximized == [3]string{} {
		return [3]string{language, script, region}
	}
	if likelyMaximize(maximized[0], "", "", true) == maximized {
		return [3]string{maximized[0], "", ""}
	}
	if likelyMaximize(maximized[0], "", maximized[2], true) == maximized {
		return [3]string{maximized[0], "", maximized[2]}
	}
	if likelyMaximize(maximized[0], maximized[1], "", true) == maximized {
		return [3]string{maximized[0], maximized[1], ""}
	}
	return maximized
}

// spoofedLikelyTag 按 Camoufox 读取账户语言与地区的区域设置伪装返回 Intl.Locale maximize 或 minimize 的基础标签
func spoofedLikelyTag(tag, account string, maximize bool) string {
	parts := strings.Split(tag, "-")
	script, region := "", ""
	rest := parts[1:]
	if len(rest) > 0 && len(rest[0]) == 4 && rest[0][0] > '9' {
		script, rest = rest[0], rest[1:]
	}
	if len(rest) > 0 && (len(rest[0]) == 2 || len(rest[0]) == 3 && rest[0][0] >= '0' && rest[0][0] <= '9') {
		region, rest = rest[0], rest[1:]
	}
	accountParts := strings.Split(account, "-")
	language := accountParts[0]
	for _, part := range accountParts[1:] {
		if len(part) == 2 || len(part) == 3 && part[0] >= '0' && part[0] <= '9' {
			region = part
		}
	}
	if maximize && language != "und" && script != "" && script != "Zzzz" && region != "" && region != "ZZ" {
		return tag
	}
	var result [3]string
	if maximize {
		result = likelyAdd(language, script, region)
	} else {
		result = likelyMinimize(language, script, region)
	}
	if region == "" {
		region = result[2]
	}
	out := []string{language}
	if result[1] != "" {
		out = append(out, result[1])
	}
	if region != "" {
		out = append(out, region)
	}
	return strings.Join(append(out, rest...), "-")
}
