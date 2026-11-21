//go:build unit

package repository

import (
	"context"
	"testing"
	"time"

	dbent "github.com/Wei-Shaw/sub2api/ent"
	"github.com/Wei-Shaw/sub2api/ent/redeemcode"
	"github.com/Wei-Shaw/sub2api/internal/pkg/pagination"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

func newRedeemCodeHashRepo(t *testing.T) (*redeemCodeRepository, *dbent.Client) {
	t.Helper()
	return newRedeemCodePaymentBalanceRepo(t)
}

func redeemCodeHashLedgerCount(t *testing.T, client *dbent.Client, codeHash string) int {
	t.Helper()
	rows, err := client.QueryContext(context.Background(), `SELECT COUNT(*) FROM redeem_code_hashes WHERE code_hash = $1`, codeHash)
	require.NoError(t, err)
	defer func() { _ = rows.Close() }()
	require.True(t, rows.Next())
	var count int
	require.NoError(t, rows.Scan(&count))
	require.NoError(t, rows.Err())
	return count
}

// TestRedeemCodeRepositoryCreateStoresOnlyHash 覆盖交付要求 1/2：
// 生成写入数据库的只有 sha256 哈希与前缀，明文列必须是 NULL。
func TestRedeemCodeRepositoryCreateStoresOnlyHash(t *testing.T) {
	repo, client := newRedeemCodeHashRepo(t)
	ctx := context.Background()
	plain := "0123456789ABCDEF0123456789ABCDEF"

	code := &service.RedeemCode{
		Code:   plain,
		Type:   service.RedeemTypeBalance,
		Value:  10,
		Status: service.StatusUnused,
	}
	require.NoError(t, repo.Create(ctx, code))
	require.NotZero(t, code.ID)

	stored, err := client.RedeemCode.Query().Where(redeemcode.IDEQ(code.ID)).Only(ctx)
	require.NoError(t, err)
	require.Empty(t, stored.Code, "plaintext code column must stay NULL for new rows")
	require.Equal(t, service.HashRedeemCode(plain), stored.CodeHash)
	require.Equal(t, "01234567", stored.CodePrefix)
	require.Equal(t, 1, redeemCodeHashLedgerCount(t, client, stored.CodeHash), "hash ledger must be written in the same transaction")

	// 内存对象仍然保留明文（生成回执要返回一次）。
	require.Equal(t, plain, code.Code)
}

func TestRedeemCodeRepositoryCreateBatchStoresOnlyHashAndLedger(t *testing.T) {
	repo, client := newRedeemCodeHashRepo(t)
	ctx := context.Background()

	const count = 5
	codes := make([]service.RedeemCode, 0, count)
	for i := 0; i < count; i++ {
		code, err := service.GenerateRedeemCode()
		require.NoError(t, err)
		codes = append(codes, service.RedeemCode{
			Code:   code,
			Type:   service.RedeemTypeBalance,
			Value:  1,
			Status: service.StatusUnused,
		})
	}
	require.NoError(t, repo.CreateBatch(ctx, codes))

	stored, err := client.RedeemCode.Query().Order(dbent.Asc(redeemcode.FieldID)).All(ctx)
	require.NoError(t, err)
	require.Len(t, stored, count)

	seen := make(map[string]struct{}, count)
	for i := range stored {
		require.Empty(t, stored[i].Code, "batch insert must not persist plaintext")
		require.NotEmpty(t, stored[i].CodeHash)
		require.Len(t, stored[i].CodeHash, service.RedeemCodeHashHexLen)
		require.Equal(t, codes[i].CodeHash, stored[i].CodeHash)
		require.Equal(t, codes[i].Code[:8], stored[i].CodePrefix)
		_, duplicate := seen[stored[i].CodeHash]
		require.False(t, duplicate)
		seen[stored[i].CodeHash] = struct{}{}
		require.Equal(t, 1, redeemCodeHashLedgerCount(t, client, stored[i].CodeHash))
	}

	var ledgerTotal int
	rows, err := client.QueryContext(ctx, `SELECT COUNT(*) FROM redeem_code_hashes`)
	require.NoError(t, err)
	require.True(t, rows.Next())
	require.NoError(t, rows.Scan(&ledgerTotal))
	require.NoError(t, rows.Close())
	require.Equal(t, count, ledgerTotal)
}

// TestRedeemCodeRepositoryGetByCodeHitsHashAndFallsBackToLegacyPlaintext 覆盖要求 3：
// 兑换按 sha256(输入) 命中；历史明文行（尚未回填）走明文回退。
func TestRedeemCodeRepositoryGetByCodeHitsHashAndFallsBackToLegacyPlaintext(t *testing.T) {
	repo, client := newRedeemCodeHashRepo(t)
	ctx := context.Background()

	plain := "FEEDFACECAFEBEEFFEEDFACECAFEBEEF"
	code := &service.RedeemCode{Code: plain, Type: service.RedeemTypeBalance, Value: 10, Status: service.StatusUnused}
	require.NoError(t, repo.Create(ctx, code))

	found, err := repo.GetByCode(ctx, plain)
	require.NoError(t, err)
	require.Equal(t, code.ID, found.ID)
	require.Equal(t, service.HashRedeemCode(plain), found.CodeHash)
	require.Empty(t, found.Code, "hash rows expose no plaintext even to internal callers")

	found, err = repo.GetByCode(ctx, "  "+plain+"  ")
	require.NoError(t, err, "lookup normalizes surrounding whitespace like creation does")
	require.Equal(t, code.ID, found.ID)

	// 尚未回填哈希的历史行：只有明文列。
	legacy, err := client.RedeemCode.Create().
		SetCode("legacy-plaintext-code").
		SetType(service.RedeemTypeBalance).
		SetValue(1).
		SetStatus(service.StatusUnused).
		Save(ctx)
	require.NoError(t, err)

	legacyFound, err := repo.GetByCode(ctx, "legacy-plaintext-code")
	require.NoError(t, err)
	require.Equal(t, legacy.ID, legacyFound.ID)
	require.Empty(t, legacyFound.CodeHash)

	_, err = repo.GetByCode(ctx, "no-such-code")
	require.ErrorIs(t, err, service.ErrRedeemCodeNotFound)
	_, err = repo.GetByCode(ctx, "   ")
	require.ErrorIs(t, err, service.ErrRedeemCodeNotFound)
}

// TestRedeemCodeRepositorySearchUsesHashPrefixAndLegacyPlaintext 覆盖要求 4：
// 后台搜索不能再依赖明文列，必须同时支持「完整卡密 → 哈希」与「前缀」。
func TestRedeemCodeRepositorySearchUsesHashPrefixAndLegacyPlaintext(t *testing.T) {
	repo, client := newRedeemCodeHashRepo(t)
	ctx := context.Background()

	first := "AAAABBBBCCCCDDDDEEEEFFFF00001111"
	second := "11110000FFFFEEEEDDDDCCCCBBBBAAAA"
	for _, plain := range []string{first, second} {
		require.NoError(t, repo.Create(ctx, &service.RedeemCode{
			Code: plain, Type: service.RedeemTypeBalance, Value: 1, Status: service.StatusUnused,
		}))
	}
	_, err := client.RedeemCode.Create().
		SetCode("legacy-searchable").
		SetType(service.RedeemTypeBalance).
		SetValue(1).
		SetStatus(service.StatusUnused).
		Save(ctx)
	require.NoError(t, err)

	params := pagination.PaginationParams{Page: 1, PageSize: 50}

	byFullCode, _, err := repo.ListWithFilters(ctx, params, "", "", first)
	require.NoError(t, err)
	require.Len(t, byFullCode, 1)
	require.Equal(t, "AAAABBBB", byFullCode[0].CodePrefix)

	byPrefix, _, err := repo.ListWithFilters(ctx, params, "", "", "AAAABBBB")
	require.NoError(t, err)
	require.Len(t, byPrefix, 1)
	require.Equal(t, first[:8], byPrefix[0].CodePrefix)

	byLegacyPlaintext, _, err := repo.ListWithFilters(ctx, params, "", "", "legacy-searchable")
	require.NoError(t, err)
	require.Len(t, byLegacyPlaintext, 1)
	require.Equal(t, "legacy-searchable", byLegacyPlaintext[0].Code)

	// 子串查询是「逐字符确认卡密片段」的 oracle（#489 审计 B-2 第 2 条），
	// 哈希化之后必须删除：只允许完整卡密 / 前缀 / 完整历史明文命中。
	bySubstring, _, err := repo.ListWithFilters(ctx, params, "", "", "legacy-search")
	require.NoError(t, err)
	require.Empty(t, bySubstring, "plaintext substring search must be gone")

	none, _, err := repo.ListWithFilters(ctx, params, "", "", "ZZZZZZZZ")
	require.NoError(t, err)
	require.Empty(t, none)
}

// TestRedeemCodeRepositoryUpdateKeepsPlaintextColumnNull 覆盖要求 6：
// 更新路径不得把明文写回（否则回填过的行会重新长出明文卡密）。
func TestRedeemCodeRepositoryUpdateKeepsPlaintextColumnNull(t *testing.T) {
	repo, client := newRedeemCodeHashRepo(t)
	ctx := context.Background()

	plain := "ABCDEF0123456789ABCDEF0123456789"
	code := &service.RedeemCode{Code: plain, Type: service.RedeemTypeBalance, Value: 10, Status: service.StatusUnused}
	require.NoError(t, repo.Create(ctx, code))
	require.NotEmpty(t, code.CodeHash)

	code.Status = service.StatusExpired
	code.Notes = "operator note"
	require.NoError(t, repo.Update(ctx, code))

	stored, err := client.RedeemCode.Query().Where(redeemcode.IDEQ(code.ID)).Only(ctx)
	require.NoError(t, err)
	require.Equal(t, service.StatusExpired, stored.Status)
	require.Equal(t, "operator note", derefString(stored.Notes))
	require.Empty(t, stored.Code, "update must never rewrite the plaintext credential")
	require.Equal(t, service.HashRedeemCode(plain), stored.CodeHash)
}

func TestRedeemCodeRepositoryDeleteRemovesHashLedgerRow(t *testing.T) {
	repo, client := newRedeemCodeHashRepo(t)
	ctx := context.Background()

	code := &service.RedeemCode{
		Code: "FFFF0000111122223333444455556666", Type: service.RedeemTypeBalance, Value: 1, Status: service.StatusUnused,
	}
	require.NoError(t, repo.Create(ctx, code))
	require.Equal(t, 1, redeemCodeHashLedgerCount(t, client, code.CodeHash))

	require.NoError(t, repo.Delete(ctx, code.ID))
	require.Equal(t, 0, redeemCodeHashLedgerCount(t, client, code.CodeHash), "ledger row must cascade-delete with the code")
}

func TestRedeemCodeRepositoryCreateRejectsRowsWithoutHash(t *testing.T) {
	repo, _ := newRedeemCodeHashRepo(t)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	require.Error(t, repo.Create(ctx, nil))
	require.Error(t, repo.Create(ctx, &service.RedeemCode{
		Type: service.RedeemTypeBalance, Value: 1, Status: service.StatusUnused,
	}), "a code with neither plaintext nor hash must not be persisted")
}
