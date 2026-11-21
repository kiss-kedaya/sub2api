package repository

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"strings"
	"time"

	"github.com/Wei-Shaw/sub2api/ent"
	"github.com/Wei-Shaw/sub2api/ent/securitysecret"
	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/cryptopay/wallet"
	"github.com/Wei-Shaw/sub2api/internal/payment"
	"github.com/Wei-Shaw/sub2api/internal/service"
)

const (
	securitySecretKeyJWT        = "jwt_secret"
	securitySecretKeyTOTP       = "totp_encryption_key"
	securitySecretReadRetryMax  = 5
	securitySecretReadRetryWait = 10 * time.Millisecond
)

var readRandomBytes = rand.Read

func ensureBootstrapSecrets(ctx context.Context, client *ent.Client, cfg *config.Config) error {
	if client == nil {
		return fmt.Errorf("nil ent client")
	}
	if cfg == nil {
		return fmt.Errorf("nil config")
	}
	if readOnlyBootstrapSecrets() {
		if err := loadIsolatedTestJWTSecret(cfg); err != nil {
			return err
		}
		return ensureTOTPInstallationKey(ctx, client, cfg, true)
	}

	cfg.JWT.Secret = strings.TrimSpace(cfg.JWT.Secret)
	if cfg.JWT.Secret != "" {
		storedSecret, err := createSecuritySecretIfAbsent(ctx, client, securitySecretKeyJWT, cfg.JWT.Secret)
		if err != nil {
			return fmt.Errorf("persist jwt secret: %w", err)
		}
		if storedSecret != cfg.JWT.Secret {
			log.Println("Warning: configured JWT secret mismatches persisted value; using persisted secret for cross-instance consistency.")
		}
		cfg.JWT.Secret = storedSecret
		return ensureTOTPInstallationKey(ctx, client, cfg, false)
	}

	secret, created, err := getOrCreateGeneratedSecuritySecret(ctx, client, securitySecretKeyJWT, 32)
	if err != nil {
		return fmt.Errorf("ensure jwt secret: %w", err)
	}
	cfg.JWT.Secret = secret

	if created {
		log.Println("Warning: JWT secret auto-generated and persisted to database. Consider rotating to a managed secret for production.")
	}
	return ensureTOTPInstallationKey(ctx, client, cfg, false)
}

// ensureTOTPInstallationKey supplies the same durable installation key to TOTP,
// payment configuration and the wallet. An explicit legacy key is persisted
// unchanged, so upgrading never rotates existing ciphertext. A conflicting
// configuration fails closed instead of silently replacing either key.
func ensureTOTPInstallationKey(ctx context.Context, client *ent.Client, cfg *config.Config, readOnly bool) error {
	configured := cfg.Totp.HasPersistentEncryptionKey()
	configuredKey := strings.TrimSpace(cfg.Totp.EncryptionKey)
	if configured {
		if err := validateTOTPInstallationKey(configuredKey); err != nil {
			return fmt.Errorf("configured TOTP installation key: %w", err)
		}
	}
	if readOnly && configured {
		// Isolated review instances may supply their own key but never persist
		// it into, or replace a secret in, the shared database.
		cfg.Totp.EncryptionKey = configuredKey
		return nil
	}

	var key string
	stored, err := client.SecuritySecret.Query().Where(securitysecret.KeyEQ(securitySecretKeyTOTP)).Only(ctx)
	if err == nil {
		key = strings.TrimSpace(stored.Value)
		if configured && !strings.EqualFold(key, configuredKey) {
			return fmt.Errorf("configured TOTP installation key conflicts with the persisted installation key; restore the original key instead of rotating existing ciphertext")
		}
	} else if !ent.IsNotFound(err) {
		return fmt.Errorf("load TOTP installation key: %w", err)
	} else {
		if readOnly {
			return fmt.Errorf("read-only crypto test mode requires an existing persistent installation key or an explicitly configured TOTP_ENCRYPTION_KEY")
		}
		// A legacy deployment's explicit key must authenticate every durable
		// secret BEFORE it can become the installation key. With no key, even
		// one existing ciphertext forbids creating a replacement key.
		if err := inspectLegacyInstallationSecrets(ctx, client, configuredKey, configured); err != nil {
			return err
		}
		if configured {
			key, err = createSecuritySecretIfAbsent(ctx, client, securitySecretKeyTOTP, configuredKey)
			if err == nil && !strings.EqualFold(key, configuredKey) {
				return fmt.Errorf("configured TOTP installation key conflicts with the persisted installation key; restore the original key instead of rotating existing ciphertext")
			}
		} else {
			// Discard config.Load's temporary key. Atomic insert-or-read keeps
			// concurrent first boots on one generated installation key.
			key, _, err = getOrCreateGeneratedSecuritySecret(ctx, client, securitySecretKeyTOTP, 32)
		}
		if err != nil {
			return fmt.Errorf("persist TOTP installation key: %w", err)
		}
	}
	if err := validateTOTPInstallationKey(key); err != nil {
		return fmt.Errorf("stored TOTP installation key: %w", err)
	}
	cfg.Totp.EncryptionKey = key
	cfg.Totp.EncryptionKeyPersistent = true
	return nil
}

