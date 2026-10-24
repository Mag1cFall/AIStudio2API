package waa

import (
	"encoding/binary"
	"fmt"
	"math"
	"strings"
	"sync"

	"golang.org/x/text/language"
)

// segmentText 按 Firefox 152 的 ICU4X 分段器切分 UTF-16 文本，返回各段起点（UTF-16 下标，首个为 0，不含末尾长度）与 word 粒度下各段是否 word-like
func segmentText(granularity, locale string, text []uint16) (starts []int, wordLike []bool) {
	rules, err := segmentRuleData()
	if err != nil {
		panic(err)
	}
	var it *segmentIterator
	switch granularity {
	case "grapheme":
		it = newSegmentIterator(rules.grapheme, nil, false, text)
	case "word":
		it = newSegmentIterator(rules.word, segmentOverride(rules.word.overrides, locale), true, text)
	case "sentence":
		it = newSegmentIterator(rules.sentence, segmentOverride(rules.sentence.overrides, locale), false, text)
	}
	index, previous := 0, 0
	for index < len(text) {
		next, ok := it.next()
		if !ok {
			next = -1
		}
		if next > index {
			starts = append(starts, previous)
			if granularity == "word" {
				wordLike = append(wordLike, it.wordType() != segmentWordNone)
			}
			index = next
		}
		previous = next
	}
	return starts, wordLike
}

// segmentTrie 是 Small 类型的 ICU4X CodePointTrie<u8>
type segmentTrie struct {
	highStart uint32
	index     []uint16
	data      []uint8
}

// get 返回码位的属性值（icu_collections CodePointTrie::get32）
func (t *segmentTrie) get(c uint32) uint8 {
	if c <= 0xfff {
		return t.data[uint32(t.index[c>>6])+c&0x3f]
	}
	if c >= t.highStart {
		return t.data[len(t.data)-2]
	}
	i3Block := uint32(t.index[uint32(t.index[c>>14+0x40])+(c>>9)&0x1f])
	i3Pos := (c >> 4) & 0x1f
	var dataBlock uint32
	if i3Block&0x8000 == 0 {
		dataBlock = uint32(t.index[i3Block+i3Pos])
	} else {
		i3Block = (i3Block & 0x7fff) + (i3Pos &^ 7) + (i3Pos >> 3)
		i3Pos &= 7
		dataBlock = (uint32(t.index[i3Block]) << (2 + 2*i3Pos)) & 0x30000
		i3Block++
		dataBlock |= uint32(t.index[i3Block+i3Pos])
	}
	return t.data[dataBlock+c&0xf]
}

// segmentRules 是 ICU4X RuleBreakData 与按区域设置键取的 override 属性表
type segmentRules struct {
	propertyCount         uint8
	lastCodepointProperty uint8
	sotProperty           uint8
	eotProperty           uint8
	complexProperty       uint8
	property              *segmentTrie
	breakState            []uint8
	wordType              []uint8
	overrides             map[string]*segmentTrie
}

// segmentRuleSets 是 grapheme、word 与 sentence 三种粒度的规则数据
type segmentRuleSets struct {
	grapheme *segmentRules
	word     *segmentRules
	sentence *segmentRules
}

// segmentReader 读取 build-segmenter.mjs 生成的小端二进制数据
type segmentReader struct {
	data []byte
}

// u8 读取一个字节
func (r *segmentReader) u8() uint8 {
	v := r.data[0]
	r.data = r.data[1:]
	return v
}

// u16 读取小端 uint16
func (r *segmentReader) u16() uint16 {
	v := binary.LittleEndian.Uint16(r.data)
	r.data = r.data[2:]
	return v
}

// u32 读取小端 uint32
func (r *segmentReader) u32() uint32 {
	v := binary.LittleEndian.Uint32(r.data)
	r.data = r.data[4:]
	return v
}

// bytes 读取以 uint32 长度开头的字节串
func (r *segmentReader) bytes() []byte {
	n := r.u32()
	v := r.data[:n]
	r.data = r.data[n:]
	return v
}

// str 读取以单字节长度开头的 UTF-8 字符串
func (r *segmentReader) str() string {
	n := r.u8()
	v := string(r.data[:n])
	r.data = r.data[n:]
	return v
}

// segmentUint16s 把小端字节解码为 uint16 数组
func segmentUint16s(raw []byte) []uint16 {
	out := make([]uint16, len(raw)/2)
	for i := range out {
		out[i] = binary.LittleEndian.Uint16(raw[2*i:])
	}
	return out
}

// trie 读取 CodePointTrie
func (r *segmentReader) trie() *segmentTrie {
	return &segmentTrie{highStart: r.u32(), index: segmentUint16s(r.bytes()), data: r.bytes()}
}

