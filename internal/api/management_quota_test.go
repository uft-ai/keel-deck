package api

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/oh-my-cpa/oh-my-cpa/internal/crypto"
	"github.com/oh-my-cpa/oh-my-cpa/internal/domain"
	"github.com/oh-my-cpa/oh-my-cpa/internal/quota"
	"github.com/oh-my-cpa/oh-my-cpa/internal/repository"
)

func TestManagementQuotaEndpoints(t *testing.T) {
	const managementKey = "management-secret"

	var cpaResetCalledWith string
	var shouldFailReset bool
	var cpaApiCallCalledWith string
	var cpaConsumeMethod string
	var cpaConsumeHeader map[string]string
	var cpaConsumeBody string

	cpaServer := newFakeCPA(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.Header.Get("Authorization") != "Bearer "+managementKey {
			writer.WriteHeader(http.StatusUnauthorized)
			return
		}
		writer.Header().Set("Content-Type", "application/json")

		switch request.URL.Path {
		case "/v8/management/credentials":
			_, _ = writer.Write([]byte(`{
				"files": [
					{
						"id": "file-1",
						"name": "codex-main.json",
						"auth_index": "codex-idx-1",
						"type": "codex",
						"provider": "codex",
						"disabled": false,
						"id_token": {
							"chatgpt_account_id": "acct-e2e",
							"chatgpt_subscription_active_until": 1800000000
						}
					},
					{
						"id": "file-2",
						"name": "claude-team.json",
						"auth_index": "claude-idx-2",
						"type": "claude",
						"provider": "claude",
						"disabled": false
					}
				]
			}`))

		case "/v8/management/routing/cooldown/reset":
			var payload struct {
				AuthIndex string `json:"auth_index"`
			}
			_ = json.NewDecoder(request.Body).Decode(&payload)
			cpaResetCalledWith = payload.AuthIndex
			if shouldFailReset {
				writer.WriteHeader(http.StatusBadGateway)
				_, _ = writer.Write([]byte(`{"error":"private-upstream-diagnostic"}`))
				return
			}
			_, _ = writer.Write([]byte(`{"status":"ok"}`))

		case "/v8/management/requests/api-call":
			var payload struct {
				AuthIndex string            `json:"auth_index"`
				URL       string            `json:"url"`
				Method    string            `json:"method"`
				Header    map[string]string `json:"header"`
				Data      string            `json:"data"`
			}
			_ = json.NewDecoder(request.Body).Decode(&payload)
			_ = cpaApiCallCalledWith
			cpaApiCallCalledWith = payload.URL

			// The consume check must precede the details check and name the real path:
			// the two endpoints share a prefix, so a typo here silently routes the redeem
			// call into the details response and the test passes without exercising it.
			if strings.HasSuffix(payload.URL, "/rate-limit-reset-credits/consume") {
				cpaConsumeMethod = payload.Method
				cpaConsumeHeader = payload.Header
				cpaConsumeBody = payload.Data
				_, _ = writer.Write([]byte(`{
					"status_code": 200,
					"body": {"code": "reset"}
				}`))
			} else if strings.Contains(payload.URL, "rate-limit-reset-credits") {
				_, _ = writer.Write([]byte(`{
					"status_code": 200,
					"body": {
						"available_count": 2,
						"credits": [
							{
								"id": "credit-1",
								"status": "available",
								"reset_type": "codex_rate_limits",
								"granted_at": "1790000000",
								"expires_at": "1810000000"
							},
							{
								"id": "credit-2",
								"status": "available",
								"reset_type": "codex_rate_limits",
								"granted_at": "1790000000",
								"expires_at": "1820000000"
							},
							{
								"id": "credit-3",
								"status": "used",
								"reset_type": "codex_rate_limits",
								"granted_at": "1790000000",
								"expires_at": "1805000000"
							}
						]
					}
				}`))
			} else if strings.Contains(payload.URL, "backend-api/wham/usage") {
				_, _ = writer.Write([]byte(`{
					"status_code": 200,
					"body": {
						"plan_type": "pro",
						"rate_limit": {
							"primary_window": {
								"used_percent": 15,
								"limit_window_seconds": 18000,
								"reset_after_seconds": 3600
							}
						},
						"rate_limit_reset_credits": {
							"available_count": 2,
							"applicable_available_count": 1
						}
					}
				}`))
			} else {
				_, _ = writer.Write([]byte(`{"status_code": 200, "body": {}}`))
			}

		default:
			writer.WriteHeader(http.StatusNotFound)
		}
	}))
	defer cpaServer.Close()

	handler := testHandler(t, "")
	appServer := httptest.NewServer(handler.Router())
	defer appServer.Close()

	// Configure CPA instance
	cipher, err := crypto.New("01234567890123456789012345678901")
	if err != nil {
		t.Fatal(err)
	}
	ciphertext, nonce, err := cipher.Encrypt([]byte(managementKey))
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC().Truncate(time.Second)
	if err := handler.repo.UpsertInstance(context.Background(), domain.CPAInstance{
		ID:                      "default",
		Name:                    "Default",
		BaseURL:                 cpaServer.URL,
		UsageAddr:               strings.TrimPrefix(cpaServer.URL, "http://"),
		ManagementKeyCiphertext: ciphertext,
		ManagementKeyNonce:      nonce,
		CreatedAt:               now,
		UpdatedAt:               now,
	}); err != nil {
		t.Fatal(err)
	}

	client := signedInClient(t, appServer.URL+"/api/auth/login", "management-secret")

	// 1. GET /api/v1/management/quota
	quotaResp, err := client.Get(appServer.URL + "/api/v1/management/quota")
	if err != nil {
		t.Fatalf("GET /api/v1/management/quota failed: %v", err)
	}
	defer quotaResp.Body.Close()

	if quotaResp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", quotaResp.StatusCode)
	}

	var overview quota.QuotaOverviewResponse
	if err := json.NewDecoder(quotaResp.Body).Decode(&overview); err != nil {
		t.Fatalf("decode overview failed: %v", err)
	}

	if overview.Summary.TotalCredentials != 2 {
		t.Fatalf("total credentials = %d, want 2", overview.Summary.TotalCredentials)
	}
	if len(overview.Quotas) != 2 {
		t.Fatalf("len(quotas) = %d, want 2", len(overview.Quotas))
	}

	// 2. POST /api/v1/management/quota/refresh for codex-idx-1
	refResp, err := client.Post(appServer.URL+"/api/v1/management/quota/refresh", "application/json", bytes.NewBufferString(`{"auth_index":"codex-idx-1"}`))
	if err != nil {
		t.Fatalf("POST /refresh failed: %v", err)
	}
	defer refResp.Body.Close()

	if refResp.StatusCode != http.StatusOK {
		t.Fatalf("refresh status = %d, want 200", refResp.StatusCode)
	}

	var refreshResult struct {
		Status string                `json:"status"`
		Quota  quota.NormalizedQuota `json:"quota"`
	}
	if err := json.NewDecoder(refResp.Body).Decode(&refreshResult); err != nil {
		t.Fatalf("decode refresh result failed: %v", err)
	}

	if refreshResult.Quota.Plan == nil || refreshResult.Quota.Plan.Tier != "elite" {
		t.Errorf("plan = %+v, want elite", refreshResult.Quota.Plan)
	}
	if refreshResult.Quota.Plan == nil || refreshResult.Quota.Plan.ExpiresAtMS == nil {
		t.Errorf("plan expiry missing, want id_token fallback: %+v", refreshResult.Quota.Plan)
	}
	if len(refreshResult.Quota.Windows) != 1 || *refreshResult.Quota.Windows[0].UsedPercent != 15.0 {
		t.Errorf("windows = %+v, want 15%% used", refreshResult.Quota.Windows)
	}
	if refreshResult.Quota.ResetCredits == nil || refreshResult.Quota.ResetCredits.AvailableCount != 2 {
		t.Errorf("reset credits = %+v, want 2", refreshResult.Quota.ResetCredits)
	}
	if refreshResult.Quota.ResetCredits == nil || len(refreshResult.Quota.ResetCredits.Credits) != 2 {
		t.Errorf("reset credit rows = %+v, want 2 available credits after filtering", refreshResult.Quota.ResetCredits)
	}

	// Refreshed plan must survive the snapshot round-trip (renewal time persists).
	overviewAfter, err := client.Get(appServer.URL + "/api/v1/management/quota")
	if err != nil {
		t.Fatalf("GET /quota after refresh failed: %v", err)
	}
	defer overviewAfter.Body.Close()
	var overviewAfterBody quota.QuotaOverviewResponse
	if err := json.NewDecoder(overviewAfter.Body).Decode(&overviewAfterBody); err != nil {
		t.Fatalf("decode overview after refresh failed: %v", err)
	}
	for _, item := range overviewAfterBody.Quotas {
		if item.AuthIndex != "codex-idx-1" {
			continue
		}
		if item.Plan == nil || item.Plan.PlanType != "pro" {
			t.Errorf("snapshot plan = %+v, want pro", item.Plan)
		}
		if item.Plan == nil || item.Plan.ExpiresAtMS == nil || *item.Plan.ExpiresAtMS != 1800000000000 {
			t.Errorf("snapshot plan expiry = %+v, want 1800000000000", item.Plan)
		}
		if item.ResetCredits == nil || item.ResetCredits.AvailableCount != 2 {
			t.Errorf("snapshot reset credits = %+v, want 2", item.ResetCredits)
		}
	}

	// 3. POST /api/v1/management/quota/clear-cooldown
	ccResp, err := client.Post(appServer.URL+"/api/v1/management/quota/clear-cooldown", "application/json", bytes.NewBufferString(`{"auth_index":"codex-idx-1"}`))
	if err != nil {
		t.Fatalf("POST /clear-cooldown failed: %v", err)
	}
	defer ccResp.Body.Close()

	if ccResp.StatusCode != http.StatusOK {
		t.Fatalf("clear-cooldown status = %d, want 200", ccResp.StatusCode)
	}
	if cpaResetCalledWith != "codex-idx-1" {
		t.Errorf("cpaResetCalledWith = %q, want codex-idx-1", cpaResetCalledWith)
	}

	// 4. POST /api/v1/management/quota/redeem-credit
	// A spent credit does not clear the gateway cooldown by itself, so redeeming
	// also resets it; the marker is cleared first to prove the redeem step made
	// the call rather than the clear-cooldown step above.
	cpaResetCalledWith = ""
	redeemResp, err := client.Post(appServer.URL+"/api/v1/management/quota/redeem-credit", "application/json", bytes.NewBufferString(`{"auth_index":"codex-idx-1"}`))
	if err != nil {
		t.Fatalf("POST /redeem-credit failed: %v", err)
	}
	defer redeemResp.Body.Close()

	if redeemResp.StatusCode != http.StatusOK {
		t.Fatalf("redeem-credit status = %d, want 200", redeemResp.StatusCode)
	}
	var redeemResult struct {
		Status          string `json:"status"`
		CooldownCleared bool   `json:"cooldown_cleared"`
	}
	if err := json.NewDecoder(redeemResp.Body).Decode(&redeemResult); err != nil {
		t.Fatalf("decode redeem result failed: %v", err)
	}
	if redeemResult.Status != "ok" || !redeemResult.CooldownCleared {
		t.Errorf("redeem result = %+v, want status ok with cooldown cleared", redeemResult)
	}
	if cpaResetCalledWith != "codex-idx-1" {
		t.Errorf("redeem cpaResetCalledWith = %q, want codex-idx-1", cpaResetCalledWith)
	}
	if cpaConsumeMethod != http.MethodPost {
		t.Errorf("consume method = %q, want POST", cpaConsumeMethod)
	}
	if cpaConsumeHeader["Authorization"] != "Bearer $TOKEN$" {
		t.Errorf("consume Authorization = %q, want the CPA credential marker", cpaConsumeHeader["Authorization"])
	}
	if cpaConsumeHeader["Chatgpt-Account-Id"] != "acct-e2e" {
		t.Errorf("consume Chatgpt-Account-Id = %q, want acct-e2e", cpaConsumeHeader["Chatgpt-Account-Id"])
	}
	if !strings.Contains(cpaConsumeBody, "redeem_request_id") {
		t.Errorf("consume body = %q, want a redeem_request_id", cpaConsumeBody)
	}

	// Redemption remains successful when the separate gateway clear fails, but
	// the ordinary response must not expose CPA's raw diagnostic body.
	shouldFailReset = true
	failedClearResp, err := client.Post(appServer.URL+"/api/v1/management/quota/redeem-credit", "application/json", bytes.NewBufferString(`{"auth_index":"codex-idx-1"}`))
	if err != nil {
		t.Fatalf("POST /redeem-credit with failed cooldown clear: %v", err)
	}
	failedClearBody, readErr := io.ReadAll(failedClearResp.Body)
	failedClearResp.Body.Close()
	if readErr != nil {
		t.Fatal(readErr)
	}
	if failedClearResp.StatusCode != http.StatusOK {
		t.Fatalf("failed clear status = %d, want 200", failedClearResp.StatusCode)
	}
	var failedClearResult struct {
		Status          string `json:"status"`
		CooldownCleared bool   `json:"cooldown_cleared"`
		CooldownError   string `json:"cooldown_error"`
	}
	if err := json.Unmarshal(failedClearBody, &failedClearResult); err != nil {
		t.Fatal(err)
	}
	if failedClearResult.Status != "ok" || failedClearResult.CooldownCleared || failedClearResult.CooldownError != "cooldown_clear_failed" {
		t.Fatalf("failed clear result = %+v, want successful redemption with a stable cooldown error", failedClearResult)
	}
	if strings.Contains(string(failedClearBody), "private-upstream-diagnostic") {
		t.Fatal("redemption response exposed the upstream diagnostic")
	}
	shouldFailReset = false

	// 5. GET /api/v1/management/quota/{authIndex}
	detailResp, err := client.Get(appServer.URL + "/api/v1/management/quota/codex-idx-1")
	if err != nil {
		t.Fatalf("GET /quota/codex-idx-1 failed: %v", err)
	}
	defer detailResp.Body.Close()

	if detailResp.StatusCode != http.StatusOK {
		t.Fatalf("detail status = %d, want 200", detailResp.StatusCode)
	}

	var detailResult struct {
		Quota   quota.NormalizedQuota            `json:"quota"`
		History []repository.QuotaSnapshotRecord `json:"history"`
	}
	if err := json.NewDecoder(detailResp.Body).Decode(&detailResult); err != nil {
		t.Fatalf("decode detail failed: %v", err)
	}

	if len(detailResult.History) == 0 {
		t.Errorf("expected snapshot history to be persisted")
	}
}