// inspectLegacyInstallationSecrets runs only before first registration. Every
// durable consumer of the shared AES key belongs here, including disabled and
// soft-deleted records. Queries stream values and never include user identities
// or secret material in errors. JWT-derived Live attestations, plugin UI tokens,
// and other short-lived capabilities do not use this persistent-key inventory.
func inspectLegacyInstallationSecrets(ctx context.Context, client *ent.Client, keyHex string, verify bool) error {
	var encryptor service.SecretEncryptor
	var key []byte
	if verify {
		var err error
		encryptor, err = NewAESEncryptor(&config.Config{Totp: config.TotpConfig{EncryptionKey: keyHex}})
		if err != nil {
			return fmt.Errorf("invalid installation encryption key")
		}
		key, _ = hex.DecodeString(keyHex)
		defer clear(key)
	}
	check := func(source, format, value string) error {
		if value == "" {
			return nil
		}
		if format == "aes_optional_plaintext" {
			// Classify the legacy plaintext compatibility case before requiring
			// any key: plaintext has no installation-key dependency.
			decoded, err := base64.StdEncoding.DecodeString(value)
			if err != nil || len(decoded) < 12+16 {
				return nil
			}
		}
		if !verify {
			return fmt.Errorf("existing encrypted %s requires the original TOTP_ENCRYPTION_KEY before installation-key bootstrap", source)
		}
		valid := false
		switch format {
		case "aes", "aes_optional_plaintext":
			_, err := encryptor.Decrypt(value)
			valid = err == nil
		case "payment":
			// The legacy decoder assumes a 12-byte nonce. Validate its framing
			// first so malformed data cannot panic during startup validation.
			parts := strings.Split(value, ":")
			if len(parts) == 3 {
				nonce, nonceErr := base64.StdEncoding.DecodeString(parts[0])
				tag, tagErr := base64.StdEncoding.DecodeString(parts[1])
				if nonceErr == nil && tagErr == nil && len(nonce) == 12 && len(tag) == 16 {
					//nolint:staticcheck // Registration must preserve historical payment ciphertext.
					_, err := payment.Decrypt(value, key)
					valid = err == nil
				}
			}
		case "wallet":
			// Reuse the wallet's domain-separated cipher and format checks.
			// This call occurs after settings rows are closed, so a one-connection
			// pool cannot deadlock on the wallet's read.
			db, err := ProvideSQLDB(client)
			if err == nil {
				manager := wallet.New(db, keyHex)
				status, err := manager.Status(ctx)
				manager.Close()
				valid = err == nil && status != nil && status.Configured && status.Ready
			}
		}
		if !valid {
			return fmt.Errorf("configured installation key cannot decrypt existing %s; no installation key was registered", source)
		}
		return nil
	}
	stream := func(source, query string, visit func(string) error) error {
		rows, err := client.QueryContext(ctx, query)
		if err != nil {
			return fmt.Errorf("inspect installation secrets in %s failed", source)
		}
		defer func() { _ = rows.Close() }()
		for rows.Next() {
			var value string
			if rows.Scan(&value) != nil {
				return fmt.Errorf("inspect installation secrets in %s failed", source)
			}
			if err := visit(value); err != nil {
				return err
			}
		}
		if rows.Err() != nil {
			return fmt.Errorf("inspect installation secrets in %s failed", source)
		}
		return nil
	}
	for _, field := range []struct{ source, query string }{
		{"TOTP authenticators", `SELECT totp_secret_encrypted FROM users WHERE totp_secret_encrypted IS NOT NULL AND totp_secret_encrypted <> ''`},
		{"Bark device keys", `SELECT bark_device_key_encrypted FROM users WHERE bark_device_key_encrypted <> ''`},
		{"channel monitor keys", `SELECT api_key_encrypted FROM channel_monitors WHERE api_key_encrypted <> ''`},
		{"plugin configuration", `SELECT config_encrypted FROM sub2api_plugin_installations WHERE config_encrypted <> ''`},
	} {
		if err := stream(field.source, field.query, func(value string) error { return check(field.source, "aes", value) }); err != nil {
			return err
		}
	}
	if err := stream("Ollama sessions", `SELECT extra FROM accounts WHERE extra IS NOT NULL`, func(raw string) error {
		var fields map[string]json.RawMessage
		if json.Unmarshal([]byte(raw), &fields) != nil {
			return fmt.Errorf("inspect installation secrets in account metadata failed")
		}
		if session, ok := fields[service.OllamaCloudUsageSessionExtraKey]; ok && string(session) != "null" {
			var value string
			if json.Unmarshal(session, &value) != nil {
				return fmt.Errorf("inspect installation secrets in Ollama sessions failed")
			}
			return check("Ollama sessions", "aes", value)
		}
		return nil
	}); err != nil {
		return err
	}
	if err := stream("payment provider configuration", `SELECT config FROM payment_provider_instances WHERE config <> ''`, func(value string) error {
		if json.Valid([]byte(value)) {
			return nil // Current provider configuration is plaintext JSON.
		}
		return check("payment provider configuration", "payment", value)
	}); err != nil {
		return err
	}
	// Collect this bounded settings subset before wallet validation re-reads it.
	rows, err := client.QueryContext(ctx, `SELECT key, value FROM settings WHERE key IN ('backup_s3_config','image_storage_config','prompt_audit_config','crypto.signer.vault') AND value <> ''`)
	if err != nil {
		return fmt.Errorf("inspect installation secrets in settings failed")
	}
	values := map[string]string{}
	for rows.Next() {
		var name, value string
		if rows.Scan(&name, &value) != nil {
			_ = rows.Close()
			return fmt.Errorf("inspect installation secrets in settings failed")
		}
		values[name] = value
	}
	err = rows.Err()
	_ = rows.Close()
	if err != nil {
		return fmt.Errorf("inspect installation secrets in settings failed")
	}
	for _, name := range []string{"backup_s3_config", "image_storage_config", "prompt_audit_config"} {
		raw := values[name]
		if raw == "" {
			continue
		}
		var stored struct {
			SecretAccessKey string `json:"secret_access_key"`
			Endpoints       []struct {
				TokenCiphertext string `json:"token_ciphertext"`
			} `json:"endpoints"`
		}
		if json.Unmarshal([]byte(raw), &stored) != nil {
			return fmt.Errorf("inspect installation secrets in %s failed", name)
		}
		if name == "prompt_audit_config" {
			for _, endpoint := range stored.Endpoints {
				if err := check("prompt audit node tokens", "aes", endpoint.TokenCiphertext); err != nil {
					return err
				}
			}
		} else if err := check(name, "aes_optional_plaintext", stored.SecretAccessKey); err != nil {
			return err
		}
	}
	return check("wallet seeds", "wallet", values["crypto.signer.vault"])
}

