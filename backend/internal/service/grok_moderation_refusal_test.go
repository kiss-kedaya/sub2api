package service

import (
	"errors"
	"math"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestIsGrokModerationRefusalFailureRequiresReal403(t *testing.T) {
	body := []byte(`{"error":{"message":"I'm sorry, I can't help with that request. Failed check: SAFETY_CHECK_TYPE"}}`)
	for _, status := range []int{400, 401, 403, 429, 500, 502, 529} {
		err := &UpstreamFailoverError{StatusCode: status, ResponseBody: body}
		require.Equal(t, status == 403, IsGrokModerationRefusalFailure(PlatformGrok, body, err))
	}
	require.False(t, IsGrokModerationRefusalFailure(PlatformGrok, body, nil))
	require.False(t, IsGrokModerationRefusalFailure(PlatformGrok, body, errors.New(string(body))))
	require.True(t, IsGrokModerationRefusalFailure(PlatformGrok, body, errors.New(grokContentPolicyRejectionPrefix+string(body))))
	require.True(t, IsGrokModerationRefusalFailure(PlatformGrok, []byte(`{"status":403,"error":{"message":"I'm sorry, I can't help with that request."}}`), nil))
	require.False(t, IsGrokModerationRefusalFailure(PlatformGrok, []byte(`{"status":500,"error":{"message":"I'm sorry, I can't help with that request."}}`), nil))
	require.False(t, IsGrokModerationRefusalFailure(PlatformOpenAI, body, &UpstreamFailoverError{StatusCode: 403, ResponseBody: body}))
}

func TestGrokAccountSuspensionOverridesRefusalText(t *testing.T) {
	for _, body := range []string{
		`{"error":{"message":"Your account has been suspended. I'm sorry, I can't help with that request. Failed check: SAFETY_CHECK_TYPE"}}`,
		`{"error":{"code":"account_suspended","message":"I'm sorry, I can't help with that request. Failed check: SAFETY_CHECK_TYPE"}}`,
	} {
		require.False(t, IsGrokModerationRefusalResponse(PlatformGrok, http.StatusForbidden, []byte(body)), body)
		require.False(t, IsGrokModerationRefusalFailure(PlatformGrok, nil, &UpstreamFailoverError{
			StatusCode: http.StatusForbidden, ResponseBody: []byte(body),
		}), body)
	}
}

func TestGrokModerationRefusalProtocolStatusRequiresIntegerJSONNumber(t *testing.T) {
	tests := []struct {
		name string
		body string
		want int
	}{
		{name: "integer status", body: `{"status":403}`, want: http.StatusForbidden},
		{name: "nested integer status", body: `{"response":{"error":{"status_code":403}}}`, want: http.StatusForbidden},
		{name: "string status", body: `{"status":"403"}`},
		{name: "fractional status", body: `{"status":403.0}`},
		{name: "exponent status", body: `{"status":4.03e2}`},
		{name: "boolean status", body: `{"status":true}`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			require.Equal(t, tt.want, grokModerationRefusalProtocolStatus([]byte(tt.body)))
		})
	}
	for _, body := range []string{
		`{"status":"403","error":{"message":"text is sensitive"}}`,
		`{"status":403.0,"error":{"message":"text is sensitive"}}`,
		`{"status":4.03e2,"error":{"message":"text is sensitive"}}`,
	} {
		require.False(t, IsGrokModerationRefusalFailure(PlatformGrok, []byte(body), nil), body)
	}
}

func TestIsGrokModerationRefusal(t *testing.T) {
	if !IsGrokModerationRefusal(PlatformGrok, []byte(`status_code=403, I'm sorry, I can't help with that request.`)) {
		t.Fatal("expected Grok refusal to match")
	}
	if !IsGrokModerationRefusal(PlatformGrok, []byte(`I'M SORRY, I CAN'T HELP WITH THAT REQUEST.`)) {
		t.Fatal("expected case-insensitive refusal to match")
	}
	if IsGrokModerationRefusal(PlatformGrok, []byte(`I'm sorry`)) {
		t.Fatal("incomplete refusal must not match")
	}
	if IsGrokModerationRefusal(PlatformGrok, []byte(`I'm sorry, I can't help`)) {
		t.Fatal("truncated refusal must not match")
	}
	if IsGrokModerationRefusal(PlatformOpenAI, []byte(`I'm sorry`)) {
		t.Fatal("non-Grok refusal must not match")
	}
	if IsGrokModerationRefusal(PlatformGrok, []byte(`upstream error`)) {
		t.Fatal("ordinary upstream error must not match")
	}
}

