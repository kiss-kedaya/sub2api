package service

import (
	"context"
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestR4BaselineLegacyMultiKey(t *testing.T) {
	a := &Account{ID: 9801, Type: AccountTypeAPIKey, Platform: PlatformOpenAI, Status: StatusActive, Credentials: map[string]any{"base_url": "https://example.invalid/v1", "api_key": "synthetic-r4-baseline-one"}, Extra: map[string]any{UpstreamBillingProbeExtraKey: &UpstreamBillingProbeSnapshot{Status: "ok", UpstreamKind: UpstreamKindOpenAICompatible}}}
	b := *a
	b.ID = 9802
	b.Credentials = map[string]any{"base_url": "https://example.invalid/v1", "api_key": "synthetic-r4-baseline-two"}
	upstream := &upstreamKindRouteStub{routes: map[string]upstreamKindRoute{
		upstreamLegacySubscriptionPath: {status: http.StatusOK, body: `{"object":"billing_subscription","hard_limit_usd":20}`},
		upstreamLegacyUsagePath:        {status: http.StatusOK, body: `{"total_usage":750}`},
	}}
	probe := newUpstreamBillingProbeTestService(&upstreamBillingProbeAccountRepo{accounts: map[int64]*Account{a.ID: a, b.ID: &b}}, upstream, &upstreamBillingProbeSettingRepo{})
	for _, id := range []int64{a.ID, b.ID} {
		snapshot, err := probe.ProbeAccount(context.Background(), id)
		require.NoError(t, err)
		require.Equal(t, "ok", snapshot.Balance.Status)
		require.Equal(t, UpstreamKindOpenAICompatible, snapshot.UpstreamKind)
		require.Equal(t, 12.5, snapshot.Balance.Data["remaining"])
		t.Logf("account=%d kind=%s status=%s remaining=%v unit=%v", id, snapshot.UpstreamKind, snapshot.Balance.Status, snapshot.Balance.Data["remaining"], snapshot.Balance.Data["unit"])
	}
}
func TestR4BaselineRulesNativeAndSavedCustomUsage(t *testing.T) {
	for _, saved := range []bool{false, true} {
		name := "native snapshot only"
		if saved {
			name = "saved custom query"
		}
		t.Run(name, func(t *testing.T) {
			now := time.Now().UTC()
			fresh := now.Add(time.Hour)
			a := &Account{ID: 9701, Name: "Synthetic baseline", Type: AccountTypeAPIKey, Platform: PlatformOpenAI, Status: StatusActive, Schedulable: true, Credentials: map[string]any{"base_url": "https://example.invalid", "api_key": "synthetic-r4-baseline"}, Extra: map[string]any{UpstreamBillingProbeExtraKey: &UpstreamBillingProbeSnapshot{Status: "ok", UpstreamKind: UpstreamKindSub2API, Balance: &UpstreamBalanceSnapshot{Status: "ok", ReceivedAt: &now, FreshUntil: &fresh, Data: map[string]any{"remaining": 12.5, "unit": "USD"}}}}}
			if saved {
				config, _ := customUsageAccountDefaults(a)
				a.Extra[CustomUsageExtraKey] = config
			}
			svc, fixture := newOpsCustomUsageFixture(t, a, &opsCustomUsageClock{at: now})
			c := defaultAccountOpsConfig()
			c.Enabled, c.GroupsInitialized, c.Recipient = true, true, "synthetic@example.invalid"
			c.Groups = []AccountOpsGroup{{ID: "manual-baseline-wallet", Name: "Synthetic baseline", Provider: "sub2api", Site: "https://example.invalid", AccountIDs: []int64{a.ID}}}
			c.BalanceThresholds = []AccountOpsBalanceRule{{AccountID: a.ID, Enabled: true, Threshold: 20, Unit: "USD"}}
			require.NoError(t, svc.SaveConfig(context.Background(), c))
			items, err := svc.ThresholdAccounts(context.Background())
			require.NoError(t, err)
			require.Len(t, items, 1)
			require.Equal(t, "ok", items[0].BalanceStatus)
			require.Equal(t, 12.5, *items[0].Balance)
			require.Equal(t, "USD", items[0].Unit)
			event, state := svc.assessThresholdGroup(context.Background(), accountOpsGroupAssignment(a, c), []accountOpsThresholdMember{{id: a.ID, account: a}}, "balance_threshold", c, now)
			require.Equal(t, "active", state)
			require.Equal(t, 12.5, *event.Details.Balance)
			t.Logf("saved=%v page=%s/%.2f/%s threshold=%s/%.2f query_calls=%d", saved, items[0].BalanceStatus, *items[0].Balance, items[0].Unit, state, *event.Details.Balance, fixture.calls)
		})
	}
}
