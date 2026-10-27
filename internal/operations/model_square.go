package operations

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"regexp"
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/oh-my-cpa/oh-my-cpa/internal/capability"
	"github.com/oh-my-cpa/oh-my-cpa/internal/cpa/gateway"
	"github.com/oh-my-cpa/oh-my-cpa/internal/cpa/management"
	"github.com/oh-my-cpa/oh-my-cpa/internal/modelcatalog"
)

// ModelSquareRoute is one way an advertised client name can reach a model.
// ReferenceID is the models.dev key resolved for this route, "" when unresolved.
// NameScope records how the name was constructed: "provider" for a configured
// prefix, "global" for an unprefixed name, "unknown" when reads do not prove it.
type ModelSquareRoute struct {
	ProviderID    string                              `json:"provider_id"`
	UpstreamModel string                              `json:"upstream_model"`
	CallPoint     string                              `json:"call_point"`
	ReferenceID   string                              `json:"reference_id"`
	NameScope     string                              `json:"name_scope"`
	Configuration *management.ModelRouteConfiguration `json:"configuration"`
}

const (
	NameScopeGlobal   = "global"
	NameScopeProvider = "provider"
	NameScopeUnknown  = "unknown"
)

// ModelPrefixPolicy is CPA's routing.force-model-prefix as far as it could be read.
type ModelPrefixPolicy struct {
	IsForced bool
	IsKnown  bool
}
type ModelSquareProvider struct {
	ID           string `json:"id"`
	Family       string `json:"family"`
	Name         string `json:"name"`
	EndpointHost string `json:"endpoint_host,omitempty"`
	IsOAuth      bool   `json:"is_oauth"`
	IconID       string `json:"icon_id,omitempty"`
}

const MODEL_SQUARE_ENRICHMENT_TIMEOUT = 5 * time.Second

var ErrClientKeyRequired = errors.New("client_key_required")

type ModelSquareQuery struct{}
type ModelSquareDirectory struct {
	ModelInfo         map[string]modelcatalog.Model `json:"model_info"`
	MetadataUpdatedAt string                        `json:"metadata_updated_at"`
	Models            []gateway.Model               `json:"models"`

	Providers []ModelSquareProvider `json:"providers"`
	Routes    []ModelSquareRoute    `json:"routes"`
	Partial   []string              `json:"partial"`
}

// ProjectModelSquare does not use the pricing alias map: an alias with several targets
// must retain every route here, rather than becoming the pricing ambiguity sentinel.
func ProjectModelSquare(snapshot management.ConfiguredModelSnapshot, aliases map[string][]management.OAuthModelAlias, hasAliases bool, policy ModelPrefixPolicy) ModelSquareDirectory {
	directory := ModelSquareDirectory{Providers: []ModelSquareProvider{}, Routes: []ModelSquareRoute{}, Partial: []string{}}
	if !hasAliases {
		directory.Partial = append(directory.Partial, "oauth_aliases")
	}
	markPartial := func(marker string) {
		if !slices.Contains(directory.Partial, marker) {
			directory.Partial = append(directory.Partial, marker)
		}
	}
	type routeIdentity struct{ provider, upstream, callPoint, scope string }
	seen := map[routeIdentity][]*management.ModelRouteConfiguration{}
	// Equal routes collapse only with equal configuration: two rows for one upstream
	// can state different operations, and neither may overwrite the other.
	addRoute := func(route ModelSquareRoute) {
		if route.CallPoint == "" {
			return
		}
		identity := routeIdentity{route.ProviderID, route.UpstreamModel, route.CallPoint, route.NameScope}
		if slices.ContainsFunc(seen[identity], func(other *management.ModelRouteConfiguration) bool {
			return reflect.DeepEqual(other, route.Configuration)
		}) {
			return
		}
		seen[identity] = append(seen[identity], route.Configuration)
		directory.Routes = append(directory.Routes, route)
	}
	for _, provider := range snapshot.Providers {
		directory.Providers = append(directory.Providers, ModelSquareProvider{ID: provider.ID, Family: provider.Family, Name: provider.Name, EndpointHost: provider.EndpointHost, IsOAuth: provider.IsOAuth})
		if !provider.IsOAuth {
			for _, route := range provider.Routes {
				if isModelExcluded(route.UpstreamModel, provider.ExcludedModels) {
					continue
				}
				if route.IsConfigurationInvalid {
					markPartial("route_configuration")
				}
				base := route.BaseCallPoint
				if base == "" || base == route.CallPoint {
					addRoute(ModelSquareRoute{ProviderID: provider.ID, UpstreamModel: route.UpstreamModel, CallPoint: route.CallPoint, NameScope: NameScopeGlobal, Configuration: route.Configuration})
					continue
				}
				addRoute(ModelSquareRoute{ProviderID: provider.ID, UpstreamModel: route.UpstreamModel, CallPoint: route.CallPoint, NameScope: NameScopeProvider, Configuration: route.Configuration})
				// CPA also publishes the bare name unless prefixes are forced, and always
				// when the prefix equals the name. An unreadable policy proves neither.
				if !policy.IsKnown {
					markPartial("prefix_policy")
					continue
				}
				if !policy.IsForced || management.NormalizeModelPrefix(provider.Prefix) == base {
					addRoute(ModelSquareRoute{ProviderID: provider.ID, UpstreamModel: route.UpstreamModel, CallPoint: base, NameScope: NameScopeGlobal, Configuration: route.Configuration})
				}
			}
			continue
		}
		for _, model := range provider.Models {
			// CPA's OAuth directory is already alias/exclusion-aware. Only invert a mapping
			// that actually appears there; fabricating a native call point would bypass fork semantics.
			targets := []string{}
			if hasAliases {
				for _, alias := range aliases[provider.Family] {
					if strings.EqualFold(alias.Alias, model) {
						targets = append(targets, alias.Name)
					}
				}
				if len(targets) == 0 {
					targets = append(targets, model)
				}
			} else {
				targets = append(targets, "")
			}
			// OAuth groups several credentials per family and the directory does not show
			// their prefixes, so whether a name is shared or scoped stays unknown.
			for _, target := range targets {
				addRoute(ModelSquareRoute{ProviderID: provider.ID, UpstreamModel: target, CallPoint: model, NameScope: NameScopeUnknown})
			}
		}
	}
	sort.SliceStable(directory.Routes, func(left, right int) bool {
		first, second := directory.Routes[left], directory.Routes[right]
		if first.CallPoint != second.CallPoint {
			return first.CallPoint < second.CallPoint
		}
		if first.UpstreamModel != second.UpstreamModel {
			return first.UpstreamModel < second.UpstreamModel
		}
		return first.ProviderID < second.ProviderID
	})
	catalogProviders := map[string]string{}
	for _, provider := range snapshot.Providers {
		catalogProviders[provider.ID] = provider.CatalogProvider
	}
	for index := range directory.Routes {
		route := &directory.Routes[index]
		if route.UpstreamModel == "" {
			continue
		}
		if model, exists := modelcatalog.DEFAULT_CATALOG.MatchFrom(catalogProviders[route.ProviderID], route.UpstreamModel); exists {
			route.ReferenceID = model.ID
		}
	}
	return directory
}

