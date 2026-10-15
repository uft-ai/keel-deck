package operations

import (
	"reflect"
	"strings"
	"testing"

	"github.com/oh-my-cpa/oh-my-cpa/internal/cpa/gateway"
	"github.com/oh-my-cpa/oh-my-cpa/internal/cpa/management"
)

func TestModelSquarePreservesAliasFanoutAndOAuthDirectory(t *testing.T) {
	snapshot := management.ConfiguredModelSnapshot{Models: map[string]string{"fast": ""}, Providers: []management.ConfiguredModelProvider{
		{ID: "openai-compat-0", Routes: []management.ConfiguredModelRoute{{UpstreamModel: "gpt-5", CallPoint: "team/fast"}, {UpstreamModel: "gpt-5", CallPoint: "team/fast"}}},
		{ID: "openai-compat-1", Routes: []management.ConfiguredModelRoute{{UpstreamModel: "claude-sonnet", CallPoint: "team/fast"}}},
		{ID: "oauth:claude", Family: "claude", IsOAuth: true, Models: []string{"fast", "claude-opus"}},
	}}
	aliases := map[string][]management.OAuthModelAlias{"claude": {{Name: "claude-sonnet", Alias: "fast"}}}
	directory := ProjectModelSquare(snapshot, aliases, true, ModelPrefixPolicy{IsKnown: true})
	if len(directory.Routes) != 4 {
		t.Fatalf("routes = %#v", directory.Routes)
	}
	if directory.Routes[1].CallPoint != "fast" || directory.Routes[1].UpstreamModel != "claude-sonnet" {
		t.Fatalf("OAuth alias = %#v", directory.Routes[1])
	}
	for _, route := range directory.Routes {
		if route.CallPoint == "claude-sonnet" {
			t.Fatal("invented an OAuth call point that the directory did not publish")
		}
	}
	if directory.Routes[2].CallPoint != "team/fast" || directory.Routes[3].CallPoint != "team/fast" {
		t.Fatal("lost shared call point")
	}
	if len(directory.Partial) != 0 {
		t.Fatal(directory.Partial)
	}
}

func TestModelSquarePrefixPolicyDecidesBareRoutes(t *testing.T) {
	native := &management.ModelRouteConfiguration{Operations: []string{"responses"}, Native: []string{"responses"}}
	normal := &management.ModelRouteConfiguration{Operations: []string{"chat"}}
	snapshot := management.ConfiguredModelSnapshot{Providers: []management.ConfiguredModelProvider{
		{ID: "work", Prefix: "team", Routes: []management.ConfiguredModelRoute{
			{UpstreamModel: "gpt-5", CallPoint: "team/fast", BaseCallPoint: "fast", Configuration: normal},
			{UpstreamModel: "gpt-5", CallPoint: "team/fast-native", BaseCallPoint: "fast-native", Configuration: native},
		}},
		{ID: "same", Prefix: "mini", Routes: []management.ConfiguredModelRoute{{UpstreamModel: "gpt-5-mini", CallPoint: "mini/mini", BaseCallPoint: "mini"}}},
		{ID: "plain", Routes: []management.ConfiguredModelRoute{{UpstreamModel: "claude", CallPoint: "claude", BaseCallPoint: "claude"}}},
	}}
	describe := func(directory ModelSquareDirectory) []string {
		routes := []string{}
		for _, route := range directory.Routes {
			routes = append(routes, route.ProviderID+"|"+route.CallPoint+"|"+route.NameScope)
		}
		return routes
	}
	for _, input := range []struct {
		name    string
		policy  ModelPrefixPolicy
		routes  []string
		partial []string
	}{
		{"not forced", ModelPrefixPolicy{IsKnown: true}, []string{"plain|claude|global", "work|fast|global", "work|fast-native|global", "same|mini|global", "same|mini/mini|provider", "work|team/fast|provider", "work|team/fast-native|provider"}, []string{}},
		// Forced prefixes keep the bare name only where the prefix equals it.
		{"forced", ModelPrefixPolicy{IsKnown: true, IsForced: true}, []string{"plain|claude|global", "same|mini|global", "same|mini/mini|provider", "work|team/fast|provider", "work|team/fast-native|provider"}, []string{}},
		{"unknown", ModelPrefixPolicy{}, []string{"plain|claude|global", "same|mini/mini|provider", "work|team/fast|provider", "work|team/fast-native|provider"}, []string{"prefix_policy"}},
	} {
		directory := ProjectModelSquare(snapshot, nil, true, input.policy)
		if got := describe(directory); !reflect.DeepEqual(got, input.routes) || !reflect.DeepEqual(directory.Partial, input.partial) {
			t.Fatalf("%s: routes %v partial %v, want %v %v", input.name, got, directory.Partial, input.routes, input.partial)
		}
		// Normal and native aliases of one upstream keep their own settings.
		for _, route := range directory.Routes {
			if strings.HasSuffix(route.CallPoint, "fast-native") && route.Configuration != native || strings.HasSuffix(route.CallPoint, "/fast") && route.Configuration != normal {
				t.Fatalf("%s: %s configuration %#v", input.name, route.CallPoint, route.Configuration)
			}
		}
	}
	// Equal routes collapse; a different configuration on the same name is kept.
	duplicate := management.ConfiguredModelSnapshot{Providers: []management.ConfiguredModelProvider{{ID: "work", Routes: []management.ConfiguredModelRoute{
		{UpstreamModel: "gpt-5", CallPoint: "fast", Configuration: normal},
		{UpstreamModel: "gpt-5", CallPoint: "fast", Configuration: &management.ModelRouteConfiguration{Operations: []string{"chat"}}},
		{UpstreamModel: "gpt-5", CallPoint: "fast", Configuration: native},
		{UpstreamModel: "gpt-5", CallPoint: "broken", IsConfigurationInvalid: true},
	}}}}
	directory := ProjectModelSquare(duplicate, nil, true, ModelPrefixPolicy{IsKnown: true})
	if len(directory.Routes) != 3 || !reflect.DeepEqual(directory.Partial, []string{"route_configuration"}) || directory.Routes[0].Configuration != nil {
		t.Fatalf("deduplicated = %#v %v", directory.Routes, directory.Partial)
	}
}

