package service

import (
	"context"
	"errors"
	"fmt"
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

// silentUpstream accepts a request and never answers until its context ends,
// like the provider that held 30MB requests for over an hour.
type silentUpstream struct{ calls int }

func (u *silentUpstream) Do(req *http.Request, _ string, _ int64, _ int) (*http.Response, error) {
	u.calls++
	<-req.Context().Done()
	return nil, req.Context().Err()
}

func (u *silentUpstream) DoWithTLS(req *http.Request, proxyURL string, accountID int64, concurrency int, _ *tlsfingerprint.Profile) (*http.Response, error) {
	return u.Do(req, proxyURL, accountID, concurrency)
}

func shortenDeepSeekUpstreamWaitLimit(t *testing.T, limit time.Duration) {
	t.Helper()
	previous := deepSeekUpstreamWaitLimit
	deepSeekUpstreamWaitLimit = limit
	t.Cleanup(func() { deepSeekUpstreamWaitLimit = previous })
}

func waitLimitRequest(t *testing.T, ctx context.Context, profile HTTPUpstreamProfile) *http.Request {
	t.Helper()
	req, err := http.NewRequestWithContext(WithHTTPUpstreamProfile(ctx, profile), http.MethodPost, "http://upstream.test/v1/responses", strings.NewReader("{}"))
	require.NoError(t, err)
	return req
}

func TestOpenAIUpstreamWaitLimitFor(t *testing.T) {
	deepSeek := &Account{ID: 1, Platform: PlatformDeepseek, Type: AccountTypeAPIKey}
	openAI := &Account{ID: 2, Platform: PlatformOpenAI, Type: AccountTypeAPIKey}
	ctx := context.Background()
	for _, tc := range []struct {
		name    string
		account *Account
		profile HTTPUpstreamProfile
		want    time.Duration
	}{
		{"deepseek chat forward", deepSeek, HTTPUpstreamProfileOpenAI, deepSeekUpstreamWaitLimit},
		{"deepseek tools keep their own deadlines", deepSeek, HTTPUpstreamProfileDeepSeekTools, 0},
		{"deepseek search and fetch use no profile", deepSeek, HTTPUpstreamProfileDefault, 0},
		{"openai is untouched", openAI, HTTPUpstreamProfileOpenAI, 0},
		{"no account", nil, HTTPUpstreamProfileOpenAI, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			require.Equal(t, tc.want, openAIUpstreamWaitLimitFor(waitLimitRequest(t, ctx, tc.profile), tc.account))
		})
	}
	require.Zero(t, openAIUpstreamWaitLimitFor(nil, deepSeek))
}

// /v1/messages reaches DeepSeek through the native Anthropic endpoint, which
// is built without the OpenAI profile.
func TestNativeAnthropicRequestCarriesTheWaitLimit(t *testing.T) {
	svc := &OpenAIGatewayService{}
	body := []byte(`{"model":"deepseek-flash","max_tokens":1,"messages":[{"role":"user","content":"hi"}]}`)
	for _, tc := range []struct {
		platform string
		want     time.Duration
	}{
		{PlatformDeepseek, deepSeekUpstreamWaitLimit},
		{PlatformKimi, 0},
	} {
		account := &Account{ID: 1, Platform: tc.platform, Type: AccountTypeAPIKey}
		req, _, err := svc.buildNativeAnthropicUpstreamRequest(context.Background(), nil, account, body, "synthetic-key", "http://upstream.test/v1/messages")
		require.NoError(t, err)
		require.Equal(t, tc.want, openAIUpstreamWaitLimitFor(req, account), tc.platform)
	}
}

func TestDoOpenAIUpstreamEndsSilentDeepSeekRequest(t *testing.T) {
	shortenDeepSeekUpstreamWaitLimit(t, 40*time.Millisecond)
	upstream := &silentUpstream{}
	svc := &OpenAIGatewayService{httpUpstream: upstream}
	account := &Account{ID: 1, Platform: PlatformDeepseek, Type: AccountTypeAPIKey}

	started := time.Now()
	resp, err := svc.doOpenAIUpstream(waitLimitRequest(t, context.Background(), HTTPUpstreamProfileOpenAI), "", account)

	require.Nil(t, resp)
	require.ErrorIs(t, err, errOpenAIUpstreamWaitLimit)
	require.NotErrorIs(t, err, context.Canceled, "the limit must not look like a client disconnect")
	require.Less(t, time.Since(started), 2*time.Second)
	require.Equal(t, 1, upstream.calls)
}

