package management

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"
)

func TestConfiguredQuotaSubjectsFollowHostStaySecretFreeAndUnambiguous(t *testing.T) {
	const secret = "sk-nan-secret-value-1234"
	providers := []map[string]any{
		{"name": "nan", "base-url": "https://api.nan.builders/v1", "api-key-entries": []any{
			map[string]any{"api-key": secret, "auth-index": "nan-1"},
			map[string]any{"api-key": "sk-second-secret-value", "auth-index": "nan-2"},
			map[string]any{"api-key": "sk-missing-index-value"},
		}},
		{"name": "NAN", "disabled": true, "base-url": "https://api.nan.builders/v1", "api-key-entries": []any{
			map[string]any{"api-key": "sk-disabled-secret-value", "auth-index": "nan-disabled"},
			map[string]any{"api-key": "sk-repeated-secret-value", "auth-index": "nan-repeat"},
		}},
		{"name": "other", "base-url": "https://other.example/v1", "api-key-entries": []any{
			map[string]any{"api-key": "sk-other-secret-value", "auth-index": "other-1"},
		}},
		{"name": "Renamed NaN", "base-url": "https://api.nan.builders/v1", "api-key-entries": []any{
			map[string]any{"api-key": "sk-renamed-secret-value", "auth-index": "nan-renamed"},
		}},
		{"name": "Lookalike", "base-url": "https://api.nan.builders.example/v1", "api-key-entries": []any{
			map[string]any{"api-key": "sk-lookalike-secret-value", "auth-index": "lookalike-1"},
		}},
		// The name selects nothing: an entry named nan on another host has no quota reader.
		{"name": "nan", "base-url": "http://172.31.77.10:8090/v1", "api-key-entries": []any{
			map[string]any{"api-key": "sk-elsewhere-secret-value", "auth-index": "nan-elsewhere"},
		}},
		{"name": "nan", "base-url": "https://api.nan.builders/v1", "api-key-entries": []any{
			map[string]any{"api-key": "sk-repeat-again-secret", "auth-index": "nan-repeat"},
		}},
	}
	body, err := json.Marshal(map[string]any{"openai-compatibility": providers})
	if err != nil {
		t.Fatal(err)
	}
	server := newV8Server(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.URL.Path != "/v0/management/openai-compatibility" {
			t.Fatalf("path = %s", request.URL.Path)
		}
		_, _ = writer.Write(body)
	}))
	defer server.Close()
	client, err := NewClient(server.URL, "management-secret", time.Second, false)
	if err != nil {
		t.Fatal(err)
	}

	got, err := client.ConfiguredQuotaSubjects(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	encoded, err := json.Marshal(got)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(encoded), secret) || strings.Contains(string(encoded), "sk-") {
		t.Fatalf("subject projection contains a secret: %s", encoded)
	}
	if got.Subjects[0].Label != "nan nan-1" {
		t.Fatalf("label = %q", got.Subjects[0].Label)
	}
	if len(got.Subjects) != 4 {
		t.Fatalf("subjects = %#v", got.Subjects)
	}
	if got.Subjects[0].AuthIndex != "nan-1" || got.Subjects[0].Provider != "nan" || got.Subjects[0].Channel != "openai-compatible-nan" || got.Subjects[0].Disabled {
		t.Fatalf("first subject = %#v", got.Subjects[0])
	}
	if got.Subjects[1].AuthIndex != "nan-2" || got.Subjects[2].AuthIndex != "nan-disabled" || !got.Subjects[2].Disabled {
		t.Fatalf("remaining subjects = %#v", got.Subjects[1:])
	}
	selected, ok := got.Subject("nan-2")
	if !ok || selected.AuthIndex != "nan-2" {
		t.Fatalf("selected = %#v %v", selected, ok)
	}
	if _, ok := got.Subject("nan-disabled"); ok {
		t.Fatal("disabled subject was selectable")
	}
	if _, ok := got.Subject("nan-repeat"); ok {
		t.Fatal("duplicate index was selectable")
	}
	if _, ok := got.Subject("other-1"); ok {
		t.Fatal("unrelated provider was selectable")
	}
	renamed, ok := got.Subject("nan-renamed")
	if !ok || renamed.Provider != "nan" || renamed.Channel != "openai-compatible-renamed nan" || renamed.Label != "Renamed NaN nan-renamed" {
		t.Fatalf("renamed subject = %#v %v", renamed, ok)
	}
	if _, ok := got.Subject("lookalike-1"); ok {
		t.Fatal("lookalike host was selectable")
	}
	if _, ok := got.Subject("nan-elsewhere"); ok {
		t.Fatal("an entry named nan on another host was selectable")
	}
	reasons := map[string]ConfiguredQuotaDiagnostic{}
	for _, diagnostic := range got.Diagnostics {
		reasons[diagnostic.Reason] = diagnostic
	}
	if len(reasons) != 2 || reasons["missing_auth_index"].Channel != "openai-compatible-nan" {
		t.Fatalf("diagnostics = %#v", got.Diagnostics)
	}
	if reasons["duplicate_auth_index"].Count != 2 || reasons["duplicate_auth_index"].AuthIndex != "nan-repeat" {
		t.Fatalf("duplicate diagnostic = %#v", reasons["duplicate_auth_index"])
	}
}

func TestConfiguredQuotaSubjectsAllowNoKeys(t *testing.T) {
	server := newV8Server(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		_, _ = writer.Write([]byte(`{"openai-compatibility":[{"name":"other","api-key-entries":[{"api-key":"sk-other","auth-index":"other-1"}]}]}`))
	}))
	defer server.Close()
	client, err := NewClient(server.URL, "management-secret", time.Second, false)
	if err != nil {
		t.Fatal(err)
	}
	got, err := client.ConfiguredQuotaSubjects(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Subjects) != 0 || len(got.Diagnostics) != 0 {
		t.Fatalf("empty nan configuration = %#v", got)
	}
}
