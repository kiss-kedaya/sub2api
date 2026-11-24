package service

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

const queryAllowBareUpstream = "https://upstream.example/v1/messages"

// queryAllowCtx is the inbound request the bridge reads its query string from.
func queryAllowCtx(rawQuery string) *gin.Context {
	gin.SetMode(gin.TestMode)
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses?"+rawQuery, nil)
	c.Request.Header.Set("Content-Type", "application/json")
	return c
}

func queryAllowUpstreamRequest(t *testing.T, rawURL string) *http.Request {
	t.Helper()
	req, err := http.NewRequest(http.MethodPost, rawURL, nil)
	require.NoError(t, err)
	return req
}

func TestMergeInboundQueryParams_Allowlist(t *testing.T) {
	long64 := strings.Repeat("a", maxForwardedQueryValueLen)
	long65 := strings.Repeat("a", maxForwardedQueryValueLen+1)
	// withJunkKeys returns a query string holding first plus n distinct junk keys.
	withJunkKeys := func(first string, n int) string {
		parts := []string{first}
		for i := 0; i < n; i++ {
			parts = append(parts, fmt.Sprintf("k%d=v", i))
		}
		return strings.Join(parts, "&")
	}

	tests := []struct {
		name     string
		upstream string
		inbound  string
		want     string // expected RawQuery of the upstream request afterwards
	}{
		{name: "beta reaches an upstream URL without a query", upstream: queryAllowBareUpstream, inbound: "beta=true", want: "beta=true"},
		{name: "keys outside the allow list are dropped", upstream: queryAllowBareUpstream, inbound: "foo=bar&alt=sse&idempotency_key=1&client_request_id=r1&proxy=http%3A%2F%2Fp%3A1", want: ""},
		{
			name: "credential-like keys are dropped", upstream: queryAllowBareUpstream, want: "",
			inbound: "access_token=a&token=b&key=c&api_key=d&apikey=e&x-api-key=f&Authorization=g&auth=h&id_token=i&refresh_token=j&secret=k&password=l&signature=m",
		},
		{name: "only the allow-listed key survives a mixed query", upstream: queryAllowBareUpstream, inbound: "beta=true&alt=sse&access_token=x&foo=bar", want: "beta=true"},
		{name: "an upstream beta is not overridden", upstream: queryAllowBareUpstream + "?beta=true", inbound: "beta=false", want: "beta=true"},
		{
			name: "the existing query stays byte-for-byte and the new key is appended", want: "tenant=a%20b&proxy=http%3A%2F%2Fp%3A1&beta=true",
			upstream: queryAllowBareUpstream + "?tenant=a%20b&proxy=http%3A%2F%2Fp%3A1", inbound: "beta=true&foo=bar",
		},
		{name: "a repeated key forwards its first value only", upstream: queryAllowBareUpstream, inbound: "beta=true&beta=false", want: "beta=true"},
		{name: "keys are case-sensitive", upstream: queryAllowBareUpstream, inbound: "Beta=true&BETA=true", want: ""},
		{name: "a value at the length limit is forwarded", upstream: queryAllowBareUpstream, inbound: "beta=" + long64, want: "beta=" + long64},
		{name: "a value over the length limit is dropped", upstream: queryAllowBareUpstream, inbound: "beta=" + long65, want: ""},
		{name: "a key count at the limit is forwarded", upstream: queryAllowBareUpstream, inbound: withJunkKeys("beta=true", maxInboundQueryKeys-1), want: "beta=true"},
		{name: "a key count over the limit forwards nothing", upstream: queryAllowBareUpstream, inbound: withJunkKeys("beta=true", maxInboundQueryKeys), want: ""},
		{name: "no inbound query leaves the upstream URL untouched", upstream: queryAllowBareUpstream + "?beta=true", inbound: "", want: "beta=true"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := queryAllowUpstreamRequest(t, tt.upstream)
			mergeInboundQueryParams(req, queryAllowCtx(tt.inbound))
			require.Equal(t, tt.want, req.URL.RawQuery)
		})
	}

	t.Run("nil inputs are ignored", func(t *testing.T) {
		require.NotPanics(t, func() {
			mergeInboundQueryParams(nil, nil)
			mergeInboundQueryParams(queryAllowUpstreamRequest(t, queryAllowBareUpstream), nil)
		})
	})
}

// The deny list holds even when the allow list is widened by mistake: a benign
// key added to the allow list is forwarded, credential-like keys added next to it
// are not.
func TestMergeInboundQueryParams_DenyListBeatsAllowList(t *testing.T) {
	saved := forwardedInboundQueryKeys
	forwardedInboundQueryKeys = map[string]struct{}{}
	for _, key := range []string{"beta", "trace", "access_token", "token", "key", "api_key", "Authorization", "X-Api-Key", "id-token", "client_secret"} {
		forwardedInboundQueryKeys[key] = struct{}{}
	}
	t.Cleanup(func() { forwardedInboundQueryKeys = saved })

	req := queryAllowUpstreamRequest(t, queryAllowBareUpstream)
	mergeInboundQueryParams(req, queryAllowCtx("trace=t1&access_token=a&token=b&key=c&api_key=d&Authorization=e&X-Api-Key=f&id-token=g&client_secret=h"))

	require.Equal(t, "trace=t1", req.URL.RawQuery)
}

