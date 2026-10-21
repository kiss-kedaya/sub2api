package handler

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestCryptoVaultRejectsAmbiguousOrOversizedJSON(t *testing.T) {
	for name, body := range map[string]string{
		"concatenated":     `{"passphrase":"example"}{"passphrase":"other"}`,
		"unknown property": `{"passphrase":"example","deposit_mnemonic":"must not rotate"}`,
		"oversized":        `{"passphrase":"` + strings.Repeat("x", 5<<10) + `"}`,
		"truncated":        `{"passphrase":`,
	} {
		t.Run(name, func(t *testing.T) {
			w := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(w)
			c.Request = httptest.NewRequest(http.MethodPost, "/", strings.NewReader(body))
			h := &CryptoHandler{}
			h.AdminExportVaultBackup(c)
			require.Equal(t, http.StatusBadRequest, w.Code)
			require.Contains(t, w.Header().Get("Cache-Control"), "no-store")
			require.NotContains(t, w.Body.String(), "must not rotate")
		})
	}
}

func TestCryptoVaultDoesNotEchoSensitiveRequestOnUnavailableWallet(t *testing.T) {
	for _, endpoint := range []string{"backup", "import"} {
		t.Run(endpoint, func(t *testing.T) {
			w := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(w)
			body := `{"passphrase":"private vault password"}`
			if endpoint == "import" {
				body = `{"passphrase":"private vault password","hot_mnemonic":"private mnemonic"}`
			}
			c.Request = httptest.NewRequest(http.MethodPost, "/", strings.NewReader(body))
			h := &CryptoHandler{}
			if endpoint == "import" {
				h.AdminImportHotMnemonic(c)
			} else {
				h.AdminExportVaultBackup(c)
			}
			require.Equal(t, http.StatusServiceUnavailable, w.Code)
			require.NotContains(t, w.Body.String(), "private")
			require.Empty(t, w.Header().Get("Content-Disposition"))
			require.Contains(t, w.Header().Get("Cache-Control"), "no-store")
		})
	}
}

func TestAdminUnlockWalletRejectsAmbiguousOrOversizedSecrets(t *testing.T) {
	for name, body := range map[string]string{
		"unknown property": `{"passphrase":"example","unexpected":"value"}`,
		"concatenated":     `{"passphrase":"example"}{"passphrase":"other"}`,
		"oversized":        `{"passphrase":"` + strings.Repeat("x", 5<<10) + `"}`,
		"partial seeds":    `{"passphrase":"example","deposit_mnemonic":"seed words"}`,
	} {
		t.Run(name, func(t *testing.T) {
			w := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(w)
			c.Request = httptest.NewRequest(http.MethodPost, "/", strings.NewReader(body))
			(&CryptoHandler{}).AdminUnlockWallet(c)
			require.Equal(t, http.StatusBadRequest, w.Code)
			require.Contains(t, w.Header().Get("Cache-Control"), "no-store")
			require.NotContains(t, w.Body.String(), "example")
		})
	}
}

func TestAdminUnlockWalletDoesNotEchoSecretsWhenUnavailable(t *testing.T) {
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodPost, "/", strings.NewReader(`{"passphrase":"private vault password"}`))
	(&CryptoHandler{}).AdminUnlockWallet(c)
	require.Equal(t, http.StatusServiceUnavailable, w.Code)
	require.NotContains(t, w.Body.String(), "private vault password")
	require.Contains(t, w.Header().Get("Cache-Control"), "no-store")
}
