// Package wallet embeds the encrypted deposit and gas wallets in the gateway.
// It never opens a listener or uses the former signer's policy or budget store.
package wallet

import "errors"

// Status describes availability, seed residency and the signing switch.
type Status struct {
	OK             bool `json:"ok"`
	Unlocked       bool `json:"unlocked"`
	SigningEnabled bool `json:"signing_enabled"`
}

// HotAddress contains the public gas wallet addresses at the fixed index zero.
type HotAddress struct {
	Index  uint32 `json:"index"`
	EVM    string `json:"evm"`
	Solana string `json:"solana"`
}

// EVMSignRequest is the shared sweep/gas transaction contract. Amount and
// GasPrice are positive integers in base units; Token empty or native means gas.
type EVMSignRequest struct {
	Index    uint32  `json:"index"`
	Kind     string  `json:"kind,omitempty"`
	GasIndex *uint32 `json:"gas_index,omitempty"`
	ChainID  int64   `json:"chain_id"`
	Token    string  `json:"token"`
	To       string  `json:"to"`
	Amount   string  `json:"amount"`
	Nonce    uint64  `json:"nonce"`
	GasPrice string  `json:"gas_price"`
	GasLimit uint64  `json:"gas_limit"`
}

// MnemonicExport is plaintext for this explicit authenticated response only.
type MnemonicExport struct {
	DepositMnemonic string `json:"deposit_mnemonic"`
	HotMnemonic     string `json:"hot_mnemonic"`
}

// VaultBackup preserves the former signer's v1 encrypted export format.
type VaultBackup struct {
	Format  string `json:"format"`
	Version int    `json:"version"`
	Vault   string `json:"vault"`
}

var (
	// ErrMnemonicExportRejected means the supplied vault credentials were refused.
	ErrMnemonicExportRejected = errors.New("wallet refused mnemonic export")
	// ErrMnemonicExportLimited means another vault operation or recent failures
	// exhausted the shared authentication guard.
	ErrMnemonicExportLimited = errors.New("wallet mnemonic export is rate limited")
	// ErrSigningDisabled means the runtime signing switch is off.
	ErrSigningDisabled = errors.New("wallet signing is disabled")
	// ErrVaultUnavailable deliberately omits database driver details.
	ErrVaultUnavailable = errors.New("encrypted wallet vault unavailable")
	// ErrAuditUnavailable prevents unaudited wallet results from being returned.
	ErrAuditUnavailable = errors.New("wallet audit unavailable")
	// ErrSeedReplacement rejects seed inputs for an existing encrypted vault.
	ErrSeedReplacement = errors.New("vault already exists; seed replacement is not allowed")
	// ErrDepositSeedMismatch prevents deriving from a different deposit seed.
	ErrDepositSeedMismatch = errors.New("deposit seed does not match existing deposit addresses")
	// ErrSweepBusy means another connection holds the shared sweep lock.
	ErrSweepBusy = errors.New("sweep is running in another instance")
	// ErrSweepPending means a durable sweep still needs its existing hot seed.
	ErrSweepPending = errors.New("unfinished sweep jobs prevent hot mnemonic import")
)
