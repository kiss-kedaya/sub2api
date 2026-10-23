package main

import (
	"context"
	"database/sql"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/repository"
	"github.com/Wei-Shaw/sub2api/internal/service"
)

var (
	errAdmin2FAApprovalArguments = errors.New("server authorization requires only -issue-admin-2fa-code -user-id <positive ID>")
	errAdmin2FAApprovalConfig    = errors.New("server authorization failed: unable to load configuration")
	errAdmin2FAApprovalDatabase  = errors.New("server authorization failed: database operation could not be completed")
	errAdmin2FAApprovalCache     = errors.New("server authorization failed: authorization cache unavailable")
	errAdmin2FAApprovalOutput    = errors.New("server authorization was issued, but its code could not be written; issue a new code")
)

const admin2FAApprovalUserSQL = `SELECT id, role, status, totp_enabled, totp_secret_encrypted
FROM users WHERE id = $1 AND role = 'admin' AND status = 'active' AND deleted_at IS NULL`

// Handle this before the legacy recovery dispatcher because both use user-id.
// A misplaced/explicitly false issue flag must never fall through to startup.
func dispatchAdmin2FAApproval(fs *flag.FlagSet, enabled bool, userID int64, run func(int64) error) (bool, error) {
	requested, conflicting := false, false
	fs.Visit(func(f *flag.Flag) {
		switch f.Name {
		case "issue-admin-2fa-code":
			requested = true
		case "user-id":
		default:
			conflicting = true
		}
	})
	for _, arg := range fs.Args() {
		name, _, _ := strings.Cut(strings.TrimLeft(arg, "-"), "=")
		if name == "issue-admin-2fa-code" {
			requested = true
		}
	}
	if !requested {
		return false, nil
	}
	if !enabled || userID <= 0 || conflicting || fs.NArg() != 0 || run == nil {
		return true, errAdmin2FAApprovalArguments
	}
	return true, run(userID)
}

func runAdmin2FAApprovalCommand(userID int64) error {
	if service.CryptoAdminTestMode() {
		return service.ErrAdmin2FAApprovalReadOnly
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	return executeAdmin2FAApproval(ctx, userID, os.Stdout, config.LoadForBootstrap, openAdmin2FAResetDatabase, openAdmin2FAApprovalStore)
}

type admin2FAApprovalSQLReader struct{ db *sql.DB }

func (r admin2FAApprovalSQLReader) GetByID(ctx context.Context, userID int64) (*service.User, error) {
	var user service.User
	var secret sql.NullString
	err := r.db.QueryRowContext(ctx, admin2FAApprovalUserSQL, userID).Scan(&user.ID, &user.Role, &user.Status, &user.TotpEnabled, &secret)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, errAdmin2FAApprovalDatabase
	}
	if secret.Valid {
		user.TotpSecretEncrypted = &secret.String
	}
	return &user, nil
}

func openAdmin2FAApprovalStore(cfg *config.Config) (service.Admin2FAApprovalStore, func(), error) {
	isolated := *cfg
	isolated.Redis.PoolSize = 1
	isolated.Redis.MinIdleConns = 0
	isolated.Redis.DialTimeoutSeconds = 3
	isolated.Redis.ReadTimeoutSeconds = 3
	isolated.Redis.WriteTimeoutSeconds = 3
	rdb := repository.InitRedis(&isolated)
	store, ok := repository.NewTotpCache(rdb).(service.Admin2FAApprovalStore)
	if !ok {
		_ = rdb.Close()
		return nil, nil, errAdmin2FAApprovalCache
	}
	return store, func() { _ = rdb.Close() }, nil
}

func executeAdmin2FAApproval(
	ctx context.Context,
	userID int64,
	out io.Writer,
	loadConfig func() (*config.Config, error),
	openDatabase func(*config.Config) (*sql.DB, error),
	openStore func(*config.Config) (service.Admin2FAApprovalStore, func(), error),
) error {
	if service.CryptoAdminTestMode() {
		return service.ErrAdmin2FAApprovalReadOnly
	}
	if userID <= 0 || out == nil || loadConfig == nil || openDatabase == nil || openStore == nil {
		return errAdmin2FAApprovalArguments
	}
	cfg, err := loadConfig()
	if err != nil || cfg == nil {
		return errAdmin2FAApprovalConfig
	}
	db, err := openDatabase(cfg)
	if err != nil || db == nil {
		return errAdmin2FAApprovalDatabase
	}
	defer func() { _ = db.Close() }()
	store, closeStore, err := openStore(cfg)
	if err != nil || store == nil {
		return errAdmin2FAApprovalCache
	}
	if closeStore != nil {
		defer closeStore()
	}
	code, expiresAt, err := service.NewAdmin2FAApprovalService(admin2FAApprovalSQLReader{db: db}, store).Issue(ctx, userID)
	if err != nil {
		return err
	}
	// The code is printed only to the operator's explicit CLI output, never to
	// application logs, HTTP responses or persistent configuration.
	_, err = fmt.Fprintf(out, "Administrator two-factor disable authorization code: %s\nExpires at (UTC): %s\nThis code can be used once for the requested administrator only.\n", code, expiresAt.UTC().Format(time.RFC3339))
	if err != nil {
		return errAdmin2FAApprovalOutput
	}
	return nil
}
