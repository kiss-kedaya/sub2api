package service

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
	"github.com/stretchr/testify/require"
)

func referralTestService(t *testing.T, plan string, handler http.HandlerFunc) (*OpenAIQuotaService, *stubQuotaAccountRepo) {
	t.Helper()
	a := &Account{ID: 100, Platform: PlatformOpenAI, Type: AccountTypeOAuth, Status: StatusActive,
		Credentials: map[string]any{"chatgpt_account_id": "workspace-test", "plan_type": plan}}
	repo := &stubQuotaAccountRepo{accounts: map[int64]*Account{100: a}}
	tokens := &stubQuotaTokenCache{tokens: map[string]string{OpenAITokenCacheKey(a): "test-token"}}
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)
	return NewOpenAIQuotaService(repo, nil, NewOpenAITokenProvider(repo, tokens, nil), newQuotaRedirectingFactory(srv)), repo
}

func TestOpenAIReferralSend(t *testing.T) {
	for _, tc := range []struct{ plan, program string }{
		{"plus", openAIReferralConsumer}, {"team", openAIReferralWorkspace},
		{"self_serve_business_usage_based", openAIReferralWorkspace},
	} {
		t.Run(tc.plan, func(t *testing.T) {
			var gets, posts int
			svc, repo := referralTestService(t, tc.plan, func(w http.ResponseWriter, r *http.Request) {
				require.Equal(t, "Bearer test-token", r.Header.Get("Authorization"))
				require.Equal(t, "workspace-test", r.Header.Get("ChatGPT-Account-ID"))
				w.Header().Set("Content-Type", "application/json")
				switch r.URL.Path {
				case "/backend-api/referrals/invite/eligibility":
					gets++
					require.Equal(t, http.MethodGet, r.Method)
					require.Equal(t, tc.program, r.URL.Query().Get("program_id"))
					require.Equal(t, "persistent", r.URL.Query().Get("entrypoint"))
					_, _ = w.Write([]byte(`{"should_show":true,"remaining_send_capacity":8,"remaining_reward_capacity":3,"grants":[{"grant_type":"rate_limit_reset_credit","amount":1,"recipient":"referrer"}],"rules":["Offer rule"]}`))
				case "/backend-api/referrals/invite":
					posts++
					require.Equal(t, http.MethodPost, r.Method)
					var body map[string]any
					require.NoError(t, json.NewDecoder(r.Body).Decode(&body))
					require.Equal(t, map[string]any{"program_id": tc.program, "entrypoint": "persistent", "emails": []any{"friend@example.com"}}, body)
					_, _ = w.Write([]byte(`{"invites":[{"referral_id":"test-invite","email":"friend@example.com"}]}`))
				default:
					t.Errorf("unexpected path: %s", r.URL.Path)
				}
			})
			eligibility, err := svc.QueryReferralEligibility(context.Background(), 100)
			require.NoError(t, err)
			require.Equal(t, 3, *eligibility.AvailableInvites)
			require.Equal(t, []string{"Offer rule"}, eligibility.Rules)
			require.Positive(t, eligibility.FetchedAt)
			require.NoError(t, svc.CacheReferralSnapshot(context.Background(), 100, eligibility))
			require.Equal(t, eligibility, repo.extraUpdates[100][openAIReferralSnapshotKey])
			result, err := svc.SendReferralInvite(context.Background(), 100, OpenAIReferralSendRequest{
				Email: " friend@example.com ", ProgramID: tc.program, Confirmed: true,
			})
			require.NoError(t, err)
			require.True(t, result.Sent)
			require.Equal(t, "friend@example.com", result.Email)
			require.Equal(t, 2, gets, "send must recheck eligibility")
			require.Equal(t, 1, posts)
		})
	}
}

func TestOpenAIReferralSendGuards(t *testing.T) {
	for _, tc := range []struct {
		name, email, body, reason string
		confirmed, shadow         bool
	}{
		{"invalid email", "bad-email", `{}`, "OPENAI_REFERRAL_INVALID_EMAIL", true, false},
		{"multiple emails", "a@example.com,b@example.com", `{}`, "OPENAI_REFERRAL_INVALID_EMAIL", true, false},
		{"display name", "User <a@example.com>", `{}`, "OPENAI_REFERRAL_INVALID_EMAIL", true, false},
		{"header injection", "a@example.com\r\nBcc: b@example.com", `{}`, "OPENAI_REFERRAL_INVALID_EMAIL", true, false},
		{"no consent", "a@example.com", `{"should_show":true,"remaining_send_capacity":2}`, "OPENAI_REFERRAL_CONFIRMATION_REQUIRED", false, false},
		{"ineligible", "a@example.com", `{"should_show":false,"remaining_send_capacity":2}`, "OPENAI_REFERRAL_UNAVAILABLE", true, false},
		{"exhausted", "a@example.com", `{"should_show":true,"remaining_send_capacity":0}`, "OPENAI_REFERRAL_UNAVAILABLE", true, false},
		{"unknown capacity", "a@example.com", `{"should_show":true}`, "OPENAI_REFERRAL_UNAVAILABLE", true, false},
		{"reward exhausted", "a@example.com", `{"should_show":true,"remaining_send_capacity":3,"offer_id":"credits_250","remaining_reward_capacity":0}`, "OPENAI_REFERRAL_UNAVAILABLE", true, false},
		{"unknown reward capacity", "a@example.com", `{"should_show":true,"remaining_send_capacity":3,"grants":[{}]}`, "OPENAI_REFERRAL_UNAVAILABLE", true, false},
		{"null response", "a@example.com", `null`, "OPENAI_REFERRAL_INVALID_RESPONSE", true, false},
		{"shadow", "a@example.com", `{}`, "OPENAI_REFERRAL_SHADOW_ACCOUNT", true, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			posts := 0
			svc, repo := referralTestService(t, "plus", func(w http.ResponseWriter, r *http.Request) {
				if r.Method == http.MethodPost {
					posts++
				}
				w.Header().Set("Content-Type", "application/json")
				_, _ = w.Write([]byte(tc.body))
			})
			if tc.shadow {
				parentID := int64(200)
				repo.accounts[100].ParentAccountID = &parentID
			}
			_, err := svc.SendReferralInvite(context.Background(), 100, OpenAIReferralSendRequest{Email: tc.email, ProgramID: openAIReferralConsumer, Confirmed: tc.confirmed})
			require.Equal(t, tc.reason, infraerrors.Reason(err))
			require.Zero(t, posts)
		})
	}
}

func TestOpenAIReferralSendDoesNotRetryOrExposeUpstreamErrors(t *testing.T) {
	for _, status := range []int{400, 401, 403, 409, 429, 500} {
		t.Run(http.StatusText(status), func(t *testing.T) {
			posts := 0
			svc, _ := referralTestService(t, "plus", func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				if r.Method == http.MethodGet {
					_, _ = w.Write([]byte(`{"should_show":true,"remaining_send_capacity":2,"requires_explicit_confirmation":false,"offer_id":"none"}`))
					return
				}
				posts++
				w.WriteHeader(status)
				_, _ = w.Write([]byte(`{"detail":"secret-token someone@example.com"}`))
			})
			_, err := svc.SendReferralInvite(context.Background(), 100, OpenAIReferralSendRequest{Email: "friend@example.com", ProgramID: openAIReferralConsumer})
			require.Error(t, err)
			require.NotContains(t, err.Error(), "secret-token")
			require.NotContains(t, err.Error(), "someone@example.com")
			require.Equal(t, 1, posts)
		})
	}
}
