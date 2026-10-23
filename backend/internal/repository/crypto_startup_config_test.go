package repository

import (
	"context"
	"strings"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/config"
)

func TestCryptoReviewMigrationCommandRejectsWritesBeforeConnecting(t *testing.T) {
	t.Setenv("CRYPTO_ADMIN_TEST_MODE", "true")
	err := RunConfiguredMigrations(context.Background(), &config.Config{}, false)
	if err == nil || !strings.Contains(err.Error(), "forbids migration writes") {
		t.Fatalf("expected read-only entry-point rejection before opening a connection, got %v", err)
	}
}

func TestCryptoReviewRejectsMalformedExplicitTOTPKeyBeforeConnecting(t *testing.T) {
	t.Setenv("CRYPTO_ADMIN_TEST_MODE", "true")
	cfg := &config.Config{Database: config.DatabaseConfig{MigrationMode: config.DatabaseMigrationModeValidate}}
	cfg.Totp.EncryptionKeyConfigured = true
	cfg.Totp.EncryptionKey = "invalid"
	_, _, err := InitEnt(cfg)
	if err == nil || !strings.Contains(err.Error(), "TOTP key") {
		t.Fatalf("expected malformed key rejection before database access, got %v", err)
	}
}
