//go:build unit

package service

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestWriteUpstreamFailureDumpCapturesBodiesAndHeaders(t *testing.T) {
	dir := t.TempDir()
	t.Setenv(DebugDumpDirEnv, dir)
	require.True(t, DebugDumpEnabled())

	upstream := httptest.NewRequest(http.MethodPost, "https://relay.example/v1/responses", nil)
	upstream.Header.Set("Authorization", "Bearer sk-testplaceholder0123456789")
	upstream.Header.Set("Content-Type", "application/json")
	resp := &http.Response{
		StatusCode: http.StatusBadRequest,
		Request:    upstream,
		Header:     http.Header{"X-New-Api-Version": []string{"v1.0.0"}},
	}
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", nil)

	WriteUpstreamFailureDump(c, &Account{ID: 4242, Name: "relay", Platform: PlatformDeepseek},
		[]byte(`{"model":"deepseek-flash","input":[]}`), resp, []byte(`{"error":"boom"}`))

	files, err := ListDebugDumps()
	require.NoError(t, err)
	require.Len(t, files, 1)

	body, err := ReadDebugDump(files[0])
	require.NoError(t, err)
	text := string(body)
	require.Contains(t, text, "upstream_status: 400")
	require.Contains(t, text, "https://relay.example/v1/responses")
	require.Contains(t, text, `{"model":"deepseek-flash","input":[]}`)
	require.Contains(t, text, `{"error":"boom"}`)
	require.Contains(t, text, "X-New-Api-Version: v1.0.0")
	// A dump is meant to be shareable: the key must not survive in full.
	require.NotContains(t, text, "testplaceholder0123456789")
	require.Contains(t, text, "***")
}

func TestReadDebugDumpRejectsTraversal(t *testing.T) {
	t.Setenv(DebugDumpDirEnv, t.TempDir())
	_, err := ReadDebugDump("../../etc/passwd")
	require.Error(t, err)
	_, err = ReadDebugDump("passwd")
	require.Error(t, err, "only .txt files are served")
}

func TestListDebugDumpsReportsDisabled(t *testing.T) {
	t.Setenv(DebugDumpDirEnv, "")
	require.False(t, DebugDumpEnabled())
	_, err := ListDebugDumps()
	require.Error(t, err)
	require.Contains(t, err.Error(), DebugDumpDirEnv)
}

func TestWriteUpstreamFailureDumpIsBestEffortWhenDisabled(t *testing.T) {
	t.Setenv(DebugDumpDirEnv, "")
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	// Must not panic or write anywhere.
	WriteUpstreamFailureDump(c, nil, nil, nil, nil)
}

func TestTruncateDumpBody(t *testing.T) {
	small := "hello"
	require.Equal(t, small, truncateDumpBody(small))

	big := make([]byte, debugDumpMaxBody+10)
	for i := range big {
		big[i] = 'x'
	}
	out := truncateDumpBody(string(big))
	require.LessOrEqual(t, len(out), debugDumpMaxBody+80)
	require.Contains(t, out, fmt.Sprintf("%d bytes total", len(big)))
}

func TestCountReasoningItemsSeparatesItemsFromText(t *testing.T) {
	body := []byte(`{"input":[
		{"type":"message","role":"user","content":[{"type":"input_text","text":"q"}]},
		{"type":"reasoning","id":"r1","summary":[{"type":"summary_text","text":"s"}],"content":[{"type":"reasoning_text","text":"real"}]},
		{"type":"reasoning","id":"r2","summary":[],"content":[]},
		{"type":"reasoning","id":"r3","content":[{"type":"reasoning_text","text":"  "}]}
	]}`)
	items, withText := countReasoningItems(body)
	require.Equal(t, 3, items)
	require.Equal(t, 1, withText, "only the item with non-empty reasoning_text replays")
}

func TestRecordDebugStepAppearsInTheDump(t *testing.T) {
	t.Setenv(DebugDumpDirEnv, t.TempDir())
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", nil)
	RecordDebugStep(c, "normalize.out", "bytes=10 reasoning_items=0")

	resp := &http.Response{StatusCode: http.StatusBadRequest, Header: http.Header{}, Request: c.Request}
	WriteUpstreamFailureDump(c, &Account{ID: 4242, Platform: PlatformDeepseek}, []byte(`{}`), resp, []byte(`{}`))

	files, err := ListDebugDumps()
	require.NoError(t, err)
	body, err := ReadDebugDump(files[0])
	require.NoError(t, err)
	require.Contains(t, string(body), "## request path")
	require.Contains(t, string(body), "normalize.out")
	require.Contains(t, string(body), "reasoning_items=0")
}
