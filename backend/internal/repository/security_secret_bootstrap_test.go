package repository

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/hkdf"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"

	dbent "github.com/Wei-Shaw/sub2api/ent"
	"github.com/Wei-Shaw/sub2api/ent/enttest"
	"github.com/Wei-Shaw/sub2api/ent/securitysecret"
	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/payment"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"

	"entgo.io/ent/dialect"
	entsql "entgo.io/ent/dialect/sql"
	_ "modernc.org/sqlite"
)

func newSecuritySecretTestClient(t *testing.T) *dbent.Client {
	t.Helper()
	name := strings.ReplaceAll(t.Name(), "/", "_")
	dsn := fmt.Sprintf("file:%s?mode=memory&cache=shared&_fk=1", name)

	db, err := sql.Open("sqlite", dsn)
	require.NoError(t, err)
	db.SetMaxOpenConns(1)
	t.Cleanup(func() { _ = db.Close() })

	_, err = db.Exec("PRAGMA foreign_keys = ON")
	require.NoError(t, err)

	drv := entsql.OpenDB(dialect.SQLite, db)
	client := enttest.NewClient(t, enttest.WithOptions(dbent.Driver(drv)))
	// These durable encrypted fields are SQL-migration-only, outside Ent's
	// generated schema. Mirror their storage for real bootstrap regression tests.
	_, err = db.Exec(`ALTER TABLE users ADD COLUMN bark_device_key_encrypted TEXT NOT NULL DEFAULT ''`)
	require.NoError(t, err)
	_, err = db.Exec(`CREATE TABLE sub2api_plugin_installations (id INTEGER PRIMARY KEY, config_encrypted TEXT NOT NULL DEFAULT '')`)
	require.NoError(t, err)
	t.Cleanup(func() { _ = client.Close() })
	return client
}

func legacyInstallationCiphertextFixtures() map[string]func(*testing.T, *dbent.Client, string, string) {
	settingFixture := func(name string, content func(string) any) func(*testing.T, *dbent.Client, string, string) {
		return func(t *testing.T, client *dbent.Client, _, encrypted string) {
			raw, err := json.Marshal(content(encrypted))
			require.NoError(t, err)
			_, err = client.Setting.Create().SetKey(name).SetValue(string(raw)).Save(t.Context())
			require.NoError(t, err)
		}
	}
	return map[string]func(*testing.T, *dbent.Client, string, string){
		"totp": func(t *testing.T, client *dbent.Client, _, encrypted string) {
			_, err := client.User.Create().SetEmail("fixture@example.test").SetPasswordHash("fixture-hash").
				SetTotpSecretEncrypted(encrypted).SetTotpEnabled(false).Save(t.Context())
			require.NoError(t, err)
		},
		"bark": func(t *testing.T, client *dbent.Client, _, encrypted string) {
			u, err := client.User.Create().SetEmail("fixture@example.test").SetPasswordHash("fixture-hash").Save(t.Context())
			require.NoError(t, err)
			_, err = client.ExecContext(t.Context(), `UPDATE users SET bark_device_key_encrypted = $1 WHERE id = $2`, encrypted, u.ID)
			require.NoError(t, err)
		},
		"channel_monitor": func(t *testing.T, client *dbent.Client, _, encrypted string) {
			_, err := client.ChannelMonitor.Create().SetName("fixture").SetProvider("openai").SetCreatedBy(1).
				SetEndpoint("https://example.test").SetPrimaryModel("fixture-model").SetEnabled(false).
				SetIntervalSeconds(60).SetAPIKeyEncrypted(encrypted).Save(t.Context())
			require.NoError(t, err)
		},
		"plugin": func(t *testing.T, client *dbent.Client, _, encrypted string) {
			_, err := client.ExecContext(t.Context(), `INSERT INTO sub2api_plugin_installations (id, config_encrypted) VALUES (1, $1)`, encrypted)
			require.NoError(t, err)
		},
		"ollama_session": func(t *testing.T, client *dbent.Client, _, encrypted string) {
			_, err := client.Account.Create().SetName("fixture").SetPlatform("openai").SetType("api_key").
				SetExtra(map[string]any{service.OllamaCloudUsageSessionExtraKey: encrypted}).Save(t.Context())
			require.NoError(t, err)
		},
		"backup_s3": settingFixture("backup_s3_config", func(value string) any {
			return map[string]string{"secret_access_key": value}
		}),
		"image_storage_s3": settingFixture("image_storage_config", func(value string) any {
			return map[string]string{"secret_access_key": value}
		}),
		"audit_node_token": settingFixture("prompt_audit_config", func(value string) any {
			return map[string]any{"endpoints": []map[string]string{{"token_ciphertext": value}}}
		}),
		"legacy_payment_config": func(t *testing.T, client *dbent.Client, keyHex, _ string) {
			key, err := hex.DecodeString(keyHex)
			require.NoError(t, err)
			//nolint:staticcheck // Exercise the real historical payment format.
			encrypted, err := payment.Encrypt(`{"key":"fixture-only"}`, key)
			require.NoError(t, err)
			_, err = client.PaymentProviderInstance.Create().SetProviderKey("fixture").SetConfig(encrypted).Save(t.Context())
			require.NoError(t, err)
		},
		"wallet": func(t *testing.T, client *dbent.Client, keyHex, _ string) {
			key, err := hex.DecodeString(keyHex)
			require.NoError(t, err)
			derived, err := hkdf.Key(sha256.New, key, nil, "sub2api/crypto-wallet/seed-encryption/v2", 32)
			require.NoError(t, err)
			block, err := aes.NewCipher(derived)
			require.NoError(t, err)
			aead, err := cipher.NewGCMWithRandomNonce(block)
			require.NoError(t, err)
			// Public BIP39 test vectors, identical to wallet package fixtures.
			plain, err := json.Marshal(map[string]string{
				"deposit": "abandon abandon abandon abandon abandon abandon abandon abandon abandon abandon abandon about",
				"hot":     "legal winner thank year wave sausage worth useful legal winner thank yellow",
			})
			require.NoError(t, err)
			const prefix = "sub2api-wallet:v2:"
			raw := prefix + base64.StdEncoding.EncodeToString(aead.Seal(nil, nil, plain, []byte(prefix)))
			_, err = client.Setting.Create().SetKey("crypto.signer.vault").SetValue(raw).Save(t.Context())
			require.NoError(t, err)
		},
	}
}