func TestModelSquareReferencesStayPerRoute(t *testing.T) {
	snapshot := management.ConfiguredModelSnapshot{Providers: []management.ConfiguredModelProvider{
		{ID: "official", CatalogProvider: "nan", Routes: []management.ConfiguredModelRoute{{UpstreamModel: "deepseek-v4-flash", CallPoint: "deepseek-v4-flash"}}},
		{ID: "generic", Routes: []management.ConfiguredModelRoute{{UpstreamModel: "deepseek-v4-flash", CallPoint: "deepseek-v4-flash"}}},
	}}
	directory := ProjectModelSquare(snapshot, nil, true, ModelPrefixPolicy{IsKnown: true})
	references := map[string]string{}
	for _, route := range directory.Routes {
		references[route.ProviderID] = route.ReferenceID
	}
	if references["official"] != "deepseek/deepseek-v4.1-flash" || references["generic"] != "" {
		t.Fatalf("references = %v", references)
	}
	info := modelSquareReferences(directory.Routes)
	if _, exists := info["deepseek/deepseek-v4.1-flash"]; !exists {
		t.Fatal("canonical record missing")
	}
	if _, exists := info["deepseek-v4-flash"]; exists {
		t.Fatal("qualified record stored under the shared bare key")
	}
}

func TestModelSquareDoesNotGuessFailedOAuthMapping(t *testing.T) {
	directory := ProjectModelSquare(management.ConfiguredModelSnapshot{Providers: []management.ConfiguredModelProvider{{ID: "oauth:claude", IsOAuth: true, Models: []string{"fast"}}}}, nil, false, ModelPrefixPolicy{IsKnown: true})
	if len(directory.Partial) != 1 || len(directory.Routes) != 1 || directory.Routes[0].UpstreamModel != "" {
		t.Fatalf("unknown mapping = %#v", directory)
	}
}

func TestModelSquareUsesAdvertisedModelsAsTheAuthority(t *testing.T) {
	directory := ModelSquareDirectory{Providers: []ModelSquareProvider{{ID: "active"}, {ID: "hidden"}}, Routes: []ModelSquareRoute{
		{ProviderID: "active", UpstreamModel: "gpt-5", CallPoint: "fast"},
		{ProviderID: "active", UpstreamModel: "claude-sonnet", CallPoint: "fast"},
		{ProviderID: "hidden", UpstreamModel: "not-published", CallPoint: "not-published"},
	}}
	advertised := []gateway.Model{{ID: "fast", CallPoint: "fast"}, {ID: "runtime-only", CallPoint: "runtime-only"}}
	projected := RetainAdvertisedModels(directory, advertised)
	if len(projected.Models) != 2 || len(projected.Routes) != 3 || len(projected.Providers) != 1 {
		t.Fatalf("directory = %#v", projected)
	}
	if projected.Routes[2].CallPoint != "runtime-only" || projected.Routes[2].ProviderID != "" || projected.Routes[2].UpstreamModel != "" {
		t.Fatalf("invented runtime provenance = %#v", projected.Routes[2])
	}
	// An exact catalog ID keeps its literal reference without gaining provenance or scope.
	literal := RetainAdvertisedModels(ModelSquareDirectory{}, []gateway.Model{{ID: "deepseek/deepseek-v4.1-flash"}})
	if route := literal.Routes[0]; route.ReferenceID != "deepseek/deepseek-v4.1-flash" || route.ProviderID != "" || route.NameScope != NameScopeUnknown || route.Configuration != nil {
		t.Fatalf("literal reference route = %#v", route)
	}
	empty := RetainAdvertisedModels(directory, []gateway.Model{})
	if len(empty.Models) != 0 || len(empty.Routes) != 0 || len(empty.Providers) != 0 {
		t.Fatalf("empty live directory advertised configured models: %#v", empty)
	}
}

func TestModelSquareNativeExclusionsApplyWithoutGuessingAliases(t *testing.T) {
	for _, input := range []struct {
		model, pattern string
		isExcluded     bool
	}{
		{"GPT-5", "gpt-*", true}, {"openai/gpt-5", "*gpt*", true}, {"claude-sonnet", "gpt-*", false}, {"model[1]", "model[1]", true},
	} {
		if got := isModelExcluded(input.model, []string{input.pattern}); got != input.isExcluded {
			t.Fatalf("%q / %q = %v", input.model, input.pattern, got)
		}
	}
}