// readSegmentFile 解压 intl/segmenter 目录中的分段数据
func readSegmentFile(name string) (*segmentReader, error) {
	raw, err := intlFiles.ReadFile("intl/segmenter/" + name + ".bin.gz")
	if err != nil {
		return nil, fmt.Errorf("读取分段数据 %s: %w", name, err)
	}
	data, err := gunzip(raw)
	if err != nil {
		return nil, fmt.Errorf("解压分段数据 %s: %w", name, err)
	}
	return &segmentReader{data: data}, nil
}

// readSegmentRules 读取一种粒度的规则数据
func readSegmentRules(name string) (*segmentRules, error) {
	r, err := readSegmentFile(name)
	if err != nil {
		return nil, err
	}
	rules := &segmentRules{
		propertyCount:         r.u8(),
		lastCodepointProperty: r.u8(),
		sotProperty:           r.u8(),
		eotProperty:           r.u8(),
		complexProperty:       r.u8(),
	}
	rules.property = r.trie()
	rules.breakState = r.bytes()
	rules.wordType = r.bytes()
	rules.overrides = map[string]*segmentTrie{}
	for n := r.u8(); n > 0; n-- {
		key := r.str()
		rules.overrides[key] = r.trie()
	}
	return rules, nil
}

// segmentRuleData 读取三种粒度的规则数据，各 goroutine 共享
var segmentRuleData = sync.OnceValues(func() (*segmentRuleSets, error) {
	var sets segmentRuleSets
	var err error
	if sets.grapheme, err = readSegmentRules("grapheme"); err != nil {
		return nil, err
	}
	if sets.word, err = readSegmentRules("word"); err != nil {
		return nil, err
	}
	if sets.sentence, err = readSegmentRules("sentence"); err != nil {
		return nil, err
	}
	return &sets, nil
})

// segmentDictionary 读取中日文词典 cjdict 的 Char16Trie 数据，各 goroutine 共享
var segmentDictionary = sync.OnceValues(func() ([]uint16, error) {
	r, err := readSegmentFile("cjdict")
	if err != nil {
		return nil, err
	}
	return segmentUint16s(r.data), nil
})

// segmentOverride 按 ICU4X 语言优先的区域设置回退链为 content locale 选择 override 属性表
func segmentOverride(overrides map[string]*segmentTrie, locale string) *segmentTrie {
	if trie, ok := overrides[locale]; ok {
		return trie
	}
	base, rawScript, rawRegion := language.Make(locale).Raw()
	lang := base.String()
	script, region := "", ""
	if rawScript != (language.Script{}) {
		script = rawScript.String()
	}
	if rawRegion != (language.Region{}) {
		region = rawRegion.String()
	}
	langScript := segmentLikelyScript(lang)
	if script != "" {
		defaultScript := langScript
		if region != "" {
			defaultScript = segmentLikelyScript(lang + "-" + region)
		}
		if script == defaultScript {
			script = ""
		}
	}
	for {
		key := lang
		if script != "" {
			key += "-" + script
		}
		if region != "" {
			key += "-" + region
		}
		if trie, ok := overrides[key]; ok {
			return trie
		}
		if region == "" {
			return nil
		}
		if script == "" {
			if regionScript := segmentLikelyScript(lang + "-" + region); regionScript != langScript {
				script = regionScript
			}
		}
		region = ""
	}
}

// segmentLikelyScript 返回 CLDR 似然子标签给出的书写系统
func segmentLikelyScript(tag string) string {
	script, _ := language.Make(tag).Script()
	return script.String()
}

// segmentWordType 是 ICU4X WordType
type segmentWordType uint8

const (
	segmentWordNone   segmentWordType = 0
	segmentWordLetter segmentWordType = 2
)

// ICU4X BreakState 在断点状态表中的编码
const (
	// segmentIntermediate 是 Intermediate 状态的编码起点，更小的值为 Index 状态
	segmentIntermediate = 120
	// segmentBreak 是 Break 状态
	segmentBreak = 253
	// segmentNoMatch 是 NoMatch 状态
	segmentNoMatch = 254
	// segmentKeep 是 Keep 状态
	segmentKeep = 255
)

// segmentCursor 是 ICU4X Utf16Indices，按码位遍历 UTF-16，孤立代理项原样作为码位
type segmentCursor struct {
	text  []uint16
	front int
}

// next 返回下一个码位的起点与码位
func (c *segmentCursor) next() (int, uint32, bool) {
	if c.front >= len(c.text) {
		return 0, 0, false
	}
	index := c.front
	ch := uint32(c.text[index])
	c.front++
	if ch&0xfc00 != 0xd800 {
		return index, ch, true
	}
	if c.front < len(c.text) {
		if next := uint32(c.text[c.front]); next&0xfc00 == 0xdc00 {
			ch = (ch&0x3ff)<<10 + next&0x3ff + 0x10000
			c.front++
		}
	}
	return index, ch, true
}

