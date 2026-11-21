package repository

import (
	"context"
	"database/sql"
	"encoding/json"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/logger"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/lib/pq"
)

const secretsBackfillBatchSize = 200

// RunSecretsEncryptionBackfill rewrites every row that is still stored in the
// legacy plaintext format so a completed deployment leaves no credential
// material readable at rest. It is:
//
//   - idempotent: already-encrypted rows are skipped;
//   - resumable: keyset pagination means a interrupted run simply continues on
//     the next start;
//   - compare-and-swap: each rewrite is guarded by the exact observed value so
//     a concurrent admin save is never clobbered (the new value will already be
//     encrypted by the write path anyway);
//   - best-effort: a single unreadable row is logged (never its value) and the
//     rest of the batch still migrates. Reads of that row fail closed.
//
// It runs once per process, shortly after startup, for accounts, proxies and the
// credential-bearing settings. The SQL emitted here is what the PR documents as
// the batch migration query; AES-GCM cannot be evaluated in SQL, so the Go
// process performs the encryption.
func RunSecretsEncryptionBackfill(ctx context.Context, db *sql.DB, vault service.SecretVault) error {
	if db == nil || !service.SecretVaultAvailable(vault) {
		return nil
	}
	if err := backfillAccountCredentials(ctx, db, vault); err != nil {
		return err
	}
	if err := backfillProxyPasswords(ctx, db, vault); err != nil {
		return err
	}
	return backfillSensitiveSettings(ctx, db, vault)
}

func backfillAccountCredentials(ctx context.Context, db *sql.DB, vault service.SecretVault) error {
	var lastID int64
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		rows, err := db.QueryContext(ctx, `
			SELECT id, credentials
			FROM accounts
			WHERE id > $1 AND credentials IS NOT NULL
			ORDER BY id
			LIMIT $2
		`, lastID, secretsBackfillBatchSize)
		if err != nil {
			logger.LegacyPrintf("repository.secrets_backfill", "[SecretsBackfill] account query failed: %v", err)
			return nil
		}
		type accountRow struct {
			id  int64
			raw []byte
		}
		batch := make([]accountRow, 0, secretsBackfillBatchSize)
		for rows.Next() {
			var row accountRow
			if err := rows.Scan(&row.id, &row.raw); err != nil {
				_ = rows.Close()
				return nil
			}
			batch = append(batch, row)
		}
		_ = rows.Close()
		if len(batch) == 0 {
			return nil
		}
		for _, row := range batch {
			lastID = row.id
			if len(row.raw) == 0 {
				continue
			}
			var stored map[string]any
			if err := json.Unmarshal(row.raw, &stored); err != nil {
				continue
			}
			if _, needsRewrite, err := service.DecryptCredentialsFromStorage(vault, stored); err != nil {
				logger.LegacyPrintf("repository.secrets_backfill",
					"[SecretsBackfill] account credentials unreadable, left for admin re-save: account=%d", row.id)
				continue
			} else if !needsRewrite {
				continue
			}
			encrypted, err := service.EncryptCredentialsForStorage(vault, stored)
			if err != nil {
				continue
			}
			payload, err := json.Marshal(encrypted)
			if err != nil {
				continue
			}
			if _, err := db.ExecContext(ctx, `
				UPDATE accounts SET credentials = $2
				WHERE id = $1 AND credentials = $3
			`, row.id, string(payload), string(row.raw)); err != nil {
				logger.LegacyPrintf("repository.secrets_backfill",
					"[SecretsBackfill] account credentials rewrite failed: account=%d err=%v", row.id, err)
			}
		}
		if len(batch) < secretsBackfillBatchSize {
			return nil
		}
	}
}

