// Package atrestmigrate 实现 `sub2api -atrest-migrate` 的分批数据迁移：
// 把历史明文敏感列加密进密文列并封存明文列。
//
// 安全约束（2026-11 事故教训）：
//   - 只由新版二进制的显式 CLI 触发，不在启动时自动执行；
//   - 写入前必须同时满足 SUB2API_ATREST_ENABLED=true 与有效 SUB2API_ATREST_KEY；
//     总开关默认关闭，仅配置密钥不会让迁移或业务写入产生密文；
//   - 默认先 dry-run 统计，显式 -atrest-dry-run=false 才写库；
//   - 每行先做「加密 → 解密回读」一致性校验，任何失败立即停止并报告已处理数量；
//   - 幂等：密文已存在的行跳过，重复执行不会二次加密；
//   - 批量 200 行/批、批间 sleep 200ms，避免长时间锁表影响线上请求；
//   - 开启前必须先跑真实网关请求验收（2026-11-22 上游 api_key 读不到导致全站 502）。
package atrestmigrate

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/atrest"
	"github.com/Wei-Shaw/sub2api/internal/repository"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/lib/pq"
)

const (
	TableAccounts = "accounts"
	TableProxies  = "proxies"
	TableSettings = "settings"

	DefaultBatchSize = 200
	DefaultBatchGap  = 200 * time.Millisecond
	DefaultTables    = "accounts,proxies,settings"
)

// Options 是迁移参数。
type Options struct {
	Tables    []string
	BatchSize int
	DryRun    bool
	Verify    bool
	BatchGap  time.Duration
}

// TableResult 是单表迁移/校验统计。
type TableResult struct {
	Table string
	// Scanned 扫描行数。
	Scanned int64
	// Migrated 本次新写入密文列的行数。
	Migrated int64
	// Sealed 本次完成封存（删除明文敏感值）的行数。
	Sealed int64
	// Already 已有密文、跳过的行数。
	Already int64
	// RemainingPlaintext 剩余明文行数（校验标准：全部归零）。
	RemainingPlaintext int64
	// Broken 密文无法解密、或封存但缺密文的坏行数。
	Broken int64
}

// Result 是整次运行的统计。
type Result struct {
	DryRun  bool
	Verify  bool
	Elapsed time.Duration
	Tables  []TableResult
}

// ParseTables 解析 -atrest-tables 的逗号分隔表名。
func ParseTables(raw string) ([]string, error) {
	if strings.TrimSpace(raw) == "" {
		raw = DefaultTables
	}
	seen := make(map[string]struct{}, 3)
	tables := make([]string, 0, 3)
	for _, part := range strings.Split(raw, ",") {
		name := strings.ToLower(strings.TrimSpace(part))
		if name == "" {
			continue
		}
		switch name {
		case TableAccounts, TableProxies, TableSettings:
		default:
			return nil, fmt.Errorf("unsupported at-rest table %q (allowed: accounts,proxies,settings)", name)
		}
		if _, ok := seen[name]; ok {
			continue
		}
		seen[name] = struct{}{}
		tables = append(tables, name)
	}
	if len(tables) == 0 {
		return nil, errors.New("at least one table is required")
	}
	return tables, nil
}

// Run 执行迁移或只读校验。
func Run(ctx context.Context, db *sql.DB, opts Options) (*Result, error) {
	if db == nil {
		return nil, errors.New("database is not configured")
	}
	if opts.BatchSize <= 0 {
		opts.BatchSize = DefaultBatchSize
	}
	if opts.BatchGap <= 0 {
		opts.BatchGap = DefaultBatchGap
	}
	if len(opts.Tables) == 0 {
		parsed, err := ParseTables(DefaultTables)
		if err != nil {
			return nil, err
		}
		opts.Tables = parsed
	}
	if !opts.DryRun && !opts.Verify && !atrest.Enabled() {
		return nil, fmt.Errorf("at-rest sealing is disabled: set %s=true and configure a valid %s before running an encryption migration", atrest.EnabledEnv, atrest.KeyEnv)
	}

	started := time.Now()
	result := &Result{DryRun: opts.DryRun, Verify: opts.Verify}
	for _, table := range opts.Tables {
		var (
			tableResult TableResult
			err         error
		)
		switch table {
		case TableAccounts:
			tableResult, err = migrateAccounts(ctx, db, opts)
		case TableProxies:
			tableResult, err = migrateProxies(ctx, db, opts)
		case TableSettings:
			tableResult, err = migrateSettings(ctx, db, opts)
		}
		if err != nil {
			result.Elapsed = time.Since(started)
			return result, fmt.Errorf("%s: %w", table, err)
		}
		tableResult.Table = table
		result.Tables = append(result.Tables, tableResult)
	}
	result.Elapsed = time.Since(started)
	return result, nil
}

