// Package authsubject 统一第三方登录身份 subject（auth_identities.provider_subject、
// auth_identity_channels.channel_subject、pending_auth_sessions.provider_subject）
// 的规范化、keyed 盲索引与展示掩码规则。
//
// 安全模型：
//   - 数据库只保留 HMAC-SHA256(installation key, domain|provider_type|subject) 的
//     hex 摘要（盲索引）与很短的前缀；完整 subject 不再回显给后台/接口/日志。
//   - 使用 keyed hash（而不是裸 SHA-256）是因为 email 会作为 email provider 的
//     subject 落库：邮箱可枚举，裸 SHA-256 能被字典/彩虹表反推。HMAC 的 key 是
//     安装密钥（security_secrets.totp_encryption_key，跨实例持久且共享），
//     没有 key 的库泄露无法离线验证候选 subject。
//   - domain 参与 MAC 输入，把 identity / channel 两个用途隔离开，避免同一
//     subject 在两个列之间可关联。
//   - provider_type 参与 MAC 输入，把不同 provider 命名空间隔离开。
//     provider_key 不参与：微信历史别名（wechat/wechat-main/自定义 AppID）有
//     三套兼容归一化规则，把 provider_key 放进 MAC 输入会让「写入时的 key」
//     与「查询时兼容 key 集合」算出的哈希不一致，直接导致老用户登录失败。
//   - key 是 installation key（见 repository/security_secret_bootstrap.go），
//     丢失 key 等价于丢失 TOTP 密文：所有盲索引都将无法再计算，必须从数据库
//     备份恢复同一把 key，不要轮换。
package authsubject

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"io"
	"strings"
	"sync/atomic"
	"testing"
)

const (
	// HashHexLen 是 HMAC-SHA256 hex 摘要的固定长度，也是
	// provider_subject_hash / channel_subject_hash CHAR(64) 的列宽。
	HashHexLen = 64
	// PrefixColumnLen 是 *_prefix VARCHAR(16) 的列宽。
	PrefixColumnLen = 16
	// PrefixHexLen 是落库 / 展示用盲索引前缀保留的 hex 字符数。
	// 前缀来自 hash 本身（不是 subject 原文），只用于后台/SRE 关联，
	// 无法据此还原或猜测 subject。
	PrefixHexLen = 8

	// DomainIdentity 是 auth_identities.provider_subject / pending 会话的 MAC 域。
	DomainIdentity = "identity"
	// DomainChannel 是 auth_identity_channels.channel_subject 的 MAC 域。
	DomainChannel = "channel"

	// InstallationKeyHexLen 是安装密钥（AES-256）的 hex 长度。
	InstallationKeyHexLen = 64

	// SealedPrefix 标记「用安装密钥 AES-256-GCM 封装的敏感值」。
	// pending 会话需要跨请求带回 subject 才能完成身份落库，但库里不应出现原文，
	// 因此存密文；解密只发生在同一安装密钥的实例内。
	SealedPrefix = "enc:sub:v1:"
)

// testInstallationKey 只用于 go test：正式运行必须由
// ConfigureAuthSubjectHashKey/ConfigureInstallationKey 注入持久安装密钥，
// 未配置时 KeyedHash 会 panic（fail-closed），不会静默使用弱常量。
const testInstallationKey = "000102030405060708090a0b0c0d0e0f101112131415161718191a1b1c1d1e1f"

var installationKey atomic.Pointer[[32]byte]

func init() {
	if testing.Testing() {
		if err := ConfigureInstallationKey(testInstallationKey); err != nil {
			panic("authsubject: configure test installation key: " + err.Error())
		}
	}
}

// ConfigureInstallationKey 注入安装密钥（64 位 hex，AES-256 的同一把 key）。
// 启动阶段在密钥 bootstrap 之后调用一次；重复配置同一把 key 是幂等的。
func ConfigureInstallationKey(keyHex string) error {
	keyHex = strings.TrimSpace(keyHex)
	raw, err := hex.DecodeString(keyHex)
	if err != nil {
		return errors.New("installation key is not valid hex")
	}
	if len(raw) != 32 {
		return errors.New("installation key must be 32 bytes (64 hex chars)")
	}
	var key [32]byte
	copy(key[:], raw)
	installationKey.Store(&key)
	return nil
}

// InstallationKeyConfigured 报告安装密钥是否已注入（启动自检/测试断言用）。
func InstallationKeyConfigured() bool {
	return installationKey.Load() != nil
}

// Normalize 返回参与哈希与明文回退比对的规范形式。
//
// 只做 TrimSpace：历史写入路径本来就对 subject 调用了 strings.TrimSpace，
// 这里保持一致才能保证「新哈希查询」与「旧明文查询」命中同一批行。
// 不要在这里做大小写折叠——email 的 lowercase 发生在更早的
// normalizeEmailAuthIdentitySubject，其它 provider 的 subject 大小写敏感。
func Normalize(subject string) string {
	return strings.TrimSpace(subject)
}

