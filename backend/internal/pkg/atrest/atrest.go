// Package atrest 提供数据库敏感字段的 at-rest 加密原语。
//
// 设计约束（2026-11 事故后的硬性要求）：
//   - 加密写入（sealing）由总开关 SUB2API_ATREST_ENABLED 控制，默认 false（关闭）。
//     只有开关显式设为 "true" 且 SUB2API_ATREST_KEY 有效时才允许产生新的密文。
//   - 密钥只来自部署环境变量 SUB2API_ATREST_KEY，绝不落库、绝不写入仓库。
//   - 密文格式固定为 "enc:v2:" + base64(nonce || ciphertext || tag)，
//     AES-256-GCM，每次加密使用随机 12 字节 nonce。
//   - 读取路径与开关无关：加载到有效密钥时，历史 enc:v2: 密文仍能解密；
//     未配置/无法解析密钥时遇到 enc:v2: 密文返回 ErrNoKey（fail-closed，
//     绝不把密文当业务值向上返回）。总开关关闭时明文原样读取。
//   - 开启 sealing 前必须先跑真实网关请求验收（历史事故：2026-11-22 上游
//     api_key 读不到导致全站 502；数据已回退、环境密钥已删除）。默认关闭
//     就是为了防止把 SUB2API_ATREST_KEY 加回环境后再次自动加密。
//   - 错误信息不包含明文或密文片段。
package atrest

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"strings"
	"sync/atomic"
)

// getenv 是 os.Getenv 的薄封装，便于测试替换。
var getenv = os.Getenv

// Prefix 标记 at-rest 密文。旧格式（明文、enc:managed:v1: 等）一律不含该前缀。
const Prefix = "enc:v2:"

// KeyEnv 是唯一受支持的密钥来源。值为 32 字节密钥的 hex（64 字符）
// 或标准/URL base64 编码；生成方法：openssl rand -hex 32。
const KeyEnv = "SUB2API_ATREST_KEY"

// EnabledEnv 是 at-rest 加密写入（sealing）的总开关。默认关闭；
// 只有显式设为 "true"（大小写不敏感）且 KeyEnv 有效时才允许加密写入。
// 读取历史密文不受该开关影响：只要加载到有效密钥就能解密。
const EnabledEnv = "SUB2API_ATREST_ENABLED"

// ErrNoKey 表示当前实例未配置 at-rest 密钥：
// 不允许加密写入，也不允许把密文当明文读取。
var ErrNoKey = errors.New("atrest: SUB2API_ATREST_KEY is not configured")

// ErrSealingDisabled 表示总开关未开启：拒绝任何新的加密写入。
// 注意与 ErrNoKey 区分：这里密钥可能有效，但 sealing 被开关禁用。
var ErrSealingDisabled = errors.New("atrest: sealing is disabled (SUB2API_ATREST_ENABLED is not true)")

// ErrDecrypt 表示密文无法解密（密钥不匹配、数据损坏或不支持的格式）。
var ErrDecrypt = errors.New("atrest: ciphertext cannot be decrypted")

// Cipher 是配置好的 AES-256-GCM 加解密器。
type Cipher struct {
	aead        cipher.AEAD
	fingerprint string
}

// ParseKey 解析密钥材料：hex（64 字符，可带 0x 前缀）或 base64
// （标准/URL、可带 padding），解码后必须是 32 字节。
func ParseKey(keyMaterial string) ([]byte, error) {
	keyMaterial = strings.TrimSpace(keyMaterial)
	if keyMaterial == "" {
		return nil, ErrNoKey
	}
	trimmed := keyMaterial
	if strings.HasPrefix(trimmed, "0x") || strings.HasPrefix(trimmed, "0X") {
		trimmed = trimmed[2:]
	}
	if len(trimmed) == 64 && isHex(trimmed) {
		key, err := hex.DecodeString(trimmed)
		if err != nil {
			return nil, fmt.Errorf("atrest: hex key is invalid")
		}
		return key, nil
	}
	encodings := []*base64.Encoding{
		base64.StdEncoding,
		base64.RawStdEncoding,
		base64.URLEncoding,
		base64.RawURLEncoding,
	}
	for _, encoding := range encodings {
		decoded, err := encoding.DecodeString(trimmed)
		if err != nil {
			continue
		}
		if len(decoded) == 32 {
			return decoded, nil
		}
	}
	return nil, fmt.Errorf("atrest: key must be 32 bytes encoded as 64 hex chars or base64")
}

func isHex(value string) bool {
	for _, r := range value {
		switch {
		case r >= '0' && r <= '9':
		case r >= 'a' && r <= 'f':
		case r >= 'A' && r <= 'F':
		default:
			return false
		}
	}
	return len(value) > 0
}

