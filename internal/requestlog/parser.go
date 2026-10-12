// Package requestlog parses the plain-text request logs Relay retains for one
// request into bounded, display-ready sections.
//
// The format has no escaping: a payload line that is byte-for-byte a section
// marker cannot be told apart from a real one. The parser accepts a marker only
// in Relay's write order (request info, headers, client body, timelines,
// upstream requests, upstream errors, upstream responses, client response), so
// most marker-looking payload lines stay text, but the boundaries remain a
// heuristic and callers must say so.
package requestlog

import (
	"bufio"
	"bytes"
	"errors"
	"io"
	"strconv"
	"strings"
	"unicode/utf8"
)

// Section kinds, in Relay's write order.
const (
	KindUnrecognized         = "unrecognized"
	KindRequestInfo          = "request_info"
	KindHeaders              = "headers"
	KindRequestBody          = "request_body"
	KindWebsocketTimeline    = "websocket_timeline"
	KindAPIWebsocketTimeline = "api_websocket_timeline"
	KindAPIRequest           = "api_request"
	KindAPIErrorResponse     = "api_error_response"
	KindAPIResponse          = "api_response"
	KindResponse             = "response"
)

// OmittedBinary marks a body that was counted but not retained.
const OmittedBinary = "binary"

const (
	readBufferBytes   = 64 * 1024
	maxMarkerLine     = 64
	maxMarkerText     = 160
	maxSectionMarkers = 8
)

// Limits bounds one parse. Each limit that is reached is reported in Result.
type Limits struct {
	// ScanBytes is how much of the log is read before parsing stops.
	ScanBytes int64
	// SectionBytes caps the retained text of each section's metadata and body
	// separately, so one large body cannot hide the sections after it.
	SectionBytes int
	// MaxSections caps how many sections keep their text; later sections are
	// still parsed for boundaries and counted in Result.SectionsOmitted.
	MaxSections int
}

// DefaultLimits are the inspector's bounds.
func DefaultLimits() Limits {
	return Limits{ScanBytes: 64 << 20, SectionBytes: 64 << 10, MaxSections: 64}
}

func (l Limits) normalized() Limits {
	defaults := DefaultLimits()
	if l.ScanBytes <= 0 {
		l.ScanBytes = defaults.ScanBytes
	}
	if l.SectionBytes <= 0 {
		l.SectionBytes = defaults.SectionBytes
	}
	if l.MaxSections <= 0 {
		l.MaxSections = defaults.MaxSections
	}
	return l
}

// Section is one bounded part of a retained log.
type Section struct {
	Kind string `json:"kind"`
	Name string `json:"name"`
	// Attempt is the upstream attempt number; 0 for unnumbered sections.
	Attempt int `json:"attempt,omitempty"`
	// Status is the HTTP status the section records, when it records one.
	Status int    `json:"status,omitempty"`
	Meta   string `json:"meta,omitempty"`
	Body   string `json:"body,omitempty"`
	// BodyBytes counts every body byte read, retained or not.
	BodyBytes int64 `json:"body_bytes"`
	// Truncated means Meta or Body holds less than the section contained.
	Truncated      bool   `json:"truncated"`
	BodyOmitted    bool   `json:"body_omitted,omitempty"`
	OmissionReason string `json:"omission_reason,omitempty"`
	// Markers are Relay's own capture notes (truncation, missing request,
	// omitted bodies) found anywhere in the section, including past the
	// retained text.
	Markers []string `json:"markers,omitempty"`
}

// Result is what one parse could see.
type Result struct {
	// RequestID is the "Request ID:" value from the request info section;
	// empty when the log predates that line.
	RequestID       string
	Sections        []Section
	SectionsOmitted int
	BytesRead       int64
	// EOF reports that the whole log was read within ScanBytes.
	EOF bool
	// Recognized reports that at least one Relay section marker was found.
	Recognized bool
}

// Parse reads at most limits.ScanBytes from reader. A read error stops the parse
// and is returned together with everything parsed before it.
func Parse(reader io.Reader, limits Limits) (Result, error) {
	limits = limits.normalized()
	p := &parser{limits: limits, stage: -1, lastAttempt: map[string]int{}}
	counted := &countingReader{reader: io.LimitReader(reader, limits.ScanBytes)}
	buffered := bufio.NewReaderSize(counted, readBufferBytes)
	lineStart := true
	var readErr error
	for {
		fragment, err := buffered.ReadSlice('\n')
		if len(fragment) > 0 {
			lineEnd := err == nil || errors.Is(err, io.EOF)
			p.feed(fragment, lineStart, lineEnd)
			lineStart = err == nil
		}
		if err == nil || errors.Is(err, bufio.ErrBufferFull) {
			continue
		}
		if !errors.Is(err, io.EOF) {
			readErr = err
		}
		break
	}
	p.result.BytesRead = counted.read
	if readErr == nil {
		p.result.EOF = counted.read < limits.ScanBytes || atEOF(reader)
	}
	p.closeCurrent(!p.result.EOF)
	return p.result, readErr
}

