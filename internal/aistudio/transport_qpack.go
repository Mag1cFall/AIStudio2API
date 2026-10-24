package aistudio

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"math"
	"sync"

	"golang.org/x/net/http2/hpack"
)

const (
	// qpackEncoderStreamType 是 QPACK 编码器单向流的类型
	qpackEncoderStreamType = 0x02
	// qpackDecoderStreamType 是 QPACK 解码器单向流的类型
	qpackDecoderStreamType = 0x03
	// firefoxQPACKTableSize 是 Firefox network.http.http3.default_qpack_table_size，编码与解码两侧共用
	firefoxQPACKTableSize = 65536
	// firefoxQPACKBlockedStreams 是 Firefox network.http.http3.default_max_stream_blocked
	firefoxQPACKBlockedStreams = 20
	// qpackEntryOverhead 是 QPACK 动态表条目在名与值之外计入的字节数
	qpackEntryOverhead = 32
)

// qpackField 是一条 QPACK 头字段
type qpackField struct {
	name  string
	value string
}

// qpackStaticTable 是 RFC 9204 附录 A 的 QPACK 静态表
var qpackStaticTable = [...]qpackField{
	{":authority", ""}, {":path", "/"}, {"age", "0"}, {"content-disposition", ""}, {"content-length", "0"}, {"cookie", ""}, {"date", ""}, {"etag", ""},
	{"if-modified-since", ""}, {"if-none-match", ""}, {"last-modified", ""}, {"link", ""}, {"location", ""}, {"referer", ""}, {"set-cookie", ""},
	{":method", "CONNECT"}, {":method", "DELETE"}, {":method", "GET"}, {":method", "HEAD"}, {":method", "OPTIONS"}, {":method", "POST"}, {":method", "PUT"},
	{":scheme", "http"}, {":scheme", "https"}, {":status", "103"}, {":status", "200"}, {":status", "304"}, {":status", "404"}, {":status", "503"},
	{"accept", "*/*"}, {"accept", "application/dns-message"}, {"accept-encoding", "gzip, deflate, br"}, {"accept-ranges", "bytes"},
	{"access-control-allow-headers", "cache-control"}, {"access-control-allow-headers", "content-type"}, {"access-control-allow-origin", "*"},
	{"cache-control", "max-age=0"}, {"cache-control", "max-age=2592000"}, {"cache-control", "max-age=604800"}, {"cache-control", "no-cache"},
	{"cache-control", "no-store"}, {"cache-control", "public, max-age=31536000"}, {"content-encoding", "br"}, {"content-encoding", "gzip"},
	{"content-type", "application/dns-message"}, {"content-type", "application/javascript"}, {"content-type", "application/json"},
	{"content-type", "application/x-www-form-urlencoded"}, {"content-type", "image/gif"}, {"content-type", "image/jpeg"}, {"content-type", "image/png"},
	{"content-type", "text/css"}, {"content-type", "text/html; charset=utf-8"}, {"content-type", "text/plain"}, {"content-type", "text/plain;charset=utf-8"},
	{"range", "bytes=0-"}, {"strict-transport-security", "max-age=31536000"}, {"strict-transport-security", "max-age=31536000; includesubdomains"},
	{"strict-transport-security", "max-age=31536000; includesubdomains; preload"}, {"vary", "accept-encoding"}, {"vary", "origin"},
	{"x-content-type-options", "nosniff"}, {"x-xss-protection", "1; mode=block"}, {":status", "100"}, {":status", "204"}, {":status", "206"},
	{":status", "302"}, {":status", "400"}, {":status", "403"}, {":status", "421"}, {":status", "425"}, {":status", "500"}, {"accept-language", ""},
	{"access-control-allow-credentials", "FALSE"}, {"access-control-allow-credentials", "TRUE"}, {"access-control-allow-headers", "*"},
	{"access-control-allow-methods", "get"}, {"access-control-allow-methods", "get, post, options"}, {"access-control-allow-methods", "options"},
	{"access-control-expose-headers", "content-length"}, {"access-control-request-headers", "content-type"}, {"access-control-request-method", "get"},
	{"access-control-request-method", "post"}, {"alt-svc", "clear"}, {"authorization", ""},
	{"content-security-policy", "script-src 'none'; object-src 'none'; base-uri 'none'"}, {"early-data", "1"}, {"expect-ct", ""}, {"forwarded", ""},
	{"if-range", ""}, {"origin", ""}, {"purpose", "prefetch"}, {"server", ""}, {"timing-allow-origin", "*"}, {"upgrade-insecure-requests", "1"},
	{"user-agent", ""}, {"x-forwarded-for", ""}, {"x-frame-options", "deny"}, {"x-frame-options", "sameorigin"},
}