// New 根据密钥材料构造加解密器。空密钥返回 (nil, nil)，表示只读兼容模式。
// 注意：本函数只构造密钥能力，不改变进程级 sealing 开关。
func New(keyMaterial string) (*Cipher, error) {
	keyMaterial = strings.TrimSpace(keyMaterial)
	if keyMaterial == "" {
		return nil, nil
	}
	key, err := ParseKey(keyMaterial)
	if err != nil {
		return nil, err
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, fmt.Errorf("atrest: create cipher: %w", err)
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("atrest: create gcm: %w", err)
	}
	sum := sha256.Sum256(key)
	return &Cipher{aead: aead, fingerprint: hex.EncodeToString(sum[:8])}, nil
}

// Fingerprint 返回密钥的短指纹（sha256 前 8 字节 hex），仅用于日志/运维核对，
// 不可逆推出密钥本身。
func (c *Cipher) Fingerprint() string {
	if c == nil {
		return ""
	}
	return c.fingerprint
}

// EncryptString 用随机 nonce 加密明文，返回带 enc:v2: 前缀的密文。
// 这是底层原语，不检查 SUB2API_ATREST_ENABLED 总开关：业务写路径应先经
// atrest.Enabled() 或包级 atrest.EncryptString 判定，默认关闭时不得落密文。
// 未配置密钥时返回 ErrNoKey（fail-closed，禁止加密写入）。
func (c *Cipher) EncryptString(plaintext string) (string, error) {
	if c == nil {
		return "", ErrNoKey
	}
	nonce := make([]byte, c.aead.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return "", fmt.Errorf("atrest: generate nonce: %w", err)
	}
	sealed := c.aead.Seal(nil, nonce, []byte(plaintext), nil)
	payload := make([]byte, 0, len(nonce)+len(sealed))
	payload = append(payload, nonce...)
	payload = append(payload, sealed...)
	return Prefix + base64.StdEncoding.EncodeToString(payload), nil
}

// DecryptString 解密 enc:v2: 密文。不含前缀的值视为历史明文原样返回；
// 含前缀但无法解密时返回错误（绝不返回密文本身）。
func (c *Cipher) DecryptString(value string) (string, error) {
	if !IsEncrypted(value) {
		return value, nil
	}
	if c == nil {
		return "", ErrNoKey
	}
	payload, err := base64.StdEncoding.DecodeString(strings.TrimPrefix(value, Prefix))
	if err != nil {
		return "", fmt.Errorf("%w: invalid base64 payload", ErrDecrypt)
	}
	nonceSize := c.aead.NonceSize()
	if len(payload) < nonceSize+c.aead.Overhead() {
		return "", fmt.Errorf("%w: payload too short", ErrDecrypt)
	}
	nonce, ciphertext := payload[:nonceSize], payload[nonceSize:]
	plaintext, err := c.aead.Open(nil, nonce, ciphertext, nil)
	if err != nil {
		return "", fmt.Errorf("%w: authentication failed", ErrDecrypt)
	}
	return string(plaintext), nil
}

// IsEncrypted 判断值是否为 enc:v2: 密文（历史明文返回 false）。
func IsEncrypted(value string) bool {
	return strings.HasPrefix(value, Prefix)
}

// ── 进程级默认实例 ──────────────────────────────────────────────────────────
//
// 服务进程在启动时调用 ConfigureFromEnv 一次；仓储层通过包级函数读写。
// 单测可通过 Configure 显式注入，并用 Reset 清理。
//
// 进程状态分成两件事：
//   - cipher：加载到的密钥，决定「能不能解密历史密文」；
//   - sealing：总开关，决定「能不能产生新密文」。
//
// 二者解耦是为了默认关闭：即使运维把 SUB2API_ATREST_KEY 加回环境，只要没
// 显式打开 SUB2API_ATREST_ENABLED，实例仍然只写明文，不会再次触发
// 2026-11-22 的 502 事故。
var (
	current        atomic.Pointer[Cipher]
	sealingEnabled atomic.Bool
)

// Configure 设置进程级默认加解密器，并按其是否为空启用/关闭 sealing。
// 仅用于单测与内部装配：生产环境请用 ConfigureFromEnv（默认关闭）。
func Configure(c *Cipher) {
	current.Store(c)
	setSealing(c != nil)
}

func setSealing(enabled bool) {
	sealingEnabled.Store(enabled)
}

// SetSealingEnabled 覆盖 sealing 开关（仅用于单测与显式装配）。
// 生产环境的开关来自 SUB2API_ATREST_ENABLED，默认关闭。
func SetSealingEnabled(enabled bool) {
	setSealing(enabled)
}

