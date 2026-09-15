//go:build unit

package admin

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

type referralHandlerStub struct {
	eligibility        *service.OpenAIReferralEligibility
	queryErr, cacheErr error
	input              service.OpenAIReferralSendRequest
	cached             *service.OpenAIReferralEligibility
	sends              int
}

func (s *referralHandlerStub) QueryReferralEligibility(context.Context, int64) (*service.OpenAIReferralEligibility, error) {
	return s.eligibility, s.queryErr
}
func (s *referralHandlerStub) CacheReferralSnapshot(_ context.Context, _ int64, e *service.OpenAIReferralEligibility) error {
	s.cached = e
	return s.cacheErr
}
func (s *referralHandlerStub) SendReferralInvite(_ context.Context, _ int64, input service.OpenAIReferralSendRequest) (*service.OpenAIReferralSendResult, error) {
	s.sends++
	s.input = input
	return &service.OpenAIReferralSendResult{Email: input.Email, Sent: true}, nil
}

func referralHandlerRequest(t *testing.T, stub *referralHandlerStub, path, body string) *httptest.ResponseRecorder {
	t.Helper()
	gin.SetMode(gin.TestMode)
	h := &OpenAIOAuthHandler{referralService: stub}
	router := gin.New()
	router.POST("/:id/referrals/refresh", h.RefreshReferrals)
	router.POST("/:id/referrals/invite", h.SendReferralInvite)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, path, strings.NewReader(body)))
	return rec
}

func TestOpenAIReferralHandlerRefresh(t *testing.T) {
	count := 2
	stub := &referralHandlerStub{eligibility: &service.OpenAIReferralEligibility{ShouldShow: true, AvailableInvites: &count}, cacheErr: errors.New("cache failed")}
	rec := referralHandlerRequest(t, stub, "/100/referrals/refresh", "")
	require.Equal(t, http.StatusOK, rec.Code)
	var body struct {
		Data openAIReferralRefreshResponse `json:"data"`
	}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &body))
	require.Equal(t, 2, *body.Data.Eligibility.AvailableInvites)
	require.False(t, body.Data.CachePersisted)
	require.Zero(t, stub.sends)
}

func TestOpenAIReferralHandlerSentSurvivesRefreshFailure(t *testing.T) {
	stub := &referralHandlerStub{queryErr: errors.New("upstream timed out")}
	rec := referralHandlerRequest(t, stub, "/100/referrals/invite", `{"email":"friend@example.com","program_id":"codex_referral_consumer","confirmed":true}`)
	require.Equal(t, http.StatusOK, rec.Code)
	var body struct {
		Data openAIReferralSendResponse `json:"data"`
	}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &body))
	require.True(t, body.Data.Sent)
	require.Equal(t, "friend@example.com", body.Data.Email)
	require.True(t, body.Data.RefreshFailed)
	require.True(t, body.Data.CachePersisted)
	require.Nil(t, stub.cached, "stale remaining count must be invalidated")
	require.True(t, stub.input.Confirmed)
	require.Equal(t, 1, stub.sends)
}

func TestOpenAIReferralHandlerRejectsInvalidInput(t *testing.T) {
	stub := &referralHandlerStub{}
	for _, path := range []string{"/invalid/referrals/invite", "/0/referrals/invite", "/100/referrals/invite"} {
		rec := referralHandlerRequest(t, stub, path, "not-json")
		require.Equal(t, http.StatusBadRequest, rec.Code)
	}
	require.Zero(t, stub.sends)
}
