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
	if shouldEnsureSimpleModeStartupState(config.RunModeSimple) {
		t.Fatal("test mode must skip default-group and admin-concurrency writes")
	}
}

func TestNormalSimpleModeStillInitializesStartupState(t *testing.T) {
	t.Setenv("CRYPTO_ADMIN_TEST_MODE", "false")
	if !shouldEnsureSimpleModeStartupState(config.RunModeSimple) {
		t.Fatal("normal simple mode must retain startup initialization")
	}
}
