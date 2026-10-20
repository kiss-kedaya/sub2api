package main

import (
	"encoding/json"
	"fmt"
	"io"
	"math/big"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"

	"github.com/ethereum/go-ethereum/common"
	"github.com/gagliardetto/solana-go"
)

// Policy is approved independently of the web/database administrator. Missing
// chains, fee bounds or amount limits deny signing; there is no RPC fallback.
type Policy struct {
	Comment       string                    `json:"_comment,omitempty"`
	Targets       []string                  `json:"targets"`
	MaxPerTx      map[string]string         `json:"max_per_tx"`
	MaxDaily      map[string]string         `json:"max_daily"`
	RatePerMinute int                       `json:"rate_per_minute"`
	HotIndex      uint32                    `json:"hot_index"`
	EVMChains     map[string]EVMChainPolicy `json:"evm_chains"`
	Solana        *SolanaPolicy             `json:"solana,omitempty"`
}

type EVMChainPolicy struct {
	MaxGasLimit uint64 `json:"max_gas_limit"`
	MaxGasPrice string `json:"max_gas_price"` // wei/gas, also the EIP-1559 fee cap
	MaxFee      string `json:"max_fee"`       // gasLimit * feeCap, in wei
}

// These are approved upper bounds, not prices supplied by RPC. SPL's
// idempotent ATA creation additionally needs a rent allowance.
type SolanaPolicy struct {
	MaxFeeLamports  uint64 `json:"max_fee_lamports"`
	ATARentLamports uint64 `json:"ata_rent_lamports"`
}

// AllowsTarget preserves the helper, but only EVM addresses fold case.
func (p *Policy) AllowsTarget(to string) bool {
	if common.IsHexAddress(to) {
		return p.allowsEVMTarget(to)
	}
	return p.allowsSolanaTarget(to)
}

func (p *Policy) allowsEVMTarget(to string) bool {
	if !common.IsHexAddress(to) {
		return false
	}
	for _, target := range p.Targets {
		if common.IsHexAddress(target) && common.HexToAddress(target) == common.HexToAddress(to) {
			return true
		}
	}
	return false
}

func (p *Policy) allowsSolanaTarget(to string) bool {
	if _, err := solana.PublicKeyFromBase58(to); err != nil {
		return false
	}
	for _, target := range p.Targets {
		if target == to {
			return true
		}
	}
	return false
}

// Base58 mint addresses are case sensitive; only EVM contracts are normalized.
func TokenKey(token string) string {
	if token == "" || token == "native" {
		return "native"
	}
	if common.IsHexAddress(token) {
		return strings.ToLower(token)
	}
	return token
}

func (p *Policy) limitKey(chain, token string) string {
	key := chain + ":" + TokenKey(token)
	_, tx := p.MaxPerTx[key]
	_, daily := p.MaxDaily[key]
	if tx || daily || chain == "solana" {
		return key
	}
	// Legacy EVM keys remain usable with a shared cross-chain daily total.
	return TokenKey(token)
}

func positiveUint(raw string, bits int) (*big.Int, error) {
	v, err := parseBigInt(raw)
	if err != nil || v.Sign() <= 0 || v.BitLen() > bits {
		return nil, fmt.Errorf("expected a positive uint%d", bits)
	}
	return v, nil
}

func (p *Policy) validate() error {
	if p.HotIndex >= 1<<31 || p.RatePerMinute < 0 || len(p.Targets) == 0 {
		return fmt.Errorf("invalid hot_index, rate_per_minute or targets")
	}
	for _, target := range p.Targets {
		if !common.IsHexAddress(target) {
			if _, err := solana.PublicKeyFromBase58(target); err != nil {
				return fmt.Errorf("invalid policy target")
			}
		}
	}
	for _, limits := range []map[string]string{p.MaxPerTx, p.MaxDaily} {
		for key, raw := range limits {
			if _, err := positiveUint(raw, 256); err != nil || key == "" {
				return fmt.Errorf("invalid policy limit for %s", key)
			}
		}
	}
	for id, chain := range p.EVMChains {
		n, err := strconv.ParseInt(id, 10, 64)
		if err != nil || n <= 0 || strconv.FormatInt(n, 10) != id {
			return fmt.Errorf("invalid EVM chain id %q", id)
		}
		if chain.MaxGasLimit < 21_000 || chain.MaxGasLimit > 1_000_000 {
			return fmt.Errorf("chain %s: max_gas_limit must be 21000-1000000", id)
		}
		if _, err := positiveUint(chain.MaxGasPrice, 256); err != nil {
			return fmt.Errorf("chain %s: invalid max_gas_price", id)
		}
		if _, err := positiveUint(chain.MaxFee, 256); err != nil {
			return fmt.Errorf("chain %s: invalid max_fee", id)
		}
	}
	if p.Solana != nil && p.Solana.MaxFeeLamports == 0 {
		return fmt.Errorf("solana: max_fee_lamports required")
	}
	return nil
}

// Deployment MUST deny the web UID access to the policy and its directory,
// or mount it read-only only in signer. File modes cannot prove UID isolation.
// The validated snapshot stays fixed until an operator restarts the signer.
func loadPolicyFile(path string) (*Policy, error) {
	if !localAbsolutePath(path) {
		return nil, fmt.Errorf("policy requires an absolute local path")
	}
	info, err := os.Lstat(path)
	if err != nil {
		return nil, fmt.Errorf("stat policy: %w", err)
	}
	if !info.Mode().IsRegular() || info.Mode().Perm()&0o222 != 0 {
		return nil, fmt.Errorf("policy must be a regular read-only file (0440/0444)")
	}
	dir, err := os.Lstat(filepath.Dir(path))
	if err != nil || !dir.IsDir() || dir.Mode()&os.ModeSymlink != 0 {
		return nil, fmt.Errorf("policy directory must be a real local directory")
	}
	if runtime.GOOS != "windows" && dir.Mode().Perm()&0o022 != 0 {
		return nil, fmt.Errorf("policy directory must not be group/world writable")
	}
	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("open policy: %w", err)
	}
	defer f.Close()
	opened, err := f.Stat()
	if err != nil || !os.SameFile(info, opened) {
		return nil, fmt.Errorf("policy changed while opening")
	}
	var p Policy
	dec := json.NewDecoder(io.LimitReader(f, 1<<20))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&p); err != nil {
		return nil, fmt.Errorf("decode policy: %w", err)
	}
	if err := dec.Decode(new(any)); err != io.EOF {
		return nil, fmt.Errorf("policy must contain exactly one JSON object")
	}
	if err := p.validate(); err != nil {
		return nil, fmt.Errorf("invalid independent policy: %w", err)
	}
	return &p, nil
}

func localAbsolutePath(path string) bool {
	if !filepath.IsAbs(path) {
		return false
	}
	// UNC/device paths are not local files. On Unix, mounting a network
	// filesystem still requires operator checks; a pathname cannot prove it.
	volume := filepath.VolumeName(path)
	return runtime.GOOS != "windows" || (len(volume) == 2 && volume[1] == ':')
}
