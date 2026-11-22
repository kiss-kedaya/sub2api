package repository

import (
	"encoding/json"
	"strings"
	"testing"

	dbent "github.com/Wei-Shaw/sub2api/ent"
	"github.com/Wei-Shaw/sub2api/internal/pkg/atrest"
	"github.com/stretchr/testify/require"
)

const repositoryAtRestKeyHex = "aabbccddeeff00112233445566778899aabbccddeeff00112233445566778899"

func configureRepositoryAtRest(t *testing.T) {
	t.Helper()
	configureRepositoryAtRestSealing(t, true)
}

// configureRepositoryAtRestSealing 注入密钥并按需打开/关闭 sealing 总开关。
// sealing=false 对应「配置了密钥但 SUB2API_ATREST_ENABLED 未开」的只读兼容态。
func configureRepositoryAtRestSealing(t *testing.T, sealing bool) {
	t.Helper()
	cipher, err := atrest.New(repositoryAtRestKeyHex)
	require.NoError(t, err)
	atrest.Configure(cipher)
	atrest.SetSealingEnabled(sealing)
	t.Cleanup(atrest.Reset)
}

func TestAccountCredentialsFromEntity_PrefersPlaintextOverCipher(t *testing.T) {
	configureRepositoryAtRest(t)
	authoritative := map[string]any{"api_key": "sk-cipher", "base_url": "https://cipher.example.com"}
	plain, err := json.Marshal(authoritative)
	require.NoError(t, err)
	ciphertext, err := atrest.EncryptString(string(plain))
	require.NoError(t, err)

	// 双写行：两列都是完整副本，明文优先（这是 2026-11-22 事故的读取修复）。
	entity := &dbent.Account{
		ID:                1,
		Credentials:       map[string]any{"api_key": "sk-plain", "base_url": "https://plain.example.com"},
		CredentialsCipher: &ciphertext,
	}
	credentials, err := accountCredentialsFromEntity(entity)
	require.NoError(t, err)
	require.Equal(t, "sk-plain", credentials["api_key"])
	require.Equal(t, "https://plain.example.com", credentials["base_url"])
}

func TestAccountCredentialsFromEntity_UsesCipherForSealedRows(t *testing.T) {
	configureRepositoryAtRest(t)
	authoritative := map[string]any{"api_key": "sk-cipher", "base_url": "https://cipher.example.com"}
	plain, err := json.Marshal(authoritative)
	require.NoError(t, err)
	ciphertext, err := atrest.EncryptString(string(plain))
	require.NoError(t, err)

	sealed := SealCredentials(map[string]any{"api_key": "sk-cipher", "base_url": "https://cipher.example.com"})
	entity := &dbent.Account{ID: 5, Credentials: sealed, CredentialsCipher: &ciphertext}
	credentials, err := accountCredentialsFromEntity(entity)
	require.NoError(t, err)
	require.Equal(t, "sk-cipher", credentials["api_key"])
}

func TestAccountCredentialsFromEntity_FallsBackToPlaintext(t *testing.T) {
	configureRepositoryAtRest(t)
	entity := &dbent.Account{ID: 2, Credentials: map[string]any{"api_key": "sk-legacy"}}
	credentials, err := accountCredentialsFromEntity(entity)
	require.NoError(t, err)
	require.Equal(t, "sk-legacy", credentials["api_key"])
}

// 回归 2026-11-22：双写行在环境密钥被删除后仍必须能读到明文 api_key，
// 否则网关全站 502。
func TestAccountCredentialsFromEntity_DualWriteRowSurvivesMissingKey(t *testing.T) {
	atrest.Reset()
	t.Cleanup(atrest.Reset)
	ciphertext := "enc:v2:AAAA"
	entity := &dbent.Account{
		ID:                6,
		Credentials:       map[string]any{"api_key": "sk-plain"},
		CredentialsCipher: &ciphertext,
	}
	credentials, err := accountCredentialsFromEntity(entity)
	require.NoError(t, err)
	require.Equal(t, "sk-plain", credentials["api_key"], "plaintext must win over undecryptable cipher")
}

