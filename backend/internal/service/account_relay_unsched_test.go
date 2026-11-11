//go:build unit

package service

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// relayUpstreamAPIKey is an API-key account whose base_url is a relay/aggregator
// site rather than the vendor's own API.
func relayUpstreamAPIKey(id int64, base string) *Account {
	return &Account{
		ID:       id,
		Platform: PlatformOpenAI,
		Type:     AccountTypeAPIKey,
		Status:   StatusActive,
		Credentials: map[string]any{
			"api_key":  "sk-test",
			"base_url": base,
			// A rule that would stop scheduling if the gateway were allowed to apply it.
			"temp_unschedulable_enabled": true,
			"temp_unschedulable_rules": []any{
				map[string]any{"error_code": float64(429), "duration_minutes": float64(10)},
				map[string]any{"error_code": float64(503), "duration_minutes": float64(10)},
			},
		},
	}
}

func TestAccountIsRelayAPIKeyAccount(t *testing.T) {
	for name, tc := range map[string]struct {
		account *Account
		want    bool
	}{
		"relay base url":       {relayUpstreamAPIKey(1, "https://relay.example/v1"), true},
		"aggregator with path": {relayUpstreamAPIKey(2, "https://platform.deepseek.bid/v1"), true},
		"official openai":      {relayUpstreamAPIKey(3, "https://api.openai.com"), false},
		"official anthropic":   {relayUpstreamAPIKey(4, "https://api.anthropic.com"), false},
		"official moonshot":    {relayUpstreamAPIKey(5, "https://api.moonshot.cn/v1"), false},
		"no base url":          {relayUpstreamAPIKey(6, ""), false},
		"oauth account":        {&Account{ID: 7, Type: AccountTypeOAuth, Platform: PlatformOpenAI, Credentials: map[string]any{"base_url": "https://relay.example/v1"}}, false},
		"nil account":          {nil, false},
	} {
		t.Run(name, func(t *testing.T) {
			require.Equal(t, tc.want, tc.account.IsRelayAPIKeyAccount())
			if tc.account == nil {
				require.False(t, tc.account.ShouldAutoTempUnscheduleOnTransportError())
				return
			}
			require.Equal(t, !tc.want, tc.account.ShouldAutoTempUnscheduleOnTransportError())
		})
	}
}

// The gateway's own transport-error heuristics must leave relay API-key accounts
// alone: their upstream is a third-party site whose flapping says nothing about
// the account, and the operator stops them only through the custom error code rule.
func TestTransportErrorTempsUnschedSkipsRelayAPIKey(t *testing.T) {
	repo := &transportTempUnschedRepoStub{}
	gateway := &GatewayService{accountRepo: repo}

	gateway.tempUnscheduleTransportError(context.Background(), relayUpstreamAPIKey(11, "https://relay.example/v1"), "dial tcp 1.2.3.4:443: connect: no route to host")
	require.Zero(t, repo.calls, "a relay API-key account is not temporarily unscheduled by a transport error")

	gateway.tempUnscheduleTransportError(context.Background(), relayUpstreamAPIKey(12, "https://api.openai.com"), "dial tcp 1.2.3.4:443: connect: no route to host")
	require.Equal(t, 1, repo.calls, "an account on the vendor's own API keeps the existing behaviour")
	require.Equal(t, int64(12), repo.lastID)
}

func TestAnthropicTransportErrorTempsUnschedSkipsRelayAPIKey(t *testing.T) {
	repo := &transportTempUnschedRepoStub{}
	gateway := &GatewayService{accountRepo: repo}

	gateway.tempUnscheduleAnthropicTransportError(context.Background(), relayUpstreamAPIKey(21, "https://relay.example/v1"), "boom")
	require.Zero(t, repo.calls)

	gateway.tempUnscheduleAnthropicTransportError(context.Background(), relayUpstreamAPIKey(22, "https://api.anthropic.com"), "boom")
	require.Equal(t, 1, repo.calls)
}

func TestOpenAITransportErrorTempsUnschedSkipsRelayAPIKey(t *testing.T) {
	repo := &openaiTransportAccountRepoStub{}
	gateway := &OpenAIGatewayService{accountRepo: repo}

	gateway.tempUnscheduleOpenAITransportError(context.Background(), relayUpstreamAPIKey(31, "https://relay.example/v1"), "boom")
	require.Empty(t, repo.tempUnschedCalls)

	gateway.tempUnscheduleOpenAITransportError(context.Background(), relayUpstreamAPIKey(32, "https://api.openai.com"), "boom")
	require.Len(t, repo.tempUnschedCalls, 1)
	require.Equal(t, int64(32), repo.tempUnschedCalls[0].accountID)
	require.True(t, repo.tempUnschedCalls[0].until.After(time.Now()))
}

// The status-code rules (429/503 and the rest) take the same decision: no
// automatic temporary unscheduling for relay API-key accounts.
func TestTryTempUnschedulableSkipsRelayAPIKey(t *testing.T) {
	repo := &transportTempUnschedRepoStub{}
	svc := &RateLimitService{accountRepo: repo}

	relay := relayUpstreamAPIKey(41, "https://relay.example/v1")
	require.False(t, svc.tryTempUnschedulable(context.Background(), relay, 429, []byte(`{"error":"rate limited"}`)))
	require.Zero(t, repo.calls, "no rule may stop a relay API-key account automatically")
}

// An account with custom error codes configured also decides by itself: the
// automatic paths, including the transport-error ones that carry no status code,
// must not stop it. The configured code still stops it for 30 days.
func TestCustomErrorCodesSuppressAutomaticTempUnsched(t *testing.T) {
	account := relayUpstreamAPIKey(61, "https://api.openai.com") // vendor API, so only the custom-code rule can explain suppression
	account.Credentials["custom_error_codes_enabled"] = true
	account.Credentials["custom_error_codes"] = []any{float64(429)}

	require.False(t, account.ShouldAutoTempUnscheduleOnTransportError())

	repo := &transportTempUnschedRepoStub{}
	gateway := &GatewayService{accountRepo: repo}
	gateway.tempUnscheduleTransportError(context.Background(), account, "dial tcp: no route to host")
	require.Zero(t, repo.calls, "a transport error must not stop an account that configured its own error codes")

	// The operator's rule still stops it, for the configured duration.
	svc := &RateLimitService{accountRepo: repo}
	require.Equal(t, ErrorPolicyMatched, svc.CheckErrorPolicy(context.Background(), account, 429, []byte(`{"error":"rate limited"}`)))
}

// The operator's own custom error code rule still stops scheduling for a relay
// account: that path is the account's configuration, not a gateway heuristic.
func TestCustomErrorCodesStillStopRelayAPIKey(t *testing.T) {
	relay := relayUpstreamAPIKey(51, "https://relay.example/v1")
	relay.Credentials["custom_error_codes_enabled"] = true
	relay.Credentials["custom_error_codes"] = []any{float64(429)}

	svc := &RateLimitService{accountRepo: &transportTempUnschedRepoStub{}}
	require.Equal(t, ErrorPolicyMatched, svc.CheckErrorPolicy(context.Background(), relay, 429, []byte(`{"error":"rate limited"}`)))
	require.Equal(t, ErrorPolicySkipped, svc.CheckErrorPolicy(context.Background(), relay, 500, []byte(`{"error":"boom"}`)))
}
