package api

import (
	"encoding/json"
	"slices"
	"testing"
)

// TestAnthropicServerToolChoice 核对 tool_choice 指定 server tool 时只开启该工具对应的 Google 工具与它的选项，指定客户端函数时仍只允许该函数
func TestAnthropicServerToolChoice(t *testing.T) {
	const search = `{"type":"web_search_20250305","name":"web_search","max_uses":1}`
	const tools = search + `,{"type":"web_fetch_20250910","name":"web_fetch","allowed_domains":["example.com"]},{"name":"Read","input_schema":{"type":"object"}}`
	for _, test := range []struct {
		name, tools, choice string
		google, functions   []string
		allowed             []string
		search, fetch       bool
	}{
		{"web_search", search, "web_search", []string{"google_search"}, nil, nil, true, false},
		{"web_search with functions", tools, "web_search", []string{"google_search"}, nil, nil, true, false},
		{"web_fetch with functions", tools, "web_fetch", []string{"url_context"}, nil, nil, false, true},
		{"function", tools, "Read", []string{"google_search", "url_context"}, []string{"Read"}, []string{"Read"}, true, true},
	} {
		t.Run(test.name, func(t *testing.T) {
			var request anthropicRequest
			body := `{"model":"m","max_tokens":400,"messages":[{"role":"user","content":"x"}],"tools":[` + test.tools + `],"tool_choice":{"type":"tool","name":"` + test.choice + `"}}`
			if err := json.Unmarshal([]byte(body), &request); err != nil {
				t.Fatal(err)
			}
			got, err := request.toGenerateRequest("id")
			if err != nil {
				t.Fatal(err)
			}
			var functions []string
			for _, declaration := range got.Tools.Functions {
				functions = append(functions, declaration.Name)
			}
			config := got.Tools.ToolConfig
			if config.Mode != "required" || !slices.Equal(config.AllowedFunctionNames, test.allowed) || !slices.Equal(got.Tools.Google, test.google) || !slices.Equal(functions, test.functions) ||
				(got.Tools.GoogleSearch != nil) != test.search || (got.Tools.URLContext != nil) != test.fetch {
				t.Fatalf("tools=%+v functions=%v", got.Tools, functions)
			}
		})
	}
}
