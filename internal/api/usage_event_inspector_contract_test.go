package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"reflect"
	"testing"
)

// The inspector wire corpus is consumed by the typed client test and the browser
// fixtures. This test pins every case against the real handler, so a mock can
// never become the definition of what the server sends.
func TestRequestInspectorSharedWireContract(t *testing.T) {
	payload, err := os.ReadFile("../../scripts/acceptance/contracts/request-inspector.json")
	if err != nil {
		t.Fatal(err)
	}
	var contract struct {
		Version int `json:"version"`
		Cases   []struct {
			ID        string `json:"id"`
			RequestID string `json:"request_id"`
			Relay     struct {
				Status  int    `json:"status"`
				Fixture string `json:"fixture"`
				Body    string `json:"body"`
			} `json:"relay"`
			Status   int             `json:"status"`
			Response json.RawMessage `json:"response"`
		} `json:"cases"`
	}
	if err := json.Unmarshal(payload, &contract); err != nil {
		t.Fatal(err)
	}
	if contract.Version != 1 || len(contract.Cases) == 0 {
		t.Fatal("unsupported or empty request inspector contract")
	}
	seen := map[string]bool{}
	for _, entry := range contract.Cases {
		if entry.ID == "" || seen[entry.ID] {
			t.Fatalf("invalid contract case identity %q", entry.ID)
		}
		seen[entry.ID] = true
		t.Run(entry.ID, func(t *testing.T) {
			relayBody := []byte(entry.Relay.Body)
			if entry.Relay.Fixture != "" {
				relayBody, err = os.ReadFile("../requestlog/testdata/" + entry.Relay.Fixture)
				if err != nil {
					t.Fatal(err)
				}
			}
			client, baseURL, repo := startDashboardTestServer(t, func(writer http.ResponseWriter, request *http.Request) {
				writer.WriteHeader(entry.Relay.Status)
				_, _ = writer.Write(relayBody)
			})
			id := seedInspectorEvent(t, repo, entry.RequestID)
			response, actual := getJSON(t, client, fmt.Sprintf("%s/omc/api/v1/usage/events/%d/inspector", baseURL, id))
			if response.StatusCode != entry.Status {
				t.Fatalf("status=%d expected=%d body=%s", response.StatusCode, entry.Status, actual)
			}
			var actualValue, expectedValue any
			if json.Unmarshal(actual, &actualValue) != nil || json.Unmarshal(entry.Response, &expectedValue) != nil || !reflect.DeepEqual(actualValue, expectedValue) {
				t.Fatalf("wire response drift: actual=%s expected=%s", actual, entry.Response)
			}
		})
	}
}
