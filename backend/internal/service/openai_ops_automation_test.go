package service

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestAutomationSchedulerKeepsProtocolStrataAndStickyOwnership(t *testing.T) {
	engine := NewOpsAutomationService(nil, nil, nil, nil)
	cfg := DefaultOpsAutomationConfig()
	cfg.Ranking.Enabled = true
	cfg.Ranking.GroupIDs = []int64{8}
	cfg.Ranking.Mode = "experience"
	engine.config.Store(cfg)
	group := int64(8)
	accounts := []*Account{
		{ID: 1, Platform: PlatformOpenAI, Type: AccountTypeAPIKey, Priority: 1},
		{ID: 2, Platform: PlatformDeepseek, Type: AccountTypeAPIKey, Priority: 1, Credentials: map[string]any{"api_protocol": APIProtocolResponses}},
		{ID: 3, Platform: PlatformOpenAI, Type: AccountTypeAPIKey, Priority: 1},
		{ID: 4, Platform: PlatformOpenAI, Type: AccountTypeAPIKey, Priority: 2},
		{ID: 5, Platform: PlatformDeepseek, Type: AccountTypeAPIKey, Priority: 1},
	}
	engine.ranks[automationRankKey(group, "m")] = automationRankEntry{expires: time.Now().Add(time.Minute), signals: map[int64]AutomationRankSignal{
		1: {Samples: 100, P90MS: 30000}, 2: {Samples: 100, P90MS: 10}, 3: {Samples: 100, P90MS: 100}, 4: {Samples: 100, P90MS: 1}, 5: {Samples: 100, P90MS: 1},
	}}
	gw := &OpenAIGatewayService{opsAutomation: engine}
	ordered := gw.rankAutomationPool(&group, "m", accounts, true, true, true)
	require.Equal(t, []int64{3, 2, 1, 4, 5}, []int64{ordered[0].ID, ordered[1].ID, ordered[2].ID, ordered[3].ID, ordered[4].ID})
	scheduler := &defaultOpenAIAccountScheduler{service: gw}
	items := []openAIAccountCandidateScore{{account: accounts[0]}, {account: accounts[2]}}
	for _, req := range []OpenAIAccountScheduleRequest{
		{GroupID: &group, RequestedModel: "m", StickyAccountID: 1},
		{GroupID: &group, RequestedModel: "m", StickyPreviousAccountID: 1},
		{GroupID: &group, RequestedModel: "m", PreviousResponseID: "resp_fixture"},
	} {
		require.Equal(t, items, scheduler.rankAutomationCandidates(req, items))
	}
}
