package handler

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/cryptopay"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

// fakeConfigReloader stands in for the crypto service's bounded apply. The real
// lock behaviour (sweepMu, coalescing, disconnects, Close) is pinned in package
// cryptopay; these tests pin what the handler promises around it.
type fakeConfigReloader struct {
	applied bool
	calls   int
	waited  time.Duration
	onCall  func()
}

func (f *fakeConfigReloader) ReloadWithin(_ context.Context, wait time.Duration) bool {
	f.calls++
	f.waited = wait
	if f.onCall != nil {
		f.onCall()
	}
	return f.applied
}

// putCryptoConfig sends one PUT and fails the test, instead of hanging it, when
// the handler does not answer: that is the regression these tests guard against.
func putCryptoConfig(t *testing.T, h *CryptoHandler, body string) *httptest.ResponseRecorder {
	t.Helper()
	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.Use(cryptoConfigTestIdentity)
	router.PUT("/config", h.AdminUpdateConfig)
	w := httptest.NewRecorder()
	served := make(chan struct{})
	go func() {
		router.ServeHTTP(w, httptest.NewRequest(http.MethodPut, "/config", strings.NewReader(body)))
		close(served)
	}()
	select {
	case <-served:
	case <-time.After(15 * time.Second):
		t.Fatal("the config save never answered: it is hanging behind the sweep")
	}
	return w
}

func newConfigApplyHandler(reloader cryptoConfigReloader) (*CryptoHandler, *cryptoConfigStore) {
	store := &cryptoConfigStore{values: map[string]string{cryptopay.SettingKeyCryptoConfig: `{}`}}
	return &CryptoHandler{store: store, security: &cryptoConfigTestSecurity{}, reloader: reloader}, store
}

// A manual sweep owns the service for minutes. The save is stored, so the answer
// must come back after a short bounded wait with applied=false (the console then
// says "saved, applies when the run ends") instead of hanging into a network error.
func TestAdminUpdateConfigAnswersAppliedFalseInsteadOfWaitingForASweep(t *testing.T) {
	t.Setenv("CRYPTO_ADMIN_TEST_MODE", "false")
	fake := &fakeConfigReloader{applied: false}
	h, store := newConfigApplyHandler(fake)
	fake.onCall = func() {
		require.Contains(t, store.values[cryptopay.SettingKeyCryptoConfig], `"sweep_global_threshold":2000`,
			"the config must be stored before the apply is attempted")
	}

	w := putCryptoConfig(t, h, `{"sweep_global_threshold":2000}`)

	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	require.JSONEq(t, `{"data":{"saved":true,"applied":false}}`, w.Body.String())
	require.Equal(t, 1, fake.calls)
	require.Positive(t, fake.waited)
	require.LessOrEqual(t, fake.waited, 3*time.Second, "the request must never wait longer than this for the apply")
}

func TestAdminUpdateConfigReportsAppliedWhenTheServicePickedItUp(t *testing.T) {
	t.Setenv("CRYPTO_ADMIN_TEST_MODE", "false")
	fake := &fakeConfigReloader{applied: true}
	h, _ := newConfigApplyHandler(fake)

	w := putCryptoConfig(t, h, `{"sweep_global_threshold":2000}`)

	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	require.JSONEq(t, `{"data":{"saved":true,"applied":true}}`, w.Body.String())
	require.Equal(t, 1, fake.calls)
}

func TestAdminUpdateConfigWithoutARunningServiceHasNothingPending(t *testing.T) {
	t.Setenv("CRYPTO_ADMIN_TEST_MODE", "false")
	h, _ := newConfigApplyHandler(nil)

	w := putCryptoConfig(t, h, `{"sweep_global_threshold":2000}`)

	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	require.True(t, gjson.Get(w.Body.String(), "data.saved").Bool(), "older consoles read only saved")
	require.True(t, gjson.Get(w.Body.String(), "data.applied").Bool())
}

