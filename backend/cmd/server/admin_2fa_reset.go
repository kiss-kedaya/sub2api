package main

import (
	"context"
	"database/sql"
	"errors"
	"flag"
	"fmt"
	"io"
	"net"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/repository"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/lib/pq"
	"github.com/redis/go-redis/v9"
)

var (
	errAdmin2FAResetArguments = errors.New("administrator TOTP recovery requires only -reset-admin-2fa -user-id <positive ID> -confirm")
	errAdmin2FAResetConfig    = errors.New("administrator TOTP recovery failed: unable to load configuration")
	errAdmin2FAResetDatabase  = errors.New("administrator TOTP recovery failed: database operation could not be completed")
	errAdmin2FAResetIdentity  = errors.New("administrator TOTP recovery refused: no active administrator matched the requested user ID")
	errAdmin2FAResetCommit    = errors.New("administrator TOTP recovery commit could not be confirmed; verify account state before retrying")
	errAdmin2FAResetOutput    = errors.New("administrator TOTP recovery completed, but its result could not be written")
)

const admin2FAResetSQL = `UPDATE users
SET totp_enabled = FALSE, totp_secret_encrypted = NULL, totp_enabled_at = NULL, updated_at = CURRENT_TIMESTAMP
WHERE id = $1 AND role = 'admin' AND status = 'active' AND deleted_at IS NULL
RETURNING id`

// A recovery-related flag always consumes the command, including invalid or
// explicitly false flags. It must never accidentally fall through to HTTP setup,
// migrations, dependency wiring, or normal server startup.
func dispatchAdmin2FAReset(fs *flag.FlagSet, enabled bool, userID int64, confirmed bool, run func(int64) error) (bool, error) {
	requested, conflicting := false, false
	fs.Visit(func(f *flag.Flag) {
		switch f.Name {
		case "reset-admin-2fa", "user-id", "confirm":
			requested = true
		default:
			conflicting = true
		}
	})
	// flag parsing stops at a positional argument or "--". A misplaced
	// recovery flag still signals recovery intent and must not start the server.
	for _, arg := range fs.Args() {
		name, _, _ := strings.Cut(strings.TrimLeft(arg, "-"), "=")
		switch name {
		case "reset-admin-2fa", "user-id", "confirm":
			requested = true
		}
	}
	if !requested {
		return false, nil
	}
	if !enabled || userID <= 0 || !confirmed || conflicting || fs.NArg() != 0 || run == nil {
		return true, errAdmin2FAResetArguments
	}
	return true, run(userID)
}

func runAdmin2FAResetCommand(userID int64) error {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	return executeAdmin2FAReset(ctx, userID, os.Stdout, config.LoadForBootstrap, openAdmin2FAResetDatabase, clearAdmin2FAResetSessions)
}

func openAdmin2FAResetDatabase(cfg *config.Config) (*sql.DB, error) {
	// URL encoding prevents whitespace and punctuation in existing credentials
	// from being interpreted as additional connection options.
	dsn := &url.URL{
		Scheme: "postgres",
		User:   url.UserPassword(cfg.Database.User, cfg.Database.Password),
		Host:   net.JoinHostPort(cfg.Database.Host, strconv.Itoa(cfg.Database.Port)),
		Path:   "/" + cfg.Database.DBName,
	}
	options := url.Values{"sslmode": {cfg.Database.SSLMode}, "connect_timeout": {"10"}}
	dsn.RawQuery = options.Encode()
	connector, err := pq.NewConnector(dsn.String())
	if err != nil {
		return nil, errAdmin2FAResetDatabase
	}
	db := sql.OpenDB(connector)
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)
	return db, nil
}

