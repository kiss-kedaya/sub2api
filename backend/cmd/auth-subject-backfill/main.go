// Command auth-subject-backfill 是 329_auth_identity_subject_hash.sql 的一次性
// 回填工具（不进部署镜像）：在没有 pgcrypto、或需要把回填放在跳板机执行的场景，
// 用安装密钥在应用侧算 keyed 盲索引，再把 *_hash / *_prefix 写回数据库。
//
// 与 SQL 回填完全等价：
//
//	hash = HMAC-SHA256(key, domain || 0x00 || lower(trim(provider_type)) || 0x00 || trim(subject))
//
// 安全约束：
//   - 默认 dry-run，只统计并打印待回填行数，不写库；
//   - 安装密钥只从命令行参数或环境变量读取，不打印、不落盘；
//     推荐用环境变量 AUTH_SUBJECT_INSTALLATION_KEY / TOTP_ENCRYPTION_KEY，
//     避免 key 出现在进程 argv 与 shell history；
//   - 幂等：只处理 *_hash IS NULL 的行，可重复执行；
//   - --verify-samples N 用安装密钥重算抽样行的 HMAC 与库里 hash 比对（只读）。
package main

import (
	"context"
	"database/sql"
	"flag"
	"fmt"
	"os"
	"strings"
	"time"

	_ "github.com/lib/pq"

	"github.com/Wei-Shaw/sub2api/internal/pkg/authsubject"
)

const backfillStatementTimeout = 30 * time.Second

type backfillTarget struct {
	name        string
	selectQuery string
	updateQuery string
	// sampleQuery 抽样校验用：只读 hash 非空且明文还在的行（封存值除外）。
	sampleQuery string
	hash        func(providerType, subject string) string
}

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "auth-subject-backfill:", err)
		os.Exit(1)
	}
}

func run() error {
	var (
		dsn       = flag.String("dsn", "", "PostgreSQL DSN（只读账号即可做 dry-run）")
		keyHex    = flag.String("installation-key-hex", "", "installation key (64 hex chars, security_secrets.totp_encryption_key)")
		apply     = flag.Bool("apply", false, "write results (default: dry-run)")
		batchSize = flag.Int("batch", 2000, "rows per batch")
		// 抽样校验：重算这些行的 HMAC，与库里已存 hash 比对；只读、不打印明文。
		verifySamples = flag.Int("verify-samples", 0, "verify N hashed rows per table against the installation key, then exit (read-only)")
	)
	flag.Parse()

	if strings.TrimSpace(*dsn) == "" {
		// 也可以把连接串放进环境变量，避免出现在进程 argv / shell history。
		*dsn = firstNonEmptyEnv("AUTH_SUBJECT_BACKFILL_DSN", "DATABASE_DSN")
	}
	if strings.TrimSpace(*dsn) == "" {
		return fmt.Errorf("--dsn is required")
	}
	if strings.TrimSpace(*keyHex) == "" {
		// 生产上更安全的传参方式：把 key 放进环境变量，避免出现在进程 argv / shell history。
		*keyHex = firstNonEmptyEnv("AUTH_SUBJECT_INSTALLATION_KEY", "TOTP_ENCRYPTION_KEY")
	}
	if err := authsubject.ConfigureInstallationKey(strings.TrimSpace(*keyHex)); err != nil {
		return fmt.Errorf("installation key: %w", err)
	}
	if *batchSize <= 0 {
		return fmt.Errorf("--batch must be positive")
	}
	if *verifySamples < 0 {
		return fmt.Errorf("--verify-samples must not be negative")
	}

	db, err := sql.Open("postgres", *dsn)
	if err != nil {
		return fmt.Errorf("open database: %w", err)
	}
	defer func() { _ = db.Close() }()

	targets := []backfillTarget{
		{
			name: "auth_identities",
			selectQuery: `
SELECT id, provider_type, provider_subject
FROM auth_identities
WHERE provider_subject_hash IS NULL
  AND provider_subject IS NOT NULL
  AND id > $1
ORDER BY id
LIMIT $2`,
			sampleQuery: `
SELECT id, provider_type, provider_subject, provider_subject_hash
FROM auth_identities
WHERE provider_subject_hash IS NOT NULL
  AND provider_subject IS NOT NULL
ORDER BY id
LIMIT $1`,
			updateQuery: `
UPDATE auth_identities
SET provider_subject_hash = $2,
    provider_subject_prefix = NULLIF($3, '')
WHERE id = $1
  AND provider_subject_hash IS NULL`,
			hash: authsubject.HashIdentity,
		},
		{
			name: "auth_identity_channels",
			selectQuery: `
SELECT id, provider_type, channel_subject
FROM auth_identity_channels
WHERE channel_subject_hash IS NULL
  AND channel_subject IS NOT NULL
  AND id > $1
ORDER BY id
LIMIT $2`,
			sampleQuery: `
SELECT id, provider_type, channel_subject, channel_subject_hash
FROM auth_identity_channels
WHERE channel_subject_hash IS NOT NULL
  AND channel_subject IS NOT NULL
ORDER BY id
LIMIT $1`,
			updateQuery: `
UPDATE auth_identity_channels
SET channel_subject_hash = $2,
    channel_subject_prefix = NULLIF($3, '')
WHERE id = $1
  AND channel_subject_hash IS NULL`,
			hash: authsubject.HashChannel,
		},
		{
			name: "pending_auth_sessions",
			selectQuery: `
SELECT id, provider_type, provider_subject
FROM pending_auth_sessions
WHERE provider_subject_hash IS NULL
  AND provider_subject IS NOT NULL
  AND provider_subject NOT LIKE 'enc:sub:v1:%'
  AND id > $1
ORDER BY id
LIMIT $2`,
			sampleQuery: `
SELECT id, provider_type, provider_subject, provider_subject_hash
FROM pending_auth_sessions
WHERE provider_subject_hash IS NOT NULL
  AND provider_subject IS NOT NULL
  AND provider_subject NOT LIKE 'enc:sub:v1:%'
ORDER BY id
LIMIT $1`,
			updateQuery: `
UPDATE pending_auth_sessions
SET provider_subject_hash = $2,
    provider_subject_prefix = NULLIF($3, '')
WHERE id = $1
  AND provider_subject_hash IS NULL`,
			hash: authsubject.HashIdentity,
		},
	}

	if *verifySamples > 0 {
		for _, target := range targets {
			if err := verifyTargetSamples(db, target, *verifySamples); err != nil {
				return fmt.Errorf("%s: %w", target.name, err)
			}
		}
		return nil
	}

	for _, target := range targets {
		if err := backfillTargetRows(db, target, *apply, *batchSize); err != nil {
			return fmt.Errorf("%s: %w", target.name, err)
		}
	}
	return nil
}

