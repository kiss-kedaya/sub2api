//go:build unit

package integration

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/handler"
	"github.com/Wei-Shaw/sub2api/internal/repository"
	"github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/alicebob/miniredis/v2"
	"github.com/gin-gonic/gin"
	"github.com/pquerna/otp/totp"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/require"
)

type totpDisableUserRepo struct {
	service.UserRepository
	user service.User
}

func (r *totpDisableUserRepo) GetByID(context.Context, int64) (*service.User, error) {
	u := r.user
	return &u, nil
}
func (r *totpDisableUserRepo) BindTotpIfDisabled(_ context.Context, _ int64, secret string) error {
	if r.user.TotpEnabled || r.user.Status != service.StatusActive {
		return service.ErrUserSecurityStateChanged
	}
	r.user.TotpSecretEncrypted = &secret
	r.user.TotpEnabled = true
	now := time.Now()
	r.user.TotpEnabledAt = &now
	return nil
}
func (r *totpDisableUserRepo) DisableTotpWithExpectedRole(_ context.Context, _ int64, expectedRole string, expectedSecret *string) error {
	if r.user.Role != expectedRole || r.user.Status != service.StatusActive {
		return service.ErrUserSecurityStateChanged
	}
	if (r.user.TotpSecretEncrypted == nil) != (expectedSecret == nil) || (expectedSecret != nil && *r.user.TotpSecretEncrypted != *expectedSecret) {
		return service.ErrUserSecurityStateChanged
	}
	r.user.TotpEnabled = false
	r.user.TotpSecretEncrypted = nil
	return nil
}

type totpDisableSettingRepo struct{ service.SettingRepository }

func (totpDisableSettingRepo) GetValue(context.Context, string) (string, error) { return "true", nil }

type totpDisableEncryptor struct{}

func (totpDisableEncryptor) Encrypt(s string) (string, error) { return "encrypted:" + s, nil }
func (totpDisableEncryptor) Decrypt(s string) (string, error) {
	return strings.TrimPrefix(s, "encrypted:"), nil
}

func TestAdminTotpHTTPEnableWithoutServerApprovalAndDisableWithCurrentTotp(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := miniredis.RunT(t)
	rdb := redis.NewClient(&redis.Options{Addr: r.Addr()})
	t.Cleanup(func() { _ = rdb.Close() })
	users := &totpDisableUserRepo{user: service.User{ID: 42, Email: "admin@example.com", Role: service.RoleAdmin, Status: service.StatusActive}}
	require.NoError(t, users.user.SetPassword("correct-password"))
	cache := repository.NewTotpCache(rdb)
	svc := service.NewTotpService(users, totpDisableEncryptor{}, cache, service.NewSettingService(totpDisableSettingRepo{}, nil), nil, nil)
	h := handler.NewTotpHandler(svc)
	call := func(path string, payload map[string]string, handle gin.HandlerFunc) *httptest.ResponseRecorder {
		raw, err := json.Marshal(payload)
		require.NoError(t, err)
		rec := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(rec)
		c.Request = httptest.NewRequest(http.MethodPost, path, bytes.NewReader(raw))
		c.Request.Header.Set("Content-Type", "application/json")
		c.Set(string(middleware.ContextKeyUser), middleware.AuthSubject{UserID: 42})
		handle(c)
		return rec
	}
	setup := call("/user/totp/setup", map[string]string{"password": "correct-password"}, h.InitiateSetup)
	require.Equal(t, http.StatusOK, setup.Code, setup.Body.String())
	var data struct {
		Data service.TotpSetupResponse `json:"data"`
	}
	require.NoError(t, json.Unmarshal(setup.Body.Bytes(), &data))
	code, err := totp.GenerateCode(data.Data.Secret, time.Now())
	require.NoError(t, err)
	enabled := call("/user/totp/enable", map[string]string{"totp_code": code, "setup_token": data.Data.SetupToken}, h.Enable)
	require.Equal(t, http.StatusOK, enabled.Code, enabled.Body.String())
	require.True(t, users.user.TotpEnabled)
	// A cached step-up grant and the correct password still cannot replace the
	// actual authenticator code on the personal disable endpoint.
	require.NoError(t, cache.SetStepUpGrant(context.Background(), 42, "admin-session", service.StepUpGrantTTL))
	for _, payload := range []map[string]string{
		{"password": "correct-password", "email_code": "654321"},
		{"server_code": strings.Repeat("a", 64)},
		{"password": "correct-password", "totp_code": "not-a-code"},
	} {
		disabled := call("/user/totp/disable", payload, h.Disable)
		require.NotEqual(t, http.StatusOK, disabled.Code)
		require.True(t, users.user.TotpEnabled)
	}
	// Too many failed codes keep the account protected even with a valid code.
	for range 5 {
		_, _ = cache.IncrementVerifyAttempts(context.Background(), 42)
	}
	blocked := call("/user/totp/disable", map[string]string{"totp_code": code}, h.Disable)
	require.Equal(t, http.StatusTooManyRequests, blocked.Code)
	require.True(t, users.user.TotpEnabled)
	require.NoError(t, cache.ClearVerifyAttempts(context.Background(), 42))
	expired, err := totp.GenerateCode(data.Data.Secret, time.Now().Add(-10*time.Minute))
	require.NoError(t, err)
	// Avoid the negligible chance of equal six-digit values in different slots.
	if expired != code {
		rejected := call("/user/totp/disable", map[string]string{"totp_code": expired}, h.Disable)
		require.NotEqual(t, http.StatusOK, rejected.Code)
		require.True(t, users.user.TotpEnabled)
	}
	disabled := call("/user/totp/disable", map[string]string{"totp_code": code}, h.Disable)
	require.Equal(t, http.StatusOK, disabled.Code, disabled.Body.String())
	require.False(t, users.user.TotpEnabled)
	require.Nil(t, users.user.TotpSecretEncrypted)
}
