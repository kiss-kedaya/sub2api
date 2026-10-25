//go:build integration

package cryptopay

import (
	"math/big"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/cryptopay/sweeper"
	"github.com/stretchr/testify/require"
)

func TestPostgresSweepPreviewTracksCreditedDepositsWithoutReservingFunds(t *testing.T) {
	db := isolatedSweepAlertPostgres(t)
	for _, name := range []string{"261_crypto_deposit.sql", "266_crypto_sweep_jobs.sql"} {
		migration, err := os.ReadFile(filepath.Join("..", "..", "migrations", name))
		require.NoError(t, err)
		_, err = db.ExecContext(t.Context(), string(migration))
		require.NoError(t, err)
	}
	const address = "0x3333333333333333333333333333333333333333"
	const token = "0x1111111111111111111111111111111111111111"
	_, err := db.ExecContext(t.Context(), `INSERT INTO deposit_addresses(user_id,chain,address,derivation_index)
		VALUES (1,'evm',$1,2)`, address)
	require.NoError(t, err)
	deposit := func(hash, amount, status string) {
		t.Helper()
		_, err := db.ExecContext(t.Context(), `INSERT INTO deposit_transactions
			(chain,tx_hash,from_address,to_address,token,amount,block_number,status)
			VALUES ('bsc',$1,'sender',$2,$3,$4,100,$5)`, hash, address, token, amount, status)
		require.NoError(t, err)
	}
	deposit("first", "10", "credited")
	reader := newBalanceReader(db, map[string]int{tokenDecimalsKey("bsc", token): 6})
	planner := sweeper.NewPlanner(db, reader, sweeper.Config{GlobalThreshold: 1000,
		MinPerAddress: map[string]float64{"bsc": 1}}, nil)
	preview, err := planner.BuildManualPlan(t.Context())
	require.NoError(t, err)
	require.Len(t, preview.Entries, 1)
	require.Equal(t, "10000000", preview.Entries[0].RawAmount.String())
	deposit("after-preview", "2", "credited")
	deposit("uncredited", "50", "ignored")
	preview, err = planner.BuildManualPlan(t.Context())
	require.NoError(t, err)
	require.Len(t, preview.Entries, 1)
	require.Equal(t, "12000000", preview.Entries[0].RawAmount.String())
	var reserved int
	require.NoError(t, db.QueryRowContext(t.Context(), "SELECT COUNT(*) FROM sweep_records").Scan(&reserved))
	require.Zero(t, reserved, "preview must not reserve funds or persist an old plan")
	_, err = db.ExecContext(t.Context(), `INSERT INTO sweep_records
		(chain,from_address,to_address,token,amount,raw_amount,token_decimals,
		derivation_index,status,job_version,confirmations_required)
		VALUES ('bsc',$1,'destination',$2,10,'10000000',6,2,'planned',1,3)`, address, token)
	require.NoError(t, err)
	preview, err = planner.BuildManualPlan(t.Context())
	require.NoError(t, err)
	require.Len(t, preview.Entries, 1, "unsigned pending jobs must not hide the live preview")
	fresh, err := reader.CurrentBalance(t.Context(), "bsc", address, strings.ToUpper(token))
	require.NoError(t, err)
	require.Equal(t, big.NewInt(12_000_000), fresh.Raw)
	require.Equal(t, uint32(2), fresh.DerivationIndex)
	automatic, err := planner.BuildPlan(t.Context())
	require.NoError(t, err)
	require.Empty(t, automatic.Entries, "automatic sweeps still require their configured threshold")
	_, err = db.ExecContext(t.Context(), `UPDATE sweep_records SET
		status='sweep_pending',sweep_raw_tx='signed',sweep_tx_hash='hash',sweep_nonce='0'`)
	require.NoError(t, err)
	preview, err = planner.BuildManualPlan(t.Context())
	require.NoError(t, err)
	require.Empty(t, preview.Entries, "signed pending token transactions must keep the address reserved")
	_, err = db.ExecContext(t.Context(), "UPDATE sweep_records SET status='swept'")
	require.NoError(t, err)
	fresh, err = reader.CurrentBalance(t.Context(), "bsc", address, token)
	require.NoError(t, err)
	require.Equal(t, "2000000", fresh.Raw.String(), "successful transfers are subtracted from the next amount")
}
