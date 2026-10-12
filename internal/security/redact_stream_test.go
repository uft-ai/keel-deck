package security

import (
	"encoding/json"
	"strings"
	"testing"
)

func streamRedaction(t *testing.T, payload string, chunk int) string {
	t.Helper()
	stream := NewRedactionStream()
	var result strings.Builder
	for len(payload) > 0 {
		n := chunk
		if n > len(payload) {
			n = len(payload)
		}
		result.WriteString(stream.Write(payload[:n]))
		payload = payload[n:]
		if len(stream.tail) > redactionRetainLimit || len(stream.url) > redactionRetainLimit {
			t.Fatalf("unbounded retained state: %d/%d", len(stream.tail), len(stream.url))
		}
	}
	result.WriteString(stream.Close())
	if stream.Close() != "" || stream.Write("late") != "" {
		t.Fatal("closed redactor emitted more text")
	}
	return result.String()
}

func TestRedactionStreamChunkingDoesNotLeakOrLoseContext(t *testing.T) {
	secret := "CREDENTIAL_abcdefghij0123456789"
	samples := []string{
		"before\nAuthorization: Bearer " + secret + "\nafter",
		"before\nProxy-Authorization: Basic " + secret + "\nafter",
		"before\nCookie: session=" + secret + "; other=" + secret + "\nafter",
		"before Bearer " + secret + " after",
		"before Authorization: Bearer\t" + secret + " after",
		"before Proxy-Authorization: Basic\t" + secret + " after",
		"before api_key=" + secret + " after",
		`{"api_key" : "` + secret + `","conversation":"before and after","max_tokens":32}`,
		`{"password":"` + secret + ` with spaces and an escaped \" quote","tool_result":"before and after"}`,
		`{"secret":{"nested":["` + secret + `"]},"conversation":"before and after"}`,
		"before https://user:" + secret + "@example.com/path?api_key=" + secret + "&page=2 after",
	}
	for _, prefix := range []string{"sk-", "pk-", "pat-", "glpat-", "ghp_", "gho_", "ghu_", "ghs_", "ghr_", "github_pat_", "xoxb-", "xoxp-", "xoxa-", "xoxr-", "xoxs-"} {
		samples = append(samples, "before "+prefix+secret+" after")
	}
	for _, payload := range samples {
		want := streamRedaction(t, payload, len(payload))
		for _, chunk := range []int{1, 2, 3, 7, 17, 31, 32, 33, 64, 127, 4096} {
			got := streamRedaction(t, payload, chunk)
			if got != want {
				t.Fatalf("chunk %d changes output for %q: want %q got %q", chunk, payload, want, got)
			}
			if strings.Contains(got, secret) || !strings.Contains(got, "before") || !strings.Contains(got, "after") {
				t.Fatalf("leaked credential or lost context: %q", got)
			}
		}
		if json.Valid([]byte(payload)) && !json.Valid([]byte(want)) {
			t.Fatalf("redaction broke JSON: %s", want)
		}
	}
}

func TestRedactionStreamDiscardsUnboundedCredentialValues(t *testing.T) {
	secret := strings.Repeat("SECRET0123456789", 10000)
	samples := []string{
		"before\nAuthorization: Bearer " + secret + "\nafter",
		"before\nCookie: session=" + secret + "; more=" + secret + "\nafter",
		`{"api_key":"` + secret + `","conversation":"before and after"}`,
		"before github_pat_" + secret + " after",
	}
	for _, payload := range samples {
		for _, chunk := range []int{17, 4096, 64 * 1024} {
			got := streamRedaction(t, payload, chunk)
			if strings.Contains(got, "SECRET0123456789") || !strings.Contains(got, "before") || !strings.Contains(got, "after") {
				t.Fatalf("long credential leaked or lost context, output bytes=%d", len(got))
			}
		}
	}
}

func TestRedactionStreamKeepsOrdinaryConversationAndLongWhitespace(t *testing.T) {
	payload := `{"max_tokens":32,"conversation":"` + strings.Repeat("safe tokenization and tools ", 10000) + `","tool_result":{"accounting":42}}`
	if got := streamRedaction(t, payload, 127); got != payload {
		t.Fatal("ordinary conversation or tool values changed")
	}
	secret := "CREDENTIAL_abcdefghij0123456789"
	payload = "before api_key" + strings.Repeat(" ", 20000) + "=" + strings.Repeat(" ", 20000) + secret + " after"
	got := streamRedaction(t, payload, 17)
	if strings.Contains(got, secret) || !strings.Contains(got, "before") || !strings.Contains(got, "after") {
		t.Fatal("whitespace split bypassed credential redaction")
	}
}

func TestRedactionStreamBoundsLongURLs(t *testing.T) {
	payload := "before https://user:" + strings.Repeat("PRIVATE", 10000) + "@example.com/path after"
	got := streamRedaction(t, payload, 127)
	if strings.Contains(got, "PRIVATE") || !strings.Contains(got, "URL omitted") || !strings.Contains(got, "after") {
		t.Fatal("oversized URL was not safely omitted")
	}
}
