package repository

import (
	"strings"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/config"
)

func TestCryptoAdminTestModeRejectsMigrationWritesBeforeConnecting(t *testing.T) {
	t.Setenv("CRYPTO_ADMIN_TEST_MODE", "true")
	for _, migrationMode := range []string{"", config.DatabaseMigrationModeApply} {
		cfg := &config.Config{Database: config.DatabaseConfig{MigrationMode: migrationMode}}
		_, _, err := InitEnt(cfg)
		if err == nil || !strings.Contains(err.Error(), "DATABASE_MIGRATION_MODE=validate") {
			t.Fatalf("mode %q must fail before database access: %v", migrationMode, err)
		}
	}
}

func TestCryptoAdminTestModeSkipsSimpleModeStartupWrites(t *testing.T) {
	t.Setenv("CRYPTO_ADMIN_TEST_MODE", "true")
	t.Setenv("CRYPTO_ADMIN_TEST_WRITE_ENABLED", "true")
	if shouldEnsureSimpleModeStartupState(config.RunModeSimple) {
		t.Fatal("test mode must skip default-group and admin-concurrency writes")
	}
}

func TestCryptoWritableReviewKeepsSeparateJWTSecretAndMigrationValidation(t *testing.T) {
	t.Setenv("CRYPTO_ADMIN_TEST_MODE", "true")
	t.Setenv("CRYPTO_ADMIN_TEST_WRITE_ENABLED", "true")
	if !readOnlyBootstrapSecrets() {
		t.Fatal("writable test instance must never reuse the production JWT signing key")
	}
	cfg := &config.Config{JWT: config.JWTConfig{Secret: strings.Repeat("x", 40)}}
	if err := loadIsolatedTestJWTSecret(cfg); err != nil {
		t.Fatal(err)
	}
	_, _, err := InitEnt(&config.Config{Database: config.DatabaseConfig{MigrationMode: config.DatabaseMigrationModeApply}})
	if err == nil || !strings.Contains(err.Error(), "DATABASE_MIGRATION_MODE=validate") {
		t.Fatalf("writable review permitted schema writes: %v", err)
	}
}

func TestNormalSimpleModeStillInitializesStartupState(t *testing.T) {
	t.Setenv("CRYPTO_ADMIN_TEST_MODE", "false")
	if !shouldEnsureSimpleModeStartupState(config.RunModeSimple) {
		t.Fatal("normal simple mode must retain startup initialization")
	}
}
