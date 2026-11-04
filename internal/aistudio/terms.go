package aistudio

import (
	"encoding/json"
	"fmt"
)

const (
	// termsDialogSource 是官网条款对话框发送 AcceptTerms 时的第 9 字段
	termsDialogSource = 1
	// termsOnboardingSource 是官网 onboarding 条款表单发送 AcceptTerms 时的第 9 字段
	termsOnboardingSource = 5
)

// acceptTermsBody 按官网同意条款的条件读取 GetLoggingContext，需要同意时返回不订阅邮件的 AcceptTerms 请求体，否则返回 nil
func acceptTermsBody(raw []byte) ([]byte, error) {
	value, err := decodeJSONValue(raw)
	if err != nil {
		return nil, withMethod(err, "GetLoggingContext")
	}
	root, err := rawArray(value, "$", value)
	if err != nil {
		return nil, withMethod(err, "GetLoggingContext")
	}
	if isJSONNull(rawAt(root, 5)) || isJSONNull(rawAt(root, 6)) {
		return nil, nil
	}
	accepted, err := rawBool(root[5], "$[5]", value)
	if err != nil {
		return nil, withMethod(err, "GetLoggingContext")
	}
	if accepted {
		return nil, nil
	}
	request := []any{"users/me", nil, nil, nil, nil, 1, nil, nil, termsDialogSource}
	for _, field := range []struct{ source, target int }{{6, 2}, {14, 6}} {
		if isJSONNull(rawAt(root, field.source)) {
			continue
		}
		number, err := rawInt64(root[field.source], fmt.Sprintf("$[%d]", field.source), value)
		if err != nil {
			return nil, withMethod(err, "GetLoggingContext")
		}
		if number != 0 {
			request[field.target] = number
		}
	}
	onboarded := rawAt(root, 9)
	if isJSONNull(onboarded) {
		request[8] = termsOnboardingSource
	} else if version, err := rawInt64(onboarded, "$[9]", value); err != nil {
		return nil, withMethod(err, "GetLoggingContext")
	} else if version == 0 {
		request[8] = termsOnboardingSource
	}
	return json.Marshal(request)
}