func TestQuotaSnapshotRoundTripsResetGrants(t *testing.T) {
	handler := capacityTestHandler(t)
	ctx := context.Background()
	nowMS := time.Now().UnixMilli()
	persisted := &quota.NormalizedQuota{
		AuthIndex:    "claude-idx-9",
		Provider:     "claude",
		Status:       "healthy",
		ObservedAtMS: nowMS,
		ResetGrants: []quota.ClaudeResetGrant{{
			ID: "grant-1", Label: "five_hour", ResetsTotal: 2, ResetsLeft: 1,
			EndsAtMS: func() *int64 { ends := nowMS + 3600000; return &ends }(),
		}},
	}
	if err := handler.persistNormalizedQuotaSnapshot(ctx, persisted); err != nil {
		t.Fatalf("persist snapshot: %v", err)
	}
	restored := handler.loadPriorNormalizedQuota(ctx, "claude-idx-9")
	if restored == nil {
		t.Fatal("snapshot missing after persist")
	}
	if len(restored.ResetGrants) != 1 || restored.ResetGrants[0].ID != "grant-1" || restored.ResetGrants[0].ResetsLeft != 1 {
		t.Fatalf("grants after roundtrip = %+v, want the persisted grant", restored.ResetGrants)
	}
}

func TestQuotaOverviewAdvertisesPluginRefresh(t *testing.T) {
	const managementKey = "management-secret"
	cpaServer := newFakeCPA(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.Header.Get("Authorization") != "Bearer "+managementKey {
			writer.WriteHeader(http.StatusUnauthorized)
			return
		}
		writer.Header().Set("Content-Type", "application/json")
		if request.URL.Path == "/v8/management/credentials" {
			_, _ = writer.Write([]byte(`{"files": [{
				"id": "file-plugin-1", "name": "review.json", "auth_index": "plugin-idx-1",
				"type": "codebuddy", "provider": "codebuddy", "disabled": false,
				"supports_quota": true, "quota_provider": "codebuddy"
			}]}`))
			return
		}
		_, _ = writer.Write([]byte(`{}`))
	}))
	defer cpaServer.Close()

	handler := testHandler(t, "")
	appServer := httptest.NewServer(handler.Router())
	defer appServer.Close()

	cipher, err := crypto.New("01234567890123456789012345678901")
	if err != nil {
		t.Fatal(err)
	}
	ciphertext, nonce, err := cipher.Encrypt([]byte(managementKey))
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC().Truncate(time.Second)
	if err := handler.repo.UpsertInstance(context.Background(), domain.CPAInstance{
		ID: "default", Name: "Default", BaseURL: cpaServer.URL,
		UsageAddr:               strings.TrimPrefix(cpaServer.URL, "http://"),
		ManagementKeyCiphertext: ciphertext, ManagementKeyNonce: nonce,
		CreatedAt: now, UpdatedAt: now,
	}); err != nil {
		t.Fatal(err)
	}

	client := signedInClient(t, appServer.URL+"/api/auth/login", "management-secret")

	quotaResp, err := client.Get(appServer.URL + "/api/v1/management/quota")
	if err != nil {
		t.Fatalf("GET overview failed: %v", err)
	}
	defer quotaResp.Body.Close()
	if quotaResp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", quotaResp.StatusCode)
	}
	var overview quota.QuotaOverviewResponse
	if err := json.NewDecoder(quotaResp.Body).Decode(&overview); err != nil {
		t.Fatalf("decode overview failed: %v", err)
	}
	if len(overview.Quotas) != 1 {
		t.Fatalf("len(quotas) = %d, want 1", len(overview.Quotas))
	}
	if !overview.Quotas[0].Capabilities.RefreshSupported {
		t.Fatalf("plugin credential capabilities = %+v, want refresh supported",
			overview.Quotas[0].Capabilities)
	}
}

