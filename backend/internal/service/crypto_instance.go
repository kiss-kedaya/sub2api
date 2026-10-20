package service

import (
	"os"
	"strconv"
	"strings"
)

// CryptoInstanceEnabled is a local deployment gate. A shared settings row may
// enable deposits only on instances whose operator permits the feature. The
// absent value preserves existing installations; malformed values fail closed.
func CryptoInstanceEnabled() bool {
	raw := strings.TrimSpace(os.Getenv("CRYPTO_INSTANCE_ENABLED"))
	if raw == "" {
		return true
	}
	enabled, err := strconv.ParseBool(raw)
	return err == nil && enabled
}

// CryptoAdminTestMode restricts the initial shared-database test to real admin
// accounts. This is an instance setting, never a database-wide user policy.
func CryptoAdminTestMode() bool {
	raw := strings.TrimSpace(os.Getenv("CRYPTO_ADMIN_TEST_MODE"))
	if raw == "" {
		return false
	}
	enabled, err := strconv.ParseBool(raw)
	return err != nil || enabled
}