// errQPACK 是 QPACK 数据无法按 RFC 9204 解释时的错误
var errQPACK = errors.New("QPACK 数据无效")

// qpackEntry 是动态表中的一条记录，index 为绝对索引
type qpackEntry struct {
	qpackField
	index uint64
	refs  int
}

func (entry *qpackEntry) size() uint64 {
	return uint64(len(entry.name)+len(entry.value)) + qpackEntryOverhead
}

// qpackTable 是 neqo 语义的 QPACK 动态表，entries 按插入顺序从新到旧排列
type qpackTable struct {
	entries  []*qpackEntry
	capacity uint64
	used     uint64
	base     uint64
	acked    uint64
}

func (table *qpackTable) canReduce(entry *qpackEntry) bool {
	return entry.refs == 0 && entry.index < table.acked
}

func (table *qpackTable) evictTo(size uint64) bool {
	for len(table.entries) > 0 && table.used > size {
		oldest := table.entries[len(table.entries)-1]
		if !table.canReduce(oldest) {
			return false
		}
		table.used -= oldest.size()
		table.entries = table.entries[:len(table.entries)-1]
	}
	return true
}

func (table *qpackTable) canEvictTo(size uint64) bool {
	evictable := uint64(0)
	for i := len(table.entries) - 1; i >= 0 && table.canReduce(table.entries[i]); i-- {
		evictable += table.entries[i].size()
	}
	return table.used-evictable <= size
}

func (table *qpackTable) setCapacity(capacity uint64) error {
	if !table.evictTo(capacity) {
		return errQPACK
	}
	table.capacity = capacity
	return nil
}

func (table *qpackTable) insert(field qpackField) (uint64, error) {
	entry := &qpackEntry{qpackField: field, index: table.base}
	if entry.size() > table.capacity || !table.evictTo(table.capacity-entry.size()) {
		return 0, errQPACK
	}
	table.base++
	table.used += entry.size()
	table.entries = append([]*qpackEntry{entry}, table.entries...)
	return entry.index, nil
}

func (table *qpackTable) absolute(index uint64) *qpackEntry {
	if index >= table.base || table.base-index-1 >= uint64(len(table.entries)) {
		return nil
	}
	return table.entries[table.base-index-1]
}

// relative 按头块的 base 与相对索引取条目，post 表示 post-base 索引
func (table *qpackTable) relative(index uint64, base uint64, post bool) (*qpackEntry, error) {
	var absolute uint64
	if post {
		absolute = base + index
	} else {
		if index >= base {
			return nil, errQPACK
		}
		absolute = base - index - 1
	}
	if entry := table.absolute(absolute); entry != nil {
		return entry, nil
	}
	return nil, errQPACK
}

// lookup 按 neqo 的顺序先查静态表再查动态表，返回完整匹配或首个同名条目
func (table *qpackTable) lookup(field qpackField, canBlock bool) (index uint64, static bool, exact bool, found bool) {
	for i, entry := range qpackStaticTable {
		if entry.name != field.name {
			continue
		}
		if entry.value == field.value {
			return uint64(i), true, true, true
		}
		if !found {
			index, static, found = uint64(i), true, true
		}
	}
	for _, entry := range table.entries {
		if !canBlock && entry.index >= table.acked {
			continue
		}
		if entry.name != field.name {
			continue
		}
		if entry.value == field.value {
			return entry.index, false, true, true
		}
		if !found {
			index, static, found = entry.index, false, true
		}
	}
	return index, static, false, found
}