func normalizeProviderType(providerType string) string {
	return strings.ToLower(strings.TrimSpace(providerType))
}

// KeyedHash 返回 HMAC-SHA256(key, domain \x00 provider_type \x00 subject) 的小写 hex。
func KeyedHash(domain, providerType, subject string) string {
	key := installationKey.Load()
	if key == nil {
		panic("authsubject: installation key is not configured; call ConfigureInstallationKey during startup")
	}
	mac := hmac.New(sha256.New, key[:])
	_, _ = mac.Write([]byte(strings.TrimSpace(domain)))
	_, _ = mac.Write([]byte{0})
	_, _ = mac.Write([]byte(normalizeProviderType(providerType)))
	_, _ = mac.Write([]byte{0})
	_, _ = mac.Write([]byte(Normalize(subject)))
	return hex.EncodeToString(mac.Sum(nil))
}

// HashIdentity 返回 auth_identities.provider_subject 的盲索引。
func HashIdentity(providerType, subject string) string {
	return KeyedHash(DomainIdentity, providerType, subject)
}

// HashChannel 返回 auth_identity_channels.channel_subject 的盲索引。
func HashChannel(providerType, subject string) string {
	return KeyedHash(DomainChannel, providerType, subject)
}

// PrefixFromHash 返回盲索引的展示前缀（hash 前 8 个 hex 字符）。
// 前缀取自 hash 而不是 subject 原文：后台展示不会泄露身份 ID 的首尾字符。
func PrefixFromHash(hashValue string) string {
	hashValue = strings.ToLower(strings.TrimSpace(hashValue))
	if len(hashValue) < PrefixHexLen {
		return ""
	}
	return hashValue[:PrefixHexLen]
}

// MaskPrefix 把前缀渲染成可展示的掩码（没有前缀时返回固定掩码）。
func MaskPrefix(prefix string) string {
	prefix = strings.TrimSpace(prefix)
	if prefix == "" {
		return "***"
	}
	return prefix + "***"
}

// IsHash 判断字符串是否形如 HMAC-SHA256 hex 摘要（64 个 hex 字符）。
// 只用于迁移期判断「这个值是不是已经哈希过」，不用作安全边界。
func IsHash(value string) bool {
	if len(value) != HashHexLen {
		return false
	}
	for i := 0; i < len(value); i++ {
		c := value[i]
		if (c >= '0' && c <= '9') || (c >= 'a' && c <= 'f') || (c >= 'A' && c <= 'F') {
			continue
		}
		return false
	}
	return true
}

// SealSensitive 用安装密钥 AES-256-GCM 封装敏感值，输出 "enc:sub:v1:" + base64(nonce+ct)。
func SealSensitive(plaintext string) (string, error) {
	key := installationKey.Load()
	if key == nil {
		return "", errors.New("authsubject: installation key is not configured")
	}
	block, err := aes.NewCipher(key[:])
	if err != nil {
		return "", err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return "", err
	}
	nonce := make([]byte, gcm.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return "", err
	}
	sealed := gcm.Seal(nonce, nonce, []byte(plaintext), nil)
	return SealedPrefix + base64.StdEncoding.EncodeToString(sealed), nil
}

// OpenSensitive 解开 SealSensitive 的输出；ok=false 表示该值不是封存格式
// （历史明文行 / 未加密的合成会话）。
func OpenSensitive(value string) (plaintext string, ok bool, err error) {
	value = strings.TrimSpace(value)
	if !strings.HasPrefix(value, SealedPrefix) {
		return "", false, nil
	}
	key := installationKey.Load()
	if key == nil {
		return "", true, errors.New("authsubject: installation key is not configured")
	}
	raw, err := base64.StdEncoding.DecodeString(strings.TrimPrefix(value, SealedPrefix))
	if err != nil {
		return "", true, err
	}
	block, err := aes.NewCipher(key[:])
	if err != nil {
		return "", true, err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return "", true, err
	}
	if len(raw) < gcm.NonceSize() {
		return "", true, errors.New("sealed value too short")
	}
	nonce, ciphertext := raw[:gcm.NonceSize()], raw[gcm.NonceSize():]
	opened, err := gcm.Open(nil, nonce, ciphertext, nil)
	if err != nil {
		return "", true, err
	}
	return string(opened), true, nil
}

// IsSealed 判断值是否被封存（不尝试解密）。
func IsSealed(value string) bool {
	return strings.HasPrefix(strings.TrimSpace(value), SealedPrefix)
}
