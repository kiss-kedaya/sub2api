package handler

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/cryptopay"
	"github.com/Wei-Shaw/sub2api/internal/cryptopay/mode"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func scanRunContext(method, target, body string) (*gin.Context, *httptest.ResponseRecorder) {
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(method, target, strings.NewReader(body))
	c.Request.Header.Set("Content-Type", "application/json")
	return c, w
}

func TestScanStartFailureMapsToFixedStatusesAndMessages(t *testing.T) {
	for name, tc := range map[string]struct {
		err    error
		status int
	}{
		"job running":   {cryptopay.ErrScanJobRunning, http.StatusConflict},
		"lock busy":     {fmt.Errorf("wrapped: %w", cryptopay.ErrScanBusy), http.StatusConflict},
		"read only":     {mode.ErrReadOnly, http.StatusForbidden},
		"unavailable":   {cryptopay.ErrScanUnavailable, http.StatusServiceUnavailable},
		"anything else": {errors.New("dial https://rpc.example/key123"), http.StatusServiceUnavailable},
	} {
		t.Run(name, func(t *testing.T) {
			status, message := scanStartFailure(tc.err)
			require.Equal(t, tc.status, status)
			require.NotEmpty(t, message)
			require.NotContains(t, message, "key123")
			require.NotContains(t, message, "rpc.example")
		})
	}
}

func TestScanCancelFailureMapsToFixedStatusesAndMessages(t *testing.T) {
	status, _ := scanCancelFailure(cryptopay.ErrScanRunNotFound)
	require.Equal(t, http.StatusNotFound, status)
	status, _ = scanCancelFailure(fmt.Errorf("x: %w", cryptopay.ErrScanRunFinished))
	require.Equal(t, http.StatusConflict, status)
	status, message := scanCancelFailure(errors.New("dial https://rpc.example/key123"))
	require.Equal(t, http.StatusInternalServerError, status)
	require.NotContains(t, message, "key123")
}

func TestScanRunEndpointsOnAnEmptyService(t *testing.T) {
	h := &CryptoHandler{service: &cryptopay.Service{}}

	c, w := scanRunContext(http.MethodGet, "/api/v1/admin/crypto/scan/runs", "")
	h.AdminScanRuns(c)
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	var listed struct {
		Data []json.RawMessage `json:"data"`
	}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &listed))
	require.NotNil(t, listed.Data, "no runs must encode as [] and not null")
	require.Empty(t, listed.Data)

	c, w = scanRunContext(http.MethodGet, "/api/v1/admin/crypto/scan/runs/scan-missing", "")
	c.Params = gin.Params{{Key: "id", Value: "scan-missing"}}
	h.AdminScanRunStatus(c)
	require.Equal(t, http.StatusNotFound, w.Code)

	c, w = scanRunContext(http.MethodPost, "/api/v1/admin/crypto/scan/runs/scan-missing/cancel", "")
	c.Params = gin.Params{{Key: "id", Value: "scan-missing"}}
	h.AdminCancelScanRun(c)
	require.Equal(t, http.StatusNotFound, w.Code)
}

func TestScanRunEndpointsWithoutAServiceAreBadRequests(t *testing.T) {
	h := &CryptoHandler{}
	for name, call := range map[string]func(*gin.Context){
		"status": h.AdminScanRunStatus,
		"list":   h.AdminScanRuns,
		"cancel": h.AdminCancelScanRun,
	} {
		t.Run(name, func(t *testing.T) {
			c, w := scanRunContext(http.MethodGet, "/x", "")
			call(c)
			require.Equal(t, http.StatusBadRequest, w.Code)
		})
	}
}

func TestAsyncScanReportsAnUnavailableScannerInsteadOfAccepting(t *testing.T) {
	t.Setenv("CRYPTO_ADMIN_TEST_MODE", "false")
	h := &CryptoHandler{service: &cryptopay.Service{}}
	before := len((&cryptopay.Service{}).ScanRuns())
	c, w := scanRunContext(http.MethodPost, "/api/v1/admin/crypto/scan/run?async=1", `{"from_block":0}`)
	h.AdminRunScan(c)
	require.Equal(t, http.StatusServiceUnavailable, w.Code, w.Body.String())
	require.Contains(t, w.Body.String(), "Payment scanner unavailable")
	require.Len(t, (&cryptopay.Service{}).ScanRuns(), before, "a refused start must not leave a run behind")
}

func TestScanWithoutAsyncKeepsTheSynchronousContract(t *testing.T) {
	t.Setenv("CRYPTO_ADMIN_TEST_MODE", "false")
	h := &CryptoHandler{service: &cryptopay.Service{}}
	for _, target := range []string{
		"/api/v1/admin/crypto/scan/run",
		"/api/v1/admin/crypto/scan/run?async=0",
		"/api/v1/admin/crypto/scan/run?async=true",
	} {
		c, w := scanRunContext(http.MethodPost, target, `{"from_block":0}`)
		h.AdminRunScan(c)
		require.Equal(t, http.StatusServiceUnavailable, w.Code, target)
		require.Contains(t, w.Body.String(), "Payment scan did not complete", "%s must take the old one-pass path", target)
	}
}

func TestAsyncScanStillRequiresTheSecondFactorForARewind(t *testing.T) {
	t.Setenv("CRYPTO_ADMIN_TEST_MODE", "false")
	// No security verifier: requireCryptoRisk must fail closed before any run starts.
	h := &CryptoHandler{service: &cryptopay.Service{}}
	c, w := scanRunContext(http.MethodPost, "/api/v1/admin/crypto/scan/run?async=1", `{"from_block":12345}`)
	h.AdminRunScan(c)
	require.Equal(t, http.StatusServiceUnavailable, w.Code, w.Body.String())
	require.Contains(t, w.Body.String(), "CRYPTO_TOTP_REQUIRED")
	require.NotContains(t, w.Body.String(), "Payment scanner unavailable", "the step-up check must run before the async start")
}
