// Package mode holds the local shared-database review safety gate.
package mode

import (
	"errors"
	"os"
	"strconv"
	"strings"
)

var ErrReadOnly = errors.New("cryptopay is read-only in CRYPTO_ADMIN_TEST_MODE")

// ReadOnly deliberately fails closed for malformed nonempty values. This is
// a process-local restriction and must never be loaded from shared settings.
func ReadOnly() bool {
	raw := strings.TrimSpace(os.Getenv("CRYPTO_ADMIN_TEST_MODE"))
	if raw == "" {
		return false
	}
	enabled, err := strconv.ParseBool(raw)
	return err != nil || enabled
}

func CheckWritable() error {
	if ReadOnly() {
		return ErrReadOnly
	}
	return nil
}
