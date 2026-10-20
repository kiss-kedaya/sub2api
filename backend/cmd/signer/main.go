// Command signer is the isolated key-holding service for the crypto
// deposit channel. It owns the BIP39 seed and hot-wallet signing, and
// talks to the web process over a local unix socket only.
//
// The web process must never hold seed or key material.
package main

import (
	"context"
	"encoding/json"
	"errors"
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
	"unicode/utf8"
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
	policyPath string // nonempty only for an independently loaded local policy
	limiter    *limiter
	audit      *auditLog

	// vaultMu guards the unlock state. Until unlock the derivers are nil and
	// every signing path answers ErrVaultLocked, so a stolen process image or
	// a restarted host cannot sign without the operator's passphrase.
	vaultMu sync.RWMutex
	// policyMu guards access to the startup-approved snapshot. HTTP handlers
	// never replace it; changing an approval requires an operator restart.
	policyMu sync.RWMutex
	store    *store
	backup   vaultBackupGuard // shared by every backup/import client, never keyed by caller
}

// parseSocketMode deliberately accepts only these two octal spellings. Never
// grant access to other users or confuse decimal and octal permission values.
func parseSocketMode(raw string) (os.FileMode, error) {
	switch raw {
	case "0600":
		return 0o600, nil
	case "0660":
		return 0o660, nil
	default:
		return 0, fmt.Errorf("-socket-mode must be 0600 or 0660")
	}
}

func main() {
	var (
		socketPath    = flag.String("socket", "/run/sub2api/signer.sock", "unix socket path")
		socketModeRaw = flag.String("socket-mode", "0600", "unix socket permissions: 0600 (owner only) or 0660 (shared IPC group)")
		dbDSN         = flag.String("db", os.Getenv("SIGNER_DB_DSN"), "Postgres DSN for vault and audit; DB policy is diagnostic only")
		policyPath    = flag.String("policy", "", "absolute path to independently approved read-only policy; required for signing")
		statePath     = flag.String("state", "", "absolute path to signer-private persistent budget SQLite ledger")
		initState     = flag.Bool("init-state", false, "offline initialization of a NEW budget ledger, then exit; never resets an existing ledger")
		genSeed       = flag.Bool("gen-seed", false, "generate a new mnemonic and exit")
		recoverBackup = flag.String("recover-backup", "", "offline recovery of an encrypted signer backup; passphrase is read from tty or stdin")
		recoverOutput = flag.String("output", "", "NEW private file for recovered deposit/hot mnemonics; never overwritten")
	)
	flag.Parse()
	// Recovery must exit before opening the database, budget ledger or socket,
	// or considering any environment-provided seeds/bootstrap passphrase.
	if *recoverBackup != "" {
		if flag.NArg() != 0 || *initState || *genSeed {
			log.Fatal("recovery does not accept positional arguments or other offline operations")
		}
		if err := recoverVaultBackup(*recoverBackup, *recoverOutput, os.Stdin, os.Stderr); err != nil {
			log.Fatal(err)
		}
		return
	}
	if *recoverOutput != "" {
		log.Fatal("--output requires --recover-backup")
	}
	socketMode, err := parseSocketMode(*socketModeRaw)
	if err != nil {
		log.Fatal(err)
	}
	if *initState {
		if err := initializeBudget(*statePath, time.Now()); err != nil {
			log.Fatalf("initialize budget: %v", err)
		}
		log.Print("new budget initialized; existing installations must migrate today's usage or remain stopped until the next UTC day")
		return
	}

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
	if *dbDSN == "" {
		log.Fatal("SIGNER_DB_DSN (or -db) is required for the vault and audit")
	}
	st, err := newStore(*dbDSN)
	if err != nil {
		log.Fatalf("init store: %v", err)
	}
	defer func() { _ = st.Close() }()
	// Env seeds are a one-time bootstrap for an installation that has no
	// vault yet; they must never make a vault-backed signer permanently
	// unlocked. Once a vault exists the seeds live only there and /unlock
	// requires the operator passphrase.
	var deriver, hotDeriver *Deriver
	existingVault, err := st.getSetting(context.Background(), settingVault)
	if err != nil {
		log.Fatalf("read vault: %v", err)
	}
	if existingVault == "" && mnemonic != "" && hotMnemonic != "" {
		if hotMnemonic == mnemonic {
			log.Fatal("SIGNER_HOT_MNEMONIC must differ from SIGNER_MNEMONIC")
		}
		if deriver, err = NewDeriver(mnemonic); err != nil {
			log.Fatalf("init deriver: %v", err)
		}
		if hotDeriver, err = NewDeriver(hotMnemonic); err != nil {
			log.Fatalf("init hot deriver: %v", err)
		}
		// Keep the seeds in memory only when the operator explicitly asked to
		// seal them during this bootstrap; otherwise drop them and wait for
		// /unlock like any other run.
		if strings.TrimSpace(os.Getenv("SIGNER_VAULT_BOOTSTRAP_PASSPHRASE")) == "" {
			deriver, hotDeriver = nil, nil
		}
	}
	var policy *Policy
	var limiter *limiter
	if *policyPath != "" {
		policy, err = loadPolicyFile(*policyPath)
		if err != nil {
			log.Fatalf("load independent policy: %v", err)
		}
		limiter, err = newPersistentLimiter(*statePath)
		if err != nil {
			log.Fatalf("init independent budget: %v", err)
		}
		defer limiter.db.Close()
	} else {
		// Compatibility diagnostic only: never authorize using this data.
		if _, err := st.loadPolicyFromDB(context.Background()); err != nil {
			log.Printf("legacy policy diagnostic: %v", err)
		}
		log.Print("signing disabled: -policy and -state are required; DB policy cannot authorize withdrawals")
	}
	srv := &server{
		deriver:    deriver,
		hotDeriver: hotDeriver,
		policy:     policy,
		policyPath: *policyPath,
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
	// to the service account; 0660 additionally permits the configured IPC group.
	if runtime.GOOS != "windows" {
		if err := os.Chmod(*socketPath, socketMode); err != nil {
			log.Fatalf("chmod socket: %v", err)
		}
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", srv.handleHealth)
	mux.HandleFunc("/unlock", srv.handleUnlock)
	srv.registerVaultBackupRoutes(mux)
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
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "unlocked": unlocked,
		"signing_enabled": s.policyPath != "" && s.currentPolicy() != nil && s.limiter != nil && s.limiter.db != nil})
}

