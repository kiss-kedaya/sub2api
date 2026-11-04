//go:build unit

package admin

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

type revealCredentialAdminService struct {
	service.AdminService
	accounts map[int64]*service.Account
}

func (s *revealCredentialAdminService) GetAccount(_ context.Context, id int64) (*service.Account, error) {
	account := s.accounts[id]
	if account == nil {
		return nil, service.ErrAccountNotFound
	}
	return account, nil
}

func newRevealCredentialHandler(accounts ...*service.Account) *AccountHandler {
	byID := make(map[int64]*service.Account, len(accounts))
	for _, account := range accounts {
		byID[account.ID] = account
	}
	return &AccountHandler{adminService: &revealCredentialAdminService{accounts: byID}}
}

func callRevealCredential(t *testing.T, h *AccountHandler, id string) (*httptest.ResponseRecorder, map[string]any) {
	t.Helper()
	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)
	ctx.Request = httptest.NewRequest(http.MethodGet, "/api/v1/admin/accounts/"+id+"/credential", nil)
	ctx.Params = gin.Params{{Key: "id", Value: id}}
	h.RevealAPIKey(ctx)

	var body map[string]any
	require.NoError(t, json.Unmarshal(recorder.Body.Bytes(), &body))
	return recorder, body
}

func TestRevealAPIKeyReturnsOnlyTheStoredKey(t *testing.T) {
	parentID := int64(9)
	apiKeyAccount := &service.Account{
		ID:       1,
		Platform: "openai",
		Type:     "apikey",
		Credentials: map[string]any{
			"api_key":  "sk-test-value",
			"base_url": "https://api.openai.com",
		},
	}
	h := newRevealCredentialHandler(
		apiKeyAccount,
		&service.Account{ID: 2, Platform: "openai", Type: "oauth", Credentials: map[string]any{"access_token": "oauth-secret"}},
		&service.Account{ID: 3, Platform: "openai", Type: "apikey", ParentAccountID: &parentID},
		&service.Account{ID: 4, Platform: "openai", Type: "apikey", Credentials: map[string]any{"api_key": ""}},
	)

	t.Run("apikey", func(t *testing.T) {
		recorder, body := callRevealCredential(t, h, "1")
		require.Equal(t, http.StatusOK, recorder.Code)
		data, ok := body["data"].(map[string]any)
		require.True(t, ok)
		require.Equal(t, "api_key", data["credential_key"])
		require.Equal(t, "sk-test-value", data["value"])
		require.NotContains(t, recorder.Body.String(), "base_url")
		require.Contains(t, recorder.Header().Get("Cache-Control"), "no-store")
	})

	for _, tc := range []struct {
		name string
		id   string
	}{
		{"oauth account", "2"},
		{"spark shadow", "3"},
		{"empty key", "4"},
		{"missing account", "5"},
		{"invalid id", "not-a-number"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			recorder, _ := callRevealCredential(t, h, tc.id)
			require.NotEqual(t, http.StatusOK, recorder.Code)
			require.True(t, strings.Contains(recorder.Body.String(), "error") || strings.Contains(recorder.Body.String(), "message"))
			require.NotContains(t, recorder.Body.String(), "oauth-secret")
			require.NotContains(t, recorder.Body.String(), "sk-test-value")
		})
	}
}
