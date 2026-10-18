//go:build unit

package admin

import (
	"encoding/json"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/handler/dto"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

// Simple mode hides composite groups. Everything else must stay in the full
// dto.Group shape: the account editor feeds these objects into GroupSelector,
// which reads rate_multiplier, subscription_type and account_count. A trimmed
// reference object silently blanks the picker rows and breaks rendering.
func TestAccountWithConcurrencySimpleModeKeepsFullGroupShape(t *testing.T) {
	visible := &dto.Group{
		ID:                        7,
		Name:                      "visible",
		Platform:                  service.PlatformOpenAI,
		Status:                    "active",
		RateMultiplier:            0.8,
		SubscriptionType:          "subscription",
		LongContextPricingEnabled: true,
	}
	composite := &dto.Group{ID: 9, Name: "composite", Platform: service.PlatformComposite, Status: "active"}
	account := &dto.Account{
		ID:       1,
		Name:     "acc",
		Platform: service.PlatformOpenAI,
		Type:     "apikey",
		GroupIDs: []int64{7, 9},
		Groups:   []*dto.Group{visible, composite},
		AccountGroups: []dto.AccountGroup{
			{AccountID: 1, GroupID: 7, Group: visible},
			{AccountID: 1, GroupID: 9, Group: composite},
		},
	}

	payload, err := json.Marshal(AccountWithConcurrency{Account: account, simpleMode: true})
	require.NoError(t, err)

	var decoded map[string]any
	require.NoError(t, json.Unmarshal(payload, &decoded))

	groups, ok := decoded["groups"].([]any)
	require.True(t, ok, "groups must be an array")
	require.Len(t, groups, 1, "composite groups are hidden in simple mode")

	group, ok := groups[0].(map[string]any)
	require.True(t, ok)
	require.EqualValues(t, 7, group["id"])
	require.Contains(t, group, "rate_multiplier", "GroupSelector renders the rate label from this field")
	require.Contains(t, group, "subscription_type", "GroupSelector renders the subscription label from this field")
	require.EqualValues(t, 0.8, group["rate_multiplier"])

	groupIDs, ok := decoded["group_ids"].([]any)
	require.True(t, ok)
	require.Len(t, groupIDs, 1, "composite group id is filtered out")
	require.EqualValues(t, 7, groupIDs[0])

	accountGroups, ok := decoded["account_groups"].([]any)
	require.True(t, ok)
	require.Len(t, accountGroups, 1, "composite account group is filtered out")
	entry, ok := accountGroups[0].(map[string]any)
	require.True(t, ok)
	entryGroup, ok := entry["group"].(map[string]any)
	require.True(t, ok)
	require.Contains(t, entryGroup, "rate_multiplier", "account_groups[].group must keep the full shape too")
}

// Non-simple deployments must keep the untouched payload.
func TestAccountWithConcurrencyStandardModeKeepsEverything(t *testing.T) {
	composite := &dto.Group{ID: 9, Name: "composite", Platform: service.PlatformComposite, Status: "active"}
	account := &dto.Account{
		ID:       1,
		Platform: service.PlatformOpenAI,
		Type:     "apikey",
		GroupIDs: []int64{9},
		Groups:   []*dto.Group{composite},
	}
	payload, err := json.Marshal(AccountWithConcurrency{Account: account})
	require.NoError(t, err)

	var decoded map[string]any
	require.NoError(t, json.Unmarshal(payload, &decoded))
	groups, ok := decoded["groups"].([]any)
	require.True(t, ok)
	require.Len(t, groups, 1, "standard mode keeps composite groups")
}