func executeAdmin2FAReset(
	ctx context.Context,
	userID int64,
	out io.Writer,
	loadConfig func() (*config.Config, error),
	openDatabase func(*config.Config) (*sql.DB, error),
	clearSessions func(context.Context, *config.Config, int64) error,
) error {
	if userID <= 0 {
		return errAdmin2FAResetArguments
	}
	cfg, err := loadConfig()
	if err != nil || cfg == nil {
		return errAdmin2FAResetConfig
	}
	db, err := openDatabase(cfg)
	if err != nil || db == nil {
		return errAdmin2FAResetDatabase
	}
	defer func() { _ = db.Close() }()
	if err := resetAdmin2FAInDatabase(ctx, db, userID); err != nil {
		return err
	}

	// Recovery is already committed. Redis failure must not be reported as an
	// unchanged database, and should not prevent a forgotten-TOTP recovery.
	message := "Administrator two-factor authentication has been reset.\n"
	if clearSessions == nil || clearSessions(ctx, cfg, userID) != nil {
		message += "Warning: database recovery completed, but refresh-session and step-up cleanup could not be completed; some sessions may remain active.\n"
	} else {
		message += "Refresh sessions and discoverable step-up grants have been revoked.\n"
	}
	message += "Existing access JWTs remain valid until their current expiry; this command does not revoke them.\n"
	if _, err := io.WriteString(out, message); err != nil {
		return errAdmin2FAResetOutput
	}
	return nil
}

func resetAdmin2FAInDatabase(ctx context.Context, db *sql.DB, userID int64) error {
	if userID <= 0 {
		return errAdmin2FAResetArguments
	}
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return errAdmin2FAResetDatabase
	}
	defer func() { _ = tx.Rollback() }()
	var matchedID int64
	if err := tx.QueryRowContext(ctx, admin2FAResetSQL, userID).Scan(&matchedID); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return errAdmin2FAResetIdentity
		}
		return errAdmin2FAResetDatabase
	}
	if matchedID != userID {
		return errAdmin2FAResetDatabase
	}
	if err := tx.Commit(); err != nil {
		return errAdmin2FAResetCommit
	}
	return nil
}

func clearAdmin2FAResetSessions(ctx context.Context, cfg *config.Config, userID int64) error {
	// Keep recovery's connection budget small even if the server's normal pool
	// is large. Reuse the configured Redis credentials and TLS setup.
	recoveryConfig := *cfg
	recoveryConfig.Redis.PoolSize = 1
	recoveryConfig.Redis.MinIdleConns = 0
	recoveryConfig.Redis.DialTimeoutSeconds = 3
	recoveryConfig.Redis.ReadTimeoutSeconds = 3
	recoveryConfig.Redis.WriteTimeoutSeconds = 3
	rdb := repository.InitRedis(&recoveryConfig)
	defer func() { _ = rdb.Close() }()
	return clearAdmin2FAResetSessionsWithCache(ctx, repository.NewRefreshTokenCache(rdb), rdb, userID)
}

func clearAdmin2FAResetSessionsWithCache(ctx context.Context, cache service.RefreshTokenCache, rdb *redis.Client, userID int64) error {
	hashes, lookupErr := cache.GetUserTokenHashes(ctx, userID)
	unsafeIndex := lookupErr != nil
	ownedHashes := make([]string, 0, len(hashes))
	// Legacy sessions use u<id>; current sessions use refresh-token family IDs.
	// Discover only this user's indexed sessions. Never scan the Redis database.
	keys := map[string]struct{}{
		fmt.Sprintf("totp:stepup:%d:u%d", userID, userID): {},
		fmt.Sprintf("totp:setup:%d", userID):              {},
		fmt.Sprintf("totp:attempts:%d", userID):           {},
	}
	for _, hash := range hashes {
		data, err := cache.GetRefreshToken(ctx, hash)
		if errors.Is(err, service.ErrRefreshTokenNotFound) {
			continue
		}
		if err != nil {
			lookupErr = err
			unsafeIndex = true
			continue
		}
		if data == nil || data.UserID != userID {
			lookupErr = errAdmin2FAResetIdentity
			unsafeIndex = true
			continue
		}
		ownedHashes = append(ownedHashes, hash)
		if data.FamilyID != "" {
			keys[fmt.Sprintf("totp:stepup:%d:%s", userID, data.FamilyID)] = struct{}{}
		}
	}
	var revokeErr error
	if unsafeIndex {
		// A malformed or cross-user index must not authorize deleting a token
		// whose ownership could not be verified.
		for _, hash := range ownedHashes {
			revokeErr = errors.Join(revokeErr, cache.DeleteRefreshToken(ctx, hash))
		}
	} else {
		revokeErr = cache.DeleteUserRefreshTokens(ctx, userID)
	}
	toDelete := make([]string, 0, len(keys))
	for key := range keys {
		toDelete = append(toDelete, key)
	}
	_, grantErr := rdb.Del(ctx, toDelete...).Result()
	return errors.Join(lookupErr, revokeErr, grantErr)
}
