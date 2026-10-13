package management

import (
	"context"
	"fmt"
	"net/url"
	"strings"
)

// ConfiguredQuotaSubject is one configured key that can be observed without an auth file.
//
// The secret never leaves the management client. Label is a display mask, and
// Channel is the usage label CPA derives from the configured provider name.
// Disabled reports the provider switch; a disabled key stays listed so the
// console can show it, but it is not a selected observation target.
type ConfiguredQuotaSubject struct {
	Provider  string   `json:"provider"`
	Channel   string   `json:"channel"`
	AuthIndex string   `json:"auth_index"`
	Label     string   `json:"label"`
	Disabled  bool     `json:"disabled"`
	Models    []string `json:"models,omitempty"`
}

// ConfiguredQuotaDiagnostic names a key that cannot be selected. It carries no
// secret and no position: a missing or repeated index is not resolved by either.
// AuthIndex is set for a repeated index, so callers can count it with other claims.
type ConfiguredQuotaDiagnostic struct {
	Channel   string `json:"channel"`
	Reason    string `json:"reason"`
	AuthIndex string `json:"auth_index,omitempty"`
	Count     int    `json:"count,omitempty"`
}

// ConfiguredQuotaSubjects is the secret-free result of reading configured keys.
type ConfiguredQuotaSubjects struct {
	Subjects    []ConfiguredQuotaSubject
	Diagnostics []ConfiguredQuotaDiagnostic
	byIndex     map[string]int
}

// Subject returns the one enabled subject for authIndex. A missing, repeated,
// or disabled index is not selected.
func (subjects ConfiguredQuotaSubjects) Subject(authIndex string) (ConfiguredQuotaSubject, bool) {
	index, ok := subjects.byIndex[strings.TrimSpace(authIndex)]
	if !ok {
		return ConfiguredQuotaSubject{}, false
	}
	subject := subjects.Subjects[index]
	if subject.Disabled {
		return ConfiguredQuotaSubject{}, false
	}
	return subject, true
}

// ConfiguredQuotaSubjects enumerates the OpenAI-compatible keys whose endpoint
// host belongs to a provider with a configured-key quota reader. Today only
// NaN's host qualifies. The name never does: an entry named nan on another host
// gets no subject, and a renamed entry on NaN's host keeps one. The channel
// still uses the configured name, because that is the usage label CPA writes.
// Keys without an auth index, and an index claimed more than once, become
// diagnostics rather than a positional or masked selection.
func (c *Client) ConfiguredQuotaSubjects(ctx context.Context) (ConfiguredQuotaSubjects, error) {
	response, err := c.OpenAICompatibility(ctx)
	if IsMissingCapability(err) {
		// An older gateway has no compatibility list. That is no configured
		// key, not a failure of the auth-file quota overview.
		return ConfiguredQuotaSubjects{Subjects: []ConfiguredQuotaSubject{}, Diagnostics: []ConfiguredQuotaDiagnostic{}}, nil
	}
	if err != nil {
		return ConfiguredQuotaSubjects{}, err
	}
	result := ConfiguredQuotaSubjects{byIndex: map[string]int{}}
	type claim struct {
		subject ConfiguredQuotaSubject
		count   int
	}
	claims := map[string]*claim{}
	order := make([]string, 0)
	for _, entry := range response.Entries {
		provider := configuredQuotaProvider(entry.BaseURL)
		if provider == "" {
			continue
		}
		channel := OpenAICompatibilityLabelPrefix + strings.ToLower(strings.TrimSpace(entry.Name))
		for _, key := range entry.APIKeyEntries {
			authIndex := strings.TrimSpace(key.AuthIndex)
			if authIndex == "" {
				result.Diagnostics = append(result.Diagnostics, ConfiguredQuotaDiagnostic{
					Channel: channel,
					Reason:  "missing_auth_index",
				})
				continue
			}
			current := claims[authIndex]
			if current == nil {
				order = append(order, authIndex)
				current = &claim{subject: ConfiguredQuotaSubject{
					Provider:  provider,
					Channel:   channel,
					AuthIndex: authIndex,
					Label:     configuredQuotaLabel(entry.Name, authIndex),
					Disabled:  entry.Disabled,
					Models:    configuredUpstreamModels(entry.Models),
				}}
				claims[authIndex] = current
			}
			current.count++
			if entry.Disabled {
				current.subject.Disabled = true
			}
		}
	}
	for _, authIndex := range order {
		current := claims[authIndex]
		if current.count != 1 {
			result.Diagnostics = append(result.Diagnostics, ConfiguredQuotaDiagnostic{
				Channel:   current.subject.Channel,
				Reason:    "duplicate_auth_index",
				AuthIndex: authIndex,
				Count:     current.count,
			})
			continue
		}
		result.byIndex[authIndex] = len(result.Subjects)
		result.Subjects = append(result.Subjects, current.subject)
	}
	if result.Subjects == nil {
		result.Subjects = []ConfiguredQuotaSubject{}
	}
	if result.Diagnostics == nil {
		result.Diagnostics = []ConfiguredQuotaDiagnostic{}
	}
	return result, nil
}

func configuredUpstreamModels(models []ModelAlias) []string {
	seen := map[string]struct{}{}
	names := make([]string, 0, len(models))
	for _, model := range models {
		name := strings.TrimSpace(model.Name)
		if name == "" {
			continue
		}
		if _, duplicate := seen[name]; duplicate {
			continue
		}
		seen[name] = struct{}{}
		names = append(names, name)
	}
	return names
}

// IsNanBaseURL reports whether an endpoint is NaN.Builders' official API host.
// It is Deck's one NaN host check: a provider named nan elsewhere is not NaN.
func IsNanBaseURL(baseURL string) bool {
	parsed, err := url.Parse(strings.TrimSpace(baseURL))
	return err == nil && strings.EqualFold(parsed.Hostname(), "api.nan.builders")
}

// configuredQuotaProvider names the quota reader for a configured endpoint, or
// returns "" when no reader exists for its host.
func configuredQuotaProvider(baseURL string) string {
	if IsNanBaseURL(baseURL) {
		return "nan"
	}
	return ""
}

func configuredQuotaLabel(name, authIndex string) string {
	// The label names the key by its runtime index. A masked secret still
	// exposes a recoverable prefix, so it is not used as a quota label.
	return fmt.Sprintf("%s %s", strings.TrimSpace(name), authIndex)
}
