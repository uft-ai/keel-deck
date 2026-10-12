package api

import (
	"fmt"
	"net/http"
	"strings"
	"testing"
)

func storedModel(t *testing.T, provider map[string]any, name string) map[string]any {
	t.Helper()
	for _, raw := range anyList(provider["models"]) {
		if model := raw.(map[string]any); model["name"] == name {
			return model
		}
	}
	t.Fatalf("model %s is not stored: %#v", name, provider["models"])
	return nil
}

func TestProviderModelOptionsRoundTrip(t *testing.T) {
	fixture := newProviderTestFixture(t)
	fixture.state.mu.Lock()
	fixture.state.oaiProviders = []map[string]any{{
		"name": "relay", "base-url": "https://relay.test/v1",
		"api-key-entries": []any{map[string]any{"api-key": "sk-relay-1", "auth-index": "r-1"}},
		"models": []any{map[string]any{
			"name": "gpt-x", "alias": "x", "display-name": "GPT X", "max-context-length": float64(128000),
			"input-modalities": []any{"text", "image"}, "use-max-completion-tokens": true,
			"thinking":     map[string]any{"min": float64(1024), "max": float64(32768), "levels": []any{"low", "high"}},
			"future-field": "kept",
		}},
	}}
	fixture.state.mu.Unlock()

	listed := listedProvider(t, fixture, "openai-compat-0").ModelEntries[0]
	if listed.Options == nil || listed.Options.DisplayName != "GPT X" || listed.Options.MaxContextLength != 128000 ||
		fmt.Sprint(listed.Options.InputModalities) != "[text image]" || !listed.Options.UseMaxCompletionTokens ||
		listed.Options.ThinkingMin != 1024 || listed.Options.ThinkingMax != 32768 {
		t.Fatalf("listed options = %+v", listed.Options)
	}

	body := `{"family":"openai-compatibility","name":"relay","base_url":"https://relay.test/v1","keys":[{"api_key":""}],
		"model_entries":[{"name":"gpt-x","alias":"x","thinking":{"levels":["low"]},"options":{
			"display_name":" GPT X2 ","force_mapping":true,"is_compat":true,
			"input_modalities":[" Text ","text","AUDIO"],"thinking_max":8192,"thinking_dynamic_allowed":true}}]}`
	if resp, payload := doJSON(t, fixture.client, http.MethodPut, fixture.baseURL+providersEndpoint+"/openai-compat-0", body); resp.StatusCode != http.StatusOK {
		t.Fatalf("update = %d %s", resp.StatusCode, payload)
	}

	fixture.state.mu.Lock()
	model := storedModel(t, fixture.state.oaiProviders[0], "gpt-x")
	fixture.state.mu.Unlock()
	if model["display-name"] != "GPT X2" || model["force-mapping"] != true || model["is-compat"] != true {
		t.Fatalf("stored model = %#v, want the stated settings under CPA's wire names", model)
	}
	if fmt.Sprint(model["input-modalities"]) != "[text audio]" {
		t.Fatalf("input-modalities = %#v, want them lowercased and deduplicated", model["input-modalities"])
	}
	// Options state the whole entry: a setting left out of them is cleared.
	for _, field := range []string{"max-context-length", "use-max-completion-tokens"} {
		if _, isStored := model[field]; isStored {
			t.Fatalf("%s = %#v, want it cleared by options that omit it", field, model[field])
		}
	}
	thinking := model["thinking"].(map[string]any)
	if _, hasMin := thinking["min"]; hasMin || thinking["max"] != float64(8192) || thinking["dynamic_allowed"] != true || fmt.Sprint(thinking["levels"]) != "[low]" {
		t.Fatalf("thinking = %#v, want the stated bounds beside the row's levels", thinking)
	}
	if model["future-field"] != "kept" {
		t.Fatalf("future-field = %#v, want a setting the console does not model kept", model["future-field"])
	}
}

