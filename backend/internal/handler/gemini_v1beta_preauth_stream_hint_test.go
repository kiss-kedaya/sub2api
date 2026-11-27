//go:build unit

package handler

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"

	"github.com/Wei-Shaw/sub2api/internal/pkg/ctxkey"
	middleware "github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
)

type streamHintCapturingPreauthorizer struct {
	capturedReq   *service.BalancePreauthorizationRequest
	streamHint    bool
	hasStreamHint bool
	err           error
}

func (s *streamHintCapturingPreauthorizer) Preauthorize(ctx context.Context, req service.BalancePreauthorizationRequest) (*service.BalancePreauthorizationGuard, error) {
	copyReq := req
	s.capturedReq = &copyReq
	s.streamHint, s.hasStreamHint = service.BalancePreauthorizationStreamHintFromContext(ctx)
	return nil, s.err
}

func (s *streamHintCapturingPreauthorizer) RequiresPreauthorization(int8) bool {
	return true
}

func TestGeminiV1BetaModels_PreauthStreamHintMatchesURLAction(t *testing.T) {
	gin.SetMode(gin.TestMode)
	groupID := int64(8101)
	group := &service.Group{ID: groupID, Hydrated: true, Platform: service.PlatformGemini, Status: service.StatusActive}

	tests := []struct {
		name               string
		modelAction        string
		body               string
		wantHasStreamHint  bool
		wantStreamHint     bool
		wantInitialOutHint int
	}{
		{
			name:               "streamGenerateContent without maxOutputTokens attaches stream=true hint",
			modelAction:        "/gemini-2.5-pro:streamGenerateContent",
			body:               `{"contents":[{"role":"user","parts":[{"text":"hi"}]}]}`,
			wantHasStreamHint:  true,
			wantStreamHint:     true,
			wantInitialOutHint: service.DefaultBalancePreauthorizationNonStreamOutputWindow,
		},
		{
			name:               "generateContent with spoofed stream=true attaches stream=false hint",
			modelAction:        "/gemini-2.5-pro:generateContent",
			body:               `{"stream":true,"contents":[{"role":"user","parts":[{"text":"hi"}]}]}`,
			wantHasStreamHint:  true,
			wantStreamHint:     false,
			wantInitialOutHint: service.DefaultBalancePreauthorizationOutputWindow,
		},
		{
			name:               "generateContent with explicit snake_case max_output_tokens keeps explicit cap without hint",
			modelAction:        "/gemini-2.5-pro:generateContent",
			body:               `{"contents":[{"role":"user","parts":[{"text":"hi"}]}],"generation_config":{"max_output_tokens":768}}`,
			wantHasStreamHint:  false,
			wantStreamHint:     false,
			wantInitialOutHint: 768,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			account := &service.Account{
				ID: 8102, Platform: service.PlatformGemini, Type: service.AccountTypeAPIKey,
				Status: service.StatusActive, Schedulable: true, Concurrency: 1,
				AccountGroups: []service.AccountGroup{{AccountID: 8102, GroupID: groupID}},
			}
			h, cleanup := newTestGatewayHandler(t, group, []*service.Account{account})
			defer cleanup()

			stub := &streamHintCapturingPreauthorizer{err: service.ErrBalanceWithholdingFailed}
			h.balancePreauthorizer = stub

			rec := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(rec)
			req := httptest.NewRequest(http.MethodPost, "/v1beta/models"+tc.modelAction, bytes.NewBufferString(tc.body))
			req.Header.Set("Content-Type", "application/json")
			c.Request = req.WithContext(context.WithValue(req.Context(), ctxkey.Group, group))
			c.Params = gin.Params{{Key: "modelAction", Value: tc.modelAction}}
			apiKey := &service.APIKey{
				ID: 8104, UserID: 8105, GroupID: &groupID, Group: group, Status: service.StatusActive,
				User: &service.User{ID: 8105, Concurrency: 10, Balance: 100},
			}
			c.Set(string(middleware.ContextKeyAPIKey), apiKey)
			c.Set(string(middleware.ContextKeyUser), middleware.AuthSubject{UserID: apiKey.UserID, Concurrency: 10})

			h.GeminiV1BetaModels(c)

			require.Equal(t, http.StatusTooManyRequests, rec.Code, rec.Body.String())
			require.NotNil(t, stub.capturedReq)
			require.Equal(t, tc.wantHasStreamHint, stub.hasStreamHint)
			require.Equal(t, tc.wantStreamHint, stub.streamHint)
			require.Equal(t, tc.wantInitialOutHint, stub.capturedReq.InitialOutputWindowTokens)
		})
	}
}