func TestAccountCredentialsFromEntity_FailsClosedWhenKeyMissing(t *testing.T) {
	atrest.Reset()
	t.Cleanup(atrest.Reset)
	ciphertext := "enc:v2:AAAA"
	// 封存行：明文列只剩结构键，敏感值只在密文里 → 无密钥必须 fail-closed。
	entity := &dbent.Account{
		ID:                3,
		Credentials:       map[string]any{"_sealed": true, "base_url": "https://api.example.com"},
		CredentialsCipher: &ciphertext,
	}
	_, err := accountCredentialsFromEntity(entity)
	require.Error(t, err)
	require.ErrorIs(t, err, atrest.ErrNoKey)
}

func TestAccountCredentialsFromEntity_DecryptsHistoricalCipherWhenSealingDisabled(t *testing.T) {
	configureRepositoryAtRestSealing(t, false)
	ciphertext, err := atrest.Current().EncryptString(`{"api_key":"sk-historical"}`)
	require.NoError(t, err)
	sealed := SealCredentials(map[string]any{"api_key": "sk-historical"})
	entity := &dbent.Account{ID: 7, Credentials: sealed, CredentialsCipher: &ciphertext}
	credentials, err := accountCredentialsFromEntity(entity)
	require.NoError(t, err)
	require.Equal(t, "sk-historical", credentials["api_key"])
}

func TestAccountCredentialsFromEntity_FailsClosedOnWrongKey(t *testing.T) {
	encryptor, err := atrest.New(repositoryAtRestKeyHex)
	require.NoError(t, err)
	ciphertext, err := encryptor.EncryptString(`{"api_key":"sk-secret"}`)
	require.NoError(t, err)

	other, err := atrest.New(strings.Repeat("11", 32))
	require.NoError(t, err)
	atrest.Configure(other)
	t.Cleanup(atrest.Reset)

	entity := &dbent.Account{ID: 4, CredentialsCipher: &ciphertext}
	credentials, err := accountCredentialsFromEntity(entity)
	require.Error(t, err)
	require.Nil(t, credentials, "wrong key must never return ciphertext or partial credentials")
	require.NotContains(t, err.Error(), "sk-secret")
}

func TestSealCredentials_RemovesSensitiveValuesKeepsStructure(t *testing.T) {
	credentials := map[string]any{
		"api_key":              "sk-live",
		"access_token":         "at-live",
		"refresh_token":        "rt-live",
		"cookie":               "session=secret",
		"custom_usage_secrets": map[string]any{"api_key": "nested-secret"},
		"base_url":             "https://api.example.com",
		"model_mapping":        map[string]any{"gpt": "gpt-4"},
		"oauth_type":           "openai",
	}
	sealed := SealCredentials(credentials)
	require.Equal(t, true, sealed["_sealed"])
	require.Equal(t, "https://api.example.com", sealed["base_url"])
	require.NotNil(t, sealed["model_mapping"])
	require.Equal(t, "openai", sealed["oauth_type"])
	require.NotContains(t, sealed, "api_key")
	require.NotContains(t, sealed, "access_token")
	require.NotContains(t, sealed, "refresh_token")
	require.NotContains(t, sealed, "cookie")
	require.NotContains(t, sealed, "custom_usage_secrets")

	keys, ok := sealed["_sealed_keys"].([]string)
	require.True(t, ok)
	require.Equal(t, []string{"access_token", "api_key", "cookie", "custom_usage_secrets", "refresh_token"}, keys)

	// SQL 仍需要按值比较的两类键写入不可逆摘要。
	require.Equal(t, atrest.IdentityDigest("api_key", "sk-live"), sealed["_id_api_key"])
	require.Equal(t, atrest.IdentityDigest("access_token", "at-live"), sealed["_id_access_token"])
	require.NotContains(t, mustJSON(t, sealed), "sk-live")
	require.NotContains(t, mustJSON(t, sealed), "at-live")

	// 确定性：CAS 依赖同一明文永远得到同一投影。
	require.Equal(t, mustJSON(t, sealed), mustJSON(t, SealCredentials(credentials)))
}