// modelSquareReferences keys each route's resolved record by its canonical ID, and
// keeps the earlier generic per-identity entries for existing readers. A record found
// through a provider's qualified alias is only ever stored under its canonical key.
func modelSquareReferences(routes []ModelSquareRoute) map[string]modelcatalog.Model {
	identities := []string{}
	for _, route := range routes {
		identity := route.UpstreamModel
		if identity == "" {
			identity = route.CallPoint
		}
		identities = append(identities, identity)
	}
	references := modelcatalog.DEFAULT_CATALOG.Select(identities)
	for _, route := range routes {
		if model, exists := modelcatalog.DEFAULT_CATALOG.Models[route.ReferenceID]; exists {
			references[route.ReferenceID] = model
		}
	}
	return references
}

func (s *Service) ListModelSquare(ctx context.Context, input ModelSquareQuery) (ModelSquareDirectory, error) {
	client, err := s.Client(ctx)
	if err != nil {
		return ModelSquareDirectory{}, err
	}
	view, _, err := client.ConfigView(ctx)
	if err != nil {
		return ModelSquareDirectory{}, err
	}
	var policy ModelPrefixPolicy
	policy.IsForced, policy.IsKnown = management.ForceModelPrefixIn(view)
	selectedKey := ""
	for _, key := range management.ClientAPIKeysIn(view) {
		if key = strings.TrimSpace(key); key != "" {
			selectedKey = key
			break
		}
	}
	if selectedKey == "" {
		return ModelSquareDirectory{}, ErrClientKeyRequired
	}
	inference, err := gateway.NewClient(client.BaseURL(), selectedKey, s.TLSSkipVerify)
	if err != nil {
		return ModelSquareDirectory{}, err
	}
	advertised, err := inference.ListModels(ctx)
	if err != nil {
		return ModelSquareDirectory{}, err
	}
	// Availability has already been read. All optional provenance shares one
	// budget, so credential count and slow follow-up reads cannot consume the
	// HTTP server's write timeout or hide advertised models.
	enrichmentCtx, cancelEnrichment := context.WithTimeout(ctx, MODEL_SQUARE_ENRICHMENT_TIMEOUT)
	defer cancelEnrichment()
	snapshot, snapshotErr := client.ListConfiguredModelProvenance(enrichmentCtx)
	// A native API-key entry may rely on CPA's built-in catalog rather than listing
	// every model in configuration. Resolve only those configured families, never the
	// OpenRouter universe, and apply that entry's exclusions before publishing routes.
	hasMissingModels := false
	definitions := map[string][]management.AuthModel{}
	for index := range snapshot.Providers {
		provider := &snapshot.Providers[index]
		if provider.IsOAuth || len(provider.Routes) > 0 {
			continue
		}
		if provider.Family == "openai-compatibility" {
			// Configured names without routes mean every row is disabled: the
			// list is known, it only serves nothing.
			if len(provider.Models) == 0 {
				hasMissingModels = true
			}
			continue
		}
		models, hasDefinitions := definitions[provider.Family]
		if !hasDefinitions {
			models, err = client.StaticModelDefinitions(enrichmentCtx, provider.Family)
			if err != nil {
				models = nil
			}
			definitions[provider.Family] = models
		}
		if models == nil {
			hasMissingModels = true
			continue
		}
		for _, model := range models {
			if isModelExcluded(model.ID, provider.ExcludedModels) {
				continue
			}
			// Built-in definitions have no configured row, so no configuration either.
			provider.Routes = append(provider.Routes, management.ConfiguredModelRoute{UpstreamModel: model.ID, CallPoint: management.PrefixModelID(provider.Prefix, model.ID), BaseCallPoint: model.ID})
		}
	}
	// Alias rules observed before the budget expires remain valid provenance.
	// Re-reading them here could erase resolved identities after a later timeout.
	aliases, hasAliases := snapshot.OAuthAliases, snapshot.HasOAuthAliases
	if !hasAliases {
		var aliasErr error
		aliases, aliasErr = client.OAuthModelAliases(enrichmentCtx)
		hasAliases = aliasErr == nil
	}
	directory := ProjectModelSquare(snapshot, aliases, hasAliases, policy)
	if snapshotErr != nil {
		directory.Partial = append(directory.Partial, "routes")
	}
	directory = RetainAdvertisedModels(directory, advertised)
	directory.ModelInfo = modelSquareReferences(directory.Routes)
	directory.MetadataUpdatedAt = modelcatalog.DEFAULT_CATALOG.UpdatedAt
	if hasMissingModels {
		directory.Partial = append(directory.Partial, "provider_models")
	}
	if s.Repo != nil {
		names := map[string]string{}
		icons := map[string]string{}
		if raw, _, readErr := s.Repo.GetPreference(ctx, "provider_names"); readErr == nil {
			_ = json.Unmarshal([]byte(raw), &names)
		}
		if raw, _, readErr := s.Repo.GetPreference(ctx, "provider_icons"); readErr == nil {
			_ = json.Unmarshal([]byte(raw), &icons)
		}
		for index := range directory.Providers {
			provider := &directory.Providers[index]
			if name := names[provider.ID]; name != "" {
				provider.Name = name
			}
			if provider.Name == "" {
				provider.Name = provider.Family
			}
			provider.IconID = icons[provider.ID]
		}
	}
	return directory, nil
}

