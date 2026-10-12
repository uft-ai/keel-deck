package api

import (
	"context"
	"strings"

	"github.com/oh-my-cpa/oh-my-cpa/internal/cpa/management"
	"github.com/oh-my-cpa/oh-my-cpa/internal/quota"
)

// quotaSubject is one credential the quota pages can address.
//
// Models carries the exact configured upstream names for a NaN key. It is
// empty for an auth file, whose quota probe does not use this list.
//
// An auth file keeps its own identity. A configured NaN key is not turned into
// an auth file: Source says which list it came from, and the synthetic file is
// only the observation input RefreshCredentialQuota already accepts.
type quotaSubject struct {
	File   management.AuthFile
	Source string
	Models []string
}

func quotaSubjects(ctx context.Context, client *management.Client) ([]quotaSubject, error) {
	files, err := client.AuthFiles(ctx)
	if err != nil {
		return nil, err
	}
	subjects := make([]quotaSubject, 0, len(files.Files))
	seen := map[string]struct{}{}
	for _, file := range files.Files {
		index := strings.TrimSpace(file.AuthIndex)
		if index == "" {
			continue
		}
		if _, duplicate := seen[index]; duplicate {
			continue
		}
		seen[index] = struct{}{}
		subjects = append(subjects, quotaSubject{File: file, Source: "auth_file"})
	}
	configured, err := client.NanConfiguredQuotaSubjects(ctx)
	if err != nil {
		return nil, err
	}
	for _, subject := range configured.Subjects {
		if _, duplicate := seen[subject.AuthIndex]; duplicate {
			continue
		}
		seen[subject.AuthIndex] = struct{}{}
		subjects = append(subjects, quotaSubject{
			Source: "configured_key",
			Models: append([]string(nil), subject.Models...),
			File: management.AuthFile{
				Name:      subject.Label,
				AuthIndex: subject.AuthIndex,
				Type:      "nan",
				Provider:  subject.Provider,
				Disabled:  subject.Disabled,
			},
		})
	}
	return subjects, nil
}

func findQuotaSubject(subjects []quotaSubject, authIndex string) (quotaSubject, bool) {
	wanted := strings.TrimSpace(authIndex)
	for _, subject := range subjects {
		if strings.TrimSpace(subject.File.AuthIndex) == wanted {
			return subject, true
		}
	}
	return quotaSubject{}, false
}

func normalizedQuotaForSubject(subject quotaSubject, nowMS int64) quota.NormalizedQuota {
	file := subject.File
	provider := quota.DetectProvider(file.Type, file.Provider)
	return quota.NormalizedQuota{
		AuthIndex:    strings.TrimSpace(file.AuthIndex),
		Name:         file.Name,
		Type:         file.Type,
		Provider:     provider,
		Disabled:     file.Disabled,
		ObservedAtMS: nowMS,
		Capabilities: quota.CapabilitiesForFile(file),
		Windows:      []quota.QuotaWindow{},
	}
}
