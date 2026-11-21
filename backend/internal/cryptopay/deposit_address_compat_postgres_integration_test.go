//go:build integration

package cryptopay

import (
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/cryptopay/addresses"
	"github.com/Wei-Shaw/sub2api/migrations"
	"github.com/stretchr/testify/require"
	"golang.org/x/sync/errgroup"
)

func TestPostgresDepositAddressAllocationWithBothIndexShapes(t *testing.T) {
	for _, tc := range []struct {
		name       string
		migrations []string
	}{
		{"original index", []string{"261_crypto_deposit.sql"}},
		{"partial active index", []string{"261_crypto_deposit.sql", "323_crypto_deposit_address_rotate.sql"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			db := isolatedSweepAlertPostgres(t)
			for _, name := range tc.migrations {
				migration, err := migrations.FS.ReadFile(name)
				require.NoError(t, err)
				_, err = db.ExecContext(t.Context(), string(migration))
				require.NoError(t, err)
			}
			const userID = 42
			const family = addresses.FamilyEVM
			repo := addresses.NewRepo(db)
			ctx := t.Context()
			require.NoError(t, repo.InsertPoolBatch(ctx, family, 10, []string{"0x0000000000000000000000000000000000000001", "0x0000000000000000000000000000000000000002"}))

			// Concurrent first allocations must share one assignment and leave one pool slot.
			g, groupCtx := errgroup.WithContext(ctx)
			got := make([]*addresses.Assignment, 4)
			for i := range got {
				g.Go(func() error {
					var err error
					got[i], err = repo.AssignFromPool(groupCtx, userID, family)
					return err
				})
			}
			require.NoError(t, g.Wait())
			require.NotNil(t, got[0])
			require.Equal(t, "active", got[0].Status)
			for _, a := range got[1:] {
				require.Equal(t, got[0], a)
			}
			require.NoError(t, repo.SetStatus(ctx, got[0].ID, "disabled"))
			stillDisabled, err := repo.AssignFromPool(ctx, userID, family)
			require.NoError(t, err)
			require.NotNil(t, stillDisabled)
			require.Equal(t, got[0].ID, stillDisabled.ID)
			require.Equal(t, "disabled", stillDisabled.Status)
			svc := addresses.NewService(repo, nil)
			svc.SetFamilies([]string{family})
			_, err = svc.GetOrAssign(ctx, userID)
			require.ErrorContains(t, err, "deposit address is disabled")
			rows, err := repo.ListByUser(ctx, userID)
			require.NoError(t, err)
			require.Len(t, rows, 1)
			free, err := repo.FreePoolCount(ctx, family)
			require.NoError(t, err)
			require.EqualValues(t, 1, free)
		})
	}
}