// segmentCharLen 是码位的 UTF-16 码元数
func segmentCharLen(c uint32) int {
	if c >= 0x10000 {
		return 2
	}
	return 1
}

// segmentPosition 是 RuleBreakIterator 的 iter 与 current_pos_data
type segmentPosition struct {
	iter segmentCursor
	pos  int
	ch   uint32
	ok   bool
}

// segmentIterator 是 ICU4X RuleBreakIterator 的 UTF-16 实现
type segmentIterator struct {
	at       segmentPosition
	length   int
	cache    []int
	rules    *segmentRules
	override *segmentTrie
	complex  bool
	boundary uint8
}

// newSegmentIterator 创建规则分段迭代器，complex 为 word 粒度的复杂文字处理
func newSegmentIterator(rules *segmentRules, override *segmentTrie, complex bool, text []uint16) *segmentIterator {
	return &segmentIterator{
		at:       segmentPosition{iter: segmentCursor{text: text}},
		length:   len(text),
		rules:    rules,
		override: override,
		complex:  complex,
	}
}

// advance 前进一个码位
func (it *segmentIterator) advance() {
	it.at.pos, it.at.ch, it.at.ok = it.at.iter.next()
}

// property 返回码位的断点属性，override 非零时优先
func (it *segmentIterator) property(c uint32) uint8 {
	if it.override != nil {
		if p := it.override.get(c); p != 0 {
			return p
		}
	}
	return it.rules.property.get(c)
}

// currentProperty 返回当前码位的断点属性
func (it *segmentIterator) currentProperty() (uint8, bool) {
	if !it.at.ok {
		return 0, false
	}
	return it.property(it.at.ch), true
}

// state 查断点状态表
func (it *segmentIterator) state(left, right uint8) uint8 {
	return it.rules.breakState[int(left)*int(it.rules.propertyCount)+int(right)]
}

// wordType 返回刚返回的边界之前一段的词类（ICU4X RuleBreakIterator::word_type）
func (it *segmentIterator) wordType() segmentWordType {
	if len(it.cache) > 0 {
		return segmentWordLetter
	}
	if it.boundary == 0 {
		return segmentWordNone
	}
	return segmentWordType(it.rules.wordType[it.boundary-1])
}

// next 返回下一个边界（ICU4X RuleBreakIterator::next）
func (it *segmentIterator) next() (int, bool) {
	if len(it.cache) > 0 {
		first := it.cache[0]
		i := 0
		for {
			if i == first {
				it.cache = shiftSegmentBreaks(it.cache[1:], i)
				return it.at.pos, it.at.ok
			}
			if it.at.ok {
				i += segmentCharLen(it.at.ch)
			}
			it.advance()
			if !it.at.ok {
				it.cache = it.cache[:0]
				it.boundary = it.rules.complexProperty
				return it.length, true
			}
		}
	}

	if !it.at.ok {
		it.advance()
		if !it.at.ok && it.length == 0 {
			it.length = 1
			return 0, true
		}
		right, ok := it.currentProperty()
		if !ok {
			it.boundary = 0
			return 0, false
		}
		if s := it.state(it.rules.sotProperty, right); s == segmentBreak || s == segmentNoMatch {
			it.boundary = 0
			return it.at.pos, true
		}
	}

outer:
	for {
		left := it.at.ch
		leftProp := it.property(left)
		it.advance()
		right, ok := it.currentProperty()
		if !ok {
			it.boundary = leftProp
			return it.length, true
		}
		if right == it.rules.complexProperty {
			if leftProp != it.rules.complexProperty {
				it.boundary = leftProp
				return it.at.pos, true
			}
			if it.complex {
				return it.handleComplex(left), true
			}
		}

		s := it.state(leftProp, right)
		switch s {
		case segmentKeep:
			continue
		case segmentBreak, segmentNoMatch:
			it.boundary = leftProp
			return it.at.pos, true
		}
		index := segmentStateIndex(s)
		previous := it.at
		previousLeft := leftProp
		for {
			it.advance()
			prop, ok := it.currentProperty()
			if !ok {
				it.boundary = index
				if it.state(index, it.rules.eotProperty) == segmentNoMatch {
					it.boundary = previousLeft
					it.at = previous
					return it.at.pos, it.at.ok
				}
				return it.length, true
			}
			wasCodepointProperty := index <= it.rules.lastCodepointProperty
			switch s := it.state(index, prop); {
			case s == segmentKeep:
				continue outer
			case s == segmentNoMatch:
				it.boundary = previousLeft
				it.at = previous
				return it.at.pos, it.at.ok
			case s == segmentBreak:
				return it.at.pos, true
			case s >= segmentIntermediate:
				index = s - segmentIntermediate
				if wasCodepointProperty {
					previousLeft = index
				}
				previous = it.at
			default:
				index = s
				if wasCodepointProperty {
					previous = it.at
					previousLeft = index
				}
			}
		}
	}
}