func TestInstallationKeyFirstRegistrationAuthenticatesEveryDurableCiphertext(t *testing.T) {
	const original = "11223344556677889900aabbccddeeff00112233445566778899aabbccddeeff"
	for name, fixture := range legacyInstallationCiphertextFixtures() {
		t.Run(name, func(t *testing.T) {
			client := newSecuritySecretTestClient(t)
			encryptor, err := NewAESEncryptor(&config.Config{Totp: config.TotpConfig{EncryptionKey: original}})
			require.NoError(t, err)
			encrypted, err := encryptor.Encrypt("fixture-only-durable-secret")
			require.NoError(t, err)
			fixture(t, client, original, encrypted)

			// Temporary defaults cannot silently replace any legacy key.
			withoutKey := &config.Config{Totp: config.TotpConfig{EncryptionKey: strings.Repeat("ab", 32)}}
			err = ensureBootstrapSecrets(t.Context(), client, withoutKey)
			require.ErrorContains(t, err, "requires the original TOTP_ENCRYPTION_KEY")
			exists, queryErr := client.SecuritySecret.Query().Where(securitysecret.KeyEQ(securitySecretKeyTOTP)).Exist(t.Context())
			require.NoError(t, queryErr)
			require.False(t, exists)

			// A syntactically valid but incorrect key must never be registered.
			wrong := &config.Config{Totp: config.TotpConfig{EncryptionKey: strings.Repeat("ab", 32), EncryptionKeyConfigured: true}}
			err = ensureBootstrapSecrets(t.Context(), client, wrong)
			require.ErrorContains(t, err, "cannot decrypt existing")
			require.NotContains(t, err.Error(), encrypted)
			require.NotContains(t, err.Error(), wrong.Totp.EncryptionKey)
			exists, queryErr = client.SecuritySecret.Query().Where(securitysecret.KeyEQ(securitySecretKeyTOTP)).Exist(t.Context())
			require.NoError(t, queryErr)
			require.False(t, exists)

			correct := &config.Config{Totp: config.TotpConfig{EncryptionKey: original, EncryptionKeyConfigured: true}}
			require.NoError(t, ensureBootstrapSecrets(t.Context(), client, correct))
			require.True(t, correct.Totp.EncryptionKeyPersistent)
			restarted := &config.Config{}
			require.NoError(t, ensureBootstrapSecrets(t.Context(), client, restarted))
			require.True(t, restarted.Totp.EncryptionKey == original, "restart must preserve the legacy key")
		})
	}
}

