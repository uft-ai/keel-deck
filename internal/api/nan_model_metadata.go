package api

import "github.com/oh-my-cpa/oh-my-cpa/internal/cpa/management"

type modelPullMetadata struct {
	Source    string `json:"source"`
	CheckedAt string `json:"checked_at"`
	Basis     string `json:"basis"`
}

// nanProviderKind is the read-side identity of a compatible entry that calls NaN.Builders.
const nanProviderKind = "nan"

// providerKindForBaseURL derives the provider identity the console shows for a
// compatible entry. Nothing stores it, so the endpoint stays the only source.
func providerKindForBaseURL(baseURL string) string {
	if management.IsNanBaseURL(baseURL) {
		return nanProviderKind
	}
	return ""
}

// NaN discovery supplies availability, while its dated OpenAPI supplies these defaults.
// Unknown limits stay unset; the catalog never adds a model the selected key did not list.
func nanModelDefaults(baseURL string, discovered []string) (map[string]ProviderModelDTO, *modelPullMetadata) {
	if !management.IsNanBaseURL(baseURL) {
		return nil, nil
	}
	defaults := make(map[string]ProviderModelDTO)
	for _, name := range discovered {
		model := ProviderModelDTO{Name: name, Options: &ProviderModelOptionsDTO{}}
		options := model.Options
		switch name {
		case "qwen3.8-flash", "qwen3.6", "gemma4", "deepseek-v4-flash", "glm5.3-flash", "glm5.3", "mimo-v2.6-flash":
			options.Operations = []string{"chat", "responses"}
			options.InputModalities = []string{"text", "image"}
			options.OutputModalities = []string{"text"}
			model.Thinking = &ThinkingDTO{Levels: []string{"none", "minimal", "low", "medium", "high", "max"}}
			if name == "qwen3.6" {
				options.Operations = append(options.Operations, "completions")
			} else if name != "gemma4" {
				options.MaxContextLength = 1_000_000
			}
			if name == "mimo-v2.6-flash" || name == "glm5.3" {
				options.InputModalities = append(options.InputModalities, "audio")
			}
			if name == "glm5.3" {
				options.InputModalities = append(options.InputModalities, "video")
			}
		case "qwen3-embedding":
			options.Operations = []string{"embeddings"}
			options.InputModalities = []string{"text"}
		case "rerank":
			options.Operations = []string{"rerank"}
			options.InputModalities = []string{"text"}
		case "kokoro":
			options.Operations = []string{"speech"}
			options.InputModalities, options.OutputModalities = []string{"text"}, []string{"audio"}
		case "whisper":
			options.Operations = []string{"transcription"}
			options.InputModalities, options.OutputModalities = []string{"audio"}, []string{"text"}
		case "flux-2-klein", "qwen-image-2.1":
			model.Image = true
			options.Operations = []string{"image-generation"}
			options.InputModalities, options.OutputModalities = []string{"text"}, []string{"image"}
			if name == "flux-2-klein" {
				options.Operations = append(options.Operations, "image-edit")
				options.InputModalities = append(options.InputModalities, "image")
			}
		default:
			continue
		}
		defaults[name] = model
	}
	return defaults, &modelPullMetadata{Source: "https://nan.builders/openapi.json", CheckedAt: "2026-10-12", Basis: "documented"}
}
