package scanner

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/Wei-Shaw/sub2api/internal/cryptopay/mode"
)

// CursorStore persists per-chain scan progress.
type CursorStore struct {
	db *sql.DB
}

func NewCursorStore(db *sql.DB) *CursorStore {
	return &CursorStore{db: db}
}

// Load returns the stored last block for a chain (0 when absent).
func (s *CursorStore) Load(ctx context.Context, chain string) (uint64, error) {
	var last sql.NullInt64
	err := s.db.QueryRowContext(ctx,
		`SELECT last_block FROM crypto_scan_cursors WHERE chain = $1`, chain).Scan(&last)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, nil
	}
	if err != nil {
		return 0, fmt.Errorf("load cursor %s: %w", chain, err)
	}
	if !last.Valid || last.Int64 < 0 {
		return 0, nil
	}
	return uint64(last.Int64), nil
}

// Save upserts the cursor after a committed batch.
func (s *CursorStore) Save(ctx context.Context, chain string, lastBlock uint64) error {
	if err := mode.CheckWritable(); err != nil {
		return err
	}
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO crypto_scan_cursors (chain, last_block, updated_at)
		 VALUES ($1, $2, NOW())
		 ON CONFLICT (chain) DO UPDATE SET last_block = EXCLUDED.last_block, updated_at = NOW()`,
		chain, int64(lastBlock))
	if err != nil {
		return fmt.Errorf("save cursor %s: %w", chain, err)
	}
	return nil
}
