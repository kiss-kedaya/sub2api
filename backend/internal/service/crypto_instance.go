package service

import (
	"os"
	"strconv"
	"strings"

	"github.com/Wei-Shaw/sub2api/internal/cryptopay/mode"
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
	return mode.TestInstance()
}

// CryptoAdminTestReadOnly reports the effective write gate without disabling
// isolated JWT bootstrap, migration validation or background-job suppression.
func CryptoAdminTestReadOnly() bool { return mode.ReadOnly() }