func backfillProxyPasswords(ctx context.Context, db *sql.DB, vault service.SecretVault) error {
	var lastID int64
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		rows, err := db.QueryContext(ctx, `
			SELECT id, password
			FROM proxies
			WHERE id > $1 AND password IS NOT NULL AND password <> ''
			ORDER BY id
			LIMIT $2
		`, lastID, secretsBackfillBatchSize)
		if err != nil {
			logger.LegacyPrintf("repository.secrets_backfill", "[SecretsBackfill] proxy query failed: %v", err)
			return nil
		}
		type proxyRow struct {
			id       int64
			password string
		}
		batch := make([]proxyRow, 0, secretsBackfillBatchSize)
		for rows.Next() {
			var row proxyRow
			if err := rows.Scan(&row.id, &row.password); err != nil {
				_ = rows.Close()
				return nil
			}
			batch = append(batch, row)
		}
		_ = rows.Close()
		if len(batch) == 0 {
			return nil
		}
		for _, row := range batch {
			lastID = row.id
			plaintext, needsRewrite, err := service.DecryptProxyPassword(vault, row.password)
			if err != nil {
				logger.LegacyPrintf("repository.secrets_backfill",
					"[SecretsBackfill] proxy password unreadable, left for admin re-save: proxy=%d", row.id)
				continue
			}
			if !needsRewrite {
				continue
			}
			encrypted, err := service.EncryptProxyPassword(vault, plaintext)
			if err != nil {
				continue
			}
			if _, err := db.ExecContext(ctx, `
				UPDATE proxies SET password = $2, updated_at = NOW()
				WHERE id = $1 AND password IS NOT DISTINCT FROM $3
			`, row.id, encrypted, row.password); err != nil {
				logger.LegacyPrintf("repository.secrets_backfill",
					"[SecretsBackfill] proxy password rewrite failed: proxy=%d err=%v", row.id, err)
			}
		}
		if len(batch) < secretsBackfillBatchSize {
			return nil
		}
	}
}

func backfillSensitiveSettings(ctx context.Context, db *sql.DB, vault service.SecretVault) error {
	keys := append([]string(nil), service.SensitiveSettingKeys...)
	if len(keys) == 0 {
		return nil
	}
	var lastID int64
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		rows, err := db.QueryContext(ctx, `
			SELECT id, key, value
			FROM settings
			WHERE id > $1 AND key = ANY($2) AND value <> ''
			ORDER BY id
			LIMIT $3
		`, lastID, pq.Array(keys), secretsBackfillBatchSize)
		if err != nil {
			logger.LegacyPrintf("repository.secrets_backfill", "[SecretsBackfill] settings query failed: %v", err)
			return nil
		}
		type settingRow struct {
			id    int64
			key   string
			value string
		}
		batch := make([]settingRow, 0, secretsBackfillBatchSize)
		for rows.Next() {
			var row settingRow
			if err := rows.Scan(&row.id, &row.key, &row.value); err != nil {
				_ = rows.Close()
				return nil
			}
			batch = append(batch, row)
		}
		_ = rows.Close()
		if len(batch) == 0 {
			return nil
		}
		for _, row := range batch {
			lastID = row.id
			_, needsRewrite, err := service.DecryptSettingValue(vault, row.key, row.value)
			if err != nil {
				logger.LegacyPrintf("repository.secrets_backfill",
					"[SecretsBackfill] setting unreadable, left for admin re-save: key=%s", row.key)
				continue
			}
			if !needsRewrite {
				continue
			}
			encrypted, err := service.EncryptSettingValue(vault, row.key, row.value)
			if err != nil {
				continue
			}
			if _, err := db.ExecContext(ctx, `
				UPDATE settings SET value = $1, updated_at = NOW()
				WHERE id = $2 AND value = $3
			`, encrypted, row.id, row.value); err != nil {
				logger.LegacyPrintf("repository.secrets_backfill",
					"[SecretsBackfill] setting rewrite failed: key=%s err=%v", row.key, err)
			}
		}
		if len(batch) < secretsBackfillBatchSize {
			return nil
		}
	}
}

// SecretsBackfillTimeout bounds the startup migration so a stuck database cannot
// hold a connection for the lifetime of the process.
const SecretsBackfillTimeout = 30 * time.Minute

// RunSecretsEncryptionDecrypt is the rollback counterpart of
// RunSecretsEncryptionBackfill. It rewrites every encrypted credential back to
// plaintext so an older binary (which has no at-rest encryption) can read the
// deployment again. Run it with the server stopped and only as a rollback step:
// while it runs, the database temporarily holds plaintext credential material.
func RunSecretsEncryptionDecrypt(ctx context.Context, db *sql.DB, vault service.SecretVault) error {
	if db == nil || !service.SecretVaultAvailable(vault) {
		return nil
	}
	if err := decryptAccountCredentials(ctx, db, vault); err != nil {
		return err
	}
	if err := decryptProxyPasswords(ctx, db, vault); err != nil {
		return err
	}
	return decryptSensitiveSettings(ctx, db, vault)
}

