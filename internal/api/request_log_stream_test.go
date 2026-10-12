package api

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/oh-my-cpa/oh-my-cpa/internal/repository"
	"github.com/oh-my-cpa/oh-my-cpa/internal/usage"
)

const gatewayLogSecret = "sk-gatewaylogsecretvalue0123456789"

// gatewayLogFixture serves request and error logs by name and records which
// logs the gateway was asked for.
type gatewayLogFixture struct {
	mu        sync.Mutex
	requested []string
}

func (f *gatewayLogFixture) serve(writer http.ResponseWriter, request *http.Request) {
	path := strings.TrimPrefix(request.URL.Path, "/v8/management/observability")
	if !strings.HasPrefix(path, "/logs/") {
		writer.WriteHeader(http.StatusNotFound)
		return
	}
	f.mu.Lock()
	f.requested = append(f.requested, path)
	f.mu.Unlock()
	switch path {
	case "/logs/errors/error-1.log":
		_, _ = writer.Write([]byte("=== REQUEST INFO ===\nAuthorization: Bearer " + gatewayLogSecret + "\n"))
	case "/logs/requests/aaaa1111", "/logs/requests/1111":
		// Relay names a log by the last eight characters of a longer id.
		_, _ = writer.Write([]byte("=== REQUEST INFO ===\nRequest ID: 0000-aaaa1111\n\n=== REQUEST BODY ===\n{\"api_key\":\"" + gatewayLogSecret + "\"}\n"))
	case "/logs/requests/full-request-id":
		_, _ = writer.Write([]byte("=== REQUEST INFO ===\nRequest ID: full-request-id\n\n=== REQUEST BODY ===\nhello\n"))
	case "/logs/requests/noline01":
		_, _ = writer.Write([]byte("=== REQUEST INFO ===\nURL: /v1/chat/completions\n\n=== REQUEST BODY ===\nhello\n"))
	case "/logs/requests/pending1":
		writer.Header().Set("Content-Type", "application/json")
		writer.WriteHeader(http.StatusConflict)
		_, _ = writer.Write([]byte(`{"error":"capture_pending"}`))
	case "/logs/requests/split-request":
		// The credential straddles the 64 KiB read boundary of the stream.
		head := "=== REQUEST INFO ===\nRequest ID: split-request\n\n=== REQUEST BODY ===\n"
		head += strings.Repeat("x", 64*1024-len(head)-10) + " "
		_, _ = writer.Write([]byte(head + gatewayLogSecret[:9]))
		if flusher, ok := writer.(http.Flusher); ok {
			flusher.Flush()
		}
		_, _ = writer.Write([]byte(gatewayLogSecret[9:] + " end\n"))
	default:
		writer.Header().Set("Content-Type", "application/json")
		writer.WriteHeader(http.StatusNotFound)
		_, _ = writer.Write([]byte(`{"error":"log file not found for the given request ID"}`))
	}
}

func (f *gatewayLogFixture) seen() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.requested...)
}

// seedRequestLogEvents stores one usage event per request id and returns the
// stored event id for each.
func seedRequestLogEvents(t *testing.T, repo *repository.Repository, requestIDs ...string) map[string]int64 {
	t.Helper()
	now := time.Now().UTC()
	events := make([]usage.Event, 0, len(requestIDs))
	for index, requestID := range requestIDs {
		event := eventFor("gateway-log-"+requestID, now.Add(-time.Duration(index+1)*time.Second), usage.TokenStats{TotalTokens: 1}, false)
		event.RequestID = requestID
		events = append(events, event)
	}
	if _, err := repo.InsertUsageEvents(context.Background(), events); err != nil {
		t.Fatal(err)
	}
	page, err := repo.ListUsageEvents(context.Background(), repository.UsageEventFilter{
		InstanceID: "default", FromMS: now.Add(-time.Hour).UnixMilli(), ToMS: now.UnixMilli(), Limit: 100,
	})
	if err != nil {
		t.Fatal(err)
	}
	ids := map[string]int64{}
	for _, item := range page.Items {
		ids[item.RequestID] = item.ID
	}
	for _, requestID := range requestIDs {
		if ids[requestID] == 0 {
			t.Fatalf("seed: event for %q not stored", requestID)
		}
	}
	return ids
}