// appendQPACKInt 按 RFC 9204 4.1.1 写入带前缀的整数，prefix 为前缀位，bits 为前缀位数
func appendQPACKInt(buffer []byte, prefix byte, bits uint8, value uint64) []byte {
	limit := uint64(1)<<(8-bits) - 1
	if value < limit {
		return append(buffer, prefix|byte(value))
	}
	buffer = append(buffer, prefix|byte(limit))
	value -= limit
	for value >= 0x80 {
		buffer = append(buffer, byte(value)|0x80)
		value >>= 7
	}
	return append(buffer, byte(value))
}

// appendQPACKString 以 Huffman 编码写入字符串字面量，H 位紧随 bits 个前缀位
func appendQPACKString(buffer []byte, prefix byte, bits uint8, value string) []byte {
	buffer = appendQPACKInt(buffer, prefix|0x80>>bits, bits+1, hpack.HuffmanEncodeLength(value))
	return hpack.AppendHuffmanString(buffer, value)
}

// qpackEncoder 按 neqo 0.28 的策略编码请求头：静态表没有的头名连同取值写入动态表并按索引引用，字面量一律 Huffman 编码
type qpackEncoder struct {
	table        qpackTable
	maxEntries   uint64
	maxBlocked   uint64
	blocked      uint64
	nextCapacity uint64
	hasNext      bool
	// unacked 记录每个请求流尚未确认的头块引用的动态表条目，按发送顺序排列
	unacked map[int64][][]uint64
	// pending 是待写入编码器流的字节
	pending []byte
}

func newQPACKEncoder() *qpackEncoder {
	return &qpackEncoder{unacked: map[int64][][]uint64{}, pending: []byte{qpackEncoderStreamType}}
}

// setPeerSettings 按服务端 SETTINGS 的动态表容量与阻塞流上限设置编码器
func (encoder *qpackEncoder) setPeerSettings(capacity uint64, blocked uint64) {
	encoder.maxBlocked = blocked
	if capacity = min(capacity, firefoxQPACKTableSize); capacity != encoder.table.capacity {
		encoder.nextCapacity, encoder.hasNext = capacity, true
	}
	_ = encoder.sendCapacity()
}

func (encoder *qpackEncoder) sendCapacity() error {
	if !encoder.hasNext {
		return nil
	}
	if encoder.nextCapacity < encoder.table.capacity && !encoder.table.canEvictTo(encoder.nextCapacity) {
		return errQPACK
	}
	encoder.pending = appendQPACKInt(encoder.pending, 0x20, 3, encoder.nextCapacity)
	_ = encoder.table.setCapacity(encoder.nextCapacity)
	encoder.maxEntries = encoder.nextCapacity / 32
	encoder.hasNext = false
	return nil
}

func (encoder *qpackEncoder) isBlocker(stream int64) bool {
	for _, refs := range encoder.unacked[stream] {
		for _, index := range refs {
			if index >= encoder.table.acked {
				return true
			}
		}
	}
	return false
}

