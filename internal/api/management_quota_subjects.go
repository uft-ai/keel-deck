package api

import (
	"context"
	"fmt"
	"net/http"
	"sort"
	"strings"

	"github.com/oh-my-cpa/oh-my-cpa/internal/cpa/management"
	"github.com/oh-my-cpa/oh-my-cpa/internal/quota"
)

// quotaSubject is one credential the quota pages can address.
//
// Models carries the exact configured upstream names for a configured key. It
// is empty for an auth file, whose quota probe does not use this list.
//
// An auth file keeps its own identity. A configured key is not turned into an
// auth file: Source says which list it came from, and the synthetic file is
// only the observation input RefreshCredentialQuota already accepts.
type quotaSubject struct {
	File   management.AuthFile
	Source string
	Models []string
}

// quotaDiagnostic reports an auth index more than one credential claims. Count
// is the number of claims across auth files and configured keys together.
type quotaDiagnostic struct {
	Reason    string `json:"reason"`
	AuthIndex string `json:"auth_index"`
	Count     int    `json:"count"`
}

const duplicateAuthIndex = "duplicate_auth_index"

// quotaSubjectList is every credential with exactly one claim on its auth index.
// An index claimed more than once is not a subject: reading it would store an
// observation that belongs to neither credential, so every caller refuses it and
// reports the same diagnostic.
type quotaSubjectList struct {
	Subjects    []quotaSubject
	Diagnostics []quotaDiagnostic
}

// find returns the subject for authIndex, or whether the index is ambiguous.
func (list quotaSubjectList) find(authIndex string) (subject quotaSubject, found bool, ambiguous bool) {
	wanted := strings.TrimSpace(authIndex)
	for _, diagnostic := range list.Diagnostics {
		if diagnostic.AuthIndex == wanted {
			return quotaSubject{}, false, true
		}
	}
	for _, subject := range list.Subjects {
		if strings.TrimSpace(subject.File.AuthIndex) == wanted {
			return subject, true, false
		}
	}
	return quotaSubject{}, false, false
}

// writeDuplicateAuthIndex refuses a targeted read of an ambiguous index.
func writeDuplicateAuthIndex(writer http.ResponseWriter, authIndex string) {
	writeJSON(writer, http.StatusConflict, map[string]string{
		"error": fmt.Sprintf("auth_index %q is claimed by more than one credential", authIndex),
		"code":  duplicateAuthIndex,
	})
}

func quotaSubjects(ctx context.Context, client *management.Client) (quotaSubjectList, error) {
	files, err := client.AuthFiles(ctx)
	if err != nil {
		return quotaSubjectList{}, err
	}
	configured, err := client.ConfiguredQuotaSubjects(ctx)
	if err != nil {
		return quotaSubjectList{}, err
	}
	claims := map[string]int{}
	candidates := make([]quotaSubject, 0, len(files.Files)+len(configured.Subjects))
	for _, file := range files.Files {
		index := strings.TrimSpace(file.AuthIndex)
		if index == "" {
			continue
		}
		claims[index]++
		candidates = append(candidates, quotaSubject{File: file, Source: "auth_file"})
	}
	for _, subject := range configured.Subjects {
		claims[subject.AuthIndex]++
		candidates = append(candidates, configuredQuotaSubject(subject))
	}
	// A configured index repeated inside the compatibility list is already left out
	// of its subjects; its claims still count against an auth file with that index.
	for _, diagnostic := range configured.Diagnostics {
		if diagnostic.Reason == duplicateAuthIndex {
			claims[diagnostic.AuthIndex] += diagnostic.Count
		}
	}
	list := quotaSubjectList{Subjects: []quotaSubject{}, Diagnostics: []quotaDiagnostic{}}
	for _, subject := range candidates {
		if claims[strings.TrimSpace(subject.File.AuthIndex)] == 1 {
			list.Subjects = append(list.Subjects, subject)
		}
	}
	for index, count := range claims {
		if count > 1 {
			list.Diagnostics = append(list.Diagnostics, quotaDiagnostic{Reason: duplicateAuthIndex, AuthIndex: index, Count: count})
		}
	}
	sort.Slice(list.Diagnostics, func(i, j int) bool { return list.Diagnostics[i].AuthIndex < list.Diagnostics[j].AuthIndex })
	return list, nil
}

func configuredQuotaSubject(subject management.ConfiguredQuotaSubject) quotaSubject {
	return quotaSubject{
		Source: "configured_key",
		Models: append([]string(nil), subject.Models...),
		File: management.AuthFile{
			Name:      subject.Label,
			AuthIndex: subject.AuthIndex,
			Type:      subject.Provider,
			Provider:  subject.Provider,
			Disabled:  subject.Disabled,
		},
	}
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
