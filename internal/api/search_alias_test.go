package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"

	"github.com/Mag1cFall/AIStudio2API/internal/aistudio"
)

func TestGeminiSearchAliasInjectsGoogleSearch(t *testing.T) {
	var request geminiRequest
	if err := json.Unmarshal([]byte(`{"contents":[{"parts":[{"text":"hello"}]}]}`), &request); err != nil {
		t.Fatal(err)
	}
	generateRequest, err := request.toGenerateRequest("test-id", "gemini-3.8-flash-search")
	if err != nil {
		t.Fatal(err)
	}
	service := &searchAliasCaptureService{}
	if _, err := (&server{service: service}).generate(context.Background(), generateRequest); err != nil {
		t.Fatal(err)
	}
	assertResolvedSearchAlias(t, service.generated, true)
}

func TestOpenAISearchAliasInjectsGoogleSearch(t *testing.T) {
	var request chatRequest
	if err := json.Unmarshal([]byte(`{"model":"gemini-3.8-flash-search","messages":[{"role":"user","content":"hello"}]}`), &request); err != nil {
		t.Fatal(err)
	}
	generateRequest, err := request.toGenerateRequest("test-id")
	if err != nil {
		t.Fatal(err)
	}
	service := &searchAliasCaptureService{}
	if _, err := (&server{service: service}).generate(context.Background(), generateRequest); err != nil {
		t.Fatal(err)
	}
	assertResolvedSearchAlias(t, service.generated, true)
}

func TestRegularModelDoesNotInjectGoogleSearch(t *testing.T) {
	request := aistudio.GenerateRequest{Model: "gemini-3.8-flash"}
	resolved, enabled := aistudio.ResolveSearchGenerateRequest(request)
	if enabled || resolved.Model != request.Model || resolved.Tools.GoogleSearch != nil {
		t.Fatalf("regular model changed: %#v, enabled=%v", resolved, enabled)
	}
}

func TestSearchAliasPreservesFunctionDeclarations(t *testing.T) {
	function := aistudio.FunctionDeclaration{Name: "lookup", Parameters: json.RawMessage(`{"type":"object"}`)}
	request := aistudio.GenerateRequest{
		Model: "gemini-3.8-flash-search",
		Tools: aistudio.Tools{Functions: []aistudio.FunctionDeclaration{function}},
	}
	resolved, enabled := aistudio.ResolveSearchGenerateRequest(request)
	assertResolvedSearchAlias(t, resolved, enabled)
	if !reflect.DeepEqual(resolved.Tools.Functions, []aistudio.FunctionDeclaration{function}) {
		t.Fatalf("function declarations changed: %#v", resolved.Tools.Functions)
	}
}

func TestSearchAliasDoesNotDuplicateExistingGoogleSearch(t *testing.T) {
	request := aistudio.GenerateRequest{
		Model:    "gemini-3.8-flash-search",
		Contents: []aistudio.Content{{Role: aistudio.RoleUser, Parts: []aistudio.Part{{Text: "hello"}}}},
		Tools:    aistudio.Tools{GoogleSearch: &aistudio.GoogleSearchOptions{WebSearch: true}},
	}
	resolved, enabled := aistudio.ResolveSearchGenerateRequest(request)
	assertResolvedSearchAlias(t, resolved, enabled)
	wire := encodeSearchAliasGenerateRequest(t, resolved)
	tools, ok := wire[6].([]any)
	if !ok || len(tools) != 1 {
		t.Fatalf("wire search tools = %#v, want exactly one", wire[6])
	}
}

func TestSearchAliasEncodeGenerateContentUsesBaseModelAndExistingSearchWire(t *testing.T) {
	request, enabled := aistudio.ResolveSearchGenerateRequest(aistudio.GenerateRequest{
		Model:    "gemini-3.8-flash-search",
		Contents: []aistudio.Content{{Role: aistudio.RoleUser, Parts: []aistudio.Part{{Text: "hello"}}}},
	})
	assertResolvedSearchAlias(t, request, enabled)
	wire := encodeSearchAliasGenerateRequest(t, request)
	if wire[0] != "models/gemini-3.8-flash" {
		t.Fatalf("wire model = %#v", wire[0])
	}
	wantSearch := []any{nil, nil, nil, []any{nil, []any{[]any{}}}}
	tools, ok := wire[6].([]any)
	if !ok || len(tools) != 1 || !reflect.DeepEqual(tools[0], wantSearch) {
		t.Fatalf("wire tools = %#v, want %#v", wire[6], []any{wantSearch})
	}
}