// encode 编码一个头块，需要写入编码器流的指令追加到 pending
func (encoder *qpackEncoder) encode(fields []qpackField, stream int64) []byte {
	encoderBlocked := encoder.sendCapacity() != nil
	base := encoder.table.base
	streamBlocker := encoder.isBlocker(stream)
	canBlock := encoder.blocked < encoder.maxBlocked || streamBlocker
	refs := make([]uint64, 0)
	referenced := map[uint64]bool{}
	maxRef, hasRef := uint64(0), false
	reference := func(index uint64) {
		if !hasRef || index > maxRef {
			maxRef, hasRef = index, true
		}
	}
	addRef := func(index uint64) {
		if !referenced[index] {
			referenced[index] = true
			refs = append(refs, index)
			encoder.table.absolute(index).refs++
		}
	}
	var body []byte
	for _, field := range fields {
		index, static, exact, found := encoder.table.lookup(field, canBlock)
		switch {
		case found && exact && static:
			body = appendQPACKInt(body, 0xc0, 2, index)
		case found && exact:
			body = encoder.appendDynamicIndex(body, index, base)
			reference(index)
		case found && static:
			body = appendQPACKInt(body, 0x50, 4, index)
			body = appendQPACKString(body, 0, 0, field.value)
		case found:
			if index < base {
				body = appendQPACKInt(body, 0x40, 4, base-index-1)
			} else {
				body = appendQPACKInt(body, 0x00, 5, index-base)
			}
			body = appendQPACKString(body, 0, 0, field.value)
			reference(index)
		case canBlock && !encoderBlocked:
			inserted, ok := encoder.insert(field)
			if !ok {
				encoderBlocked = true
				body = appendQPACKString(body, 0x20, 4, field.name)
				body = appendQPACKString(body, 0, 0, field.value)
				continue
			}
			body = encoder.appendDynamicIndex(body, inserted, base)
			reference(inserted)
			addRef(inserted)
			continue
		default:
			body = appendQPACKString(body, 0x20, 4, field.name)
			body = appendQPACKString(body, 0, 0, field.value)
			continue
		}
		if found && !static {
			addRef(index)
		}
	}
	block := encoder.appendPrefix(nil, base, maxRef, hasRef)
	block = append(block, body...)
	if !streamBlocker && hasRef && maxRef >= encoder.table.acked {
		encoder.blocked++
	}
	if len(refs) > 0 {
		encoder.unacked[stream] = append(encoder.unacked[stream], refs)
	}
	return block
}

func (encoder *qpackEncoder) insert(field qpackField) (uint64, bool) {
	size := uint64(len(field.name)+len(field.value)) + qpackEntryOverhead
	if size > encoder.table.capacity || !encoder.table.canEvictTo(encoder.table.capacity-size) {
		return 0, false
	}
	encoder.pending = appendQPACKString(encoder.pending, 0x40, 2, field.name)
	encoder.pending = appendQPACKString(encoder.pending, 0, 0, field.value)
	index, err := encoder.table.insert(field)
	return index, err == nil
}

func (encoder *qpackEncoder) appendDynamicIndex(buffer []byte, index uint64, base uint64) []byte {
	if index < base {
		return appendQPACKInt(buffer, 0x80, 2, base-index-1)
	}
	return appendQPACKInt(buffer, 0x10, 4, index-base)
}

func (encoder *qpackEncoder) appendPrefix(buffer []byte, base uint64, maxRef uint64, hasRef bool) []byte {
	if !hasRef {
		buffer = appendQPACKInt(buffer, 0, 0, 0)
		return appendQPACKInt(buffer, 0x00, 1, base)
	}
	required := maxRef + 1
	buffer = appendQPACKInt(buffer, 0, 0, required%(2*encoder.maxEntries)+1)
	if required <= base {
		return appendQPACKInt(buffer, 0x00, 1, base-required)
	}
	return appendQPACKInt(buffer, 0x80, 1, required-base-1)
}

func (encoder *qpackEncoder) recalculateBlocked() {
	encoder.blocked = 0
	for stream := range encoder.unacked {
		if encoder.isBlocker(stream) {
			encoder.blocked++
		}
	}
}

func (encoder *qpackEncoder) incrementAcked(increment uint64) error {
	encoder.table.acked += increment
	if encoder.table.base < encoder.table.acked {
		return errQPACK
	}
	encoder.recalculateBlocked()
	return nil
}

func (encoder *qpackEncoder) sectionAck(stream int64) error {
	blocks := encoder.unacked[stream]
	if len(blocks) == 0 {
		return errQPACK
	}
	acked := encoder.table.acked
	for _, index := range blocks[0] {
		encoder.table.absolute(index).refs--
		if index >= acked {
			acked = index + 1
		}
	}
	if blocks = blocks[1:]; len(blocks) == 0 {
		delete(encoder.unacked, stream)
	} else {
		encoder.unacked[stream] = blocks
	}
	if acked > encoder.table.acked {
		return encoder.incrementAcked(acked - encoder.table.acked)
	}
	return nil
}

