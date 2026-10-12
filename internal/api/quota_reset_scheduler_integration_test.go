package api

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/oh-my-cpa/oh-my-cpa/internal/crypto"
	"github.com/oh-my-cpa/oh-my-cpa/internal/domain"
	"github.com/oh-my-cpa/oh-my-cpa/internal/quota"
	"github.com/oh-my-cpa/oh-my-cpa/internal/repository"
)

func TestRefreshDueQuotaUsesTheConfiguredSubjectOnce(t *testing.T) {
	var calls atomic.Int32
	entered, release := make(chan struct{}), make(chan struct{})
	var releaseOnce sync.Once
	unblock := func() { releaseOnce.Do(func() { close(release) }) }
	cpa := newFakeCPA(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writer.Header().Set("Content-Type", "application/json")
		switch request.URL.Path {
		case "/v8/management/credentials":
			_, _ = writer.Write([]byte(`{"files":[]}`))
		case "/v0/management/openai-compatibility":
			_, _ = writer.Write([]byte(`{"openai-compatibility":[{"name":"nan","base-url":"https://api.nan.builders/v1","models":[{"name":"qwen3.8-flash"}],"api-key-entries":[{"api-key":"sk-enabled","auth-index":"1111111111111111"}]},{"name":"nan","disabled":true,"base-url":"https://api.nan.builders/v1","models":[{"name":"qwen3.8-flash"}],"api-key-entries":[{"api-key":"sk-disabled","auth-index":"2222222222222222"}]}]}`))
		case "/v8/management/availability/period-exhaustion":
			_, _ = writer.Write([]byte(`{"record":null,"observed_at":"2026-10-12T03:00:00Z"}`))
		case "/v8/management/requests/api-call":
			var body struct {
				AuthIndex string `json:"auth_index"`
			}
			if err := json.NewDecoder(request.Body).Decode(&body); err != nil || body.AuthIndex != "1111111111111111" {
				t.Errorf("disabled or incorrect quota subject: %s, %v", body.AuthIndex, err)
			}
			call := calls.Add(1)
			if call == 1 {
				close(entered)
				select {
				case <-release:
				case <-request.Context().Done():
					return
				}
			}
			if call <= 2 {
				writer.WriteHeader(http.StatusBadGateway)
				return
			}
			periodEnd := "2026-10-11T00:00:00Z"
			if call >= 4 {
				periodEnd = "2026-11-09T00:00:00Z"
			}
			_, _ = writer.Write([]byte(`{"status_code":200,"body":{"models":[{"model":"qwen3.8-flash","tokensUsed":1,"cap":10,"remaining":9,"periodEnd":"` + periodEnd + `"}]}}`))
		default:
			writer.WriteHeader(http.StatusNotFound)
		}
	}))
	defer cpa.Close()
	defer unblock()
	handler := configuredQuotaHandler(t, cpa.URL)
	now := time.Date(2026, 10, 12, 3, 0, 0, 0, time.UTC)
	handler.nowFn = func() time.Time { return now }
	windows, _ := json.Marshal([]map[string]any{{"model": "qwen3.8-flash", "reset_at_ms": now.Add(-time.Minute).UnixMilli()}})
	for _, index := range []string{"1111111111111111", "2222222222222222"} {
		if err := handler.repo.SaveQuotaSnapshot(context.Background(), repository.QuotaSnapshotRecord{AuthIndex: index, Provider: "nan", Status: "idle", WindowsJSON: string(windows), ObservedAtMS: now.Add(-time.Hour).UnixMilli()}); err != nil {
			t.Fatal(err)
		}
	}
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	results := make(chan error, 2)
	go func() { results <- handler.RefreshDueQuota(ctx) }()
	select {
	case <-entered:
	case <-ctx.Done():
		t.Fatal("first quota fetch did not start")
	}
	started := make(chan struct{})
	go func() { close(started); results <- handler.RefreshDueQuota(ctx) }()
	<-started
	unblock()
	for range 2 {
		if err := <-results; err != nil {
			t.Fatal(err)
		}
	}
	if calls.Load() != 1 {
		t.Fatalf("overlapping refreshes bypassed failure hold: %d", calls.Load())
	}
	restarted := configuredQuotaHandler(t, cpa.URL)
	restarted.repo, restarted.cipher, restarted.nowFn = handler.repo, handler.cipher, handler.nowFn
	if err := restarted.RefreshDueQuota(ctx); err != nil {
		t.Fatal(err)
	}
	if calls.Load() != 2 {
		t.Fatalf("restart did not retry the retained due failure: %d", calls.Load())
	}
	now = now.Add(2 * time.Minute)
	if err := handler.RefreshDueQuota(ctx); err != nil {
		t.Fatal(err)
	}
	if calls.Load() != 3 {
		t.Fatalf("failed quota was not retried after backoff: %d", calls.Load())
	}
	if err := handler.RefreshDueQuota(ctx); err != nil {
		t.Fatal(err)
	}
	if calls.Load() != 3 {
		t.Fatal("successful but still-overdue reading was fetched again immediately")
	}
	now = now.Add(2 * time.Minute)
	if err := handler.RefreshDueQuota(ctx); err != nil {
		t.Fatal(err)
	}
	if calls.Load() != 4 {
		t.Fatal("overdue reading did not get a bounded follow-up")
	}
	restarted = configuredQuotaHandler(t, cpa.URL)
	restarted.repo, restarted.cipher, restarted.nowFn = handler.repo, handler.cipher, handler.nowFn
	if err := restarted.RefreshDueQuota(ctx); err != nil {
		t.Fatal(err)
	}
	if calls.Load() != 4 {
		t.Fatal("future verified reset was fetched on restart")
	}
}

