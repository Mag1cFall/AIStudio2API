package api

import (
	"bytes"
	"encoding/json"
	"sync"

	"github.com/Mag1cFall/AIStudio2API/internal/aistudio"
)

// thoughtSignatureCapacity 是 Chat 工具调用思考签名的保留条数
const thoughtSignatureCapacity = 4096

// thoughtSignatureStore 保存本进程 Chat 响应中工具调用的思考签名
type thoughtSignatureStore struct {
	mu         sync.Mutex
	signatures map[string]string
	order      []string
}

func newThoughtSignatureStore() *thoughtSignatureStore {
	return &thoughtSignatureStore{signatures: make(map[string]string)}
}

// Remember 保存一次响应中带签名的工具调用
func (store *thoughtSignatureStore) Remember(calls []aistudio.FunctionCall) {
	store.mu.Lock()
	defer store.mu.Unlock()
	for _, call := range calls {
		if call.ID == "" || call.ThoughtSignature == "" {
			continue
		}
		key := thoughtSignatureKey(call)
		if _, exists := store.signatures[key]; !exists {
			store.order = append(store.order, key)
		}
		store.signatures[key] = call.ThoughtSignature
	}
	for len(store.order) > thoughtSignatureCapacity {
		delete(store.signatures, store.order[0])
		store.order = store.order[1:]
	}
}

// Restore 为客户端未回传签名的历史工具调用补回本进程保存的签名
func (store *thoughtSignatureStore) Restore(contents []aistudio.Content) {
	store.mu.Lock()
	defer store.mu.Unlock()
	for _, content := range contents {
		for _, part := range content.Parts {
			call := part.FunctionCall
			if call == nil || call.ID == "" || call.ThoughtSignature != "" || part.ThoughtSignature != "" {
				continue
			}
			call.ThoughtSignature = store.signatures[thoughtSignatureKey(*call)]
		}
	}
}

// thoughtSignatureKey 以调用 ID、函数名和规范化参数标识一次工具调用
func thoughtSignatureKey(call aistudio.FunctionCall) string {
	arguments := []byte(call.Arguments)
	decoder := json.NewDecoder(bytes.NewReader(arguments))
	decoder.UseNumber()
	var value any
	if decoder.Decode(&value) == nil {
		if canonical, err := json.Marshal(value); err == nil {
			arguments = canonical
		}
	}
	return call.ID + "\x00" + call.Name + "\x00" + string(arguments)
}
