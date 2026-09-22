//go:build unit

package service

import (
	"context"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/stretchr/testify/require"
)

func TestStickySessionShouldYieldToFailover_MissingAccount(t *testing.T) {
	svc := &OpenAIGatewayService{accountRepo: schedulerTestOpenAIAccountRepo{}}
	require.True(t, svc.stickySessionShouldYieldToFailover(context.Background(), 29396))
}

func TestOpenAIGatewayService_SelectAccountWithScheduler_SessionStickyMissingAccountClearsBinding(t *testing.T) {
	resetOpenAIAdvancedSchedulerSettingCacheForTest()
	defer resetOpenAIAdvancedSchedulerSettingCacheForTest()

	ctx := context.Background()
	groupID := int64(36001)
	live := Account{
		ID:          29159,
		Platform:    PlatformOpenAI,
		Type:        AccountTypeAPIKey,
		Status:      StatusActive,
		Schedulable: true,
		Concurrency: 1,
		Priority:    5,
		GroupIDs:    []int64{groupID},
		AccountGroups: []AccountGroup{
			{AccountID: 29159, GroupID: groupID},
		},
	}
	cache := &schedulerTestGatewayCache{sessionBindings: map[string]int64{"openai:session_hash_deleted_account": 29396}}
	cfg := &config.Config{}
	cfg.Gateway.Scheduling.LoadBatchEnabled = false
	svc := &OpenAIGatewayService{
		accountRepo:        schedulerGroupAwareOpenAIAccountRepo{schedulerTestOpenAIAccountRepo{accounts: []Account{live}}},
		cache:              cache,
		cfg:                cfg,
		concurrencyService: NewConcurrencyService(schedulerTestConcurrencyCache{}),
	}

	selection, decision, err := svc.SelectAccountWithScheduler(ctx, &groupID, "", "session_hash_deleted_account", "gpt-5.1", nil, OpenAIUpstreamTransportAny, false)
	require.NoError(t, err)
	require.NotNil(t, selection)
	require.NotNil(t, selection.Account)
	require.Equal(t, int64(29159), selection.Account.ID)
	require.False(t, decision.StickySessionHit)
	require.Equal(t, 1, cache.deletedSessions["openai:session_hash_deleted_account"])
	if selection.ReleaseFunc != nil {
		selection.ReleaseFunc()
	}
}