func TestRefreshDueQuotaRetriesAFailedPeriodDelivery(t *testing.T) {
	var calls atomic.Int32
	var puts atomic.Int32
	cpa := newFakeCPA(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writer.Header().Set("Content-Type", "application/json")
		switch request.URL.Path {
		case "/v8/management/credentials":
			_, _ = writer.Write([]byte(`{"files":[]}`))
		case "/v0/management/openai-compatibility":
			_, _ = writer.Write([]byte(`{"openai-compatibility":[{"name":"nan","base-url":"https://api.nan.builders/v1","models":[{"name":"qwen3.8-flash"},{"name":"absent-model"}],"api-key-entries":[{"api-key":"sk-enabled","auth-index":"1111111111111111"}]}]}`))
		case "/v8/management/availability/period-exhaustion":
			if request.Method == http.MethodPut {
				puts.Add(1)
				writer.WriteHeader(http.StatusServiceUnavailable)
				_, _ = writer.Write([]byte(`{"error":"unavailable"}`))
				return
			}
			_, _ = writer.Write([]byte(`{"record":null,"observed_at":"2026-10-12T03:00:00Z"}`))
		case "/v8/management/requests/api-call":
			calls.Add(1)
			_, _ = writer.Write([]byte(`{"status_code":200,"body":"{\"models\":[{\"model\":\"qwen3.8-flash\",\"tokensUsed\":1,\"cap\":10,\"remaining\":9,\"periodEnd\":\"2026-11-09T00:00:00Z\"}]}"}`))
		default:
			writer.WriteHeader(http.StatusNotFound)
		}
	}))
	defer cpa.Close()
	handler := configuredQuotaHandler(t, cpa.URL)
	now := time.Date(2026, 10, 12, 3, 0, 0, 0, time.UTC)
	handler.nowFn = func() time.Time { return now }
	windows, _ := json.Marshal([]map[string]any{{"model": "qwen3.8-flash", "reset_at_ms": now.Add(-time.Minute).UnixMilli()}})
	if err := handler.repo.SaveQuotaSnapshot(context.Background(), repository.QuotaSnapshotRecord{AuthIndex: "1111111111111111", Provider: "nan", Status: "idle", WindowsJSON: string(windows), ObservedAtMS: now.Add(-time.Hour).UnixMilli()}); err != nil {
		t.Fatal(err)
	}
	if err := handler.RefreshDueQuota(context.Background()); err != nil {
		t.Fatal(err)
	}
	latest, err := handler.repo.GetLatestQuotaSnapshots(context.Background(), []string{"1111111111111111"})
	if err != nil {
		t.Fatal(err)
	}
	var stored []quota.QuotaWindow
	if err := json.Unmarshal([]byte(latest["1111111111111111"].WindowsJSON), &stored); err != nil {
		t.Fatal(err)
	}
	if !stored[0].PeriodDeliveryPending || stored[0].ResetAtMS == nil || *stored[0].ResetAtMS <= now.UnixMilli() {
		t.Fatalf("future reset did not retain the failed delivery: %+v", stored)
	}
	beforeHold := calls.Load()
	if err := handler.RefreshDueQuota(context.Background()); err != nil {
		t.Fatal(err)
	}
	if calls.Load() != beforeHold {
		t.Fatal("successful refresh with a past reset was fetched again immediately")
	}
	restarted := testHandler(t, "")
	restarted.repo = handler.repo
	restarted.cipher = handler.cipher
	restarted.nowFn = func() time.Time { return now.Add(time.Hour) }
	before := calls.Load()
	if err := restarted.RefreshDueQuota(context.Background()); err != nil {
		t.Fatal(err)
	}
	if calls.Load() != before+1 || puts.Load() < 2 {
		t.Fatalf("restart did not retry the pending delivery: fetches=%d puts=%d", calls.Load(), puts.Load())
	}
}

func configuredQuotaHandler(t *testing.T, baseURL string) *Handler {
	t.Helper()
	handler := testHandler(t, "")
	cipher, err := crypto.New("01234567890123456789012345678901")
	if err != nil {
		t.Fatal(err)
	}
	ciphertext, nonce, err := cipher.Encrypt([]byte("management-secret"))
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	if err := handler.repo.UpsertInstance(context.Background(), domain.CPAInstance{ID: "default", Name: "Default", BaseURL: baseURL, UsageAddr: strings.TrimPrefix(baseURL, "http://"), ManagementKeyCiphertext: ciphertext, ManagementKeyNonce: nonce, CreatedAt: now, UpdatedAt: now}); err != nil {
		t.Fatal(err)
	}
	return handler
}
