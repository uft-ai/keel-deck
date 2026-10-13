package api

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/oh-my-cpa/oh-my-cpa/internal/crypto"
	"github.com/oh-my-cpa/oh-my-cpa/internal/domain"
	"github.com/oh-my-cpa/oh-my-cpa/internal/quota"
	"github.com/oh-my-cpa/oh-my-cpa/internal/repository"
)

const (
	nanEnabledIndex   = "1111111111111111"
	nanDisabledIndex  = "2222222222222222"
	nanAmbiguousIndex = "4444444444444444"
)

// nanLedgerFixture is a gateway with configured NaN keys and a period ledger. It records
// every quota probe and ledger call in order, and answers ledger writes with putStatus.
type nanLedgerFixture struct {
	mu        sync.Mutex
	calls     []string
	putStatus int
}

func (f *nanLedgerFixture) record(call string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, call)
}

func (f *nanLedgerFixture) setPutStatus(status int) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.putStatus = status
}

// take returns the calls made since the last take.
func (f *nanLedgerFixture) take() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	calls := f.calls
	f.calls = nil
	return calls
}

func newNanLedgerHandler(t *testing.T, fixture *nanLedgerFixture) *Handler {
	t.Helper()
	cpa := newFakeCPA(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writer.Header().Set("Content-Type", "application/json")
		switch request.URL.Path {
		case "/v8/management/credentials":
			// The auth file shares its index with a configured key: neither is read.
			_, _ = writer.Write([]byte(`{"files":[{"id":"f-amb","name":"amb.json","auth_index":"` + nanAmbiguousIndex + `","type":"codex","provider":"codex"}]}`))
		case "/v0/management/openai-compatibility":
			_, _ = writer.Write([]byte(`{"openai-compatibility":[` +
				`{"name":"nan","base-url":"https://api.nan.builders/v1","models":[{"name":"qwen3.8-flash","alias":"fast"}],"api-key-entries":[{"api-key":"sk-enabled","auth-index":"` + nanEnabledIndex + `"},{"api-key":"sk-amb","auth-index":"` + nanAmbiguousIndex + `"}]},` +
				`{"name":"nan","disabled":true,"base-url":"https://api.nan.builders/v1","models":[{"name":"qwen3.8-flash"}],"api-key-entries":[{"api-key":"sk-disabled","auth-index":"` + nanDisabledIndex + `"}]},` +
				// The name selects nothing: this key on another host is never read.
				`{"name":"nan","base-url":"http://172.31.77.10:8090/v1","models":[{"name":"qwen3.8-flash"}],"api-key-entries":[{"api-key":"sk-elsewhere","auth-index":"5555555555555555"}]}]}`))
		case "/v8/management/availability/period-exhaustion":
			if request.Method == http.MethodPut {
				var observation struct {
					AuthIndex string `json:"auth_index"`
					Model     string `json:"model"`
				}
				_ = json.NewDecoder(request.Body).Decode(&observation)
				fixture.record("put " + observation.AuthIndex + " " + observation.Model)
				fixture.mu.Lock()
				status := fixture.putStatus
				fixture.mu.Unlock()
				if status != http.StatusOK {
					writer.WriteHeader(status)
					_, _ = writer.Write([]byte(`{"error":"ledger refused"}`))
					return
				}
				_, _ = writer.Write([]byte(`{"record":{"auth_index":"` + observation.AuthIndex + `","model":"` + observation.Model + `","version":1}}`))
				return
			}
			query := request.URL.Query()
			fixture.record("get " + query.Get("auth_index") + " " + query.Get("model"))
			_, _ = writer.Write([]byte(`{"record":null,"observed_at":"` + time.Now().UTC().Format(time.RFC3339Nano) + `"}`))
		case "/v8/management/requests/api-call":
			var body struct {
				AuthIndex string `json:"auth_index"`
			}
			_ = json.NewDecoder(request.Body).Decode(&body)
			fixture.record("probe " + body.AuthIndex)
			periodEnd := time.Now().Add(30 * 24 * time.Hour).UTC().Format(time.RFC3339)
			_, _ = writer.Write([]byte(`{"status_code":200,"body":"{\"models\":[{\"model\":\"qwen3.8-flash\",\"tokensUsed\":1,\"cap\":10,\"remaining\":9,\"periodEnd\":\"` + periodEnd + `\"}]}"}`))
		default:
			writer.WriteHeader(http.StatusNotFound)
		}
	}))
	t.Cleanup(cpa.Close)
	return configuredQuotaHandler(t, cpa.URL)
}