func TestDoOpenAIUpstreamLeavesOtherRequestsWaiting(t *testing.T) {
	shortenDeepSeekUpstreamWaitLimit(t, 20*time.Millisecond)
	for _, tc := range []struct {
		name    string
		account *Account
		profile HTTPUpstreamProfile
	}{
		{"openai account", &Account{ID: 2, Platform: PlatformOpenAI, Type: AccountTypeAPIKey}, HTTPUpstreamProfileOpenAI},
		{"deepseek tool route", &Account{ID: 1, Platform: PlatformDeepseek, Type: AccountTypeAPIKey}, HTTPUpstreamProfileDeepSeekTools},
	} {
		t.Run(tc.name, func(t *testing.T) {
			svc := &OpenAIGatewayService{httpUpstream: &silentUpstream{}}
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			done := make(chan error, 1)
			go func() {
				_, err := svc.doOpenAIUpstream(waitLimitRequest(t, ctx, tc.profile), "", tc.account)
				done <- err
			}()
			select {
			case err := <-done:
				t.Fatalf("request ended on its own: %v", err)
			case <-time.After(150 * time.Millisecond):
			}
			cancel()
			require.ErrorIs(t, <-done, context.Canceled)
		})
	}
}

func TestDoWithUpstreamWaitLimit(t *testing.T) {
	t.Run("a stream that started is never cut", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusOK)
			if flusher, ok := w.(http.Flusher); ok {
				flusher.Flush()
			}
			time.Sleep(600 * time.Millisecond) // well past the limit
			_, _ = io.WriteString(w, "late but complete")
		}))
		defer server.Close()
		req, err := http.NewRequest(http.MethodGet, server.URL, nil)
		require.NoError(t, err)

		// The limit only has to outlast the headers of a local round trip; it is
		// generous so a loaded CI machine does not trip it.
		resp, err := doWithUpstreamWaitLimit(req, 300*time.Millisecond, http.DefaultClient.Do)
		require.NoError(t, err)
		body, err := io.ReadAll(resp.Body)
		require.NoError(t, err)
		require.Equal(t, "late but complete", string(body))
		require.NoError(t, resp.Body.Close())
	})

	t.Run("an upstream error is returned as is", func(t *testing.T) {
		boom := errors.New("dial tcp: connection refused")
		req, err := http.NewRequest(http.MethodGet, "http://upstream.test", nil)
		require.NoError(t, err)
		resp, err := doWithUpstreamWaitLimit(req, time.Hour, func(*http.Request) (*http.Response, error) { return nil, boom })
		require.Nil(t, resp)
		require.ErrorIs(t, err, boom)
		require.NotErrorIs(t, err, errOpenAIUpstreamWaitLimit)
	})

	t.Run("a client that leaves is not reported as the limit", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, "http://upstream.test", nil)
		require.NoError(t, err)
		time.AfterFunc(20*time.Millisecond, cancel)
		_, err = doWithUpstreamWaitLimit(req, time.Hour, func(r *http.Request) (*http.Response, error) {
			<-r.Context().Done()
			return nil, r.Context().Err()
		})
		require.ErrorIs(t, err, context.Canceled)
		require.NotErrorIs(t, err, errOpenAIUpstreamWaitLimit)
	})
}

func TestUpstreamWaitLimitEndsTheRequestWithoutFailover(t *testing.T) {
	gin.SetMode(gin.TestMode)
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", nil)
	account := &Account{ID: 1, Name: "synthetic", Platform: PlatformDeepseek, Type: AccountTypeAPIKey, Status: StatusActive, Schedulable: true}
	svc := &OpenAIGatewayService{}

	err := svc.handleOpenAIUpstreamTransportError(context.Background(), c, account, fmt.Errorf("%w (%s)", errOpenAIUpstreamWaitLimit, 10*time.Minute), false)

	var failover *UpstreamFailoverError
	require.ErrorAs(t, err, &failover)
	require.Equal(t, http.StatusGatewayTimeout, failover.StatusCode)
	require.False(t, failover.ShouldRetryNextAccount(), "another account would hold the same payload for another full limit")
	require.False(t, failover.RetryableOnSameAccount)
	require.True(t, failover.IsUpstreamWaitLimit())
	require.False(t, failover.ShouldReportAccountScheduleFailure(), "a silent provider is not the account's fault")
	require.JSONEq(t, `{"error":{"type":"upstream_error","message":"Upstream did not respond in time"}}`, string(failover.ResponseBody))
	require.Equal(t, http.StatusGatewayTimeout, c.GetInt(OpsUpstreamStatusCodeKey))
	require.Contains(t, c.GetString(OpsUpstreamErrorMessageKey), "10m0s")
	require.True(t, account.Schedulable)
	require.Nil(t, account.TempUnschedulableUntil)
	require.False(t, rec.Flushed)
	require.Zero(t, rec.Body.Len(), "the handler owns the response")
}
