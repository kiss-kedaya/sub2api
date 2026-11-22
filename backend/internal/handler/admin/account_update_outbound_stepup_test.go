package admin

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

// 账号 base_url / api_base_urls 是服务端出站目标：改到内网等于把账号变成
// 内网请求代理。这里锁定「只有真实变更才要求 step-up」与审计字段。

type accountOutboundStepUpStub struct {
	service.AdminService
	existing    *service.Account
	updateCalls int
	lastInput   *service.UpdateAccountInput
}

func (s *accountOutboundStepUpStub) GetAccount(context.Context, int64) (*service.Account, error) {
	account := *s.existing
	return &account, nil
}

func (s *accountOutboundStepUpStub) UpdateAccount(_ context.Context, _ int64, input *service.UpdateAccountInput) (*service.Account, error) {
	s.updateCalls++
	s.lastInput = input
	account := *s.existing
	return &account, nil
}

type accountOutboundHarness struct {
	stepUpEnabled bool
	actorTotp     bool
	grant         bool
	adminAPIKey   bool
}

func setupAccountOutboundRouter(t *testing.T, h accountOutboundHarness) (*gin.Engine, *accountOutboundStepUpStub, *map[string]any) {
	t.Helper()
	gin.SetMode(gin.TestMode)

	existing := &service.Account{
		ID:       77,
		Name:     "relay",
		Platform: service.PlatformDeepseek,
		Type:     service.AccountTypeAPIKey,
		Status:   service.StatusActive,
		Credentials: map[string]any{
			"base_url":      "https://api.deepseek.com",
			"api_base_urls": map[string]any{"anthropic": "https://api.deepseek.com"},
			"api_key":       "sk-test",
		},
	}
	stub := &accountOutboundStepUpStub{existing: existing}

	actorRepo := &totpDemotionActorRepo{actor: &service.User{ID: 41, Role: service.RoleAdmin, Status: service.StatusActive, TotpEnabled: h.actorTotp}}
	cache := &totpDemotionGrantCache{}
	if h.grant {
		cache.grantUserID = 41
		cache.grantSession = "admin-session"
	}
	totpSvc := service.NewTotpService(actorRepo, nil, cache, nil, nil, nil)
	userSvc := service.NewUserService(actorRepo, nil, nil, nil)
	enabled := "false"
	if h.stepUpEnabled {
		enabled = "true"
	}
	settings := service.NewSettingService(&settingHandlerRepoStub{values: map[string]string{service.SettingKeyStepUpEnabled: enabled}}, nil)

	handler := NewAccountHandler(stub, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil)
	handler.SetStepUpDependencies(totpSvc, userSvc, settings)

	captured := map[string]any{}
	router := gin.New()
	router.Use(func(c *gin.Context) {
		c.Set(string(middleware.ContextKeyUser), middleware.AuthSubject{UserID: 41})
		c.Set(middleware.ContextKeySessionID, "admin-session")
		if h.adminAPIKey {
			c.Set("auth_method", service.AuditAuthMethodAdminAPIKey)
		} else {
			c.Set("auth_method", "jwt")
		}
		c.Next()
		if extra, ok := c.Get("audit_extra"); ok {
			if fields, ok := extra.(map[string]any); ok {
				for key, value := range fields {
					captured[key] = value
				}
			}
		}
	})
	router.PUT("/api/v1/admin/accounts/:id", handler.Update)
	return router, stub, &captured
}

func accountOutboundUpdateBody(credentials map[string]any) string {
	raw, _ := json.Marshal(map[string]any{"credentials": credentials})
	return string(raw)
}

func TestAccountBaseURLChangeRequiresStepUp(t *testing.T) {
	cases := []struct {
		name        string
		harness     accountOutboundHarness
		credentials map[string]any
		wantStatus  int
		wantCode    string
		wantCalls   int
	}{
		{
			name:        "base_url_changed_without_grant",
			harness:     accountOutboundHarness{stepUpEnabled: true, actorTotp: true},
			credentials: map[string]any{"base_url": "http://10.1.2.3:8080"},
			wantStatus:  http.StatusForbidden,
			wantCode:    "STEP_UP_REQUIRED",
		},
		{
			name:        "api_base_urls_changed_without_grant",
			harness:     accountOutboundHarness{stepUpEnabled: true, actorTotp: true},
			credentials: map[string]any{"api_base_urls": map[string]any{"anthropic": "http://169.254.169.254"}},
			wantStatus:  http.StatusForbidden,
			wantCode:    "STEP_UP_REQUIRED",
		},
		{
			name:        "base_url_unchanged_does_not_trigger",
			harness:     accountOutboundHarness{stepUpEnabled: true, actorTotp: true},
			credentials: map[string]any{"base_url": "https://api.deepseek.com"},
			wantStatus:  http.StatusOK,
			wantCalls:   1,
		},
		{
			name:        "step_up_disabled_keeps_legacy_behavior",
			harness:     accountOutboundHarness{stepUpEnabled: false, actorTotp: true},
			credentials: map[string]any{"base_url": "https://new-relay.example.com"},
			wantStatus:  http.StatusOK,
			wantCalls:   1,
		},
		{
			name:        "grant_allows_change_and_audits_old_new",
			harness:     accountOutboundHarness{stepUpEnabled: true, actorTotp: true, grant: true},
			credentials: map[string]any{"base_url": "https://new-relay.example.com"},
			wantStatus:  http.StatusOK,
			wantCalls:   1,
		},
		{
			name:        "machine_credential_rejected",
			harness:     accountOutboundHarness{stepUpEnabled: true, actorTotp: true, adminAPIKey: true},
			credentials: map[string]any{"base_url": "https://new-relay.example.com"},
			wantStatus:  http.StatusForbidden,
			wantCode:    "STEP_UP_ADMIN_API_KEY_FORBIDDEN",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			router, stub, captured := setupAccountOutboundRouter(t, tc.harness)
			w := httptest.NewRecorder()
			req := httptest.NewRequest(http.MethodPut, "/api/v1/admin/accounts/77", strings.NewReader(accountOutboundUpdateBody(tc.credentials)))
			req.Header.Set("Content-Type", "application/json")
			router.ServeHTTP(w, req)

			require.Equal(t, tc.wantStatus, w.Code, w.Body.String())
			if tc.wantCode != "" {
				require.Contains(t, w.Body.String(), tc.wantCode)
			}
			require.Equal(t, tc.wantCalls, stub.updateCalls)
			if tc.wantCalls == 1 && tc.name == "grant_allows_change_and_audits_old_new" {
				require.Equal(t, "https://api.deepseek.com", (*captured)["account_base_url_old"])
				require.Equal(t, "https://new-relay.example.com", (*captured)["account_base_url_new"])
				require.Equal(t, true, (*captured)["changed"])
			}
		})
	}
}
