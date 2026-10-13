package api

import (
	"net/http"
	"testing"
)

func seedDisabledRowProvider(fixture providerTestFixture) {
	fixture.state.mu.Lock()
	defer fixture.state.mu.Unlock()
	fixture.state.oaiProviders = []map[string]any{{
		"name": "relay", "base-url": "https://relay.test/v1",
		"api-key-entries": []any{map[string]any{"api-key": "sk-relay-1", "auth-index": "r-1"}},
		"models": []any{
			map[string]any{"name": "kept-off", "alias": "kept-off", "disabled": true, "future-field": "kept"},
			map[string]any{"name": "served", "alias": "served"},
		},
	}}
}

func storedDisabled(t *testing.T, fixture providerTestFixture, name string) bool {
	t.Helper()
	fixture.state.mu.Lock()
	defer fixture.state.mu.Unlock()
	disabled, _ := storedModel(t, fixture.state.oaiProviders[0], name)["disabled"].(bool)
	return disabled
}

func saveRows(t *testing.T, fixture providerTestFixture, rows string) {
	t.Helper()
	body := `{"family":"openai-compatibility","name":"relay","base_url":"https://relay.test/v1","keys":[{"api_key":""}],"model_entries":` + rows + `}`
	if resp, payload := doJSON(t, fixture.client, http.MethodPut, fixture.baseURL+providersEndpoint+"/openai-compat-0", body); resp.StatusCode != http.StatusOK {
		t.Fatalf("update = %d %s", resp.StatusCode, payload)
	}
}

func TestDisabledModelRowIsListed(t *testing.T) {
	fixture := newProviderTestFixture(t)
	seedDisabledRowProvider(fixture)
	entries := listedProvider(t, fixture, "openai-compat-0").ModelEntries
	if len(entries) != 2 || !entries[0].Disabled || entries[1].Disabled {
		t.Fatalf("listed rows = %+v, want the first disabled and the second served", entries)
	}
}

func TestEditWithoutTheSwitchKeepsADisabledRowOff(t *testing.T) {
	fixture := newProviderTestFixture(t)
	seedDisabledRowProvider(fixture)
	// An older client: no switch, with and without options.
	saveRows(t, fixture, `[{"name":"kept-off","alias":"kept-off"},{"name":"served","alias":"served","options":{}}]`)
	if !storedDisabled(t, fixture, "kept-off") {
		t.Fatal("an edit that did not state the switch enabled a disabled row")
	}
	saveRows(t, fixture, `[{"name":"kept-off","alias":"kept-off","options":{"display_name":"Off"}},{"name":"served","alias":"served"}]`)
	if !storedDisabled(t, fixture, "kept-off") {
		t.Fatal("an options edit that did not state the switch enabled a disabled row")
	}
	fixture.state.mu.Lock()
	kept := storedModel(t, fixture.state.oaiProviders[0], "kept-off")["future-field"]
	fixture.state.mu.Unlock()
	if kept != "kept" {
		t.Fatalf("future-field = %#v, want the unmodelled setting kept", kept)
	}
}

func TestExplicitSwitchEnablesAndDisablesARow(t *testing.T) {
	fixture := newProviderTestFixture(t)
	seedDisabledRowProvider(fixture)
	saveRows(t, fixture, `[{"name":"kept-off","alias":"kept-off","disabled":false},{"name":"served","alias":"served","disabled":true}]`)
	if storedDisabled(t, fixture, "kept-off") || !storedDisabled(t, fixture, "served") {
		t.Fatal("the explicit switch was not applied")
	}
	fixture.state.mu.Lock()
	_, isStored := storedModel(t, fixture.state.oaiProviders[0], "kept-off")["disabled"]
	fixture.state.mu.Unlock()
	if isStored {
		t.Fatal("an enabled row stored disabled: false, want the field omitted")
	}
}

func TestRenamedAliasKeepsItsDisabledChoice(t *testing.T) {
	fixture := newProviderTestFixture(t)
	seedDisabledRowProvider(fixture)
	saveRows(t, fixture, `[{"name":"kept-off","alias":"renamed","original_name":"kept-off","original_alias":"kept-off"},{"name":"served","alias":"served"}]`)
	fixture.state.mu.Lock()
	model := storedModel(t, fixture.state.oaiProviders[0], "kept-off")
	fixture.state.mu.Unlock()
	if model["alias"] != "renamed" || model["disabled"] != true {
		t.Fatalf("renamed row = %#v, want the new alias and the disabled choice", model)
	}
}

func TestNewRowWithoutTheSwitchIsEnabled(t *testing.T) {
	fixture := newProviderTestFixture(t)
	seedDisabledRowProvider(fixture)
	saveRows(t, fixture, `[{"name":"kept-off","alias":"kept-off"},{"name":"served","alias":"served"},{"name":"added"}]`)
	if storedDisabled(t, fixture, "added") {
		t.Fatal("a new row without the switch was stored disabled")
	}
}

func TestOnlyCompatibleModelsCanBeDisabled(t *testing.T) {
	disabled := true
	_, err := editedProviderModels("codex", SaveProviderRequest{ModelEntries: []SaveProviderModelEntry{{Name: "gpt-x", Disabled: &disabled}}})
	if err == nil {
		t.Fatal("a disabled model on a non-compatible family was accepted")
	}
}
