package waa

import (
	"crypto/sha1"
	"crypto/sha256"
	"crypto/sha512"
	"strings"

	"golang.org/x/text/encoding"
	"golang.org/x/text/encoding/htmlindex"
)

// digestBytes 按 SubtleCrypto.digest 的算法名计算摘要
func digestBytes(name string, data []byte) []byte {
	switch name {
	case "SHA-1":
		sum := sha1.Sum(data)
		return sum[:]
	case "SHA-384":
		sum := sha512.Sum384(data)
		return sum[:]
	case "SHA-512":
		sum := sha512.Sum512(data)
		return sum[:]
	default:
		sum := sha256.Sum256(data)
		return sum[:]
	}
}

// encodingName 按 WHATWG Encoding 标准把标签解析为编码名，不支持的标签返回空串
func encodingName(label string) string {
	encoding, err := htmlindex.Get(strings.TrimSpace(label))
	if err != nil {
		return ""
	}
	name, err := htmlindex.Name(encoding)
	if err != nil {
		return ""
	}
	return name
}

// decodeText 以 WHATWG 编码解码字节，fatal 时遇到无效序列返回错误
func decodeText(name string, data []byte, fatal bool) (string, error) {
	encoder, err := htmlindex.Get(name)
	if err != nil {
		return "", err
	}
	decoded, err := encoder.NewDecoder().Bytes(data)
	if err != nil {
		return "", err
	}
	if fatal && strings.ContainsRune(string(decoded), '�') && !strings.ContainsRune(string(data), '�') {
		return "", encoding.ErrInvalidUTF8
	}
	return string(decoded), nil
}