func TestCredentialsCASParam_WrapsPlainAndSealed(t *testing.T) {
	credentials := map[string]any{"api_key": "sk-x", "base_url": "https://api.example.com"}
	param, err := credentialsCASParam(credentials)
	require.NoError(t, err)
	var wrapped map[string]json.RawMessage
	require.NoError(t, json.Unmarshal([]byte(param), &wrapped))
	require.Contains(t, wrapped, "plain")
	require.Contains(t, wrapped, "sealed")

	var plain map[string]any
	require.NoError(t, json.Unmarshal(wrapped["plain"], &plain))
	require.Equal(t, "sk-x", plain["api_key"])

	var sealed map[string]any
	require.NoError(t, json.Unmarshal(wrapped["sealed"], &sealed))
	require.Equal(t, true, sealed["_sealed"])
	require.NotContains(t, sealed, "api_key")

	// CAS 谓词/写入表达式按行状态选择形态。
	match := credentialsCASMatchSQL("a.credentials", "$7")
	require.Contains(t, match, "a.credentials ? '_sealed'")
	require.Contains(t, match, "$7::jsonb -> 'sealed'")
	require.Contains(t, match, "$7::jsonb -> 'plain'")
}

func TestCredentialsCipherForStorage_FollowsSealingSwitch(t *testing.T) {
	// 默认（无 env）：不产生密文。
	atrest.Reset()
	t.Cleanup(atrest.Reset)
	ciphertext, err := credentialsCipherForStorage(map[string]any{"api_key": "sk-x"})
	require.NoError(t, err)
	require.Empty(t, ciphertext, "default must never produce ciphertext")

	// 只有密钥、总开关关闭：仍然不产生密文（只保留解密能力）。
	configureRepositoryAtRestSealing(t, false)
	ciphertext, err = credentialsCipherForStorage(map[string]any{"api_key": "sk-x"})
	require.NoError(t, err)
	require.Empty(t, ciphertext, "key without SUB2API_ATREST_ENABLED=true must not seal")

	// 显式开启 sealing：走原加密路径。
	configureRepositoryAtRest(t)
	ciphertext, err = credentialsCipherForStorage(map[string]any{"api_key": "sk-x"})
	require.NoError(t, err)
	require.True(t, atrest.IsEncrypted(ciphertext))
	plain, err := atrest.DecryptString(ciphertext)
	require.NoError(t, err)
	require.JSONEq(t, `{"api_key":"sk-x"}`, plain)
}

// 默认关闭时写路径必须落在明文、不产生任何密文；这是防止「密钥被加回环境
// 又自动加密」的核心回归。
func TestWritePathsStayPlaintextByDefault(t *testing.T) {
	atrest.Reset()
	t.Cleanup(atrest.Reset)

	plainJSON, _, cipher, err := credentialsStorageParams(map[string]any{"api_key": "sk-x"})
	require.NoError(t, err)
	require.JSONEq(t, `{"api_key":"sk-x"}`, plainJSON, "unsealed row keeps full plaintext")
	require.Nil(t, cipher, "credentials_cipher must stay NULL by default")

	plain, proxyCipher, err := proxyPasswordForStorage("proxy-pass", true)
	require.NoError(t, err)
	require.Equal(t, "proxy-pass", plain)
	require.Empty(t, proxyCipher, "password_cipher must stay NULL by default")

	encoded, err := encodeSettingValue("smtp_password", "smtp-secret")
	require.NoError(t, err)
	require.Equal(t, "smtp-secret", encoded, "settings stay plaintext by default")
}