// ── accounts ────────────────────────────────────────────────────────────────

func migrateAccounts(ctx context.Context, db *sql.DB, opts Options) (TableResult, error) {
	var stats TableResult
	lastID := int64(0)
	for {
		rows, err := db.QueryContext(ctx, `
			SELECT id, credentials, COALESCE(credentials_cipher, '')
			FROM accounts
			WHERE id > $1
			ORDER BY id ASC
			LIMIT $2
		`, lastID, opts.BatchSize)
		if err != nil {
			return stats, err
		}
		type accountRow struct {
			id          int64
			credentials []byte
			cipher      string
		}
		batch := make([]accountRow, 0, opts.BatchSize)
		for rows.Next() {
			var row accountRow
			if err := rows.Scan(&row.id, &row.credentials, &row.cipher); err != nil {
				_ = rows.Close()
				return stats, err
			}
			batch = append(batch, row)
		}
		rowErr := rows.Err()
		_ = rows.Close()
		if rowErr != nil {
			return stats, rowErr
		}
		if len(batch) == 0 {
			break
		}
		for _, row := range batch {
			lastID = row.id
			stats.Scanned++
			if err := migrateOneAccount(ctx, db, &stats, row.id, row.credentials, row.cipher, opts); err != nil {
				return stats, fmt.Errorf("account id=%d: %w", row.id, err)
			}
		}
		if err := sleepBetweenBatches(ctx, opts.BatchGap); err != nil {
			return stats, err
		}
	}
	return stats, nil
}