func TestSharedUpstreamNameKeepsDistinctAliasMetadata(t *testing.T) {
	fixture := newProviderTestFixture(t)
	fixture.state.mu.Lock()
	fixture.state.oaiProviders = []map[string]any{{
		"name": "nan", "prefix": "nan", "base-url": "https://api.nan.builders/v1",
		"api-key-entries": []any{map[string]any{"api-key": "sk-nan", "auth-index": "nan-1"}},
		"models": []any{
			map[string]any{"name": "qwen3.6", "alias": "qwen3.6"},
			map[string]any{"name": "qwen3.6", "alias": "qwen3.6-native", "operations": []any{"chat", "responses", "completions"}, "native": []any{"responses", "completions"}, "max-output-tokens": float64(8192)},
		},
	}}
	fixture.state.mu.Unlock()

	body := `{"family":"openai-compatibility","name":"nan","prefix":"nan","base_url":"https://api.nan.builders/v1","keys":[{"api_key":""}],
		"model_entries":[
			{"name":"qwen3.6","alias":"qwen3.6","options":{}},
			{"name":"qwen3.6","alias":"qwen3.6-native","options":{"operations":["Chat","responses","completions"],"native":["responses","completions"],"max_output_tokens":8192}}
		]}`
	if resp, payload := doJSON(t, fixture.client, http.MethodPut, fixture.baseURL+providersEndpoint+"/openai-compat-0", body); resp.StatusCode != http.StatusOK {
		t.Fatalf("update = %d %s", resp.StatusCode, payload)
	}
	fixture.state.mu.Lock()
	models := anyList(fixture.state.oaiProviders[0]["models"])
	fixture.state.mu.Unlock()
	if len(models) != 2 {
		t.Fatalf("stored models = %d, want both aliases", len(models))
	}
	native := models[1].(map[string]any)
	if native["alias"] != "qwen3.6-native" || fmt.Sprint(native["native"]) != "[responses completions]" || native["max-output-tokens"] != float64(8192) {
		t.Fatalf("native route = %#v", native)
	}
	translated := models[0].(map[string]any)
	if _, hasNative := translated["native"]; hasNative {
		t.Fatalf("translated route inherited native metadata: %#v", translated)
	}
}

func TestCaseDistinctUpstreamNamesStaySeparate(t *testing.T) {
	fixture := newProviderTestFixture(t)
	fixture.state.mu.Lock()
	fixture.state.oaiProviders = []map[string]any{{
		"name": "nan", "base-url": "https://api.nan.builders/v1",
		"api-key-entries": []any{map[string]any{"api-key": "sk-nan", "auth-index": "nan-1"}},
		"models": []any{
			map[string]any{"name": "Qwen3.6", "alias": "upper", "future-field": "upper"},
			map[string]any{"name": "qwen3.6", "alias": "lower", "future-field": "lower"},
		},
	}}
	fixture.state.mu.Unlock()

	body := `{"family":"openai-compatibility","name":"nan","base_url":"https://api.nan.builders/v1","keys":[{"api_key":""}],
		"model_entries":[
			{"name":"Qwen3.6","alias":"upper","original_name":"Qwen3.6","original_alias":"upper"},
			{"name":"qwen3.6","alias":"lower","original_name":"qwen3.6","original_alias":"lower"}
		]}`
	if resp, payload := doJSON(t, fixture.client, http.MethodPut, fixture.baseURL+providersEndpoint+"/openai-compat-0", body); resp.StatusCode != http.StatusOK {
		t.Fatalf("update = %d %s", resp.StatusCode, payload)
	}
	fixture.state.mu.Lock()
	models := anyList(fixture.state.oaiProviders[0]["models"])
	fixture.state.mu.Unlock()
	if models[0].(map[string]any)["future-field"] != "upper" || models[1].(map[string]any)["future-field"] != "lower" {
		t.Fatalf("case-distinct routes merged: %#v", models)
	}
}