// segmentStateIndex 返回 Index 或 Intermediate 状态携带的下标
func segmentStateIndex(s uint8) uint8 {
	if s >= segmentIntermediate {
		return s - segmentIntermediate
	}
	return s
}

// shiftSegmentBreaks 把缓存的断点改为相对当前位置
func shiftSegmentBreaks(breaks []int, by int) []int {
	for k := range breaks {
		breaks[k] -= by
	}
	return breaks
}

// handleComplex 把连续的复杂文字交给词典或 LSTM 分段（ICU4X word.rs Utf16::word_handle_complex_language）
func (it *segmentIterator) handleComplex(left uint32) int {
	start := it.at
	s := []uint16{uint16(left)}
	for {
		s = append(s, uint16(it.at.ch))
		it.advance()
		if prop, ok := it.currentProperty(); !ok || prop != it.rules.complexProperty {
			break
		}
	}
	it.at = start
	it.cache = segmentComplexBreaks(s)
	first := it.cache[0]
	for i := 1; ; i++ {
		if i == first {
			it.cache = shiftSegmentBreaks(it.cache[1:], i)
			return it.at.pos
		}
		it.advance()
		if !it.at.ok {
			it.cache = it.cache[:0]
			return it.length
		}
	}
}

// segmentLanguage 是 ICU4X 复杂文字分段的语言
type segmentLanguage uint8

const (
	segmentUnknown segmentLanguage = iota
	segmentBurmese
	segmentChineseOrJapanese
	segmentKhmer
	segmentLao
	segmentThai
)

// segmentLanguageOf 按码位范围判断语言（ICU4X language.rs get_language）
func segmentLanguageOf(c uint32) segmentLanguage {
	switch {
	case c >= 0xe01 && c <= 0xe7f:
		return segmentThai
	case c >= 0x0e80 && c <= 0x0eff:
		return segmentLao
	case c >= 0x1000 && c <= 0x109f:
		return segmentBurmese
	case c >= 0x1780 && c <= 0x17ff, c >= 0x19e0 && c <= 0x19ff:
		return segmentKhmer
	case c >= 0x2e80 && c <= 0x2eff, c >= 0x2f00 && c <= 0x2fdf, c >= 0x3040 && c <= 0x30ff,
		c >= 0x31f0 && c <= 0x31ff, c >= 0x32d0 && c <= 0x32fe, c >= 0x3400 && c <= 0x4dbf,
		c >= 0x4e00 && c <= 0x9fff:
		return segmentChineseOrJapanese
	case c >= 0xa9e0 && c <= 0xa9ff, c >= 0xaa60 && c <= 0xaa7f:
		return segmentBurmese
	case c >= 0xf900 && c <= 0xfaff, c >= 0xff66 && c <= 0xff9d, c >= 0x16fe2 && c <= 0x16fe3,
		c >= 0x16ff0 && c <= 0x16ff1, c >= 0x1aff0 && c <= 0x1b16f, c == 0x1f200,
		c >= 0x20000 && c <= 0x2fa1f, c >= 0x30000 && c <= 0x3134f:
		return segmentChineseOrJapanese
	}
	return segmentUnknown
}

// segmentComplexBreaks 按语言分段复杂文字，返回各断点（ICU4X ComplexPayloadsBorrowed::complex_language_segment_utf16）
func segmentComplexBreaks(input []uint16) []int {
	var result []int
	offset := 0
	for rest := input; len(rest) > 0; {
		lang := segmentLanguageOf(uint32(rest[0]))
		n := 1
		for n < len(rest) && segmentLanguageOf(uint32(rest[n])) == lang {
			n++
		}
		slice := rest[:n]
		rest = rest[n:]
		var breaks []int
		switch lang {
		case segmentChineseOrJapanese:
			breaks = segmentDictionaryBreaks(slice)
		case segmentUnknown:
			breaks = []int{len(slice)}
		default:
			models, err := segmentModels()
			if err != nil {
				panic(err)
			}
			breaks = models[lang].segment(slice)
		}
		for _, b := range breaks {
			result = append(result, offset+b)
		}
		offset += len(slice)
	}
	return result
}

