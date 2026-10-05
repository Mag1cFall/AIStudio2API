package aistudio

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"
)

func fallbackTestRequest(raw string) GenerateRequest {
	return GenerateRequest{Model: "test-model", Tools: Tools{Functions: []FunctionDeclaration{{
		Name: "get_weather", Parameters: json.RawMessage(raw),
	}}}}
}

// TestPlaygroundSchemaStrictBaseline reproduces v0.2.4 without an upstream call.
func TestPlaygroundSchemaStrictBaseline(t *testing.T) {
	request := fallbackTestRequest(`{"type":"object","properties":{"location":{"type":"string"},"forecast_days":{"type":"array"}},"required":["location"]}`)
	if !requestNeedsBuildSchema(request) {
		t.Fatal("missing array items must reproduce the native-schema routing condition")
	}
	service := &PooledService{pool: NewAccountPool(nil, 1)}
	_, err := service.Generate(context.Background(), request)
	if !errors.Is(err, ErrInvalidArgument) || !strings.Contains(err.Error(), "此 JSON Schema 需要 Build 通道") {
		t.Fatalf("expected the pre-network schema error, got %v", err)
	}
}

func TestPlaygroundSchemaFallback(t *testing.T) {
	service := &PooledService{pool: NewAccountPool(nil, 1), SchemaFallback: true}
	for _, test := range []struct{ name, input, want string }{
		{"missing items", `{"type":"array"}`, `{"type":"array","items":{"type":"string"}}`},
		{"uppercase array", `{"type":"ARRAY"}`, `{"type":"ARRAY","items":{"type":"string"}}`},
		{"nested open property", `{"type":"object","properties":{"value":{}}}`, `{"type":"object","properties":{"value":{"type":"string"}}}`},
		{"typed boolean and nested array", `{"type":"object","properties":{"enabled":{"type":"boolean"},"rows":{"type":"array","items":{"type":"array"}}},"required":["enabled"],"example":{"enabled":false}}`, `{"type":"object","properties":{"enabled":{"type":"boolean"},"rows":{"type":"array","items":{"type":"array","items":{"type":"string"}}}},"required":["enabled"],"example":{"enabled":false}}`},
		{"optional nulls and zero", `{"type":"array","items":null,"minItems":0,"nullable":false,"example":null}`, `{"type":"array","items":{"type":"string"},"minItems":0,"nullable":false,"example":null}`},
	} {
		t.Run(test.name, func(t *testing.T) {
			request := fallbackTestRequest(test.input)
			request.Contents = []Content{{Role: RoleUser, Parts: []Part{{Text: "unchanged"}}}}
			before, _ := json.Marshal(request)
			got, fallback, err := service.prepareSchemaRequest(request)
			if err != nil || !fallback || requestNeedsBuildSchema(got) {
				t.Fatalf("fallback=%v err=%v request=%+v", fallback, err, got)
			}
			var actual, expected any
			_ = json.Unmarshal(got.Tools.Functions[0].Parameters, &actual)
			_ = json.Unmarshal([]byte(test.want), &expected)
			if !reflect.DeepEqual(actual, expected) {
				t.Fatalf("got %s, want %s", got.Tools.Functions[0].Parameters, test.want)
			}
			wire, err := encodeFunctionDeclaration(got.Tools.Functions[0])
			if err != nil || len(wire) == 0 {
				t.Fatalf("Playground declaration encoding failed: %v", err)
			}
			after, _ := json.Marshal(request)
			if string(before) != string(after) || !reflect.DeepEqual(request.Contents, got.Contents) {
				t.Fatal("caller-owned request changed")
			}
			again, fallback, err := service.prepareSchemaRequest(got)
			if err != nil || fallback || !reflect.DeepEqual(got, again) {
				t.Fatal("fallback must be idempotent")
			}
		})
	}
	// With no accounts, successful schema preparation must reach normal scheduling.
	_, err := service.Generate(context.Background(), fallbackTestRequest(`{"type":"array"}`))
	if !errors.Is(err, ErrNoEligibleAccount) {
		t.Fatalf("Generate did not reach scheduling: %v", err)
	}
}