func TestInstallationKeyChecksAllCiphertextsBeforePersisting(t *testing.T) {
	client := newSecuritySecretTestClient(t)
	key := strings.Repeat("ab", 32)
	encryptor, err := NewAESEncryptor(&config.Config{Totp: config.TotpConfig{EncryptionKey: key}})
	require.NoError(t, err)
	good, err := encryptor.Encrypt("fixture-only")
	require.NoError(t, err)
	fixtures := legacyInstallationCiphertextFixtures()
	fixtures["totp"](t, client, key, good)
	fixtures["plugin"](t, client, key, "corrupted-fixture-ciphertext")
	cfg := &config.Config{Totp: config.TotpConfig{EncryptionKey: key, EncryptionKeyConfigured: true}}
	require.ErrorContains(t, ensureBootstrapSecrets(t.Context(), client, cfg), "cannot decrypt existing plugin")
	exists, err := client.SecuritySecret.Query().Where(securitysecret.KeyEQ(securitySecretKeyTOTP)).Exist(t.Context())
	require.NoError(t, err)
	require.False(t, exists)
}

func TestInstallationKeyAcceptsLegacyPlaintextS3Compatibility(t *testing.T) {
	for _, configured := range []bool{false, true} {
		t.Run(fmt.Sprintf("configured_%v", configured), func(t *testing.T) {
			client := newSecuritySecretTestClient(t)
			for _, name := range []string{"backup_s3_config", "image_storage_config"} {
				_, err := client.Setting.Create().SetKey(name).SetValue(`{"secret_access_key":"legacy-plaintext-credential"}`).Save(t.Context())
				require.NoError(t, err)
			}
			cfg := &config.Config{Totp: config.TotpConfig{EncryptionKey: strings.Repeat("ab", 32), EncryptionKeyConfigured: configured}}
			require.NoError(t, ensureBootstrapSecrets(t.Context(), client, cfg))
			require.True(t, cfg.Totp.EncryptionKeyPersistent)
			restarted := &config.Config{}
			require.NoError(t, ensureBootstrapSecrets(t.Context(), client, restarted))
			require.True(t, cfg.Totp.EncryptionKey == restarted.Totp.EncryptionKey, "plaintext legacy settings must permit a stable automatic installation key")
		})
	}
}

func TestInstallationKeyConcurrentFirstBootConverges(t *testing.T) {
	client := newSecuritySecretTestClient(t)
	const instances = 6
	configs := make([]*config.Config, instances)
	errors := make([]error, instances)
	var group sync.WaitGroup
	for i := range instances {
		configs[i] = &config.Config{}
		group.Add(1)
		go func(index int) {
			defer group.Done()
			errors[index] = ensureBootstrapSecrets(t.Context(), client, configs[index])
		}(i)
	}
	group.Wait()
	for i := range instances {
		require.NoError(t, errors[i])
		require.True(t, configs[i].Totp.EncryptionKey == configs[0].Totp.EncryptionKey, "concurrent instances must share one persisted key")
	}
}

func TestEnsureBootstrapSecretsNilInputs(t *testing.T) {
	err := ensureBootstrapSecrets(context.Background(), nil, &config.Config{})
	require.Error(t, err)
	require.Contains(t, err.Error(), "nil ent client")

	client := newSecuritySecretTestClient(t)
	err = ensureBootstrapSecrets(context.Background(), client, nil)
	require.Error(t, err)
	require.Contains(t, err.Error(), "nil config")
}

func TestEnsureBootstrapSecretsGenerateAndPersistJWTSecret(t *testing.T) {
	client := newSecuritySecretTestClient(t)
	cfg := &config.Config{}

	err := ensureBootstrapSecrets(context.Background(), client, cfg)
	require.NoError(t, err)
	require.NotEmpty(t, cfg.JWT.Secret)
	require.GreaterOrEqual(t, len([]byte(cfg.JWT.Secret)), 32)

	stored, err := client.SecuritySecret.Query().Where(securitysecret.KeyEQ(securitySecretKeyJWT)).Only(context.Background())
	require.NoError(t, err)
	require.Equal(t, cfg.JWT.Secret, stored.Value)
}

