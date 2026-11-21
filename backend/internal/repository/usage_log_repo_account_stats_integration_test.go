//go:build integration

package repository

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

// The merged account-stats aggregate must keep AVG(duration_ms) semantics while
// folding duration into the daily scan: rows with a NULL duration stay out of
// the denominator, and the single GROUPING SETS statement still returns the
// model and both endpoint breakdowns.
func TestUsageLogRepositoryAccountUsageStatsDurationAndBreakdown(t *testing.T) {
	ctx := context.Background()
	client := testEntClient(t)
	repo := newUsageLogRepositoryWithSQL(client, integrationDB)

	suffix := time.Now().UnixNano()
	user := mustCreateUser(t, client, &service.User{Email: fmt.Sprintf("account-stats-duration-%d@example.com", suffix)})
	apiKey := mustCreateApiKey(t, client, &service.APIKey{UserID: user.ID, Key: fmt.Sprintf("it-accstats-%d", suffix), Name: "k"})
	account := mustCreateAccount(t, client, &service.Account{Name: fmt.Sprintf("acc-account-stats-duration-%d", suffix)})

	base := time.Date(2025, 2, 10, 0, 0, 0, 0, time.UTC)
	firstDuration, secondDuration := 1000, 3000
	logs := []*service.UsageLog{
		{
			UserID: user.ID, APIKeyID: apiKey.ID, AccountID: account.ID,
			Model: "claude-3-opus", InputTokens: 100, OutputTokens: 200,
			TotalCost: 0.5, ActualCost: 0.4, DurationMs: &firstDuration,
			CreatedAt: base.Add(12 * time.Hour),
		},
		{
			UserID: user.ID, APIKeyID: apiKey.ID, AccountID: account.ID,
			Model: "claude-3-sonnet", InputTokens: 50, OutputTokens: 100,
			TotalCost: 0.2, ActualCost: 0.15, DurationMs: &secondDuration,
			CreatedAt: base.Add(36 * time.Hour),
		},
		{
			// NULL duration: must not count towards the average.
			UserID: user.ID, APIKeyID: apiKey.ID, AccountID: account.ID,
			Model:     "claude-3-opus",
			CreatedAt: base.Add(13 * time.Hour),
		},
	}
	for _, log := range logs {
		_, err := repo.Create(ctx, log)
		require.NoError(t, err)
	}

	resp, err := repo.GetAccountUsageStats(ctx, account.ID, base, base.Add(72*time.Hour))
	require.NoError(t, err)
	require.Len(t, resp.History, 2, "two distinct days")
	require.EqualValues(t, 3, resp.Summary.TotalRequests)
	require.EqualValues(t, 450, resp.Summary.TotalTokens)
	require.Equal(t, 2000.0, resp.Summary.AvgDurationMs, "(1000+3000)/2 with the NULL row ignored")
	require.Len(t, resp.Models, 2)
	require.Len(t, resp.Endpoints, 1)
	require.Len(t, resp.UpstreamEndpoints, 1)
}