func atEOF(reader io.Reader) bool {
	var probe [1]byte
	n, err := io.ReadFull(reader, probe[:])
	return n == 0 && errors.Is(err, io.EOF)
}

type countingReader struct {
	reader io.Reader
	read   int64
}

func (c *countingReader) Read(buffer []byte) (int, error) {
	n, err := c.reader.Read(buffer)
	c.read += int64(n)
	return n, err
}

type marker struct {
	kind    string
	name    string
	stage   int
	attempt int
}

var fixedMarkers = map[string]marker{
	"REQUEST INFO":           {kind: KindRequestInfo, stage: 0},
	"HEADERS":                {kind: KindHeaders, stage: 1},
	"REQUEST BODY":           {kind: KindRequestBody, stage: 2},
	"WEBSOCKET TIMELINE":     {kind: KindWebsocketTimeline, stage: 3},
	"API WEBSOCKET TIMELINE": {kind: KindAPIWebsocketTimeline, stage: 4},
	"API ERROR RESPONSE":     {kind: KindAPIErrorResponse, stage: 6},
	"RESPONSE":               {kind: KindResponse, stage: 8},
}

const stageResponse = 8

func parseMarker(line string) (marker, bool) {
	if len(line) > maxMarkerLine || !strings.HasPrefix(line, "=== ") || !strings.HasSuffix(line, " ===") || len(line) < 9 {
		return marker{}, false
	}
	name := line[4 : len(line)-4]
	if found, ok := fixedMarkers[name]; ok {
		found.name = name
		return found, true
	}
	for _, numbered := range []marker{{kind: KindAPIRequest, name: "API REQUEST", stage: 5}, {kind: KindAPIResponse, name: "API RESPONSE", stage: 7}} {
		if name == numbered.name {
			return numbered, true
		}
		rest, ok := strings.CutPrefix(name, numbered.name+" ")
		if !ok {
			continue
		}
		attempt, err := strconv.Atoi(rest)
		if err == nil && attempt > 0 && strconv.Itoa(attempt) == rest {
			numbered.name = name
			numbered.attempt = attempt
			return numbered, true
		}
	}
	return marker{}, false
}

type parser struct {
	limits      Limits
	result      Result
	stage       int
	lastAttempt map[string]int
	current     *sectionState
}

// accepts keeps markers in Relay's write order; anything else is payload text.
func (p *parser) accepts(next marker) bool {
	if p.stage < 0 {
		return true
	}
	if p.stage == stageResponse || next.stage < p.stage {
		return false
	}
	if next.stage > p.stage {
		return true
	}
	switch next.kind {
	case KindAPIErrorResponse:
		return true
	case KindAPIRequest, KindAPIResponse:
		return next.attempt > p.lastAttempt[next.kind]
	}
	return false
}

func (p *parser) feed(fragment []byte, lineStart, lineEnd bool) {
	if lineStart && lineEnd && len(fragment) <= maxMarkerLine+2 {
		if next, ok := parseMarker(strings.TrimRight(string(fragment), "\r\n")); ok && p.accepts(next) {
			p.open(next)
			return
		}
	}
	if p.current == nil {
		p.current = p.newSection(marker{kind: KindUnrecognized})
	}
	p.current.feed(p, fragment, lineStart, lineEnd)
}

func (p *parser) open(next marker) {
	p.closeCurrent(false)
	p.result.Recognized = true
	p.stage = next.stage
	if next.attempt > 0 {
		p.lastAttempt[next.kind] = next.attempt
	}
	p.current = p.newSection(next)
}

func (p *parser) newSection(next marker) *sectionState {
	state := &sectionState{
		section:  Section{Kind: next.kind, Name: next.name, Attempt: next.attempt},
		retained: len(p.result.Sections) < p.limits.MaxSections,
	}
	switch next.kind {
	case KindUnrecognized, KindRequestBody, KindWebsocketTimeline, KindAPIWebsocketTimeline:
		state.inBody = true
	}
	return state
}

func (p *parser) closeCurrent(stoppedEarly bool) {
	state := p.current
	if state == nil {
		return
	}
	p.current = nil
	if stoppedEarly {
		state.section.Truncated = true
	} else if len(state.carry) > 0 {
		state.omit()
	}
	if !state.retained {
		p.result.SectionsOmitted++
		return
	}
	state.section.Meta = strings.TrimRight(state.meta.String(), "\r\n")
	if !state.section.BodyOmitted {
		state.section.Body = strings.TrimRight(state.body.String(), "\r\n")
	}
	p.result.Sections = append(p.result.Sections, state.section)
}

type sectionState struct {
	section   Section
	retained  bool
	inBody    bool
	metaLines int
	meta      strings.Builder
	body      strings.Builder
	carry     []byte
	binary    bool
}

