package api

import (
	"encoding/json"
	"fmt"
	"testing"

	"github.com/Mag1cFall/AIStudio2API/internal/aistudio"
)

func TestChatRestoresStoredThoughtSignature(t *testing.T) {
	store := newThoughtSignatureStore()
	store.Remember([]aistudio.FunctionCall{{ID: "call_sig_1", Name: "get_weather", Arguments: json.RawMessage(`{"city":"Berlin"}`), ThoughtSignature: "sig-abc"}})

	var request chatRequest
	raw := `{"model":"m","messages":[{"role":"user","content":"hi"},{"role":"assistant","content":null,"tool_calls":[{"id":"call_sig_1","type":"function","function":{"name":"get_weather","arguments":"{\"city\":\"Berlin\"}"}}]}]}`
	if err := json.Unmarshal([]byte(raw), &request); err != nil {
		t.Fatal(err)
	}
	generate, err := request.toGenerateRequest("id")
	if err != nil {
		t.Fatal(err)
	}
	store.Restore(generate.Contents)
	if got := generate.Contents[1].Parts[0].FunctionCall.ThoughtSignature; got != "sig-abc" {
		t.Fatalf("stored signature not restored: got %q", got)
	}

	request.Messages[1].ToolCalls[0].ExtraContent.Google.ThoughtSignature = "sig-from-client"
	generate, err = request.toGenerateRequest("id")
	if err != nil {
		t.Fatal(err)
	}
	store.Restore(generate.Contents)
	if got := generate.Contents[1].Parts[0].FunctionCall.ThoughtSignature; got != "sig-from-client" {
		t.Fatalf("client-supplied signature must win: got %q", got)
	}
}

func TestThoughtSignatureStoreIsBounded(t *testing.T) {
	store := newThoughtSignatureStore()
	for i := 0; i < thoughtSignatureCapacity+10; i++ {
		store.Remember([]aistudio.FunctionCall{{ID: fmt.Sprintf("call_%d", i), Name: "f", Arguments: json.RawMessage(`{}`), ThoughtSignature: "s"}})
	}
	if len(store.signatures) != thoughtSignatureCapacity || len(store.order) != thoughtSignatureCapacity {
		t.Fatalf("store size = %d/%d, want %d", len(store.signatures), len(store.order), thoughtSignatureCapacity)
	}
}
