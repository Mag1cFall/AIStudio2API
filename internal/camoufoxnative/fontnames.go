package camoufoxnative

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"sync"
	"unicode/utf16"
)

// substituteFontsMu 串行化字体目录的替换名写入
var substituteFontsMu sync.Mutex

// substituteFontDirectories 为本进程已补入替换名的字体目录
var substituteFontDirectories = map[string]bool{}

// substitutePostScriptSuffix 为补入替换名的字体 PostScript 名后缀，使其不与 macOS 系统中的同名字体重名
const substitutePostScriptSuffix = "-Windows"

// fontNameRecord 为 sfnt name 表的一条名称记录
type fontNameRecord struct {
	platform, encoding, language, nameID, length, offset uint16
}

// addWindowsSubstituteNames 在 directory 的字体文件 name 表中补入以其字体族为替换目标的 Windows 字体替换名
func addWindowsSubstituteNames(directory string) error {
	substituteFontsMu.Lock()
	defer substituteFontsMu.Unlock()
	if substituteFontDirectories[directory] {
		return nil
	}
	aliases := map[string][]string{}
	for alias, target := range windowsFontData.Substitutes {
		aliases[target] = append(aliases[target], alias)
	}
	entries, err := os.ReadDir(directory)
	if err != nil {
		return fmt.Errorf("读取 Camoufox 字体目录: %w", err)
	}
	for _, entry := range entries {
		if !entry.Type().IsRegular() {
			continue
		}
		path := filepath.Join(directory, entry.Name())
		table, err := readFontNameTable(path)
		if err != nil {
			return err
		}
		if table == nil {
			continue
		}
		family, names := fontFamilyNames(table)
		var missing []string
		for _, alias := range aliases[family] {
			if !slices.Contains(names, alias) {
				missing = append(missing, alias)
			}
		}
		if len(missing) == 0 {
			continue
		}
		slices.Sort(missing)
		if err := rewriteFontNames(path, missing); err != nil {
			return err
		}
	}
	substituteFontDirectories[directory] = true
	return nil
}

// readFontNameTable 返回单字体 sfnt 文件的 name 表，字体集合与其他文件返回 nil
func readFontNameTable(path string) ([]byte, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	header := make([]byte, 12)
	if _, err := io.ReadFull(file, header); err != nil {
		return nil, nil
	}
	switch string(header[:4]) {
	case "\x00\x01\x00\x00", "OTTO", "true":
	default:
		return nil, nil
	}
	directory := make([]byte, 16*int(binary.BigEndian.Uint16(header[4:])))
	if _, err := io.ReadFull(file, directory); err != nil {
		return nil, fmt.Errorf("读取字体 %s: %w", path, err)
	}
	for index := 0; index < len(directory); index += 16 {
		if string(directory[index:index+4]) != "name" {
			continue
		}
		table := make([]byte, binary.BigEndian.Uint32(directory[index+12:]))
		if _, err := file.ReadAt(table, int64(binary.BigEndian.Uint32(directory[index+8:]))); err != nil {
			return nil, fmt.Errorf("读取字体 %s: %w", path, err)
		}
		return table, nil
	}
	return nil, nil
}

// parseFontNames 解析 name 表，返回名称记录与字符串区
func parseFontNames(table []byte) ([]fontNameRecord, []byte) {
	records := make([]fontNameRecord, binary.BigEndian.Uint16(table[2:]))
	for index := range records {
		field := func(position int) uint16 { return binary.BigEndian.Uint16(table[6+12*index+2*position:]) }
		records[index] = fontNameRecord{field(0), field(1), field(2), field(3), field(4), field(5)}
	}
	return records, table[binary.BigEndian.Uint16(table[4:]):]
}

// fontFamilyNames 返回 Windows 平台英文字体族名与全部 Windows 平台字体族名
func fontFamilyNames(table []byte) (string, []string) {
	records, storage := parseFontNames(table)
	var family string
	var names []string
	for _, record := range records {
		if record.platform != 3 || record.encoding != 1 || record.nameID != 1 {
			continue
		}
		raw := storage[record.offset : record.offset+record.length]
		units := make([]uint16, len(raw)/2)
		for index := range units {
			units[index] = binary.BigEndian.Uint16(raw[2*index:])
		}
		name := string(utf16.Decode(units))
		if record.language == 0x0409 {
			family = name
		}
		names = append(names, name)
	}
	return family, names
}

