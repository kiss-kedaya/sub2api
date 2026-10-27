package service

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"github.com/Wei-Shaw/sub2api/internal/serverless"
)

type serverlessStore struct {
	settings SettingRepository
	db       *sql.DB
}

func (s serverlessStore) Load(ctx context.Context) (serverless.Config, error) {
	c := serverless.Config{Pods: []serverless.PodPolicy{}, Regions: []serverless.Region{}}
	raw, err := s.settings.GetValue(ctx, serverless.SettingKey)
	if errors.Is(err, ErrSettingNotFound) {
		return c, nil
	}
	if err != nil {
		return c, err
	}
	if raw != "" {
		err = json.Unmarshal([]byte(raw), &c)
	}
	return c, err
}
func (s serverlessStore) Save(ctx context.Context, c serverless.Config, expected string) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err = tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock(784120299)`); err != nil {
		return err
	}
	var raw string
	err = tx.QueryRowContext(ctx, `SELECT value FROM settings WHERE key=$1 FOR UPDATE`, serverless.SettingKey).Scan(&raw)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	var previous serverless.Config
	if raw != "" {
		if err = json.Unmarshal([]byte(raw), &previous); err != nil {
			return err
		}
	}
	if previous.Revision != expected {
		return serverless.ErrConfigConflict
	}
	encoded, err := json.Marshal(c)
	if err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO settings(key,value,updated_at) VALUES($1,$2,NOW()) ON CONFLICT(key) DO UPDATE SET value=EXCLUDED.value,updated_at=NOW()`, serverless.SettingKey, string(encoded))
	if err != nil {
		return err
	}
	return tx.Commit()
}
