package service

import (
	"crypto/sha256"
	"encoding/hex"
)

const (
	// APIKeyHashHexLen 是 SHA-256 hex 摘要的固定长度，也是 api_keys.key_hash /
	// api_key_hashes.key_hash 的列宽（CHAR(64)）。
	APIKeyHashHexLen = 64
	// APIKeyDisplayPrefixLen 是落库展示前缀与对外脱敏前缀的长度。
	// 迁移 327 的 key_prefix VARCHAR(16) 留了余量，当前固定取前 8 位
	// （sk-xxxxx），与 #464 的 key_prefix DTO 语义一致。
	APIKeyDisplayPrefixLen = 8
)

// HashAPIKey 返回 API Key 明文的 SHA-256 小写 hex 摘要。
//
// 为什么是「不加盐的 SHA-256」：
//   - 生成的 key 本体是 256 bit CSPRNG 随机值（GenerateKey 用 crypto/rand
//     32 字节），根本不存在可枚举的口令空间，离线字典/彩虹表攻击不成立；
//   - 鉴权是等值查询（WHERE key_hash = sha256(presented)），每行独立加盐会
//     让索引失效、必须全表扫描 + 逐行比对，热路径不可接受；
//   - 不加 pepper 意味着不需要额外密钥托管：一旦引入 pepper，轮换 pepper
//     就等于所有 key 全部失效，必须和回填/清理同版本协调。
//
// 唯一需要接受的是：如果用户自定义了低熵 key（ValidateCustomKey 允许
// 16 位以上的字母数字），哈希后可被字典攻击还原。这一点和「明文落库」
// 相比仍是净收益，且轮换即可解决。
func HashAPIKey(key string) string {
	sum := sha256.Sum256([]byte(key))
	return hex.EncodeToString(sum[:])
}

// apiKeyDisplayMinPlaintextLen 与 handler 层脱敏规则保持一致：
// 短于该长度的凭据不展示任何可识别前缀（避免把短 key 大半暴露）。
// 正常 key（sk- + 64 hex / 16 位以上自定义 key）都远长于它。
const apiKeyDisplayMinPlaintextLen = 13

// APIKeyPrefixFromPlaintext 返回展示用前缀（前 8 位）。
func APIKeyPrefixFromPlaintext(key string) string {
	if len(key) < apiKeyDisplayMinPlaintextLen {
		return ""
	}
	return key[:APIKeyDisplayPrefixLen]
}

// IsAPIKeyHash 判断字符串是否形如 sha256 hex 摘要（64 个小写/大写 hex）。
// 仅用于迁移期判断「这个值是不是已经哈希过」，不用作安全边界。
func IsAPIKeyHash(value string) bool {
	if len(value) != APIKeyHashHexLen {
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