func TestRefreshTokenPredicatesCoverSealedRows(t *testing.T) {
	present := refreshTokenPresentSQL("credentials")
	require.Contains(t, present, "credentials ? '_sealed'")
	require.Contains(t, present, "'_sealed_keys'")
	require.Contains(t, present, "btrim(credentials->>'refresh_token') <> ''")

	absent := refreshTokenAbsentSQL("a.credentials")
	require.Contains(t, absent, "a.credentials ? '_sealed'")
	require.Contains(t, absent, "NOT COALESCE")
}

func TestDecodeCredentialsStored(t *testing.T) {
	configureRepositoryAtRest(t)
	ciphertext, err := atrest.EncryptString(`{"api_key":"sk-cipher"}`)
	require.NoError(t, err)

	// 双写行：明文优先（2026-11-22 事故修复）。
	credentials, err := decodeCredentialsStored([]byte(`{"api_key":"sk-plain"}`), ciphertext)
	require.NoError(t, err)
	require.Equal(t, "sk-plain", credentials["api_key"])

	// 封存行：明文只剩结构键，必须解密。
	sealed, err := json.Marshal(SealCredentials(map[string]any{"api_key": "sk-cipher"}))
	require.NoError(t, err)
	credentials, err = decodeCredentialsStored(sealed, ciphertext)
	require.NoError(t, err)
	require.Equal(t, "sk-cipher", credentials["api_key"])

	legacy, err := decodeCredentialsStored([]byte(`{"api_key":"sk-plain"}`), "")
	require.NoError(t, err)
	require.Equal(t, "sk-plain", legacy["api_key"])

	_, err = decodeCredentialsStored([]byte(`not-json`), "")
	require.Error(t, err)
}

func TestProxyPasswordFromEntity(t *testing.T) {
	configureRepositoryAtRest(t)
	ciphertext, err := atrest.EncryptString("proxy-pass")
	require.NoError(t, err)
	plaintextPassword := "legacy-pass"

	// 双写行：明文优先，无密钥时也不会因为密文拖死读取。
	dualWrite := &dbent.Proxy{ID: 1, PasswordCipher: &ciphertext, Password: &plaintextPassword}
	password, err := proxyPasswordFromEntity(dualWrite)
	require.NoError(t, err)
	require.Equal(t, "legacy-pass", password, "plaintext column wins on dual-write rows")

	// 封存行：明文列为空，走密文解密。
	empty := ""
	sealedRow := &dbent.Proxy{ID: 4, PasswordCipher: &ciphertext, Password: &empty}
	password, err = proxyPasswordFromEntity(sealedRow)
	require.NoError(t, err)
	require.Equal(t, "proxy-pass", password, "cipher is used when plaintext column is empty")

	legacy := &dbent.Proxy{ID: 2, Password: &plaintextPassword}
	password, err = proxyPasswordFromEntity(legacy)
	require.NoError(t, err)
	require.Equal(t, "legacy-pass", password)

	managed := &dbent.Proxy{ID: 3, Password: atrestStringPtr("enc:managed:v1:abc")}
	password, err = proxyPasswordFromEntity(managed)
	require.NoError(t, err)
	require.Equal(t, "enc:managed:v1:abc", password, "managed ciphertext is decoded by the decorator")

	atrest.Reset()
	password, err = proxyPasswordFromEntity(dualWrite)
	require.NoError(t, err)
	require.Equal(t, "legacy-pass", password, "dual-write row must survive a missing key")
	_, err = proxyPasswordFromEntity(sealedRow)
	require.Error(t, err)
	require.ErrorIs(t, err, atrest.ErrNoKey)
}