// handleUnlock takes the operator passphrase and loads both seeds into
// memory. The passphrase is never stored: it exists only for the scrypt
// derivation that opens the vault.
func (s *server) handleUnlock(w http.ResponseWriter, r *http.Request) {
	vaultResponseHeaders(w)
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
	if err := decodeVaultRequest(w, r, &req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid JSON"})
		return
	}
	if !validVaultPassphrase(req.Passphrase) {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "passphrase required (at most 1024 bytes)"})
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
	if raw != "" && (req.DepositMnemonic != "" || req.HotMnemonic != "") {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "vault already exists; seed replacement is not allowed; existing seeds unchanged"})
		return
	}

	// First run: seal the provided seeds into a new vault.
	if raw == "" {
		if utf8.RuneCountInString(req.Passphrase) < 12 {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "a new vault requires a passphrase of at least 12 characters"})
			return
		}
		deposit := strings.TrimSpace(req.DepositMnemonic)
		hot := strings.TrimSpace(req.HotMnemonic)
		if deposit == "" || hot == "" {
			writeJSON(w, http.StatusBadRequest, map[string]string{
				"error": "no vault yet: send deposit_mnemonic and hot_mnemonic to create it",
			})
			return
		}
		if strings.Join(strings.Fields(deposit), " ") == strings.Join(strings.Fields(hot), " ") {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "hot seed must differ from the deposit seed"})
			return
		}
		// Validate before sealing: a typo used to be encrypted and persisted,
		// after which every derivation failed and the vault could only be
		// rebuilt with manual SQL.
		if _, err := NewDeriver(deposit); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "deposit seed invalid: " + err.Error()})
			return
		}
		if _, err := NewDeriver(hot); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "hot seed invalid: " + err.Error()})
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
	if req.Count == 0 || req.Count > 500 || req.Start >= 1<<31 || uint64(req.Start)+uint64(req.Count) > 1<<31 {
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

// handleHotAddress returns the actual gas funding addresses (EVM + Solana).
// Sweep destinations are separate policy targets and need not equal these.
// Addresses are public data; no key material is exposed.
func (s *server) handleHotAddress(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
		return
	}
	_, hotDeriver, ok := s.requireUnlocked(w)
	if !ok {
		return
	}
	policy, ok := s.independentPolicy(w)
	if !ok {
		return
	}
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

// A shared DB policy can be read at startup for migration diagnostics, never
// used for authorization. No request reloads DB policy or changes hot_index.
func (s *server) independentPolicy(w http.ResponseWriter) (*Policy, bool) {
	p := s.currentPolicy()
	if s.policyPath == "" || p == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "signing disabled: independent -policy required"})
		return nil, false
	}
	return p, true
}