func TestRequestIDMatchesOnlyRelaysShortForm(t *testing.T) {
	for _, testCase := range []struct {
		logged, expected string
		want             bool
	}{
		{"full-request-id", "full-request-id", true},
		{"0000-aaaa1111", "aaaa1111", true},
		{"aaaa1111", "aaaa1111", true},
		{"0000-aaaa1111", "1111", false},
		{"0000-aaaa1111", "1", false},
		{"aaaa1111", "0000-aaaa1111", false},
		{"0000-bbbb2222", "aaaa1111", false},
		{"00000-aaaa1111", "0-aaaa1111", false},
	} {
		if got := requestIDMatches(testCase.logged, testCase.expected); got != testCase.want {
			t.Errorf("requestIDMatches(%q, %q) = %t, want %t", testCase.logged, testCase.expected, got, testCase.want)
		}
	}
}

// The structured inspector and the file downloads give the same identity
// answer for the same file.
func TestGatewayLogIdentityIsOneRuleForEveryRoute(t *testing.T) {
	fixture := &gatewayLogFixture{}
	client, baseURL, repo := startDashboardTestServer(t, fixture.serve)
	ids := seedRequestLogEvents(t, repo, "aaaa1111", "1111", "full-request-id", "noline01")

	for _, testCase := range []struct {
		requestID string
		served    bool
		match     string
	}{
		{requestID: "aaaa1111", served: true, match: "matched"},
		{requestID: "full-request-id", served: true, match: "matched"},
		{requestID: "noline01", served: true, match: "unknown"},
		// An id shorter than Relay's short form is used whole, so a log naming
		// a longer id that merely ends with it belongs to another request.
		{requestID: "1111", served: false, match: "mismatch"},
	} {
		inspector := readInspector(t, client, baseURL, ids[testCase.requestID])
		if inspector.Identity.Match != testCase.match {
			t.Errorf("%s inspector match = %q, want %q", testCase.requestID, inspector.Identity.Match, testCase.match)
		}
		if strings.Contains(fmt.Sprint(inspector.Sections), gatewayLogSecret) {
			t.Errorf("%s inspector leaked the credential", testCase.requestID)
		}
		for _, path := range []string{"request-log", "inspector/export"} {
			response, payload := getJSON(t, client, fmt.Sprintf("%s/omc/api/v1/usage/events/%d/%s", baseURL, ids[testCase.requestID], path))
			if strings.Contains(string(payload), gatewayLogSecret) {
				t.Errorf("%s %s leaked the credential", testCase.requestID, path)
			}
			if testCase.served && response.StatusCode != http.StatusOK {
				t.Errorf("%s %s = %d %s, want the redacted log", testCase.requestID, path, response.StatusCode, payload)
			}
			if !testCase.served && (response.StatusCode != http.StatusNotFound || !strings.Contains(string(payload), requestLogNotFoundCode)) {
				t.Errorf("%s %s = %d %s, want request_log_not_found", testCase.requestID, path, response.StatusCode, payload)
			}
		}
	}

	response, payload := getJSON(t, client, baseURL+"/omc/api/v1/management/request-logs/1111")
	if response.StatusCode != http.StatusNotFound || strings.Contains(string(payload), gatewayLogSecret) {
		t.Fatalf("by-id read of a short id that names another request = %d %s", response.StatusCode, payload)
	}
}

func TestGatewayLogFileRouteRedactsASecretSplitAcrossReads(t *testing.T) {
	fixture := &gatewayLogFixture{}
	client, baseURL, _ := startDashboardTestServer(t, fixture.serve)
	response, payload := getJSON(t, client, baseURL+"/omc/api/v1/management/request-logs/split-request")
	if response.StatusCode != http.StatusOK || !strings.HasSuffix(string(payload), " end\n") {
		t.Fatalf("split log = %d, %d bytes", response.StatusCode, len(payload))
	}
	if strings.Contains(string(payload), gatewayLogSecret) || strings.Contains(string(payload), gatewayLogSecret[9:]) {
		t.Fatal("a credential split across reads reached the browser")
	}
}

