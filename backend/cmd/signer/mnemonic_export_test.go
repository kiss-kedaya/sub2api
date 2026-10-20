package main

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestMnemonicExportReturnsOnlyTwoFieldsAfterDurableAudit(t *testing.T) {
	s, mock := backupTestServer(t, true)
	fixture := backupTestVault(t)
	expectBackupRead(mock, fixture)
	expectVaultAudit(mock, "mnemonic_export", "success", nil)
	response := backupRequest(t, s, "/backup/mnemonics", http.MethodPost, backupBody(backupTestPassphrase))
	require.Equal(t, http.StatusOK, response.Code, response.Body.String())
	require.Equal(t, "nosniff", response.Header().Get("X-Content-Type-Options"))
	var payload map[string]string
	require.NoError(t, json.Unmarshal(response.Body.Bytes(), &payload))
	require.Len(t, payload, 2)
	require.Equal(t, testDepositMnemonic, payload["deposit_mnemonic"])
	require.Equal(t, testHotMnemonic, payload["hot_mnemonic"])
}

func TestMnemonicExportDoesNotReturnSeedsWhenAuditWriteFails(t *testing.T) {
	s, mock := backupTestServer(t, false)
	fixture := backupTestVault(t)
	expectBackupRead(mock, fixture)
	expectVaultAudit(mock, "mnemonic_export", "success", errors.New("audit db unavailable"))
	expectVaultAudit(mock, "mnemonic_export", "denied", nil)
	response := backupRequest(t, s, "/backup/mnemonics", http.MethodPost, backupBody(backupTestPassphrase))
	require.Equal(t, http.StatusServiceUnavailable, response.Code)
	require.NotContains(t, response.Body.String(), testDepositMnemonic)
	require.NotContains(t, response.Body.String(), testHotMnemonic)
	require.Contains(t, response.Body.String(), "vault audit unavailable")
}

func TestMnemonicExportBadPassphraseAndBodyLimitDoNotEchoInput(t *testing.T) {
	t.Run("bad passphrase", func(t *testing.T) {
		s, mock := backupTestServer(t, true)
		expectBackupRead(mock, backupTestVault(t))
		expectVaultAudit(mock, "mnemonic_export", "denied", nil)
		response := backupRequest(t, s, "/backup/mnemonics", http.MethodPost, backupBody("secret wrong passphrase"))
		require.Equal(t, http.StatusForbidden, response.Code)
		require.NotContains(t, response.Body.String(), "secret wrong passphrase")
		require.NotContains(t, response.Body.String(), testDepositMnemonic)
	})
	t.Run("body over 4KB", func(t *testing.T) {
		s, mock := backupTestServer(t, false)
		expectVaultAudit(mock, "mnemonic_export", "denied", nil)
		body := `{"passphrase":"` + strings.Repeat("s", 5<<10) + `"}`
		response := backupRequest(t, s, "/backup/mnemonics", http.MethodPost, body)
		require.Equal(t, http.StatusBadRequest, response.Code)
		require.NotContains(t, response.Body.String(), strings.Repeat("s", 80))
	})
}
