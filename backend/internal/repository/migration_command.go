package repository

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/lib/pq"
)

// RunConfiguredMigrations opens only PostgreSQL and either applies or validates
// migrations. It deliberately avoids application wiring, Redis, workers, and
// startup seeders so deployment jobs have one auditable responsibility.
func RunConfiguredMigrations(ctx context.Context, cfg *config.Config, validateOnly bool) error {
	if cfg == nil {
		return errors.New("nil config")
	}
	connector, err := pq.NewConnector(cfg.Database.DSNWithTimezone(cfg.Timezone))
	if err != nil {
		return fmt.Errorf("create database connector: %w", err)
	}
	db := sql.OpenDB(connector)
	defer db.Close()
	applyDBPoolSettings(db, cfg)

	if err := db.PingContext(ctx); err != nil {
		return fmt.Errorf("connect database: %w", err)
	}
	if validateOnly {
		return CheckMigrations(ctx, db)
	}
	return ApplyMigrations(ctx, db)
}