// segmentDictionaryBreaks 用 cjdict 词典分段（ICU4X dictionary.rs DictionaryBreakIterator）
func segmentDictionaryBreaks(input []uint16) []int {
	trie, err := segmentDictionary()
	if err != nil {
		panic(err)
	}
	rules, err := segmentRuleData()
	if err != nil {
		panic(err)
	}
	iter := segmentCursor{text: input}
	grapheme := newSegmentIterator(rules.grapheme, nil, false, input)
	var result []int
	for {
		cursor := char16TrieCursor{trie: trie, remaining: -1}
		intermediateLength := 0
		notMatch := false
		var previousMatch segmentCursor
		lastGraphemeOffset := 0
		end := -1
	scan:
		for {
			pos, ch, ok := iter.next()
			if !ok {
				break
			}
			c := ch
			if c >= 0xd800 && c <= 0xdfff {
				c = 0xfffd
			}
			charEnd := pos + segmentCharLen(ch)
			switch cursor.next(c) {
			case char16FinalValue:
				end = charEnd
				break scan
			case char16IntermediateValue:
				for lastGraphemeOffset < charEnd {
					if offset, ok := grapheme.next(); ok {
						lastGraphemeOffset = offset
						continue
					}
					lastGraphemeOffset = len(input)
					break
				}
				if lastGraphemeOffset != charEnd {
					continue
				}
				intermediateLength = charEnd
				previousMatch = iter
			case char16NoMatch:
				if intermediateLength > 0 {
					iter = previousMatch
					end = intermediateLength
				} else {
					end = charEnd
				}
				break scan
			case char16NoValue:
				notMatch = true
			}
		}
		if end < 0 {
			switch {
			case intermediateLength > 0:
				end = intermediateLength
			case notMatch:
				end = len(input)
			default:
				return result
			}
		}
		result = append(result, end)
	}
}

// char16TrieResult 是 ICU4X Char16Trie 的匹配结果
type char16TrieResult uint8

const (
	char16NoMatch char16TrieResult = iota
	char16NoValue
	char16FinalValue
	char16IntermediateValue
)

// char16TrieCursor 是 ICU4X Char16TrieIterator，pos 为 -1 表示已停止，remaining 为 -1 表示不在线性匹配节点中
type char16TrieCursor struct {
	trie      []uint16
	pos       int
	remaining int
}

// next 匹配一个码位，补充平面码位拆成代理对
func (t *char16TrieCursor) next(c uint32) char16TrieResult {
	if c <= 0xffff {
		return t.next16(uint16(c))
	}
	switch t.next16(uint16(c>>10 + 0xd7c0)) {
	case char16NoValue, char16IntermediateValue:
		return t.next16(uint16(c&0x3ff | 0xdc00))
	}
	return char16NoMatch
}

// next16 匹配一个 UTF-16 码元
func (t *char16TrieCursor) next16(c uint16) char16TrieResult {
	pos := t.pos
	if pos < 0 {
		return char16NoMatch
	}
	if t.remaining >= 0 {
		if c == t.trie[pos] {
			pos++
			t.pos = pos
			if t.remaining == 0 {
				t.remaining = -1
				if t.trie[pos] >= 0x40 {
					return t.valueResult(pos)
				}
			} else {
				t.remaining--
			}
			return char16NoValue
		}
		t.pos = -1
		return char16NoMatch
	}
	return t.nextImpl(pos, c)
}

// nextImpl 从节点开始匹配
func (t *char16TrieCursor) nextImpl(pos int, in uint16) char16TrieResult {
	node := t.trie[pos]
	pos++
	for {
		if node < 0x30 {
			return t.branchNext(pos, int(node), in)
		} else if node < 0x40 {
			length := int(node - 0x30)
			if in == t.trie[pos] {
				pos++
				t.pos = pos
				if length == 0 {
					t.remaining = -1
					if t.trie[pos] >= 0x40 {
						return t.valueResult(pos)
					}
					return char16NoValue
				}
				t.remaining = length - 1
				return char16NoValue
			}
			break
		} else if node&0x8000 != 0 {
			break
		} else {
			pos = char16SkipNodeValue(pos, node)
			node &= 0x3f
		}
	}
	t.pos = -1
	return char16NoMatch
}

