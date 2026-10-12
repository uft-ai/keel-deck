package requestlog

import (
	"errors"
	"io"
	"os"
	"strings"
	"testing"
)

func parseFixture(t *testing.T, name string, limits Limits) Result {
	t.Helper()
	file, err := os.Open("testdata/" + name)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	result, err := Parse(file, limits)
	if err != nil {
		t.Fatal(err)
	}
	return result
}

func sectionKinds(result Result) []string {
	kinds := make([]string, 0, len(result.Sections))
	for _, section := range result.Sections {
		kinds = append(kinds, section.Name)
	}
	return kinds
}

func TestParseRelayRetryLogKeepsEveryAttempt(t *testing.T) {
	result := parseFixture(t, "relay-chat-retry.log", Limits{})
	if !result.EOF || !result.Recognized || result.RequestID != "01a12e30-d92f-7d85-b6a0-d6e8093d62ba" {
		t.Fatalf("result = %+v", result)
	}
	want := []string{"REQUEST INFO", "HEADERS", "REQUEST BODY", "API REQUEST 1", "API REQUEST 2", "API RESPONSE 1", "API RESPONSE 2", "RESPONSE"}
	if got := sectionKinds(result); strings.Join(got, "|") != strings.Join(want, "|") {
		t.Fatalf("sections = %v", got)
	}
	first, second := result.Sections[5], result.Sections[6]
	if first.Kind != KindAPIResponse || first.Attempt != 1 || first.Status != 429 || !strings.Contains(first.Meta, "Retry-After: 1") || !strings.Contains(first.Body, "rate_limit") {
		t.Fatalf("attempt 1 response = %+v", first)
	}
	if second.Attempt != 2 || second.Status != 200 || !strings.Contains(second.Body, "chatcmpl-1") {
		t.Fatalf("attempt 2 response = %+v", second)
	}
	if request := result.Sections[4]; request.Attempt != 2 || !strings.Contains(request.Body, `"attempt":2`) || strings.Contains(request.Meta, `"attempt":2`) {
		t.Fatalf("attempt 2 request = %+v", request)
	}
	if response := result.Sections[7]; response.Status != 200 || !strings.Contains(response.Meta, "Content-Type") || !strings.HasPrefix(response.Body, `{"choices"`) {
		t.Fatalf("client response = %+v", response)
	}
}

func TestParseRelayErrorAndStreamLogs(t *testing.T) {
	failed := parseFixture(t, "relay-upstream-error.log", Limits{})
	want := []string{"REQUEST INFO", "HEADERS", "REQUEST BODY", "API REQUEST 1", "API ERROR RESPONSE", "API RESPONSE 1", "RESPONSE"}
	if got := sectionKinds(failed); strings.Join(got, "|") != strings.Join(want, "|") {
		t.Fatalf("sections = %v", got)
	}
	if apiErr := failed.Sections[4]; apiErr.Status != 502 || !strings.Contains(apiErr.Body, "upstream unavailable") {
		t.Fatalf("api error = %+v", apiErr)
	}
	if transport := failed.Sections[5]; !strings.Contains(transport.Meta, "connection refused") {
		t.Fatalf("transport failure = %+v", transport)
	}

	stream := parseFixture(t, "relay-responses-stream.log", Limits{})
	upstream := stream.Sections[4]
	if upstream.Kind != KindAPIResponse || !strings.Contains(upstream.Body, "event: response.completed") || upstream.BodyOmitted {
		t.Fatalf("stream response = %+v", upstream)
	}
}

