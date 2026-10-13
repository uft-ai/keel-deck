package api

import (
	"fmt"
	"net/http"
	"testing"
)

func TestProviderKindFollowsTheOfficialNanHostOnly(t *testing.T) {
	for base, want := range map[string]string{
		"https://api.nan.builders/v1":             nanProviderKind,
		"https://API.NAN.BUILDERS/v1":             nanProviderKind,
		"https://api.nan.builders.example/v1":     "",
		"https://api.nan.builders@example.com/v1": "",
		"https://nan.example.test/v1":             "",
		"://not a url":                            "",
	} {
		if got := providerKindForBaseURL(base); got != want {
			t.Errorf("providerKindForBaseURL(%q) = %q, want %q", base, got, want)
		}
	}
}

// The kind is derived on every read: a provider named "nan" elsewhere is not NaN,
// a renamed entry on the official host still is, and nothing reaches CPA's config.
func TestProviderListDerivesNanKindFromTheEndpoint(t *testing.T) {
	client, baseURL, state := startProviderTestServer(t)
	for _, body := range []string{
		`{"family":"openai-compatibility","name":"My inference","base_url":"https://api.nan.builders/v1","api_key":"sk-kind-1"}`,
		`{"family":"openai-compatibility","name":"nan","base_url":"https://relay.example.test/v1","api_key":"sk-kind-2"}`,
	} {
		resp, payload := doJSON(t, client, http.MethodPost, baseURL+"/omc/api/v1/management/providers", body)
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("create status = %d body %s", resp.StatusCode, payload)
		}
	}
	official := fmt.Sprintf("openai-compat-%d", len(state.oaiProviders)-2)
	named := fmt.Sprintf("openai-compat-%d", len(state.oaiProviders)-1)
	if got := websiteOfProvider(t, client, baseURL, official).ProviderKind; got != nanProviderKind {
		t.Fatalf("official host kind = %q", got)
	}
	if got := websiteOfProvider(t, client, baseURL, named).ProviderKind; got != "" {
		t.Fatalf("a name is not evidence of NaN, got kind %q", got)
	}
	state.mu.Lock()
	defer state.mu.Unlock()
	for _, stored := range state.oaiProviders {
		if _, present := stored["provider_kind"]; present {
			t.Fatalf("provider kind must not be stored: %#v", stored)
		}
	}
}