func validateTOTPInstallationKey(value string) error {
	decoded, err := hex.DecodeString(value)
	if err != nil || len(decoded) != 32 {
		return fmt.Errorf("must be 32 bytes encoded as 64 hexadecimal characters")
	}
	return nil
}

func readOnlyBootstrapSecrets() bool {
	return service.CryptoAdminTestMode()
}

func loadIsolatedTestJWTSecret(cfg *config.Config) error {
	secret := strings.TrimSpace(cfg.JWT.Secret)
	if len([]byte(secret)) < 32 {
		return fmt.Errorf("a separate JWT_SECRET of at least 32 bytes is required in read-only test mode")
	}
	cfg.JWT.Secret = secret
	return nil
}

func getOrCreateGeneratedSecuritySecret(ctx context.Context, client *ent.Client, key string, byteLength int) (string, bool, error) {
	existing, err := client.SecuritySecret.Query().Where(securitysecret.KeyEQ(key)).Only(ctx)
	if err == nil {
		value := strings.TrimSpace(existing.Value)
		if len([]byte(value)) < 32 {
			return "", false, fmt.Errorf("stored secret %q must be at least 32 bytes", key)
		}
		return value, false, nil
	}
	if !ent.IsNotFound(err) {
		return "", false, err
	}

	generated, err := generateHexSecret(byteLength)
	if err != nil {
		return "", false, err
	}

	if err := client.SecuritySecret.Create().
		SetKey(key).
		SetValue(generated).
		OnConflictColumns(securitysecret.FieldKey).
		DoNothing().
		Exec(ctx); err != nil {
		if !isSQLNoRowsError(err) {
			return "", false, err
		}
	}

	stored, err := querySecuritySecretWithRetry(ctx, client, key)
	if err != nil {
		return "", false, err
	}
	value := strings.TrimSpace(stored.Value)
	if len([]byte(value)) < 32 {
		return "", false, fmt.Errorf("stored secret %q must be at least 32 bytes", key)
	}
	return value, value == generated, nil
}

