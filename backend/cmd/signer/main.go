// Command signer is the isolated key-holding service for the crypto
// deposit channel. It owns the BIP39 seed and hot-wallet signing, and
// talks to the web process over a local unix socket only.
//
// The web process must never hold seed or key material.
package main

import (
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
	policy  *Policy
	limiter *limiter
	audit   *auditLog
}

func main() {
	var (
		socketPath = flag.String("socket", "/run/sub2api/signer.sock", "unix socket path")
		policyPath = flag.String("policy", "/etc/sub2api/signer-policy.json", "policy JSON path")
		statePath  = flag.String("state", "/var/lib/sub2api/signer-state.json", "daily limit state path")
		auditPath  = flag.String("audit", "/var/lib/sub2api/signer-audit.log", "audit log path")
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
	if mnemonic == "" {
		log.Fatal("SIGNER_MNEMONIC is not set")
	}
	deriver, err := NewDeriver(mnemonic)
	if err != nil {
		log.Fatalf("init deriver: %v", err)
	}
	policy, err := loadPolicy(*policyPath)
	if err != nil {
		log.Fatalf("load policy: %v", err)
	}
	limiter, err := newLimiter(*statePath)
	if err != nil {
		log.Fatalf("init limiter: %v", err)
	}
	srv := &server{
		deriver: deriver,
		policy:  policy,
		limiter: limiter,
		audit:   newAuditLog(*auditPath),
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
	mux.HandleFunc("/derive", srv.handleDerive)
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
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "seeded": true})
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

func (s *server) handleDerive(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
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
		_, evmAddr, err := s.deriver.EVMKey(index)
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
			return
		}
		_, solAddr, err := s.deriver.SolanaKey(index)
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

func (s *server) handleSignEVM(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
		return
	}
	var req evmSignRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid JSON"})
		return
	}
	if !s.policy.AllowsTarget(req.To) {
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
		if !gasTargetAllowed(s.deriver, req) {
			s.audit.write(auditEntry{Op: "sign_evm", Index: req.Index, To: req.To, Result: "denied", Reason: "gas_target_not_derived"})
			writeJSON(w, http.StatusForbidden, map[string]string{"error": "gas target is not our derived address"})
			return
		}
		if req.Token != "" && !strings.EqualFold(req.Token, "native") {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "gas top-up must be native"})
			return
		}
		req.Index = s.policy.HotIndex
	}
	amount, err := parseBigInt(req.Amount)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid amount"})
		return
	}
	reason, err := s.limiter.Allow(s.policy, TokenKey(req.Token), amount, time.Now())
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	if reason != "" {
		s.audit.write(auditEntry{Op: "sign_evm", Kind: req.Kind, Index: req.Index, To: req.To, Token: req.Token, Amount: req.Amount, Result: "denied", Reason: reason})
		writeJSON(w, http.StatusForbidden, map[string]string{"error": reason})
		return
	}
	signed, err := signEVMTransfer(s.deriver, req)
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
	var req solSignRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid JSON"})
		return
	}
	if !s.policy.AllowsTarget(req.To) {
		s.audit.write(auditEntry{Op: "sign_sol", Index: req.Index, To: req.To, Result: "denied", Reason: "target_not_allowed"})
		writeJSON(w, http.StatusForbidden, map[string]string{"error": "target not allowed"})
		return
	}
	amount, err := parseBigInt(req.Amount)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid amount"})
		return
	}
	reason, err := s.limiter.Allow(s.policy, TokenKey(req.Mint), amount, time.Now())
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	if reason != "" {
		s.audit.write(auditEntry{Op: "sign_sol", Index: req.Index, To: req.To, Token: req.Mint, Amount: req.Amount, Result: "denied", Reason: reason})
		writeJSON(w, http.StatusForbidden, map[string]string{"error": reason})
		return
	}
	signed, err := signSolTransfer(s.deriver, req)
	if err != nil {
		s.audit.write(auditEntry{Op: "sign_sol", Index: req.Index, To: req.To, Token: req.Mint, Amount: req.Amount, Result: "error", Reason: err.Error()})
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	s.audit.write(auditEntry{Op: "sign_sol", Index: req.Index, To: req.To, Token: req.Mint, Amount: req.Amount, Result: "ok"})
	writeJSON(w, http.StatusOK, map[string]string{"signed_tx": signed})
}
