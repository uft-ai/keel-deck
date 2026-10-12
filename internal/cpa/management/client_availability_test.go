package management

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"
)

func TestPeriodExhaustionUsesTheReducedSchema(t *testing.T) {
	var seenMethod, seenPath, seenQuery, seenBody string
	server := newV8Server(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		seenMethod = request.Method
		seenPath = request.URL.Path
		seenQuery = request.URL.RawQuery
		body, _ := io.ReadAll(request.Body)
		seenBody = string(body)
		writer.Header().Set("Content-Type", "application/json")
		if request.Method == http.MethodGet {
			_, _ = writer.Write([]byte(`{"record":null,"observed_at":"2026-10-12T01:00:00Z"}`))
			return
		}
		_, _ = writer.Write([]byte(`{"record":{"auth_index":"1111111111111111","model":"qwen3.8-flash","blocked":true,"version":1,"period_end":null,"exhausted_at":"2026-10-12T01:02:00Z","account_period_start":null,"updated_at":"2026-10-12T01:02:00Z","reset_unknown":true}}`))
	}))
	defer server.Close()
	client, err := NewClient(server.URL, "management-secret", time.Second, false)
	if err != nil {
		t.Fatal(err)
	}

	read, err := client.GetPeriodExhaustion(context.Background(), "1111111111111111", "qwen3.8-flash")
	if err != nil {
		t.Fatal(err)
	}
	if seenMethod != http.MethodGet || seenPath != "/v8/management/availability/period-exhaustion" {
		t.Fatalf("GET = %s %s", seenMethod, seenPath)
	}
	if !strings.Contains(seenQuery, "auth_index=1111111111111111") || !strings.Contains(seenQuery, "model=qwen3.8-flash") {
		t.Fatalf("query = %s", seenQuery)
	}
	if read.Record != nil || read.ObservedAt.Format(time.RFC3339) != "2026-10-12T01:00:00Z" {
		t.Fatalf("read = %+v", read)
	}

	remaining := int64(0)
	record, err := client.PutPeriodExhaustion(context.Background(), PeriodObservation{
		ObservationID:  "deck-1",
		BasedOnVersion: 0,
		State:          "exhausted",
		AuthIndex:      "1111111111111111",
		Model:          "qwen3.8-flash",
		Remaining:      &remaining,
		NotBefore:      read.ObservedAt,
	})
	if err != nil {
		t.Fatal(err)
	}
	if seenMethod != http.MethodPut {
		t.Fatalf("method = %s", seenMethod)
	}
	var body map[string]json.RawMessage
	if err := json.Unmarshal([]byte(seenBody), &body); err != nil {
		t.Fatal(err)
	}
	for _, retired := range []string{"source", "reset_at", "blocked_period_end", "observed_at"} {
		if _, present := body[retired]; present {
			t.Fatalf("retired field %s in %s", retired, seenBody)
		}
	}
	if string(body["based_on_version"]) != "0" || string(body["pool_id"]) != `""` || string(body["period_end"]) != "null" {
		t.Fatalf("body = %s", seenBody)
	}
	if !record.Blocked || record.Version != 1 || !record.ResetUnknown {
		t.Fatalf("record = %+v", record)
	}
}
