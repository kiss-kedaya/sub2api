package handler

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/cryptopay"
	"github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

const (
	sweepHandlerOriginalEVM = "0x1111111111111111111111111111111111111111"
	sweepHandlerChangedEVM  = "0x2222222222222222222222222222222222222222"
)

// newSweepDestinationTestRouter builds the two routes that can touch the sweep
// destination, behind the shared audit middleware.
func newSweepDestinationTestRouter(t *testing.T, store cryptopay.SettingStore, audit *service.AuditLogService) *gin.Engine {
	t.Helper()
	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.Use(cryptoConfigTestIdentity)
	if audit != nil {
		// The audit writer is asynchronous; tests must call Stop before
		// inspecting the capture.
		router.Use(gin.HandlerFunc(middleware.NewAuditLogMiddleware(audit)))
	}
	h := &CryptoHandler{store: store, security: &cryptoConfigTestSecurity{}}
	router.POST("/sweep-destination", h.AdminUpdateSweepDestination)
	router.PUT("/config", h.AdminUpdateConfig)
	return router
}

func sweepDestinationBody(evm, code, phrase string, acknowledge bool) string {
	payload := map[string]any{
		"hot_wallet_evm":   evm,
		"totp_code":        code,
		"risk_phrase":      phrase,
		"acknowledge_risk": acknowledge,
	}
	encoded, _ := json.Marshal(payload)
	return string(encoded)
}

func postSweepDestination(t *testing.T, router http.Handler, body string) *httptest.ResponseRecorder {
	t.Helper()
	request := httptest.NewRequest(http.MethodPost, "/sweep-destination", strings.NewReader(body))
	request.Header.Set("Content-Type", "application/json")
	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, request)
	return recorder
}

func loadSweepDestinationConfig(t *testing.T, store cryptopay.SettingStore) cryptopay.CryptoConfig {
	t.Helper()
	cfg, err := cryptopay.LoadConfig(context.Background(), store)
	require.NoError(t, err)
	return cfg
}

func TestAdminUpdateSweepDestinationRejectsUnverifiedRequests(t *testing.T) {
	store := &cryptoConfigStore{values: map[string]string{
		cryptopay.SettingKeyCryptoConfig: `{"enabled":true,"chains":["bsc"],"hot_wallet_evm":"` + sweepHandlerOriginalEVM + `"}`,
	}}
	router := newSweepDestinationTestRouter(t, store, nil)

	for name, tc := range map[string]struct {
		body   string
		status int
		reason string
	}{
		"missing acknowledgement": {
			body:   sweepDestinationBody(sweepHandlerChangedEVM, "123456", SignerRiskPhraseZH, false),
			status: http.StatusBadRequest,
			reason: "CRYPTO_SIGNER_RISK_ACK_REQUIRED",
		},
		"wrong risk phrase": {
			body:   sweepDestinationBody(sweepHandlerChangedEVM, "123456", "I did not read the notice", true),
			status: http.StatusBadRequest,
			reason: "CRYPTO_SIGNER_RISK_ACK_REQUIRED",
		},
		"wrong authenticator code": {
			body:   sweepDestinationBody(sweepHandlerChangedEVM, "000000", SignerRiskPhraseZH, true),
			status: http.StatusForbidden,
			reason: "CRYPTO_TOTP_REQUIRED",
		},
		"invalid address format": {
			body:   sweepDestinationBody("0x1234", "123456", SignerRiskPhraseZH, true),
			status: http.StatusBadRequest,
			reason: "SWEEP_DESTINATION_INVALID",
		},
	} {
		t.Run(name, func(t *testing.T) {
			recorder := postSweepDestination(t, router, tc.body)
			require.Equal(t, tc.status, recorder.Code, recorder.Body.String())
			require.Contains(t, recorder.Body.String(), tc.reason)
		})
	}

	// Nothing was written by any rejected attempt.
	require.Empty(t, store.writes)
	stored := loadSweepDestinationConfig(t, store)
	require.Equal(t, sweepHandlerOriginalEVM, stored.HotWalletEVM)
	require.Nil(t, stored.SweepDestinationLock)
}

func TestAdminUpdateSweepDestinationChangesAddressAndAuditsIt(t *testing.T) {
	store := &cryptoConfigStore{values: map[string]string{
		cryptopay.SettingKeyCryptoConfig: `{"enabled":true,"chains":["bsc"],"hot_wallet_evm":"` + sweepHandlerOriginalEVM + `"}`,
	}}
	capture := &cryptoAuditCapture{}
	audit := service.NewAuditLogService(capture, nil)
	audit.Start()
	router := newSweepDestinationTestRouter(t, store, audit)

	recorder := postSweepDestination(t, router, sweepDestinationBody(sweepHandlerChangedEVM, "123456", SignerRiskPhraseZH, true))
	require.Equal(t, http.StatusOK, recorder.Code, recorder.Body.String())
	audit.Stop()

	var payload struct {
		Data struct {
			Saved       bool `json:"saved"`
			Changed     bool `json:"changed"`
			Locked      bool `json:"locked"`
			LockCreated bool `json:"lock_created"`
			Lock        struct {
				EVM            string `json:"evm"`
				LockedByUserID int64  `json:"locked_by_user_id"`
			} `json:"sweep_destination_lock"`
		} `json:"data"`
	}
	require.NoError(t, json.Unmarshal(recorder.Body.Bytes(), &payload))
	require.True(t, payload.Data.Saved)
	require.True(t, payload.Data.Changed)
	require.True(t, payload.Data.Locked)
	require.True(t, payload.Data.LockCreated)
	require.Equal(t, sweepHandlerChangedEVM, payload.Data.Lock.EVM)
	require.Equal(t, int64(1), payload.Data.Lock.LockedByUserID)

	stored := loadSweepDestinationConfig(t, store)
	require.Equal(t, sweepHandlerChangedEVM, stored.HotWalletEVM)
	require.NotNil(t, stored.SweepDestinationLock)
	require.Equal(t, sweepHandlerChangedEVM, stored.SweepDestinationLock.EVM)
	require.Equal(t, int64(1), stored.SweepDestinationLock.LockedByUserID)

	require.Len(t, capture.records, 1, "the change must produce exactly one audit record")
	entry := capture.records[0]
	require.Equal(t, auditActionCryptoSweepDestinationChanged, entry.Action)
	require.Equal(t, http.StatusOK, entry.StatusCode)
	require.Equal(t, sweepHandlerOriginalEVM, entry.Extra["hot_wallet_evm_old"])
	require.Equal(t, sweepHandlerChangedEVM, entry.Extra["hot_wallet_evm_new"])
	// The one-time code must never be persisted, in the body or the mask.
	require.NotContains(t, entry.RequestBody, "123456")
	require.NotContains(t, entry.CredentialMasked, "123456")
}

