//go:build unit

package service

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestScheduledVisualReviewUsesExistingOAuthTransport(t *testing.T) {
	c, recorder := newTestContext()
	ctx := context.WithValue(c.Request.Context(), scheduledVisualReviewKey{}, []scheduledVisualFrame{{Time: 0, PNG: "AAAA"}})
	c.Request = c.Request.WithContext(ctx)
	verdict := `{"checks":{"pelican":true,"bicycle":true,"riding":false,"motion":false}}`
	delta, err := json.Marshal(map[string]any{"type": "response.output_text.delta", "delta": verdict})
	require.NoError(t, err)
	resp := newJSONResponse(http.StatusOK, "data: "+string(delta)+"\n\ndata: {\"type\":\"response.completed\"}\n\n")
	resp.Header.Set("x-codex-primary-used-percent", "88")
	repo := &openAIAccountTestRepo{}
	upstream := &queuedHTTPUpstream{responses: []*http.Response{resp}}
	svc := &AccountTestService{accountRepo: repo, httpUpstream: upstream}
	account := &Account{ID: 1, Platform: PlatformOpenAI, Type: AccountTypeOAuth, Credentials: map[string]any{"access_token": "fixture-token"}}
	require.NoError(t, svc.testOpenAIAccountConnection(c, account, "gpt-5.4", scheduledVisualReviewPrompt, AccountTestModeDefault))
	require.Len(t, upstream.requests, 1)
	request := upstream.requests[0]
	require.Equal(t, "Bearer fixture-token", request.Header.Get("Authorization"))
	raw, err := io.ReadAll(request.Body)
	require.NoError(t, err)
	require.Contains(t, string(raw), `"type":"input_image"`)
	require.Contains(t, string(raw), "data:image/png;base64,AAAA")
	require.NotContains(t, string(raw), "max_output_tokens")
	text, upstreamError := parseTestSSEOutput(recorder.Body.String())
	require.Empty(t, upstreamError)
	status, _ := parseScheduledVisualReview(text)
	require.Equal(t, "degraded", status)
	require.Empty(t, repo.updatedExtra, "visual verdict must not rewrite quota state")
}

func TestScheduledVisualReviewUpstreamFailureDoesNotDisableAccount(t *testing.T) {
	for _, code := range []int{401, 429, 500, 502} {
		for _, chat := range []bool{false, true} {
			c, recorder := newTestContext()
			ctx := context.WithValue(c.Request.Context(), scheduledVisualReviewKey{}, []scheduledVisualFrame{{Time: 0, PNG: "AAAA"}})
			c.Request = c.Request.WithContext(ctx)
			repo := &openAIAccountTestRepo{}
			upstream := &queuedHTTPUpstream{responses: []*http.Response{newJSONResponse(code, "fixture upstream failure")}}
			svc := &AccountTestService{accountRepo: repo, httpUpstream: upstream}
			account := &Account{ID: 1, Platform: PlatformOpenAI, Type: AccountTypeOAuth, Credentials: map[string]any{"access_token": "fixture-token"}}
			var err error
			if chat {
				err = svc.testOpenAIChatCompletionsConnection(c, account, "gpt-5.4", scheduledVisualReviewPrompt, "https://example.test", "fixture-token")
			} else {
				err = svc.testOpenAIAccountConnection(c, account, "gpt-5.4", scheduledVisualReviewPrompt, AccountTestModeDefault)
			}
			require.Error(t, err)
			require.Zero(t, repo.setErrorID)
			require.Zero(t, repo.rateLimitedID)
			_, message := parseTestSSEOutput(recorder.Body.String())
			require.True(t, strings.Contains(message, "fixture upstream failure"))
		}
	}
}