func migrateOneAccount(
	ctx context.Context,
	db *sql.DB,
	stats *TableResult,
	id int64,
	credentialsJSON []byte,
	ciphertext string,
	opts Options,
) error {
	var credentials map[string]any
	if len(credentialsJSON) > 0 && string(credentialsJSON) != "null" {
		if err := json.Unmarshal(credentialsJSON, &credentials); err != nil {
			return fmt.Errorf("decode credentials: %w", err)
		}
	}
	if credentials == nil {
		credentials = map[string]any{}
	}
	if strings.TrimSpace(ciphertext) != "" {
		// 已有密文：校验可解密，统计是否已经封存。
		stats.Already++
		plaintext, err := atrest.DecryptString(ciphertext)
		if err != nil {
			stats.Broken++
			slog.Error("at-rest verify: credentials_cipher cannot be decrypted", "table", TableAccounts, "id", id, "err", err)
			return nil
		}
		var decoded map[string]any
		if err := json.Unmarshal([]byte(plaintext), &decoded); err != nil {
			stats.Broken++
			slog.Error("at-rest verify: decrypted credentials is not JSON", "table", TableAccounts, "id", id, "err", err)
			return nil
		}
		if sealedCredentials(credentials) {
			return nil
		}
		stats.RemainingPlaintext++
		if opts.Verify || opts.DryRun {
			return nil
		}
		sealedJSON, err := marshalSealed(decoded)
		if err != nil {
			return err
		}
		return updateAccountSealed(ctx, db, id, sealedJSON, ciphertext)
	}

	stats.RemainingPlaintext++
	if sealedCredentials(credentials) {
		// 明文列已封存但密文缺失：明文已丢失，无法恢复，属于坏行。
		stats.Broken++
		slog.Error("at-rest verify: account is sealed but credentials_cipher is empty", "table", TableAccounts, "id", id)
		return nil
	}
	if opts.Verify || opts.DryRun {
		return nil
	}
	plainJSON, err := json.Marshal(credentials)
	if err != nil {
		return err
	}
	newCipher, err := atrest.EncryptString(string(plainJSON))
	if err != nil {
		return err
	}
	// 回读校验：确保密文可解密且与明文逐字节一致后再落库。
	readBack, err := atrest.DecryptString(newCipher)
	if err != nil || readBack != string(plainJSON) {
		return fmt.Errorf("encrypt read-back verification failed: %v", err)
	}
	sealedJSON, err := marshalSealed(credentials)
	if err != nil {
		return err
	}
	result, err := db.ExecContext(ctx, `
		UPDATE accounts
		SET credentials = $1::jsonb,
			credentials_cipher = $2
		WHERE id = $3
			AND credentials = $4::jsonb
			AND COALESCE(credentials_cipher, '') = ''
	`, sealedJSON, newCipher, id, string(plainJSON))
	if err != nil {
		return err
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if affected == 0 {
		// 并发写入了新凭据：本次跳过，下一轮迁移会重新处理。
		slog.Warn("at-rest migrate: account changed concurrently, skipped", "table", TableAccounts, "id", id)
		stats.RemainingPlaintext--
		return nil
	}
	stats.Migrated++
	stats.Sealed++
	stats.RemainingPlaintext--
	return nil
}

func updateAccountSealed(ctx context.Context, db *sql.DB, id int64, sealedJSON, ciphertext string) error {
	_, err := db.ExecContext(ctx, `
		UPDATE accounts
		SET credentials = $1::jsonb
		WHERE id = $2
			AND COALESCE(credentials_cipher, '') = $3
			AND NOT (credentials ? '_sealed')
	`, sealedJSON, id, ciphertext)
	return err
}

// ── proxies ─────────────────────────────────────────────────────────────────

func migrateProxies(ctx context.Context, db *sql.DB, opts Options) (TableResult, error) {
	var stats TableResult
	lastID := int64(0)
	for {
		rows, err := db.QueryContext(ctx, `
			SELECT id, COALESCE(password, ''), COALESCE(password_cipher, '')
			FROM proxies
			WHERE id > $1
			ORDER BY id ASC
			LIMIT $2
		`, lastID, opts.BatchSize)
		if err != nil {
			return stats, err
		}
		type proxyRow struct {
			id       int64
			password string
			cipher   string
		}
		batch := make([]proxyRow, 0, opts.BatchSize)
		for rows.Next() {
			var row proxyRow
			if err := rows.Scan(&row.id, &row.password, &row.cipher); err != nil {
				_ = rows.Close()
				return stats, err
			}
			batch = append(batch, row)
		}
		rowErr := rows.Err()
		_ = rows.Close()
		if rowErr != nil {
			return stats, rowErr
		}
		if len(batch) == 0 {
			break
		}
		for _, row := range batch {
			lastID = row.id
			stats.Scanned++
			if err := migrateOneProxy(ctx, db, &stats, row.id, row.password, row.cipher, opts); err != nil {
				return stats, fmt.Errorf("proxy id=%d: %w", row.id, err)
			}
		}
		if err := sleepBetweenBatches(ctx, opts.BatchGap); err != nil {
			return stats, err
		}
	}
	return stats, nil
}

func migrateOneProxy(ctx context.Context, db *sql.DB, stats *TableResult, id int64, password, ciphertext string, opts Options) error {
	if strings.TrimSpace(ciphertext) != "" {
		stats.Already++
		if _, err := atrest.DecryptString(ciphertext); err != nil {
			stats.Broken++
			slog.Error("at-rest verify: password_cipher cannot be decrypted", "table", TableProxies, "id", id, "err", err)
			return nil
		}
		if password == "" {
			return nil
		}
		stats.RemainingPlaintext++
		if opts.Verify || opts.DryRun {
			return nil
		}
		return clearProxyPlaintext(ctx, db, id, password)
	}
	if password == "" {
		return nil
	}
	if strings.HasPrefix(password, "enc:") {
		// 托管代理（enc:managed:v1:）等既有密文不算明文，保持原样。
		return nil
	}
	stats.RemainingPlaintext++
	if opts.Verify || opts.DryRun {
		return nil
	}
	newCipher, err := atrest.EncryptString(password)
	if err != nil {
		return err
	}
	readBack, err := atrest.DecryptString(newCipher)
	if err != nil || readBack != password {
		return fmt.Errorf("encrypt read-back verification failed: %v", err)
	}
	result, err := db.ExecContext(ctx, `
		UPDATE proxies
		SET password = '',
			password_cipher = $1,
			updated_at = NOW()
		WHERE id = $2
			AND password = $3
			AND COALESCE(password_cipher, '') = ''
	`, newCipher, id, password)
	if err != nil {
		return err
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if affected == 0 {
		slog.Warn("at-rest migrate: proxy changed concurrently, skipped", "table", TableProxies, "id", id)
		stats.RemainingPlaintext--
		return nil
	}
	stats.Migrated++
	stats.Sealed++
	stats.RemainingPlaintext--
	return nil
}

func clearProxyPlaintext(ctx context.Context, db *sql.DB, id int64, password string) error {
	_, err := db.ExecContext(ctx, `
		UPDATE proxies
		SET password = '', updated_at = NOW()
		WHERE id = $1 AND password = $2 AND password_cipher IS NOT NULL
	`, id, password)
	return err
}

// ── settings ────────────────────────────────────────────────────────────────

func migrateSettings(ctx context.Context, db *sql.DB, opts Options) (TableResult, error) {
	var stats TableResult
	rows, err := db.QueryContext(ctx, `
		SELECT key, value
		FROM settings
		WHERE key = ANY($1)
		ORDER BY key ASC
	`, pq.Array(repository.SensitiveSettingKeys))
	if err != nil {
		return stats, err
	}
	type settingRow struct {
		key   string
		value string
	}
	batch := make([]settingRow, 0, 32)
	for rows.Next() {
		var row settingRow
		if err := rows.Scan(&row.key, &row.value); err != nil {
			_ = rows.Close()
			return stats, err
		}
		batch = append(batch, row)
	}
	rowErr := rows.Err()
	_ = rows.Close()
	if rowErr != nil {
		return stats, rowErr
	}
	for _, row := range batch {
		stats.Scanned++
		if row.value == "" {
			continue
		}
		// SQL 已过滤白名单；这里再校验一次，防止误把非密钥配置改成密文。
		if !repository.IsSensitiveSettingKey(row.key) {
			continue
		}
		if atrest.IsEncrypted(row.value) {
			stats.Already++
			if _, err := atrest.DecryptString(row.value); err != nil {
				stats.Broken++
				slog.Error("at-rest verify: setting cannot be decrypted", "table", TableSettings, "key", row.key, "err", err)
			}
			continue
		}
		stats.RemainingPlaintext++
		if opts.Verify || opts.DryRun {
			continue
		}
		newCipher, err := atrest.EncryptString(row.value)
		if err != nil {
			return stats, fmt.Errorf("setting %s: %w", row.key, err)
		}
		readBack, err := atrest.DecryptString(newCipher)
		if err != nil || readBack != row.value {
			return stats, fmt.Errorf("setting %s: encrypt read-back verification failed: %v", row.key, err)
		}
		result, err := db.ExecContext(ctx, `
			UPDATE settings
			SET value = $1, updated_at = NOW()
			WHERE key = $2 AND value = $3
		`, newCipher, row.key, row.value)
		if err != nil {
			return stats, err
		}
		affected, err := result.RowsAffected()
		if err != nil {
			return stats, err
		}
		if affected == 0 {
			slog.Warn("at-rest migrate: setting changed concurrently, skipped", "table", TableSettings, "key", row.key)
			stats.RemainingPlaintext--
			continue
		}
		stats.Migrated++
		stats.Sealed++
		stats.RemainingPlaintext--
	}
	return stats, nil
}

// ── shared ──────────────────────────────────────────────────────────────────

func sealedCredentials(credentials map[string]any) bool {
	marker, ok := credentials["_sealed"].(bool)
	return ok && marker
}

// marshalSealed 计算与 repository.SealCredentials 同构的封存投影。
// （迁移器不导入 repository，避免把整棵依赖带进 CLI；两处逻辑有单测把关一致性。）
func marshalSealed(credentials map[string]any) (string, error) {
	sealed := make(map[string]any, len(credentials)+3)
	keys := make([]string, 0, len(credentials))
	for key, value := range credentials {
		if service.IsSensitiveCredentialKey(key) {
			if value != nil {
				keys = append(keys, key)
			}
			continue
		}
		sealed[key] = value
	}
	sortStrings(keys)
	sealed["_sealed"] = true
	if len(keys) > 0 {
		sealed["_sealed_keys"] = keys
	}
	for _, key := range []string{"api_key", "access_token"} {
		value, ok := credentials[key].(string)
		if !ok || value == "" {
			continue
		}
		if key == "access_token" {
			value = strings.TrimSpace(value)
		}
		sealed["_id_"+key] = atrest.IdentityDigest(key, value)
	}
	raw, err := json.Marshal(sealed)
	if err != nil {
		return "", err
	}
	return string(raw), nil
}

func sortStrings(values []string) {
	for i := 1; i < len(values); i++ {
		for j := i; j > 0 && values[j] < values[j-1]; j-- {
			values[j], values[j-1] = values[j-1], values[j]
		}
	}
}

func sleepBetweenBatches(ctx context.Context, gap time.Duration) error {
	if gap <= 0 {
		return nil
	}
	timer := time.NewTimer(gap)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}