func (encoder *qpackEncoder) streamCancel(stream int64) {
	wasBlocker := false
	for _, refs := range encoder.unacked[stream] {
		for _, index := range refs {
			encoder.table.absolute(index).refs--
			wasBlocker = wasBlocker || index >= encoder.table.acked
		}
	}
	delete(encoder.unacked, stream)
	if wasBlocker && encoder.blocked > 0 {
		encoder.blocked--
	}
}

// readQPACKInt 读取带前缀的整数，first 为已读出的首字节，bits 为前缀位数
func readQPACKInt(reader io.ByteReader, first byte, bits uint8) (uint64, error) {
	limit := uint64(1)<<(8-bits) - 1
	value := uint64(first) & limit
	if value < limit {
		return value, nil
	}
	for shift := uint(0); shift < 63; shift += 7 {
		next, err := reader.ReadByte()
		if err != nil {
			return 0, err
		}
		value += uint64(next&0x7f) << shift
		if next&0x80 == 0 {
			return value, nil
		}
	}
	return 0, errQPACK
}

// qpackByteReader 是读取 QPACK 字面量所需的字节读取接口
type qpackByteReader interface {
	io.ByteReader
	io.Reader
}

// readQPACKValue 读取首字节没有其他字段的字符串字面量
func readQPACKValue(reader qpackByteReader) (string, error) {
	first, err := reader.ReadByte()
	if err != nil {
		return "", err
	}
	return readQPACKString(reader, first, 0)
}

// readQPACKString 读取字符串字面量，H 位紧随 bits 个前缀位
func readQPACKString(reader qpackByteReader, first byte, bits uint8) (string, error) {
	length, err := readQPACKInt(reader, first, bits+1)
	if err != nil {
		return "", err
	}
	if length > 1<<20 {
		return "", errQPACK
	}
	raw := make([]byte, length)
	if _, err := io.ReadFull(reader, raw); err != nil {
		return "", err
	}
	if first&(0x80>>bits) == 0 {
		return string(raw), nil
	}
	return hpack.HuffmanDecodeToString(raw)
}

// readDecoderStream 读取服务端解码器流上的指令并更新编码器状态
func (encoder *qpackEncoder) readDecoderStream(stream io.Reader, mu *sync.Mutex) error {
	reader := bufio.NewReader(stream)
	for {
		first, err := reader.ReadByte()
		if err != nil {
			return err
		}
		var apply func() error
		switch {
		case first&0x80 != 0:
			id, err := readQPACKInt(reader, first, 1)
			if err != nil {
				return err
			}
			apply = func() error { return encoder.sectionAck(int64(id)) }
		case first&0x40 != 0:
			id, err := readQPACKInt(reader, first, 2)
			if err != nil {
				return err
			}
			apply = func() error { encoder.streamCancel(int64(id)); return nil }
		default:
			increment, err := readQPACKInt(reader, first, 2)
			if err != nil {
				return err
			}
			apply = func() error { return encoder.incrementAcked(increment) }
		}
		mu.Lock()
		err = apply()
		mu.Unlock()
		if err != nil {
			return err
		}
	}
}

// qpackDecoder 维护服务端编码器写入的动态表并解码响应头块，回执时机与 neqo 0.28 一致
type qpackDecoder struct {
	mu      sync.Mutex
	changed chan struct{}
	table   qpackTable
	acked   uint64
	blocked int
	// pending 是待写入解码器流的字节
	pending []byte
}

func newQPACKDecoder() *qpackDecoder {
	return &qpackDecoder{changed: make(chan struct{}), table: qpackTable{acked: math.MaxUint64}, pending: []byte{qpackDecoderStreamType}}
}

// maxEntries 是 RFC 9204 3.2.2 按本端声明的动态表容量算出的最大条目数
func (decoder *qpackDecoder) maxEntries() uint64 {
	return firefoxQPACKTableSize / 32
}

// readEncoderStream 读取服务端编码器流的指令写入动态表，每读完一批到达的数据调用一次 flush
func (decoder *qpackDecoder) readEncoderStream(stream io.Reader, flush func()) error {
	reader := bufio.NewReader(stream)
	for {
		first, err := reader.ReadByte()
		if err != nil {
			return err
		}
		if err := decoder.readInstruction(reader, first); err != nil {
			return err
		}
		if reader.Buffered() == 0 {
			flush()
		}
	}
}