func TestConfiguredQuotaSubjectsFollowHostAndRefuseAmbiguousIndexes(t *testing.T) {
	const secret = "sk-nan-secret-value-1234"
	const managementKey = "management-secret"
	var upstreamCalls, validCalls atomic.Int32
	cpaServer := newFakeCPA(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.Header.Get("Authorization") != "Bearer "+managementKey {
			writer.WriteHeader(http.StatusUnauthorized)
			return
		}
		writer.Header().Set("Content-Type", "application/json")
		switch request.URL.Path {
		case "/v8/management/credentials":
			// shared-1 is also a configured key's index, so neither credential is a subject.
			_, _ = writer.Write([]byte(`{"files":[
				{"name":"codex.json","auth_index":"codex-idx-1","type":"codex","provider":"codex"},
				{"name":"claude.json","auth_index":"shared-1","type":"claude","provider":"claude"}
			]}`))
		case "/v8/management/requests/api-call", "/v8/management/availability/period-exhaustion":
			// Only renamed-1, a valid selected subject, may be read. No provider read or
			// ledger call may happen for an ambiguous or non-NaN index.
			raw, _ := io.ReadAll(request.Body)
			var call struct {
				AuthIndex string `json:"auth_index"`
			}
			_ = json.Unmarshal(raw, &call)
			if call.AuthIndex == "" {
				call.AuthIndex = request.URL.Query().Get("auth_index")
			}
			if call.AuthIndex != "renamed-1" {
				upstreamCalls.Add(1)
				writer.WriteHeader(http.StatusServiceUnavailable)
				return
			}
			validCalls.Add(1)
			switch {
			case request.URL.Path == "/v8/management/requests/api-call":
				periodEnd := time.Now().Add(30 * 24 * time.Hour).UTC().Format(time.RFC3339)
				_, _ = writer.Write([]byte(`{"status_code":200,"body":"{\"models\":[{\"model\":\"qwen3.8-flash\",\"tokensUsed\":1,\"cap\":10,\"remaining\":9,\"periodEnd\":\"` + periodEnd + `\"}]}"}`))
			case request.Method == http.MethodPut:
				_, _ = writer.Write([]byte(`{"record":{"auth_index":"renamed-1","model":"qwen3.8-flash","version":1}}`))
			default:
				_, _ = writer.Write([]byte(`{"record":null,"observed_at":"` + time.Now().UTC().Format(time.RFC3339Nano) + `"}`))
			}
		case "/v0/management/openai-compatibility":
			_, _ = writer.Write([]byte(`{"openai-compatibility":[
				{"name":"nan","base-url":"https://api.nan.builders/v1","api-key-entries":[
					{"api-key":"` + secret + `","auth-index":"nan-1"},
					{"api-key":"sk-second-secret-value","auth-index":"nan-2"},
					{"api-key":"sk-missing-index-value"}
				]},
				{"name":"nan","disabled":true,"base-url":"https://api.nan.builders/v1","api-key-entries":[
					{"api-key":"sk-disabled-secret-value","auth-index":"nan-disabled"},
					{"api-key":"sk-repeat-secret-value","auth-index":"nan-repeat"}
				]},
				{"name":"other","base-url":"https://other.example/v1","api-key-entries":[
					{"api-key":"sk-other-secret-value","auth-index":"other-1"}
				]},
				{"name":"nan","base-url":"https://api.nan.builders/v1","api-key-entries":[
					{"api-key":"sk-repeat-again-secret","auth-index":"nan-repeat"}
				]},
				{"name":"nan","base-url":"http://172.31.77.10:8090/v1","api-key-entries":[
					{"api-key":"sk-elsewhere-secret-value","auth-index":"nan-elsewhere"}
				]},
				{"name":"Renamed","base-url":"https://API.nan.builders/v1","models":[{"name":"qwen3.8-flash"}],"api-key-entries":[
					{"api-key":"sk-renamed-secret-value","auth-index":"renamed-1"},
					{"api-key":"sk-shared-secret-value","auth-index":"shared-1"}
				]}
			]}`))
		default:
			upstreamCalls.Add(1)
			writer.WriteHeader(http.StatusNotFound)
		}
	}))
	defer cpaServer.Close()

	handler := testHandler(t, "")
	appServer := httptest.NewServer(handler.Router())
	defer appServer.Close()
	cipher, err := crypto.New("01234567890123456789012345678901")
	if err != nil {
		t.Fatal(err)
	}
	ciphertext, nonce, err := cipher.Encrypt([]byte(managementKey))
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC().Truncate(time.Second)
	if err := handler.repo.UpsertInstance(context.Background(), domain.CPAInstance{
		ID: "default", Name: "Default", BaseURL: cpaServer.URL,
		UsageAddr:               strings.TrimPrefix(cpaServer.URL, "http://"),
		ManagementKeyCiphertext: ciphertext, ManagementKeyNonce: nonce,
		CreatedAt: now, UpdatedAt: now,
	}); err != nil {
		t.Fatal(err)
	}
	jar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatal(err)
	}
	client := &http.Client{Jar: jar}
	login, err := client.Post(appServer.URL+"/api/auth/login", "application/json", bytes.NewBufferString(`{"password":"management-secret"}`))
	if err != nil {
		t.Fatal(err)
	}
	login.Body.Close()

	response, err := client.Get(appServer.URL + "/api/v1/management/quota")
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	body, err := io.ReadAll(response.Body)
	if err != nil {
		t.Fatal(err)
	}
	if response.StatusCode != http.StatusOK {
		t.Fatalf("overview = %d %s", response.StatusCode, body)
	}
	if bytes.Contains(body, []byte(secret)) || bytes.Contains(body, []byte("sk-")) {
		t.Fatalf("overview contains a secret: %s", body)
	}
	var overview struct {
		Quotas      []QuotaItemDTO    `json:"quotas"`
		Diagnostics []quotaDiagnostic `json:"diagnostics"`
	}
	if err := json.Unmarshal(body, &overview); err != nil {
		t.Fatal(err)
	}
	seen := map[string]QuotaItemDTO{}
	for _, item := range overview.Quotas {
		seen[item.AuthIndex] = item
	}
	for _, want := range []string{"codex-idx-1", "nan-1", "nan-2", "nan-disabled", "renamed-1"} {
		if _, ok := seen[want]; !ok {
			t.Fatalf("missing %s in %#v", want, overview.Quotas)
		}
	}
	if _, ok := seen["nan-repeat"]; ok {
		t.Fatal("duplicate index was listed")
	}
	if _, ok := seen["other-1"]; ok {
		t.Fatal("unrelated provider was listed")
	}
	if seen["nan-1"].Provider != "nan" || seen["nan-1"].Type != "nan" || seen["nan-1"].Name != "nan nan-1" || seen["nan-disabled"].Disabled != true {
		t.Fatalf("configured subjects = %#v", seen)
	}
	if seen["codex-idx-1"].Provider != "codex" || seen["codex-idx-1"].Source != "auth_file" {
		t.Fatalf("oauth subject changed: %#v", seen["codex-idx-1"])
	}
	// The host, not the name, selects NaN: a renamed official entry keeps its subject,
	// and an entry named nan on another host has none.
	if seen["renamed-1"].Provider != "nan" || seen["renamed-1"].Source != "configured_key" || seen["nan-1"].Source != "configured_key" {
		t.Fatalf("renamed subject = %#v", seen["renamed-1"])
	}
	for _, absent := range []string{"nan-elsewhere", "shared-1"} {
		if _, ok := seen[absent]; ok {
			t.Fatalf("%s was listed", absent)
		}
	}
	wantDiagnostics := []quotaDiagnostic{
		{Reason: "duplicate_auth_index", AuthIndex: "nan-repeat", Count: 2},
		{Reason: "duplicate_auth_index", AuthIndex: "shared-1", Count: 2},
	}
	if !reflect.DeepEqual(overview.Diagnostics, wantDiagnostics) {
		t.Fatalf("diagnostics = %#v", overview.Diagnostics)
	}

	detail, err := client.Get(appServer.URL + "/api/v1/management/quota/nan-2")
	if err != nil {
		t.Fatal(err)
	}
	defer detail.Body.Close()
	if detail.StatusCode != http.StatusOK {
		payload, _ := io.ReadAll(detail.Body)
		t.Fatalf("detail = %d %s", detail.StatusCode, payload)
	}
	for _, ambiguous := range []string{"nan-repeat", "shared-1"} {
		conflict, err := client.Get(appServer.URL + "/api/v1/management/quota/" + ambiguous)
		if err != nil {
			t.Fatal(err)
		}
		payload, _ := io.ReadAll(conflict.Body)
		conflict.Body.Close()
		if conflict.StatusCode != http.StatusConflict || !bytes.Contains(payload, []byte(`"code":"duplicate_auth_index"`)) {
			t.Fatalf("%s detail = %d %s", ambiguous, conflict.StatusCode, payload)
		}
		refresh, err := client.Post(appServer.URL+"/api/v1/management/quota/refresh", "application/json",
			bytes.NewBufferString(`{"auth_index":"`+ambiguous+`"}`))
		if err != nil {
			t.Fatal(err)
		}
		payload, _ = io.ReadAll(refresh.Body)
		refresh.Body.Close()
		if refresh.StatusCode != http.StatusConflict || !bytes.Contains(payload, []byte(`"code":"duplicate_auth_index"`)) {
			t.Fatalf("%s refresh = %d %s", ambiguous, refresh.StatusCode, payload)
		}
	}
	// A batch skips the ambiguous index and reports it; it never fails the batch.
	batch, err := client.Post(appServer.URL+"/api/v1/management/quota/refresh", "application/json",
		bytes.NewBufferString(`{"auth_indexes":["shared-1","nan-elsewhere"]}`))
	if err != nil {
		t.Fatal(err)
	}
	var batchBody struct {
		Total       int               `json:"total"`
		Diagnostics []quotaDiagnostic `json:"diagnostics"`
	}
	if err := json.NewDecoder(batch.Body).Decode(&batchBody); err != nil {
		t.Fatal(err)
	}
	batch.Body.Close()
	if batch.StatusCode != http.StatusOK || batchBody.Total != 0 || !reflect.DeepEqual(batchBody.Diagnostics, wantDiagnostics[1:]) {
		t.Fatalf("batch = %d %#v", batch.StatusCode, batchBody)
	}
	// A mixed batch refreshes its valid subject and still reports the ambiguous one.
	mixed, err := client.Post(appServer.URL+"/api/v1/management/quota/refresh", "application/json",
		bytes.NewBufferString(`{"auth_indexes":["shared-1","renamed-1"]}`))
	if err != nil {
		t.Fatal(err)
	}
	var mixedBody struct {
		Quotas      []quota.NormalizedQuota `json:"quotas"`
		Total       int                     `json:"total"`
		Diagnostics []quotaDiagnostic       `json:"diagnostics"`
	}
	if err := json.NewDecoder(mixed.Body).Decode(&mixedBody); err != nil {
		t.Fatal(err)
	}
	mixed.Body.Close()
	if mixed.StatusCode != http.StatusOK || mixedBody.Total != 1 || mixedBody.Quotas[0].AuthIndex != "renamed-1" ||
		mixedBody.Quotas[0].Error != "" || !reflect.DeepEqual(mixedBody.Diagnostics, wantDiagnostics[1:]) {
		t.Fatalf("mixed batch = %d %#v", mixed.StatusCode, mixedBody)
	}
	// The valid subject took the ledger read, the provider read and the ledger write.
	if calls := validCalls.Load(); calls != 3 {
		t.Fatalf("valid subject calls = %d, want 3", calls)
	}
	// The agent's quota tool refuses the same index with its existing non-retryable code.
	if _, err := handler.operationsService().RefreshQuota(context.Background(), "shared-1"); err == nil || err.Error() != "resource_conflict" {
		t.Fatalf("agent refresh error = %v, want resource_conflict", err)
	}
	if calls := upstreamCalls.Load(); calls != 0 {
		t.Fatalf("ambiguous or non-NaN indexes made %d provider or ledger calls", calls)
	}
}
