package service

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestAccountRuntimeQualityScoresAndDecays(t *testing.T) {
	now := time.Unix(1_700_000_000, 0)
	quality := NewAccountRuntimeQuality()
	quality.now = func() time.Time { return now }

	require.InDelta(t, 1.0, quality.Score(1, 0), 1e-9, "unknown account at zero load scores 1")

	quality.ObserveFailure(1)
	require.InDelta(t, accountRuntimeQualityAlpha, quality.ErrorRate(1), 1e-9)
	require.Less(t, quality.Score(1, 0), 1.0)

	// Five minutes is one half-life.
	now = now.Add(accountRuntimeQualityHalfLife)
	require.InDelta(t, accountRuntimeQualityAlpha/2, quality.ErrorRate(1), 1e-9, "error rate decays over time")

	quality.ObserveSuccess(1)
	require.Less(t, quality.ErrorRate(1), accountRuntimeQualityAlpha/2, "a success pulls the rate down")
}

func TestAccountRuntimeQualityLoadAffectsScore(t *testing.T) {
	quality := NewAccountRuntimeQuality()
	require.InDelta(t, 1.0, quality.Score(1, 0), 1e-9)
	require.InDelta(t, 0.5, quality.Score(1, 100), 1e-9, "full load halves the score")
}

func candidateFor(accountID int64, loadRate int, lastUsed time.Time) accountWithLoad {
	return accountWithLoad{
		account:  &Account{ID: accountID, LastUsedAt: &lastUsed},
		loadInfo: &AccountLoadInfo{AccountID: accountID, LoadRate: loadRate},
	}
}

func TestSelectByRuntimeQualityPrefersHealthyCandidate(t *testing.T) {
	runtime := NewAccountRuntimeQuality()
	runtime.ObserveFailure(1)
	svc := &GatewayService{rateLimitService: &RateLimitService{accountRuntime: runtime}}
	now := time.Now()
	candidates := []accountWithLoad{
		candidateFor(1, 0, now.Add(-time.Hour)), // unhealthy but older
		candidateFor(2, 0, now),
	}
	selected := svc.selectByRuntimeQuality(candidates, false)
	require.NotNil(t, selected)
	require.Equal(t, int64(2), selected.account.ID, "the healthy peer must win inside the same priority/load layer")
}

func TestSelectByRuntimeQualityTiesFallBackToLRU(t *testing.T) {
	svc := &GatewayService{rateLimitService: &RateLimitService{accountRuntime: NewAccountRuntimeQuality()}}
	now := time.Now()
	older, newer := now.Add(-time.Hour), now
	candidates := []accountWithLoad{
		candidateFor(1, 0, newer),
		candidateFor(2, 0, older),
	}
	selected := svc.selectByRuntimeQuality(candidates, false)
	require.NotNil(t, selected)
	require.Equal(t, int64(2), selected.account.ID, "equal scores keep the existing LRU choice")
}

func TestSelectByRuntimeQualityWithoutStateUsesLRU(t *testing.T) {
	svc := &GatewayService{}
	now := time.Now()
	older, newer := now.Add(-time.Hour), now
	candidates := []accountWithLoad{
		candidateFor(1, 0, newer),
		candidateFor(2, 0, older),
	}
	selected := svc.selectByRuntimeQuality(candidates, false)
	require.NotNil(t, selected)
	require.Equal(t, int64(2), selected.account.ID)
}