// Every read of a gateway log records an explicit outcome after its attempt.
func TestRequestInspectorRecordsAnOutcomeForEveryRead(t *testing.T) {
	fixture := &gatewayLogFixture{}
	client, baseURL, repo := startDashboardTestServer(t, fixture.serve)
	ids := seedRequestLogEvents(t, repo, "aaaa1111", "1111", "pending1", "missing1")
	for _, requestID := range []string{"aaaa1111", "1111", "pending1", "missing1"} {
		readInspector(t, client, baseURL, ids[requestID])
	}
	events, err := repo.ListAuditEvents(context.Background(), 50)
	if err != nil {
		t.Fatal(err)
	}
	outcomes := requestLogAuditOutcomes(events, "request_log.inspect")
	for requestID, want := range map[string]string{"aaaa1111": "success", "1111": "failure", "pending1": "failure", "missing1": "failure"} {
		if !outcomes[requestID]["attempt"] || !outcomes[requestID][want] {
			t.Errorf("%s inspector audit = %v, want attempt and %s", requestID, outcomes[requestID], want)
		}
	}
	if outcomes["1111"]["success"] || outcomes["pending1"]["success"] || outcomes["missing1"]["success"] {
		t.Fatalf("an unserved read was audited as a success: %v", outcomes)
	}
}

// A full log download outlasts the request timeout while the gateway keeps
// sending, and a gateway that stops ends the download with the incomplete marker.
func TestGatewayLogDownloadIsBoundedByProgressNotDuration(t *testing.T) {
	const wait = 200 * time.Millisecond
	release := make(chan struct{})
	t.Cleanup(func() { close(release) })
	serve := func(writer http.ResponseWriter, request *http.Request) {
		flusher := writer.(http.Flusher)
		switch strings.TrimPrefix(request.URL.Path, "/v8/management/observability") {
		case "/logs/requests/steady01":
			_, _ = writer.Write([]byte("=== REQUEST INFO ===\nRequest ID: steady01\n\n=== REQUEST BODY ===\n"))
			for index := 0; index < 8; index++ {
				_, _ = writer.Write([]byte("chunk Bearer " + gatewayLogSecret + "\n"))
				flusher.Flush()
				time.Sleep(wait / 3)
			}
			_, _ = writer.Write([]byte("end\n"))
		case "/logs/requests/stalled1":
			_, _ = writer.Write([]byte("=== REQUEST INFO ===\nRequest ID: stalled1\n\n=== REQUEST BODY ===\nAuthorization: Bearer " + gatewayLogSecret + "\n"))
			flusher.Flush()
			select {
			case <-release:
			case <-request.Context().Done():
			}
		default:
			writer.WriteHeader(http.StatusNotFound)
		}
	}
	client, baseURL, repo := startDashboardTestServer(t, serve, func(handler *Handler) {
		handler.cfg.RequestTimeout = wait
	})

	started := time.Now()
	response, payload := getJSON(t, client, baseURL+"/omc/api/v1/management/request-logs/steady01")
	if elapsed := time.Since(started); elapsed <= wait {
		t.Fatalf("the steady log took %s, so it did not outlast the %s timeout", elapsed, wait)
	}
	text := string(payload)
	if response.StatusCode != http.StatusOK || !strings.HasSuffix(text, "end\n") || strings.Contains(text, exportIncompleteMarker) {
		t.Fatalf("steady log = %d %q, want the whole log", response.StatusCode, text)
	}
	if strings.Contains(text, gatewayLogSecret) || strings.Count(text, "chunk ") != 8 {
		t.Fatalf("steady log was not redacted line by line: %q", text)
	}

	response, payload = getJSON(t, client, baseURL+"/omc/api/v1/management/request-logs/stalled1")
	text = string(payload)
	if response.StatusCode != http.StatusOK || !strings.HasSuffix(text, exportIncompleteMarker) || strings.Contains(text, gatewayLogSecret) {
		t.Fatalf("stalled log = %d %q, want a redacted prefix and the incomplete marker", response.StatusCode, text)
	}

	events, err := repo.ListAuditEvents(context.Background(), 20)
	if err != nil {
		t.Fatal(err)
	}
	outcomes := requestLogAuditOutcomes(events, "request_log.download")
	if !outcomes["steady01"]["success"] || outcomes["steady01"]["failure"] {
		t.Errorf("steady log audit = %v, want success", outcomes["steady01"])
	}
	if !outcomes["stalled1"]["failure"] || outcomes["stalled1"]["success"] {
		t.Errorf("stalled log audit = %v, want failure", outcomes["stalled1"])
	}
}