func TestIsGrokModerationRefusalResponseRequiresHTTP403(t *testing.T) {
	body := []byte(`{"error":{"message":"text is sensitive"}}`)
	require.True(t, IsGrokModerationRefusalResponse(PlatformGrok, http.StatusForbidden, body))
	require.False(t, IsGrokModerationRefusalResponse(PlatformGrok, http.StatusTooManyRequests, body))
	require.False(t, IsGrokModerationRefusalResponse(PlatformGrok, http.StatusInternalServerError, body))
	require.False(t, IsGrokModerationRefusalResponse(PlatformOpenAI, http.StatusForbidden, body))
}

func TestPrepareGrokModerationRefusalResult(t *testing.T) {
	result := PrepareGrokModerationRefusalResult(nil, "grok-4", GrokModerationRefusalBilledCost(nil))
	if result.Model != "grok-4" || result.UpstreamModel != "grok-4" || result.NonBillableUpstreamError || result.ForcedBillingCost != 0.1 || !result.ForceBalanceBilling {
		t.Fatalf("unexpected result: %+v", result)
	}
}

func TestGrokModerationRefusalBilledCostUsesGroupOverride(t *testing.T) {
	cases := []struct {
		name  string
		group *Group
		want  float64
	}{
		{"nil group falls back to 0.1", nil, 0.1},
		{"unset override falls back to 0.1", &Group{}, 0.1},
		{"explicit 0 disables", &Group{GrokModerationRefusalPrice: ptrGrokPrice(0)}, 0},
		{"custom price", &Group{GrokModerationRefusalPrice: ptrGrokPrice(0.5)}, 0.5},
		{"negative clamps to 0", &Group{GrokModerationRefusalPrice: ptrGrokPrice(-1)}, 0},
	}
	for _, tc := range cases {
		got := GrokModerationRefusalBilledCost(tc.group)
		if math.Abs(got-tc.want) > 1e-12 {
			t.Fatalf("%s: want=%v got=%v", tc.name, tc.want, got)
		}
	}

	result := PrepareGrokModerationRefusalResult(nil, "grok-4", 0.5)
	if math.Abs(result.ForcedBillingCost-0.5) > 1e-12 || !result.ForceBalanceBilling {
		t.Fatalf("unexpected result: %+v", result)
	}

	disabled := PrepareGrokModerationRefusalResult(nil, "grok-4", 0)
	if disabled.ForcedBillingCost != 0 || disabled.ForceBalanceBilling {
		t.Fatalf("explicit zero must not force billing: %+v", disabled)
	}
}

func TestPrepareGrokModerationRefusalOpenAIResult(t *testing.T) {
	result := PrepareGrokModerationRefusalOpenAIResult(nil, "grok-4.7", 0.25)
	if result.Model != "grok-4.7" || result.UpstreamModel != "grok-4.7" ||
		result.NonBillableUpstreamError || math.Abs(result.ForcedBillingCost-0.25) > 1e-12 || !result.ForceBalanceBilling {
		t.Fatalf("unexpected result: %+v", result)
	}
}

func ptrGrokPrice(v float64) *float64 { return &v }

func TestIsGrokPlatformContentPolicyRejection(t *testing.T) {
	if !IsGrokPlatformContentPolicyRejection(PlatformGrok,
		errors.New("grok content policy rejection: text is sensitive")) {
		t.Fatal("expected content policy rejection to match")
	}
	if IsGrokPlatformContentPolicyRejection(PlatformOpenAI,
		errors.New("grok content policy rejection: x")) {
		t.Fatal("non-Grok platform must not match")
	}
	if IsGrokPlatformContentPolicyRejection(PlatformGrok, errors.New("some other error")) {
		t.Fatal("unrelated error must not match")
	}
	if IsGrokPlatformContentPolicyRejection(PlatformGrok, nil) {
		t.Fatal("nil error must not match")
	}
}

