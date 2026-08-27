package service

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

type redeemGenerationRepo struct {
	RedeemCodeRepository
	createCalls int
	batchCalls  int
	codes       []RedeemCode
}

func (r *redeemGenerationRepo) Create(context.Context, *RedeemCode) error {
	r.createCalls++
	return nil
}

func (r *redeemGenerationRepo) CreateBatch(_ context.Context, codes []RedeemCode) error {
	r.batchCalls++
	now := time.Now().UTC()
	for i := range codes {
		codes[i].ID = int64(i + 1)
		codes[i].CreatedAt = now
	}
	r.codes = append([]RedeemCode(nil), codes...)
	return nil
}

func TestAdminGenerateRedeemCodesUsesSingleBatchAtLimit(t *testing.T) {
	repo := &redeemGenerationRepo{}
	svc := &adminServiceImpl{redeemCodeRepo: repo}

	codes, err := svc.GenerateRedeemCodes(context.Background(), &GenerateRedeemCodesInput{
		Count: MaxRedeemCodesPerBatch,
		Type:  RedeemTypeBalance,
		Value: 10,
	})
	require.NoError(t, err)
	require.Len(t, codes, MaxRedeemCodesPerBatch)
	require.Equal(t, 0, repo.createCalls)
	require.Equal(t, 1, repo.batchCalls)
	require.Len(t, repo.codes, MaxRedeemCodesPerBatch)
	require.NotZero(t, codes[0].ID)
	require.False(t, codes[0].CreatedAt.IsZero())

	seen := make(map[string]struct{}, len(codes))
	for i := range codes {
		require.NotEmpty(t, codes[i].Code)
		_, duplicate := seen[codes[i].Code]
		require.False(t, duplicate)
		seen[codes[i].Code] = struct{}{}
	}
}

func TestAdminGenerateRedeemCodesRejectsCountAboveLimit(t *testing.T) {
	repo := &redeemGenerationRepo{}
	svc := &adminServiceImpl{redeemCodeRepo: repo}

	_, err := svc.GenerateRedeemCodes(context.Background(), &GenerateRedeemCodesInput{
		Count: MaxRedeemCodesPerBatch + 1,
		Type:  RedeemTypeBalance,
		Value: 10,
	})
	require.ErrorContains(t, err, "cannot generate more than 1000 codes at once")
	require.Zero(t, repo.createCalls)
	require.Zero(t, repo.batchCalls)
}