// rewriteFontNames 为字体文件补入 Windows 平台字体族名记录，语言取未使用的自定义语言 ID，PostScript 名加 substitutePostScriptSuffix，并更新校验和
func rewriteFontNames(path string, names []string) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	count := int(binary.BigEndian.Uint16(data[4:]))
	type table struct {
		tag  string
		data []byte
	}
	tables := make([]table, count)
	for index := range tables {
		entry := data[12+16*index:]
		offset, length := binary.BigEndian.Uint32(entry[8:]), binary.BigEndian.Uint32(entry[12:])
		tables[index] = table{string(entry[:4]), data[offset : offset+length]}
	}
	nameIndex := slices.IndexFunc(tables, func(item table) bool { return item.tag == "name" })
	records, storage := parseFontNames(tables[nameIndex].data)
	text := bytes.Clone(storage)
	for index, record := range records {
		if record.nameID != 6 {
			continue
		}
		suffix := []byte(substitutePostScriptSuffix)
		if record.platform != 1 {
			suffix = utf16BigEndian(substitutePostScriptSuffix)
		}
		records[index].offset, records[index].length = uint16(len(text)), record.length+uint16(len(suffix))
		text = append(append(text, storage[record.offset:record.offset+record.length]...), suffix...)
	}
	language := uint16(0x0200)
	for _, name := range names {
		for slices.ContainsFunc(records, func(record fontNameRecord) bool {
			return record.platform == 3 && record.encoding == 1 && record.language == language && record.nameID == 1
		}) {
			language++
		}
		encoded := utf16BigEndian(name)
		records = append(records, fontNameRecord{3, 1, language, 1, uint16(len(encoded)), uint16(len(text))})
		text = append(text, encoded...)
		language++
	}
	slices.SortFunc(records, func(a, b fontNameRecord) int {
		return slices.Compare([]uint16{a.platform, a.encoding, a.language, a.nameID}, []uint16{b.platform, b.encoding, b.language, b.nameID})
	})
	name := binary.BigEndian.AppendUint16(nil, 0)
	name = binary.BigEndian.AppendUint16(name, uint16(len(records)))
	name = binary.BigEndian.AppendUint16(name, uint16(6+12*len(records)))
	for _, record := range records {
		for _, value := range []uint16{record.platform, record.encoding, record.language, record.nameID, record.length, record.offset} {
			name = binary.BigEndian.AppendUint16(name, value)
		}
	}
	tables[nameIndex].data = append(name, text...)

	output := bytes.Clone(data[:12+16*count])
	head := -1
	for index, item := range tables {
		for len(output)%4 != 0 {
			output = append(output, 0)
		}
		entry := output[12+16*index:]
		binary.BigEndian.PutUint32(entry[8:], uint32(len(output)))
		binary.BigEndian.PutUint32(entry[12:], uint32(len(item.data)))
		if item.tag == "head" {
			head = len(output)
		}
		output = append(output, item.data...)
	}
	for len(output)%4 != 0 {
		output = append(output, 0)
	}
	if head >= 0 {
		binary.BigEndian.PutUint32(output[head+8:], 0)
	}
	for index, item := range tables {
		entry := output[12+16*index:]
		offset := binary.BigEndian.Uint32(entry[8:])
		binary.BigEndian.PutUint32(entry[4:], fontChecksum(output[offset:offset+(uint32(len(item.data))+3)&^3]))
	}
	if head >= 0 {
		binary.BigEndian.PutUint32(output[head+8:], 0xB1B0AFBA-fontChecksum(output))
	}

	temporary, err := os.CreateTemp(filepath.Dir(filepath.Dir(path)), ".font-*")
	if err != nil {
		return fmt.Errorf("写入字体 %s: %w", path, err)
	}
	defer os.Remove(temporary.Name())
	_, writeErr := temporary.Write(output)
	closeErr := temporary.Close()
	if writeErr != nil || closeErr != nil {
		return fmt.Errorf("写入字体 %s: %w", path, firstError(writeErr, closeErr))
	}
	if err := os.Chmod(temporary.Name(), 0o644); err != nil {
		return fmt.Errorf("写入字体 %s: %w", path, err)
	}
	if err := os.Rename(temporary.Name(), path); err != nil {
		return fmt.Errorf("写入字体 %s: %w", path, err)
	}
	return nil
}

// utf16BigEndian 返回 name 表中 Unicode 与 Windows 平台使用的 UTF-16BE 编码
func utf16BigEndian(text string) []byte {
	var encoded []byte
	for _, unit := range utf16.Encode([]rune(text)) {
		encoded = binary.BigEndian.AppendUint16(encoded, unit)
	}
	return encoded
}

// fontChecksum 为按大端 uint32 累加的 sfnt 校验和，data 长度为 4 的倍数
func fontChecksum(data []byte) uint32 {
	var sum uint32
	for index := 0; index+4 <= len(data); index += 4 {
		sum += binary.BigEndian.Uint32(data[index:])
	}
	return sum
}
