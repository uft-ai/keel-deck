package api

import (
	"encoding/json"
	"net/http"
	"os"
	"testing"
	"time"

	"github.com/oh-my-cpa/oh-my-cpa/internal/repository"
	"github.com/oh-my-cpa/oh-my-cpa/internal/usage"
)

// The browser request-list fixtures copy their pricing fields from this corpus.
// Each case is pinned against the real list handler, so a fixture row can only
// offer a price when a stored request with those counters would.
func TestUsageEventPricingSharedWireContract(t *testing.T) {
	payload, err := os.ReadFile("../../scripts/acceptance/contracts/usage-event-pricing.json")
	if err != nil {
		t.Fatal(err)
	}
	var contract struct {
		Version int `json:"version"`
		Cases   []struct {
			ID    string `json:"id"`
			Event struct {
				Model          string `json:"model"`
				Generate       bool   `json:"generate"`
				TokensReported *bool  `json:"tokens_reported"`
				Input          int64  `json:"input"`
				Output         int64  `json:"output"`
				CacheRead      int64  `json:"cache_read"`
				Total          int64  `json:"total"`
			} `json:"event"`
			Row struct {
				PricingStatus  string `json:"pricing_status"`
				UnpricedReason string `json:"unpriced_reason"`
				TokensReported *bool  `json:"tokens_reported"`
			} `json:"row"`
		} `json:"cases"`
	}
	if err := json.Unmarshal(payload, &contract); err != nil {
		t.Fatal(err)
	}
	if contract.Version != 1 || len(contract.Cases) == 0 {
		t.Fatal("unsupported or empty usage event pricing contract")
	}
	seen := map[string]bool{}
	for _, entry := range contract.Cases {
		if entry.ID == "" || seen[entry.ID] {
			t.Fatalf("invalid contract case identity %q", entry.ID)
		}
		seen[entry.ID] = true
		t.Run(entry.ID, func(t *testing.T) {
			client, baseURL, repo := startDashboardTestServer(t, func(writer http.ResponseWriter, _ *http.Request) {
				writer.WriteHeader(http.StatusNotFound)
			})
			event := eventFor("pricing-"+entry.ID, time.Now().UTC().Add(-time.Minute), usage.TokenStats{
				InputTokens: entry.Event.Input, OutputTokens: entry.Event.Output,
				CacheReadTokens: entry.Event.CacheRead, TotalTokens: entry.Event.Total,
			}, false)
			event.Model = entry.Event.Model
			event.Generate = entry.Event.Generate
			event.TokensReported = entry.Event.TokensReported
			seedEvents(t, repo, time.Now(), []repository.UsageDecoded{{Event: event}})

			response, body := getJSON(t, client, baseURL+"/omc/api/v1/usage/events?preset=24h")
			if response.StatusCode != http.StatusOK {
				t.Fatalf("list status = %d body %s", response.StatusCode, body)
			}
			var page struct {
				Items []map[string]json.RawMessage `json:"items"`
			}
			if err := json.Unmarshal(body, &page); err != nil || len(page.Items) != 1 {
				t.Fatalf("list body = %s err=%v", body, err)
			}
			row := page.Items[0]
			if _, priced := row["cost_usd"]; priced {
				t.Fatalf("unpriced fixture row carries a cost: %s", body)
			}
			var status, reason string
			var tokens struct {
				Reported *bool `json:"reported"`
			}
			_ = json.Unmarshal(row["pricing_status"], &status)
			_ = json.Unmarshal(row["unpriced_reason"], &reason)
			_ = json.Unmarshal(row["tokens"], &tokens)
			if status != entry.Row.PricingStatus || reason != entry.Row.UnpricedReason {
				t.Fatalf("pricing drift: status=%q reason=%q expected %q/%q", status, reason, entry.Row.PricingStatus, entry.Row.UnpricedReason)
			}
			if (tokens.Reported == nil) != (entry.Row.TokensReported == nil) ||
				(tokens.Reported != nil && *tokens.Reported != *entry.Row.TokensReported) {
				t.Fatalf("tokens.reported drift: %s", row["tokens"])
			}
		})
	}
}