func (decoder *qpackDecoder) readInstruction(reader *bufio.Reader, first byte) error {
	var field qpackField
	switch {
	case first&0x80 != 0:
		index, err := readQPACKInt(reader, first, 2)
		if err != nil {
			return err
		}
		value, err := readQPACKValue(reader)
		if err != nil {
			return err
		}
		decoder.mu.Lock()
		defer decoder.mu.Unlock()
		if first&0x40 != 0 {
			if index >= uint64(len(qpackStaticTable)) {
				return errQPACK
			}
			field = qpackField{qpackStaticTable[index].name, value}
		} else {
			entry, err := decoder.table.relative(index, decoder.table.base, false)
			if err != nil {
				return err
			}
			field = qpackField{entry.name, value}
		}
		return decoder.insertLocked(field)
	case first&0x40 != 0:
		name, err := readQPACKString(reader, first, 2)
		if err != nil {
			return err
		}
		value, err := readQPACKValue(reader)
		if err != nil {
			return err
		}
		decoder.mu.Lock()
		defer decoder.mu.Unlock()
		return decoder.insertLocked(qpackField{name, value})
	case first&0x20 != 0:
		capacity, err := readQPACKInt(reader, first, 3)
		if err != nil {
			return err
		}
		if capacity > firefoxQPACKTableSize {
			return errQPACK
		}
		decoder.mu.Lock()
		defer decoder.mu.Unlock()
		return decoder.table.setCapacity(capacity)
	default:
		index, err := readQPACKInt(reader, first, 3)
		if err != nil {
			return err
		}
		decoder.mu.Lock()
		defer decoder.mu.Unlock()
		entry, err := decoder.table.relative(index, decoder.table.base, false)
		if err != nil {
			return err
		}
		return decoder.insertLocked(entry.qpackField)
	}
}

func (decoder *qpackDecoder) insertLocked(field qpackField) error {
	if _, err := decoder.table.insert(field); err != nil {
		return err
	}
	close(decoder.changed)
	decoder.changed = make(chan struct{})
	return nil
}

// decode 解码一个头块，引用尚未到达的动态表条目时等待编码器流，解码后追加 Section Acknowledgment
func (decoder *qpackDecoder) decode(ctx context.Context, block []byte, stream int64) ([]qpackField, error) {
	reader := &qpackBlockReader{data: block}
	first, err := reader.ReadByte()
	if err != nil {
		return nil, err
	}
	encoded, err := readQPACKInt(reader, first, 0)
	if err != nil {
		return nil, err
	}
	signByte, err := reader.ReadByte()
	if err != nil {
		return nil, err
	}
	delta, err := readQPACKInt(reader, signByte, 1)
	if err != nil {
		return nil, err
	}
	decoder.mu.Lock()
	defer decoder.mu.Unlock()
	required, err := decoder.requiredInsertCount(encoded)
	if err != nil {
		return nil, err
	}
	var base uint64
	if signByte&0x80 != 0 {
		if required <= delta {
			return nil, errQPACK
		}
		base = required - delta - 1
	} else {
		base = required + delta
	}
	if decoder.table.base < required {
		if decoder.blocked > firefoxQPACKBlockedStreams {
			return nil, fmt.Errorf("QPACK 阻塞流超过 %d", firefoxQPACKBlockedStreams)
		}
		decoder.blocked++
		for decoder.table.base < required {
			changed := decoder.changed
			decoder.mu.Unlock()
			select {
			case <-changed:
				decoder.mu.Lock()
			case <-ctx.Done():
				decoder.mu.Lock()
				decoder.blocked--
				return nil, ctx.Err()
			}
		}
		decoder.blocked--
	}
	fields, err := decoder.decodeFields(reader, base)
	if err != nil {
		return nil, err
	}
	if required != 0 {
		decoder.pending = appendQPACKInt(decoder.pending, 0x80, 1, uint64(stream))
		decoder.acked = max(decoder.acked, required)
	}
	return fields, nil
}

