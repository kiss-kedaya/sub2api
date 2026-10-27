package service

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/tlsfingerprint"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

type excelBPSForwardResult struct {
	result *OpenAIForwardResult
	err    error
}

type excelBPSUpstreamFunc func(*http.Request) (*http.Response, error)

func (f excelBPSUpstreamFunc) Do(req *http.Request, _ string, _ int64, _ int) (*http.Response, error) {
	return f(req)
}

func (f excelBPSUpstreamFunc) DoWithTLS(req *http.Request, _ string, _ int64, _ int, _ *tlsfingerprint.Profile) (*http.Response, error) {
	return f(req)
}

func excelBPSGatewayFixture(t *testing.T, upstream HTTPUpstream) (*OpenAIGatewayService, *Account) {
	t.Helper()
	grants, repo, _, accounts := fixtureExcelGrant(t)
	grant := excelGrantTokens{AccessToken: "independent-excel-token", AccountID: "workspace-fixture", ExpiresAt: time.Now().Add(time.Hour).Unix()}
	raw, err := json.Marshal(grant)
	require.NoError(t, err)
	repo.record.Cipher, err = grants.encryptor.Encrypt(string(raw))
	require.NoError(t, err)
	accounts.account.Extra = map[string]any{"excel_bps_enabled": true, "excel_bps_models": []string{"gpt-fixture"}}
	svc := openAIClientToolsTestService(nil)
	svc.httpUpstream = upstream
	svc.SetExcelBPSChannel(NewExcelBPSChannel(grants, &excelBPSFixtureState{}))
	return svc, &accounts.account
}

func TestExcelBPSGatewayCancelledClientStillRecordsAcceptedUsage(t *testing.T) {
	gin.SetMode(gin.TestMode)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	arrived := make(chan struct{})
	upstreamCancelled := make(chan bool, 1)
	upstream := excelBPSUpstreamFunc(func(req *http.Request) (*http.Response, error) {
		close(arrived)
		reader, writer := io.Pipe()
		go func() {
			defer func() { _ = writer.Close() }()
			<-ctx.Done()
			upstreamCancelled <- req.Context().Err() != nil
			_, _ = io.WriteString(writer, "data: {\"type\":\"response.completed\",\"response\":{\"id\":\"resp_excel\",\"status\":\"completed\",\"output\":[],\"usage\":{\"input_tokens\":13,\"output_tokens\":4}}}\n\n")
		}()
		return &http.Response{StatusCode: http.StatusOK, Header: http.Header{"Content-Type": {"text/event-stream"}}, Body: reader}, nil
	})
	svc, account := excelBPSGatewayFixture(t, upstream)
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	body := []byte(`{"model":"gpt-fixture","stream":true,"prompt_cache_key":"conversation-one","input":"hello"}`)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", bytes.NewReader(body)).WithContext(ctx)
	c.Set("api_key", &APIKey{ID: 8})
	done := make(chan excelBPSForwardResult, 1)
	go func() {
		result, handled, err := svc.forwardSelectedExcelBPS(ctx, c, account, body, time.Now(), "gpt-fixture")
		if !handled && err == nil {
			err = io.ErrUnexpectedEOF
		}
		done <- excelBPSForwardResult{result: result, err: err}
	}()
	select {
	case <-arrived:
	case <-time.After(2 * time.Second):
		t.Fatal("BPS request did not reach upstream")
	}
	cancel()
	select {
	case got := <-done:
		require.NoError(t, got.err)
		require.NotNil(t, got.result)
		require.True(t, got.result.ClientDisconnect)
		require.Equal(t, 13, got.result.Usage.InputTokens)
		require.Equal(t, 4, got.result.Usage.OutputTokens)
		require.False(t, <-upstreamCancelled)
	case <-time.After(2 * time.Second):
		t.Fatal("accepted BPS usage did not drain")
	}
}

func TestExcelBPSGatewayBufferedFailedTurnKeepsUsageAndCannotFailover(t *testing.T) {
	gin.SetMode(gin.TestMode)
	wire := "event: response.failed\ndata: {\"type\":\"response.failed\",\"response\":{\"id\":\"resp_excel_failed\",\"status\":\"failed\",\"output\":[],\"usage\":{\"input_tokens\":17,\"output_tokens\":2},\"error\":{\"code\":\"server_error\",\"message\":\"temporary failure\"}}}\n\n"
	upstream := &httpUpstreamRecorder{resp: &http.Response{StatusCode: http.StatusOK, Header: http.Header{"Content-Type": {"text/event-stream"}}, Body: io.NopCloser(strings.NewReader(wire))}}
	svc, account := excelBPSGatewayFixture(t, upstream)
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	body := []byte(`{"model":"gpt-fixture","stream":false,"prompt_cache_key":"conversation-one","input":"hello"}`)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", bytes.NewReader(body))
	c.Set("api_key", &APIKey{ID: 8})
	result, handled, err := svc.forwardSelectedExcelBPS(c.Request.Context(), c, account, body, time.Now(), "gpt-fixture")
	require.True(t, handled)
	require.Error(t, err)
	var failover *UpstreamFailoverError
	require.NotErrorAs(t, err, &failover)
	require.NotNil(t, result)
	require.Equal(t, 17, result.Usage.InputTokens)
	require.Equal(t, 2, result.Usage.OutputTokens)
	require.Len(t, upstream.requests, 1)
	require.Equal(t, "Bearer independent-excel-token", upstream.lastReq.Header.Get("Authorization"))
	require.Equal(t, "/basispoints/api/responses", upstream.lastReq.URL.Path)
}

func TestExcelBPSUsageTapHandlesSplitFrames(t *testing.T) {
	wire := "data: {\"type\":\"response.failed\",\"response\":{\"usage\":{\"input_tokens\":19,\"output_tokens\":3}}}\n\n"
	svc := &OpenAIGatewayService{}
	tap := &excelBPSUsageBody{ReadCloser: io.NopCloser(strings.NewReader(wire)), parse: svc.parseSSEUsageBytesWithType}
	for {
		var b [1]byte
		_, err := tap.Read(b[:])
		if err != nil {
			require.ErrorIs(t, err, io.EOF)
			break
		}
	}
	require.Equal(t, 19, tap.usage.InputTokens)
	require.Equal(t, 3, tap.usage.OutputTokens)
}
