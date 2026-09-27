package waa

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"strings"
)

// Challenge 保存 Waa/Create 返回的客户端解释器输入
type Challenge struct {
	MessageID                  string
	InterpreterJavaScript      string
	InterpreterURL             string
	InterpreterHash            string
	Program                    string
	GlobalName                 string
	ClientExperimentsStateBlob string
}

// ParseChallenge 解析 Waa/Create 响应，第 2 字段的混淆 challenge 为空时读取第 1 字段的明文 challenge
func ParseChallenge(raw []byte) (Challenge, error) {
	var outer []json.RawMessage
	if err := json.Unmarshal(raw, &outer); err != nil {
		return Challenge{}, fmt.Errorf("解析 Waa/Create 响应: %w", err)
	}
	var encoded string
	if len(outer) > 1 {
		_ = json.Unmarshal(outer[1], &encoded)
	}
	var fields []json.RawMessage
	if encoded != "" {
		decoded, err := base64.StdEncoding.DecodeString(encoded)
		if err != nil {
			return Challenge{}, fmt.Errorf("解码 Waa/Create challenge: %w", err)
		}
		for index := range decoded {
			decoded[index] += 97
		}
		if err := json.Unmarshal(decoded, &fields); err != nil {
			return Challenge{}, fmt.Errorf("解析 Waa/Create challenge: %w", err)
		}
	} else if len(outer) == 0 || json.Unmarshal(outer[0], &fields) != nil || len(fields) == 0 {
		return Challenge{}, fmt.Errorf("Waa/Create 响应缺少 challenge")
	}
	if len(fields) < 8 {
		return Challenge{}, fmt.Errorf("Waa/Create challenge 字段不足: %d", len(fields))
	}
	challenge := Challenge{
		MessageID:                  decodeString(fields[0]),
		InterpreterJavaScript:      decodeFirstString(fields[1]),
		InterpreterHash:            decodeString(fields[3]),
		Program:                    decodeString(fields[4]),
		GlobalName:                 decodeString(fields[5]),
		ClientExperimentsStateBlob: decodeString(fields[7]),
	}
	if interpreterPath := decodeFirstString(fields[2]); interpreterPath != "" {
		challenge.InterpreterURL = "https:" + strings.TrimPrefix(interpreterPath, "https:")
	}
	if challenge.InterpreterHash == "" || challenge.Program == "" || challenge.GlobalName == "" {
		return Challenge{}, fmt.Errorf("Waa/Create challenge 缺少解释器标识或程序")
	}
	return challenge, nil
}

func decodeString(raw json.RawMessage) string {
	var value string
	_ = json.Unmarshal(raw, &value)
	return value
}

func decodeFirstString(raw json.RawMessage) string {
	var values []json.RawMessage
	if err := json.Unmarshal(raw, &values); err != nil {
		return ""
	}
	for _, rawValue := range values {
		if value := decodeString(rawValue); value != "" {
			return value
		}
	}
	return ""
}

// InterpreterHash 返回解释器源码的 SHA-256 base64url 摘要
func InterpreterHash(source []byte) string {
	digest := sha256.Sum256(source)
	return base64.RawURLEncoding.EncodeToString(digest[:])
}
