package handler

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/cryptopay"
	"github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

// Configuration-only tests inject a verifier explicitly; a nil production
// verifier must never be interpreted as verification having succeeded.
type cryptoConfigTestSecurity struct {
	cryptoAdminSecurityVerifier
	calls atomic.Int32
	err   error
}

func (s *cryptoConfigTestSecurity) VerifyRisk(_ context.Context, userID int64, code string) error {
	s.calls.Add(1)
	if userID != 1 {
		return service.ErrCryptoOperatorRequired
	}
	if s.err != nil {
		return s.err
	}
	if code != "123456" {
		return service.ErrCryptoTotpRequired
	}
	return nil
}

func cryptoConfigTestIdentity(c *gin.Context) {
	c.Set(string(middleware.ContextKeyUser), middleware.AuthSubject{UserID: 1})
	c.Next()
}

func TestCryptoConfigPublicViewNeverReturnsRPCURLsOrCredentialFragments(t *testing.T) {
	const secretURL = "https://fixture-user:fixture-secret@rpc.example.test/v1/private?token=fixture-token"
	store := &cryptoConfigStore{values: map[string]string{
		cryptopay.SettingKeyCryptoConfig: `{"chains":["bsc","ethereum"],"rpc":{"bsc":"` + secretURL + `","ethereum":"https://second.example.test","base":" ","https://unrecognized.example.test/private":"ignored"}}`,
	}}
	router := gin.New()
	router.GET("/config", (&CryptoHandler{store: store}).AdminGetConfig)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/config", nil))
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	require.Equal(t, "{}", gjson.Get(w.Body.String(), "data.rpc").Raw)
	require.Equal(t, `["bsc","ethereum"]`, gjson.Get(w.Body.String(), "data.rpc_configured").Raw)
	for _, secret := range []string{"https://", "fixture-user", "fixture-secret", "fixture-token", "unrecognized.example.test"} {
		require.NotContains(t, w.Body.String(), secret)
	}
	require.Equal(t, "no-store", w.Header().Get("Cache-Control"))
	require.Contains(t, store.values[cryptopay.SettingKeyCryptoConfig], secretURL, "redacting a response must not clear persisted endpoints")
}

func TestCryptoConfigOnlyFiveEffectiveFieldsMayChangeWithoutFreshCode(t *testing.T) {
	t.Setenv("CRYPTO_ADMIN_TEST_MODE", "false")
	for _, body := range []string{
		`{"sweep_global_threshold":2000}`,
		`{"sweep_min_per_address":{"bsc":5}}`,
		`{"scan_range":{"bsc":500}}`,
		`{"credit_min_amount":0.01}`,
		`{"credit_max_amount":5000}`,
		`{"sweep_global_threshold":2000,"credit_min_amount":0.01,"credit_max_amount":5000,"scan_range":{"bsc":500},"sweep_min_per_address":{"bsc":5}}`,
		// An unchanged sensitive value is not a sensitive configuration change.
		`{"enabled":false,"credit_multiplier":6.7,"credit_min_amount":0.01}`,
	} {
		t.Run(body, func(t *testing.T) {
			store := &cryptoConfigStore{values: map[string]string{cryptopay.SettingKeyCryptoConfig: `{"rpc":{"bsc":"https://unchanged.example.test"}}`}}
			security := &cryptoConfigTestSecurity{err: errors.New("exempt fields must not consume a TOTP code")}
			router := gin.New()
			router.Use(cryptoConfigTestIdentity)
			router.PUT("/config", (&CryptoHandler{store: store, security: security}).AdminUpdateConfig)
			w := httptest.NewRecorder()
			router.ServeHTTP(w, httptest.NewRequest(http.MethodPut, "/config", strings.NewReader(body)))
			require.Equal(t, http.StatusOK, w.Code, w.Body.String())
			require.Zero(t, security.calls.Load())
			require.Equal(t, "https://unchanged.example.test", gjson.Get(store.values[cryptopay.SettingKeyCryptoConfig], "rpc.bsc").String())
		})
	}
}