// The app's server ends every response at its write timeout. A log download
// moves that deadline with each write, so a log that keeps arriving outlasts
// it, and so does an answer that came late from the gateway.
func TestGatewayLogDownloadOutlastsTheServerWriteTimeout(t *testing.T) {
	const writeTimeout = 150 * time.Millisecond
	serve := func(writer http.ResponseWriter, request *http.Request) {
		flusher := writer.(http.Flusher)
		// Each answer starts after the server's whole write timeout has passed.
		time.Sleep(2 * writeTimeout)
		switch strings.TrimPrefix(request.URL.Path, "/v8/management/observability") {
		case "/logs/requests/steady02":
			_, _ = writer.Write([]byte("=== REQUEST INFO ===\nRequest ID: steady02\n\n=== REQUEST BODY ===\n"))
			for index := 0; index < 6; index++ {
				_, _ = writer.Write([]byte("chunk Bearer " + gatewayLogSecret + "\n"))
				flusher.Flush()
				time.Sleep(writeTimeout)
			}
			_, _ = writer.Write([]byte("end\n"))
		default:
			writer.WriteHeader(http.StatusNotFound)
		}
	}
	client, baseURL, _ := startDashboardTestServerWriting(t, writeTimeout, serve, func(handler *Handler) {
		handler.gatewayLogWriteWait = 2 * time.Second
	})

	started := time.Now()
	response, payload := getJSON(t, client, baseURL+"/omc/api/v1/management/request-logs/steady02")
	if elapsed := time.Since(started); elapsed <= 4*writeTimeout {
		t.Fatalf("the steady log took %s, so it did not outlast the %s write timeout", elapsed, writeTimeout)
	}
	text := string(payload)
	if response.StatusCode != http.StatusOK || !strings.HasSuffix(text, "end\n") || strings.Contains(text, exportIncompleteMarker) {
		t.Fatalf("steady log = %d %q, want the whole log", response.StatusCode, text)
	}
	if strings.Contains(text, gatewayLogSecret) || strings.Count(text, "chunk ") != 6 {
		t.Fatalf("steady log was not redacted line by line: %q", text)
	}

	response, payload = getJSON(t, client, baseURL+"/omc/api/v1/management/request-logs/missing2")
	if response.StatusCode != http.StatusNotFound || !strings.Contains(string(payload), requestLogNotFoundCode) {
		t.Fatalf("late missing answer = %d %s, want the 404", response.StatusCode, payload)
	}
}

// No gateway log route asks the gateway for a log it cannot audit first.
func TestGatewayLogRoutesFailClosedWithoutAnAudit(t *testing.T) {
	fixture := &gatewayLogFixture{}
	client, baseURL, repo := startDashboardTestServer(t, fixture.serve)
	ids := seedRequestLogEvents(t, repo, "aaaa1111")
	if _, err := repo.SQL().Exec("DROP TABLE audit_events"); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{
		"/omc/api/v1/management/request-error-logs/error-1.log",
		"/omc/api/v1/management/request-logs/aaaa1111",
		fmt.Sprintf("/omc/api/v1/usage/events/%d/request-log", ids["aaaa1111"]),
		fmt.Sprintf("/omc/api/v1/usage/events/%d/inspector/export", ids["aaaa1111"]),
		fmt.Sprintf("/omc/api/v1/usage/events/%d/inspector", ids["aaaa1111"]),
	} {
		response, payload := getJSON(t, client, baseURL+path)
		if response.StatusCode != http.StatusInternalServerError || !strings.Contains(string(payload), "audit") ||
			strings.Contains(string(payload), gatewayLogSecret) || response.Header.Get("Content-Disposition") != "" {
			t.Errorf("%s = %d %s, want an audit failure without log text", path, response.StatusCode, payload)
		}
	}
	if seen := fixture.seen(); len(seen) != 0 {
		t.Fatalf("the gateway was asked for logs it could not audit: %v", seen)
	}
}
