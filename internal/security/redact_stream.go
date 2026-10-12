package security

import (
	"regexp"
	"strings"
)

type redactMode uint8

const (
	redactText redactMode = iota
	redactAfterKey
	redactBeforeValue
	redactToken
	redactQuoted
	redactHeader
	redactPrefixedToken
	redactContainer
	redactURL
)

const redactionRetainLimit = 8192
const redactionLookbehind = 32

var streamKeyPattern = regexp.MustCompile(`(?i)["']?(?:proxy[_-]?authorization|authorization|client[_-]?secret|refresh[_-]?token|access[_-]?token|id[_-]?token|api[_-]?key|set-cookie|password|passwd|secret|cookie|account|token)["']?`)
var streamBearerPattern = regexp.MustCompile(`(?i)\bBearer[ \t\r\n]+`)
var streamURLPattern = regexp.MustCompile(`(?i)\bhttps?://`)
var streamKeyPrefixPattern = regexp.MustCompile(`\b(?:sk|pk|pat|glpat)-[A-Za-z0-9_-]{8}|\b(?:ghp|gho|ghu|ghs|ghr)_[A-Za-z0-9]{8}|\bgithub_pat_[A-Za-z0-9_]{8}|\bxox[baprs]-[A-Za-z0-9-]{8}`)

// RedactionStream retains only incomplete markers and bounded URL text. Once a
// credential starts, its bytes are discarded until its delimiter, regardless of length.
type RedactionStream struct {
	tail       string
	mode       redactMode
	key        string
	keyQuote   byte
	quotedKey  bool
	quote      byte
	escaped    bool
	depth      int
	last       byte
	lineStart  bool
	header     bool
	url        []byte
	urlOmitted bool
	closed     bool
}

func NewRedactionStream() *RedactionStream { return &RedactionStream{lineStart: true} }

func (s *RedactionStream) Write(chunk string) string {
	if s.closed {
		return ""
	}
	var out strings.Builder
	// Bound retained state even if a caller supplies an unusually large chunk.
	for len(chunk) > 0 {
		n := len(chunk)
		if n > 4096 {
			n = 4096
		}
		s.tail += chunk[:n]
		chunk = chunk[n:]
		s.drain(&out, false)
	}
	return out.String()
}

func (s *RedactionStream) Close() string {
	if s.closed {
		return ""
	}
	s.closed = true
	var out strings.Builder
	s.drain(&out, true)
	if s.mode == redactURL && !s.urlOmitted {
		out.WriteString(RedactText(string(s.url)))
	}
	s.tail = ""
	s.url = nil
	return out.String()
}

func (s *RedactionStream) emit(out *strings.Builder, value string) {
	out.WriteString(value)
	for i := 0; i < len(value); i++ {
		s.last = value[i]
		if value[i] == '\n' || value[i] == '\r' {
			s.lineStart = true
		} else if value[i] != ' ' && value[i] != '\t' {
			s.lineStart = false
		}
	}
}

func streamWord(c byte) bool {
	return c == '_' || c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9'
}
func prefixValueByte(c byte) bool { return streamWord(c) || c == '-' }
func streamSpace(c byte) bool     { return c == ' ' || c == '\t' || c == '\r' || c == '\n' }

