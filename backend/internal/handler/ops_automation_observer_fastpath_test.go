package handler

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

type headerCountingResponseWriter struct {
	gin.ResponseWriter
	headerCalls int
	writeStrCnt int
}

func (w *headerCountingResponseWriter) Header() http.Header {
	w.headerCalls++
	return w.ResponseWriter.Header()
}

func (w *headerCountingResponseWriter) WriteString(s string) (int, error) {
	w.writeStrCnt++
	return w.ResponseWriter.WriteString(s)
}

func TestAutomationObserverWriter_CachesIsSSEAndReusesFrame(t *testing.T) {
	gin.SetMode(gin.TestMode)
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", nil)
	c.Header("Content-Type", "text/event-stream")

	counter := &headerCountingResponseWriter{ResponseWriter: c.Writer}
	w := &automationObserverWriter{ResponseWriter: counter}

	const deltaLine = "data: {\"type\":\"response.output_text.delta\",\"delta\":\"if a \\u003c b \\u0026\\u0026 c \\u003e d\"}\n"
	for i := 0; i < 50; i++ {
		n, err := w.WriteString(deltaLine)
		require.NoError(t, err)
		require.Equal(t, len(deltaLine), n)
	}
	// Header() should be checked at most once to determine isSSE across all 50 chunks.
	require.Equal(t, 1, counter.headerCalls)
	require.Equal(t, 50, counter.writeStrCnt)
	require.True(t, w.isSSE)
	require.Empty(t, w.body)
	require.False(t, w.failed)
	require.False(t, w.terminal)

	// Split writes must reuse w.frame backing capacity instead of dropping it to nil.
	_, err := w.WriteString("data: {\"type\":\"message_")
	require.NoError(t, err)
	_, err = w.WriteString("stop\"}\n")
	require.NoError(t, err)
	require.True(t, w.terminal)
	require.Empty(t, w.frame)
	capAfterFirst := cap(w.frame)
	require.Greater(t, capAfterFirst, 0, "frame capacity should be retained for reuse")
	// A subsequent split line of the same size reuses the exact retained capacity.
	_, err = w.WriteString("data: {\"type\":\"ping")
	require.NoError(t, err)
	_, err = w.WriteString("\"}\n")
	require.NoError(t, err)
	require.Equal(t, capAfterFirst, cap(w.frame))
}

func TestAutomationObserverWriter_WriteStringZeroAllocOnBenignSSELine(t *testing.T) {
	gin.SetMode(gin.TestMode)
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", nil)
	c.Header("Content-Type", "text/event-stream")

	w := &automationObserverWriter{ResponseWriter: c.Writer}
	line := "data: {\"type\":\"response.output_text.delta\",\"delta\":\"x \\u003c y \\u0026 z \\u003e 0\"}\n"
	// Warm up ResponseRecorder buffer and contentTypeChecked flag.
	_, err := w.WriteString(line)
	require.NoError(t, err)
	rec.Body.Grow(1 << 20)

	allocs := testing.AllocsPerRun(200, func() {
		_, _ = w.WriteString(line)
	})
	require.Zero(t, allocs)
}

func TestAutomationLineMayMatter_TightenedUnicodeEscapeHandling(t *testing.T) {
	// Common Go HTML/control/non-ASCII escapes must NOT trigger json.Unmarshal fallback.
	for _, benign := range []string{
		`{"type":"response.output_text.delta","delta":"a \u003c b"}`,
		`{"type":"response.output_text.delta","delta":"a \u003e b"}`,
		`{"type":"response.output_text.delta","delta":"a \u0026 b"}`,
		`{"type":"response.output_text.delta","delta":"line\u2028sep\u2029中文\u4e2d\u6587"}`,
		`{"type":"response.output_text.delta","delta":"emoji \ud83d\ude00 and \u0000 ctrl"}`,
		`{"type":"response.output_text.delta","delta":"quote \u0022 inside"}`,
		`{"type":"response.output_text.delta","delta":"escaped backslash \\u003c"}`,
		`{"type":"response.output_text.delta","delta":"truncated \u12"}`,
		`{"type":"response.output_text.delta","delta":"invalid hex \u123g"}`,
	} {
		require.False(t, automationLineMayMatter([]byte(benign)), "benign escape should be skipped: %s", benign)
		checkAutomationLine(t, "data: "+benign)
	}

	// Escapes that can spell verdict literals or case-folded "error" MUST still fall back.
	for _, sensitive := range []string{
		`{"\u0065rror":{"message":"boom"}}`,
		`{"\u0045RROR":{"message":"boom"}}`,
		`{"err\u004Fr":{"message":"boom"}}`,
		`{"err\u006fr":{"message":"boom"}}`,
		`{"type":"message_\u0073top"}`,
		`{"type":"response.\u0066ailed"}`,
		`{"type":"response.\u0063ompleted","response":{"status":"completed"}}`,
		`{"choices":[{"finish_reason":"\u0073top"}]}`,
		`{"candidates":[{"finishReason":"MAX_\u0054OKENS"}]}`,
		`{"delta":"escaped backslash before quote \\","type":"message_stop"}`,
		`{"delta":"odd backslashes \\\u0065","type":"response.output_text.delta"}`,
		`{"delta":"surrogate \uD83D\uDE00 then verdict","choices":[{"finish_reason":"stop"}]}`,
	} {
		require.True(t, automationLineMayMatter([]byte(sensitive)), "verdict escape must not be skipped: %s", sensitive)
		checkAutomationLine(t, "data: "+sensitive)
	}

	// Pin invariant: every byte of every verdict literal (plus case-folded "error")
	// must be covered by automationVerdictEscapeChar.
	allVerdicts := []string{
		"error", "ERROR",
		"response.failed", "response.incomplete", "response.completed",
		"message_stop", "stop", "length", "tool_calls", "function_call",
		"STOP", "MAX_TOKENS",
	}
	for _, literal := range allVerdicts {
		for i := 0; i < len(literal); i++ {
			ch := literal[i]
			require.True(t, ch < 128 && automationVerdictEscapeChar[ch],
				"verdict literal %q byte %q missing from automationVerdictEscapeChar", literal, strings.ToValidUTF8(string(ch), ""))
		}
	}
}
