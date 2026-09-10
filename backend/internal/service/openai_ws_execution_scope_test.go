package service

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func newOpenAIWSExecutionScopeTestContext(headers map[string]string) *gin.Context {
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodGet, "/v1/responses", nil)
	for k, v := range headers {
		c.Request.Header.Set(k, v)
	}
	return c
}

func TestResolveOpenAIWSClientThreadID(t *testing.T) {
	gin.SetMode(gin.TestMode)
	cases := []struct {
		name    string
		headers map[string]string
		body    string
		want    string
	}{
		{
			name: "thread-id header wins",
			headers: map[string]string{
				openAIWSThreadIDHeader:     " t-plain ",
				openAIWSTurnMetadataHeader: `{"session_id":"s","thread_id":"t-header"}`,
				openAIWSWindowIDHeader:     "t-window:0",
			},
			body: `{"client_metadata":{"thread_id":"t-body"}}`,
			want: "t-plain",
		},
		{
			name: "turn metadata header second",
			headers: map[string]string{
				openAIWSTurnMetadataHeader: `{"session_id":"s","thread_id":"t-header"}`,
				openAIWSWindowIDHeader:     "t-window:0",
			},
			body: `{"client_metadata":{"thread_id":"t-body"}}`,
			want: "t-header",
		},
		{
			name:    "window id fallback",
			headers: map[string]string{openAIWSWindowIDHeader: " t-window:0 "},
			body:    `{"client_metadata":{"thread_id":"t-body"}}`,
			want:    "t-window",
		},
		{
			name: "body client_metadata.thread_id",
			body: `{"client_metadata":{"thread_id":"t-body"}}`,
			want: "t-body",
		},
		{
			name: "body embedded turn metadata",
			body: `{"client_metadata":{"x-codex-turn-metadata":"{\"thread_id\":\"t-embedded\"}"}}`,
			want: "t-embedded",
		},
		{
			name:    "malformed header ignored",
			headers: map[string]string{openAIWSTurnMetadataHeader: "{not json"},
			body:    `{}`,
			want:    "",
		},
		{
			name: "none",
			body: `{"input":"hi"}`,
			want: "",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := resolveOpenAIWSClientThreadID(newOpenAIWSExecutionScopeTestContext(tc.headers), []byte(tc.body))
			require.Equal(t, tc.want, got)
		})
	}
	require.Equal(t, "t", resolveOpenAIWSClientThreadID(nil, []byte(`{"client_metadata":{"thread_id":"t"}}`)))
}

func TestResolveOpenAIWSExecutionScope(t *testing.T) {
	gin.SetMode(gin.TestMode)
	threadHeaders := map[string]string{
		"session-id":               "root-session",
		openAIWSTurnMetadataHeader: `{"session_id":"root-session","thread_id":"thread-a"}`,
	}
	bodyFirst := []byte(`{"type":"response.create","model":"gpt-5.1","instructions":"sys","input":[{"role":"user","content":"first"}]}`)
	bodyRetry := []byte(`{"type":"response.create","model":"gpt-5.1","instructions":"sys","input":[{"role":"user","content":"first"},{"role":"assistant","content":"x"},{"role":"user","content":"second"}]}`)

	scopeOf := func(headers map[string]string, body []byte, apiKeyID int64) string {
		scope, _ := resolveOpenAIWSExecutionScope(newOpenAIWSExecutionScopeTestContext(headers), body, apiKeyID)
		return scope
	}

	byThread, threadID := resolveOpenAIWSExecutionScope(newOpenAIWSExecutionScopeTestContext(threadHeaders), bodyFirst, 11)
	require.Len(t, byThread, 16)
	require.Equal(t, "thread-a", threadID)
	require.Equal(t, byThread, scopeOf(threadHeaders, bodyRetry, 11), "同线程重连的作用域不得随请求内容变化")

	otherThread := map[string]string{
		"session-id":               "root-session",
		openAIWSTurnMetadataHeader: `{"session_id":"root-session","thread_id":"thread-b"}`,
	}
	require.NotEqual(t, byThread, scopeOf(otherThread, bodyFirst, 11))
	require.NotEqual(t, byThread, scopeOf(threadHeaders, bodyFirst, 12), "API key 必须参与隔离")

	sessionOnly := map[string]string{"session-id": "root-session"}
	bySession, threadID := resolveOpenAIWSExecutionScope(newOpenAIWSExecutionScopeTestContext(sessionOnly), bodyFirst, 11)
	require.Len(t, bySession, 16)
	require.Equal(t, "", threadID)
	require.NotEqual(t, byThread, bySession, "线程标识优先于会话标识")
	require.Equal(t, bySession, scopeOf(sessionOnly, bodyRetry, 11))
	require.NotEqual(t, bySession, scopeOf(sessionOnly, bodyFirst, 12))

	byCacheKey := scopeOf(nil, []byte(`{"type":"response.create","prompt_cache_key":"cache-1","input":"hi"}`), 11)
	require.Len(t, byCacheKey, 16)

	require.Equal(t, "", scopeOf(nil, bodyFirst, 11), "只有请求内容时不得产生执行作用域")
	nilScope, nilThread := resolveOpenAIWSExecutionScope(nil, bodyFirst, 11)
	require.Equal(t, "", nilScope)
	require.Equal(t, "", nilThread)
}
