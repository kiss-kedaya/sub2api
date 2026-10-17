// Command signer is the isolated key-holding service for the crypto
// deposit channel. It owns the BIP39 seed and hot-wallet signing, and
// talks to the web process over a local unix socket only.
//
// The web process must never hold seed or key material.
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"math/big"
	"net"
	"net/http"
	"os"
	"runtime"
	"strings"
	"sync"
	"time"
)

// parseBigInt parses a non-negative decimal integer ("" = 0). Raw token
// amounts overflow int64 once decimals are 18 (1 USDT on BSC = 1e18), so
// the limiter works in big.Int throughout.
func parseBigInt(raw string) (*big.Int, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return big.NewInt(0), nil
	}
	v, ok := new(big.Int).SetString(raw, 10)
	if !ok || v.Sign() < 0 {
		return nil, fmt.Errorf("invalid integer %q", raw)
	}
	return v, nil
}

type server struct {
	deriver *Deriver
	// hotDeriver owns the gas-paying wallet: a separate seed so leaking the
	// deposit mnemonic cannot reach the swept funds.
	hotDeriver *Deriver
	policy     *Policy
	limiter    *limiter
	audit      *auditLog

	// vaultMu guards the unlock state. Until unlock the derivers are nil and
	// every signing path answers ErrVaultLocked, so a stolen process image or
	// a restarted host cannot sign without the operator's passphrase.
	vaultMu sync.RWMutex
	// policyMu guards the policy pointer: reloadPolicy swaps it before a
	// signature while other requests read it, so the swap must be atomic.
	policyMu sync.RWMutex
	store    *store
}

func main() {
	var (
		socketPath = flag.String("socket", "/run/sub2api/signer.sock", "unix socket path")
		dbDSN      = flag.String("db", os.Getenv("SIGNER_DB_DSN"), "Postgres DSN; settings hold the vault, policy and daily state")
		genSeed    = flag.Bool("gen-seed", false, "generate a new mnemonic and exit")
	)
	flag.Parse()

	if *genSeed {
		mnemonic, err := NewMnemonic()
		if err != nil {
			log.Fatalf("generate mnemonic: %v", err)
		}
		fmt.Println(mnemonic)
		fmt.Fprintln(os.Stderr, "Write this mnemonic down offline. It is the only copy.")
		return
	}

	mnemonic := strings.TrimSpace(os.Getenv("SIGNER_MNEMONIC"))
	hotMnemonic := strings.TrimSpace(os.Getenv("SIGNER_HOT_MNEMONIC"))
	// First run may bootstrap the vault from env vars; afterwards the vault is
	// the only place the seeds live and the passphrase unlocks them.
	var deriver, hotDeriver *Deriver
	if mnemonic != "" && hotMnemonic != "" {
		if hotMnemonic == mnemonic {
			log.Fatal("SIGNER_HOT_MNEMONIC must differ from SIGNER_MNEMONIC")
		}
		var err error
		if deriver, err = NewDeriver(mnemonic); err != nil {
			log.Fatalf("init deriver: %v", err)
		}
		if hotDeriver, err = NewDeriver(hotMnemonic); err != nil {
			log.Fatalf("init hot deriver: %v", err)
		}
	}
	if *dbDSN == "" {
		log.Fatal("SIGNER_DB_DSN (or -db) is required: the vault, policy and audit live in the database")
	}
	st, err := newStore(*dbDSN)
	if err != nil {
		log.Fatalf("init store: %v", err)
	}
	defer func() { _ = st.Close() }()
	policy, err := st.loadPolicyFromDB(context.Background())
	if err != nil {
		log.Fatalf("load policy: %v", err)
	}
	limiter, err := newLimiter(st)
	if err != nil {
		log.Fatalf("init limiter: %v", err)
	}
	srv := &server{
		deriver:    deriver,
		hotDeriver: hotDeriver,
		policy:     policy,
		limiter:    limiter,
		audit:      newAuditLog(st),
		store:      st,
	}

	_ = os.Remove(*socketPath)
	listener, err := net.Listen("unix", *socketPath)
	if err != nil {
		log.Fatalf("listen: %v", err)
	}
	// Windows AF_UNIX does not honor POSIX permissions; skip chmod there so
	// the socket stays connectable. On Linux the 0600 mode restricts access
	// to the service account.
	if runtime.GOOS != "windows" {
		if err := os.Chmod(*socketPath, 0o600); err != nil {
			log.Fatalf("chmod socket: %v", err)
		}
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", srv.handleHealth)
	mux.HandleFunc("/unlock", srv.handleUnlock)
	mux.HandleFunc("/derive", srv.handleDerive)
	mux.HandleFunc("/hot-address", srv.handleHotAddress)
	mux.HandleFunc("/sign/evm", srv.handleSignEVM)
	mux.HandleFunc("/sign/sol", srv.handleSignSol)

	httpServer := &http.Server{
		Handler:           mux,
		ReadHeaderTimeout: 5 * time.Second,
	}
	log.Printf("signer listening on %s", *socketPath)
	if err := httpServer.Serve(listener); err != nil {
		log.Fatalf("serve: %v", err)
	}
}

func writeJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}