func queryAllowAnthropicSSE() *http.Response {
	return &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": {"text/event-stream"}, "X-Request-Id": {"query-allow-test"}},
		Body: io.NopCloser(strings.NewReader(strings.Join([]string{
			`event: message_start`,
			`data: {"type":"message_start","message":{"id":"msg_q","type":"message","role":"assistant","content":[],"model":"claude-opus-4-8","usage":{"input_tokens":3}}}`,
			``,
			`event: content_block_start`,
			`data: {"type":"content_block_start","index":0,"content_block":{"type":"text","text":""}}`,
			``,
			`event: content_block_delta`,
			`data: {"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"ok"}}`,
			``,
			`event: content_block_stop`,
			`data: {"type":"content_block_stop","index":0}`,
			``,
			`event: message_delta`,
			`data: {"type":"message_delta","delta":{"stop_reason":"end_turn"},"usage":{"output_tokens":2}}`,
			``,
			`event: message_stop`,
			`data: {"type":"message_stop"}`,
			``, ``,
		}, "\n"))),
	}
}

// queryAllowTokenCache hands the service-account token provider a ready token so a
// Vertex account can go through ForwardAsResponses without any token exchange.
type queryAllowTokenCache struct{}

func (queryAllowTokenCache) GetAccessToken(context.Context, string) (string, error) {
	return "stub-vertex-token", nil
}
func (queryAllowTokenCache) SetAccessToken(context.Context, string, string, time.Duration) error {
	return nil
}
func (queryAllowTokenCache) DeleteAccessToken(context.Context, string) error { return nil }
func (queryAllowTokenCache) AcquireRefreshLock(context.Context, string, time.Duration) (bool, error) {
	return true, nil
}
func (queryAllowTokenCache) ReleaseRefreshLock(context.Context, string) error { return nil }

func queryAllowVertexAccount() *Account {
	return &Account{
		ID: 7301, Platform: PlatformAnthropic, Type: AccountTypeServiceAccount,
		Concurrency: 1, Status: StatusActive, Schedulable: true,
		Credentials: map[string]any{
			"project_id": "vertex-proj",
			"location":   "us-east5",
			"service_account_json": map[string]any{
				"type": "service_account", "project_id": "vertex-proj",
				"client_email": "svc@vertex-proj.example", "private_key": "placeholder",
			},
		},
	}
}

// forwardAsResponsesUpstreamRequest drives ForwardAsResponses against a recording
// upstream and returns the request that went out.
func forwardAsResponsesUpstreamRequest(t *testing.T, account *Account, inboundQuery string) *http.Request {
	t.Helper()
	cfg := &config.Config{Gateway: config.GatewayConfig{MaxLineSize: defaultMaxLineSize}}
	upstream := &anthropicHTTPUpstreamRecorder{resp: queryAllowAnthropicSSE()}
	svc := &GatewayService{
		cfg: cfg, httpUpstream: upstream, responseHeaderFilter: compileResponseHeaderFilter(cfg),
		claudeTokenProvider: NewClaudeTokenProvider(nil, queryAllowTokenCache{}, nil),
	}
	body := []byte(`{"model":"claude-opus-4-8","input":"hi","stream":false}`)
	_, err := svc.ForwardAsResponses(context.Background(), queryAllowCtx(inboundQuery), account, body,
		&ParsedRequest{Body: NewRequestBodyRef(body), Model: "claude-opus-4-8"})
	require.NoError(t, err)
	require.NotNil(t, upstream.lastReq, "the request must reach the upstream")
	return upstream.lastReq
}

// Anthropic URL: the gateway-built ?beta=true stays as is; nothing a client puts
// on /v1/responses can add or change a parameter.
func TestForwardAsResponses_InboundQueryCannotSteerAnthropicURL(t *testing.T) {
	req := forwardAsResponsesUpstreamRequest(t, newAnthropicAPIKeyAccountForTest(),
		"beta=false&alt=sse&access_token=tok&token=t&key=k&foo=bar&idempotency_key=1")

	require.Equal(t, "https://api.anthropic.com/v1/messages?beta=true", req.URL.String())
}

// Vertex URL: built without a query. Only the allow-listed beta may be added;
// alt, access_token and every other key never reach it.
func TestForwardAsResponses_InboundQueryOnVertexURL(t *testing.T) {
	t.Run("beta is forwarded, the rest is dropped", func(t *testing.T) {
		req := forwardAsResponsesUpstreamRequest(t, queryAllowVertexAccount(),
			"beta=true&alt=sse&access_token=tok&token=t&key=k&foo=bar")

		require.Equal(t, "us-east5-aiplatform.googleapis.com", req.URL.Host)
		require.True(t, strings.HasSuffix(req.URL.Path, ":streamRawPredict"), req.URL.Path)
		require.Equal(t, "beta=true", req.URL.RawQuery)
	})
	t.Run("without an allow-listed key the URL keeps no query at all", func(t *testing.T) {
		req := forwardAsResponsesUpstreamRequest(t, queryAllowVertexAccount(),
			"alt=sse&access_token=tok&token=t&key=k&foo=bar")

		require.Equal(t, "us-east5-aiplatform.googleapis.com", req.URL.Host)
		require.Empty(t, req.URL.RawQuery)
	})
}
