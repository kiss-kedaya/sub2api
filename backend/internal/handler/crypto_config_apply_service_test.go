//go:build unit

package handler

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/cryptopay"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

// These tests run the real handler against the real crypto service while a
// "sweep" owns the service lock (HoldSweepForTest), which is exactly what a
// manual sweep does for up to 30 minutes.

// unitSettingsRepo is one in-memory settings row shared by the handler (uncached
// reads + compare-and-swap) and the service (cached reads). Every cached read is
// one service reload, so reloadReads counts reloads without touching the service.
type unitSettingsRepo struct {
	service.SettingRepository // anything else must fail loudly
	mu                        sync.Mutex
	values                    map[string]string
	reloadReads               atomic.Int32
}

func (r *unitSettingsRepo) read(key string) (string, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	value, ok := r.values[key]
	if !ok {
		return "", service.ErrSettingNotFound
	}
	return value, nil
}

func (r *unitSettingsRepo) GetValue(_ context.Context, key string) (string, error) {
	r.reloadReads.Add(1)
	return r.read(key)
}

func (r *unitSettingsRepo) GetValueUncached(_ context.Context, key string) (string, error) {
	return r.read(key)
}

func (r *unitSettingsRepo) Set(_ context.Context, key, value string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.values[key] = value
	return nil
}

func (r *unitSettingsRepo) CompareAndSwapValue(_ context.Context, key string, expected *string, value string) (bool, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	current, exists := r.values[key]
	if (expected == nil && exists) || (expected != nil && (!exists || current != *expected)) {
		return false, nil
	}
	r.values[key] = value
	return true, nil
}

func newRealServiceConfigHandler(t *testing.T, applyWait time.Duration) (*CryptoHandler, *cryptopay.Service, *unitSettingsRepo) {
	t.Helper()
	t.Setenv("CRYPTO_INSTANCE_ENABLED", "false")
	t.Setenv("CRYPTO_ADMIN_TEST_MODE", "false")
	t.Setenv("SUB2API_DISABLE_BACKGROUND_JOBS", "true")
	previousWait := cryptoConfigApplyWait
	cryptoConfigApplyWait = applyWait
	t.Cleanup(func() { cryptoConfigApplyWait = previousWait })

	repo := &unitSettingsRepo{values: map[string]string{cryptopay.SettingKeyCryptoConfig: `{}`}}
	settings := service.NewSettingService(repo, nil)
	svc := cryptopay.NewService(nil, settings, nil, nil, nil)
	t.Cleanup(svc.Close)
	h := &CryptoHandler{store: cryptopay.NewServiceSettingStore(settings), service: svc, security: &cryptoConfigTestSecurity{}}
	return h, svc, repo
}

// holdSweep makes svc look like it is in the middle of a manual sweep until the
// returned func is called (it is also called on test cleanup).
func holdSweep(t *testing.T, svc *cryptopay.Service) (endSweep func()) {
	t.Helper()
	var once sync.Once
	end := svc.HoldSweepForTest()
	endSweep = func() { once.Do(end) }
	t.Cleanup(endSweep)
	return endSweep
}

func liveThreshold(svc *cryptopay.Service) float64 { return svc.Config().SweepGlobalThreshold }

func thresholdPatch(value int) string { return fmt.Sprintf(`{"sweep_global_threshold":%d}`, value) }

// The incident: Save during a running sweep. It used to hang until the browser
// gave up ("Network error") although the config had been stored.
func TestAdminUpdateConfigRealServiceAnswersWhileASweepHoldsTheLock(t *testing.T) {
	h, svc, repo := newRealServiceConfigHandler(t, 200*time.Millisecond)
	endSweep := holdSweep(t, svc)
	require.EqualValues(t, 1000, liveThreshold(svc))

	started := time.Now()
	w := putCryptoConfig(t, h, thresholdPatch(2000))

	require.Less(t, time.Since(started), 5*time.Second, "the save must not wait for the sweep")
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	require.JSONEq(t, `{"data":{"saved":true,"applied":false}}`, w.Body.String())
	stored, err := repo.read(cryptopay.SettingKeyCryptoConfig)
	require.NoError(t, err)
	require.Contains(t, stored, `"sweep_global_threshold":2000`, "the save itself is durable")
	require.EqualValues(t, 1000, liveThreshold(svc), "the running service still uses the old config until the sweep ends")

	endSweep()
	require.Eventually(t, func() bool { return liveThreshold(svc) == 2000 }, 5*time.Second, 10*time.Millisecond,
		"the apply must happen by itself once the sweep releases the lock")
}

func TestAdminUpdateConfigRealServiceFiveSavesDuringASweepCostOneReload(t *testing.T) {
	h, svc, repo := newRealServiceConfigHandler(t, 30*time.Millisecond)
	endSweep := holdSweep(t, svc)
	baseline := repo.reloadReads.Load() // NewService's own startup reload

	for i := 1; i <= 5; i++ {
		w := putCryptoConfig(t, h, thresholdPatch(2000+i))
		require.Equal(t, http.StatusOK, w.Code, w.Body.String())
		require.JSONEq(t, `{"data":{"saved":true,"applied":false}}`, w.Body.String())
	}
	require.EqualValues(t, baseline, repo.reloadReads.Load(), "nothing may reload while the sweep owns the lock")

	endSweep()
	require.Eventually(t, func() bool { return liveThreshold(svc) == 2005 }, 5*time.Second, 10*time.Millisecond,
		"the single reload must pick up the latest of the five saves")
	require.Never(t, func() bool { return repo.reloadReads.Load()-baseline > 1 }, 400*time.Millisecond, 10*time.Millisecond,
		"five saves queued behind one sweep must cost one reload")
	require.EqualValues(t, baseline+1, repo.reloadReads.Load())
}

// The browser gave up (this is what the 499s in the access log were). The save is
// stored and the apply must still happen.
func TestAdminUpdateConfigRealServiceStillAppliesAfterTheClientDisconnects(t *testing.T) {
	h, svc, _ := newRealServiceConfigHandler(t, time.Minute) // only the disconnect can end the wait
	endSweep := holdSweep(t, svc)

	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.Use(cryptoConfigTestIdentity)
	router.PUT("/config", h.AdminUpdateConfig)
	ctx, disconnect := context.WithCancel(t.Context())
	request := httptest.NewRequest(http.MethodPut, "/config", strings.NewReader(thresholdPatch(2000))).WithContext(ctx)
	recorder := httptest.NewRecorder()
	served := make(chan struct{})
	go func() {
		router.ServeHTTP(recorder, request)
		close(served)
	}()

	time.Sleep(100 * time.Millisecond) // the handler is now parked waiting for the apply
	disconnect()
	select {
	case <-served:
	case <-time.After(5 * time.Second):
		t.Fatal("the handler kept waiting after the client disconnected")
	}

	endSweep()
	require.Eventually(t, func() bool { return liveThreshold(svc) == 2000 }, 5*time.Second, 10*time.Millisecond,
		"a disconnected client must not cancel the apply")
}

func TestAdminUpdateConfigRealServiceIsLiveBeforeTheResponseWhenNoSweepRuns(t *testing.T) {
	h, svc, _ := newRealServiceConfigHandler(t, 3*time.Second)

	w := putCryptoConfig(t, h, thresholdPatch(2000))

	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	require.JSONEq(t, `{"data":{"saved":true,"applied":true}}`, w.Body.String())
	require.EqualValues(t, 2000, liveThreshold(svc), "applied=true means the service already uses the new config")
}
