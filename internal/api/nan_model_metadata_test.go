package api

import (
	"reflect"
	"testing"
)

func TestNanModelDefaultsUseOnlyDiscoveredDocumentedIdentities(t *testing.T) {
	discovered := []string{"qwen3.8-flash", "qwen3.6", "gemma4", "flux-2-klein", "qwen-image-2.1", "whisper", "kokoro", "qwen3-embedding", "rerank", "future-model", "vendor/qwen3.8-flash"}
	models, source := nanModelDefaults("https://API.NAN.BUILDERS/v1", discovered)
	if source == nil || source.Source != "https://nan.builders/openapi.json" || source.CheckedAt != "2026-10-12" || len(models) != 9 {
		t.Fatalf("metadata provenance/catalog: %+v, %v", source, models)
	}
	if _, added := models["glm5.3"]; added {
		t.Fatal("premium model was added without discovery")
	}
	if models["qwen3.8-flash"].Options.MaxContextLength != 1_000_000 || models["qwen3.6"].Options.MaxContextLength != 0 || models["gemma4"].Options.MaxContextLength != 0 {
		t.Fatal("documented and unknown context lengths were conflated")
	}
	for name, model := range models {
		if model.Options.MaxOutputTokens != 0 || len(model.Options.Native) != 0 {
			t.Fatalf("invented output limit or changed default protocol: %s", name)
		}
		if _, err := normalizedProviderModelOptions(openAICompatibilityFamily, name, *model.Options); err != nil {
			t.Fatalf("unwritable model options: %s: %v", name, err)
		}
	}
	if !models["flux-2-klein"].Image || !reflect.DeepEqual(models["flux-2-klein"].Options.Operations, []string{"image-generation", "image-edit"}) ||
		!reflect.DeepEqual(models["qwen-image-2.1"].Options.Operations, []string{"image-generation"}) {
		t.Fatal("image capabilities were merged across models")
	}
	if !reflect.DeepEqual(models["qwen3.8-flash"].Thinking.Levels, []string{"none", "minimal", "low", "medium", "high", "max"}) {
		t.Fatal("explicit reasoning levels would be coerced by generic metadata")
	}
	for _, base := range []string{"https://example.com/v1", "https://api.nan.builders.example/v1", "https://api.nan.builders@example.com/v1"} {
		if models, source := nanModelDefaults(base, discovered); models != nil || source != nil {
			t.Fatalf("NaN metadata applied to another host: %s", base)
		}
	}
}