// RetainAdvertisedModels makes /v1/models authoritative. Static configuration only
// enriches identities that the gateway actually published for this client key.
func RetainAdvertisedModels(directory ModelSquareDirectory, advertised []gateway.Model) ModelSquareDirectory {
	directory.Models = advertised
	directory.Routes = append([]ModelSquareRoute{}, directory.Routes...)
	allowed := map[string]bool{}
	for _, model := range advertised {
		allowed[model.ID] = true
	}
	filtered := []ModelSquareRoute{}
	represented := map[string]bool{}
	for _, route := range directory.Routes {
		if allowed[route.CallPoint] {
			filtered = append(filtered, route)
			represented[route.CallPoint] = true
		}
	}
	for _, model := range advertised {
		if !represented[model.ID] {
			// The literal name may still match a reference; that describes the
			// name only and gives the route no provider or scope.
			route := ModelSquareRoute{CallPoint: model.ID, NameScope: NameScopeUnknown}
			if reference, exists := modelcatalog.DEFAULT_CATALOG.Match(model.ID); exists {
				route.ReferenceID = reference.ID
			}
			filtered = append(filtered, route)
		}
	}
	directory.Routes = filtered
	usedProviders := map[string]bool{}
	for _, route := range filtered {
		usedProviders[route.ProviderID] = true
	}
	providers := []ModelSquareProvider{}
	for _, provider := range directory.Providers {
		if usedProviders[provider.ID] {
			providers = append(providers, provider)
		}
	}
	directory.Providers = providers
	return directory
}

func isModelExcluded(model string, patterns []string) bool {
	for _, pattern := range patterns {
		expression := "(?i)^" + strings.ReplaceAll(regexp.QuoteMeta(strings.TrimSpace(pattern)), `\*`, ".*") + "$"
		if matches, _ := regexp.MatchString(expression, model); matches {
			return true
		}
	}
	return false
}

func (s *Service) registerModelSquare(registry *capability.Registry) error {
	return read(registry, "models_list", "List models advertised by CPA /v1/models for an existing client key, with configured route provenance when known. Defaults to the first configured client key. Empty upstream_model or provider_id means unresolved provenance. reference_id keys model_info for that route, empty when unresolved. name_scope is global, provider (configured prefix) or unknown. configuration is the route's configured model-row settings or null; configured values do not verify provider support. Includes bundled models.dev reference metadata; it never establishes availability. No inference or credential disclosure.", func(ctx context.Context, input ModelSquareQuery) (ModelSquareDirectory, error) {
		directory, err := s.ListModelSquare(ctx, input)
		if len(directory.Routes) > 2000 {
			return ModelSquareDirectory{}, errors.New("tool_result_too_large")
		}
		return directory, err
	})
}
