package apicompat

import (
	"strings"
	"unicode"
)

// Leading <think> / <thinking> blocks that chat upstreams inline into content.
// Only the block at the start of the message is split. Mirrors cc-switch
// InlineThinkSplitter: mismatched close tags count, and reasoning is emitted
// before the close tag arrives.

var inlineThinkPairs = [][2]string{
	{"<think>", "</think>"},
	{"<thinking>", "</thinking>"},
}

const inlineThinkSeparators = "\r\n"

type inlineThinkMode int

const (
	inlineThinkDetecting inlineThinkMode = iota
	inlineThinkReasoning
	inlineThinkText
)

type inlineThinkPrefix int

const (
	inlineThinkNeedMore inlineThinkPrefix = iota
	inlineThinkIsReasoning
	inlineThinkIsText
)

// InlineThinkSplitter peels a leading think block out of streamed content.
// Push returns (thinking, text) increments. Flush is idempotent and must run
// before a tool call or at end of stream.
type InlineThinkSplitter struct {
	mode           inlineThinkMode
	buffer         string
	stripSeparator bool
}

func (s *InlineThinkSplitter) Push(delta string) (string, string) {
	switch s.mode {
	case inlineThinkText:
		return "", s.pushText(delta)
	case inlineThinkDetecting:
		s.buffer += delta
		switch leadingThinkPrefixDecision(s.buffer) {
		case inlineThinkNeedMore:
			return "", ""
		case inlineThinkIsReasoning:
			s.mode = inlineThinkReasoning
			s.stripSeparator = true
			rest, _ := stripLeadingThinkOpenTag(s.buffer)
			s.buffer = rest
			return s.drainReasoning()
		default:
			s.mode = inlineThinkText
			text := s.buffer
			s.buffer = ""
			return "", text
		}
	default:
		s.buffer += delta
		return s.drainReasoning()
	}
}

func (s *InlineThinkSplitter) Flush() (string, string) {
	buffered := s.buffer
	s.buffer = ""
	wasReasoning := s.mode == inlineThinkReasoning
	s.mode = inlineThinkText
	s.stripSeparator = false
	if wasReasoning {
		return buffered, ""
	}
	return "", buffered
}

func (s *InlineThinkSplitter) pushText(delta string) string {
	if !s.stripSeparator {
		return delta
	}
	rest := strings.TrimLeft(delta, inlineThinkSeparators)
	if rest == "" {
		return ""
	}
	s.stripSeparator = false
	return rest
}

func (s *InlineThinkSplitter) drainReasoning() (string, string) {
	if s.stripSeparator {
		s.buffer = strings.TrimLeft(s.buffer, inlineThinkSeparators)
		if s.buffer == "" {
			return "", ""
		}
		s.stripSeparator = false
	}
	if idx, tag, ok := findThinkCloseTag(s.buffer); ok {
		buffered := s.buffer
		s.buffer = ""
		s.mode = inlineThinkText
		s.stripSeparator = true
		thinking := strings.TrimRight(buffered[:idx], inlineThinkSeparators)
		return thinking, s.pushText(buffered[idx+len(tag):])
	}
	holdback := reasoningHoldbackLen(s.buffer)
	splitAt := len(s.buffer) - holdback
	thinking := s.buffer[:splitAt]
	s.buffer = s.buffer[splitAt:]
	return thinking, ""
}

// splitLeadingThinkBlock splits one complete leading think block.
// Unclosed or non-leading input returns ok=false.
func splitLeadingThinkBlock(text string) (thinking, answer string, ok bool) {
	body, ok := stripLeadingThinkOpenTag(text)
	if !ok {
		return "", "", false
	}
	idx, tag, found := findThinkCloseTag(body)
	if !found {
		return "", "", false
	}
	thinking = strings.Trim(body[:idx], inlineThinkSeparators)
	answer = strings.TrimLeft(body[idx+len(tag):], inlineThinkSeparators)
	return thinking, answer, true
}

// splitChatReasoningAndText moves a leading inline think block into reasoning.
// An existing reasoning_content value wins; the tags are still removed.
func splitChatReasoningAndText(reasoning, text string) (string, string) {
	thinking, answer, ok := splitLeadingThinkBlock(text)
	if !ok {
		return reasoning, text
	}
	if strings.TrimSpace(reasoning) == "" {
		reasoning = thinking
	}
	return reasoning, answer
}

func leadingThinkPrefixDecision(buffer string) inlineThinkPrefix {
	trimmed := strings.TrimLeftFunc(buffer, unicode.IsSpace)
	if trimmed == "" {
		return inlineThinkNeedMore
	}
	for _, pair := range inlineThinkPairs {
		if strings.HasPrefix(trimmed, pair[0]) {
			return inlineThinkIsReasoning
		}
	}
	for _, pair := range inlineThinkPairs {
		if strings.HasPrefix(pair[0], trimmed) {
			return inlineThinkNeedMore
		}
	}
	return inlineThinkIsText
}

func stripLeadingThinkOpenTag(text string) (string, bool) {
	after := strings.TrimLeftFunc(text, unicode.IsSpace)
	for _, pair := range inlineThinkPairs {
		if rest, ok := strings.CutPrefix(after, pair[0]); ok {
			return rest, true
		}
	}
	return "", false
}

func findThinkCloseTag(text string) (int, string, bool) {
	bestAt := -1
	bestTag := ""
	for _, pair := range inlineThinkPairs {
		at := strings.Index(text, pair[1])
		if at >= 0 && (bestAt < 0 || at < bestAt) {
			bestAt = at
			bestTag = pair[1]
		}
	}
	if bestAt < 0 {
		return 0, "", false
	}
	return bestAt, bestTag, true
}

func reasoningHoldbackLen(buffer string) int {
	partial := 0
	for _, pair := range inlineThinkPairs {
		closeTag := pair[1]
		for n := len(closeTag) - 1; n >= 1; n-- {
			if strings.HasSuffix(buffer, closeTag[:n]) {
				if n > partial {
					partial = n
				}
				break
			}
		}
	}
	body := buffer[:len(buffer)-partial]
	return len(buffer) - len(strings.TrimRight(body, inlineThinkSeparators))
}
