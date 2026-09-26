package api

import (
	"encoding/json"
	"testing"
)

func TestChatMessageContentRestoresStoredThoughtSignature(t *testing.T) {
	rememberThoughtSignature("call_sig_1", "sig-abc")

	var message chatMessage
	raw := `{"role":"assistant","content":null,"tool_calls":[{"id":"call_sig_1","type":"function","function":{"name":"get_weather","arguments":"{\"city\":\"Berlin\"}"}}]}`
	if err := json.Unmarshal([]byte(raw), &message); err != nil {
		t.Fatal(err)
	}
	content, err := chatMessageContent(message)
	if err != nil {
		t.Fatal(err)
	}
	if got := content.Parts[0].FunctionCall.ThoughtSignature; got != "sig-abc" {
		t.Fatalf("stored signature not restored: got %q", got)
	}

	message.ToolCalls[0].ExtraContent.Google.ThoughtSignature = "sig-from-client"
	content, err = chatMessageContent(message)
	if err != nil {
		t.Fatal(err)
	}
	if got := content.Parts[0].FunctionCall.ThoughtSignature; got != "sig-from-client" {
		t.Fatalf("client-supplied signature must win: got %q", got)
	}
}

func TestThoughtSignatureStoreIsBounded(t *testing.T) {
	for i := 0; i < thoughtSignatureLimit+10; i++ {
		rememberThoughtSignature(string(rune('a'+i%26))+string(rune(i)), "s")
	}
	thoughtSignatures.mu.Lock()
	defer thoughtSignatures.mu.Unlock()
	if len(thoughtSignatures.entries) > thoughtSignatureLimit {
		t.Fatalf("store grew past its limit: %d", len(thoughtSignatures.entries))
	}
}