func TestEnsureBootstrapSecretsLoadExistingJWTSecret(t *testing.T) {
	client := newSecuritySecretTestClient(t)
	_, err := client.SecuritySecret.Create().SetKey(securitySecretKeyJWT).SetValue("existing-jwt-secret-32bytes-long!!!!").Save(context.Background())
	require.NoError(t, err)

	cfg := &config.Config{}
	err = ensureBootstrapSecrets(context.Background(), client, cfg)
	require.NoError(t, err)
	require.Equal(t, "existing-jwt-secret-32bytes-long!!!!", cfg.JWT.Secret)
}

func TestEnsureBootstrapSecretsReadOnlyTestModeNeverCreatesJWTSecret(t *testing.T) {
	for _, mode := range []string{"1", "t", "malformed"} {
		t.Run(mode, func(t *testing.T) {
			t.Setenv("CRYPTO_ADMIN_TEST_MODE", mode)
			client := newSecuritySecretTestClient(t)
			cfg := &config.Config{
				JWT:  config.JWTConfig{Secret: "crypto-test-jwt-secret-never-reused-0001"},
				Totp: config.TotpConfig{EncryptionKey: strings.Repeat("a1", 32), EncryptionKeyConfigured: true},
			}
			err := ensureBootstrapSecrets(context.Background(), client, cfg)
			require.NoError(t, err)
			require.Equal(t, "crypto-test-jwt-secret-never-reused-0001", cfg.JWT.Secret)
			count, countErr := client.SecuritySecret.Query().Count(context.Background())
			require.NoError(t, countErr)
			require.Zero(t, count)

			missing := &config.Config{}
			require.ErrorContains(t, ensureBootstrapSecrets(context.Background(), client, missing), "separate JWT_SECRET")
		})
	}
}

func TestTOTPInstallationKeyPersistsAcrossRestartsAndInstances(t *testing.T) {
	client := newSecuritySecretTestClient(t)
	ctx := t.Context()
	first := &config.Config{Totp: config.TotpConfig{EncryptionKey: strings.Repeat("ab", 32)}}
	require.NoError(t, ensureBootstrapSecrets(ctx, client, first))
	require.True(t, first.Totp.EncryptionKeyPersistent)
	require.False(t, first.Totp.EncryptionKeyConfigured)
	require.NotEqual(t, strings.Repeat("ab", 32), first.Totp.EncryptionKey, "process-local default must be discarded")
	writer, err := NewAESEncryptor(first)
	require.NoError(t, err)
	ciphertext, err := writer.Encrypt("existing-authenticator-secret")
	require.NoError(t, err)

	second := &config.Config{Totp: config.TotpConfig{EncryptionKey: strings.Repeat("cd", 32)}}
	require.NoError(t, ensureBootstrapSecrets(ctx, client, second))
	require.Equal(t, first.Totp.EncryptionKey, second.Totp.EncryptionKey)
	reader, err := NewAESEncryptor(second)
	require.NoError(t, err)
	plaintext, err := reader.Decrypt(ciphertext)
	require.NoError(t, err)
	require.Equal(t, "existing-authenticator-secret", plaintext)
}

func TestTOTPInstallationKeyAdoptsLegacyKeyAndRejectsRotation(t *testing.T) {
	client := newSecuritySecretTestClient(t)
	legacy := strings.Repeat("ef", 32)
	cfg := &config.Config{Totp: config.TotpConfig{EncryptionKey: legacy, EncryptionKeyConfigured: true}}
	require.NoError(t, ensureBootstrapSecrets(t.Context(), client, cfg))
	withoutEnv := &config.Config{}
	require.NoError(t, ensureBootstrapSecrets(t.Context(), client, withoutEnv))
	require.Equal(t, legacy, withoutEnv.Totp.EncryptionKey)

	conflicting := &config.Config{Totp: config.TotpConfig{EncryptionKey: strings.Repeat("ab", 32), EncryptionKeyConfigured: true}}
	require.ErrorContains(t, ensureBootstrapSecrets(t.Context(), client, conflicting), "conflicts with the persisted")
	stored, err := client.SecuritySecret.Query().Where(securitysecret.KeyEQ(securitySecretKeyTOTP)).Only(t.Context())
	require.NoError(t, err)
	require.Equal(t, legacy, stored.Value)
}

