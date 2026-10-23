package dto

import (
	"encoding/json"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

func TestPublicGroupResponsesOmitSearchProviderConfiguration(t *testing.T) {
	t.Parallel()
	baseURL := "https://search-provider.example.test/private-route"
	apiKey := "fixture-search-provider-token"
	group := &service.Group{
		ID: 7, Name: "public-search-group", Platform: service.PlatformOpenAI,
		SearchProviderBaseURL: &baseURL, SearchProviderAPIKey: &apiKey,
	}
	key := service.APIKey{ID: 8, Group: group}
	subscription := service.UserSubscription{ID: 9, Group: group}
	user := &service.User{ID: 2, APIKeys: []service.APIKey{key}, Subscriptions: []service.UserSubscription{subscription}}
	usage := &service.UsageLog{ID: 10, UserID: user.ID, Group: group, APIKey: &key, Subscription: &subscription}

	for name, value := range map[string]any{
		"available group":     GroupFromService(group),
		"shallow group":       GroupFromServiceShallow(group),
		"API key":             APIKeyFromService(&key),
		"subscription":        UserSubscriptionFromService(&subscription),
		"user profile":        UserFromService(user),
		"usage record":        UsageLogFromService(usage),
		"admin usage record":  UsageLogFromServiceAdmin(usage),
		"observer usage":      UsageLogForObserver(*UsageLogFromServiceAdmin(usage)),
		"redeem history":      RedeemCodeFromService(&service.RedeemCode{ID: 11, Group: group}),
		"account association": AccountGroupFromService(&service.AccountGroup{GroupID: group.ID, Group: group}),
	} {
		t.Run(name, func(t *testing.T) {
			raw, err := json.Marshal(value)
			require.NoError(t, err)
			require.NotContains(t, string(raw), "search_provider_base_url")
			require.NotContains(t, string(raw), "search_provider_api_key_set")
			require.NotContains(t, string(raw), baseURL)
			require.NotContains(t, string(raw), apiKey)
			if name != "observer usage" {
				require.Contains(t, string(raw), group.Name, "the fixture must include its nested group")
			}
		})
	}

	// Public serialization must not erase the owner's stored configuration.
	require.Equal(t, baseURL, *group.SearchProviderBaseURL)
	require.Equal(t, apiKey, *group.SearchProviderAPIKey)
}

func TestAdminGroupRetainsSearchProviderConfigurationWithoutKeyMaterial(t *testing.T) {
	t.Parallel()
	baseURL := "https://search-provider.example.test/private-route"
	emptyKey, configuredKey := "", "fixture-search-provider-token"
	for _, test := range []struct {
		name string
		key  *string
		set  bool
	}{
		{name: "unset"},
		{name: "empty", key: &emptyKey},
		{name: "configured", key: &configuredKey, set: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			group := &service.Group{ID: 7, SearchProviderBaseURL: &baseURL, SearchProviderAPIKey: test.key}
			fields := marshalToMap(t, GroupFromServiceAdmin(group))
			require.Equal(t, baseURL, fields["search_provider_base_url"])
			require.Equal(t, test.set, fields["search_provider_api_key_set"])
			require.NotContains(t, fields, "search_provider_api_key")
			raw, err := json.Marshal(fields)
			require.NoError(t, err)
			require.NotContains(t, string(raw), configuredKey)
		})
	}
}