// verifyTargetSamples 用安装密钥重算抽样行的 HMAC，与库里已存 hash 比对。
// 只打印 id 与 hash 前缀，不打印 subject 明文；任何不一致都会返回错误。
func verifyTargetSamples(db *sql.DB, target backfillTarget, limit int) error {
	ctx, cancel := context.WithTimeout(context.Background(), backfillStatementTimeout)
	defer cancel()

	rows, err := db.QueryContext(ctx, target.sampleQuery, limit)
	if err != nil {
		return err
	}
	type sampleRow struct {
		id           int64
		providerType string
		subject      string
		storedHash   string
	}
	samples := make([]sampleRow, 0, limit)
	for rows.Next() {
		var (
			row        sampleRow
			providerTy sql.NullString
			subject    sql.NullString
			hashValue  sql.NullString
		)
		if err := rows.Scan(&row.id, &providerTy, &subject, &hashValue); err != nil {
			_ = rows.Close()
			return err
		}
		row.providerType = providerTy.String
		row.subject = subject.String
		row.storedHash = hashValue.String
		samples = append(samples, row)
	}
	scanErr := rows.Err()
	_ = rows.Close()
	if scanErr != nil {
		return scanErr
	}

	mismatched := 0
	for _, row := range samples {
		computed := target.hash(row.providerType, row.subject)
		stored := strings.ToLower(strings.TrimSpace(row.storedHash))
		if stored == computed {
			continue
		}
		mismatched++
		fmt.Printf("%s: MISMATCH id=%d stored_prefix=%s computed_prefix=%s\n",
			target.name, row.id,
			authsubject.PrefixFromHash(stored),
			authsubject.PrefixFromHash(computed),
		)
	}
	fmt.Printf("%s: verify done samples=%d mismatched=%d\n", target.name, len(samples), mismatched)
	if mismatched > 0 {
		return fmt.Errorf("%d/%d sample hashes do not match the installation key", mismatched, len(samples))
	}
	return nil
}

func backfillTargetRows(db *sql.DB, target backfillTarget, apply bool, batchSize int) error {
	ctx := context.Background()
	var (
		lastID  int64
		scanned int
		updated int
		batches int
	)
	for {
		batchCtx, cancel := context.WithTimeout(ctx, backfillStatementTimeout)
		rows, err := db.QueryContext(batchCtx, target.selectQuery, lastID, batchSize)
		if err != nil {
			cancel()
			return err
		}
		type pendingRow struct {
			id           int64
			providerType string
			subject      string
		}
		batch := make([]pendingRow, 0, batchSize)
		for rows.Next() {
			var (
				id      int64
				pt      sql.NullString
				subject sql.NullString
			)
			if err := rows.Scan(&id, &pt, &subject); err != nil {
				_ = rows.Close()
				cancel()
				return err
			}
			batch = append(batch, pendingRow{id: id, providerType: pt.String, subject: subject.String})
		}
		scanErr := rows.Err()
		_ = rows.Close()
		cancel()
		if scanErr != nil {
			return scanErr
		}
		if len(batch) == 0 {
			break
		}

		batches++
		if apply {
			txCtx, txCancel := context.WithTimeout(ctx, backfillStatementTimeout)
			tx, err := db.BeginTx(txCtx, nil)
			if err != nil {
				txCancel()
				return err
			}
			for _, row := range batch {
				timeoutCtx, timeoutCancel := context.WithTimeout(txCtx, backfillStatementTimeout)
				hashValue := target.hash(row.providerType, row.subject)
				_, err = tx.ExecContext(timeoutCtx, target.updateQuery,
					row.id,
					hashValue,
					authsubject.PrefixFromHash(hashValue),
				)
				timeoutCancel()
				if err != nil {
					_ = tx.Rollback()
					txCancel()
					return err
				}
				updated++
			}
			if err := tx.Commit(); err != nil {
				txCancel()
				return err
			}
			txCancel()
		}
		scanned += len(batch)
		lastID = batch[len(batch)-1].id
		fmt.Printf("%s: scanned=%d updated=%d last_id=%d\n", target.name, scanned, updated, lastID)
	}
	fmt.Printf("%s: done batches=%d scanned=%d updated=%d apply=%v\n", target.name, batches, scanned, updated, apply)
	return nil
}

// firstNonEmptyEnv 依次返回第一个非空环境变量，用于安全地传入安装密钥。
func firstNonEmptyEnv(names ...string) string {
	for _, name := range names {
		if value := strings.TrimSpace(os.Getenv(name)); value != "" {
			return value
		}
	}
	return ""
}
