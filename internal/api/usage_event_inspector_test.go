package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/oh-my-cpa/oh-my-cpa/internal/repository"
	"github.com/oh-my-cpa/oh-my-cpa/internal/requestlog"
	"github.com/oh-my-cpa/oh-my-cpa/internal/usage"
)

const inspectorLogPath = "/v8/management/observability/logs/requests/"

type inspectorPayload struct {
	CaptureState  string `json:"capture_state"`
	CaptureReason string `json:"capture_reason"`
	ParseState    string `json:"parse_state"`
	Boundaries    string `json:"boundaries"`
	Identity      struct {
		RequestID       string `json:"request_id"`
		LoggedRequestID string `json:"logged_request_id"`
		Match           string `json:"match"`
	} `json:"identity"`
	Limits struct {
		ScanBytes    int64 `json:"scan_bytes"`
		SectionBytes int   `json:"section_bytes"`
		MaxSections  int   `json:"max_sections"`
	} `json:"limits"`
	Preview struct {
		BytesRead       int64 `json:"bytes_read"`
		EOF             bool  `json:"eof"`
		Truncated       bool  `json:"truncated"`
		SectionsOmitted int   `json:"sections_omitted"`
	} `json:"preview"`
	Sections []requestlog.Section `json:"sections"`
}

// seedInspectorEvent stores one usage event and returns its id.
func seedInspectorEvent(t *testing.T, repo *repository.Repository, requestID string) int64 {
	t.Helper()
	now := time.Now().UTC()
	event := eventFor("inspector-"+requestID, now.Add(-time.Minute), usage.TokenStats{TotalTokens: 1}, false)
	event.RequestID = requestID
	if _, err := repo.InsertUsageEvents(context.Background(), []usage.Event{event}); err != nil {
		t.Fatal(err)
	}
	page, err := repo.ListUsageEvents(context.Background(), repository.UsageEventFilter{
		InstanceID: "default", FromMS: now.Add(-time.Hour).UnixMilli(), ToMS: now.UnixMilli(),
	})
	if err != nil || len(page.Items) != 1 {
		t.Fatalf("seed: %v", err)
	}
	return page.Items[0].ID
}

func readInspector(t *testing.T, client *http.Client, baseURL string, id int64) inspectorPayload {
	t.Helper()
	response, payload := getJSON(t, client, fmt.Sprintf("%s/omc/api/v1/usage/events/%d/inspector", baseURL, id))
	if response.StatusCode != http.StatusOK || response.Header.Get("Cache-Control") != "no-store" {
		t.Fatalf("inspector = %d %s", response.StatusCode, payload)
	}
	var decoded inspectorPayload
	if err := json.Unmarshal(payload, &decoded); err != nil {
		t.Fatalf("decode %s: %v", payload, err)
	}
	if decoded.Sections == nil || decoded.Boundaries != "heuristic" || decoded.Limits.ScanBytes == 0 || decoded.Identity.RequestID == "" {
		t.Fatalf("inspector DTO is incomplete: %s", payload)
	}
	return decoded
}

func serveRelayFixture(t *testing.T, name string) func(http.ResponseWriter, *http.Request) {
	t.Helper()
	raw, err := os.ReadFile("../requestlog/testdata/" + name)
	if err != nil {
		t.Fatal(err)
	}
	return func(writer http.ResponseWriter, request *http.Request) {
		if strings.HasPrefix(request.URL.Path, inspectorLogPath) {
			_, _ = writer.Write(raw)
			return
		}
		writer.WriteHeader(http.StatusNotFound)
	}
}

func TestUsageEventInspectorMatchesRelayRequestID(t *testing.T) {
	client, baseURL, repo := startDashboardTestServer(t, serveRelayFixture(t, "relay-chat-retry.log"))
	id := seedInspectorEvent(t, repo, "01a12e30-d92f-7d85-b6a0-d6e8093d62ba")
	inspector := readInspector(t, client, baseURL, id)
	if inspector.CaptureState != "retained" || inspector.ParseState != "recognized" || inspector.Identity.Match != "matched" || !inspector.Preview.EOF {
		t.Fatalf("inspector = %+v", inspector)
	}
	if len(inspector.Sections) != 8 || inspector.Sections[5].Status != 429 || inspector.Sections[6].Attempt != 2 {
		t.Fatalf("sections = %+v", inspector.Sections)
	}
}

func TestUsageEventInspectorHidesAnotherRequestsLog(t *testing.T) {
	client, baseURL, repo := startDashboardTestServer(t, serveRelayFixture(t, "relay-chat-retry.log"))
	// Relay's v0-compatible lookup resolves by the last eight characters, so a
	// different request id with the same suffix can return another request's file.
	id := seedInspectorEvent(t, repo, "ffffffff-ffff-ffff-ffff-ffff093d62ba")
	inspector := readInspector(t, client, baseURL, id)
	if inspector.Identity.Match != "mismatch" || inspector.Identity.LoggedRequestID != "01a12e30-d92f-7d85-b6a0-d6e8093d62ba" || len(inspector.Sections) != 0 {
		t.Fatalf("mismatched log was shown: %+v", inspector)
	}
	for _, path := range []string{"request-log"} {
		response, payload := getJSON(t, client, fmt.Sprintf("%s/omc/api/v1/usage/events/%d/%s", baseURL, id, path))
		if response.StatusCode != http.StatusNotFound || !strings.Contains(string(payload), `"code":"request_log_not_found"`) || response.Header.Get("Content-Disposition") != "" {
			t.Fatalf("%s served another request's log: %d %s", path, response.StatusCode, payload)
		}
	}
}

