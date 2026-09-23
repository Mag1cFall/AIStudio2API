package aistudio

import (
	"reflect"
	"strings"
	"testing"
)

func TestResolveSearchModelAliasStrictSuffix(t *testing.T) {
	for _, test := range []struct {
		model  string
		base   string
		search bool
	}{
		{model: "gemini-3.8-flash-search", base: "gemini-3.8-flash", search: true},
		{model: "models/gemini-3.8-flash-search", base: "models/gemini-3.8-flash", search: true},
		{model: "gemini-3.8-flash", base: "gemini-3.8-flash"},
		{model: "gemini-search-preview", base: "gemini-search-preview"},
		{model: "gemini-search-extra", base: "gemini-search-extra"},
		{model: "gemini-search ", base: "gemini-search "},
	} {
		t.Run(test.model, func(t *testing.T) {
			base, search := ResolveSearchModelAlias(test.model)
			if base != test.base || search != test.search {
				t.Fatalf("ResolveSearchModelAlias(%q) = (%q, %v), want (%q, %v)", test.model, base, search, test.base, test.search)
			}
		})
	}
}

func TestSearchAliasUnsupportedModelFailsToolValidation(t *testing.T) {
	request, enabled := ResolveSearchGenerateRequest(GenerateRequest{Model: "gemini-no-search-search"})
	if !enabled || request.Model != "gemini-no-search" {
		t.Fatalf("resolved request = %#v, enabled=%v", request, enabled)
	}
	err := validateRequestedTools(request.Tools, Model{
		ID: "gemini-no-search", Capabilities: map[string]bool{"chat_model": true},
	})
	if err == nil || !strings.Contains(err.Error(), "google_search") {
		t.Fatalf("validation error = %v, want google_search capability error", err)
	}
}

func TestModelsWithSearchAliases(t *testing.T) {
	searchCapabilities := map[string]bool{"chat_model": true, "google_search": true, "function_declarations": true}
	models := []Model{
		{
			ID: "gemini-3.8-flash", Name: "Gemini 3.8 Flash", Description: "Fast model.",
			Methods: []string{"generateContent", "countTokens"}, InputTokenLimit: 100, OutputTokenLimit: 20,
			Capabilities: searchCapabilities, CapabilityOptions: map[string][]string{"aliases": {"latest"}},
			AccessModes: []int64{1}, Paid: true,
		},
		{ID: "gemini-no-search", Name: "No Search", Methods: []string{"generateContent"}, Capabilities: map[string]bool{"chat_model": true}},
		{ID: "speech-search-capable", Name: "Speech", Methods: []string{"generateContent"}, Capabilities: map[string]bool{"google_search": true, "speech_route": true}},
		{ID: "video-search-capable", Name: "Video", Methods: []string{"predictLongRunning"}, Capabilities: map[string]bool{"google_search": true, "video_route": true}},
		{ID: "live-search-capable", Name: "Live", Methods: []string{"bidiGenerateContent"}, Capabilities: map[string]bool{"chat_model": true, "google_search": true, "live_route": true}},
	}

	got := ModelsWithSearchAliases(models)
	if len(got) != len(models)+1 {
		t.Fatalf("model count = %d, want %d: %#v", len(got), len(models)+1, got)
	}
	base, alias := got[0], got[1]
	if base.ID != "gemini-3.8-flash" || alias.ID != "gemini-3.8-flash-search" || alias.Name != "Gemini 3.8 Flash (Search)" {
		t.Fatalf("base/alias = %#v / %#v", base, alias)
	}
	if alias.InputTokenLimit != base.InputTokenLimit || alias.OutputTokenLimit != base.OutputTokenLimit || alias.Paid != base.Paid ||
		!reflect.DeepEqual(alias.Methods, base.Methods) || !reflect.DeepEqual(alias.Capabilities, base.Capabilities) ||
		!reflect.DeepEqual(alias.CapabilityOptions, base.CapabilityOptions) || !reflect.DeepEqual(alias.AccessModes, base.AccessModes) {
		t.Fatalf("alias did not inherit base metadata: base=%#v alias=%#v", base, alias)
	}
	if !strings.Contains(alias.Description, "Automatically enables Google Search") {
		t.Fatalf("alias description = %q", alias.Description)
	}
	for _, model := range got {
		if model.ID == "gemini-no-search-search" || model.ID == "speech-search-capable-search" ||
			model.ID == "video-search-capable-search" || model.ID == "live-search-capable-search" {
			t.Fatalf("ineligible alias exposed: %q", model.ID)
		}
	}

	alias.Capabilities["mutated"] = true
	alias.CapabilityOptions["aliases"][0] = "mutated"
	if base.Capabilities["mutated"] || base.CapabilityOptions["aliases"][0] != "latest" {
		t.Fatal("alias metadata shares mutable state with base model")
	}
}
