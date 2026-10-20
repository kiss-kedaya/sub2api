package ledger

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
)

// OrderIdentity is stable across retries, including when the order was
// created but the deposit's order_id could not yet be persisted.
func OrderIdentity(chain, txHash string, logIndex int) (rechargeCode, outTradeNo string) {
	digest := sha256.Sum256([]byte(fmt.Sprintf("%s|%s|%d", chain, txHash, logIndex)))
	return "crypto-" + hex.EncodeToString(digest[:12]), "crypto-" + hex.EncodeToString(digest[:20])
}
