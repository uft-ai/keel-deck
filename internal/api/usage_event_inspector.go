package api

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"math"
	"net/http"
	"strings"

	"github.com/oh-my-cpa/oh-my-cpa/internal/cpa/management"
	"github.com/oh-my-cpa/oh-my-cpa/internal/repository"
	"github.com/oh-my-cpa/oh-my-cpa/internal/requestlog"
	"github.com/oh-my-cpa/oh-my-cpa/internal/security"
)

// exportIncompleteMarker ends an export whose source stream failed after bytes
// were already sent, so a partial file never looks complete.
const exportIncompleteMarker = "\n[EXPORT INCOMPLETE: the request log stream failed before its end]\n"

type inspectorIdentity struct {
	RequestID       string `json:"request_id"`
	LoggedRequestID string `json:"logged_request_id,omitempty"`
	Match           string `json:"match"`
}

type inspectorLimits struct {
	ScanBytes    int64 `json:"scan_bytes"`
	SectionBytes int   `json:"section_bytes"`
	MaxSections  int   `json:"max_sections"`
}

type inspectorPreview struct {
	BytesRead       int64 `json:"bytes_read"`
	EOF             bool  `json:"eof"`
	Truncated       bool  `json:"truncated"`
	SectionsOmitted int   `json:"sections_omitted"`
	ReadError       bool  `json:"read_error,omitempty"`
}

// requestInspectorResponse always carries every field, whatever the capture state.
type requestInspectorResponse struct {
	CaptureState string `json:"capture_state"`
	// CaptureReason is a stable code explaining a non-retained state.
	CaptureReason string               `json:"capture_reason,omitempty"`
	ParseState    string               `json:"parse_state"`
	Boundaries    string               `json:"boundaries"`
	Identity      inspectorIdentity    `json:"identity"`
	Limits        inspectorLimits      `json:"limits"`
	Preview       inspectorPreview     `json:"preview"`
	Sections      []requestlog.Section `json:"sections"`
}

func newRequestInspectorResponse(requestID string, limits requestlog.Limits) requestInspectorResponse {
	return requestInspectorResponse{
		CaptureState: "unknown",
		ParseState:   "unrecognized",
		// Relay's plain-text format does not escape payload lines that look like
		// section markers; boundaries are inferred from Relay's write order.
		Boundaries: "heuristic",
		Identity:   inspectorIdentity{RequestID: requestID, Match: "unknown"},
		Limits:     inspectorLimits{ScanBytes: limits.ScanBytes, SectionBytes: limits.SectionBytes, MaxSections: limits.MaxSections},
		Sections:   []requestlog.Section{},
	}
}

func (h *Handler) inspectUsageEventRequestLog(writer http.ResponseWriter, request *http.Request) {
	writer.Header().Set("Cache-Control", "no-store")
	row, ok := h.usageEventForRequestLog(writer, request)
	if !ok {
		return
	}
	limits := requestlog.DefaultLimits()
	response := newRequestInspectorResponse(row.RequestID, limits)
	if strings.TrimSpace(row.RequestID) == "" {
		response.CaptureState = "missing"
		response.CaptureReason = "no_request_id"
		writeJSON(writer, http.StatusOK, response)
		return
	}
	if auditErr := h.recordAudit(request, "request_log.inspect", "request_log", row.RequestID, "attempt", nil); auditErr != nil {
		writeError(writer, http.StatusInternalServerError, "audit log failure; request inspection aborted")
		return
	}
	client, ok := h.managementClientOrError(writer, request)
	if !ok {
		return
	}
	ctx, cancel := context.WithTimeout(request.Context(), h.queryTimeout())
	defer cancel()
	body, fetchErr := client.OpenRequestLog(ctx, row.RequestID)
	if fetchErr != nil {
		response.CaptureState, response.CaptureReason = inspectorFetchState(fetchErr)
		_ = h.recordAudit(request, "request_log.inspect", "request_log", row.RequestID, "failure",
			map[string]any{"error": fetchErr.Error(), "capture_reason": response.CaptureReason})
		writeJSON(writer, http.StatusOK, response)
		return
	}
	defer body.Close()
	inspectRequestLog(&response, row.RequestID, body, limits)
	switch {
	case response.Identity.Match == "mismatch":
		_ = h.recordAudit(request, "request_log.inspect", "request_log", row.RequestID, "failure",
			map[string]any{"error": "the gateway returned another request's log", "size_bytes": response.Preview.BytesRead})
	case response.Preview.ReadError:
		_ = h.recordAudit(request, "request_log.inspect", "request_log", row.RequestID, "failure",
			map[string]any{"error": "the gateway log could not be read", "size_bytes": response.Preview.BytesRead})
	default:
		_ = h.recordAudit(request, "request_log.inspect", "request_log", row.RequestID, "success",
			map[string]any{"size_bytes": response.Preview.BytesRead})
	}
	writeJSON(writer, http.StatusOK, response)
}

