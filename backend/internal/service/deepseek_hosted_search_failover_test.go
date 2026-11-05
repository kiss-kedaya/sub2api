package service

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/tlsfingerprint"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

// hostedSearchDialFailUpstream fails selected model calls the way a dial timeout does and hands everything
// else (the remaining model calls and the search/fetch tools) to the ordinary routed upstream. The delegate
// only counts the calls it serves, so its canned responses line up with the calls that are not failed.
type hostedSearchDialFailUpstream struct {
	inner      *hostedSearchUpstream
	failModel  map[int]error
	modelCalls int
	beforeFail func()
}

func (u *hostedSearchDialFailUpstream) Do(req *http.Request, proxy string, id int64, concurrency int) (*http.Response, error) {
	if !strings.HasSuffix(req.URL.Path, "/tools/search") && !strings.HasSuffix(req.URL.Path, "/tools/fetch") {
		index := u.modelCalls
		u.modelCalls++
		if err, ok := u.failModel[index]; ok {
			if u.beforeFail != nil {
				u.beforeFail()
			}
			return nil, err
		}
	}
	return u.inner.Do(req, proxy, id, concurrency)
}

func (u *hostedSearchDialFailUpstream) DoWithTLS(req *http.Request, proxy string, id int64, concurrency int, _ *tlsfingerprint.Profile) (*http.Response, error) {
	return u.Do(req, proxy, id, concurrency)
}

// waitHostedSearchKeepaliveCommitted blocks until the heartbeat has written the 200 header, which is what
// happens in production while a slow upstream is still being dialed.
func waitHostedSearchKeepaliveCommitted(t *testing.T, c *gin.Context) {
	t.Helper()
	value, ok := c.Get(openAICompactSSEKeepaliveKey)
	require.True(t, ok, "the heartbeat must be installed")
	keepalive, ok := value.(*openAICompactSSEKeepalive)
	require.True(t, ok)
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		keepalive.mu.Lock()
		started := keepalive.started
		keepalive.mu.Unlock()
		if started {
			return
		}
		time.Sleep(2 * time.Millisecond)
	}
	t.Fatal("the heartbeat never committed the SSE header")
}

// A transport failure on the first upstream attempt, before anything reached the client, has to come back
// as the failover error so the handler can retry on a healthy account. Two things used to swallow it: the
// empty round writer made finish() report a synthetic "no Responses response" error that was checked before
// the forward error, and for streams the heartbeat bytes made Written() true, so fail() wrote response.failed
// and replaced the error with a plain one.
func TestDeepSeekHostedSearchFirstRoundTransportFailureFailsOver(t *testing.T) {
	for _, stream := range []bool{false, true} {
		t.Run(fmt.Sprintf("stream=%t", stream), func(t *testing.T) {
			dialErr := errors.New(`Post "https://api.deepseek.com/responses": dial tcp 192.0.2.1:443: i/o timeout`)
			upstream := &hostedSearchDialFailUpstream{inner: &hostedSearchUpstream{}, failModel: map[int]error{0: dialErr}}
			svc, c, recorder, account, body := hostedSearchFixture(t, &hostedSearchUpstream{}, stream, "auto", false)
			svc.httpUpstream = upstream
			stop := func() {}
			if stream {
				stop = StartOpenAIStreamSSEKeepalive(c, 5*time.Millisecond)
				upstream.beforeFail = func() { waitHostedSearchKeepaliveCommitted(t, c) }
			}
			writtenBefore := OpenAICompactKeepaliveAdjustedWrittenSize(c)

			result, err := svc.Forward(context.Background(), c, account, body)
			stop()

			var failover *UpstreamFailoverError
			require.True(t, errors.As(err, &failover), "a transport failure before any output must stay failover-eligible, got %v", err)
			require.Equal(t, http.StatusBadGateway, failover.StatusCode)
			if result != nil {
				require.Zero(t, result.Usage.InputTokens+result.Usage.OutputTokens, "nothing was produced, so nothing may be billed")
			}
			require.Equal(t, writtenBefore, OpenAICompactKeepaliveAdjustedWrittenSize(c),
				"only heartbeats may have reached the client, otherwise the handler refuses to fail over")
			require.NotContains(t, recorder.Body.String(), "response.failed")
			require.Empty(t, strings.ReplaceAll(recorder.Body.String(), ": keepalive\n\n", ""))
			if stream {
				require.NotZero(t, recorder.Body.Len(), "the heartbeat must have committed the SSE response; that is the case this test exists for")
			}
		})
	}
}

// Once a search has run (and been counted), replaying the request on another account would run and bill it
// again, so a later failure keeps ending the response in-stream with a plain error.
func TestDeepSeekHostedSearchFailureAfterSearchStillEndsTheResponse(t *testing.T) {
	dialErr := errors.New(`Post "https://api.deepseek.com/responses": dial tcp 192.0.2.1:443: i/o timeout`)
	inner := &hostedSearchUpstream{
		modelResponses: [][]byte{hostedSearchResponse("resp_search", 10, 2, hostedSearchCall())},
		searchResponse: `{"results":[{"title":"DeepSeek docs","url":"https://example.com/search","summary":"Structured web results"}]}`,
	}
	upstream := &hostedSearchDialFailUpstream{inner: inner, failModel: map[int]error{1: dialErr}}
	svc, c, recorder, account, body := hostedSearchFixture(t, inner, true, "auto", false)
	svc.httpUpstream = upstream

	result, err := svc.Forward(context.Background(), c, account, body)

	require.Error(t, err)
	var failover *UpstreamFailoverError
	require.False(t, errors.As(err, &failover), "a search already ran; failing over would run and bill it twice")
	require.Contains(t, recorder.Body.String(), "response.failed")
	require.NotNil(t, result)
	require.Equal(t, 1, result.SearchCount)
	require.Len(t, inner.searchRequests, 1)
}
