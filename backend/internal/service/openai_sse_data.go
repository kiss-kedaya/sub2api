package service

import (
	"bytes"
	"strings"

	"github.com/tidwall/gjson"
)

// openAISSEDataFrame validates one SSE data payload and caches the effective
// event type plus a reusable gjson root. Hot-path consumers must use this
// metadata instead of independently validating and rescanning the same frame.
type openAISSEDataFrame struct {
	data             []byte
	trimmed          []byte
	root             gjson.Result
	validJSON        bool
	payloadEventType string
	eventType        string
}

func parseOpenAISSEDataFrame(data []byte, fallbackEventType string) openAISSEDataFrame {
	return newOpenAISSEDataFrame(data, fallbackEventType, false)
}

// parseTrustedOpenAISSEDataFrame refreshes metadata after a successful sjson /
// json.Marshal transformation. Those transforms can only return valid JSON, so
// revalidating their output would reintroduce a second full-frame scan.
func parseTrustedOpenAISSEDataFrame(data []byte, fallbackEventType string) openAISSEDataFrame {
	return newOpenAISSEDataFrame(data, fallbackEventType, true)
}

func newOpenAISSEDataFrame(data []byte, fallbackEventType string, trustedJSON bool) openAISSEDataFrame {
	trimmed := bytes.TrimSpace(data)
	frame := openAISSEDataFrame{
		data:    data,
		trimmed: trimmed,
	}
	if len(trimmed) == 0 || bytes.Equal(trimmed, []byte("[DONE]")) {
		frame.eventType = strings.TrimSpace(fallbackEventType)
		return frame
	}

	// Parse once even for malformed payloads: gjson preserves the historical
	// best-effort type extraction (for example, a terminal object followed by
	// trailing bytes), while validJSON keeps structured consumers fail-closed.
	frame.root = gjson.ParseBytes(trimmed)
	frame.validJSON = trustedJSON || gjson.ValidBytes(trimmed)
	frame.payloadEventType = strings.TrimSpace(frame.root.Get("type").String())
	frame.eventType = frame.payloadEventType
	if frame.eventType == "" {
		frame.eventType = strings.TrimSpace(fallbackEventType)
	}
	return frame
}

func (f openAISSEDataFrame) isDone() bool {
	return bytes.Equal(f.trimmed, []byte("[DONE]"))
}

type openAISSEDataAccumulator struct {
	lines []string
}

func (a *openAISSEDataAccumulator) AddLine(line string, fn func([]byte)) {
	if fn == nil {
		return
	}
	trimmedLine := strings.TrimRight(line, "\r\n")
	if data, ok := extractOpenAISSEDataLine(trimmedLine); ok {
		a.lines = append(a.lines, data)
		return
	}
	if strings.TrimSpace(trimmedLine) == "" {
		a.Flush(fn)
	}
}

func (a *openAISSEDataAccumulator) Flush(fn func([]byte)) {
	if fn == nil || len(a.lines) == 0 {
		return
	}
	emitOpenAISSEDataPayloads(a.lines, fn)
	a.lines = a.lines[:0]
}

func forEachOpenAISSEDataPayload(body string, fn func([]byte)) {
	if fn == nil || strings.TrimSpace(body) == "" {
		return
	}
	var acc openAISSEDataAccumulator
	for _, line := range strings.Split(body, "\n") {
		acc.AddLine(line, fn)
	}
	acc.Flush(fn)
}

func forEachOpenAISSEFrame(body string, fn func(string, []byte)) {
	if fn == nil || strings.TrimSpace(body) == "" {
		return
	}
	var parser openAICompatSSEFrameParser
	emit := func(frame openAICompatSSEFrame, ok bool) {
		if !ok {
			return
		}
		emitData := func(value string) {
			value = strings.TrimSpace(value)
			if value == "" || value == "[DONE]" {
				return
			}
			data := []byte(value)
			fn(effectiveOpenAISSEEventType(data, frame.EventType), data)
		}
		if gjson.Valid(frame.Data) {
			emitData(frame.Data)
			return
		}
		for _, value := range strings.Split(frame.Data, "\n") {
			emitData(value)
		}
	}
	for _, line := range strings.Split(body, "\n") {
		emit(parser.AddLine(strings.TrimRight(line, "\r")))
	}
	emit(parser.Finish())
}

func emitOpenAISSEDataPayloads(lines []string, fn func([]byte)) {
	if fn == nil || len(lines) == 0 {
		return
	}
	if len(lines) == 1 {
		emitOpenAISSEDataPayload(lines[0], fn)
		return
	}
	joined := strings.Join(lines, "\n")
	if gjson.Valid(joined) {
		emitOpenAISSEDataPayload(joined, fn)
		return
	}
	for _, line := range lines {
		emitOpenAISSEDataPayload(line, fn)
	}
}

func emitOpenAISSEDataPayload(data string, fn func([]byte)) {
	data = strings.TrimSpace(data)
	if data == "" || data == "[DONE]" {
		return
	}
	fn([]byte(data))
}