func TestParseRelayBinaryLogKeepsStatusAndHeaders(t *testing.T) {
	result := parseFixture(t, "relay-speech-binary.log", Limits{})
	if !result.EOF || len(result.Sections) != 6 {
		t.Fatalf("sections = %v", sectionKinds(result))
	}
	for _, section := range result.Sections[4:] {
		if !section.BodyOmitted || section.OmissionReason != OmittedBinary || section.Body != "" || section.BodyBytes == 0 {
			t.Fatalf("binary body = %+v", section)
		}
		if section.Status != 200 || !strings.Contains(section.Meta, "audio/wav") {
			t.Fatalf("binary metadata = %+v", section)
		}
	}
	if request := result.Sections[3]; request.BodyOmitted || !strings.Contains(request.Body, "af_heart") {
		t.Fatalf("speech request = %+v", request)
	}
}

func TestParseBoundsEachSectionSoLaterAttemptsStayVisible(t *testing.T) {
	raw := "=== REQUEST INFO ===\nRequest ID: req-1\n\n=== REQUEST BODY ===\n" + strings.Repeat("x", 300000) +
		"\n\n=== API REQUEST 1 ===\nBody:\nupstream one\n\n=== API RESPONSE 1 ===\nStatus: 200\n\nBody:\nanswer\n"
	result, err := Parse(strings.NewReader(raw), Limits{SectionBytes: 1024})
	if err != nil || !result.EOF {
		t.Fatal(err)
	}
	body := result.Sections[1]
	if !body.Truncated || len(body.Body) != 1024 || body.BodyBytes < 300000 {
		t.Fatalf("client body = truncated:%v len:%d bytes:%d", body.Truncated, len(body.Body), body.BodyBytes)
	}
	if len(result.Sections) != 4 || result.Sections[3].Body != "answer" || result.Sections[3].Truncated {
		t.Fatalf("later sections = %+v", result.Sections[2:])
	}
}

func TestParseReportsScanAndSectionLimits(t *testing.T) {
	var raw strings.Builder
	raw.WriteString("=== REQUEST INFO ===\nRequest ID: req-1\n\n")
	for attempt := 1; attempt <= 5; attempt++ {
		raw.WriteString("=== API REQUEST " + string(rune('0'+attempt)) + " ===\nBody:\nbody\n\n")
	}
	result, err := Parse(strings.NewReader(raw.String()), Limits{MaxSections: 3})
	if err != nil || len(result.Sections) != 3 || result.SectionsOmitted != 3 || !result.EOF {
		t.Fatalf("sections=%d omitted=%d eof=%v err=%v", len(result.Sections), result.SectionsOmitted, result.EOF, err)
	}

	limited, err := Parse(strings.NewReader(raw.String()), Limits{ScanBytes: 60})
	if err != nil || limited.EOF || limited.BytesRead != 60 {
		t.Fatalf("limited = %+v err=%v", limited, err)
	}
	if last := limited.Sections[len(limited.Sections)-1]; !last.Truncated {
		t.Fatalf("last section must be marked truncated: %+v", last)
	}

	exact, err := Parse(strings.NewReader("=== RESPONSE ===\nStatus: 200\n"), Limits{ScanBytes: int64(len("=== RESPONSE ===\nStatus: 200\n"))})
	if err != nil || !exact.EOF {
		t.Fatalf("a log of exactly the scan size is complete: %+v", exact)
	}
}

func TestParseKeepsCaptureMarkers(t *testing.T) {
	raw := "=== REQUEST INFO ===\nRequest ID: req-1\n\n=== REQUEST BODY ===\nabc\n[REQUEST BODY TRUNCATED: captured first 3 bytes]\n\n" +
		"=== API REQUEST 1 ===\n<missing>\n\n=== API REQUEST 2 ===\nBody:\n" + strings.Repeat("y", 4096) + "\n[API REQUEST BODY TRUNCATED: captured first 4096 bytes]\n\n" +
		"=== API RESPONSE ===\nStatus: 200\nBody:\n[html body omitted]\n"
	result, err := Parse(strings.NewReader(raw), Limits{SectionBytes: 128})
	if err != nil {
		t.Fatal(err)
	}
	expect := map[int]string{1: "[REQUEST BODY TRUNCATED: captured first 3 bytes]", 2: "<missing>", 3: "[API REQUEST BODY TRUNCATED: captured first 4096 bytes]", 4: "[html body omitted]"}
	for index, marker := range expect {
		section := result.Sections[index]
		if len(section.Markers) == 0 || section.Markers[0] != marker {
			t.Fatalf("section %d markers = %v", index, section.Markers)
		}
	}
	if unnumbered := result.Sections[4]; unnumbered.Kind != KindAPIResponse || unnumbered.Attempt != 0 || unnumbered.Status != 200 {
		t.Fatalf("unnumbered response = %+v", unnumbered)
	}
}

