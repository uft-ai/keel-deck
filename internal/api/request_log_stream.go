package api

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// requestLogIdentityScanBytes bounds the read that finds the logged request id.
// Relay writes it in the first section, as its own lookup assumes.
const requestLogIdentityScanBytes = 4096

// gatewayLogRead is one browser read of a log file held by the gateway.
type gatewayLogRead struct {
	action       string
	targetType   string
	targetID     string
	fileName     string
	abortMessage string
	// expectedRequestID, when set, refuses a file whose logged request id names
	// another request, by the rule in requestIDMatches.
	expectedRequestID string
	open              func(context.Context) (io.ReadCloser, error)
	fetchError        func(http.ResponseWriter, error)
}

// serveRedactedGatewayLog is the one path by which gateway log text reaches the
// browser. The read is audited before the gateway is asked, fails closed when
// that audit cannot be written, and streams the whole file through the
// redactor with no size cap. A source failure after bytes were sent ends with
// exportIncompleteMarker and an audited failure. A read with ?preview stops
// after gatewayLogPreviewBytes+1 redacted bytes and records whether the file
// was complete.
func (h *Handler) serveRedactedGatewayLog(writer http.ResponseWriter, request *http.Request, read gatewayLogRead) {
	writer.Header().Set("Cache-Control", "no-store")
	if auditErr := h.recordAudit(request, read.action, read.targetType, read.targetID, "attempt", nil); auditErr != nil {
		writeError(writer, http.StatusInternalServerError, read.abortMessage)
		return
	}
	output := newProgressDeadlineWriter(writer, h.logWriteWait())
	body, err := read.open(request.Context())
	// The audit and the gateway's answer may have used up the server's write
	// timeout, so the first write gets a fresh deadline like every later one.
	output.extend()
	if err != nil {
		_ = h.recordAudit(request, read.action, read.targetType, read.targetID, "failure", map[string]any{"error": err.Error()})
		read.fetchError(writer, err)
		return
	}
	defer body.Close()
	source := bufio.NewReaderSize(body, requestLogIdentityScanBytes)
	if read.expectedRequestID != "" {
		logged := loggedRequestID(source)
		if logged != "" && !requestIDMatches(logged, read.expectedRequestID) {
			_ = h.recordAudit(request, read.action, read.targetType, read.targetID, "failure", map[string]any{"error": "the gateway returned another request's log"})
			// Relay answers the same way when it skips another request's file.
			writeJSON(writer, http.StatusNotFound, map[string]string{
				"error": "CPA holds no log file for this request",
				"code":  requestLogNotFoundCode,
			})
			return
		}
	}
	writer.Header().Set("Content-Type", "text/plain; charset=utf-8")
	writer.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename=%q`, read.fileName))
	writer.Header().Set("X-Content-Type-Options", "nosniff")
	if request.Method == http.MethodHead {
		return
	}
	counted := &countedReader{reader: source}
	var sink io.Writer = output
	details := map[string]any{}
	if request.URL.Query().Has("preview") {
		// One byte past the limit tells the viewer the preview is not the whole file.
		sink = &previewWriter{writer: output, left: gatewayLogPreviewBytes + 1}
		details["preview"] = true
	}
	streamErr := streamRedactedRequestLog(sink, counted)
	details["size_bytes"] = counted.read
	switch {
	case errors.Is(streamErr, errPreviewFull):
		details["complete"] = false
	case streamErr != nil:
		details["error"] = streamErr.Error()
		_ = h.recordAudit(request, read.action, read.targetType, read.targetID, "failure", details)
		return
	case details["preview"] == true:
		details["complete"] = true
	}
	_ = h.recordAudit(request, read.action, read.targetType, read.targetID, "success", details)
}

// gatewayLogPreviewBytes is the most redacted text a preview read returns before
// its extra byte; the viewer shows the same amount.
const gatewayLogPreviewBytes = 2 * 1024 * 1024

var errPreviewFull = errors.New("the preview limit was reached")

// previewWriter passes on at most left bytes, then ends the stream with
// errPreviewFull so the rest of the file is not read.
type previewWriter struct {
	writer io.Writer
	left   int
}

func (w *previewWriter) Write(data []byte) (int, error) {
	if len(data) <= w.left {
		written, err := w.writer.Write(data)
		w.left -= written
		return written, err
	}
	written, err := w.writer.Write(data[:w.left])
	w.left -= written
	if err != nil {
		return written, err
	}
	return written, errPreviewFull
}

// gatewayLogWriteWait bounds one write of a log stream to the browser. The
// server's whole-response write timeout would otherwise cut a long download, so
// each write moves the deadline instead: a browser that keeps reading gets the
// whole log, and one that stops reading is dropped.
const gatewayLogWriteWait = 30 * time.Second

// logWriteWait is gatewayLogWriteWait unless a test set a shorter wait.
func (h *Handler) logWriteWait() time.Duration {
	if h.gatewayLogWriteWait > 0 {
		return h.gatewayLogWriteWait
	}
	return gatewayLogWriteWait
}

type progressDeadlineWriter struct {
	writer     http.ResponseWriter
	controller *http.ResponseController
	wait       time.Duration
}

func newProgressDeadlineWriter(writer http.ResponseWriter, wait time.Duration) *progressDeadlineWriter {
	return &progressDeadlineWriter{writer: writer, controller: http.NewResponseController(writer), wait: wait}
}

// extend moves the connection's write deadline wait past now.
func (w *progressDeadlineWriter) extend() {
	_ = w.controller.SetWriteDeadline(time.Now().Add(w.wait))
}

func (w *progressDeadlineWriter) Write(data []byte) (int, error) {
	w.extend()
	return w.writer.Write(data)
}

// loggedRequestID reads the "Request ID:" line from the REQUEST INFO section at
// the start of a Relay request log without consuming it. A log without that
// section or line answers "".
func loggedRequestID(source *bufio.Reader) string {
	head, _ := source.Peek(requestLogIdentityScanBytes)
	lines := strings.Split(string(head), "\n")
	if len(lines) < 2 || strings.TrimRight(lines[0], "\r") != "=== REQUEST INFO ===" {
		return ""
	}
	// The last line may be cut by the scan limit; it is not trusted.
	for _, line := range lines[1 : len(lines)-1] {
		line = strings.TrimRight(line, "\r")
		if line == "" || strings.HasPrefix(line, "===") {
			return ""
		}
		if id, found := strings.CutPrefix(line, "Request ID: "); found {
			return strings.TrimSpace(id)
		}
	}
	return ""
}

// requestIDMatches reports whether a logged request id belongs to the expected
// one. Relay names a log by the last requestLogShortIDLength characters of a
// longer id and uses a shorter id whole, so only an expected id of exactly that
// length may match a longer logged id it ends with.
func requestIDMatches(logged, expected string) bool {
	if logged == expected {
		return true
	}
	return len(expected) == requestLogShortIDLength && len(logged) > requestLogShortIDLength && strings.HasSuffix(logged, expected)
}

// requestLogShortIDLength is the length of Relay's ShortRequestID.
const requestLogShortIDLength = 8
