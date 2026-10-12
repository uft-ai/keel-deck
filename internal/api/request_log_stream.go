package api

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"net/http"
	"strings"
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
// exportIncompleteMarker and an audited failure.
func (h *Handler) serveRedactedGatewayLog(writer http.ResponseWriter, request *http.Request, read gatewayLogRead) {
	writer.Header().Set("Cache-Control", "no-store")
	if auditErr := h.recordAudit(request, read.action, read.targetType, read.targetID, "attempt", nil); auditErr != nil {
		writeError(writer, http.StatusInternalServerError, read.abortMessage)
		return
	}
	body, err := read.open(request.Context())
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
	if streamErr := streamRedactedRequestLog(writer, counted); streamErr != nil {
		_ = h.recordAudit(request, read.action, read.targetType, read.targetID, "failure", map[string]any{"error": streamErr.Error(), "size_bytes": counted.read})
		return
	}
	_ = h.recordAudit(request, read.action, read.targetType, read.targetID, "success", map[string]any{"size_bytes": counted.read})
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
