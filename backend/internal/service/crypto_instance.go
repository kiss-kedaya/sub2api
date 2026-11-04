package service

import (
	"os"
	"strconv"
	"strings"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/cryptopay/mode"
)

// CryptoInstanceEnabled reports whether this host runs crypto background work
// (chain scanning, pool maintenance and the admin console). It is a local
// deployment gate so several frontends can share one settings row without
// scanning the same chains twice. It never decides whether customers see the
// deposit entry: the shared admin switch does, and any instance that can derive
// addresses may serve the customer endpoints. The absent value preserves
// existing installations; malformed values fail closed.
func CryptoInstanceEnabled() bool {
	raw := strings.TrimSpace(os.Getenv("CRYPTO_INSTANCE_ENABLED"))
	if raw == "" {
		return true
	}
	enabled, err := strconv.ParseBool(raw)
	return err == nil && enabled
}

// CryptoUserAPIReady reports whether this process can actually serve the
// customer crypto endpoints. The shared crypto.config row decides whether the
// channel is on; this predicate only rejects hosts that cannot act on it (no
// persistent installation key, or a read-only review instance). The public
// entry and the user routes must use the same predicate so the sidebar never
// advertises a channel this process cannot fulfil.
func CryptoUserAPIReady(cfg *config.Config) bool {
	if CryptoAdminTestReadOnly() {
		return false
	}
	return cfg != nil && cfg.Totp.HasPersistentEncryptionKey()
}

// CryptoAdminAllowHTTP is an explicit local opt-in for installations without
// TLS. It only relaxes the transport gate; administrator identity, fresh TOTP,
// replay protection and wallet authorization remain mandatory. Invalid or
// missing values keep the default HTTPS requirement.
func CryptoAdminAllowHTTP() bool {
	allowed, err := strconv.ParseBool(strings.TrimSpace(os.Getenv("CRYPTO_ADMIN_ALLOW_HTTP")))
	return err == nil && allowed
}

// CryptoAdminTestMode restricts the initial shared-database test to real admin
// accounts. This is an instance setting, never a database-wide user policy.
func CryptoAdminTestMode() bool {
	return mode.TestInstance()
}

// CryptoAdminTestReadOnly reports the effective write gate without disabling
// isolated JWT bootstrap, migration validation or background-job suppression.
func CryptoAdminTestReadOnly() bool { return mode.ReadOnly() }
