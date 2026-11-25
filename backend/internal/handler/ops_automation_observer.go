package handler

import (
	"bytes"
	"encoding/json"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/ctxkey"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
)

// This writer forwards bytes immediately. Only bounded frame metadata is kept;
// it never captures request prompts, response text or credentials in storage.
type automationObserverWriter struct {
	gin.ResponseWriter
	mu            sync.Mutex
	frame         []byte
	terminal      bool
	failed        bool
	truncated     bool
	body          []byte
	bodyTruncated bool
}

func (w *automationObserverWriter) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	n, err := w.ResponseWriter.Write(p)
	if err != nil {
		w.failed = true
	}
	if !strings.Contains(w.Header().Get("Content-Type"), "text/event-stream") && !w.bodyTruncated {
		if len(w.body)+n <= 64<<10 {
			w.body = append(w.body, p[:n]...)
		} else {
			w.bodyTruncated = true
			w.body = nil
		}
	}
	w.observe(p[:n])
	return n, err
}
func (w *automationObserverWriter) WriteString(s string) (int, error) { return w.Write([]byte(s)) }
func (w *automationObserverWriter) Flush() {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.ResponseWriter.Flush()
}
func (w *automationObserverWriter) observe(p []byte) {
	for len(p) > 0 {
		end := bytes.IndexByte(p, '\n')
		if end < 0 {
			if len(w.frame)+len(p) > 64<<10 {
				w.truncated = true
				w.frame = nil
			} else if !w.truncated {
				w.frame = append(w.frame, p...)
			}
			return
		}
		if !w.truncated {
			if len(w.frame)+end <= 64<<10 {
				w.frame = append(w.frame, p[:end]...)
				w.observeLine(w.frame)
			} else {
				w.failed = true
			}
		} else {
			w.failed = true
		}
		w.frame = nil
		w.truncated = false
		p = p[end+1:]
	}
}
func (w *automationObserverWriter) observeLine(line []byte) {
	line = bytes.TrimSpace(line)
	if bytes.HasPrefix(line, []byte("data:")) {
		line = bytes.TrimSpace(line[5:])
	} else {
		return
	}
	if bytes.Equal(line, []byte("[DONE]")) {
		return
	} // DONE alone cannot prove protocol completion.
	if !automationLineMayMatter(line) {
		return
	}
	var item struct {
		Type     string          `json:"type"`
		Error    json.RawMessage `json:"error"`
		Response struct {
			Status string `json:"status"`
		} `json:"response"`
		Choices []struct {
			Finish *string `json:"finish_reason"`
		} `json:"choices"`
		Candidates []struct {
			Finish string `json:"finishReason"`
		} `json:"candidates"`
	}
	if json.Unmarshal(line, &item) != nil {
		return
	}
	if len(item.Error) > 0 && string(item.Error) != "null" {
		w.failed = true
	}
	switch item.Type {
	case "error", "response.failed", "response.incomplete":
		w.failed = true
	case "message_stop":
		w.terminal = true
	case "response.completed":
		if item.Response.Status == "completed" {
			w.terminal = true
		}
	}
	for _, v := range item.Choices {
		if v.Finish != nil && (*v.Finish == "stop" || *v.Finish == "length" || *v.Finish == "tool_calls" || *v.Finish == "function_call") {
			w.terminal = true
		}
	}
	for _, v := range item.Candidates {
		if v.Finish == "STOP" || v.Finish == "MAX_TOKENS" {
			w.terminal = true
		}
	}
}

// Every verdict observeLine can reach hinges on one of a few JSON string
// literals (the terminal event types and finish reasons) or on an "error" key,
// which encoding/json matches case-insensitively. A line holding none of them
// as a whole string, and no \u escape that could spell one, cannot change the
// verdict whatever its structure, which spares decoding the great majority of
// data lines (plain deltas) in full.
//
// automationLineMayMatter is that conservative necessary condition: false
// means observeLine would leave failed and terminal untouched. One pass over
// the line: only a quote or a backslash needs a closer look.
func automationLineMayMatter(line []byte) bool {
	for i, b := range line {
		switch b {
		case '"':
			if automationVerdictLiteralAt(line[i+1:]) {
				return true
			}
		case '\\':
			if i+1 < len(line) && line[i+1] == 'u' {
				return true
			}
		}
	}
	return false
}

// The closing quote is part of each literal: they must be whole strings.
var (
	automationResponseLiterals  = []string{`response.failed"`, `response.incomplete"`, `response.completed"`}
	automationMessageLiterals   = []string{`message_stop"`}
	automationStopLiterals      = []string{`stop"`}
	automationLengthLiterals    = []string{`length"`}
	automationToolLiterals      = []string{`tool_calls"`}
	automationFunctionLiterals  = []string{`function_call"`}
	automationUpperStopLiterals = []string{`STOP"`}
	automationMaxLiterals       = []string{`MAX_TOKENS"`}
)

