package repository

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"time"
)

type mihomoBindingRepository struct{ db *sql.DB }

func NewMihomoBindingRepository(db *sql.DB) service.MihomoBindingRepository {
	return &mihomoBindingRepository{db}
}
func (r *mihomoBindingRepository) GetProxyID(ctx context.Context, host, node string) (int64, error) {
	var id int64
	err := r.db.QueryRowContext(ctx, `SELECT proxy_id FROM mihomo_proxy_bindings WHERE host_id=$1 AND node_id=$2`, host, node).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, nil
	}
	return id, err
}
func (r *mihomoBindingRepository) Save(ctx context.Context, host, node string, id int64) error {
	_, err := r.db.ExecContext(ctx, `INSERT INTO mihomo_proxy_bindings(host_id,node_id,proxy_id) VALUES($1,$2,$3)`, host, node, id)
	return err
}
func (r *mihomoBindingRepository) WithHostLock(ctx context.Context, host string, run func() error) error {
	conn, err := r.db.Conn(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = conn.Close() }()
	for {
		var locked bool
		err = conn.QueryRowContext(ctx, `SELECT pg_try_advisory_lock(hashtextextended($1,0))`, "mihomo-import:"+host).Scan(&locked)
		if err != nil {
			_ = conn.Raw(func(any) error { return driver.ErrBadConn })
			return err
		}
		if locked {
			break
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(50 * time.Millisecond):
		}
	}
	defer func() {
		clean, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		if _, e := conn.ExecContext(clean, `SELECT pg_advisory_unlock(hashtextextended($1,0))`, "mihomo-import:"+host); e != nil {
			_ = conn.Raw(func(any) error { return driver.ErrBadConn })
		}
	}()
	return run()
}