// branchNext 在分支节点中查找码元
func (t *char16TrieCursor) branchNext(pos, length int, in uint16) char16TrieResult {
	if length == 0 {
		length = int(t.trie[pos])
		pos++
	}
	length++
	for length > 5 {
		if in < t.trie[pos] {
			length >>= 1
			pos = t.jumpByDelta(pos + 1)
		} else {
			length = length - length>>1
			pos = t.skipDelta(pos + 1)
		}
	}
	for {
		if in == t.trie[pos] {
			pos++
			node := t.trie[pos]
			if node&0x8000 != 0 {
				t.pos = pos
				return t.valueResult(pos)
			}
			pos++
			if node < 0x4000 {
				pos += int(node)
			} else if node < 0x7fff {
				pos += int(node-0x4000)<<16 | int(t.trie[pos])
				pos++
			} else {
				pos += int(t.trie[pos])<<16 | int(t.trie[pos+1])
				pos += 2
			}
			t.pos = pos
			if t.trie[pos] >= 0x40 {
				return t.valueResult(pos)
			}
			return char16NoValue
		}
		length--
		pos = char16SkipValue(pos+2, t.trie[pos+1]&0x7fff)
		if length <= 1 {
			break
		}
	}
	if in == t.trie[pos] {
		pos++
		t.pos = pos
		if t.trie[pos] >= 0x40 {
			return t.valueResult(pos)
		}
		return char16NoValue
	}
	t.pos = -1
	return char16NoMatch
}

// jumpByDelta 跳过分支增量指向的子节点
func (t *char16TrieCursor) jumpByDelta(pos int) int {
	delta := t.trie[pos]
	switch {
	case delta < 0xfc00:
		return pos + 1 + int(delta)
	case delta == 0xffff:
		return pos + (int(t.trie[pos+1])<<16 | int(t.trie[pos+2])) + 3
	}
	return pos + (int(delta-0xfc00)<<16 | int(t.trie[pos+1])) + 2
}

// skipDelta 跳过分支增量本身
func (t *char16TrieCursor) skipDelta(pos int) int {
	switch delta := t.trie[pos]; {
	case delta < 0xfc00:
		return pos + 1
	case delta == 0xffff:
		return pos + 3
	}
	return pos + 2
}

// valueResult 区分最终值与中间值
func (t *char16TrieCursor) valueResult(pos int) char16TrieResult {
	if t.trie[pos]&0x8000 != 0 {
		return char16FinalValue
	}
	return char16IntermediateValue
}

// char16SkipValue 跳过分支中的值
func char16SkipValue(pos int, lead uint16) int {
	switch {
	case lead < 0x4000:
		return pos
	case lead < 0x7fff:
		return pos + 1
	}
	return pos + 2
}

// char16SkipNodeValue 跳过节点携带的中间值
func char16SkipNodeValue(pos int, lead uint16) int {
	switch {
	case lead < 0x4040:
		return pos
	case lead < 0x7fc0:
		return pos + 1
	}
	return pos + 2
}

// segmentLSTM 是 ICU4X LstmDataFloat32 的码位模型
type segmentLSTM struct {
	dic       map[string]uint16
	embedding []float32
	embedd    int
	hunits    int
	fwW       []float32
	fwU       []float32
	fwB       []float32
	bwW       []float32
	bwU       []float32
	bwB       []float32
	timeFw    []float32
	timeBw    []float32
	timeB     []float32
}

// segmentModels 读取泰、老、高棉、缅甸文的 LSTM 模型，各 goroutine 共享
var segmentModels = sync.OnceValues(func() (map[segmentLanguage]*segmentLSTM, error) {
	r, err := readSegmentFile("lstm")
	if err != nil {
		return nil, err
	}
	languages := map[string]segmentLanguage{"Burmese": segmentBurmese, "Khmer": segmentKhmer, "Lao": segmentLao, "Thai": segmentThai}
	models := map[segmentLanguage]*segmentLSTM{}
	for n := r.u8(); n > 0; n-- {
		name, _, _ := strings.Cut(r.str(), "_")
		m := &segmentLSTM{dic: map[string]uint16{}}
		for k := r.u16(); k > 0; k-- {
			key := r.str()
			m.dic[key] = r.u16()
		}
		matrix := func() ([]uint16, []float32) {
			dims := make([]uint16, r.u8())
			for i := range dims {
				dims[i] = r.u16()
			}
			raw := r.bytes()
			data := make([]float32, len(raw)/4)
			for i := range data {
				data[i] = math.Float32frombits(binary.LittleEndian.Uint32(raw[4*i:]))
			}
			return dims, data
		}
		embeddingDims, embedding := matrix()
		m.embedding, m.embedd = embedding, int(embeddingDims[1])
		_, m.fwW = matrix()
		fwUDims, fwU := matrix()
		m.fwU, m.hunits = fwU, int(fwUDims[1])
		_, m.fwB = matrix()
		_, m.bwW = matrix()
		_, m.bwU = matrix()
		_, m.bwB = matrix()
		_, timeW := matrix()
		m.timeFw, m.timeBw = timeW[:4*m.hunits], timeW[4*m.hunits:]
		_, m.timeB = matrix()
		models[languages[name]] = m
	}
	return models, nil
})

