package service

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestHashRedeemCodeIsStableTrimmedAndCaseSensitive(t *testing.T) {
	const code = "A1B2C3D4E5F60718293A4B5C6D7E8F90"

	hash := HashRedeemCode(code)
	require.Len(t, hash, RedeemCodeHashHexLen)
	require.True(t, IsRedeemCodeHash(hash))
	require.Equal(t, hash, HashRedeemCode(code), "hash must be deterministic")
	require.Equal(t, hash, HashRedeemCode("  "+code+"  "), "hash must normalize surrounding whitespace")
	require.NotEqual(t, hash, HashRedeemCode(strings.ToLower(code)), "codes are case sensitive credentials")
	require.NotEqual(t, hash, HashRedeemCode(code+"0"))

	// 32 位 hex 卡密暴露 8 位前缀 = 32 bit，不影响 128 bit 强度的可枚举性。
	require.Equal(t, "A1B2C3D4", RedeemCodePrefixFromPlaintext(code))
	require.Equal(t, "A1B2****", MaskRedeemCodePrefix(RedeemCodePrefixFromPlaintext(code)))
	require.False(t, strings.Contains(hash, code))
}

func TestRedeemCodePrefixAndMaskNeverLeakShortOrFullCodes(t *testing.T) {
	// 短自定义卡密（创建接口允许最短 3 位）：不回前缀，掩码为纯星号。
	for _, short := range []string{"abc", "abc1234", ""} {
		require.Empty(t, RedeemCodePrefixFromPlaintext(short), "short code %q must not yield a prefix", short)
		require.Equal(t, "****", MaskRedeemCodePrefix(RedeemCodePrefixFromPlaintext(short)))
	}

	// 恰好 8 位：前缀是完整卡密，但掩码只保留 4 位。
	require.Equal(t, "abcd1234", RedeemCodePrefixFromPlaintext("abcd1234"))
	require.Equal(t, "abcd****", MaskRedeemCodePrefix("abcd1234"))
}

func TestRedeemCodeMaskedCodeNeverReturnsPlaintext(t *testing.T) {
	plain := "AAAABBBBCCCCDDDDEEEEFFFF00001111"

	// 生成后的内存对象：有明文、有哈希、有前缀 → 对外只能是掩码。
	inMemory := RedeemCode{Code: plain}
	inMemory.EnsureCodeHash()
	require.Equal(t, HashRedeemCode(plain), inMemory.CodeHash)
	require.Equal(t, "AAAA****", inMemory.MaskedCode())
	require.NotContains(t, inMemory.MaskedCode(), plain)
	require.Equal(t, "AAAA****", inMemory.DisplayCode())

	// 从库里读回来的行：code 为 NULL，只有哈希与前缀。
	fromDB := RedeemCode{ID: 1, CodeHash: HashRedeemCode(plain), CodePrefix: "AAAABBBB"}
	require.Equal(t, "AAAA****", fromDB.MaskedCode())
	require.Equal(t, "AAAA****", fromDB.DisplayCode())

	// 尚未回填的历史行：只有明文，也必须掩码（绝不能原样返回）。
	legacy := RedeemCode{ID: 2, Code: plain}
	require.Equal(t, "AAAA****", legacy.MaskedCode())

	// 合成展示行（返利流水）不是凭据，保留原标签。
	synthetic := RedeemCode{ID: -1, Code: "AFF-123", SyntheticCode: true}
	require.Equal(t, "AFF-123", synthetic.DisplayCode())
}

func TestRedeemCodeMatchesCodeCoversHashAndLegacyPlaintext(t *testing.T) {
	plain := "crypto-abc123"

	hashed := RedeemCode{CodeHash: HashRedeemCode(plain)}
	require.True(t, hashed.MatchesCode(plain))
	require.True(t, hashed.MatchesCode("  "+plain+"  "), "matches must normalize whitespace like lookups do")
	require.False(t, hashed.MatchesCode(plain+"x"))

	legacy := RedeemCode{Code: plain}
	require.True(t, legacy.MatchesCode(plain))
	require.False(t, legacy.MatchesCode("other"))

	require.False(t, (&RedeemCode{}).MatchesCode(plain))
}

func TestRedeemCodeLogRefHasNoPlaintext(t *testing.T) {
	plain := "ABCDEF0123456789ABCDEF0123456789"
	ref := RedeemCodeLogRef(plain)
	require.NotContains(t, ref, plain)
	require.True(t, strings.HasPrefix(ref, "ABCD****:"))
	require.Len(t, ref, len("ABCD****:")+redeemCodeLogRefHashLen)
}

func TestAdminGenerateRedeemCodesDerivesHashBeforePersistence(t *testing.T) {
	repo := &redeemGenerationRepo{}
	svc := &adminServiceImpl{redeemCodeRepo: repo}

	codes, err := svc.GenerateRedeemCodes(context.Background(), &GenerateRedeemCodesInput{
		Count: 3,
		Type:  RedeemTypeBalance,
		Value: 10,
	})
	require.NoError(t, err)
	require.Len(t, codes, 3)

	for i := range codes {
		// 明文保留在内存对象里（生成回执要返回一次）。
		require.NotEmpty(t, codes[i].Code)
		// 哈希/前缀必须在服务层就补齐，不能依赖仓储实现。
		require.Equal(t, HashRedeemCode(codes[i].Code), codes[i].CodeHash)
		require.Equal(t, codes[i].Code[:8], codes[i].CodePrefix)
		// 对外展示只能是掩码。
		require.Equal(t, codes[i].Code[:4]+"****", codes[i].MaskedCode())
		require.NotEqual(t, codes[i].Code, codes[i].MaskedCode())
	}

	// 仓储拿到的行与返回行同源：哈希已就位（真实仓储就是靠它只落哈希）。
	require.Len(t, repo.codes, 3)
	for i := range repo.codes {
		require.Equal(t, codes[i].CodeHash, repo.codes[i].CodeHash)
	}
}

func TestRedeemServiceCreateCodeDerivesHashForDeterministicCodes(t *testing.T) {
	repo := &redeemGenerationRepo{}
	svc := NewRedeemService(repo, nil, nil, nil, nil, nil, nil, nil)

	code := &RedeemCode{
		Code:   "  crypto-order-42  ",
		Type:   RedeemTypePaymentBalance,
		Value:  5,
		Status: StatusUnused,
	}
	require.NoError(t, svc.CreateCode(context.Background(), code))
	require.Equal(t, "crypto-order-42", code.Code, "trimmed plaintext stays available to the caller")
	require.Equal(t, HashRedeemCode("crypto-order-42"), code.CodeHash)
	require.Equal(t, "crypto-o", code.CodePrefix)
}

func TestRedeemServiceCreateCodeRejectsEmptyCode(t *testing.T) {
	svc := NewRedeemService(&redeemGenerationRepo{}, nil, nil, nil, nil, nil, nil, nil)
	require.Error(t, svc.CreateCode(context.Background(), &RedeemCode{Code: "   "}))
}

// 确保测试不依赖时钟：过期判断用未来时间，避免时区/边界抖动。
func TestRedeemCodeExpiryUsesProvidedClock(t *testing.T) {
	now := time.Now()
	code := RedeemCode{Status: StatusUnused, ExpiresAt: nil}
	require.True(t, code.CanUse())
	expired := now.Add(-time.Hour)
	code.ExpiresAt = &expired
	require.False(t, code.CanUse())
	require.True(t, code.IsExpiredAt(now))
}
