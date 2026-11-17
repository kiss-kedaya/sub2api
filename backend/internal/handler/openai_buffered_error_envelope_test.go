package handler

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/pkg/openai_compat"
	"github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

// bufferedEnvelopeUpstream answers every upstream call with one HTTP 200 JSON body.
type bufferedEnvelopeUpstream struct {
	service.HTTPUpstream
	wire string
}

func (u *bufferedEnvelopeUpstream) Do(_ *http.Request, _ string, _ int64, _ int) (*http.Response, error) {
	return &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": {"application/json"}},
		Body:       io.NopCloser(strings.NewReader(u.wire)),
	}, nil
}

// runBufferedEnvelopeRequest sends one non-streaming request through the real
// handler against an account that only speaks Chat Completions, and returns what
// the client received together with the usage row and the settlement.
func runBufferedEnvelopeRequest(t *testing.T, endpoint, wire string) (*httptest.ResponseRecorder, []*service.UsageLog, []*service.UsageBillingCommand) {
	t.Helper()
	gin.SetMode(gin.TestMode)
	groupID := int64(4001)
	account := service.Account{
		ID: 9910, Name: "synthetic", Platform: service.PlatformOpenAI, Type: service.AccountTypeAPIKey,
		Status: service.StatusActive, Schedulable: true, GroupIDs: []int64{groupID},
		Credentials: map[string]any{"api_key": "synthetic", "base_url": "https://example.invalid"},
		Extra:       map[string]any{openai_compat.ExtraKeyResponsesMode: string(openai_compat.ResponsesSupportModeForceChatCompletions)},
	}
	cfg := &config.Config{RunMode: config.RunModeStandard}
	cfg.Default.RateMultiplier = 1
	cfg.Gateway.MaxAccountSwitches = 1
	admission := *cfg
	admission.RunMode = config.RunModeSimple
	billing := service.NewBillingCacheService(nil, nil, nil, nil, nil, nil, &admission, nil)
	t.Cleanup(billing.Stop)
	usage := &openAIWSFirstRefusalUsageRepo{}
	ledger := &openAIWSFirstRefusalBillingRepo{}
	gateway := service.NewOpenAIGatewayService(
		&openAIWSFailoverHandlerAccountRepoStub{accounts: []service.Account{account}}, usage, ledger,
		nil, nil, nil, nil, cfg, nil, nil, service.NewBillingService(cfg, nil), nil, billing,
		&bufferedEnvelopeUpstream{wire: wire}, &service.DeferredService{}, nil, nil, nil, nil, nil, nil, nil,
	)
	h := NewOpenAIGatewayHandler(gateway, service.NewConcurrencyService(nil), billing,
		service.NewAPIKeyService(nil, nil, nil, nil, nil, nil, cfg), nil, nil, nil, nil, cfg)
	key := &service.APIKey{
		ID: 55, UserID: 77, GroupID: &groupID,
		User: &service.User{ID: 77, Balance: 100, Status: service.StatusActive},
		Group: &service.Group{
			ID: groupID, Platform: service.PlatformOpenAI, Status: service.StatusActive, Hydrated: true,
			CrossProtocolConversionEnabled: true, AllowMessagesDispatch: true, RateMultiplier: 1,
		},
	}
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodPost, endpoint, strings.NewReader(
		`{"model":"gpt-5.1","stream":false,"input":"hello","max_tokens":128,"messages":[{"role":"user","content":"hello"}]}`))
	c.Set(string(middleware.ContextKeyAPIKey), key)
	c.Set(string(middleware.ContextKeyUser), middleware.AuthSubject{UserID: 77})
	switch endpoint {
	case "/v1/responses":
		h.Responses(c)
	case "/v1/messages":
		h.Messages(c)
	default:
		h.ChatCompletions(c)
	}
	return rec, usage.snapshot(), ledger.snapshot()
}

// An HTTP 200 error envelope from a Chat Completions fallback account ends the
// request with one error, in valid JSON, and a single zero-cost settlement.
func TestBufferedFallbackErrorEnvelopeIsOneErrorAndFree(t *testing.T) {
	for _, endpoint := range []string{"/v1/responses", "/v1/messages"} {
		for _, choices := range []string{"", `,"choices":[]`, `,"choices":null`} {
			t.Run(endpoint+choices, func(t *testing.T) {
				rec, rows, settlements := runBufferedEnvelopeRequest(t, endpoint,
					`{"error":{"type":"invalid_request_error","message":"synthetic parameter rejected"},"usage":{"prompt_tokens":1000,"completion_tokens":2}`+choices+`}`)
				require.GreaterOrEqual(t, rec.Code, http.StatusBadRequest, "the client must not be told the request succeeded")
				require.True(t, gjson.Valid(rec.Body.String()), "one JSON error, nothing appended after it: %s", rec.Body.String())
				require.True(t, gjson.Get(rec.Body.String(), "error").Exists())
				require.NotContains(t, rec.Body.String(), "event:")
				require.Len(t, rows, 1, "the usage row is kept")
				require.Len(t, settlements, 1)
				require.Equal(t, 1000, rows[0].InputTokens)
				require.Zero(t, rows[0].ActualCost)
				require.Zero(t, settlements[0].BalanceCost)
			})
		}
	}
}

// An answer the client received is paid for, whatever a relay attached next to
// it: an empty error object in any spelling, or a note beside real choices.
func TestBufferedAnswerWithErrorNoteIsBilled(t *testing.T) {
	for _, note := range []string{`{}`, `{  }`, "{\n}", `null`, `{"message":"synthetic relay note"}`} {
		for _, endpoint := range []string{"/v1/chat/completions", "/v1/responses", "/v1/messages"} {
			t.Run(endpoint+" error="+note, func(t *testing.T) {
				rec, rows, settlements := runBufferedEnvelopeRequest(t, endpoint,
					`{"id":"chat_synthetic","model":"gpt-5.1","choices":[{"message":{"role":"assistant","content":"synthetic answer"},"finish_reason":"stop"}],"usage":{"prompt_tokens":1000,"completion_tokens":2},"error":`+note+`}`)
				require.Equal(t, http.StatusOK, rec.Code)
				require.True(t, gjson.Valid(rec.Body.String()))
				require.Contains(t, rec.Body.String(), "synthetic answer")
				require.Len(t, rows, 1)
				require.Len(t, settlements, 1)
				require.Positive(t, settlements[0].BalanceCost, "a delivered answer is not free")
			})
		}
	}
}
