package api

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/oh-my-cpa/oh-my-cpa/internal/cpa/management"
	"github.com/oh-my-cpa/oh-my-cpa/internal/pricing"
	"github.com/oh-my-cpa/oh-my-cpa/internal/usage"
)

// scopedAliasLister maps the real configured snapshot the way the application does.
type scopedAliasLister struct{ client *management.Client }

func (lister scopedAliasLister) ListConfiguredModels(ctx context.Context) ([]string, error) {
	return lister.client.ListAllConfiguredModels(ctx)
}

func (lister scopedAliasLister) ListConfiguredModelSnapshot(ctx context.Context) (pricing.ModelCatalogSnapshot, error) {
	snapshot, err := lister.client.ListConfiguredModelSnapshot(ctx)
	providers := make([]pricing.CatalogProvider, 0, len(snapshot.Providers))
	for _, provider := range snapshot.Providers {
		providers = append(providers, pricing.CatalogProvider{ID: provider.ID, Family: provider.Family, Name: provider.Name, Prefix: provider.Prefix, EndpointHost: provider.EndpointHost, IsOAuth: provider.IsOAuth, Models: provider.ListPricingModelIDs()})
	}
	return pricing.ModelCatalogSnapshot{Models: snapshot.Models, Providers: providers}, err
}

type scopedAliasPriceFetcher struct{}

func (scopedAliasPriceFetcher) Fetch(context.Context) (pricing.Catalog, error) {
	return pricing.NewCatalog([]pricing.UpstreamModel{{ID: "vendor/unrelated-model", Author: "vendor", PromptPricePer1M: 1, CompletionPer1M: 1}}, time.Now()), nil
}

func TestModelSquareScopedAliasPriceSavesThroughPricingAPI(t *testing.T) {
	ctx := context.Background()
	var hasCollision, isUnreadable atomic.Bool
	handler := func(writer http.ResponseWriter, request *http.Request) {
		switch {
		case request.URL.Path == "/v8/management/credentials":
			io.WriteString(writer, `{"files":[]}`)
		case request.URL.Path == "/v8/management/config/oauth/model-alias":
			io.WriteString(writer, `{}`)
		case request.URL.Path == "/v0/management/openai-compatibility":
			if isUnreadable.Load() {
				writer.WriteHeader(http.StatusServiceUnavailable)
				return
			}
			providers := `{"name":"Work","base-url":"https://work.example.test/v1","prefix":"team","models":[{"name":"scoped-model","alias":"fast"},{"name":"retired-model","disabled":true},{"name":"shared-model"}]}`
			if hasCollision.Load() {
				providers += `,{"name":"Other","base-url":"https://other.example.test/v1","models":[{"name":"other-model","alias":"team/shared-model"}]}`
			}
			io.WriteString(writer, `{"openai-compatibility":[`+providers+`]}`)
		case strings.HasPrefix(request.URL.Path, "/v0/management/") && strings.HasSuffix(request.URL.Path, "-api-key"):
			io.WriteString(writer, `{}`)
		default:
			t.Errorf("unexpected read: %s %s", request.Method, request.URL.Path)
			writer.WriteHeader(http.StatusNotFound)
		}
	}
	var service *pricing.Service
	client, baseURL, repo := startDashboardTestServer(t, handler, func(appHandler *Handler) {
		instance, err := appHandler.repo.GetInstance(ctx, "default")
		if err != nil {
			t.Fatal(err)
		}
		managementClient, err := management.NewClient(instance.BaseURL, "management-secret-value", time.Second, false)
		if err != nil {
			t.Fatal(err)
		}
		service = pricing.NewService(appHandler.repo, scopedAliasPriceFetcher{}, nil)
		service.SetModelLister(scopedAliasLister{client: managementClient})
		appHandler.SetPricing(service)
	})
	if _, err := service.SyncOnce(ctx); err != nil {
		t.Fatal(err)
	}
	// A request recorded before any price must keep its recorded cost state.
	alias := "team/fast"
	eventID, err := repo.InsertUsageEvents(ctx, []usage.Event{{InstanceID: "default", EventKey: "before-scoped-price", Model: "scoped-model", ModelAlias: &alias, TimestampMS: time.Now().Add(-time.Minute).UnixMilli(), InputTokens: 1_000_000, Generate: true}})
	if err != nil {
		t.Fatal(err)
	}
	put := func(model string) {
		t.Helper()
		response, body := doJSON(t, client, http.MethodPut, baseURL+"/omc/api/v1/pricing/models/"+strings.ReplaceAll(model, "/", "%2F"), `{"mode":"custom","prompt_price_per_1m":2,"completion_price_per_1m":8,"price_multiplier":1}`)
		if response.StatusCode != http.StatusOK {
			t.Fatalf("save %s: %d %s", model, response.StatusCode, body)
		}
	}
	// The exact provider-scoped name, a disabled row's scoped name and a name that
	// later becomes ambiguous are all saved through the real price API.
	for _, model := range []string{"team/fast", "team/retired-model", "team/shared-model"} {
		put(model)
	}
	response, body := getJSON(t, client, baseURL+"/omc/api/v1/pricing/models/team%2Ffast")
	var detail struct {
		Price *pricing.ModelPrice `json:"price"`
	}
	if err := json.Unmarshal(body, &detail); err != nil || response.StatusCode != http.StatusOK || detail.Price == nil || detail.Price.PromptPricePer1M != 2 || detail.Price.CompletionPer1M != 8 {
		t.Fatalf("reload: %d %s %v", response.StatusCode, body, err)
	}
	hasCollision.Store(true)
	if _, err := service.SyncOnce(ctx); err != nil {
		t.Fatal(err)
	}
	catalog, err := repo.ListPricingModels(ctx)
	if err != nil {
		t.Fatal(err)
	}
	for model, target := range map[string]string{"scoped-model": "scoped-model", "fast": "scoped-model", "team/fast": "scoped-model", "retired-model": "retired-model", "team/retired-model": "retired-model", "team/shared-model": ""} {
		if actual, exists := catalog[model]; !exists || actual != target {
			t.Fatalf("catalog[%q] = %q (%t), want %q: %v", model, actual, exists, target, catalog)
		}
	}
	isUnreadable.Store(true)
	if _, err := service.SyncOnce(ctx); err == nil {
		t.Fatal("unreadable provider list published a catalog")
	}
	retained, err := repo.ListPricingModels(ctx)
	if err != nil || retained["team/fast"] != "scoped-model" {
		t.Fatalf("last complete catalog was lost: %v %v", retained, err)
	}
	prices, err := service.ListPrices(ctx)
	if err != nil {
		t.Fatal(err)
	}
	custom := map[string]bool{}
	for _, price := range prices {
		if price.Mode == pricing.ModeCustom && price.PromptPricePer1M == 2 {
			custom[price.Model] = true
		}
	}
	for _, model := range []string{"team/fast", "team/retired-model", "team/shared-model"} {
		if !custom[model] {
			t.Fatalf("custom price for %s was not retained: %+v", model, prices)
		}
	}
	event, err := repo.GetUsageEvent(ctx, eventID)
	if err != nil || event.CostUSD != nil || event.PricingStatus != "unpriced" {
		t.Fatalf("historical request was repriced: %+v %v", event, err)
	}
}