// automationVerdictLiteralAt reports whether s, which follows an opening
// quote, is one of the verdict literals up to and including its closing quote.
func automationVerdictLiteralAt(s []byte) bool {
	if len(s) == 0 {
		return false
	}
	var candidates []string
	switch s[0] {
	case 'e', 'E':
		// A key that encoding/json folds onto "error" (ASCII only: no other
		// rune folds to e, r or o) or the "error" event type.
		return len(s) >= 6 && s[1]|0x20 == 'r' && s[2]|0x20 == 'r' && s[3]|0x20 == 'o' && s[4]|0x20 == 'r' && s[5] == '"'
	case 'r':
		candidates = automationResponseLiterals
	case 'm':
		candidates = automationMessageLiterals
	case 's':
		candidates = automationStopLiterals
	case 'l':
		candidates = automationLengthLiterals
	case 't':
		candidates = automationToolLiterals
	case 'f':
		candidates = automationFunctionLiterals
	case 'S':
		candidates = automationUpperStopLiterals
	case 'M':
		candidates = automationMaxLiterals
	default:
		return false
	}
	for _, literal := range candidates {
		if len(s) >= len(literal) && string(s[:len(literal)]) == literal {
			return true
		}
	}
	return false
}

func OpsAutomationObserver(engine *service.OpsAutomationService) gin.HandlerFunc {
	return func(c *gin.Context) {
		path := c.Request.URL.Path
		if engine == nil || c.Request.Method != http.MethodPost || (!strings.HasSuffix(path, "/responses") && !strings.HasSuffix(path, "/messages") && !strings.HasSuffix(path, "/chat/completions") && !strings.Contains(path, ":generateContent") && !strings.Contains(path, ":streamGenerateContent")) {
			c.Next()
			return
		}
		started := time.Now()
		writer := &automationObserverWriter{ResponseWriter: c.Writer}
		c.Writer = writer
		c.Next()
		id := c.GetInt64(opsAccountIDKey)
		if id <= 0 {
			return
		}
		failedAccounts := map[int64]bool{}
		if raw, found := c.Get(service.OpsUpstreamErrorsKey); found {
			if events, ok := raw.([]*service.OpsUpstreamErrorEvent); ok {
				for _, e := range events {
					if e != nil && e.AccountID > 0 {
						failedAccounts[e.AccountID] = true
					}
				}
			}
		}
		for failedID := range failedAccounts {
			engine.ObserveOutcome(service.AutomationOutcome{AccountID: failedID, StartedAt: started, Success: false})
		}
		writer.mu.Lock()
		success := c.Request.Context().Err() == nil && writer.Status() >= 200 && writer.Status() < 300 && len(c.Errors) == 0 && !writer.failed && !failedAccounts[id]
		streaming := c.GetBool(opsStreamKey) || strings.Contains(writer.Header().Get("Content-Type"), "text/event-stream")
		if streaming && !writer.terminal {
			success = false
		}
		if !streaming && !automationNonstreamSuccess(writer.body) {
			success = false
		}
		if len(service.GetOpsStreamErrors(c)) > 0 {
			success = false
		}
		writer.mu.Unlock()
		if failedAccounts[id] {
			return
		}
		groupID := int64(0)
		if g, ok := c.Request.Context().Value(ctxkey.Group).(*service.Group); ok && g != nil {
			groupID = g.ID
		}
		engine.ObserveOutcome(service.AutomationOutcome{AccountID: id, GroupID: groupID, Model: c.GetString(opsModelKey), StartedAt: started, Success: success})
	}
}

func automationNonstreamSuccess(body []byte) bool {
	var v struct {
		Type    string            `json:"type"`
		Object  string            `json:"object"`
		Status  string            `json:"status"`
		Error   json.RawMessage   `json:"error"`
		Content []json.RawMessage `json:"content"`
		Output  []json.RawMessage `json:"output"`
		Choices []struct {
			Finish *string `json:"finish_reason"`
		} `json:"choices"`
		Candidates []struct {
			Finish string `json:"finishReason"`
		} `json:"candidates"`
	}
	if json.Unmarshal(body, &v) != nil || len(v.Error) > 0 && string(v.Error) != "null" {
		return false
	}
	if v.Object == "response" {
		return v.Status == "completed" && len(v.Output) > 0
	}
	if v.Type == "message" {
		return len(v.Content) > 0
	}
	for _, c := range v.Choices {
		if c.Finish != nil && (*c.Finish == "stop" || *c.Finish == "length" || *c.Finish == "tool_calls" || *c.Finish == "function_call") {
			return true
		}
	}
	for _, c := range v.Candidates {
		if c.Finish == "STOP" || c.Finish == "MAX_TOKENS" {
			return true
		}
	}
	return false
}
