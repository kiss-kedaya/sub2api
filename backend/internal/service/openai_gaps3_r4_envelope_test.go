//go:build unit

package service

import (
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

func TestOpenAIR4BufferedNoteExceptionRequiresContent(t *testing.T) {
	for _, content := range []string{`""`, `null`, `{}`, `[{"type":"text","text":""}]`, `[{"type":"text"}]`} {
		t.Run(content, func(t *testing.T) {
			root := gjson.Parse(`{"choices":[{"message":{"content":` + content + `},"finish_reason":"stop"}],"error":{"message":"synthetic rejection"}}`)
			require.True(t, openAIBufferedUpstreamError(root, ""))
		})
	}
}

func TestOpenAIR4BufferedFailureMarkersOverrideRealAnswer(t *testing.T) {
	for _, marker := range []string{`"type":"error",`, `"type":"response.failed",`, `"status":"failed",`, `"response":{"status":"failed"},`} {
		root := gjson.Parse(`{` + marker + `"choices":[{"message":{"content":"synthetic answer"},"finish_reason":"stop"}],"error":{"message":"synthetic rejection"}}`)
		require.True(t, openAIExplicitUpstreamError(root, ""))
		require.True(t, openAIBufferedUpstreamError(root, ""))
	}
	root := gjson.Parse(`{"choices":[{"message":{"content":"synthetic answer"},"finish_reason":"error"}]}`)
	require.True(t, openAIExplicitUpstreamError(root, ""))
	require.True(t, openAIBufferedUpstreamError(root, ""))
	require.True(t, openAIBufferedUpstreamError(gjson.Parse(`{"choices":[{"message":{"content":"synthetic answer"},"finish_reason":"stop"}]}`), "error"))
}

func TestOpenAIR4BufferedCyberKeepsErrorAndBilling(t *testing.T) {
	c, rec := newNonStreamingFailoverContext(t)
	wire := `{"error":{"code":"cyber_policy","message":"synthetic policy rejection"},"usage":{"prompt_tokens":1000,"completion_tokens":2}}`
	result, usage, free, err := newNonStreamingFailoverService().readCCUpstreamJSONResponse(c, explicitErrorResponse(wire, "application/json"), writeChatCompletionsError)
	require.Error(t, err)
	require.NotNil(t, result)
	require.Equal(t, OpenAIUsage{InputTokens: 1000, OutputTokens: 2}, usage)
	require.False(t, free)
	require.GreaterOrEqual(t, rec.Code, http.StatusBadRequest)
}