func TestTOTPInstallationKeyRejectsMalformedStoredKey(t *testing.T) {
	for _, value := range []string{strings.Repeat("x", 64), strings.Repeat("a", 32), strings.Repeat("ab", 33)} {
		t.Run(value, func(t *testing.T) {
			client := newSecuritySecretTestClient(t)
			_, err := client.SecuritySecret.Create().SetKey(securitySecretKeyTOTP).SetValue(value).Save(t.Context())
			require.NoError(t, err)
			require.ErrorContains(t, ensureBootstrapSecrets(t.Context(), client, &config.Config{}), "stored TOTP installation key")
		})
	}
}

func TestTOTPInstallationKeyNeverReplacesExistingWalletCiphertext(t *testing.T) {
	client := newSecuritySecretTestClient(t)
	_, err := client.Setting.Create().SetKey("crypto.signer.vault").SetValue("opaque-existing-encrypted-vault").Save(t.Context())
	require.NoError(t, err)
	require.ErrorContains(t, ensureBootstrapSecrets(t.Context(), client, &config.Config{}), "requires the original")
	exists, err := client.SecuritySecret.Query().Where(securitysecret.KeyEQ(securitySecretKeyTOTP)).Exist(t.Context())
	require.NoError(t, err)
	require.False(t, exists)
}

func TestReadOnlyTOTPInstallationKeyLoadsWithoutWriting(t *testing.T) {
	t.Setenv("CRYPTO_ADMIN_TEST_MODE", "true")
	client := newSecuritySecretTestClient(t)
	cfg := &config.Config{JWT: config.JWTConfig{Secret: "isolated-review-jwt-secret-at-least-32-bytes"}}
	require.ErrorContains(t, ensureBootstrapSecrets(t.Context(), client, cfg), "existing persistent installation key")
	key := strings.Repeat("ba", 32)
	_, err := client.SecuritySecret.Create().SetKey(securitySecretKeyTOTP).SetValue(key).Save(t.Context())
	require.NoError(t, err)
	require.NoError(t, ensureBootstrapSecrets(t.Context(), client, cfg))
	require.Equal(t, key, cfg.Totp.EncryptionKey)
	require.True(t, cfg.Totp.EncryptionKeyPersistent)
	count, err := client.SecuritySecret.Query().Count(t.Context())
	require.NoError(t, err)
	require.Equal(t, 1, count, "read-only bootstrap must not create a JWT or TOTP secret")
}

func TestEnsureBootstrapSecretsRejectInvalidStoredSecret(t *testing.T) {
	client := newSecuritySecretTestClient(t)
	_, err := client.SecuritySecret.Create().SetKey(securitySecretKeyJWT).SetValue("too-short").Save(context.Background())
	require.NoError(t, err)

	cfg := &config.Config{}
	err = ensureBootstrapSecrets(context.Background(), client, cfg)
	require.Error(t, err)
	require.Contains(t, err.Error(), "at least 32 bytes")
}

func TestEnsureBootstrapSecretsPersistConfiguredJWTSecret(t *testing.T) {
	client := newSecuritySecretTestClient(t)
	cfg := &config.Config{
		JWT: config.JWTConfig{Secret: "configured-jwt-secret-32bytes-long!!"},
	}

	err := ensureBootstrapSecrets(context.Background(), client, cfg)
	require.NoError(t, err)

	stored, err := client.SecuritySecret.Query().Where(securitysecret.KeyEQ(securitySecretKeyJWT)).Only(context.Background())
	require.NoError(t, err)
	require.Equal(t, "configured-jwt-secret-32bytes-long!!", stored.Value)
}

func TestEnsureBootstrapSecretsConfiguredSecretTooShort(t *testing.T) {
	client := newSecuritySecretTestClient(t)
	cfg := &config.Config{JWT: config.JWTConfig{Secret: "short"}}

	err := ensureBootstrapSecrets(context.Background(), client, cfg)
	require.Error(t, err)
	require.Contains(t, err.Error(), "at least 32 bytes")
}