func TestCryptoConfigSensitiveChangesVerifyBeforeNotificationAndPersistence(t *testing.T) {
	t.Setenv("CRYPTO_ADMIN_TEST_MODE", "false")
	for _, body := range []string{
		`{"enabled":true}`,
		`{"chains":["ethereum"]}`,
		`{"rpc":{"bsc":"https://new.example.test"}}`,
		`{"gas_topup":{"bsc":0.003}}`,
		`{"hot_wallet_evm":"0x2222222222222222222222222222222222222222"}`,
		`{"hot_wallet_solana":"So11111111111111111111111111111111111111112"}`,
		`{"confirmations":{"bsc":4}}`,
		`{"credit_multiplier":7}`,
		`{"sweep_auto":true}`,
		`{"sweep_provider":"bitget"}`,
		`{"bitget_max_fee":"0.75"}`,
		`{"bitget_max_fee_bps":200}`,
		`{"credit_min_amount":0.01,"rpc":{"bsc":"https://new.example.test"}}`,
	} {
		for _, failure := range []struct {
			name   string
			err    error
			status int
		}{
			{"missing code", service.ErrCryptoTotpRequired, http.StatusForbidden},
			{"unbound authenticator", service.ErrCryptoTotpNotSetup, http.StatusForbidden},
			{"replayed code", service.ErrCryptoTotpReplayed, http.StatusConflict},
		} {
			t.Run(body+"/"+failure.name, func(t *testing.T) {
				store := &cryptoConfigStore{values: map[string]string{cryptopay.SettingKeyCryptoConfig: `{}`}}
				security := &cryptoConfigTestSecurity{err: failure.err}
				pushes := 0
				h := &CryptoHandler{store: store, security: security, operationNotifier: operationNotificationFunc(func(context.Context, string, string) error { pushes++; return nil })}
				router := gin.New()
				router.Use(cryptoConfigTestIdentity)
				router.PUT("/config", h.AdminUpdateConfig)
				w := httptest.NewRecorder()
				router.ServeHTTP(w, httptest.NewRequest(http.MethodPut, "/config", strings.NewReader(body)))
				require.Equal(t, failure.status, w.Code, w.Body.String())
				require.Equal(t, int32(1), security.calls.Load())
				require.Zero(t, pushes)
				require.Empty(t, store.writes)
			})
		}
	}
}

func TestCryptoConfigMissingVerifierCannotAuthorizeSensitiveChange(t *testing.T) {
	t.Setenv("CRYPTO_ADMIN_TEST_MODE", "false")
	store := &cryptoConfigStore{values: map[string]string{cryptopay.SettingKeyCryptoConfig: `{}`}}
	router := gin.New()
	router.Use(cryptoConfigTestIdentity)
	router.PUT("/config", (&CryptoHandler{store: store}).AdminUpdateConfig)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, httptest.NewRequest(http.MethodPut, "/config", strings.NewReader(`{"rpc":{"bsc":"https://new.example.test"}}`)))
	require.Equal(t, http.StatusServiceUnavailable, w.Code, w.Body.String())
	require.Empty(t, store.writes)
}

func TestCryptoConfigSuccessfulDestinationNoticeFollowsFreshVerificationAndPrecedesCAS(t *testing.T) {
	t.Setenv("CRYPTO_ADMIN_TEST_MODE", "false")
	store := &cryptoConfigStore{values: map[string]string{cryptopay.SettingKeyCryptoConfig: `{}`}}
	security := &cryptoConfigTestSecurity{}
	notifications := 0
	h := &CryptoHandler{store: store, security: security, operationNotifier: operationNotificationFunc(func(_ context.Context, action, phase string) error {
		notifications++
		require.Equal(t, int32(1), security.calls.Load())
		require.Equal(t, "destination_change", action)
		require.Equal(t, "verified", phase)
		require.Empty(t, store.writes, "the verified notice must precede persistence")
		return nil
	})}
	router := gin.New()
	router.Use(cryptoConfigTestIdentity)
	router.PUT("/config", h.AdminUpdateConfig)
	req := httptest.NewRequest(http.MethodPut, "/config", strings.NewReader(`{"hot_wallet_evm":"0x2222222222222222222222222222222222222222"}`))
	req.Header.Set("X-Crypto-TOTP", "123456")
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	require.Equal(t, int32(1), security.calls.Load())
	require.Equal(t, 1, notifications)
	require.Equal(t, []string{cryptopay.SettingKeyCryptoConfig}, store.writes)
}