func TestProxyPasswordForStorage(t *testing.T) {
	// 默认：明文进 password，password_cipher 为空（NULL）。
	atrest.Reset()
	t.Cleanup(atrest.Reset)
	plain, cipher, err := proxyPasswordForStorage("pass", true)
	require.NoError(t, err)
	require.Equal(t, "pass", plain)
	require.Empty(t, cipher)

	// 有密钥但总开关关闭：仍只写明文。
	configureRepositoryAtRestSealing(t, false)
	plain, cipher, err = proxyPasswordForStorage("pass", true)
	require.NoError(t, err)
	require.Equal(t, "pass", plain)
	require.Empty(t, cipher, "key without sealing switch must not produce ciphertext")

	// sealing 开启：走原加密路径。
	configureRepositoryAtRest(t)
	plain, cipher, err = proxyPasswordForStorage("pass", true)
	require.NoError(t, err)
	require.Empty(t, plain, "new rows are sealed")
	require.True(t, atrest.IsEncrypted(cipher))

	plain, cipher, err = proxyPasswordForStorage("pass", false)
	require.NoError(t, err)
	require.Equal(t, "pass", plain, "existing unsealed rows keep plaintext for rollback")
	require.True(t, atrest.IsEncrypted(cipher))

	plain, cipher, err = proxyPasswordForStorage("enc:managed:v1:abc", true)
	require.NoError(t, err)
	require.Equal(t, "enc:managed:v1:abc", plain)
	require.Empty(t, cipher, "managed proxy line format must not be double-encrypted")
}

func TestSettingValueCodec(t *testing.T) {
	// 默认：保持明文。
	atrest.Reset()
	t.Cleanup(atrest.Reset)
	encoded, err := encodeSettingValue("smtp_password", "smtp-secret")
	require.NoError(t, err)
	require.Equal(t, "smtp-secret", encoded, "default keeps legacy plaintext")

	if !IsSensitiveSettingKey("smtp_password") || IsSensitiveSettingKey("some_toggle") {
		t.Fatalf("sensitive setting key whitelist is wrong")
	}

	// 只有密钥、总开关关闭：仍保持明文。
	configureRepositoryAtRestSealing(t, false)
	encoded, err = encodeSettingValue("smtp_password", "smtp-secret")
	require.NoError(t, err)
	require.Equal(t, "smtp-secret", encoded, "key without sealing switch must not encrypt settings")

	// sealing 开启：走原加密路径。
	configureRepositoryAtRest(t)
	encoded, err = encodeSettingValue("smtp_password", "smtp-secret")
	require.NoError(t, err)
	require.True(t, atrest.IsEncrypted(encoded))

	decoded, err := decodeSettingValue("smtp_password", encoded)
	require.NoError(t, err)
	require.Equal(t, "smtp-secret", decoded)

	// 非白名单键永远保持明文。
	toggle, err := encodeSettingValue("some_toggle", "true")
	require.NoError(t, err)
	require.Equal(t, "true", toggle)

	// 密文无法解密时 fail-closed，绝不返回密文。
	_, err = decodeSettingValue("smtp_password", "enc:v2:broken")
	require.Error(t, err)
}

// 总开关关闭但密钥仍在时，历史 enc:v2: 设置值必须仍能解密（读取路径与开关解耦）。
func TestSettingValueDecodesHistoricalCipherWhenSealingDisabled(t *testing.T) {
	configureRepositoryAtRestSealing(t, false)
	ciphertext, err := atrest.Current().EncryptString("smtp-historical")
	require.NoError(t, err)
	decoded, err := decodeSettingValue("smtp_password", ciphertext)
	require.NoError(t, err)
	require.Equal(t, "smtp-historical", decoded)
}

func TestSettingSensitiveKeyListMatchesServiceContract(t *testing.T) {
	expected := []string{
		"github_oauth_client_secret",
		"google_oauth_client_secret",
		"oidc_connect_client_secret",
		"smtp_password",
		"tencent_captcha_app_secret_key",
		"tencent_captcha_cloud_secret_id",
		"tencent_captcha_cloud_secret_key",
		"turnstile_secret_key",
		"notification_email_unsubscribe_secret",
	}
	require.Equal(t, expected, SensitiveSettingKeys)
}

func mustJSON(t *testing.T, value any) string {
	t.Helper()
	raw, err := json.Marshal(value)
	require.NoError(t, err)
	return string(raw)
}

func atrestStringPtr(value string) *string { return &value }