func TestPlaygroundSchemaFallbackRejectsUnsupported(t *testing.T) {
	service := &PooledService{pool: NewAccountPool(nil, 1), SchemaFallback: true}
	for _, raw := range []string{
		`{"type":"array","items":true}`,
		`{"type":"array","items":false}`,
		`{"type":"object","properties":{"deny":false},"required":["deny"]}`,
		`{"allOf":[{"type":"string"},{"type":"integer"}]}`,
		`{"anyOf":[{"type":"string"},{"type":"integer"}],"not":{"type":"string"}}`,
		`{"type":"object","properties":{"v":{"not":{"description":"negative"}}}}`,
		`{"type":"object","properties":{"value":{"minimum":0}}}`,
		`{"type":"object","properties":{"open":{},"deny":{"not":true}}}`,
		`{"type":"object","properties":{"open":{},"deny":{"not":{}}}}`,
	} {
		t.Run(raw, func(t *testing.T) {
			request := fallbackTestRequest(raw)
			// A convertible sibling must not be committed if any declaration fails.
			request.Tools.Functions = append(fallbackTestRequest(`{"type":"array"}`).Tools.Functions, request.Tools.Functions...)
			got, fallback, err := service.prepareSchemaRequest(request)
			if !errors.Is(err, ErrInvalidArgument) || fallback || !reflect.DeepEqual(got, request) || string(request.Tools.Functions[0].Parameters) != `{"type":"array"}` {
				t.Fatalf("unsupported schema accepted or caller mutated: fallback=%v err=%v", fallback, err)
			}
		})
	}
}

func TestPlaygroundSchemaFallbackBoundaries(t *testing.T) {
	service := &PooledService{pool: NewAccountPool(nil, 1), SchemaFallback: true}
	t.Run("Build enabled is never downgraded even without eligible accounts", func(t *testing.T) {
		service.pool.SetUpstreamChannels([]Channel{ChannelPlayground, ChannelBuild})
		defer service.pool.SetUpstreamChannels([]Channel{ChannelPlayground})
		request := fallbackTestRequest(`{"type":"array"}`)
		got, fallback, err := service.prepareSchemaRequest(request)
		if err != nil || fallback || !reflect.DeepEqual(got, request) {
			t.Fatalf("Build schema changed: %v", err)
		}
	})
	t.Run("response schema is never narrowed", func(t *testing.T) {
		request := fallbackTestRequest(`{"type":"array"}`)
		request.Config.ResponseSchema = json.RawMessage(`{"type":"array"}`)
		got, fallback, err := service.prepareSchemaRequest(request)
		if !errors.Is(err, ErrInvalidArgument) || fallback || !reflect.DeepEqual(got, request) {
			t.Fatalf("response schema changed: %v", err)
		}
	})
	t.Run("disabled tools ignored", func(t *testing.T) {
		request := fallbackTestRequest(`{"type":"array"}`)
		request.Tools.ToolConfig.Mode = "none"
		got, fallback, err := service.prepareSchemaRequest(request)
		if err != nil || fallback || !reflect.DeepEqual(got, request) {
			t.Fatalf("disabled tools changed: %v", err)
		}
	})
	for _, raw := range []string{`{}`, `true`, `null`, `{"type":"object","properties":{"enabled":{"type":"boolean"}}}`, `{"type":"array","items":{"type":"string"}}`} {
		request := fallbackTestRequest(raw)
		got, fallback, err := service.prepareSchemaRequest(request)
		if err != nil || fallback || !reflect.DeepEqual(got, request) {
			t.Fatalf("ordinary/zero-argument tool changed: %s err=%v", raw, err)
		}
	}
}

func TestPlaygroundSchemaFallbackConcurrent(t *testing.T) {
	request := fallbackTestRequest(`{"type":"object","properties":{"tags":{"type":"array"}}}`)
	service := &PooledService{pool: NewAccountPool(nil, 1), SchemaFallback: true}
	strict := &PooledService{pool: service.pool}
	for range 12 {
		t.Run("shared request", func(t *testing.T) {
			t.Parallel()
			if got, fallback, err := service.prepareSchemaRequest(request); err != nil || !fallback || requestNeedsBuildSchema(got) {
				t.Errorf("fallback failed: %v", err)
			}
			if _, _, err := strict.prepareSchemaRequest(request); !errors.Is(err, ErrInvalidArgument) {
				t.Errorf("strict request was affected by another caller: %v", err)
			}
		})
	}
}
