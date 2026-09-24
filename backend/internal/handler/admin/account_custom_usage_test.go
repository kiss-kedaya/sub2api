package admin

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/handler/dto"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

type customUsageHandlerRepo struct {
	a       *service.Account
	batches int
}

func (r *customUsageHandlerRepo) GetByID(_ context.Context, id int64) (*service.Account, error) {
	if id != r.a.ID {
		return nil, service.ErrAccountNotFound
	}
	return r.a, nil
}
func (r *customUsageHandlerRepo) GetByIDs(_ context.Context, ids []int64) ([]*service.Account, error) {
	r.batches++
	return []*service.Account{r.a}, nil
}
func (r *customUsageHandlerRepo) BulkUpdate(_ context.Context, _ []int64, u service.AccountBulkUpdate) (int64, error) {
	for k, v := range u.Credentials {
		r.a.Credentials[k] = v
	}
	for k, v := range u.Extra {
		r.a.Extra[k] = v
	}
	return 1, nil
}
func customUsageHandlerRouter(t *testing.T) (*gin.Engine, *customUsageHandlerRepo) {
	t.Helper()
	gin.SetMode(gin.TestMode)
	repo := &customUsageHandlerRepo{a: &service.Account{ID: 1, Type: service.AccountTypeAPIKey, Platform: service.PlatformOpenAI, Credentials: map[string]any{"api_key": "inherited-key", "base_url": "https://billing.example.com"}, Extra: map[string]any{"unrelated": true}}}
	h := &CustomUsageHandler{usage: service.NewCustomUsageService(repo)}
	router := gin.New()
	group := router.Group("/api/v1/admin/accounts")
	group.GET("/:id/custom-usage-config", h.GetConfig)
	group.PUT("/:id/custom-usage-config", h.PutConfig)
	group.POST("/:id/custom-usage-query", h.Query)
	group.POST("/custom-usage-batch", h.Batch)
	return router, repo
}
func customUsageHTTP(router *gin.Engine, method, path, body string) *httptest.ResponseRecorder {
	r := httptest.NewRequest(method, path, strings.NewReader(body))
	r.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	router.ServeHTTP(w, r)
	return w
}
func TestAccountCustomUsageConfigLifecycleAndDTO(t *testing.T) {
	router, repo := customUsageHandlerRouter(t)
	payload := `{"enabled":true,"template":"custom","base_url":"","api_key":"private-custom-key","access_token":"private-access","timeout_seconds":10,"interval_minutes":0,"request":{"url":"{{baseUrl}}/balance?token={{apiKey}}&secret=hidden-query","method":"GET","headers":{"Authorization":"Bearer {{apiKey}}","X-Vendor":"hidden-header"}},"extractor":{"remaining":{"path":"data.balance"},"unit":{"value":"USD"}}}`
	w := customUsageHTTP(router, http.MethodPut, "/api/v1/admin/accounts/1/custom-usage-config", payload)
	require.Equal(t, 200, w.Code, w.Body.String())
	for _, path := range []string{"/api/v1/admin/accounts/1/custom-usage-config"} {
		w = customUsageHTTP(router, http.MethodGet, path, "")
		require.Equal(t, 200, w.Code)
		for _, secret := range []string{"private-custom-key", "private-access", "hidden-query", "hidden-header"} {
			require.NotContains(t, w.Body.String(), secret)
		}
		require.Contains(t, w.Body.String(), `"has_api_key":true`)
	}
	account := dto.AccountFromService(repo.a)
	list := dto.AccountListItemFromAccount(account)
	for _, value := range []any{account, list} {
		b, err := json.Marshal(value)
		require.NoError(t, err)
		require.NotContains(t, string(b), "private-custom-key")
		require.NotContains(t, string(b), "hidden-header")
		require.NotContains(t, string(b), "hidden-query")
		require.NotContains(t, account.Credentials, service.CustomUsageCredentialsKey)
		require.NotContains(t, account.Extra, service.CustomUsageExtraKey)
	}
	w = customUsageHTTP(router, http.MethodPost, "/api/v1/admin/accounts/custom-usage-batch", `{"account_ids":[1,1,2]}`)
	require.Equal(t, 200, w.Code)
	require.Equal(t, 1, repo.batches)
	require.Contains(t, w.Body.String(), `"items"`)
	require.Contains(t, w.Body.String(), `"interval_minutes":0`)
	require.NotContains(t, w.Body.String(), "remaining")
	w = customUsageHTTP(router, http.MethodPost, "/api/v1/admin/accounts/1/custom-usage-query", `{}`)
	require.Equal(t, 200, w.Code)
	require.NotContains(t, w.Body.String(), "remaining")
}
func TestAccountCustomUsageExportExcludesManagedSecrets(t *testing.T) {
	router, adminSvc := setupAccountDataRouter()
	credentials := map[string]any{
		"api_key": "existing-backup-key",
		service.CustomUsageCredentialsKey: map[string]any{
			"api_key":      "custom-private-key",
			"access_token": "custom-private-token",
			"request_url":  "https://billing.example.com/?token=custom-private-query",
			"headers":      map[string]any{"X-Vendor": "custom-private-header"},
		},
	}
	extra := map[string]any{"unrelated": true, service.CustomUsageExtraKey: map[string]any{"enabled": true}}
	adminSvc.accounts = []service.Account{{ID: 1, Name: "custom-usage", Platform: service.PlatformOpenAI, Type: service.AccountTypeAPIKey, Credentials: credentials, Extra: extra}}
	response := customUsageHTTP(router, http.MethodGet, "/api/v1/admin/accounts/data", "")
	require.Equal(t, http.StatusOK, response.Code)
	for _, secret := range []string{service.CustomUsageCredentialsKey, service.CustomUsageExtraKey, "custom-private-key", "custom-private-token", "custom-private-query", "custom-private-header"} {
		require.NotContains(t, response.Body.String(), secret)
	}
	var payload dataResponse
	require.NoError(t, json.Unmarshal(response.Body.Bytes(), &payload))
	require.Len(t, payload.Data.Accounts, 1)
	require.Equal(t, "existing-backup-key", payload.Data.Accounts[0].Credentials["api_key"])
	require.Equal(t, true, payload.Data.Accounts[0].Extra["unrelated"])
	require.Contains(t, credentials, service.CustomUsageCredentialsKey)
	require.Contains(t, extra, service.CustomUsageExtraKey)
}

