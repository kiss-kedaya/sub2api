package service

import (
	"fmt"

	"github.com/Wei-Shaw/sub2api/internal/pkg/secretcrypt"
)

// SensitiveSettingKeys lists the settings rows that hold credentials and must
// be encrypted at rest. String values are encrypted directly; JSON-valued rows
// (S3 / image storage) are encrypted as one document because every field except
// the endpoint is credential material and nothing queries them in SQL.
//
// When a new credential-bearing setting is added, add it here. The repository
// layer consults this registry on every read and write, so the list is the
// single source of truth for "this value must never hit disk in plaintext".
var SensitiveSettingKeys = []string{
	// Mail
	SettingKeySMTPPassword,
	// Bot / captcha defenses
	SettingKeyTurnstileSecretKey,
	SettingKeyTencentCaptchaAppSecretKey,
	SettingKeyTencentCaptchaCloudSecretID,
	SettingKeyTencentCaptchaCloudSecretKey,
	SettingKeyAliyunCaptchaAccessKeySecret,
	// Third-party sign-in providers
	SettingKeyLinuxDoConnectClientSecret,
	SettingKeyDingTalkConnectClientSecret,
	SettingKeyWeChatConnectAppSecret,
	SettingKeyWeChatConnectOpenAppSecret,
	SettingKeyWeChatConnectMPAppSecret,
	SettingKeyWeChatConnectMobileAppSecret,
	SettingKeyOIDCConnectClientSecret,
	SettingKeyGitHubOAuthClientSecret,
	SettingKeyGoogleOAuthClientSecret,
	// External system integration
	SettingKeyAdminAPIKey,
	// JSON documents that embed access keys. These were already encrypted field
	// by field for their S3 secret; encrypting the whole document adds defense in
	// depth and covers every field at once.
	"backup_s3_config",
	"image_storage_config",
}

var sensitiveSettingKeySet = func() map[string]struct{} {
	set := make(map[string]struct{}, len(SensitiveSettingKeys))
	for _, key := range SensitiveSettingKeys {
		set[key] = struct{}{}
	}
	return set
}()

// IsSensitiveSettingKey reports whether the setting row must be encrypted at
// rest.
func IsSensitiveSettingKey(key string) bool {
	_, ok := sensitiveSettingKeySet[key]
	return ok
}

// EncryptSettingValue encrypts a sensitive setting value for storage. Values of
// non-sensitive keys pass through untouched, and so does an already-encrypted
// value (idempotent merge path).
func EncryptSettingValue(vault SecretVault, key, value string) (string, error) {
	if !IsSensitiveSettingKey(key) || value == "" {
		return value, nil
	}
	if secretcrypt.IsEncrypted(value) {
		return value, nil
	}
	if !SecretVaultAvailable(vault) {
		// No installation key: keep the legacy plaintext row. Reads of encrypted
		// rows still fail closed, and the startup bootstrap refuses to run when
		// encrypted rows exist without their key.
		return value, nil
	}
	encrypted, err := vault.EncryptString(value)
	if err != nil {
		return "", fmt.Errorf("encrypt setting %q: %w", key, err)
	}
	return encrypted, nil
}

// DecryptSettingValue decrypts a sensitive setting value. needsRewrite reports a
// legacy plaintext row so the caller can lazily re-encrypt it.
func DecryptSettingValue(vault SecretVault, key, stored string) (string, bool, error) {
	if !IsSensitiveSettingKey(key) || stored == "" {
		return stored, false, nil
	}
	if !secretcrypt.IsEncrypted(stored) {
		// Pre-encryption plaintext, still readable until the next write.
		return stored, true, nil
	}
	plaintext, err := vault.DecryptString(stored)
	if err != nil {
		return "", false, fmt.Errorf("setting %q cannot be decrypted: %w", key, err)
	}
	return plaintext, false, nil
}

// SettingValueIsEncrypted reports whether a sensitive setting row carries the
// at-rest ciphertext prefix.
func SettingValueIsEncrypted(key, value string) bool {
	return IsSensitiveSettingKey(key) && secretcrypt.IsEncrypted(value)
}
