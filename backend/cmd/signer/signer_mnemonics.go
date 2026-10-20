package main

import (
	"context"
	"net/http"
	"strings"
	"time"

	"github.com/tyler-smith/go-bip39"
)

// handleExportMnemonics returns plaintext only for this explicit request.
// The response is no-store and the durable audit must succeed before any
// mnemonic bytes are written to the unix-socket client.
func (s *server) handleExportMnemonics(w http.ResponseWriter, r *http.Request) {
	const op = "mnemonic_export"
	vaultResponseHeaders(w)
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", http.MethodPost)
		s.denyVaultRequest(w, http.StatusMethodNotAllowed, op, "method not allowed")
		return
	}
	var req struct {
		Passphrase string `json:"passphrase"`
	}
	if err := decodeVaultRequest(w, r, &req); err != nil || !validVaultPassphrase(req.Passphrase) {
		s.denyVaultRequest(w, http.StatusBadRequest, op, "one JSON object with a nonempty passphrase of at most 1024 bytes is required; body limit 4KB")
		return
	}
	defer func() { req.Passphrase = "" }()
	if !s.admitVaultRequest(w, op) {
		return
	}
	badPassphrase := false
	defer func() { s.backup.finish(badPassphrase) }()

	// Serialize with unlock/import while re-authenticating the persisted vault.
	// An in-memory unlocked signer is deliberately not sufficient authorization.
	s.vaultMu.Lock()
	defer s.vaultMu.Unlock()
	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()
	raw, err := s.readExistingBackupVault(ctx)
	if err != nil {
		s.denyVaultRequest(w, http.StatusServiceUnavailable, op, "existing encrypted vault unavailable")
		return
	}
	contents, err := openVaultString(raw, req.Passphrase)
	if err != nil {
		badPassphrase = true
		s.denyVaultRequest(w, http.StatusForbidden, op, "vault authentication failed")
		return
	}
	defer func() { contents.Deposit, contents.Hot = "", "" }()
	contents.Deposit = strings.Join(strings.Fields(contents.Deposit), " ")
	contents.Hot = strings.Join(strings.Fields(contents.Hot), " ")
	if !bip39.IsMnemonicValid(contents.Deposit) || !bip39.IsMnemonicValid(contents.Hot) || contents.Deposit == contents.Hot {
		s.denyVaultRequest(w, http.StatusServiceUnavailable, op, "encrypted vault contents are invalid")
		return
	}
	if err := s.recordVaultAudit(ctx, op, "success"); err != nil {
		s.denyVaultRequest(w, http.StatusServiceUnavailable, op, "vault audit unavailable")
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{
		"deposit_mnemonic": contents.Deposit,
		"hot_mnemonic":     contents.Hot,
	})
}
