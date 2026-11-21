package service

import (
	"crypto/rand"
	"encoding/hex"
	"time"
)

const MaxRedeemCodesPerBatch = 1000

type RedeemCode struct {
	ID int64
	// Code 是明文卡密。只允许两种情况非空：
	//  1. 生成/创建流程在内存里刚拿到的原始值（响应里返回一次，写库只落哈希）；
	//  2. 尚未回填哈希的历史行（过渡期回退用）。
	// 任何读接口都不得把 Code 直接返回，必须走 MaskedCode / DisplayPrefix。
	Code string
	// CodeHash 是 sha256(Code) 的小写 hex，库里唯一的凭据本体。
	CodeHash string
	// CodePrefix 是展示用前缀（前 8 位），列表/日志/审计只能用它识别卡密。
	CodePrefix string
	Type       string
	Value      float64
	Status     string
	UsedBy     *int64
	UsedAt     *time.Time
	Notes      string
	CreatedAt  time.Time
	ExpiresAt  *time.Time

	GroupID      *int64
	ValidityDays int

	// SyntheticCode 标记「不是真实卡密、只是凑历史展示行」的记录（当前只有
	// 代理返利流水用 AFF-<id> 合成行）。这类值不是凭据，展示时不需要掩码。
	SyntheticCode bool

	User  *User
	Group *Group
}

// EnsureCodeHash 在内存对象只有明文时补齐哈希与展示前缀。
// 生成 / 创建路径必须先调用它，写库时才只落哈希、不落明文。
func (r *RedeemCode) EnsureCodeHash() {
	if r == nil || r.Code == "" {
		return
	}
	if r.CodeHash == "" {
		r.CodeHash = HashRedeemCode(r.Code)
	}
	if r.CodePrefix == "" {
		r.CodePrefix = RedeemCodePrefixFromPlaintext(r.Code)
	}
}

// DisplayPrefix 返回可用于展示 / 搜索的前缀：优先用落库的 code_prefix；
// 内存里只有明文时（生成 / 创建回执）退化为临时截取。库里没有明文之后，
// 这是后台列表与日志识别「这是哪张卡」的唯一来源。
func (r *RedeemCode) DisplayPrefix() string {
	if r == nil {
		return ""
	}
	if r.CodePrefix != "" {
		return r.CodePrefix
	}
	return RedeemCodePrefixFromPlaintext(r.Code)
}

// MaskedCode 返回对外展示用的掩码（前 4 位 + ****），永远不会返回完整明文。
func (r *RedeemCode) MaskedCode() string {
	if r == nil {
		return ""
	}
	return MaskRedeemCodePrefix(r.DisplayPrefix())
}

// DisplayCode 是 DTO / 导出 / 审计里唯一允许出现的卡密字符串：
// 真实卡密一律掩码；合成展示行（返利流水）保留原值，因为它本来就不是凭据。
func (r *RedeemCode) DisplayCode() string {
	if r == nil {
		return ""
	}
	if r.SyntheticCode {
		return r.Code
	}
	return r.MaskedCode()
}

// MatchesCode 判断给定明文是否就是这张卡。
//
// 过渡期必须同时看两处：已回填 / 新写入的行只有 code_hash；尚未回填的历史行
// 只有明文列。少看任何一处都会让「哈希命中」之外的卡密在支付对账时误判。
func (r *RedeemCode) MatchesCode(plain string) bool {
	if r == nil {
		return false
	}
	if r.CodeHash != "" {
		return r.CodeHash == HashRedeemCode(plain)
	}
	return r.Code != "" && r.Code == NormalizeRedeemCode(plain)
}

func (r *RedeemCode) IsUsed() bool {
	return r.Status == StatusUsed
}

func (r *RedeemCode) IsExpired() bool {
	return r.IsExpiredAt(time.Now())
}

func (r *RedeemCode) IsExpiredAt(now time.Time) bool {
	if r == nil {
		return false
	}
	if r.Status == StatusExpired {
		return true
	}
	return r.Status == StatusUnused && r.ExpiresAt != nil && !r.ExpiresAt.After(now)
}

func (r *RedeemCode) CanUse() bool {
	return r.Status == StatusUnused && !r.IsExpired()
}

func GenerateRedeemCode() (string, error) {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}
