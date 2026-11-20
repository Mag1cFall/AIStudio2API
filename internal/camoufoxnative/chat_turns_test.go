package camoufoxnative

import (
	"encoding/json"
	"testing"
)

// turnContents 把 [parts, role] 形式的 wire 轮次文本拆成 contents
func turnContents(t *testing.T, value string) []json.RawMessage {
	t.Helper()
	var contents []json.RawMessage
	if err := json.Unmarshal([]byte(value), &contents); err != nil {
		t.Fatal(err)
	}
	return contents
}

// TestChatActionFollowsOfficialTurns 断言界面运行按官网用户的操作选择：页面没有对话时直接输入，同一对话再次运行点 Rerun，在上一轮之后追加 model 与 user 两轮时继续当前对话，其余对话先开新对话
func TestChatActionFollowsOfficialTurns(t *testing.T) {
	first := `[[[[null,"alpha"]],"user"]]`
	continued := `[[[[null,"alpha"]],"user"],[[[null,"A"]],"model"],[[[null,"beta"]],"user"]]`
	cases := []struct {
		name     string
		previous string
		contents string
		want     chatAction
	}{
		{name: "empty page", previous: "", contents: first, want: chatContinue},
		{name: "same conversation", previous: first, contents: first, want: chatRerun},
		{name: "model and user appended", previous: first, contents: continued, want: chatContinue},
		{name: "rerun after continuation", previous: continued, contents: continued, want: chatRerun},
		{name: "only user appended", previous: first, contents: `[[[[null,"alpha"]],"user"],[[[null,"beta"]],"user"]]`, want: chatNew},
		{name: "different first turn", previous: first, contents: `[[[[null,"gamma"]],"user"],[[[null,"A"]],"model"],[[[null,"beta"]],"user"]]`, want: chatNew},
		{name: "shorter conversation", previous: continued, contents: first, want: chatNew},
		{name: "unrelated conversation", previous: continued, contents: `[[[[null,"delta"]],"user"]]`, want: chatNew},
	}
	for _, item := range cases {
		t.Run(item.name, func(t *testing.T) {
			var previous []json.RawMessage
			if item.previous != "" {
				previous = turnContents(t, item.previous)
			}
			if got := nextChatAction(previous, turnContents(t, item.contents)); got != item.want {
				t.Fatalf("nextChatAction = %d，应为 %d", got, item.want)
			}
		})
	}
}

// TestGeneratePromptIsLastUserText 断言界面输入的提示词是最后一个 user 轮次各文本 Part 以空格连接的结果，最后一个轮次不是带文本的 user 轮次时不在界面运行
func TestGeneratePromptIsLastUserText(t *testing.T) {
	cases := []struct {
		name string
		body string
		want string
		ok   bool
	}{
		{name: "single text", body: `["models/m",[[[[null,"alpha"]],"user"]],null,null,"proof"]`, want: "alpha", ok: true},
		{name: "text parts joined", body: `["models/m",[[[[null,"a"]],"user"],[[[null,"A"]],"model"],[[[null,"b"],[null,null,["image/png","AAAA"]],[null,"c"]],"user"]],null,null,"proof"]`, want: "b c", ok: true},
		{name: "image only", body: `["models/m",[[[[null,null,["image/png","AAAA"]]],"user"]],null,null,"proof"]`, ok: false},
		{name: "model last", body: `["models/m",[[[[null,"a"]],"user"],[[[null,"A"]],"model"]],null,null,"proof"]`, ok: false},
	}
	for _, item := range cases {
		t.Run(item.name, func(t *testing.T) {
			got, ok := GeneratePrompt([]byte(item.body))
			if got != item.want || ok != item.ok {
				t.Fatalf("GeneratePrompt = %q %v，应为 %q %v", got, ok, item.want, item.ok)
			}
		})
	}
}