func TestIsGlobalIPBlacklistRejection(t *testing.T) {
	require.True(t, IsGlobalIPBlacklistResponse([]byte(`{"message":"blocked by global ip blacklist"}`)))
	require.True(t, IsGlobalIPBlacklistRejection(&UpstreamFailoverError{
		StatusCode:   403,
		ResponseBody: []byte(`{"message":"Blocked by Global IP Blacklist"}`),
	}))
	require.True(t, IsGlobalIPBlacklistRejection(errors.New("upstream rejected: blocked by global ip blacklist")))
	require.False(t, IsGlobalIPBlacklistResponse([]byte(`{"message":"forbidden"}`)))
	require.False(t, IsGlobalIPBlacklistRejection(nil))
}

func TestGrokModerationSessionHashIsolation(t *testing.T) {
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest("POST", "/v1/chat/completions", nil)
	key := &APIKey{User: &User{ID: 77}}
	require.Empty(t, GrokModerationSessionHash(c, key, []byte(`{"model":"grok-4.7"}`)))
	c.Request.Header.Set("Session_id", "conversation")
	require.Equal(t, GrokModerationSessionHash(c, key, nil), GrokModerationSessionHash(c, &APIKey{UserID: 77}, nil))
	require.NotEqual(t, GrokModerationSessionHash(c, key, nil), GrokModerationSessionHash(c, &APIKey{UserID: 78}, nil))
	require.Empty(t, GrokModerationSessionHash(c, &APIKey{}, nil))
}

func TestGrokModerationSessionHashUsesConversationIdentityNotCacheKey(t *testing.T) {
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest("POST", "/v1/responses", nil)
	key := &APIKey{UserID: 77}
	first := []byte(`{"model":"grok-4.7","prompt_cache_key":"shared-cache","input":"conversation A"}`)
	second := []byte(`{"model":"grok-4.7","prompt_cache_key":"shared-cache","input":"conversation B"}`)
	require.Empty(t, GrokModerationSessionHash(c, key, first))
	require.Empty(t, GrokModerationSessionHash(c, key, second))
	require.NotEqual(t, grokModerationTranscriptKeys(key, first, true), grokModerationTranscriptKeys(key, second, true),
		"reused prompt_cache_key must not merge unrelated conversation transcripts")

	c.Request.Header.Set(grokConversationIDHeader, "native-conversation")
	grokHash := GrokModerationSessionHash(c, key, []byte(`{"model":"grok-4.7","input":"turn 1"}`))
	nonGrokHash := GrokModerationSessionHash(c, key, []byte(`{"model":"gpt-5.6-sol","input":"turn 2"}`))
	require.NotEmpty(t, grokHash)
	require.Equal(t, grokHash, nonGrokHash, "Grok conversation identity must persist after switching model family")

	c.Request.Header.Del(grokConversationIDHeader)
	previousResponseID := []byte(`{"model":"gpt-5.6-sol","previous_response_id":"resp_0123456789abcdef","input":"continue"}`)
	require.NotEmpty(t, GrokModerationSessionHash(c, key, previousResponseID),
		"response IDs are stable conversation identifiers even after a model switch")
}

func TestGrokModerationTranscriptMatchesContinuationWithoutBlockingSharedGreeting(t *testing.T) {
	key := &APIKey{UserID: 77}
	refused := []byte(`{"model":"grok-4.7","prompt_cache_key":"shared-cache","messages":[{"role":"user","content":"hello"},{"role":"assistant","content":"history A"},{"role":"user","content":"refused turn"}]}`)
	continuation := []byte(`{"model":"grok-4.6","prompt_cache_key":"shared-cache","messages":[{"role":"user","content":"hello"},{"role":"assistant","content":"history A"},{"role":"user","content":"refused turn"},{"role":"user","content":"continue"}]}`)
	otherConversation := []byte(`{"model":"grok-4.6","prompt_cache_key":"shared-cache","messages":[{"role":"user","content":"hello"},{"role":"assistant","content":"history B"},{"role":"user","content":"ordinary request"}]}`)
	written := grokModerationTranscriptKeys(key, refused, true)
	require.NotEmpty(t, written)
	for _, stored := range written {
		require.Contains(t, grokModerationTranscriptKeys(key, continuation, false), stored)
		require.NotContains(t, grokModerationTranscriptKeys(key, otherConversation, false), stored)
		require.NotContains(t, grokModerationTranscriptKeys(&APIKey{UserID: 78}, continuation, false), stored)
	}
}
