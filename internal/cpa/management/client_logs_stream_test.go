package management

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"
)

const logStreamWait = 150 * time.Millisecond

// logStreamServer serves one request log through serve and releases any
// handler still blocked when the test ends.
func logStreamServer(t *testing.T, serve func(http.ResponseWriter, *http.Request, <-chan struct{})) *Client {
	t.Helper()
	release := make(chan struct{})
	server := newV8Server(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		serve(writer, request, release)
	}))
	t.Cleanup(server.Close)
	t.Cleanup(func() { close(release) })
	client, err := NewClient(server.URL, "management-secret", logStreamWait, false)
	if err != nil {
		t.Fatal(err)
	}
	return client
}

func flushLogPart(writer http.ResponseWriter, part string) {
	_, _ = io.WriteString(writer, part)
	writer.(http.Flusher).Flush()
}

// A log that keeps arriving is read whole, however long it takes in total.
func TestOpenRequestLogReadsASlowSteadyLogPastTheTimeout(t *testing.T) {
	const parts = 8
	client := logStreamServer(t, func(writer http.ResponseWriter, _ *http.Request, _ <-chan struct{}) {
		for index := 0; index < parts; index++ {
			flushLogPart(writer, "part\n")
			time.Sleep(logStreamWait / 3)
		}
	})
	started := time.Now()
	body, err := client.OpenRequestLog(context.Background(), "steady01")
	if err != nil {
		t.Fatal(err)
	}
	defer body.Close()
	data, err := io.ReadAll(body)
	if err != nil {
		t.Fatalf("steady log read failed after %s: %v", time.Since(started), err)
	}
	if got := strings.Count(string(data), "part\n"); got != parts {
		t.Fatalf("read %d parts, want %d", got, parts)
	}
	if elapsed := time.Since(started); elapsed <= logStreamWait {
		t.Fatalf("the read took %s, so it did not outlast the %s wait", elapsed, logStreamWait)
	}
}

// A log that stops arriving ends with ErrLogStreamStalled after the bytes read.
func TestOpenRequestLogEndsAStalledLog(t *testing.T) {
	client := logStreamServer(t, func(writer http.ResponseWriter, request *http.Request, release <-chan struct{}) {
		flushLogPart(writer, "first part\n")
		select {
		case <-release:
		case <-request.Context().Done():
		}
	})
	body, err := client.OpenRequestLog(context.Background(), "stalled1")
	if err != nil {
		t.Fatal(err)
	}
	defer body.Close()
	data, err := io.ReadAll(body)
	if !errors.Is(err, ErrLogStreamStalled) {
		t.Fatalf("stalled read error = %v, want ErrLogStreamStalled", err)
	}
	if string(data) != "first part\n" {
		t.Fatalf("stalled read kept %q", data)
	}
}

// A gateway that never answers is given up after one wait.
func TestOpenRequestLogEndsWhenTheGatewayNeverAnswers(t *testing.T) {
	client := logStreamServer(t, func(_ http.ResponseWriter, request *http.Request, release <-chan struct{}) {
		select {
		case <-release:
		case <-request.Context().Done():
		}
	})
	body, err := client.OpenRequestLog(context.Background(), "silent01")
	if err == nil {
		body.Close()
		t.Fatal("a gateway that never answered opened a log")
	}
	if !errors.Is(err, ErrLogStreamStalled) {
		t.Fatalf("unanswered open error = %v, want ErrLogStreamStalled", err)
	}
}

// Cancelling the caller's context ends the read and is not reported as a stall.
func TestOpenRequestLogStopsWhenTheCallerCancels(t *testing.T) {
	client := logStreamServer(t, func(writer http.ResponseWriter, request *http.Request, release <-chan struct{}) {
		flushLogPart(writer, "first part\n")
		select {
		case <-release:
		case <-request.Context().Done():
		}
	})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	body, err := client.OpenRequestLog(ctx, "cancel01")
	if err != nil {
		t.Fatal(err)
	}
	defer body.Close()
	buffer := make([]byte, 64)
	if _, err := body.Read(buffer); err != nil {
		t.Fatal(err)
	}
	cancel()
	_, err = io.ReadAll(body)
	if err == nil || errors.Is(err, ErrLogStreamStalled) {
		t.Fatalf("cancelled read error = %v, want a cancellation", err)
	}
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled read error = %v, want context.Canceled", err)
	}
}

// A refusal is answered as the gateway's error, not as a stall.
func TestOpenRequestLogKeepsTheGatewayRefusal(t *testing.T) {
	client := logStreamServer(t, func(writer http.ResponseWriter, _ *http.Request, _ <-chan struct{}) {
		writer.WriteHeader(http.StatusConflict)
		_, _ = io.WriteString(writer, `{"error":"capture_pending"}`)
	})
	_, err := client.OpenRequestLog(context.Background(), "pending1")
	var httpErr *HTTPError
	if !errors.As(err, &httpErr) || httpErr.StatusCode != http.StatusConflict {
		t.Fatalf("refused open error = %v, want the gateway's 409", err)
	}
}