// inspectorFetchState maps a failed log lookup. Relay answers 404 both for a
// request it never captured and for one its retention already pruned, so the
// absence is reported without claiming which.
func inspectorFetchState(err error) (string, string) {
	var httpErr *management.HTTPError
	if errors.As(err, &httpErr) && httpErr.StatusCode == http.StatusNotFound {
		return "expired_unknown", requestLogNotFoundCode
	}
	if isCapturePending(err) {
		return "pending", requestLogCapturePendingCode
	}
	if errors.Is(err, management.ErrManagementV8Required) {
		return "unknown", "cpa_v8_required"
	}
	if errors.Is(err, management.ErrManagementDisabled) {
		return "unknown", "cpa_management_disabled"
	}
	return "unknown", "cpa_unavailable"
}

// requestLogCapturePendingCode is Relay's answer while the request's log file is
// still being assembled; a read now would return an unfinished capture.
const requestLogCapturePendingCode = "capture_pending"

func isCapturePending(err error) bool {
	var httpErr *management.HTTPError
	if !errors.As(err, &httpErr) || httpErr.StatusCode != http.StatusConflict {
		return false
	}
	var body struct {
		Error string `json:"error"`
	}
	return json.Unmarshal([]byte(httpErr.Body), &body) == nil && body.Error == requestLogCapturePendingCode
}

// writeRequestLogFetchError answers a failed log lookup with the codes the
// console explains: absence, a capture still being written, or a CPA failure.
func writeRequestLogFetchError(writer http.ResponseWriter, err error) {
	var httpErr *management.HTTPError
	switch {
	case errors.As(err, &httpErr) && httpErr.StatusCode == http.StatusNotFound:
		writeJSON(writer, http.StatusNotFound, map[string]string{
			"error": "CPA holds no log file for this request",
			"code":  requestLogNotFoundCode,
		})
	case isCapturePending(err):
		writeJSON(writer, http.StatusConflict, map[string]string{
			"error": "Relay is still writing this request's log; try again when the request has finished",
			"code":  requestLogCapturePendingCode,
		})
	default:
		writeCPAFacadeError(writer, err)
	}
}

// inspectRequestLog redacts the raw stream before parsing, reading at most
// limits.ScanBytes raw bytes.
func inspectRequestLog(response *requestInspectorResponse, requestID string, body io.Reader, limits requestlog.Limits) {
	response.CaptureState = "retained"
	raw := &countedReader{reader: io.LimitReader(body, limits.ScanBytes)}
	redacted := &redactingReader{source: raw, stream: security.NewRedactionStream()}
	parseLimits := limits
	parseLimits.ScanBytes = math.MaxInt64
	result, parseErr := requestlog.Parse(redacted, parseLimits)
	eof := parseErr == nil && (raw.read < limits.ScanBytes || sourceExhausted(body))
	response.Preview = inspectorPreview{
		BytesRead:       raw.read,
		EOF:             eof,
		Truncated:       !eof,
		SectionsOmitted: result.SectionsOmitted,
		ReadError:       parseErr != nil,
	}
	if !eof && len(result.Sections) > 0 {
		result.Sections[len(result.Sections)-1].Truncated = true
	}
	switch {
	case !result.Recognized:
		response.ParseState = "unrecognized"
	case !eof || result.SectionsOmitted > 0:
		response.ParseState = "partial"
	default:
		response.ParseState = "recognized"
	}
	response.Identity.LoggedRequestID = result.RequestID
	switch {
	case result.RequestID == "":
		response.Identity.Match = "unknown"
	case requestIDMatches(result.RequestID, requestID):
		response.Identity.Match = "matched"
	default:
		// Another request's log must not be presented as this event's.
		response.Identity.Match = "mismatch"
		return
	}
	response.Sections = result.Sections
}