func TestRenameOneAliasOfSharedUpstreamName(t *testing.T) {
	fixture := newProviderTestFixture(t)
	fixture.state.mu.Lock()
	fixture.state.oaiProviders = []map[string]any{{
		"name": "nan", "base-url": "https://api.nan.builders/v1",
		"api-key-entries": []any{map[string]any{"api-key": "sk-nan", "auth-index": "nan-1"}},
		"models": []any{
			map[string]any{"name": "qwen3.6", "alias": "qwen3.6", "future-field": "translated"},
			map[string]any{"name": "qwen3.6", "alias": "qwen3.6-native", "native": []any{"responses"}, "future-field": "native"},
		},
	}}
	fixture.state.mu.Unlock()

	body := `{"family":"openai-compatibility","name":"nan","base_url":"https://api.nan.builders/v1","keys":[{"api_key":""}],
		"model_entries":[
			{"name":"qwen3.6","alias":"qwen3.6-native","original_name":"qwen3.6","original_alias":"qwen3.6-native"},
			{"name":"qwen3.6","alias":"qwen3.6","original_name":"qwen3.6","original_alias":"qwen3.6"}
		]}`
	if resp, payload := doJSON(t, fixture.client, http.MethodPut, fixture.baseURL+providersEndpoint+"/openai-compat-0", body); resp.StatusCode != http.StatusOK {
		t.Fatalf("update = %d %s", resp.StatusCode, payload)
	}
	fixture.state.mu.Lock()
	models := anyList(fixture.state.oaiProviders[0]["models"])
	fixture.state.mu.Unlock()
	first := models[0].(map[string]any)
	second := models[1].(map[string]any)
	if first["future-field"] != "native" || fmt.Sprint(first["native"]) != "[responses]" {
		t.Fatalf("reordered native route lost its fields: %#v", first)
	}
	if second["future-field"] != "translated" {
		t.Fatalf("translated route = %#v", second)
	}
	if _, hasNative := second["native"]; hasNative {
		t.Fatalf("translated route inherited native: %#v", second)
	}
}

// The agent's provider operations and older clients send rows without options.
func TestProviderModelRowWithoutOptionsKeepsStoredOptions(t *testing.T) {
	fixture := newProviderTestFixture(t)
	fixture.state.mu.Lock()
	fixture.state.codexProviders = []map[string]any{{
		"api-key": "sk-codex-1", "auth-index": "cx-1", "base-url": "https://codex.test/v1",
		"models": []any{map[string]any{
			"name": "gpt-5", "alias": "five", "display-name": "Five", "support-configuration-update": true,
			"thinking": map[string]any{"max": float64(4096), "levels": []any{"low"}},
		}},
	}}
	fixture.state.mu.Unlock()

	body := `{"family":"codex","name":"Codex","base_url":"https://codex.test/v1","keys":[{"api_key":""}],
		"model_entries":[{"name":"gpt-5","alias":"five-renamed"}]}`
	if resp, payload := doJSON(t, fixture.client, http.MethodPut, fixture.baseURL+providersEndpoint+"/codex-0", body); resp.StatusCode != http.StatusOK {
		t.Fatalf("update = %d %s", resp.StatusCode, payload)
	}

	fixture.state.mu.Lock()
	model := storedModel(t, fixture.state.codexProviders[0], "gpt-5")
	fixture.state.mu.Unlock()
	if model["alias"] != "five-renamed" || model["display-name"] != "Five" || model["support-configuration-update"] != true {
		t.Fatalf("stored model = %#v, want the alias changed and the options kept", model)
	}
	thinking := model["thinking"].(map[string]any)
	if _, hasLevels := thinking["levels"]; hasLevels || thinking["max"] != float64(4096) {
		t.Fatalf("thinking = %#v, want the row's empty levels applied and the bound kept", thinking)
	}
}

