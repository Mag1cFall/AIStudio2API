package api

// Gemini 3 refuses a functionCall that comes back in history without the
// thought signature it was issued with:
//
//	AI Studio GenerateContent 返回 HTTP 400、协议错误码 3:
//	[original: beyond::dependency::INVALID_ARGUMENT]
//	Function call is missing a thought signature. (qos=CRITICAL_PLUS)
//
// The signature does reach the client - this proxy hands it over as
// extra_content.google.thought_signature on every tool call - but no ordinary
// OpenAI client (OpenAI SDKs, litellm, most agent frameworks) echoes unknown
// fields back. By the next turn of a tool round trip the signature is gone,
// so every agent loop dies on the second request while both tool-call checks
// pass.
//
// Keep it server-side instead, keyed by the tool call id the client saw, and
// put it back when the assistant message comes home without one. A client that
// does echo extra_content still wins: chatMessageContent only consults this
// store when the field is missing, and a call this process never emitted still
// falls through to the skip_thought_signature_validator placeholder in
// internal/aistudio/request.go.

import (
	"sync"

	"github.com/Mag1cFall/AIStudio2API/internal/aistudio"
)

// thoughtSignatureLimit bounds the store. Tool call ids are unique per call
// and a client only ever echoes back ids from the recent past, so a plain FIFO
// cap is enough - it keeps a long-lived process from growing one entry per
// tool call forever.
const thoughtSignatureLimit = 1024

var thoughtSignatures = struct {
	mu      sync.Mutex
	entries map[string]string
	order   []string
}{entries: make(map[string]string)}

func rememberThoughtSignature(id, signature string) {
	if id == "" || signature == "" {
		return
	}
	thoughtSignatures.mu.Lock()
	defer thoughtSignatures.mu.Unlock()
	if _, seen := thoughtSignatures.entries[id]; !seen {
		thoughtSignatures.order = append(thoughtSignatures.order, id)
	}
	thoughtSignatures.entries[id] = signature
	for len(thoughtSignatures.order) > thoughtSignatureLimit {
		oldest := thoughtSignatures.order[0]
		thoughtSignatures.order = thoughtSignatures.order[1:]
		delete(thoughtSignatures.entries, oldest)
	}
}

func thoughtSignatureFor(id string) string {
	if id == "" {
		return ""
	}
	thoughtSignatures.mu.Lock()
	defer thoughtSignatures.mu.Unlock()
	return thoughtSignatures.entries[id]
}

// rememberThoughtSignatures stores every signed call of one model response.
// Calls the model did not sign are skipped, so a parallel response can leave
// some ids unknown - that is the model's choice, not something to invent here.
func rememberThoughtSignatures(calls []aistudio.FunctionCall) {
	for _, call := range calls {
		rememberThoughtSignature(call.ID, call.ThoughtSignature)
	}
}
