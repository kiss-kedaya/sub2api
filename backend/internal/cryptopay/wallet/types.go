// Package wallet embeds the encrypted deposit and gas wallets in the gateway.
// It never opens a listener or uses the former signer's policy or budget store.
package wallet

import "errors"

// Status describes managed seed configuration and channel readiness.
type Status struct {
	OK         bool            `json:"ok"`
	Configured bool            `json:"configured"`
	Ready      bool            `json:"ready"`
	Enabled    bool            `json:"enabled"`
	Security   *SecurityStatus `json:"security,omitempty"`
	// Locked means the vault is sealed with a data key and no shard has been
	// combined in this process, so signing/export operations need a share.
	Locked bool `json:"signer_locked"`
	// Shards reports the public shard configuration; it never contains share
	// material beyond the server-held share A, which stays sealed at rest.
	Shards *ShardStatus `json:"shards,omitempty"`
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

var (
	ErrWalletNotConfigured = errors.New("wallet seeds are not configured")
	// ErrSigningDisabled means the runtime signing switch is off.
	ErrSigningDisabled = errors.New("wallet signing is disabled")
	// ErrVaultUnavailable deliberately omits database driver details.
	ErrVaultUnavailable = errors.New("encrypted wallet seeds unavailable")
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
	// ErrSignerShareRequired means a v3 vault is sealed and no recovery share
	// has been supplied in this operation. The HTTP layer maps it to a stable
	// error code so the UI can prompt for a shard.
	ErrSignerShareRequired = errors.New("wallet signer is locked; a recovery shard is required")
	// ErrSignerShareInvalid covers every rejected share without revealing
	// whether the share was close to correct.
	ErrSignerShareInvalid = errors.New("recovery shard cannot open this wallet")
	// ErrSignerRateLimited means too many rejected shares; the operator must
	// wait before trying again.
	ErrSignerRateLimited = errors.New("too many rejected recovery shares; the signer is temporarily locked")
	// ErrShardsNotConfigured means the vault still uses the installation-key
	// ciphertext and the shard setup flow has not completed.
	ErrShardsNotConfigured = errors.New("wallet shards are not configured")
	// ErrShardsAlreadyConfigured means a shard set already exists; use the
	// re-split flow instead of silently replacing it.
	ErrShardsAlreadyConfigured = errors.New("wallet shards are already configured; use re-split")
	// ErrShardsNotAllowed means the requested shard operation is invalid for
	// the current vault state.
	ErrShardsNotAllowed = errors.New("wallet shard operation is not allowed in the current state")
)