// segment 返回各词末尾的码位下标（ICU4X lstm/mod.rs LstmSegmenter::segment_utf16 与 BiesIterator）
func (m *segmentLSTM) segment(input []uint16) []int {
	n, h, e := len(input), m.hunits, m.embedd
	seq := make([]int, n)
	for i, c := range input {
		id, ok := m.dic[string(rune(c))]
		if !ok {
			id = uint16(len(m.dic))
		}
		seq[i] = int(id)
	}
	hBw := make([]float32, n*h)
	cBw := make([]float32, h)
	for i := n - 1; i >= 0; i-- {
		if i+1 < n {
			copy(hBw[i*h:(i+1)*h], hBw[(i+1)*h:(i+2)*h])
		}
		m.computeHC(m.embedding[seq[i]*e:(seq[i]+1)*e], hBw[i*h:(i+1)*h], cBw, m.bwW, m.bwU, m.bwB)
	}
	currFw := make([]float32, h)
	cFw := make([]float32, h)
	var result []int
	for i := 0; i < n; i++ {
		m.computeHC(m.embedding[seq[i]*e:(seq[i]+1)*e], currFw, cFw, m.fwW, m.fwU, m.fwB)
		var weights [4]float32
		for k := range weights {
			weights[k] += segmentDot(currFw, m.timeFw[k*h:(k+1)*h])
		}
		currBw := hBw[i*h : (i+1)*h]
		for k := range weights {
			weights[k] += segmentDot(currBw, m.timeBw[k*h:(k+1)*h])
		}
		for k := range weights {
			weights[k] += m.timeB[k]
		}
		isEnd := weights[2] > weights[0] && weights[2] > weights[1] && weights[2] > weights[3]
		if isEnd || i == n-1 {
			result = append(result, i+1)
		}
	}
	return result
}

// computeHC 计算一步 LSTM 单元，原地更新 h 与 c（ICU4X lstm/mod.rs compute_hc）
func (m *segmentLSTM) computeHC(x, hState, cState, w, u, b []float32) {
	h, e := m.hunits, len(x)
	s := append([]float32(nil), b...)
	for i := range s {
		s[i] += segmentDot(x, w[i*e:(i+1)*e])
	}
	for i := range s {
		s[i] += segmentDot(hState, u[i*h:(i+1)*h])
	}
	for i := 0; i < 2*h; i++ {
		s[i] = 1 / (1 + libmExpf(-s[i]))
	}
	for i := 2 * h; i < 3*h; i++ {
		s[i] = libmTanhf(s[i])
	}
	for i := 3 * h; i < 4*h; i++ {
		s[i] = 1 / (1 + libmExpf(-s[i]))
	}
	for i := 0; i < h; i++ {
		cState[i] = float32(s[i]*s[2*h+i]) + float32(cState[i]*s[h+i])
	}
	for i := 0; i < h; i++ {
		hState[i] = s[3*h+i] * libmTanhf(cState[i])
	}
}

// segmentDot 是 ICU4X matrix.rs 的八路展开点积，保持 f32 求和顺序
func segmentDot(xs, ys []float32) float32 {
	chunks := len(xs) &^ 7
	sum := math.Float32frombits(0x80000000)
	for k := chunks; k < len(xs); k++ {
		sum += float32(xs[k] * ys[k])
	}
	var p [8]float32
	for k := 0; k < chunks; k += 8 {
		for j := range p {
			p[j] += float32(xs[k+j] * ys[k+j])
		}
	}
	return sum + (p[0] + p[4]) + (p[1] + p[5]) + (p[2] + p[6]) + (p[3] + p[7])
}

// libmExpf 是 Rust libm 0.2.6 的 expf
func libmExpf(x float32) float32 {
	const (
		ln2Hi  float32 = 6.9314575195e-01
		ln2Lo  float32 = 1.4286067653e-06
		invLn2 float32 = 1.4426950216e+00
		p1     float32 = 1.6666625440e-1
		p2     float32 = -2.7667332906e-3
	)
	x1p127 := math.Float32frombits(0x7f000000)
	hx := math.Float32bits(x)
	sign := int32(hx >> 31)
	hx &= 0x7fffffff
	if hx >= 0x42aeac50 {
		if hx > 0x7f800000 {
			return x
		}
		if hx >= 0x42b17218 && sign == 0 {
			return x * x1p127
		}
		if sign != 0 && hx >= 0x42cff1b5 {
			return 0
		}
	}
	var k int32
	var hi, lo float32
	if hx > 0x3eb17218 {
		if hx > 0x3f851592 {
			half := [2]float32{0.5, -0.5}
			k = int32(float32(invLn2*x) + half[sign])
		} else {
			k = 1 - sign - sign
		}
		kf := float32(k)
		hi = x - float32(kf*ln2Hi)
		lo = float32(kf * ln2Lo)
		x = hi - lo
	} else if hx > 0x39000000 {
		k = 0
		hi = x
		lo = 0
	} else {
		return 1 + x
	}
	xx := float32(x * x)
	c := x - float32(xx*(p1+float32(xx*p2)))
	y := 1 + (float32(x*c)/(2-c) - lo + hi)
	if k == 0 {
		return y
	}
	return libmScalbnf(y, k)
}