func decryptAccountCredentials(ctx context.Context, db *sql.DB, vault service.SecretVault) error {
	var lastID int64
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		rows, err := db.QueryContext(ctx, `
			SELECT id, credentials
			FROM accounts
			WHERE id > $1 AND credentials IS NOT NULL
			ORDER BY id
			LIMIT $2
		`, lastID, secretsBackfillBatchSize)
		if err != nil {
			return err
		}
		type row struct {
			id  int64
			raw []byte
		}
		batch := make([]row, 0, secretsBackfillBatchSize)
		for rows.Next() {
			var item row
			if err := rows.Scan(&item.id, &item.raw); err != nil {
				_ = rows.Close()
				return err
			}
			batch = append(batch, item)
		}
		_ = rows.Close()
		if len(batch) == 0 {
			return nil
		}
		for _, item := range batch {
			lastID = item.id
			var stored map[string]any
			if json.Unmarshal(item.raw, &stored) != nil {
				continue
			}
			if !service.CredentialsContainEncryptedValues(stored) {
				continue
			}
			plaintext, _, err := service.DecryptCredentialsFromStorage(vault, stored)
			if err != nil {
				logger.LegacyPrintf("repository.secrets_backfill",
					"[SecretsRollback] account credentials decrypt failed: account=%d", item.id)
				continue
			}
			payload, err := json.Marshal(plaintext)
			if err != nil {
				continue
			}
			if _, err := db.ExecContext(ctx, `UPDATE accounts SET credentials = $2 WHERE id = $1`, item.id, string(payload)); err != nil {
				logger.LegacyPrintf("repository.secrets_backfill",
					"[SecretsRollback] account credentials decrypt failed: account=%d err=%v", item.id, err)
			}
		}
		if len(batch) < secretsBackfillBatchSize {
			return nil
		}
	}
}

func decryptProxyPasswords(ctx context.Context, db *sql.DB, vault service.SecretVault) error {
	var lastID int64
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		rows, err := db.QueryContext(ctx, `
			SELECT id, password
			FROM proxies
			WHERE id > $1 AND password IS NOT NULL AND password <> ''
			ORDER BY id
			LIMIT $2
		`, lastID, secretsBackfillBatchSize)
		if err != nil {
			return err
		}
		type row struct {
			id       int64
			password string
		}
		batch := make([]row, 0, secretsBackfillBatchSize)
		for rows.Next() {
			var item row
			if err := rows.Scan(&item.id, &item.password); err != nil {
				_ = rows.Close()
				return err
			}
			batch = append(batch, item)
		}
		_ = rows.Close()
		if len(batch) == 0 {
			return nil
		}
		for _, item := range batch {
			lastID = item.id
			if !service.ProxyPasswordIsEncrypted(item.password) {
				continue
			}
			plaintext, _, err := service.DecryptProxyPassword(vault, item.password)
			if err != nil {
				logger.LegacyPrintf("repository.secrets_backfill",
					"[SecretsRollback] proxy password decrypt failed: proxy=%d", item.id)
				continue
			}
			if _, err := db.ExecContext(ctx, `UPDATE proxies SET password = $2 WHERE id = $1`, item.id, plaintext); err != nil {
				logger.LegacyPrintf("repository.secrets_backfill",
					"[SecretsRollback] proxy password update failed: proxy=%d err=%v", item.id, err)
			}
		}
		if len(batch) < secretsBackfillBatchSize {
			return nil
		}
	}
}

func decryptSensitiveSettings(ctx context.Context, db *sql.DB, vault service.SecretVault) error {
	keys := append([]string(nil), service.SensitiveSettingKeys...)
	var lastID int64
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		rows, err := db.QueryContext(ctx, `
			SELECT id, key, value
			FROM settings
			WHERE id > $1 AND key = ANY($2) AND value <> ''
			ORDER BY id
			LIMIT $3
		`, lastID, pq.Array(keys), secretsBackfillBatchSize)
		if err != nil {
			return err
		}
		type row struct {
			id    int64
			key   string
			value string
		}
		batch := make([]row, 0, secretsBackfillBatchSize)
		for rows.Next() {
			var item row
			if err := rows.Scan(&item.id, &item.key, &item.value); err != nil {
				_ = rows.Close()
				return err
			}
			batch = append(batch, item)
		}
		_ = rows.Close()
		if len(batch) == 0 {
			return nil
		}
		for _, item := range batch {
			lastID = item.id
			if !service.SettingValueIsEncrypted(item.key, item.value) {
				continue
			}
			plaintext, _, err := service.DecryptSettingValue(vault, item.key, item.value)
			if err != nil {
				logger.LegacyPrintf("repository.secrets_backfill",
					"[SecretsRollback] setting decrypt failed: key=%s", item.key)
				continue
			}
			if _, err := db.ExecContext(ctx, `UPDATE settings SET value = $1 WHERE id = $2`, plaintext, item.id); err != nil {
				logger.LegacyPrintf("repository.secrets_backfill",
					"[SecretsRollback] setting decrypt failed: key=%s err=%v", item.key, err)
			}
		}
		if len(batch) < secretsBackfillBatchSize {
			return nil
		}
	}
}