func TestEnsureBootstrapSecretsConfiguredSecretDuplicateIgnored(t *testing.T) {
	client := newSecuritySecretTestClient(t)
	_, err := client.SecuritySecret.Create().
		SetKey(securitySecretKeyJWT).
		SetValue("existing-jwt-secret-32bytes-long!!!!").
		Save(context.Background())
	require.NoError(t, err)

	cfg := &config.Config{JWT: config.JWTConfig{Secret: "another-configured-jwt-secret-32!!!!"}}
	err = ensureBootstrapSecrets(context.Background(), client, cfg)
	require.NoError(t, err)

	stored, err := client.SecuritySecret.Query().Where(securitysecret.KeyEQ(securitySecretKeyJWT)).Only(context.Background())
	require.NoError(t, err)
	require.Equal(t, "existing-jwt-secret-32bytes-long!!!!", stored.Value)
	require.Equal(t, "existing-jwt-secret-32bytes-long!!!!", cfg.JWT.Secret)
}

func TestGetOrCreateGeneratedSecuritySecretTrimmedExistingValue(t *testing.T) {
	client := newSecuritySecretTestClient(t)
	_, err := client.SecuritySecret.Create().
		SetKey("trimmed_key").
		SetValue("  existing-trimmed-secret-32bytes-long!!  ").
		Save(context.Background())
	require.NoError(t, err)

	value, created, err := getOrCreateGeneratedSecuritySecret(context.Background(), client, "trimmed_key", 32)
	require.NoError(t, err)
	require.False(t, created)
	require.Equal(t, "existing-trimmed-secret-32bytes-long!!", value)
}

func TestGetOrCreateGeneratedSecuritySecretQueryError(t *testing.T) {
	client := newSecuritySecretTestClient(t)
	require.NoError(t, client.Close())

	_, _, err := getOrCreateGeneratedSecuritySecret(context.Background(), client, "closed_client_key", 32)
	require.Error(t, err)
}

func TestGetOrCreateGeneratedSecuritySecretCreateValidationError(t *testing.T) {
	client := newSecuritySecretTestClient(t)
	tooLongKey := strings.Repeat("k", 101)

	_, _, err := getOrCreateGeneratedSecuritySecret(context.Background(), client, tooLongKey, 32)
	require.Error(t, err)
}

func TestGetOrCreateGeneratedSecuritySecretConcurrentCreation(t *testing.T) {
	client := newSecuritySecretTestClient(t)
	const goroutines = 8
	key := "concurrent_bootstrap_key"

	values := make([]string, goroutines)
	createdFlags := make([]bool, goroutines)
	errs := make([]error, goroutines)

	var wg sync.WaitGroup
	for i := 0; i < goroutines; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			values[idx], createdFlags[idx], errs[idx] = getOrCreateGeneratedSecuritySecret(context.Background(), client, key, 32)
		}(i)
	}
	wg.Wait()

	for i := range errs {
		require.NoError(t, errs[i])
		require.NotEmpty(t, values[i])
	}
	for i := 1; i < len(values); i++ {
		require.Equal(t, values[0], values[i])
	}

	createdCount := 0
	for _, created := range createdFlags {
		if created {
			createdCount++
		}
	}
	require.GreaterOrEqual(t, createdCount, 1)
	require.LessOrEqual(t, createdCount, 1)

	count, err := client.SecuritySecret.Query().Where(securitysecret.KeyEQ(key)).Count(context.Background())
	require.NoError(t, err)
	require.Equal(t, 1, count)
}

func TestGetOrCreateGeneratedSecuritySecretGenerateError(t *testing.T) {
	client := newSecuritySecretTestClient(t)
	originalRead := readRandomBytes
	readRandomBytes = func([]byte) (int, error) {
		return 0, errors.New("boom")
	}
	t.Cleanup(func() {
		readRandomBytes = originalRead
	})

	_, _, err := getOrCreateGeneratedSecuritySecret(context.Background(), client, "gen_error_key", 32)
	require.Error(t, err)
	require.Contains(t, err.Error(), "boom")
}

func TestCreateSecuritySecretIfAbsent(t *testing.T) {
	client := newSecuritySecretTestClient(t)

	_, err := createSecuritySecretIfAbsent(context.Background(), client, "abc", "short")
	require.Error(t, err)
	require.Contains(t, err.Error(), "at least 32 bytes")

	stored, err := createSecuritySecretIfAbsent(context.Background(), client, "abc", "valid-jwt-secret-value-32bytes-long")
	require.NoError(t, err)
	require.Equal(t, "valid-jwt-secret-value-32bytes-long", stored)

	stored, err = createSecuritySecretIfAbsent(context.Background(), client, "abc", "another-valid-secret-value-32bytes")
	require.NoError(t, err)
	require.Equal(t, "valid-jwt-secret-value-32bytes-long", stored)

	count, err := client.SecuritySecret.Query().Where(securitysecret.KeyEQ("abc")).Count(context.Background())
	require.NoError(t, err)
	require.Equal(t, 1, count)
}

