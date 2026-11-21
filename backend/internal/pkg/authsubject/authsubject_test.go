package authsubject

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"strings"
	"testing"
)

func TestHashIsStableKeyedHMAC(t *testing.T) {
	if !InstallationKeyConfigured() {
		t.Fatal("test installation key should be configured by init")
	}
	first := HashIdentity("github", "union-123")
	if second := HashIdentity("github", " union-123 "); first != second {
		t.Fatalf("trim normalization should be stable: %q vs %q", first, second)
	}
	if len(first) != HashHexLen {
		t.Fatalf("hash length = %d, want %d", len(first), HashHexLen)
	}
	// keyed：同一个 subject 在不同 provider / domain 下必须不同（防止跨命名空间关联）。
	if HashIdentity("github", "same-subject") == HashIdentity("google", "same-subject") {
		t.Fatal("provider_type must participate in the MAC input")
	}
	if HashIdentity("wechat", "same-subject") == HashChannel("wechat", "same-subject") {
		t.Fatal("domain must participate in the MAC input")
	}
	// 裸 SHA-256 断言：防止有人把实现改回无 key 的 sha256（email subject 可被字典反推）。
	bare := "ba7816bf8f01cfea414140de5dae2223b00361a396177a9cb410ff61f20015ad" // sha256("abc")
	if HashIdentity("email", "abc") == bare {
		t.Fatal("subject hash must be keyed, not bare sha256")
	}
}

func TestConfigureInstallationKeyValidatesHex(t *testing.T) {
	if err := ConfigureInstallationKey("zz"); err == nil {
		t.Fatal("invalid hex should be rejected")
	}
	if err := ConfigureInstallationKey("00112233"); err == nil {
		t.Fatal("short key should be rejected")
	}
	// 配置回测试 key，避免影响同包其它用例。
	if err := ConfigureInstallationKey(testInstallationKey); err != nil {
		t.Fatalf("restore test key: %v", err)
	}
}

func TestNormalizeOnlyTrimsWhitespace(t *testing.T) {
	if got := Normalize("  UnionID-ABC \t"); got != "UnionID-ABC" {
		t.Fatalf("normalize = %q", got)
	}
	// subject 大小写敏感：大小写折叠会让不同身份哈希碰撞。
	if HashIdentity("github", "UnionID-ABC") == HashIdentity("github", "unionid-abc") {
		t.Fatal("hash must be case sensitive")
	}
	// provider_type 只做 trim + 小写，保证大小写不同的配置指向同一命名空间。
	if HashIdentity(" GitHub ", "subject") != HashIdentity("github", "subject") {
		t.Fatal("provider_type should be normalized")
	}
}

func TestPrefixComesFromHashNotSubject(t *testing.T) {
	hashValue := HashIdentity("github", "union-abcdef0123456789")
	prefix := PrefixFromHash(hashValue)
	if prefix != hashValue[:PrefixHexLen] {
		t.Fatalf("prefix = %q, want hash[:%d]", prefix, PrefixHexLen)
	}
	// 前缀必须来自 hash，且不能包含 subject 的任何原文字符。
	if strings.Contains(strings.ToLower(prefix), "union") || strings.Contains(strings.ToLower(prefix), "abc") {
		t.Fatalf("prefix must not derive from subject: %q", prefix)
	}
	if got := PrefixFromHash("abc"); got != "" {
		t.Fatalf("short hash prefix = %q, want empty", got)
	}
	// 大小写/空白归一化。
	if got := PrefixFromHash(strings.ToUpper(hashValue) + "\n"); got != hashValue[:PrefixHexLen] {
		t.Fatalf("normalized prefix = %q", got)
	}
}

