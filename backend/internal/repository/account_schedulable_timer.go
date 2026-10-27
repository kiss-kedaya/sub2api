package repository

import (
	"context"
	"database/sql"

	"github.com/Wei-Shaw/sub2api/internal/accounttimer"
	"github.com/Wei-Shaw/sub2api/internal/service"
)

// SchedulableTimers exposes an optional capability without expanding the broad
// AccountRepository interface (and every unrelated mock). Production uses sql.DB.
func (r *accountRepository) SchedulableTimers() accounttimer.Repository {
	db, ok := r.sql.(*sql.DB)
	if !ok || db == nil {
		return nil
	}
	return &accounttimer.Store{
		DB: db, Policy: accounttimer.ConfiguredPolicy(),
		Enqueue: func(ctx context.Context, tx *sql.Tx, id int64) error {
			return enqueueSchedulerOutbox(ctx, tx, service.SchedulerOutboxEventAccountChanged, &id, nil, nil)
		},
		AfterCommit: r.syncSchedulerAccountSnapshotDetached,
	}
}