func TestUsageEventInspectorLegacyLogHasUnknownIdentity(t *testing.T) {
	raw := "=== REQUEST INFO ===\nURL: /v1/chat/completions\n\n=== REQUEST BODY ===\n{\"model\":\"m\"}\n\n=== RESPONSE ===\nStatus: 200\n\nok\n"
	client, baseURL, repo := startDashboardTestServer(t, func(writer http.ResponseWriter, request *http.Request) {
		if strings.HasPrefix(request.URL.Path, inspectorLogPath) {
			_, _ = writer.Write([]byte(raw))
			return
		}
		writer.WriteHeader(http.StatusNotFound)
	})
	inspector := readInspector(t, client, baseURL, seedInspectorEvent(t, repo, "legacy"))
	if inspector.Identity.Match != "unknown" || inspector.ParseState != "recognized" || len(inspector.Sections) != 3 {
		t.Fatalf("legacy log = %+v", inspector)
	}
}

func TestUsageEventInspectorBinaryKeepsStatusAndHeaders(t *testing.T) {
	client, baseURL, repo := startDashboardTestServer(t, serveRelayFixture(t, "relay-speech-binary.log"))
	inspector := readInspector(t, client, baseURL, seedInspectorEvent(t, repo, "01a12e30-d930-7843-bf0a-1b05e1eba84f"))
	if inspector.Identity.Match != "matched" || len(inspector.Sections) != 6 {
		t.Fatalf("binary log = %+v", inspector)
	}
	last := inspector.Sections[len(inspector.Sections)-1]
	if inspector.ParseState != "recognized" || !last.BodyOmitted || last.OmissionReason != "binary" || last.Status != 200 || !strings.Contains(last.Meta, "audio/wav") {
		t.Fatalf("binary response = %+v", last)
	}
}

func TestUsageEventInspectorMapsMissingLogs(t *testing.T) {
	client, baseURL, repo := startDashboardTestServer(t, func(writer http.ResponseWriter, request *http.Request) {
		writer.WriteHeader(http.StatusNotFound)
		_, _ = writer.Write([]byte(`{"error":"log file not found"}`))
	})
	inspector := readInspector(t, client, baseURL, seedInspectorEvent(t, repo, "gone"))
	if inspector.CaptureState != "expired_unknown" || inspector.CaptureReason != "request_log_not_found" || len(inspector.Sections) != 0 {
		t.Fatalf("missing log = %+v", inspector)
	}

	unavailable, unavailableURL, unavailableRepo := startDashboardTestServer(t, func(writer http.ResponseWriter, request *http.Request) {
		writer.WriteHeader(http.StatusInternalServerError)
	})
	inspector = readInspector(t, unavailable, unavailableURL, seedInspectorEvent(t, unavailableRepo, "outage"))
	if inspector.CaptureState != "unknown" || inspector.CaptureReason != "cpa_unavailable" {
		t.Fatalf("outage = %+v", inspector)
	}
}

func TestUsageEventInspectorReadsPastLargeSectionsAndRedacts(t *testing.T) {
	secret := "sk-" + strings.Repeat("c", 24)
	client, baseURL, repo := startDashboardTestServer(t, func(writer http.ResponseWriter, request *http.Request) {
		if !strings.HasPrefix(request.URL.Path, inspectorLogPath) {
			writer.WriteHeader(http.StatusNotFound)
			return
		}
		_, _ = writer.Write([]byte("=== REQUEST INFO ===\nRequest ID: large\n\n=== REQUEST BODY ===\n"))
		chunk := []byte(strings.Repeat("conversation ", 1024))
		for written := 0; written < 17*1024*1024; written += len(chunk) {
			_, _ = writer.Write(chunk)
		}
		_, _ = writer.Write([]byte("\n\n=== API REQUEST 1 ===\nBody:\n{\"api_key\":\"" + secret[:10]))
		_, _ = writer.Write([]byte(secret[10:] + "\"}\n\n=== API RESPONSE 1 ===\nStatus: 200\n\nBody:\nfinal answer\n"))
	})
	inspector := readInspector(t, client, baseURL, seedInspectorEvent(t, repo, "large"))
	if inspector.Preview.BytesRead < 17*1024*1024 || !inspector.Preview.EOF || inspector.ParseState != "recognized" {
		t.Fatalf("preview = %+v", inspector.Preview)
	}
	if len(inspector.Sections) != 4 || !inspector.Sections[1].Truncated || inspector.Sections[3].Body != "final answer" {
		t.Fatalf("later attempts were lost: %d sections", len(inspector.Sections))
	}
	if strings.Contains(inspector.Sections[2].Body, secret) {
		t.Fatal("split secret reached the inspector")
	}
}

