package management

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync/atomic"
	"time"
)

// OpenRequestLog opens one CPA request log by request id without a size cap.
//
// The id is validated here rather than by callers: CPA resolves it to a filename
// inside its log directory, so a traversal or empty value must never reach it.
// The caller closes the body and must redact it before it reaches a browser.
func (c *Client) OpenRequestLog(ctx context.Context, requestID string) (io.ReadCloser, error) {
	requestID = strings.TrimSpace(requestID)
	if requestID == "" || strings.ContainsAny(requestID, "/\\") || strings.Contains(requestID, "..") {
		return nil, errors.New("invalid request id")
	}
	return c.openLog(ctx, "/observability/logs/requests/"+url.PathEscape(requestID))
}

// OpenRequestErrorLog opens one named error log file without a size cap.
//
// The name is validated here rather than by callers: CPA resolves it inside its
// log directory, so an empty value, a traversal or a path separator must never
// reach it. The caller must redact the body before it reaches a browser.
func (c *Client) OpenRequestErrorLog(ctx context.Context, name string) (io.ReadCloser, error) {
	if !ValidLogFileName(name) {
		return nil, errors.New("invalid log file name")
	}
	return c.openLog(ctx, "/observability/logs/errors/"+url.PathEscape(name))
}

// openLog opens one log file as a stream.
//
// A log may be large or written slowly, so the read has no total deadline.
// The gateway must answer within the client's timeout and then deliver each
// next part within that same wait; a stalled read ends with ErrLogStreamStalled.
// Cancelling ctx ends the read at any time.
func (c *Client) openLog(ctx context.Context, path string) (io.ReadCloser, error) {
	if err := c.requireManagementV8(ctx); err != nil {
		return nil, err
	}
	ctx, cancel := context.WithCancel(ctx)
	watch := newStallWatch(c.waitTimeout, cancel)
	request, err := c.newRequest(ctx, http.MethodGet, path, nil, "")
	if err != nil {
		watch.stop()
		cancel()
		return nil, err
	}
	watch.arm()
	response, _, err := c.sendThrough(c.streamClient, request)
	watch.disarm()
	if err != nil {
		watch.stop()
		cancel()
		if watch.fired() {
			return nil, fmt.Errorf("%w: no answer within %s", ErrLogStreamStalled, c.waitTimeout)
		}
		return nil, err
	}
	return &stallBoundBody{body: response.Body, watch: watch, cancel: cancel}, nil
}

// ErrLogStreamStalled reports a log read whose gateway stopped sending.
var ErrLogStreamStalled = errors.New("CPA log stream stalled")

// stallWatch cancels a read when one wait lasts longer than its limit.
type stallWatch struct {
	limit   time.Duration
	timer   *time.Timer
	expired atomic.Bool
}

func newStallWatch(limit time.Duration, cancel context.CancelFunc) *stallWatch {
	watch := &stallWatch{limit: limit}
	watch.timer = time.AfterFunc(time.Hour, func() {
		watch.expired.Store(true)
		cancel()
	})
	watch.timer.Stop()
	return watch
}

func (w *stallWatch) arm()        { w.timer.Reset(w.limit) }
func (w *stallWatch) disarm()     { w.timer.Stop() }
func (w *stallWatch) stop()       { w.timer.Stop() }
func (w *stallWatch) fired() bool { return w.expired.Load() }

// stallBoundBody is a log body whose every read must make progress within the
// watch's limit.
type stallBoundBody struct {
	body   io.ReadCloser
	watch  *stallWatch
	cancel context.CancelFunc
}

func (b *stallBoundBody) Read(buffer []byte) (int, error) {
	b.watch.arm()
	read, err := b.body.Read(buffer)
	b.watch.disarm()
	if err != nil && !errors.Is(err, io.EOF) && b.watch.fired() {
		return read, fmt.Errorf("%w: nothing arrived for %s", ErrLogStreamStalled, b.watch.limit)
	}
	return read, err
}

func (b *stallBoundBody) Close() error {
	b.watch.stop()
	b.cancel()
	return b.body.Close()
}

// DefaultLogsLimit and MaxLogsLimit bound one incremental log read.
//
// CPAMC asks for its whole 10k-line buffer on every poll. That is a
// multi-megabyte first response for a page whose whole value is "the recent
// tail", so the default page is smaller; the cap stays at 10k for a deliberate
// widening of the searchable window.
const (
	DefaultLogsLimit = 2000
	MaxLogsLimit     = 10000
)