func TestAccountCustomUsageHandlerValidation(t *testing.T) {
	ids := make([]int64, 51)
	for i := range ids {
		ids[i] = 1
	}
	oversize, _ := json.Marshal(map[string]any{"account_ids": ids})
	for _, tc := range []struct {
		method, path, body string
		status             int
	}{
		{http.MethodGet, "/api/v1/admin/accounts/no/custom-usage-config", "", 400},
		{http.MethodGet, "/api/v1/admin/accounts/0/custom-usage-config", "", 400},
		{http.MethodGet, "/api/v1/admin/accounts/2/custom-usage-config", "", 404},
		{http.MethodPut, "/api/v1/admin/accounts/1/custom-usage-config", `{}`, 400},
		{http.MethodPut, "/api/v1/admin/accounts/1/custom-usage-config", `{"api_key":"do-not-echo",`, 400},
		{http.MethodPost, "/api/v1/admin/accounts/1/custom-usage-query", `{"force":"do-not-echo"}`, 400},
		{http.MethodPost, "/api/v1/admin/accounts/1/custom-usage-query", `{} {}`, 400},
		{http.MethodPost, "/api/v1/admin/accounts/1/custom-usage-query", `{"padding":"` + strings.Repeat("x", 33000) + `"}`, 400},
		{http.MethodPost, "/api/v1/admin/accounts/custom-usage-batch", `{"account_ids":[]}`, 400},
		{http.MethodPost, "/api/v1/admin/accounts/custom-usage-batch", `{"account_ids":[-1]}`, 400},
		{http.MethodPost, "/api/v1/admin/accounts/custom-usage-batch", string(oversize), 400},
	} {
		t.Run(tc.method+tc.path+string(bytes.TrimSpace([]byte(tc.body[:min(len(tc.body), 25)]))), func(t *testing.T) {
			router, _ := customUsageHandlerRouter(t)
			w := customUsageHTTP(router, tc.method, tc.path, tc.body)
			require.Equal(t, tc.status, w.Code, w.Body.String())
			require.NotContains(t, w.Body.String(), "do-not-echo")
		})
	}
}