// libmScalbnf 是 Rust libm 0.2.6 的 scalbnf
func libmScalbnf(x float32, n int32) float32 {
	x1p127 := math.Float32frombits(0x7f000000)
	x1pm126 := math.Float32frombits(0x800000)
	x1p24 := math.Float32frombits(0x4b800000)
	if n > 127 {
		x *= x1p127
		n -= 127
		if n > 127 {
			x *= x1p127
			n -= 127
			if n > 127 {
				n = 127
			}
		}
	} else if n < -126 {
		x *= float32(x1pm126 * x1p24)
		n += 126 - 24
		if n < -126 {
			x *= float32(x1pm126 * x1p24)
			n += 126 - 24
			if n < -126 {
				n = -126
			}
		}
	}
	return x * math.Float32frombits(uint32(0x7f+n)<<23)
}

// libmTanhf 是 Rust libm 0.2.6 的 tanhf
func libmTanhf(x float32) float32 {
	ix := math.Float32bits(x)
	sign := ix>>31 != 0
	ix &= 0x7fffffff
	x = math.Float32frombits(ix)
	var t float32
	switch {
	case ix > 0x3f0c9f54:
		if ix > 0x41200000 {
			t = 1 + 0/x
		} else {
			e := libmExpm1f(2 * x)
			t = 1 - 2/(e+2)
		}
	case ix > 0x3e82c578:
		e := libmExpm1f(2 * x)
		t = e / (e + 2)
	case ix >= 0x00800000:
		e := libmExpm1f(-2 * x)
		t = -e / (e + 2)
	default:
		t = x
	}
	if sign {
		return -t
	}
	return t
}

// libmExpm1f 是 Rust libm 0.2.6 的 expm1f
func libmExpm1f(x float32) float32 {
	const (
		oThreshold float32 = 8.8721679688e+01
		ln2Hi      float32 = 6.9313812256e-01
		ln2Lo      float32 = 9.0580006145e-06
		invLn2     float32 = 1.4426950216e+00
		q1         float32 = -3.3333212137e-2
		q2         float32 = 1.5807170421e-3
	)
	x1p127 := math.Float32frombits(0x7f000000)
	hx := math.Float32bits(x)
	sign := hx>>31 != 0
	hx &= 0x7fffffff
	if hx >= 0x4195b844 {
		if hx > 0x7f800000 {
			return x
		}
		if sign {
			return -1
		}
		if x > oThreshold {
			return x * x1p127
		}
	}
	var k int32
	var hi, lo, c float32
	if hx > 0x3eb17218 {
		if hx < 0x3f851592 {
			if !sign {
				hi = x - ln2Hi
				lo = ln2Lo
				k = 1
			} else {
				hi = x + ln2Hi
				lo = -ln2Lo
				k = -1
			}
		} else {
			half := float32(0.5)
			if sign {
				half = -0.5
			}
			k = int32(float32(invLn2*x) + half)
			t := float32(k)
			hi = x - float32(t*ln2Hi)
			lo = float32(t * ln2Lo)
		}
		x = hi - lo
		c = (hi - x) - lo
	} else if hx < 0x33000000 {
		return x
	} else {
		k = 0
	}
	hfx := float32(0.5 * x)
	hxs := float32(x * hfx)
	r1 := 1 + float32(hxs*(q1+float32(hxs*q2)))
	t := 3 - float32(r1*hfx)
	e := float32(hxs * ((r1 - t) / (6 - float32(x*t))))
	if k == 0 {
		return x - (float32(x*e) - hxs)
	}
	e = float32(x*(e-c)) - c
	e -= hxs
	if k == -1 {
		return float32(0.5*(x-e)) - 0.5
	}
	if k == 1 {
		if x < -0.25 {
			return -2 * (e - (x + 0.5))
		}
		return 1 + float32(2*(x-e))
	}
	twopk := math.Float32frombits(uint32(0x7f+k) << 23)
	if k < 0 || k > 56 {
		y := x - e + 1
		if k == 128 {
			y = float32(y*2) * x1p127
		} else {
			y = float32(y * twopk)
		}
		return y - 1
	}
	uf := math.Float32frombits(uint32(0x7f-k) << 23)
	if k < 23 {
		return (x - e + (1 - uf)) * twopk
	}
	return (x - (e + uf) + 1) * twopk
}
