package cryptopay

import (
	"context"
	"math/big"
	"strings"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/Wei-Shaw/sub2api/internal/cryptopay/addresses"
	"github.com/Wei-Shaw/sub2api/internal/cryptopay/ledger"
	"github.com/Wei-Shaw/sub2api/internal/cryptopay/scanner"
	"github.com/Wei-Shaw/sub2api/internal/cryptopay/sweeper"
	"github.com/stretchr/testify/require"
)

func TestGasWalletTokenFundingNeverCreatesDepositOrUnmatchedAlert(t *testing.T) {
	const gas = "0xaabbccddeeff0011223344556677889900112233"
	for _, chain := range []string{"ethereum", "bsc", "polygon", "plasma", "arbitrum", "base", "xlayer"} {
		t.Run(chain, func(t *testing.T) {
			db, mock, err := sqlmock.New()
			require.NoError(t, err)
			defer func() { _ = db.Close() }()
			// A missing address service makes any accidental recipient lookup
			// observable. The gas seed is separate from the deposit pool.
			resolver := newAddressResolver(nil)
			resolver.setHotAddresses(&sweeper.HotAddress{EVM: gas})
			led := ledger.New(db, nil, resolver, 1, 10000, 1)
			led.SetUnmatchedNotifier(func(_ context.Context, _ scanner.Transfer, _ string) {
				t.Fatal("internal gas-wallet transfer produced an unmatched alert")
			})
			transfer := scanner.Transfer{Chain: chain, TxHash: "0xfixture", From: strings.ToUpper(gas),
				To: "0x1111111111111111111111111111111111111111", Amount: big.NewInt(1)}
			require.NoError(t, led.ProcessPending(t.Context(), transfer, 18))
			mock.ExpectQuery(`SELECT d.status, COALESCE`).
				WillReturnRows(sqlmock.NewRows([]string{"status", "order_id", "user_id", "amount"}))
			credited, err := led.Process(t.Context(), transfer, 18)
			require.NoError(t, err)
			require.False(t, credited)
			require.NoError(t, mock.ExpectationsWereMet())
		})
	}
}

func TestGasWalletSnapshotDoesNotTreatExternalOrWrongFamilySenderAsInternal(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer func() { _ = db.Close() }()
	resolver := newAddressResolver(addresses.NewService(addresses.NewRepo(db), nil))
	resolver.setHotAddresses(&sweeper.HotAddress{EVM: "0x1111111111111111111111111111111111111111", Solana: "SolanaFixture"})
	for _, test := range []struct{ chain, family, sender string }{
		{"bsc", "evm", "0x2222222222222222222222222222222222222222"},
		{"bsc", "evm", "SolanaFixture"},
		{"solana", "solana", "solanafixture"},
	} {
		mock.ExpectQuery(`FROM deposit_addresses`).WithArgs(test.family, test.sender).
			WillReturnRows(sqlmock.NewRows([]string{"id", "user_id", "chain", "address", "derivation_index", "status"}))
		mock.ExpectQuery(`FROM crypto_address_pool`).WithArgs(test.family, test.sender).
			WillReturnRows(sqlmock.NewRows([]string{"id", "status"}))
		own, err := resolver.IsOwnAddress(t.Context(), test.chain, test.sender)
		require.NoError(t, err)
		require.False(t, own)
	}
	_, err = resolver.IsOwnAddress(t.Context(), "unsupported-chain", "0x1111111111111111111111111111111111111111")
	require.Error(t, err)
	require.NoError(t, mock.ExpectationsWereMet())
}