// currentPolicy returns the independently approved startup snapshot.
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
	depositDeriver, hotDeriver, ok := s.requireUnlocked(w)
	if !ok {
		return
	}
	policy, ok := s.independentPolicy(w)
	if !ok {
		return
	}
	if s.limiter == nil || s.limiter.db == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "persistent signer budget required"})
		return
	}
	var req evmSignRequest
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 32<<10)).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid JSON"})
		return
	}
	signingDeriver := depositDeriver
	if req.Kind == "gas" {
		// ALWAYS enforce the derivation, native-token restriction and hot seed,
		// even if To also happens to be a whitelisted sweep target.
		if !isEVMNative(req.Token) || !gasTargetAllowed(depositDeriver, req) {
			writeJSON(w, http.StatusForbidden, map[string]string{"error": "gas requires native payment to our derived gas_index"})
			return
		}
		req.Index, req.hotKey = policy.HotIndex, true
		signingDeriver = hotDeriver
	} else if !policy.allowsEVMTarget(req.To) {
		s.audit.write(auditEntry{Op: "sign_evm", Index: req.Index, To: req.To, Result: "denied", Reason: "target_not_allowed"})
		writeJSON(w, http.StatusForbidden, map[string]string{"error": "target not allowed"})
		return
	}
	charges, err := evmBudgetCharges(policy, req)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()
	signed, reason, err := s.limiter.authorizeAndSign(ctx, policy, charges, time.Now(), func() (string, error) {
		return signEVMTransfer(signingDeriver, req)
	})
	s.signResponse(w, signed, reason, err, auditEntry{Op: "sign_evm", Kind: req.Kind, Index: req.Index, ChainID: req.ChainID, To: req.To, Token: req.Token, Amount: req.Amount})
}

func (s *server) handleSignSol(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
		return
	}
	depositDeriver, _, ok := s.requireUnlocked(w)
	if !ok {
		return
	}
	policy, ok := s.independentPolicy(w)
	if !ok {
		return
	}
	if s.limiter == nil || s.limiter.db == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "persistent signer budget required"})
		return
	}
	var req solSignRequest
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 32<<10)).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid JSON"})
		return
	}
	if !policy.allowsSolanaTarget(req.To) {
		s.audit.write(auditEntry{Op: "sign_sol", Index: req.Index, To: req.To, Result: "denied", Reason: "target_not_allowed"})
		writeJSON(w, http.StatusForbidden, map[string]string{"error": "target not allowed"})
		return
	}
	charges, err := solBudgetCharges(policy, req)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()
	signed, reason, err := s.limiter.authorizeAndSign(ctx, policy, charges, time.Now(), func() (string, error) {
		return signSolTransfer(depositDeriver, req)
	})
	s.signResponse(w, signed, reason, err, auditEntry{Op: "sign_sol", Index: req.Index, To: req.To, Token: req.Mint, Amount: req.Amount})
}

func (s *server) signResponse(w http.ResponseWriter, signed, reason string, err error, entry auditEntry) {
	if err != nil {
		entry.Result, entry.Reason = "error", err.Error()
		s.audit.write(entry)
		status := http.StatusInternalServerError
		var buildErr *signBuildError
		if errors.As(err, &buildErr) {
			status = http.StatusBadRequest
		}
		writeJSON(w, status, map[string]string{"error": err.Error()})
		return
	}
	if reason != "" {
		entry.Result, entry.Reason = "denied", reason
		s.audit.write(entry)
		writeJSON(w, http.StatusForbidden, map[string]string{"error": reason})
		return
	}
	entry.Result = "ok"
	s.audit.write(entry)
	writeJSON(w, http.StatusOK, map[string]string{"signed_tx": signed})
}