func TestCreateSecuritySecretIfAbsentValidationError(t *testing.T) {
	client := newSecuritySecretTestClient(t)
	_, err := createSecuritySecretIfAbsent(
		context.Background(),
		client,
		strings.Repeat("k", 101),
		"valid-jwt-secret-value-32bytes-long",
	)
	require.Error(t, err)
}

func TestCreateSecuritySecretIfAbsentExecError(t *testing.T) {
	client := newSecuritySecretTestClient(t)
	require.NoError(t, client.Close())

	_, err := createSecuritySecretIfAbsent(context.Background(), client, "closed-client-key", "valid-jwt-secret-value-32bytes-long")
	require.Error(t, err)
}

func TestQuerySecuritySecretWithRetrySuccess(t *testing.T) {
	client := newSecuritySecretTestClient(t)
	created, err := client.SecuritySecret.Create().
		SetKey("retry_success_key").
		SetValue("retry-success-jwt-secret-value-32!!").
		Save(context.Background())
	require.NoError(t, err)

	got, err := querySecuritySecretWithRetry(context.Background(), client, "retry_success_key")
	require.NoError(t, err)
	require.Equal(t, created.ID, got.ID)
	require.Equal(t, "retry-success-jwt-secret-value-32!!", got.Value)
}

func TestQuerySecuritySecretWithRetryExhausted(t *testing.T) {
	client := newSecuritySecretTestClient(t)

	_, err := querySecuritySecretWithRetry(context.Background(), client, "retry_missing_key")
	require.Error(t, err)
	require.True(t, isSecretNotFoundError(err))
}

func TestQuerySecuritySecretWithRetryContextCanceled(t *testing.T) {
	client := newSecuritySecretTestClient(t)
	ctx, cancel := context.WithTimeout(context.Background(), securitySecretReadRetryWait/2)
	defer cancel()

	_, err := querySecuritySecretWithRetry(ctx, client, "retry_ctx_cancel_key")
	require.Error(t, err)
	require.ErrorIs(t, err, context.DeadlineExceeded)
}

func TestQuerySecuritySecretWithRetryNonNotFoundError(t *testing.T) {
	client := newSecuritySecretTestClient(t)
	require.NoError(t, client.Close())

	_, err := querySecuritySecretWithRetry(context.Background(), client, "retry_closed_client_key")
	require.Error(t, err)
	require.False(t, isSecretNotFoundError(err))
}

func TestSecretNotFoundHelpers(t *testing.T) {
	require.False(t, isSecretNotFoundError(nil))
	require.False(t, isSQLNoRowsError(nil))

	require.True(t, isSQLNoRowsError(sql.ErrNoRows))
	require.True(t, isSQLNoRowsError(fmt.Errorf("wrapped: %w", sql.ErrNoRows)))
	require.True(t, isSQLNoRowsError(errors.New("sql: no rows in result set")))

	require.True(t, isSecretNotFoundError(sql.ErrNoRows))
	require.True(t, isSecretNotFoundError(errors.New("sql: no rows in result set")))
	require.False(t, isSecretNotFoundError(errors.New("some other error")))
}

func TestGenerateHexSecretReadError(t *testing.T) {
	originalRead := readRandomBytes
	readRandomBytes = func([]byte) (int, error) {
		return 0, errors.New("read random failed")
	}
	t.Cleanup(func() {
		readRandomBytes = originalRead
	})

	_, err := generateHexSecret(32)
	require.Error(t, err)
	require.Contains(t, err.Error(), "read random failed")
}

func TestGenerateHexSecretLengths(t *testing.T) {
	v1, err := generateHexSecret(0)
	require.NoError(t, err)
	require.Len(t, v1, 64)
	_, err = hex.DecodeString(v1)
	require.NoError(t, err)

	v2, err := generateHexSecret(16)
	require.NoError(t, err)
	require.Len(t, v2, 32)
	_, err = hex.DecodeString(v2)
	require.NoError(t, err)

	require.NotEqual(t, v1, v2)
}