func TestGeminiStreamGenerateContentResolvesSearchAlias(t *testing.T) {
	service := &searchAliasCaptureService{}
	s := &server{service: service}
	request := aistudio.GenerateRequest{
		ID: "response-id", Model: "gemini-3.8-flash-search",
		Contents: []aistudio.Content{{Role: aistudio.RoleUser, Parts: []aistudio.Part{{Text: "hello"}}}},
	}
	recorder := httptest.NewRecorder()
	httpRequest := httptest.NewRequest("POST", "/v1beta/models/gemini-3.8-flash-search:streamGenerateContent", nil)
	s.handleGeminiGenerate(recorder, httpRequest, request, true)
	assertResolvedSearchAlias(t, service.generated, true)
}

func TestOpenAIModelsExposeEligibleSearchAlias(t *testing.T) {
	service := &searchAliasCaptureService{models: []aistudio.Model{
		{
			ID: "gemini-3.8-flash", Name: "Gemini 3.8 Flash", Methods: []string{"generateContent"},
			Capabilities: map[string]bool{"chat_model": true, "google_search": true},
		},
		{
			ID: "gemini-no-search", Name: "Gemini No Search", Methods: []string{"generateContent"},
			Capabilities: map[string]bool{"chat_model": true},
		},
	}}
	s := &server{service: service}
	recorder := httptest.NewRecorder()
	s.handleOpenAIModels(recorder, httptest.NewRequest(http.MethodGet, "/v1/models", nil))
	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, body=%s", recorder.Code, recorder.Body.String())
	}
	var response struct {
		Data []struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	ids := make(map[string]bool, len(response.Data))
	for _, model := range response.Data {
		ids[model.ID] = true
	}
	for _, id := range []string{"gemini-3.8-flash", "gemini-3.8-flash-search", "gemini-no-search"} {
		if !ids[id] {
			t.Errorf("missing model %q: %#v", id, ids)
		}
	}
	if ids["gemini-no-search-search"] {
		t.Fatalf("unsupported Search alias exposed: %#v", ids)
	}
}

func assertResolvedSearchAlias(t *testing.T, request aistudio.GenerateRequest, enabled bool) {
	t.Helper()
	if !enabled {
		t.Fatal("Search alias was not recognized")
	}
	if request.Model != "gemini-3.8-flash" {
		t.Fatalf("resolved model = %q", request.Model)
	}
	if request.Tools.GoogleSearch == nil || !request.Tools.GoogleSearch.WebSearch {
		t.Fatalf("Google Search not injected: %#v", request.Tools)
	}
}

type searchAliasCaptureService struct {
	generated aistudio.GenerateRequest
	models    []aistudio.Model
}

func (service *searchAliasCaptureService) Models(context.Context) ([]aistudio.Model, error) {
	return service.models, nil
}

func (service *searchAliasCaptureService) CountTokens(context.Context, aistudio.TokenCountRequest) (aistudio.TokenCount, error) {
	return aistudio.TokenCount{}, nil
}

func (service *searchAliasCaptureService) Generate(_ context.Context, request aistudio.GenerateRequest) (<-chan aistudio.Event, error) {
	service.generated = request
	events := make(chan aistudio.Event, 1)
	events <- aistudio.Event{Kind: aistudio.EventFinish, FinishReason: "stop"}
	close(events)
	return events, nil
}

func encodeSearchAliasGenerateRequest(t *testing.T, request aistudio.GenerateRequest) []any {
	t.Helper()
	body, err := aistudio.EncodeGenerateContentRequest(
		request,
		aistudio.GenerationDefaults{MaxOutputTokens: 1024},
		aistudio.RequestContext{},
	)
	if err != nil {
		t.Fatal(err)
	}
	var wire []any
	if err := json.Unmarshal(body, &wire); err != nil {
		t.Fatal(err)
	}
	return wire
}