func TestMaskNeverReturnsFullHash(t *testing.T) {
	hashValue := HashIdentity("github", "union-abcdef0123456789")
	masked := MaskPrefix(PrefixFromHash(hashValue))
	if strings.Contains(masked, hashValue) {
		t.Fatalf("mask leaked full hash: %q", masked)
	}
	if masked != hashValue[:PrefixHexLen]+"***" {
		t.Fatalf("mask = %q", masked)
	}
	if got := MaskPrefix(""); got != "***" {
		t.Fatalf("empty prefix mask = %q, want ***", got)
	}
	if got := MaskPrefix("a1b2c3d4"); got != "a1b2c3d4***" {
		t.Fatalf("prefix mask = %q", got)
	}
}

func TestIsHashOnlyAcceptsHexDigest(t *testing.T) {
	if !IsHash(HashIdentity("github", "abc")) {
		t.Fatal("should accept hmac hex")
	}
	if IsHash("abc") || IsHash(strings.Repeat("z", 64)) || IsHash(strings.Repeat("a", 63)) {
		t.Fatal("should reject non hex")
	}
	if !IsHash(strings.ToUpper(HashIdentity("github", "abc"))) {
		t.Fatal("should accept uppercase hex")
	}
}

// TestKeyedHashInputFormatMatchesDocs 锁定 MAC 输入格式：
// HMAC-SHA256(key, domain \x00 lowercase(provider_type) \x00 trim(subject))。
// 迁移回填 SQL（docs/ops/sql/329_backfill.sql）用 bytea 拼同样的字节，
// 本测试防止两边格式漂移导致回填出来的哈希与代码不匹配。
func TestKeyedHashInputFormatMatchesDocs(t *testing.T) {
	keyHex := testInstallationKey
	key, err := hex.DecodeString(keyHex)
	if err != nil {
		t.Fatalf("decode key: %v", err)
	}
	expected := func(domain, providerType, subject string) string {
		mac := hmac.New(sha256.New, key)
		_, _ = mac.Write([]byte(domain))
		_, _ = mac.Write([]byte{0})
		_, _ = mac.Write([]byte(strings.ToLower(strings.TrimSpace(providerType))))
		_, _ = mac.Write([]byte{0})
		_, _ = mac.Write([]byte(strings.TrimSpace(subject)))
		return hex.EncodeToString(mac.Sum(nil))
	}

	if got, want := HashIdentity(" GitHub ", " abc "), expected("identity", "github", "abc"); got != want {
		t.Fatalf("identity hash format drifted: got %q want %q", got, want)
	}
	if got, want := HashChannel("wechat", "openid"), expected("channel", "wechat", "openid"); got != want {
		t.Fatalf("channel hash format drifted: got %q want %q", got, want)
	}
}

func TestSealSensitiveRoundTripAndDetection(t *testing.T) {
	plaintext := "union-abcdef0123456789"
	sealed, err := SealSensitive(plaintext)
	if err != nil {
		t.Fatalf("seal: %v", err)
	}
	if !IsSealed(sealed) || !strings.HasPrefix(sealed, SealedPrefix) {
		t.Fatalf("sealed value missing prefix: %q", sealed)
	}
	if strings.Contains(sealed, plaintext) {
		t.Fatalf("sealed value leaks plaintext: %q", sealed)
	}
	opened, ok, err := OpenSensitive(sealed)
	if err != nil || !ok {
		t.Fatalf("open sealed: ok=%v err=%v", ok, err)
	}
	if opened != plaintext {
		t.Fatalf("opened = %q, want %q", opened, plaintext)
	}
	// 非封存值（历史明文）原样返回并标记 ok=false。
	opened, ok, err = OpenSensitive("legacy-plaintext")
	if err != nil || ok || opened != "" {
		t.Fatalf("legacy value handling: opened=%q ok=%v err=%v", opened, ok, err)
	}
	// 不同 installation key 解不开（多实例必须共享同一把安装密钥）。
	if err := ConfigureInstallationKey(strings.Repeat("ab", 32)); err != nil {
		t.Fatalf("rotate test key: %v", err)
	}
	if _, _, err := OpenSensitive(sealed); err == nil {
		t.Fatal("sealed value must not open under a different installation key")
	}
	if err := ConfigureInstallationKey(testInstallationKey); err != nil {
		t.Fatalf("restore test key: %v", err)
	}
}