func (s *sectionState) feed(p *parser, fragment []byte, lineStart, lineEnd bool) {
	if s.inBody {
		s.appendBody(fragment, p.limits.SectionBytes)
		return
	}
	text := strings.TrimRight(string(fragment), "\r\n")
	if lineStart && lineEnd {
		switch s.section.Kind {
		case KindAPIRequest, KindAPIResponse:
			if text == "Body:" {
				s.enterBody()
				return
			}
		case KindResponse:
			if text == "" {
				s.enterBody()
				return
			}
		}
		s.metaLine(p, text)
	}
	s.collectMarkers(fragment)
	appendBounded(&s.meta, fragment, p.limits.SectionBytes, &s.section.Truncated)
	if lineEnd {
		s.metaLines++
		if s.section.Kind == KindAPIErrorResponse && s.metaLines == 1 {
			s.enterBody()
		}
	}
}

func (s *sectionState) metaLine(p *parser, text string) {
	lower := strings.ToLower(text)
	switch {
	case s.section.Kind == KindRequestInfo && strings.HasPrefix(text, "Request ID: ") && p.result.RequestID == "":
		p.result.RequestID = strings.TrimSpace(strings.TrimPrefix(text, "Request ID: "))
	case strings.HasPrefix(text, "Status: "), strings.HasPrefix(text, "HTTP Status: "):
		if s.section.Status == 0 {
			_, value, _ := strings.Cut(text, ": ")
			s.section.Status, _ = strconv.Atoi(strings.TrimSpace(value))
		}
	case strings.HasPrefix(lower, "content-type:"):
		if isBinaryContentType(strings.TrimSpace(lower[len("content-type:"):])) {
			s.binary = true
		}
	}
}

func isBinaryContentType(value string) bool {
	for _, prefix := range []string{"audio/", "video/", "image/", "font/", "application/octet-stream", "application/zip", "application/pdf", "application/gzip"} {
		if strings.HasPrefix(value, prefix) {
			return true
		}
	}
	return false
}

func (s *sectionState) enterBody() {
	s.inBody = true
	if s.binary {
		s.omit()
	}
}

func (s *sectionState) omit() {
	s.section.BodyOmitted = true
	s.section.OmissionReason = OmittedBinary
	s.body.Reset()
	s.carry = nil
}

func (s *sectionState) appendBody(fragment []byte, limit int) {
	s.section.BodyBytes += int64(len(fragment))
	if s.section.BodyOmitted {
		return
	}
	data := fragment
	if len(s.carry) > 0 {
		data = append(append([]byte(nil), s.carry...), fragment...)
		s.carry = nil
	}
	valid, tail := splitIncompleteRune(data)
	if bytes.IndexByte(valid, 0) >= 0 || !utf8.Valid(valid) {
		s.omit()
		return
	}
	s.carry = append(s.carry, tail...)
	s.collectMarkers(valid)
	appendBounded(&s.body, valid, limit, &s.section.Truncated)
}

// splitIncompleteRune keeps a rune split across read fragments for the next one.
func splitIncompleteRune(data []byte) ([]byte, []byte) {
	for back := 1; back <= utf8.UTFMax-1 && back <= len(data); back++ {
		start := len(data) - back
		if !utf8.RuneStart(data[start]) {
			continue
		}
		if utf8.FullRune(data[start:]) {
			return data, nil
		}
		return data[:start], data[start:]
	}
	return data, nil
}

func appendBounded(builder *strings.Builder, data []byte, limit int, truncated *bool) {
	room := limit - builder.Len()
	if room <= 0 {
		if len(data) > 0 {
			*truncated = true
		}
		return
	}
	if len(data) > room {
		*truncated = true
		data = data[:room]
		for len(data) > 0 && !utf8.Valid(data) {
			data = data[:len(data)-1]
		}
	}
	builder.Write(data)
}

var capturedMarkers = []string{
	"[API REQUEST BODY TRUNCATED",
	"[REQUEST BODY TRUNCATED",
	"[DECOMPRESSED REQUEST BODY TRUNCATED]",
	"[DECOMPRESSION ERROR",
	"[html body omitted]",
	"[URL omitted: redaction length limit]",
}

func (s *sectionState) collectMarkers(data []byte) {
	if len(s.section.Markers) >= maxSectionMarkers {
		return
	}
	if string(bytes.TrimRight(data, "\r\n")) == "<missing>" {
		s.addMarker("<missing>")
	}
	for _, prefix := range capturedMarkers {
		index := bytes.Index(data, []byte(prefix))
		if index < 0 {
			continue
		}
		text := data[index:]
		if end := bytes.IndexByte(text, ']'); end >= 0 {
			text = text[:end+1]
		} else if end := bytes.IndexByte(text, '\n'); end >= 0 {
			text = text[:end]
		}
		if len(text) > maxMarkerText {
			text = text[:maxMarkerText]
		}
		s.addMarker(string(text))
	}
}

func (s *sectionState) addMarker(text string) {
	for _, existing := range s.section.Markers {
		if existing == text {
			return
		}
	}
	if len(s.section.Markers) < maxSectionMarkers {
		s.section.Markers = append(s.section.Markers, text)
	}
}