func (decoder *qpackDecoder) requiredInsertCount(encoded uint64) (uint64, error) {
	if encoded == 0 {
		return 0, nil
	}
	full := 2 * decoder.maxEntries()
	if encoded > full {
		return 0, errQPACK
	}
	maxValue := decoder.table.base + decoder.maxEntries()
	required := maxValue/full*full + encoded - 1
	if required > maxValue {
		if required < full {
			return 0, errQPACK
		}
		required -= full
	}
	return required, nil
}

func (decoder *qpackDecoder) decodeFields(reader *qpackBlockReader, base uint64) ([]qpackField, error) {
	fields := make([]qpackField, 0, 16)
	for reader.offset < len(reader.data) {
		first, _ := reader.ReadByte()
		var field qpackField
		switch {
		case first&0x80 != 0:
			index, err := readQPACKInt(reader, first, 2)
			if err != nil {
				return nil, err
			}
			if field, err = decoder.lookup(index, base, first&0x40 != 0, false); err != nil {
				return nil, err
			}
		case first&0x40 != 0:
			index, err := readQPACKInt(reader, first, 4)
			if err != nil {
				return nil, err
			}
			if field, err = decoder.lookup(index, base, first&0x10 != 0, false); err != nil {
				return nil, err
			}
			if field.value, err = readQPACKValue(reader); err != nil {
				return nil, err
			}
		case first&0x20 != 0:
			name, err := readQPACKString(reader, first, 4)
			if err != nil {
				return nil, err
			}
			value, err := readQPACKValue(reader)
			if err != nil {
				return nil, err
			}
			field = qpackField{name, value}
		case first&0x10 != 0:
			index, err := readQPACKInt(reader, first, 4)
			if err != nil {
				return nil, err
			}
			if field, err = decoder.lookup(index, base, false, true); err != nil {
				return nil, err
			}
		default:
			index, err := readQPACKInt(reader, first, 5)
			if err != nil {
				return nil, err
			}
			if field, err = decoder.lookup(index, base, false, true); err != nil {
				return nil, err
			}
			if field.value, err = readQPACKValue(reader); err != nil {
				return nil, err
			}
		}
		fields = append(fields, field)
	}
	return fields, nil
}

func (decoder *qpackDecoder) lookup(index uint64, base uint64, static bool, post bool) (qpackField, error) {
	if static {
		if index >= uint64(len(qpackStaticTable)) {
			return qpackField{}, errQPACK
		}
		return qpackStaticTable[index], nil
	}
	entry, err := decoder.table.relative(index, base, post)
	if err != nil {
		return qpackField{}, err
	}
	return entry.qpackField, nil
}

// cancel 在放弃读取请求流时追加 Stream Cancellation
func (decoder *qpackDecoder) cancel(stream int64) {
	decoder.mu.Lock()
	defer decoder.mu.Unlock()
	if decoder.table.capacity > 0 {
		decoder.pending = appendQPACKInt(decoder.pending, 0x40, 2, uint64(stream))
	}
}

// takePending 追加尚未确认插入数的 Insert Count Increment 并取出待写入解码器流的字节
func (decoder *qpackDecoder) takePending() []byte {
	decoder.mu.Lock()
	defer decoder.mu.Unlock()
	if increment := decoder.table.base - decoder.acked; increment > 0 {
		decoder.pending = appendQPACKInt(decoder.pending, 0x00, 2, increment)
		decoder.acked = decoder.table.base
	}
	pending := decoder.pending
	decoder.pending = nil
	return pending
}

// qpackBlockReader 顺序读取一个完整头块
type qpackBlockReader struct {
	data   []byte
	offset int
}

func (reader *qpackBlockReader) ReadByte() (byte, error) {
	if reader.offset >= len(reader.data) {
		return 0, errQPACK
	}
	value := reader.data[reader.offset]
	reader.offset++
	return value, nil
}

func (reader *qpackBlockReader) Read(buffer []byte) (int, error) {
	if reader.offset >= len(reader.data) {
		return 0, io.EOF
	}
	read := copy(buffer, reader.data[reader.offset:])
	reader.offset += read
	return read, nil
}
