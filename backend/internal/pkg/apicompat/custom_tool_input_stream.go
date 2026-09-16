package apicompat

import (
	"encoding/json"
	"errors"
	"io"
	"strings"
	"unicode/utf8"
)

// Only the canonical root input-string wrapper streams early. Other argument
// shapes keep the existing complete-JSON fallback, never a nested input field.
type customToolInputStream struct {
	started  bool
	closed   bool
	disabled bool
	finished bool
	offset   int
	sent     strings.Builder
}

func (s *customToolInputStream) append(arguments string) (string, error) {
	if s.closed || s.disabled {
		return "", nil
	}
	if !s.started {
		// Prefix parsing is bounded; a pathological key or whitespace prefix
		// falls back to terminal decoding instead of rescanning the whole call.
		prefix := arguments[:min(len(arguments), 256)]
		decoder := json.NewDecoder(strings.NewReader(prefix))
		opening, err := decoder.Token()
		if err != nil {
			s.disabled = len(arguments) >= 256 || (!errors.Is(err, io.EOF) && !errors.Is(err, io.ErrUnexpectedEOF))
			return "", nil
		}
		if opening != json.Delim('{') {
			s.disabled = true
			return "", nil
		}
		key, err := decoder.Token()
		if err != nil {
			s.disabled = len(arguments) >= 256 || (!errors.Is(err, io.EOF) && !errors.Is(err, io.ErrUnexpectedEOF))
			return "", nil
		}
		if key != "input" {
			s.disabled = true
			return "", nil
		}
		pos := int(decoder.InputOffset())
		for _, expected := range []byte{':', '"'} {
			for pos < len(prefix) && strings.ContainsRune(" \t\r\n", rune(prefix[pos])) {
				pos++
			}
			if pos == len(prefix) {
				s.disabled = len(arguments) >= 256
				return "", nil
			}
			if prefix[pos] != expected {
				s.disabled = true
				return "", nil
			}
			pos++
		}
		s.offset, s.started = pos, true
	}
	start, pos := s.offset, s.offset
	for pos < len(arguments) {
		ch := arguments[pos]
		if ch == '"' {
			s.closed = true
			break
		}
		if ch == '\\' {
			if pos+1 >= len(arguments) {
				break
			}
			width := 2
			if arguments[pos+1] == 'u' {
				width = 6
				if pos+width > len(arguments) {
					break
				}
				// Keep a possible surrogate pair in the same standard-library
				// decode, even when its two escape sequences arrive separately.
				if (arguments[pos+2] == 'd' || arguments[pos+2] == 'D') && strings.ContainsRune("89aAbB", rune(arguments[pos+3])) {
					next := pos + width
					if next == len(arguments) || (arguments[next] == '\\' && next+1 == len(arguments)) {
						break
					}
					if arguments[next] == '\\' && arguments[next+1] == 'u' {
						if next+6 > len(arguments) {
							break
						}
						if (arguments[next+2] == 'd' || arguments[next+2] == 'D') && strings.ContainsRune("cCdDeEfF", rune(arguments[next+3])) {
							width += 6
						}
					}
				}
			}
			if pos+width > len(arguments) {
				break
			}
			pos += width
			continue
		}
		if ch >= utf8.RuneSelf {
			if !utf8.FullRuneInString(arguments[pos:]) {
				break
			}
			_, width := utf8.DecodeRuneInString(arguments[pos:])
			pos += width
		} else {
			pos++
		}
	}
	if pos == start {
		return "", nil
	}
	raw := make([]byte, pos-start+2)
	raw[0], raw[len(raw)-1] = '"', '"'
	copy(raw[1:], arguments[start:pos])
	var delta string
	if err := json.Unmarshal(raw, &delta); err != nil {
		return "", errors.New("invalid custom tool input string")
	}
	s.offset = pos
	_, _ = s.sent.WriteString(delta)
	return delta, nil
}

func (s *customToolInputStream) finish(input string) (string, error) {
	if (s.finished && input != s.sent.String()) || !strings.HasPrefix(input, s.sent.String()) {
		return "", errors.New("custom tool terminal input differs from streamed input")
	}
	tail := input[s.sent.Len():]
	_, _ = s.sent.WriteString(tail)
	s.closed = true
	s.finished = true
	return tail, nil
}