// LogsQuery narrows one read of CPA's file log.
//
// CPA has two generations of this endpoint: an opaque server-side cursor, and
// the older `after` measured in unix seconds. The cursor wins when the server
// offers one. With `after`, the boundary is re-sent one second back, because a
// second-granularity cut drops whatever else shares that timestamp — a
// duplicated line is recoverable at render time, a missing one is not.
type LogsQuery struct {
	Cursor string
	After  int64
	Limit  int
}

// LogsPage is one normalised incremental read of the log tail.
type LogsPage struct {
	Lines       []string
	LatestAfter int64
	NextCursor  string
	CursorReset bool
}

// Logs reads the tail of CPA's file log.
func (c *Client) Logs(ctx context.Context, query LogsQuery) (LogsPage, ResponseMeta, error) {
	limit := query.Limit
	if limit <= 0 {
		limit = DefaultLogsLimit
	}
	if limit > MaxLogsLimit {
		limit = MaxLogsLimit
	}
	params := url.Values{}
	params.Set("limit", strconv.Itoa(limit))
	if cursor := strings.TrimSpace(query.Cursor); cursor != "" {
		params.Set("cursor", cursor)
	} else if query.After > 1 {
		params.Set("after", strconv.FormatInt(query.After-1, 10))
	}

	// The two positional fields are typed loosely on purpose: CPA builds vary
	// (numbers vs strings, absent vs false), and a rejected decode would blank
	// the whole log page over a cosmetic difference.
	var response struct {
		Lines       []string `json:"lines"`
		LatestAfter any      `json:"latest-timestamp"`
		NextCursor  string   `json:"next-cursor"`
		CursorReset any      `json:"cursor-reset"`
	}
	meta, err := c.DoJSONWithMeta(ctx, http.MethodGet, "/observability/logs?"+params.Encode(), &response)
	if err != nil {
		return LogsPage{Lines: []string{}}, meta, err
	}
	page := LogsPage{
		Lines:       make([]string, 0, len(response.Lines)),
		LatestAfter: unixSecondsValue(response.LatestAfter),
		NextCursor:  strings.TrimSpace(response.NextCursor),
		CursorReset: boolValue(response.CursorReset),
	}
	for _, line := range response.Lines {
		if trimmed := strings.TrimRight(line, "\r\n"); trimmed != "" {
			page.Lines = append(page.Lines, trimmed)
		}
	}
	return page, meta, nil
}

// ClearLogs truncates CPA's log file. Destructive, and never reached by a
// poll: only an explicit operator action gets here.
func (c *Client) ClearLogs(ctx context.Context) (ResponseMeta, error) {
	return c.DoJSONWithMeta(ctx, http.MethodDelete, "/observability/logs", nil)
}

// ErrorLogFile is one request-error log file held by CPA.
type ErrorLogFile struct {
	Name     string `json:"name"`
	Size     int64  `json:"size"`
	Modified int64  `json:"modified"`
}

// RequestErrorLogs lists the downloadable request-error log files.
func (c *Client) RequestErrorLogs(ctx context.Context) ([]ErrorLogFile, error) {
	var response struct {
		Files []struct {
			Name     string `json:"name"`
			Size     any    `json:"size"`
			Modified any    `json:"modified"`
		} `json:"files"`
	}
	if err := c.DoJSON(ctx, http.MethodGet, "/observability/logs/errors", &response); err != nil {
		return nil, err
	}
	files := make([]ErrorLogFile, 0, len(response.Files))
	for _, file := range response.Files {
		name := strings.TrimSpace(file.Name)
		if name == "" {
			continue
		}
		files = append(files, ErrorLogFile{
			Name:     name,
			Size:     int64Value(file.Size),
			Modified: unixSecondsValue(file.Modified),
		})
	}
	return files, nil
}

// ValidLogFileName accepts a bare log filename and nothing else.
func ValidLogFileName(name string) bool {
	name = strings.TrimSpace(name)
	if name == "" || len(name) > 128 || strings.ContainsAny(name, "/\\") || strings.Contains(name, "..") {
		return false
	}
	return !strings.HasPrefix(name, ".")
}
