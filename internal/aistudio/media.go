package aistudio

import (
	"encoding/base64"
	"encoding/json"
)

func decodeInlineMedia(raw json.RawMessage, path string, evidence json.RawMessage) (Blob, error) {
	values, err := rawArray(raw, path, evidence)
	if err != nil {
		return Blob{}, withMethod(err, "GenerateContent")
	}
	if len(values) < 2 {
		return Blob{}, &ProtocolEvidenceError{Method: "GenerateContent", Path: path, Detail: "inline media 字段不足", Raw: raw}
	}
	mime, err := rawString(values[0], path+"[0]", raw)
	if err != nil {
		return Blob{}, withMethod(err, "GenerateContent")
	}
	encoded, err := rawString(values[1], path+"[1]", raw)
	if err != nil {
		return Blob{}, withMethod(err, "GenerateContent")
	}
	data, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil {
		return Blob{}, &ProtocolEvidenceError{Method: "GenerateContent", Path: path + "[1]", Detail: "inline media 不是有效 Base64", Raw: raw}
	}
	return Blob{MIME: mime, Data: data}, nil
}