// restartedHandler is a new process on the same database: it has no attempt memory.
func restartedHandler(t *testing.T, from *Handler) *Handler {
	t.Helper()
	handler := testHandler(t, "")
	handler.repo, handler.cipher, handler.nowFn = from.repo, from.cipher, from.nowFn
	return handler
}

func storedNanWindows(t *testing.T, handler *Handler) []quota.QuotaWindow {
	t.Helper()
	latest, err := handler.repo.GetLatestQuotaSnapshots(context.Background(), []string{nanEnabledIndex})
	if err != nil {
		t.Fatal(err)
	}
	var windows []quota.QuotaWindow
	if err := json.Unmarshal([]byte(latest[nanEnabledIndex].WindowsJSON), &windows); err != nil {
		t.Fatal(err)
	}
	return windows
}

func runNanRound(t *testing.T, handler *Handler) quotaAutoRefreshRound {
	t.Helper()
	round, err := handler.runQuotaAutoRefreshRound(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	return round
}

func saveNanResetSnapshot(t *testing.T, handler *Handler, windows string) {
	t.Helper()
	if err := handler.repo.SaveQuotaSnapshot(context.Background(), repository.QuotaSnapshotRecord{
		AuthIndex: nanEnabledIndex, Provider: "nan", Status: "idle", WindowsJSON: windows,
		ObservedAtMS: time.Now().Add(-time.Hour).UnixMilli(),
	}); err != nil {
		t.Fatal(err)
	}
}

func pastResetWindows() string {
	return fmt.Sprintf(`[{"model":"qwen3.8-flash","reset_at_ms":%d}]`, time.Now().Add(-2*time.Minute).UnixMilli())
}

func TestQuotaAutoRefreshReadsEachConfiguredNanKeyOnce(t *testing.T) {
	fixture := &nanLedgerFixture{putStatus: http.StatusOK}
	handler := newNanLedgerHandler(t, fixture)

	// Nothing is stored yet, so the default six-hour schedule reads every eligible key.
	round := runNanRound(t, handler)
	calls := fixture.take()
	want := []string{"get " + nanEnabledIndex + " qwen3.8-flash", "probe " + nanEnabledIndex, "put " + nanEnabledIndex + " qwen3.8-flash"}
	if round.Due != 1 || round.Refreshed != 1 || strings.Join(calls, "|") != strings.Join(want, "|") {
		t.Fatalf("round = %+v calls = %q, want only the enabled key, ledger read first, configured model", round, calls)
	}
	if windows := storedNanWindows(t, handler); len(windows) == 0 || windows[0].PeriodDeliveryPending || windows[0].PeriodDeliveryStatus != "" {
		t.Fatalf("delivered reading = %+v, want no pending delivery", windows)
	}

	// The reading is fresh and its renewal is a month away: neither this process nor a
	// restarted one reads it again.
	if round := runNanRound(t, handler); round.Due != 0 {
		t.Fatalf("second round = %+v, want nothing due", round)
	}
	if round := runNanRound(t, restartedHandler(t, handler)); round.Due != 0 {
		t.Fatalf("restarted round = %+v, want the verified renewal left alone", round)
	}
	if calls := fixture.take(); len(calls) != 0 {
		t.Fatalf("later rounds called %q", calls)
	}
}

func TestQuotaAutoRefreshWarnsOnceAboutAnAmbiguousIndex(t *testing.T) {
	fixture := &nanLedgerFixture{putStatus: http.StatusOK}
	handler := newNanLedgerHandler(t, fixture)
	var logs bytes.Buffer
	handler.logger = slog.New(slog.NewTextHandler(&logs, nil))

	runNanRound(t, handler)
	runNanRound(t, handler)
	if count := strings.Count(logs.String(), "duplicate_auth_index"); count != 1 {
		t.Fatalf("warnings = %d, want one for an unchanged diagnostic set:\n%s", count, logs.String())
	}
	if !strings.Contains(logs.String(), nanAmbiguousIndex+":2") {
		t.Fatalf("warning does not name the index and its claims:\n%s", logs.String())
	}
	for _, call := range fixture.take() {
		if strings.Contains(call, nanAmbiguousIndex) || strings.Contains(call, "5555555555555555") {
			t.Fatalf("scheduler read a key it must skip: %q", call)
		}
	}
}

func TestQuotaAutoRefreshRetriesAnUnavailableDelivery(t *testing.T) {
	fixture := &nanLedgerFixture{putStatus: http.StatusServiceUnavailable}
	handler := newNanLedgerHandler(t, fixture)
	offset := time.Duration(0)
	handler.nowFn = func() time.Time { return time.Now().Add(offset) }
	setAutoRefreshPolicy(t, handler, `{"interval_minutes":0,"refresh_on_reset":true}`)
	saveNanResetSnapshot(t, handler, pastResetWindows())

	if round := runNanRound(t, handler); round.ByReset != 1 || round.Refreshed != 1 {
		t.Fatalf("reset round = %+v, want one reading saved although delivery failed", round)
	}
	windows := storedNanWindows(t, handler)
	if !windows[0].PeriodDeliveryPending || windows[0].PeriodDeliveryStatus != quota.PeriodDeliveryUnavailable {
		t.Fatalf("stored window = %+v, want an unavailable delivery", windows[0])
	}
	if windows[0].ResetAtMS == nil || *windows[0].ResetAtMS <= time.Now().UnixMilli() {
		t.Fatal("the saved reading lost its future renewal")
	}
	fixture.take()

	offset = 9 * time.Minute
	if round := runNanRound(t, handler); round.Due != 0 {
		t.Fatalf("round after nine minutes = %+v, want nothing due", round)
	}
	offset = 10*time.Minute + time.Second
	if round := runNanRound(t, handler); round.Due != 1 {
		t.Fatalf("round after ten minutes = %+v, want the delivery retried", round)
	}
	if calls := fixture.take(); len(calls) != 3 || !strings.HasPrefix(calls[0], "get ") || !strings.HasPrefix(calls[2], "put ") {
		t.Fatalf("retry calls = %q, want a fresh ledger read, the probe and the delivery", calls)
	}

	// A restarted process has no attempt memory: the saved status alone brings it back.
	restarted := restartedHandler(t, handler)
	offset = 25 * time.Minute
	fixture.setPutStatus(http.StatusOK)
	if round := runNanRound(t, restarted); round.Due != 1 || round.Refreshed != 1 {
		t.Fatalf("restarted round = %+v, want the saved unavailable delivery retried", round)
	}
	if windows := storedNanWindows(t, handler); windows[0].PeriodDeliveryPending || windows[0].PeriodDeliveryStatus != "" {
		t.Fatalf("delivered reading = %+v, want the pending status cleared", windows[0])
	}
	fixture.take()
	offset = 40 * time.Minute
	if round := runNanRound(t, restarted); round.Due != 0 {
		t.Fatalf("round after delivery = %+v, want no ten-minute rereads", round)
	}
	if calls := fixture.take(); len(calls) != 0 {
		t.Fatalf("delivered key was called again: %q", calls)
	}
}

func TestQuotaAutoRefreshLeavesARejectedDeliveryToTheNormalRead(t *testing.T) {
	fixture := &nanLedgerFixture{putStatus: http.StatusConflict}
	handler := newNanLedgerHandler(t, fixture)
	offset := time.Duration(0)
	handler.nowFn = func() time.Time { return time.Now().Add(offset) }
	setAutoRefreshPolicy(t, handler, `{"interval_minutes":0,"refresh_on_reset":true}`)
	saveNanResetSnapshot(t, handler, pastResetWindows())

	runNanRound(t, handler)
	if windows := storedNanWindows(t, handler); windows[0].PeriodDeliveryStatus != quota.PeriodDeliveryRejected {
		t.Fatalf("stored window = %+v, want a rejected delivery", windows[0])
	}
	fixture.take()
	offset = 30 * time.Minute
	if round := runNanRound(t, handler); round.Due != 0 {
		t.Fatalf("round = %+v, want a rejection to wait for the next reset or schedule", round)
	}
}

func TestQuotaAutoRefreshWithBothControlsOffReadsNothing(t *testing.T) {
	fixture := &nanLedgerFixture{putStatus: http.StatusOK}
	handler := newNanLedgerHandler(t, fixture)
	handler.nowFn = func() time.Time { return time.Now().Add(30 * time.Minute) }
	setAutoRefreshPolicy(t, handler, `{"interval_minutes":0,"refresh_on_reset":false}`)
	saveNanResetSnapshot(t, handler, fmt.Sprintf(`[{"model":"qwen3.8-flash","reset_at_ms":%d,"period_delivery_pending":true,"period_delivery_status":"unavailable"}]`, time.Now().Add(-2*time.Minute).UnixMilli()))

	if round := runNanRound(t, handler); round.Due != 0 {
		t.Fatalf("round = %+v, want no automatic read", round)
	}
	if calls := fixture.take(); len(calls) != 0 {
		t.Fatalf("controls off still called %q", calls)
	}
}

func TestQuotaAutoRefreshGivesALegacyPendingRowNoEarlyRetry(t *testing.T) {
	fixture := &nanLedgerFixture{putStatus: http.StatusOK}
	handler := newNanLedgerHandler(t, fixture)
	handler.nowFn = func() time.Time { return time.Now().Add(30 * time.Minute) }
	setAutoRefreshPolicy(t, handler, `{"interval_minutes":0,"refresh_on_reset":true}`)
	saveNanResetSnapshot(t, handler, fmt.Sprintf(`[{"model":"qwen3.8-flash","reset_at_ms":%d,"period_delivery_pending":true}]`, time.Now().Add(24*time.Hour).UnixMilli()))

	if round := runNanRound(t, handler); round.Due != 0 {
		t.Fatalf("round = %+v, want a row without a status to wait for its reset", round)
	}
}

func TestMarkPendingPeriodDeliveryRecordsWhy(t *testing.T) {
	reading := &quota.NormalizedQuota{
		Windows: []quota.QuotaWindow{{Model: ""}, {Model: "a"}, {Model: "b"}, {Model: "c"}},
		PeriodDeliveries: []quota.PeriodDelivery{
			{Model: "a", Status: quota.PeriodDeliveryRejected},
			{Model: "b", Status: quota.PeriodDeliveryUnavailable},
			{Model: "c", Status: "not_submitted"},
		},
	}
	markPendingPeriodDelivery(reading)
	got := make([]string, 0, len(reading.Windows))
	for _, window := range reading.Windows {
		got = append(got, fmt.Sprintf("%s:%t:%s", window.Model, window.PeriodDeliveryPending, window.PeriodDeliveryStatus))
	}
	want := ":true:unavailable|a:true:rejected|b:true:unavailable|c:false:"
	if strings.Join(got, "|") != want {
		t.Fatalf("windows = %s, want %s", strings.Join(got, "|"), want)
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
