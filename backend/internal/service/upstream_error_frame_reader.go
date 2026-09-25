package service

import (
	"bufio"
	"bytes"
	"encoding/json"
	"io"
	"strings"
)

type upstreamErrorFrameReader struct {
	reader  *bufio.Reader
	limit   int64
	pending []byte
	err     error
}

func newUpstreamErrorFrameReader(reader io.Reader, limit int64) io.Reader {
	return &upstreamErrorFrameReader{reader: bufio.NewReader(reader), limit: limit}
}

func (reader *upstreamErrorFrameReader) Read(target []byte) (int, error) {
	if len(target) == 0 {
		return 0, nil
	}
	for len(reader.pending) == 0 {
		if reader.err != nil {
			return 0, reader.err
		}
		frame, err := reader.readFrame()
		reader.err = err
		reader.pending = frame
	}
	count := copy(target, reader.pending)
	reader.pending = reader.pending[count:]
	return count, nil
}

func (reader *upstreamErrorFrameReader) readFrame() ([]byte, error) {
	var frame bytes.Buffer
	var parser openAICompatSSEFrameParser
	dataLines := 0
	plainJSON := false
	lineStart := 0
	for {
		line, err := reader.reader.ReadSlice('\n')
		if int64(frame.Len()+len(line)) > reader.limit {
			return nil, bufio.ErrTooLong
		}
		if frame.Len() == 0 && err != bufio.ErrBufferFull {
			text := strings.TrimRight(string(line), "\r\n")
			if text == "" || strings.HasPrefix(text, ":") {
				return line, err
			}
			if data, ok := extractOpenAISSEDataLine(text); ok && (data == "[DONE]" || json.Valid([]byte(data))) {
				return line, err
			}
		}
		_, _ = frame.Write(line)
		if err == bufio.ErrBufferFull {
			continue
		}
		line = frame.Bytes()[lineStart:]
		lineStart = frame.Len()
		if dataLines == 0 {
			trimmed := bytes.TrimSpace(frame.Bytes())
			plainJSON = bytes.HasPrefix(trimmed, []byte("{")) || bytes.HasPrefix(trimmed, []byte("["))
		}
		if plainJSON {
			if err != nil || json.Valid(frame.Bytes()) {
				body := frame.Bytes()
				if upstreamFinancialFailureEnvelope(body) {
					var failure any = json.RawMessage(body)
					if !json.Valid(body) {
						failure = string(body)
					}
					payload, _ := json.Marshal(map[string]any{"type": "error", "error": failure})
					return append(append([]byte("data: "), payload...), '\n', '\n'), err
				}
				return body, err
			}
			continue
		}
		text := strings.TrimRight(string(line), "\r\n")
		if frame.Len() == len(line) && strings.HasPrefix(text, ":") {
			return frame.Bytes(), err
		}
		if data, ok := extractOpenAISSEDataLine(text); ok {
			dataLines++
			if dataLines == 1 && (data == "[DONE]" || json.Valid([]byte(data))) {
				return frame.Bytes(), err
			}
		}
		parsed, ready := parser.AddLine(text)
		if err != nil && !ready {
			parsed, ready = parser.Finish()
		}
		if text == "" || err != nil {
			if ready && dataLines > 1 && (upstreamFinancialFailureEnvelope([]byte(parsed.Data)) || parsed.EventType == "error" && IsUpstreamFinancialError(0, []byte(parsed.Data))) {
				var compact bytes.Buffer
				if json.Compact(&compact, []byte(parsed.Data)) == nil {
					return []byte("event: error\ndata: " + compact.String() + "\n\n"), err
				}
			}
			return frame.Bytes(), err
		}
	}
}
