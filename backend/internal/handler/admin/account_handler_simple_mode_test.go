package admin

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

type simpleModeAccountService struct {
	*stubAdminService
	account service.Account
}

func (s *simpleModeAccountService) GetAccount(context.Context, int64) (*service.Account, error) {
	return &s.account, nil
}

func (s *simpleModeAccountService) CreateAccount(context.Context, *service.CreateAccountInput) (*service.Account, error) {
	return &s.account, nil
}

func (s *simpleModeAccountService) UpdateAccount(context.Context, int64, *service.UpdateAccountInput) (*service.Account, error) {
	return &s.account, nil
}

func TestAccountHandlerSimpleModeUsesMinimalGroupReferences(t *testing.T) {
	gin.SetMode(gin.TestMode)
	richGroup := &service.Group{ID: 7, Name: "basic", Platform: service.PlatformAnthropic, Status: service.StatusActive, RateMultiplier: 9, IsExclusive: true, SubscriptionType: service.SubscriptionTypeSubscription, RPMLimit: 99}
	account := service.Account{ID: 3, Name: "account", Platform: service.PlatformAnthropic, Type: service.AccountTypeAPIKey, Status: service.StatusActive, Groups: []*service.Group{richGroup}}
	svc := &simpleModeAccountService{stubAdminService: newStubAdminService(), account: account}
	svc.accounts = []service.Account{account}
	h := NewAccountHandler(svc, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil)
	h.cfg = &config.Config{RunMode: config.RunModeSimple}
	r := gin.New()
	r.GET("/accounts", h.List)
	r.GET("/accounts/:id", h.GetByID)
	r.POST("/accounts", h.Create)
	r.PUT("/accounts/:id", h.Update)

	tests := []struct {
		method string
		path   string
		body   string
	}{
		{http.MethodGet, "/accounts", ""},
		{http.MethodGet, "/accounts/3", ""},
		{http.MethodPost, "/accounts", `{"name":"account","platform":"anthropic","type":"apikey","credentials":{"key":"x"}}`},
		{http.MethodPut, "/accounts/3", `{"name":"account"}`},
	}
	for _, tt := range tests {
		t.Run(tt.method+tt.path, func(t *testing.T) {
			req := httptest.NewRequest(tt.method, tt.path, bytes.NewBufferString(tt.body))
			req.Header.Set("Content-Type", "application/json")
			res := httptest.NewRecorder()
			r.ServeHTTP(res, req)
			require.Equal(t, http.StatusOK, res.Code, res.Body.String())
			var payload map[string]any
			require.NoError(t, json.Unmarshal(res.Body.Bytes(), &payload))
			raw := res.Body.String()
			require.Contains(t, raw, `"groups":[{"id":7,"name":"basic","platform":"anthropic","status":"active"}]`)
		})
	}
}

func TestAccountHandlerAdvancedModeKeepsFullGroupReferences(t *testing.T) {
	group := &service.Group{ID: 7, Name: "advanced", RateMultiplier: 9, SubscriptionType: service.SubscriptionTypeSubscription}
	h := NewAccountHandler(nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil)
	raw, err := json.Marshal(h.buildAccountResponseWithRuntime(context.Background(), &service.Account{Groups: []*service.Group{group}}))
	require.NoError(t, err)
	require.Contains(t, string(raw), `"rate_multiplier":9`)
	require.Contains(t, string(raw), `"subscription_type":"subscription"`)
}