// Reset 清除进程级配置与 sealing 开关（仅用于测试）。
func Reset() {
	current.Store(nil)
	setSealing(false)
}

// Current 返回进程级默认加解密器；未配置时返回 nil。
func Current() *Cipher {
	return current.Load()
}

// ConfigureFromEnv 读取 SUB2API_ATREST_ENABLED / SUB2API_ATREST_KEY 并配置
// 进程级默认实例。返回加载到的加解密器（没有可用密钥时为 nil）。
//
// 语义（总开关默认关闭）：
//   - 开关未开启：即使密钥有效也拒绝加密写入（Enabled()=false、EncryptString
//     返回 ErrSealingDisabled）；密钥仍会加载，历史 enc:v2: 密文照常可解密。
//     开关未开启时非法密钥只告警不阻断启动（避免残留坏密钥炸掉进程）。
//   - 开关开启：必须有有效密钥，否则返回错误让进程启动失败（fail-closed），
//     避免运维以为在加密、实际却静默写明文。
//   - 开启前必须先跑真实网关请求验收（2026-11-22 全站 502 事故）。
func ConfigureFromEnv() (*Cipher, error) {
	sealingRequested, err := parseSealingFlag(getenv(EnabledEnv))
	if err != nil {
		return nil, err
	}
	raw := strings.TrimSpace(getenv(KeyEnv))
	c, keyErr := New(raw)
	if keyErr != nil {
		if sealingRequested {
			return nil, keyErr
		}
		slog.Warn("at-rest key is invalid and ignored because sealing is disabled",
			"env", KeyEnv, "error", keyErr)
		c = nil
	}
	if sealingRequested && c == nil {
		return nil, fmt.Errorf("atrest: %s=true requires a valid %s", EnabledEnv, KeyEnv)
	}
	setSealing(sealingRequested && c != nil)
	current.Store(c)
	switch {
	case sealingEnabled.Load():
		slog.Info("at-rest sealing enabled: new secrets are encrypted at rest",
			"key_fingerprint", c.Fingerprint())
	case c != nil:
		slog.Warn("at-rest sealing disabled (default): historical enc:v2: values still decrypt, new writes stay plaintext",
			"key_fingerprint", c.Fingerprint())
	default:
		slog.Warn("at-rest sealing disabled (default) and no key is configured: plaintext reads are compatible, encrypted reads fail closed")
	}
	return c, nil
}

// parseSealingFlag 解析总开关。默认 false；只接受 "true"/"false"
// （大小写不敏感、忽略首尾空白），其它值直接报错，避免拼写错误被当成开启。
func parseSealingFlag(raw string) (bool, error) {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "", "false":
		return false, nil
	case "true":
		return true, nil
	default:
		return false, fmt.Errorf("atrest: %s must be \"true\" or \"false\"", EnabledEnv)
	}
}

// Enabled 报告当前实例是否允许加密写入（sealing）。总开关默认关闭，
// 因此默认返回 false；只有开关开启且密钥有效时才为 true。
func Enabled() bool {
	return sealingEnabled.Load() && current.Load() != nil
}

// KeyConfigured 报告当前实例是否加载了可用的 at-rest 密钥（解密历史密文的能力）。
// 与 Enabled 不同：密钥存在但总开关关闭时 KeyConfigured=true、Enabled=false。
func KeyConfigured() bool {
	return current.Load() != nil
}

// KeyFingerprint 返回当前密钥指纹（未配置时为空）。
func KeyFingerprint() string {
	return Current().Fingerprint()
}

// EncryptString 使用进程级默认实例加密。总开关关闭或未配置密钥时返回错误：
// 默认关闭的部署永远写不出新密文。
func EncryptString(plaintext string) (string, error) {
	if !Enabled() {
		if Current() == nil {
			return "", ErrNoKey
		}
		return "", ErrSealingDisabled
	}
	return Current().EncryptString(plaintext)
}

// DecryptString 使用进程级默认实例解密；未配置密钥时明文原样返回、密文返回 ErrNoKey。
// 解密能力不受 sealing 开关影响，以便开关关闭时仍能读出历史密文。
func DecryptString(value string) (string, error) {
	return Current().DecryptString(value)
}

// IdentityDigest 计算敏感值在「封存投影」中的稳定身份摘要。
// 用途：删除明文后仍需要按值比较/分组的 SQL（api_key、access_token）可以比较摘要；
// 摘要为域分离的 SHA-256，不可逆，只泄露「两行值是否相等」这一点信息。
func IdentityDigest(domain, value string) string {
	sum := sha256.Sum256([]byte("sub2api/atrest/id/v2\x00" + domain + "\x00" + value))
	return hex.EncodeToString(sum[:])
}