func sourceExhausted(reader io.Reader) bool {
	var probe [1]byte
	n, err := io.ReadFull(reader, probe[:])
	return n == 0 && errors.Is(err, io.EOF)
}

type countedReader struct {
	reader io.Reader
	read   int64
}

func (c *countedReader) Read(buffer []byte) (int, error) {
	n, err := c.reader.Read(buffer)
	c.read += int64(n)
	return n, err
}

// redactingReader exposes a security.RedactionStream as an io.Reader.
type redactingReader struct {
	source  io.Reader
	stream  *security.RedactionStream
	pending string
	buffer  []byte
	done    bool
	err     error
}

func (r *redactingReader) Read(out []byte) (int, error) {
	for r.pending == "" {
		if r.done {
			return 0, r.err
		}
		if r.buffer == nil {
			r.buffer = make([]byte, 64*1024)
		}
		n, err := r.source.Read(r.buffer)
		if n > 0 {
			r.pending = r.stream.Write(string(r.buffer[:n]))
		}
		if err != nil {
			r.pending += r.stream.Close()
			r.done = true
			if !errors.Is(err, io.EOF) {
				r.err = err
			} else {
				r.err = io.EOF
			}
		}
	}
	n := copy(out, r.pending)
	r.pending = r.pending[n:]
	return n, nil
}

func (h *Handler) exportUsageEventRequestLog(writer http.ResponseWriter, request *http.Request) {
	writer.Header().Set("Cache-Control", "no-store")
	row, ok := h.usageEventForRequestLog(writer, request)
	if !ok {
		return
	}
	if strings.TrimSpace(row.RequestID) == "" {
		writeJSON(writer, http.StatusNotFound, map[string]string{
			"error": "this record has no request id to look up",
			"code":  requestLogNotFoundCode,
		})
		return
	}
	client, ok := h.managementClientOrError(writer, request)
	if !ok {
		return
	}
	h.serveRedactedGatewayLog(writer, request, gatewayLogRead{
		action:            "request_log.export",
		targetType:        "request_log",
		targetID:          row.RequestID,
		fileName:          sanitizeLogName(row.RequestID),
		abortMessage:      "audit log failure; request export aborted",
		expectedRequestID: row.RequestID,
		open: func(ctx context.Context) (io.ReadCloser, error) {
			return client.OpenRequestLog(ctx, row.RequestID)
		},
		fetchError: writeRequestLogFetchError,
	})
}

func (h *Handler) usageEventForRequestLog(writer http.ResponseWriter, request *http.Request) (repository.UsageEventRow, bool) {
	id, err := parseEventID(request)
	if err != nil {
		writeError(writer, http.StatusBadRequest, err.Error())
		return repository.UsageEventRow{}, false
	}
	row, err := h.repo.GetUsageEvent(request.Context(), id)
	if errors.Is(err, repository.ErrNotFound) {
		writeError(writer, http.StatusNotFound, "usage event not found")
		return repository.UsageEventRow{}, false
	}
	if err != nil {
		writeInternalError(writer, err)
		return repository.UsageEventRow{}, false
	}
	return row, true
}

// streamRedactedRequestLog copies body through the redactor. Once bytes have
// been sent the status cannot change, so a source failure appends
// exportIncompleteMarker and is returned for auditing; a client disconnect is
// returned without a marker.
func streamRedactedRequestLog(writer io.Writer, body io.Reader) error {
	stream := security.NewRedactionStream()
	buffer := make([]byte, 64*1024)
	for {
		read, readErr := body.Read(buffer)
		if read > 0 {
			if _, writeErr := io.WriteString(writer, stream.Write(string(buffer[:read]))); writeErr != nil {
				return writeErr
			}
		}
		if readErr == nil {
			continue
		}
		_, writeErr := io.WriteString(writer, stream.Close())
		if errors.Is(readErr, io.EOF) {
			return writeErr
		}
		_, _ = io.WriteString(writer, exportIncompleteMarker)
		return readErr
	}
}