func (s *RedactionStream) drain(out *strings.Builder, final bool) {
	for len(s.tail) > 0 {
		c := s.tail[0]
		switch s.mode {
		case redactText:
			start, end, kind := -1, 0, redactText
			patterns := []*regexp.Regexp{streamKeyPattern, streamBearerPattern, streamKeyPrefixPattern, streamURLPattern}
			kinds := []redactMode{redactAfterKey, redactBeforeValue, redactPrefixedToken, redactURL}
			for i, pattern := range patterns {
				for _, match := range pattern.FindAllStringIndex(s.tail, -1) {
					if i > 0 && match[0] == 0 && streamWord(s.last) {
						continue
					}
					if start < 0 || match[0] < start {
						start, end, kind = match[0], match[1], kinds[i]
					}
					break
				}
			}
			if start < 0 {
				n := len(s.tail)
				if !final {
					n -= redactionLookbehind
					if n <= 0 {
						return
					}
				}
				s.emit(out, s.tail[:n])
				s.tail = s.tail[n:]
				continue
			}
			s.emit(out, s.tail[:start])
			marker := s.tail[start:end]
			s.tail = s.tail[end:]
			s.mode = kind
			switch kind {
			case redactAfterKey:
				s.key = strings.ToLower(strings.Trim(marker, "\"'"))
				s.key = strings.NewReplacer("_", "", "-", "").Replace(s.key)
				s.keyQuote = 0
				quoted := marker[0] == '\'' || marker[0] == '"'
				s.quotedKey = quoted
				if quoted && marker[len(marker)-1] != marker[0] {
					s.keyQuote = marker[0]
				}
				s.header = !quoted && s.lineStart && (s.key == "authorization" || s.key == "proxyauthorization" || s.key == "cookie" || s.key == "setcookie")
				s.emit(out, marker)
			case redactBeforeValue:
				s.key = "bearer"
				s.header = false
				s.quotedKey = false
				s.emit(out, marker)
			case redactPrefixedToken:
				out.WriteString(RedactedValue)
			case redactURL:
				s.url = []byte(marker)
				s.urlOmitted = false
			}
		case redactAfterKey:
			if streamSpace(c) && c != '\r' && c != '\n' {
				s.emit(out, s.tail[:1])
				s.tail = s.tail[1:]
				continue
			}
			if s.keyQuote != 0 && c == s.keyQuote {
				s.keyQuote = 0
				s.emit(out, s.tail[:1])
				s.tail = s.tail[1:]
				continue
			}
			if c == ':' || c == '=' {
				s.emit(out, s.tail[:1])
				s.tail = s.tail[1:]
				s.mode = redactBeforeValue
			} else {
				s.mode = redactText
			}
		case redactBeforeValue:
			if c == ' ' || c == '\t' || s.key == "bearer" && streamSpace(c) {
				s.emit(out, s.tail[:1])
				s.tail = s.tail[1:]
				continue
			}
			if c == '\r' || c == '\n' || c == ',' || c == '}' || c == ']' {
				s.mode = redactText
				continue
			}
			if s.header {
				out.WriteString(RedactedValue)
				s.mode = redactHeader
				continue
			}
			if s.key == "authorization" || s.key == "proxyauthorization" {
				lower := strings.ToLower(s.tail)
				for _, scheme := range []string{"bearer", "basic", "token", "digest", "negotiate"} {
					if !final && strings.HasPrefix(scheme, lower) {
						return
					}
					if len(lower) > len(scheme) && strings.HasPrefix(lower, scheme) && streamSpace(lower[len(scheme)]) {
						s.emit(out, s.tail[:len(scheme)+1])
						s.tail = s.tail[len(scheme)+1:]
						s.key = "bearer"
						break
					}
				}
				if s.key == "bearer" {
					continue
				}
			}
			if c == '"' || c == '\'' {
				s.emit(out, s.tail[:1])
				out.WriteString(RedactedValue)
			} else if s.quotedKey {
				out.WriteString("\"" + RedactedValue + "\"")
			} else {
				out.WriteString(RedactedValue)
			}
			if c == '"' || c == '\'' {
				s.mode = redactQuoted
				s.quote = c
				s.escaped = false
				s.tail = s.tail[1:]
			} else if c == '{' || c == '[' {
				s.mode = redactContainer
				s.depth = 1
				s.quote = 0
				s.tail = s.tail[1:]
			} else {
				s.mode = redactToken
			}
		case redactQuoted:
			s.tail = s.tail[1:]
			if s.escaped {
				s.escaped = false
			} else if c == '\\' {
				s.escaped = true
			} else if c == s.quote {
				s.emit(out, string(c))
				s.mode = redactText
			}
		case redactHeader:
			if c == '\n' || c == '\r' {
				s.mode = redactText
			} else {
				s.tail = s.tail[1:]
			}
		case redactToken:
			if strings.ContainsRune(" \t\r\n,;}&\"'<>", rune(c)) {
				s.mode = redactText
			} else {
				s.tail = s.tail[1:]
			}
		case redactPrefixedToken:
			if prefixValueByte(c) {
				s.tail = s.tail[1:]
			} else {
				s.mode = redactText
			}
		case redactContainer:
			s.tail = s.tail[1:]
			if s.quote != 0 {
				if s.escaped {
					s.escaped = false
				} else if c == '\\' {
					s.escaped = true
				} else if c == s.quote {
					s.quote = 0
				}
			} else if c == '"' || c == '\'' {
				s.quote = c
			} else if c == '{' || c == '[' {
				s.depth++
			} else if c == '}' || c == ']' {
				s.depth--
				if s.depth == 0 {
					s.mode = redactText
				}
			}
		case redactURL:
			if streamSpace(c) || strings.ContainsRune("\"'<>", rune(c)) {
				if !s.urlOmitted {
					out.WriteString(RedactText(string(s.url)))
				}
				s.url = nil
				s.mode = redactText
				continue
			}
			if !s.urlOmitted {
				if len(s.url) == redactionRetainLimit {
					// shortcut: omit URLs over 8 KiB; add a URL lexer if real captures require longer public URLs.
					out.WriteString("[URL omitted: redaction length limit]")
					s.url = nil
					s.urlOmitted = true
				} else {
					s.url = append(s.url, c)
				}
			}
			s.tail = s.tail[1:]
		}
	}
}