func TestAdminUpdateConfigNeverAppliesARejectedSave(t *testing.T) {
	t.Setenv("CRYPTO_ADMIN_TEST_MODE", "false")
	fake := &fakeConfigReloader{applied: true}
	h, store := newConfigApplyHandler(fake)

	// Unparseable payload, and a sensitive change without the fresh code.
	for _, body := range []string{`{"sweep_global_threshold":`, `{"credit_multiplier":9}`} {
		w := putCryptoConfig(t, h, body)
		require.NotEqual(t, http.StatusOK, w.Code, body)
	}

	require.Zero(t, fake.calls)
	require.Empty(t, store.writes)
}

func TestAdminUpdateSweepDestinationReportsWhetherTheApplyFinished(t *testing.T) {
	for name, applied := range map[string]bool{"applied": true, "delayed by a sweep": false} {
		t.Run(name, func(t *testing.T) {
			store := &cryptoConfigStore{values: map[string]string{
				cryptopay.SettingKeyCryptoConfig: `{"enabled":true,"chains":["bsc"],"hot_wallet_evm":"` + sweepHandlerOriginalEVM + `"}`,
			}}
			fake := &fakeConfigReloader{applied: applied}
			h := &CryptoHandler{store: store, security: &cryptoConfigTestSecurity{}, reloader: fake}
			gin.SetMode(gin.TestMode)
			router := gin.New()
			router.Use(cryptoConfigTestIdentity)
			router.POST("/sweep-destination", h.AdminUpdateSweepDestination)

			recorder := postSweepDestination(t, router, sweepDestinationBody(sweepHandlerChangedEVM, "123456", SignerRiskPhraseZH, true))

			require.Equal(t, http.StatusOK, recorder.Code, recorder.Body.String())
			require.True(t, gjson.Get(recorder.Body.String(), "data.saved").Bool())
			require.Equal(t, applied, gjson.Get(recorder.Body.String(), "data.applied").Bool())
			require.Equal(t, 1, fake.calls)
			require.LessOrEqual(t, fake.waited, 3*time.Second)
			require.Equal(t, sweepHandlerChangedEVM, loadSweepDestinationConfig(t, store).HotWalletEVM, "stored before the apply")
		})
	}
}

func TestAdminUpdateSweepDestinationNeverAppliesARejectedChange(t *testing.T) {
	store := &cryptoConfigStore{values: map[string]string{
		cryptopay.SettingKeyCryptoConfig: `{"enabled":true,"chains":["bsc"],"hot_wallet_evm":"` + sweepHandlerOriginalEVM + `"}`,
	}}
	fake := &fakeConfigReloader{applied: true}
	h := &CryptoHandler{store: store, security: &cryptoConfigTestSecurity{}, reloader: fake}
	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.Use(cryptoConfigTestIdentity)
	router.POST("/sweep-destination", h.AdminUpdateSweepDestination)

	recorder := postSweepDestination(t, router, sweepDestinationBody(sweepHandlerChangedEVM, "000000", SignerRiskPhraseZH, true))

	require.Equal(t, http.StatusForbidden, recorder.Code, recorder.Body.String())
	require.Zero(t, fake.calls)
}

// A signer or vault change that cannot get the service lock because a sweep is
// running must answer with a stable, retryable code, not a 500 or a hang.
func TestAdminSignerUnlockAnswersBusyWhileASweepRuns(t *testing.T) {
	fake := &fakeSignerUnlock{err: cryptopay.ErrSweepBusy}
	h := &CryptoHandler{signer: fake, security: &cryptoConfigTestSecurity{}}

	recorder := postSignerUnlock(t, newSignerUnlockTestRouter(h), `{"share":"S2V1-SHARE","totp_code":"123456"}`)

	require.Equal(t, http.StatusConflict, recorder.Code, recorder.Body.String())
	require.Contains(t, recorder.Body.String(), "CRYPTO_SWEEP_BUSY")
	require.NotContains(t, recorder.Body.String(), "S2V1-SHARE")
}
