package service

import (
	"crypto/sha256"
	"encoding/hex"
	"strings"
)

const (
	// RedeemCodeHashHexLen 是 SHA-256 hex 摘要的固定长度，也是 redeem_codes.code_hash /
	// redeem_code_hashes.code_hash 的列宽（CHAR(64)）。
	RedeemCodeHashHexLen = 64
	// RedeemCodeDisplayPrefixLen 是落库展示前缀的长度。
	// 迁移 328 的 code_prefix VARCHAR(16) 留了余量，当前固定取前 8 位。
	RedeemCodeDisplayPrefixLen = 8
	// redeemCodeMaskVisibleLen 是掩码里保留的可见字符数（前 4 位）。
	// 32 位 hex 卡密暴露 16 bit 不降低可枚举性；短的自定义卡密也不会被整段显示。
	redeemCodeMaskVisibleLen = 4
	// redeemCodeMaskStars 是掩码固定用 4 个星号，避免按长度泄露卡密长度。
	redeemCodeMaskStars = "****"
)

// NormalizeRedeemCode 统一卡密输入形态：去掉首尾空白。
//
// 不改变大小写：卡密是区分大小写的凭据（历史自定义卡密可能是混合大小写），
// 做大小写归一会让两张不同的卡在哈希后相撞。
func NormalizeRedeemCode(code string) string {
	return strings.TrimSpace(code)
}

// HashRedeemCode 返回卡密明文的 SHA-256 小写 hex 摘要。
//
// 为什么与 API Key 一样是「不加盐的 SHA-256」：
//   - 随机生成的卡密是 128 bit CSPRNG（GenerateRedeemCode 取 crypto/rand 16 字节），
//     不存在可枚举的口令空间；自定义卡密最低 3 字符的场景由管理员自担风险，
//     这一点和「明文落库」相比仍是净收益；
//   - 兑换是等值查询（WHERE code_hash = sha256(输入)），每行加盐会让索引失效；
//   - 不引入 pepper：轮换 pepper 等于全部卡密失效，必须与回填/清理同版本协调。
//
// 输入先归一（去首尾空白），与创建时 CreateCode 的 TrimSpace 对齐。
func HashRedeemCode(code string) string {
	sum := sha256.Sum256([]byte(NormalizeRedeemCode(code)))
	return hex.EncodeToString(sum[:])
}

// RedeemCodePrefixFromPlaintext 返回展示用前缀（前 8 位）。
//
// 短于 8 位的自定义卡密不回前缀：截断后会等于（或接近）完整卡密，反而泄露。
// 这类卡密在后台显示为纯掩码，只能按完整卡密搜索到。
func RedeemCodePrefixFromPlaintext(code string) string {
	normalized := NormalizeRedeemCode(code)
	if len(normalized) < RedeemCodeDisplayPrefixLen {
		return ""
	}
	return normalized[:RedeemCodeDisplayPrefixLen]
}

// IsRedeemCodeHash 判断字符串是否形如 sha256 hex 摘要（64 个十六进制字符）。
// 仅用于迁移期判断「这个值是不是已经哈希过」，不用作安全边界。
func IsRedeemCodeHash(value string) bool {
	if len(value) != RedeemCodeHashHexLen {
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

// MaskRedeemCodePrefix 把落库前缀转成可展示的掩码（前 4 位 + ****）。
// 没有前缀（短自定义卡密 / 尚未回填）时返回纯星号，绝不回退到明文。
func MaskRedeemCodePrefix(prefix string) string {
	prefix = strings.TrimSpace(prefix)
	if len(prefix) >= redeemCodeMaskVisibleLen {
		return prefix[:redeemCodeMaskVisibleLen] + redeemCodeMaskStars
	}
	return redeemCodeMaskStars
}

// redeemCodeLogRefHashLen 是日志引用里保留的哈希字符数（48 bit），
// 足以在同一批次内区分不同的卡，又不足以做任何反推。
const redeemCodeLogRefHashLen = 12

// RedeemCodeLogRef 返回日志 / 审计里唯一允许出现的卡密引用：掩码 + 哈希短片段。
//
// 为什么不用明文：卡密是可兑现凭据，明文进日志等于换一个库继续泄露。
// 为什么保留哈希片段：排查「同一张卡为什么失败两次」需要对得上号，
// 而 redeem_code_hashes / redeem_codes.code_hash 里存的就是同一个值。
func RedeemCodeLogRef(code string) string {
	hash := HashRedeemCode(code)
	short := hash
	if len(short) > redeemCodeLogRefHashLen {
		short = short[:redeemCodeLogRefHashLen]
	}
	return MaskRedeemCodePrefix(RedeemCodePrefixFromPlaintext(code)) + ":" + short
}

// redeemCodeLogRef 是 RedeemCodeLogRef 的包内别名，保持日志调用点简短。
func redeemCodeLogRef(code string) string { return RedeemCodeLogRef(code) }
