// Package atrest 提供数据库敏感字段的 at-rest 加密原语。
//
// 设计约束（2026-11 事故后的硬性要求）：
//   - 密钥只来自部署环境变量 SUB2API_ATREST_KEY，绝不落库、绝不写入仓库。
//   - 密文格式固定为 "enc:v2:" + base64(nonce || ciphertext || tag)，
//     AES-256-GCM，每次加密使用随机 12 字节 nonce。
//   - 未配置密钥时只读兼容：明文原样读取；遇到 enc:v2: 密文直接返回
//     ErrNoKey（fail-closed，绝不把密文当业务值向上返回），并拒绝任何
//     加密写入（ErrNoKey），从而不会在没有密钥的实例上写出无法解密的行。
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

// ErrNoKey 表示当前实例未配置 at-rest 密钥：
// 不允许加密写入，也不允许把密文当明文读取。
var ErrNoKey = errors.New("atrest: SUB2API_ATREST_KEY is not configured")

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
var current atomic.Pointer[Cipher]

// Configure 设置进程级默认加解密器。nil 表示只读兼容模式。
func Configure(c *Cipher) {
	current.Store(c)
}

// Reset 清除进程级配置（仅用于测试）。
func Reset() {
	current.Store(nil)
}

// Current 返回进程级默认加解密器；未配置时返回 nil。
func Current() *Cipher {
	return current.Load()
}

// ConfigureFromEnv 读取 SUB2API_ATREST_KEY 并配置进程级默认实例。
// 返回配置好的实例；未配置时返回 nil 且不报错（只读兼容模式）。
func ConfigureFromEnv() (*Cipher, error) {
	raw := strings.TrimSpace(getenv(KeyEnv))
	c, err := New(raw)
	if err != nil {
		return nil, err
	}
	Configure(c)
	if c == nil {
		slog.Warn("at-rest encryption disabled: SUB2API_ATREST_KEY is not set; plaintext reads are compatible but encrypted writes are refused")
	} else {
		slog.Info("at-rest encryption enabled", "key_fingerprint", c.Fingerprint())
	}
	return c, nil
}

// Enabled 报告当前实例是否配置了 at-rest 密钥。
func Enabled() bool {
	return Current() != nil
}

// KeyFingerprint 返回当前密钥指纹（未配置时为空）。
func KeyFingerprint() string {
	return Current().Fingerprint()
}

// EncryptString 使用进程级默认实例加密，未配置时返回 ErrNoKey。
func EncryptString(plaintext string) (string, error) {
	return Current().EncryptString(plaintext)
}

// DecryptString 使用进程级默认实例解密；未配置时明文原样返回、密文返回 ErrNoKey。
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
