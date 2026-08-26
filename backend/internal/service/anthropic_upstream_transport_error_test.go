package service

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

type anthropicTransportErrorRepoStub struct {
	AccountRepository
	calls  int
	until  time.Time
	reason string
}

func (r *anthropicTransportErrorRepoStub) SetTempUnschedulable(_ context.Context, _ int64, until time.Time, reason string) error {
	r.calls++
	r.until = until
	r.reason = reason
	return nil
}

func newAnthropicTransportErrorTestContext() (*gin.Context, *httptest.ResponseRecorder) {
	gin.SetMode(gin.TestMode)
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/messages", nil)
	return c, rec
}

func TestHandleAnthropicUpstreamTransportError_PersistentFailsOverAndUnschedules(t *testing.T) {
	c, rec := newAnthropicTransportErrorTestContext()
	repo := &anthropicTransportErrorRepoStub{}
	svc := &GatewayService{accountRepo: repo}
	account := newAnthropicAPIKeyAccountForTest()
	started := time.Now()

	err := svc.handleAnthropicUpstreamTransportError(
		context.Background(), c, account, "https://api.anthropic.com/v1/messages", false,
		errors.New("dial tcp 203.0.113.7:443: connect: connection refused"),
	)

	var failoverErr *UpstreamFailoverError
	require.ErrorAs(t, err, &failoverErr)
	require.Equal(t, http.StatusBadGateway, failoverErr.StatusCode)
	require.JSONEq(t, string(anthropicTransportFailoverBody), string(failoverErr.ResponseBody))
	require.Equal(t, 1, repo.calls)
	require.Greater(t, repo.until, started.Add(9*time.Minute))
	require.Less(t, repo.until, started.Add(11*time.Minute))
	require.Contains(t, repo.reason, "connection refused")
	require.Equal(t, http.StatusOK, rec.Code)
	require.Empty(t, rec.Body.String())
}

func TestHandleAnthropicUpstreamTransportError_TransientFailsOverWithoutUnschedule(t *testing.T) {
	c, rec := newAnthropicTransportErrorTestContext()
	repo := &anthropicTransportErrorRepoStub{}
	svc := &GatewayService{accountRepo: repo}
	account := newAnthropicAPIKeyAccountForTest()

	err := svc.handleAnthropicUpstreamTransportError(
		context.Background(), c, account, "https://api.anthropic.com/v1/messages", true,
		errors.New("dial tcp timeout"),
	)

	var failoverErr *UpstreamFailoverError
	require.ErrorAs(t, err, &failoverErr)
	require.Zero(t, repo.calls)
	require.Equal(t, http.StatusOK, rec.Code)
	require.Empty(t, rec.Body.String())
}

func TestHandleAnthropicUpstreamTransportError_ContextCanceledDoesNotFailOverOrUnschedule(t *testing.T) {
	c, rec := newAnthropicTransportErrorTestContext()
	repo := &anthropicTransportErrorRepoStub{}
	svc := &GatewayService{accountRepo: repo}
	account := newAnthropicAPIKeyAccountForTest()

	err := svc.handleAnthropicUpstreamTransportError(
		context.Background(), c, account, "https://api.anthropic.com/v1/messages", false,
		context.Canceled,
	)

	var failoverErr *UpstreamFailoverError
	require.Error(t, err)
	require.False(t, errors.As(err, &failoverErr))
	require.Zero(t, repo.calls)
	require.Equal(t, http.StatusOK, rec.Code)
	require.Empty(t, rec.Body.String())
}