func (s *server) handleHealth(w http.ResponseWriter, _ *http.Request) {
	s.vaultMu.RLock()
	unlocked := s.deriver != nil && s.hotDeriver != nil
	s.vaultMu.RUnlock()
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "unlocked": unlocked})
}

// handleUnlock takes the operator passphrase and loads both seeds into
// memory. The passphrase is never stored: it exists only for the scrypt
// derivation that opens the vault.
func (s *server) handleUnlock(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
		return
	}
	var req struct {
		Passphrase string `json:"passphrase"`
		// Init carries the seeds on first run only, to create the vault.
		DepositMnemonic string `json:"deposit_mnemonic,omitempty"`
		HotMnemonic     string `json:"hot_mnemonic,omitempty"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid JSON"})
		return
	}
	if strings.TrimSpace(req.Passphrase) == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "passphrase required"})
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()

	s.vaultMu.Lock()
	defer s.vaultMu.Unlock()

	raw, err := s.store.getSetting(ctx, settingVault)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}

	// First run: seal the provided seeds into a new vault.
	if raw == "" {
		deposit := strings.TrimSpace(req.DepositMnemonic)
		hot := strings.TrimSpace(req.HotMnemonic)
		if deposit == "" || hot == "" {
			writeJSON(w, http.StatusBadRequest, map[string]string{
				"error": "no vault yet: send deposit_mnemonic and hot_mnemonic to create it",
			})
			return
		}
		if deposit == hot {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "hot seed must differ from the deposit seed"})
			return
		}
		sealed, err := sealContents(deposit, hot, req.Passphrase)
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
			return
		}
		if err := s.store.setSetting(ctx, settingVault, sealed); err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
			return
		}
		raw = sealed
		s.audit.write(auditEntry{Op: "vault_init", Result: "ok"})
	}

	contents, err := openVaultString(raw, req.Passphrase)
	if err != nil {
		s.audit.write(auditEntry{Op: "unlock", Result: "denied", Reason: "bad_passphrase"})
		writeJSON(w, http.StatusForbidden, map[string]string{"error": err.Error()})
		return
	}
	depositDeriver, err := NewDeriver(contents.Deposit)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "deposit seed invalid: " + err.Error()})
		return
	}
	hotDeriver, err := NewDeriver(contents.Hot)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "hot seed invalid: " + err.Error()})
		return
	}
	s.deriver = depositDeriver
	s.hotDeriver = hotDeriver
	s.audit.write(auditEntry{Op: "unlock", Result: "ok"})
	writeJSON(w, http.StatusOK, map[string]any{"unlocked": true})
}

type deriveRequest struct {
	Start uint32 `json:"start"`
	Count uint32 `json:"count"`
}

type derivedAddress struct {
	Index  uint32 `json:"index"`
	EVM    string `json:"evm"`
	Solana string `json:"solana"`
}

// requireUnlocked returns the derivers, or writes a 423 and reports false.
// Every path that touches key material goes through here: while locked the
// process holds no seeds at all.
func (s *server) requireUnlocked(w http.ResponseWriter) (*Deriver, *Deriver, bool) {
	s.vaultMu.RLock()
	defer s.vaultMu.RUnlock()
	if s.deriver == nil || s.hotDeriver == nil {
		writeJSON(w, http.StatusLocked, map[string]string{"error": ErrVaultLocked.Error()})
		return nil, nil, false
	}
	return s.deriver, s.hotDeriver, true
}

func (s *server) handleDerive(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
		return
	}
	depositDeriver, _, ok := s.requireUnlocked(w)
	if !ok {
		return
	}
	var req deriveRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid JSON"})
		return
	}
	if req.Count == 0 || req.Count > 500 {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "count must be 1-500"})
		return
	}
	out := make([]derivedAddress, 0, req.Count)
	for i := uint32(0); i < req.Count; i++ {
		index := req.Start + i
		_, evmAddr, err := depositDeriver.EVMKey(index)
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
			return
		}
		_, solAddr, err := depositDeriver.SolanaKey(index)
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
			return
		}
		out = append(out, derivedAddress{
			Index:  index,
			EVM:    evmAddr.Hex(),
			Solana: solAddr.String(),
		})
	}
	s.audit.write(auditEntry{Op: "derive", Index: req.Start, Amount: fmt.Sprintf("%d", req.Count), Result: "ok"})
	writeJSON(w, http.StatusOK, map[string]any{"addresses": out})
}

// handleHotAddress returns the hot wallet's receive addresses (EVM + Solana)
// so the operator knows exactly what to put in the sweep destination fields.
// Addresses are public data; no key material is exposed.
func (s *server) handleHotAddress(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
		return
	}
	// Reload first: the operator may have just changed hot_index in the
	// console, and this endpoint is how they discover the new address.
	s.reloadPolicy()
	_, hotDeriver, ok := s.requireUnlocked(w)
	if !ok {
		return
	}
	policy := s.currentPolicy()
	_, evmAddr, err := hotDeriver.EVMKey(policy.HotIndex)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	_, solAddr, err := hotDeriver.SolanaKey(policy.HotIndex)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	s.audit.write(auditEntry{Op: "hot_address", Index: policy.HotIndex, Result: "ok"})
	writeJSON(w, http.StatusOK, map[string]any{
		"index":  policy.HotIndex,
		"evm":    evmAddr.Hex(),
		"solana": solAddr.String(),
	})
}

// reloadPolicy re-reads the policy JSON from the database so an operator
// changing the sweep destination in the console does not require a signer
// restart. It is intentionally conservative: parse failure keeps the old
// policy instead of rejecting every signature.
func (s *server) reloadPolicy() {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	p, err := s.store.loadPolicyFromDB(ctx)
	if err != nil {
		log.Printf("signer policy reload failed: %v", err)
		return
	}
	s.policyMu.Lock()
	s.policy = p
	s.policyMu.Unlock()
}

// currentPolicy returns the active policy under the read lock. Every signing
// path reads through here so a concurrent reload cannot race the pointer.
func (s *server) currentPolicy() *Policy {
	s.policyMu.RLock()
	defer s.policyMu.RUnlock()
	return s.policy
}

func (s *server) handleSignEVM(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
		return
	}
	s.reloadPolicy()
	policy := s.currentPolicy()
	depositDeriver, hotDeriver, ok := s.requireUnlocked(w)
	if !ok {
		return
	}
	var req evmSignRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid JSON"})
		return
	}
	if !policy.AllowsTarget(req.To) {
		// Gas top-ups pay native coin TO a derived child address, which is
		// never a whitelisted payout target. Accept them only when the
		// target provably equals the address derived at gas_index: the
		// signer then needs no address book and every fork works out of
		// the box.
		if req.Kind != "gas" {
			s.audit.write(auditEntry{Op: "sign_evm", Index: req.Index, To: req.To, Result: "denied", Reason: "target_not_allowed"})
			writeJSON(w, http.StatusForbidden, map[string]string{"error": "target not allowed"})
			return
		}
		if !gasTargetAllowed(depositDeriver, req) {
			s.audit.write(auditEntry{Op: "sign_evm", Index: req.Index, To: req.To, Result: "denied", Reason: "gas_target_not_derived"})
			writeJSON(w, http.StatusForbidden, map[string]string{"error": "gas target is not our derived address"})
			return
		}
		if req.Token != "" && !strings.EqualFold(req.Token, "native") {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "gas top-up must be native"})
			return
		}
		// Signed by the hot seed, not the deposit seed.
		req.Index = policy.HotIndex
		req.hotKey = true
	}
	amount, err := parseBigInt(req.Amount)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid amount"})
		return
	}
	reason, err := s.limiter.Allow(policy, TokenKey(req.Token), amount, time.Now())
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	if reason != "" {
		s.audit.write(auditEntry{Op: "sign_evm", Kind: req.Kind, Index: req.Index, To: req.To, Token: req.Token, Amount: req.Amount, Result: "denied", Reason: reason})
		writeJSON(w, http.StatusForbidden, map[string]string{"error": reason})
		return
	}
	// Gas top-ups sign with the hot seed; everything else with the deposit
	// seed. Two seeds means one leaked mnemonic cannot drain both pools.
	signingDeriver := depositDeriver
	if req.hotKey {
		signingDeriver = hotDeriver
	}
	signed, err := signEVMTransfer(signingDeriver, req)
	if err != nil {
		s.audit.write(auditEntry{Op: "sign_evm", Kind: req.Kind, Index: req.Index, To: req.To, Token: req.Token, Amount: req.Amount, Result: "error", Reason: err.Error()})
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	s.audit.write(auditEntry{Op: "sign_evm", Kind: req.Kind, Index: req.Index, ChainID: req.ChainID, To: req.To, Token: req.Token, Amount: req.Amount, Result: "ok"})
	writeJSON(w, http.StatusOK, map[string]string{"signed_tx": signed})
}

func (s *server) handleSignSol(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
		return
	}
	s.reloadPolicy()
	policy := s.currentPolicy()
	depositDeriver, _, ok := s.requireUnlocked(w)
	if !ok {
		return
	}
	var req solSignRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid JSON"})
		return
	}
	if !policy.AllowsTarget(req.To) {
		s.audit.write(auditEntry{Op: "sign_sol", Index: req.Index, To: req.To, Result: "denied", Reason: "target_not_allowed"})
		writeJSON(w, http.StatusForbidden, map[string]string{"error": "target not allowed"})
		return
	}
	amount, err := parseBigInt(req.Amount)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid amount"})
		return
	}
	reason, err := s.limiter.Allow(policy, TokenKey(req.Mint), amount, time.Now())
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	if reason != "" {
		s.audit.write(auditEntry{Op: "sign_sol", Index: req.Index, To: req.To, Token: req.Mint, Amount: req.Amount, Result: "denied", Reason: reason})
		writeJSON(w, http.StatusForbidden, map[string]string{"error": reason})
		return
	}
	signed, err := signSolTransfer(depositDeriver, req)
	if err != nil {
		s.audit.write(auditEntry{Op: "sign_sol", Index: req.Index, To: req.To, Token: req.Mint, Amount: req.Amount, Result: "error", Reason: err.Error()})
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	s.audit.write(auditEntry{Op: "sign_sol", Index: req.Index, To: req.To, Token: req.Mint, Amount: req.Amount, Result: "ok"})
	writeJSON(w, http.StatusOK, map[string]string{"signed_tx": signed})
}
