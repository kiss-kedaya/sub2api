package scanner

import (
	"strings"

	"github.com/Wei-Shaw/sub2api/internal/cryptopay/explorer"
)

// NativeTransferFromExplorerTx turns one ordinary transaction from an
// Etherscan-compatible account API into the same Transfer the block scanner
// emits for a native value transfer.
//
// The account API does not report a block hash and a native transfer has no
// log, so BlockHash stays empty and the synthetic NativeLogIndex keeps the
// (chain, tx_hash, log_index) identity stable across both scan paths: a deposit
// first seen by the block scan and later re-returned by an overlapping txlist
// query is the same ledger row, not a second credit.
//
// The row is rejected for a contract creation (no recipient) and for a zero
// value: neither can pay us a mainnet coin.
func NativeTransferFromExplorerTx(chain string, tx explorer.Tx) (Transfer, bool) {
	if tx.To == nil || tx.Value == nil || tx.Value.Sign() <= 0 {
		return Transfer{}, false
	}
	to := strings.ToLower(*tx.To)
	from := strings.ToLower(tx.From)
	if !validHex(from, 20) || !validHex(to, 20) || !validHex(tx.Hash, 32) {
		return Transfer{}, false
	}
	return Transfer{
		Chain:       chain,
		TxHash:      strings.ToLower(tx.Hash),
		LogIndex:    NativeLogIndex,
		From:        from,
		To:          to,
		Token:       NativeToken,
		Amount:      tx.Value,
		Native:      true,
		BlockNumber: tx.BlockNumber,
	}, true
}

// ExplorerTxIncoming reports whether the row pays recipient a positive amount.
func ExplorerTxIncoming(tx explorer.Tx, recipient string) bool {
	return tx.To != nil && tx.Value != nil && tx.Value.Sign() > 0 &&
		strings.EqualFold(*tx.To, recipient)
}