func TestAdminUpdateSweepDestinationIsIdempotent(t *testing.T) {
	store := &cryptoConfigStore{values: map[string]string{
		cryptopay.SettingKeyCryptoConfig: `{"enabled":true,"chains":["bsc"],"hot_wallet_evm":"` + sweepHandlerOriginalEVM + `"}`,
	}}
	router := newSweepDestinationTestRouter(t, store, nil)

	first := postSweepDestination(t, router, sweepDestinationBody(sweepHandlerChangedEVM, "123456", SignerRiskPhraseZH, true))
	require.Equal(t, http.StatusOK, first.Code, first.Body.String())
	lockedAt := loadSweepDestinationConfig(t, store).SweepDestinationLock.LockedAt
	writesAfterFirst := len(store.writes)

	second := postSweepDestination(t, router, sweepDestinationBody(sweepHandlerChangedEVM, "123456", SignerRiskPhraseZH, true))
	require.Equal(t, http.StatusOK, second.Code, second.Body.String())
	require.Contains(t, second.Body.String(), `"changed":false`)
	require.Len(t, store.writes, writesAfterFirst, "an idempotent resubmit must not write")
	require.Equal(t, lockedAt, loadSweepDestinationConfig(t, store).SweepDestinationLock.LockedAt)
}

func TestAdminUpdateConfigRejectsLockedSweepDestination(t *testing.T) {
	lockedAt := time.Date(2026, 11, 22, 9, 0, 0, 0, time.UTC).Format(time.RFC3339)
	store := &cryptoConfigStore{values: map[string]string{
		cryptopay.SettingKeyCryptoConfig: `{"enabled":true,"chains":["bsc"],"hot_wallet_evm":"` + sweepHandlerOriginalEVM +
			`","sweep_destination_lock":{"evm":"` + sweepHandlerOriginalEVM + `","locked_at":"` + lockedAt + `","locked_by_user_id":41}}`,
	}}
	router := newSweepDestinationTestRouter(t, store, nil)

	// The ordinary config save path must refuse to move the pinned destination,
	// even with a valid step-up code supplied.
	request := httptest.NewRequest(http.MethodPut, "/config", strings.NewReader(`{"hot_wallet_evm":"`+sweepHandlerChangedEVM+`"}`))
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("X-Crypto-TOTP", "123456")
	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, request)
	require.Equal(t, http.StatusConflict, recorder.Code, recorder.Body.String())
	require.Contains(t, recorder.Body.String(), "SWEEP_DESTINATION_LOCKED")
	require.Contains(t, recorder.Body.String(), "/admin/crypto/signer/sweep-destination")
	require.Empty(t, store.writes)
	require.Equal(t, sweepHandlerOriginalEVM, loadSweepDestinationConfig(t, store).HotWalletEVM)

	// Re-saving the pinned address (or other fields) still works.
	same := httptest.NewRequest(http.MethodPut, "/config", strings.NewReader(`{"hot_wallet_evm":"`+sweepHandlerOriginalEVM+`","sweep_auto":true}`))
	same.Header.Set("Content-Type", "application/json")
	same.Header.Set("X-Crypto-TOTP", "123456")
	sameRecorder := httptest.NewRecorder()
	router.ServeHTTP(sameRecorder, same)
	require.Equal(t, http.StatusOK, sameRecorder.Code, sameRecorder.Body.String())
	require.True(t, loadSweepDestinationConfig(t, store).SweepAuto)
}

func TestAdminGetConfigMasksNotifyWebhook(t *testing.T) {
	store := &cryptoConfigStore{values: map[string]string{
		cryptopay.SettingKeyCryptoConfig: `{"enabled":true,"chains":["bsc"],"notify":{"enabled":true,` +
			`"webhook_url":"https://api.day.app/placeholder-key","email":"ops@example.test"}}`,
	}}
	h := &CryptoHandler{store: store}
	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodGet, "/api/v1/admin/crypto/config", nil)
	h.AdminGetConfig(c)

	require.Equal(t, http.StatusOK, recorder.Code, recorder.Body.String())
	require.NotContains(t, recorder.Body.String(), "placeholder-key", "the webhook bearer key must not leak to the public settings view")
	require.Contains(t, recorder.Body.String(), "ops@example.test")
}
