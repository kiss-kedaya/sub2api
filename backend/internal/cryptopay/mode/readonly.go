// Package mode holds the local shared-database review safety gate.
package mode

import (
	"errors"
	"os"
	"strconv"
	"strings"
)

var ErrReadOnly = errors.New("cryptopay is read-only in CRYPTO_ADMIN_TEST_MODE")

// TestInstance deliberately fails closed for malformed nonempty values.
// Startup isolation and background-job suppression apply even when writes
// have been explicitly enabled for a shared-database test instance.
func TestInstance() bool {
	raw := strings.TrimSpace(os.Getenv("CRYPTO_ADMIN_TEST_MODE"))
	if raw == "" {
		return false
	}
	enabled, err := strconv.ParseBool(raw)
	return err != nil || enabled
}

// ReadOnly is lifted only by two explicit valid deployment values. Neither
// malformed test mode nor a malformed write switch may unlock production data.
// Shared settings and HTTP requests cannot change these process-local gates.
func ReadOnly() bool {
	if !TestInstance() {
		return false
	}
	test, testErr := strconv.ParseBool(strings.TrimSpace(os.Getenv("CRYPTO_ADMIN_TEST_MODE")))
	write, writeErr := strconv.ParseBool(strings.TrimSpace(os.Getenv("CRYPTO_ADMIN_TEST_WRITE_ENABLED")))
	return testErr != nil || !test || writeErr != nil || !write
}

func CheckWritable() error {
	if ReadOnly() {
		return ErrReadOnly
	}
	return nil
}