func createSecuritySecretIfAbsent(ctx context.Context, client *ent.Client, key, value string) (string, error) {
	value = strings.TrimSpace(value)
	if len([]byte(value)) < 32 {
		return "", fmt.Errorf("secret %q must be at least 32 bytes", key)
	}

	if err := client.SecuritySecret.Create().
		SetKey(key).
		SetValue(value).
		OnConflictColumns(securitysecret.FieldKey).
		DoNothing().
		Exec(ctx); err != nil {
		if !isSQLNoRowsError(err) {
			return "", err
		}
	}

	stored, err := querySecuritySecretWithRetry(ctx, client, key)
	if err != nil {
		return "", err
	}
	storedValue := strings.TrimSpace(stored.Value)
	if len([]byte(storedValue)) < 32 {
		return "", fmt.Errorf("stored secret %q must be at least 32 bytes", key)
	}
	return storedValue, nil
}

func querySecuritySecretWithRetry(ctx context.Context, client *ent.Client, key string) (*ent.SecuritySecret, error) {
	var lastErr error
	for attempt := 0; attempt <= securitySecretReadRetryMax; attempt++ {
		stored, err := client.SecuritySecret.Query().Where(securitysecret.KeyEQ(key)).Only(ctx)
		if err == nil {
			return stored, nil
		}
		if !isSecretNotFoundError(err) {
			return nil, err
		}
		lastErr = err
		if attempt == securitySecretReadRetryMax {
			break
		}

		timer := time.NewTimer(securitySecretReadRetryWait)
		select {
		case <-ctx.Done():
			timer.Stop()
			return nil, ctx.Err()
		case <-timer.C:
		}
	}
	return nil, lastErr
}

func isSecretNotFoundError(err error) bool {
	if err == nil {
		return false
	}
	return ent.IsNotFound(err) || isSQLNoRowsError(err)
}

func isSQLNoRowsError(err error) bool {
	if err == nil {
		return false
	}
	return errors.Is(err, sql.ErrNoRows) || strings.Contains(err.Error(), "no rows in result set")
}

func generateHexSecret(byteLength int) (string, error) {
	if byteLength <= 0 {
		byteLength = 32
	}
	buf := make([]byte, byteLength)
	if _, err := readRandomBytes(buf); err != nil {
		return "", fmt.Errorf("generate random secret: %w", err)
	}
	return hex.EncodeToString(buf), nil
}