func TestParseTreatsOutOfOrderMarkersAsPayload(t *testing.T) {
	raw := "=== REQUEST INFO ===\nRequest ID: req-1\n\n=== REQUEST BODY ===\nquoted log:\n=== REQUEST INFO ===\n=== HEADERS ===\n\n" +
		"=== API REQUEST 2 ===\nBody:\none\n=== API REQUEST 1 ===\n\n=== RESPONSE ===\nStatus: 200\n\n=== API RESPONSE 1 ===\n"
	result, err := Parse(strings.NewReader(raw), Limits{})
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(sectionKinds(result), "|"); got != "REQUEST INFO|REQUEST BODY|API REQUEST 2|RESPONSE" {
		t.Fatalf("sections = %s", got)
	}
	if !strings.Contains(result.Sections[1].Body, "=== HEADERS ===") || !strings.Contains(result.Sections[2].Body, "=== API REQUEST 1 ===") || !strings.Contains(result.Sections[3].Body, "=== API RESPONSE 1 ===") {
		t.Fatalf("payload marker lines were dropped: %+v", result.Sections)
	}
}

func TestParseUnrecognizedTextAndInvalidUTF8(t *testing.T) {
	result, err := Parse(strings.NewReader("plain text\n"), Limits{})
	if err != nil || result.Recognized || len(result.Sections) != 1 || result.Sections[0].Kind != KindUnrecognized || result.Sections[0].Body != "plain text" {
		t.Fatalf("unrecognized = %+v", result)
	}
	invalid, err := Parse(strings.NewReader("=== RESPONSE ===\nStatus: 200\n\nok \xff\xfe\n"), Limits{})
	if err != nil || !invalid.Sections[0].BodyOmitted || invalid.Sections[0].Status != 200 {
		t.Fatalf("invalid utf-8 = %+v", invalid)
	}
}

func TestParseKeepsMultibyteRunesSplitAcrossReads(t *testing.T) {
	text := strings.Repeat("é", 50000)
	result, err := Parse(io.MultiReader(strings.NewReader("=== REQUEST BODY ===\n"), &oneByteReader{data: []byte(text + "\n")}), Limits{SectionBytes: 1 << 20})
	if err != nil || result.Sections[0].BodyOmitted || result.Sections[0].Body != text {
		t.Fatalf("split runes were not reassembled: omitted=%v len=%d", result.Sections[0].BodyOmitted, len(result.Sections[0].Body))
	}
}

func TestParseReturnsReadErrorWithPartialResult(t *testing.T) {
	failure := errors.New("connection reset")
	result, err := Parse(io.MultiReader(strings.NewReader("=== REQUEST INFO ===\nRequest ID: req-1\n"), &failingReader{err: failure}), Limits{})
	if !errors.Is(err, failure) || result.EOF || result.RequestID != "req-1" || !result.Sections[0].Truncated {
		t.Fatalf("result = %+v err = %v", result, err)
	}
}

type oneByteReader struct{ data []byte }

func (r *oneByteReader) Read(buffer []byte) (int, error) {
	if len(r.data) == 0 {
		return 0, io.EOF
	}
	buffer[0] = r.data[0]
	r.data = r.data[1:]
	return 1, nil
}

type failingReader struct{ err error }

func (r *failingReader) Read([]byte) (int, error) { return 0, r.err }