func TestUsageEventExportIsAnAttachmentAndMarksStreamFailure(t *testing.T) {
	client, baseURL, repo := startDashboardTestServer(t, func(writer http.ResponseWriter, request *http.Request) {
		if !strings.HasPrefix(request.URL.Path, inspectorLogPath) {
			writer.WriteHeader(http.StatusNotFound)
			return
		}
		// A declared length that is never delivered makes the client read fail.
		writer.Header().Set("Content-Length", "100000")
		_, _ = writer.Write([]byte("=== REQUEST INFO ===\nRequest ID: cut\n"))
	})
	response, payload := getJSON(t, client, fmt.Sprintf("%s/omc/api/v1/usage/events/%d/request-log", baseURL, seedInspectorEvent(t, repo, "cut")))
	if response.StatusCode != http.StatusOK || !strings.HasPrefix(response.Header.Get("Content-Disposition"), "attachment;") || response.Header.Get("X-Content-Type-Options") != "nosniff" {
		t.Fatalf("export headers = %d %v", response.StatusCode, response.Header)
	}
	if !strings.Contains(string(payload), "Request ID: cut") || !strings.HasSuffix(string(payload), exportIncompleteMarker) {
		t.Fatalf("export = %q", payload)
	}
}

func TestUsageEventExportMapsMissingLog(t *testing.T) {
	client, baseURL, repo := startDashboardTestServer(t, func(writer http.ResponseWriter, request *http.Request) {
		writer.WriteHeader(http.StatusNotFound)
	})
	response, payload := getJSON(t, client, fmt.Sprintf("%s/omc/api/v1/usage/events/%d/request-log", baseURL, seedInspectorEvent(t, repo, "absent")))
	if response.StatusCode != http.StatusNotFound || !strings.Contains(string(payload), `"code":"request_log_not_found"`) {
		t.Fatalf("export = %d %s", response.StatusCode, payload)
	}
}

func TestUsageEventInspectorReportsCapturePending(t *testing.T) {
	client, baseURL, repo := startDashboardTestServer(t, func(writer http.ResponseWriter, request *http.Request) {
		writer.Header().Set("Content-Type", "application/json")
		writer.WriteHeader(http.StatusConflict)
		_, _ = writer.Write([]byte(`{"error":"capture_pending"}`))
	})
	id := seedInspectorEvent(t, repo, "in-flight")
	inspector := readInspector(t, client, baseURL, id)
	if inspector.CaptureState != "pending" || inspector.CaptureReason != "capture_pending" || len(inspector.Sections) != 0 || inspector.Preview.BytesRead != 0 {
		t.Fatalf("pending capture = %+v", inspector)
	}
	for _, path := range []string{"request-log"} {
		response, payload := getJSON(t, client, fmt.Sprintf("%s/omc/api/v1/usage/events/%d/%s", baseURL, id, path))
		if response.StatusCode != http.StatusConflict || !strings.Contains(string(payload), `"code":"capture_pending"`) || response.Header.Get("Content-Disposition") != "" {
			t.Fatalf("%s = %d %v %s", path, response.StatusCode, response.Header, payload)
		}
	}
}

func TestStreamRedactedRequestLogDoesNotMarkCompleteStreams(t *testing.T) {
	var out strings.Builder
	if err := streamRedactedRequestLog(&out, strings.NewReader("complete log\n")); err != nil || out.String() != "complete log\n" {
		t.Fatalf("out = %q err = %v", out.String(), err)
	}
	out.Reset()
	failure := errors.New("reset")
	if err := streamRedactedRequestLog(&out, &failingLogReader{err: failure}); !errors.Is(err, failure) || out.String() != exportIncompleteMarker {
		t.Fatalf("out = %q err = %v", out.String(), err)
	}
}

func TestInspectRequestLogScanLimitInsideACredentialLeaksNoPrefix(t *testing.T) {
	secret := "sk-" + strings.Repeat("q", 40)
	header := "=== REQUEST INFO ===\nRequest ID: cut\n\n=== API REQUEST 1 ===\nHeaders:\nAuthorization: Bearer "
	body := header + secret + "\n\n=== API RESPONSE 1 ===\nStatus: 200\n"
	for _, cut := range []int{len(header) + 3, len(header) + 12, len(header) + len(secret) - 1} {
		limits := requestlog.DefaultLimits()
		limits.ScanBytes = int64(cut)
		response := newRequestInspectorResponse("cut", limits)
		inspectRequestLog(&response, "cut", strings.NewReader(body), limits)
		if response.Preview.EOF || !response.Preview.Truncated || response.Preview.BytesRead != int64(cut) {
			t.Fatalf("cut %d preview = %+v", cut, response.Preview)
		}
		encoded, err := json.Marshal(response)
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(string(encoded), "qqqq") {
			t.Fatalf("cut %d leaked a credential prefix: %s", cut, encoded)
		}
	}
}

type failingLogReader struct{ err error }

func (r *failingLogReader) Read([]byte) (int, error) { return 0, r.err }