// Original identity finds the stored row. The requested name is still applied,
// and an identity that names nothing is refused instead of appended.
func TestProviderModelRowWithoutOptionsRenamesUpstreamByOriginalIdentity(t *testing.T) {
	fixture := newProviderTestFixture(t)
	fixture.state.mu.Lock()
	fixture.state.oaiProviders = []map[string]any{{
		"name": "nan", "base-url": "https://api.nan.builders/v1",
		"api-key-entries": []any{map[string]any{"api-key": "sk-nan", "auth-index": "nan-1"}},
		"models": []any{map[string]any{
			"name": "old", "alias": "route", "display-name": "Kept", "future-field": "kept",
		}},
	}}
	fixture.state.mu.Unlock()

	rename := `{"family":"openai-compatibility","name":"nan","base_url":"https://api.nan.builders/v1","keys":[{"api_key":""}],
		"model_entries":[{"name":"new","alias":"route","original_name":"old","original_alias":"route"}]}`
	if resp, payload := doJSON(t, fixture.client, http.MethodPut, fixture.baseURL+providersEndpoint+"/openai-compat-0", rename); resp.StatusCode != http.StatusOK {
		t.Fatalf("rename = %d %s", resp.StatusCode, payload)
	}
	fixture.state.mu.Lock()
	models := anyList(fixture.state.oaiProviders[0]["models"])
	fixture.state.mu.Unlock()
	if len(models) != 1 {
		t.Fatalf("models = %#v, want the renamed row only", models)
	}
	model := models[0].(map[string]any)
	if model["name"] != "new" || model["alias"] != "route" || model["display-name"] != "Kept" || model["future-field"] != "kept" {
		t.Fatalf("renamed model = %#v, want name new and the stored fields kept", model)
	}

	stale := `{"family":"openai-compatibility","name":"nan","base_url":"https://api.nan.builders/v1","keys":[{"api_key":""}],
		"model_entries":[{"name":"other","alias":"route","original_name":"missing","original_alias":"route"}]}`
	resp, payload := doJSON(t, fixture.client, http.MethodPut, fixture.baseURL+providersEndpoint+"/openai-compat-0", stale)
	if resp.StatusCode != http.StatusBadRequest || !strings.Contains(string(payload), "missing") {
		t.Fatalf("stale identity = %d %s", resp.StatusCode, payload)
	}
	fixture.state.mu.Lock()
	defer fixture.state.mu.Unlock()
	models = anyList(fixture.state.oaiProviders[0]["models"])
	if len(models) != 1 || models[0].(map[string]any)["name"] != "new" {
		t.Fatalf("refused save wrote %#v", models)
	}
}

func TestProviderModelOptionsRefuseWhatCPAWouldReject(t *testing.T) {
	fixture := newProviderTestFixture(t)
	fixture.state.mu.Lock()
	fixture.state.codexProviders = []map[string]any{{"api-key": "sk-codex-1", "auth-index": "cx-1", "base-url": "https://codex.test/v1"}}
	*fixture.state.providerList("vertex") = []map[string]any{{"api-key": "sk-vertex-1", "auth-index": "vx-1"}}
	fixture.state.mu.Unlock()

	cases := []struct {
		id, family, options, reason string
	}{
		{"codex-0", "codex", `{"use_max_completion_tokens":true}`, "no use_max_completion_tokens"},
		{"codex-0", "codex", `{"input_modalities":["text"]}`, "no modalities"},
		{"vertex-0", "vertex", `{"max_context_length":1000}`, "no max_context_length"},
		{"vertex-0", "vertex", `{"is_compat":true}`, "no is_compat"},
		{"vertex-0", "vertex", `{"support_configuration_update":true}`, "no support_configuration_update"},
		{"codex-0", "codex", `{"max_context_length":-1}`, "must not be negative"},
		{"codex-0", "codex", `{"thinking_min":9000,"thinking_max":100}`, "must not exceed"},
		{"codex-0", "codex", `{"thinking_min":-1}`, "must not be negative"},
	}
	for _, testCase := range cases {
		body := fmt.Sprintf(`{"family":%q,"name":"p","base_url":"https://codex.test/v1","keys":[{"api_key":""}],
			"model_entries":[{"name":"m","options":%s}]}`, testCase.family, testCase.options)
		resp, payload := doJSON(t, fixture.client, http.MethodPut, fixture.baseURL+providersEndpoint+"/"+testCase.id, body)
		if resp.StatusCode != http.StatusBadRequest || !strings.Contains(string(payload), testCase.reason) {
			t.Errorf("%s %s = %d %s, want 400 naming %q", testCase.family, testCase.options, resp.StatusCode, payload, testCase.reason)
		}
	}

	fixture.state.mu.Lock()
	defer fixture.state.mu.Unlock()
	if models := fixture.state.codexProviders[0]["models"]; len(anyList(models)) != 0 {
		t.Fatalf("models = %#v, want a refused save to write nothing", models)
	}
}
