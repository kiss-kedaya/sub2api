package handler

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/cryptopay"
	"github.com/Wei-Shaw/sub2api/internal/cryptopay/wallet"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

// fakeSignerUnlock records the share the handler hands to the service. The
// embedded nil interface satisfies the rest of cryptoSignerService: a test that
// calls another method is a bug, and would panic loudly.
type fakeSignerUnlock struct {
	cryptoSignerService
	status *wallet.ShardStatus
	err    error
	share  string
	calls  int
}

func (f *fakeSignerUnlock) UnlockSignerWindow(_ context.Context, share string) (*wallet.ShardStatus, error) {
	f.calls++
	f.share = share
	return f.status, f.err
}

func newSignerUnlockTestRouter(h *CryptoHandler) *gin.Engine {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.Use(cryptoConfigTestIdentity)
	router.POST("/signer/unlock", h.AdminSignerUnlock)
	return router
}

func postSignerUnlock(t *testing.T, router http.Handler, body string) *httptest.ResponseRecorder {
	t.Helper()
	request := httptest.NewRequest(http.MethodPost, "/signer/unlock", strings.NewReader(body))
	request.Header.Set("Content-Type", "application/json")
	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, request)
	return recorder
}

func decodedSignerStatus(t *testing.T, recorder *httptest.ResponseRecorder) map[string]any {
	t.Helper()
	var body struct {
		Data map[string]any `json:"data"`
	}
	require.NoError(t, json.Unmarshal(recorder.Body.Bytes(), &body))
	return body.Data
}

func TestAdminSignerUnlockReturnsTheBoundedWindow(t *testing.T) {
	fake := &fakeSignerUnlock{status: &wallet.ShardStatus{
		Configured:         true,
		Scheme:             "shamir-gf256",
		Threshold:          2,
		Total:              3,
		VaultVersion:       3,
		Locked:             false,
		UnlockedUntil:      "2026-11-23T01:15:00Z",
		UnlockLeaseSeconds: 899,
	}}
	h := &CryptoHandler{signer: fake, security: &cryptoConfigTestSecurity{}}
	recorder := postSignerUnlock(t, newSignerUnlockTestRouter(h), `{"share":" S2V1-SHARE ","totp_code":"123456"}`)
	require.Equal(t, http.StatusOK, recorder.Code, recorder.Body.String())
	require.Equal(t, 1, fake.calls)
	require.Equal(t, "S2V1-SHARE", fake.share)

	data := decodedSignerStatus(t, recorder)
	require.Equal(t, false, data["locked"])
	require.Equal(t, "2026-11-23T01:15:00Z", data["unlocked_until"])
	require.EqualValues(t, 899, data["unlock_lease_seconds"])
}

func TestAdminSignerUnlockRequiresAFreshCode(t *testing.T) {
	fake := &fakeSignerUnlock{status: &wallet.ShardStatus{}}
	h := &CryptoHandler{signer: fake, security: &cryptoConfigTestSecurity{}}
	recorder := postSignerUnlock(t, newSignerUnlockTestRouter(h), `{"share":"S2V1-SHARE"}`)
	require.Equal(t, http.StatusBadRequest, recorder.Code, recorder.Body.String())
	require.Contains(t, recorder.Body.String(), "CRYPTO_SIGNER_TOTP_REQUIRED")
	require.Zero(t, fake.calls)
}

func TestAdminSignerUnlockMapsARejectedShareToAStableCode(t *testing.T) {
	fake := &fakeSignerUnlock{err: wallet.ErrSignerShareInvalid}
	h := &CryptoHandler{signer: fake, security: &cryptoConfigTestSecurity{}}
	recorder := postSignerUnlock(t, newSignerUnlockTestRouter(h), `{"share":"S2V1-WRONG","totp_code":"123456"}`)
	require.Equal(t, http.StatusBadRequest, recorder.Code, recorder.Body.String())
	require.Contains(t, recorder.Body.String(), "CRYPTO_SIGNER_SHARE_INVALID")
}

// fakeSweepGate answers with a canned verdict so the handler contract (shape,
// cache headers, unavailable service) is pinned without a live wallet.
type fakeSweepGate struct {
	gate *cryptopay.SweepGate
	err  error
}

func (f *fakeSweepGate) SweepGate(context.Context) (*cryptopay.SweepGate, error) {
	return f.gate, f.err
}

func newSweepReadinessTestRouter(h *CryptoHandler) *gin.Engine {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.Use(cryptoConfigTestIdentity)
	router.GET("/sweep/readiness", h.AdminSweepReadiness)
	return router
}

func TestAdminSweepReadinessRendersTheBackendVerdict(t *testing.T) {
	fake := &fakeSweepGate{gate: &cryptopay.SweepGate{
		Ready:                 false,
		Reason:                cryptopay.SweepGateSignerLocked,
		ChannelEnabled:        true,
		WalletOK:              true,
		WalletReady:           false,
		SignerLocked:          true,
		DestinationConfigured: true,
		DestinationLocked:     true,
	}}
	h := &CryptoHandler{sweepGate: fake}
	recorder := httptest.NewRecorder()
	newSweepReadinessTestRouter(h).ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/sweep/readiness", nil))
	require.Equal(t, http.StatusOK, recorder.Code, recorder.Body.String())

	var body struct {
		Data cryptopay.SweepGate `json:"data"`
	}
	require.NoError(t, json.Unmarshal(recorder.Body.Bytes(), &body))
	require.Equal(t, cryptopay.SweepGateSignerLocked, body.Data.Reason)
	require.False(t, body.Data.Ready)
	require.True(t, body.Data.SignerLocked)
	require.True(t, body.Data.DestinationLocked)
}

func TestAdminSweepReadinessWithoutAServiceIsUnavailable(t *testing.T) {
	h := &CryptoHandler{}
	recorder := httptest.NewRecorder()
	newSweepReadinessTestRouter(h).ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/sweep/readiness", nil))
	require.Equal(t, http.StatusServiceUnavailable, recorder.Code, recorder.Body.String())
}
