//go:build integration

package cryptopay

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/cryptopay/wallet"
	"github.com/stretchr/testify/require"
	"github.com/tyler-smith/go-bip39"
)

func TestPostgresUserDepositHistoryHidesInternalFundingBeforeLimitAndPreservesOrders(t *testing.T) {
	db := isolatedSweepAlertPostgres(t)
	for _, name := range []string{"261_crypto_deposit.sql", "263_crypto_signer_audit.sql", "264_crypto_deposit_reason.sql"} {
		migration, err := os.ReadFile(filepath.Join("..", "..", "migrations", name))
		require.NoError(t, err)
		_, err = db.ExecContext(t.Context(), string(migration))
		require.NoError(t, err)
	}
	_, err := db.ExecContext(t.Context(), `CREATE TABLE payment_orders (
		id BIGINT PRIMARY KEY, user_id BIGINT NOT NULL, amount NUMERIC NOT NULL,
		out_trade_no TEXT NOT NULL DEFAULT '', payment_type TEXT NOT NULL DEFAULT 'crypto',
		status TEXT NOT NULL DEFAULT 'COMPLETED')`)
	require.NoError(t, err)
	// Synthetic, unfunded test seeds in a disposable database only.
	entropy := make([]byte, 32)
	depositMnemonic, err := bip39.NewMnemonic(entropy)
	require.NoError(t, err)
	entropy[len(entropy)-1] = 1
	gasMnemonic, err := bip39.NewMnemonic(entropy)
	require.NoError(t, err)
	manager := wallet.New(db, strings.Repeat("ab", 32))
	t.Cleanup(manager.Close)
	// Package tests use distinct schemas but share the PostgreSQL sweep lock.
	// Wait only for that expected contention; any other initialization error
	// remains an immediate failure of this history-query fixture.
	require.Eventually(t, func() bool {
		err = manager.Initialize(t.Context(), depositMnemonic, gasMnemonic)
		return !errors.Is(err, wallet.ErrSweepBusy)
	}, 10*time.Second, 25*time.Millisecond)
	require.NoError(t, err)
	hot, err := manager.PublicHotAddresses(t.Context())
	require.NoError(t, err)
	const recipient = "0x1111111111111111111111111111111111111111"
	const otherRecipient = "0x2222222222222222222222222222222222222222"
	const external = "0x3333333333333333333333333333333333333333"
	const token = "0x55d398326f99059ff775485246999027b3197955"
	_, err = db.ExecContext(t.Context(), `INSERT INTO deposit_addresses(user_id,chain,address,derivation_index)
		VALUES (42,'evm',$1,1),(43,'evm',$2,2)`, recipient, otherRecipient)
	require.NoError(t, err)
	_, err = db.ExecContext(t.Context(), `INSERT INTO payment_orders(id,user_id,amount) VALUES (10,42,3),(11,42,4)`)
	require.NoError(t, err)
	insert := func(hash, from, to, status string, order any) {
		t.Helper()
		_, err := db.ExecContext(t.Context(), `INSERT INTO deposit_transactions
			(chain,tx_hash,from_address,to_address,token,amount,block_number,status,reason,order_id)
			VALUES ('bsc',$1,$2,$3,$4,0.1,100,$5,'below_min',$6)`, hash, from, to, token, status, order)
		require.NoError(t, err)
	}
	insert("external-too-small", external, recipient, "ignored", nil)
	insert("linked-credited", hot.EVM, recipient, "credited", int64(10))
	insert("linked-pending", hot.EVM, recipient, "pending", int64(11))
	insert("legacy-credited", hot.EVM, recipient, "credited", nil)
	insert("other-user", external, otherRecipient, "ignored", nil)
	insert("gas-ignored", strings.ToLower(hot.EVM), recipient, "ignored", nil)
	insert("gas-pending", hot.EVM, recipient, "pending", nil)
	svc := &Service{db: db, wallet: manager}
	rows, err := svc.UserDepositHistory(t.Context(), 42, 50)
	require.NoError(t, err)
	var hashes []string
	for _, row := range rows {
		hashes = append(hashes, row.TxHash)
	}
	require.ElementsMatch(t, []string{"external-too-small", "linked-credited", "linked-pending", "legacy-credited"}, hashes)
	limited, err := svc.UserDepositHistory(t.Context(), 42, 1)
	require.NoError(t, err)
	require.Len(t, limited, 1, "hidden funding must not consume the page limit")
	require.Equal(t, "legacy-credited", limited[0].TxHash)
	other, err := svc.UserDepositHistory(t.Context(), 43, 50)
	require.NoError(t, err)
	require.Len(t, other, 1)
	require.Equal(t, "other-user", other[0].TxHash)
	// Filtering is presentation-only. No deposit or linked order was mutated.
	var count int
	require.NoError(t, db.QueryRowContext(t.Context(), `SELECT COUNT(*) FROM deposit_transactions`).Scan(&count))
	require.Equal(t, 7, count)

	_, err = db.ExecContext(t.Context(), `TRUNCATE deposit_transactions`)
	require.NoError(t, err)
	insertDust := func(hash, amount, status string, order any) {
		t.Helper()
		_, err := db.ExecContext(t.Context(), `INSERT INTO deposit_transactions
			(chain,tx_hash,from_address,to_address,token,amount,block_number,status,reason,order_id)
			VALUES ('bsc',$1,$2,$3,$4,$5,100,$6,'below_min',$7)`, hash, external, recipient, token, amount, status, order)
		require.NoError(t, err)
	}
	insertDust("dust-tiny", "0.000001", "ignored", nil)
	insertDust("dust-boundary", "0.0001", "ignored", nil)
	insertDust("dust-linked", "0.00000001", "ignored", int64(11))
	insertDust("dust-credited", "0.00000001", "credited", int64(10))
	insertDust("dust-pending", "0.00000001", "pending", nil)
	insertDust("dust-below-boundary", "0.00009999", "ignored", nil)
	svc.config.CreditMinAmount = 0.001
	rows, err = svc.UserDepositHistory(t.Context(), 42, 50)
	require.NoError(t, err)
	hashes = nil
	for _, row := range rows {
		hashes = append(hashes, row.TxHash)
	}
	require.ElementsMatch(t, []string{"dust-boundary", "dust-linked", "dust-credited", "dust-pending"}, hashes)
	limited, err = svc.UserDepositHistory(t.Context(), 42, 1)
	require.NoError(t, err)
	require.Len(t, limited, 1)
	require.Equal(t, "dust-pending", limited[0].TxHash, "dust noise must be excluded before LIMIT")
	// Lowering the acceptance minimum below a historical rejected transfer
	// prevents the presentation threshold from hiding that transfer.
	svc.config.CreditMinAmount = 0.0000001
	rows, err = svc.UserDepositHistory(t.Context(), 42, 50)
	require.NoError(t, err)
	require.Len(t, rows, 6)
	svc.config.CreditMinAmount = 0
	rows, err = svc.UserDepositHistory(t.Context(), 42, 50)
	require.NoError(t, err)
	require.Len(t, rows, 6)
	require.NoError(t, db.QueryRowContext(t.Context(), `SELECT COUNT(*) FROM deposit_transactions`).Scan(&count))
	require.Equal(t, 6, count, "no dust deposit should be deleted from the administrator ledger")
}
