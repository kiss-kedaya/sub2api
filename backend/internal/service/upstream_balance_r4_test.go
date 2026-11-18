package service

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

type r4ProjectedAccountRepo struct {
	*upstreamBillingProbeAccountRepo
}

func (r r4ProjectedAccountRepo) ListAccountOpsSnapshots(ctx context.Context, ids []int64) ([]Account, error) {
	values := make([]Account, 0, len(ids))
	for _, id := range ids {
		a, err := r.GetByID(ctx, id)
		if err != nil {
			return nil, err
		}
		values = append(values, Account{ID: a.ID, Name: a.Name, Platform: a.Platform, Type: a.Type, Credentials: a.Credentials, Extra: a.Extra, SessionWindowEnd: a.SessionWindowEnd, Status: a.Status, Schedulable: a.Schedulable})
	}
	return values, nil
}

func TestR4BalanceRefreshThroughSnapshotReader(t *testing.T) {
	for _, tc := range []struct {
		name        string
		status      string
		schedulable bool
		calls       int32
		state       string
	}{
		{"active", StatusActive, true, 1, "active"},
		{"paused scheduling", StatusActive, false, 0, "unknown"},
		{"disabled", "disabled", true, 0, "unknown"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var calls atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == upstreamNewAPIStatusPath {
					_, _ = io.WriteString(w, `{"data":{"quota_per_unit":500000}}`)
					return
				}
				require.Equal(t, upstreamNewAPIUsagePath, r.URL.Path)
				calls.Add(1)
				_, _ = io.WriteString(w, `{"code":true,"data":{"object":"token_usage","total_available":500000,"unlimited_quota":false,"user_quota":{"total_available":1000000}}}`)
			}))
			defer server.Close()
			a := &Account{ID: 9101, Type: AccountTypeAPIKey, Platform: PlatformOpenAI, Status: tc.status, Schedulable: tc.schedulable, Credentials: map[string]any{"base_url": server.URL, "api_key": "synthetic-r4-key"}, Extra: map[string]any{UpstreamBillingProbeEnabledExtraKey: false, UpstreamBillingProbeExtraKey: &UpstreamBillingProbeSnapshot{Status: "ok", UpstreamKind: UpstreamKindNewAPI}}}
			repo := &upstreamBillingProbeAccountRepo{accounts: map[int64]*Account{a.ID: a}}
			probe := newUpstreamBillingProbeTestService(repo, upstreamBalanceNewAPIHTTP{server.Client()}, &upstreamBillingProbeSettingRepo{})
			svc := NewAccountOpsService(nil, nil, nil)
			svc.accounts = r4ProjectedAccountRepo{repo}
			probe.SetAccountOpsService(svc)
			c := defaultAccountOpsConfig()
			c.BalanceThresholds = []AccountOpsBalanceRule{{AccountID: a.ID, Enabled: true, Threshold: 5, Unit: "USD"}}
			members := svc.loadThresholdAccounts(context.Background(), thresholdBalanceMemberIDs(c))
			svc.opsRefreshNativeBalances(context.Background(), members, c)
			_, state := svc.assessThresholdGroup(context.Background(), accountOpsGroupAssignment(a, c), []accountOpsThresholdMember{members[a.ID]}, "balance_threshold", c, time.Now())
			require.Equal(t, tc.calls, calls.Load())
			require.Equal(t, tc.state, state)
		})
	}
}

func TestR4NewAPIBudgetDoesNotBlockLegacyBilling(t *testing.T) {
	a := &Account{ID: 9201, Type: AccountTypeAPIKey, Platform: PlatformOpenAI, Status: StatusActive, Credentials: map[string]any{"base_url": "https://example.invalid/v1", "api_key": "synthetic-r4-one"}, Extra: map[string]any{UpstreamBillingProbeExtraKey: &UpstreamBillingProbeSnapshot{Status: "ok", UpstreamKind: UpstreamKindOpenAICompatible}}}
	b := *a
	b.ID = 9202
	b.Credentials = map[string]any{"base_url": "https://example.invalid/v1", "api_key": "synthetic-r4-two"}
	upstream := &upstreamKindRouteStub{routes: map[string]upstreamKindRoute{
		upstreamLegacySubscriptionPath: {status: http.StatusOK, body: `{"object":"billing_subscription","hard_limit_usd":20}`},
		upstreamLegacyUsagePath:        {status: http.StatusOK, body: `{"total_usage":750}`},
	}}
	probe := newUpstreamBillingProbeTestService(&upstreamBillingProbeAccountRepo{accounts: map[int64]*Account{a.ID: a, b.ID: &b}}, upstream, &upstreamBillingProbeSettingRepo{})
	for _, id := range []int64{a.ID, b.ID} {
		snapshot, err := probe.ProbeAccount(context.Background(), id)
		require.NoError(t, err)
		require.NotNil(t, snapshot.Balance)
		require.Equal(t, "ok", snapshot.Balance.Status)
		require.Equal(t, 12.5, snapshot.Balance.Data["remaining"])
		t.Logf("account=%d kind=%s status=%s remaining=%v unit=%v", id, snapshot.UpstreamKind, snapshot.Balance.Status, snapshot.Balance.Data["remaining"], snapshot.Balance.Data["unit"])
	}
}

func TestR4GroupDoesNotBorrowAnUnprovenWallet(t *testing.T) {
	for _, tc := range []struct {
		name     string
		site     string
		provider string
		userID   int64
		state    string
	}{
		{"different user", "https://example.invalid", "new_api", 8, "unknown"},
		{"unknown user", "https://example.invalid", "new_api", 0, "unknown"},
		{"different provider", "https://example.invalid", "sub2api", 7, "unknown"},
		{"different site", "https://other.example.invalid", "new_api", 7, "unknown"},
		{"same wallet", "https://example.invalid", "new_api", 7, "active"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			now := time.Now().UTC()
			fresh := now.Add(time.Hour)
			a := &Account{ID: 9301, Type: AccountTypeAPIKey, Credentials: map[string]any{"base_url": "https://example.invalid"}, Extra: map[string]any{accountOpsProviderExtraKey: "new_api", UpstreamBillingUserIDExtraKey: int64(7)}}
			b := &Account{ID: 9302, Type: AccountTypeAPIKey, Credentials: map[string]any{"base_url": tc.site}, Extra: map[string]any{accountOpsProviderExtraKey: tc.provider, UpstreamBillingUserIDExtraKey: tc.userID, UpstreamBillingProbeExtraKey: &UpstreamBillingProbeSnapshot{Status: "ok", Balance: &UpstreamBalanceSnapshot{Status: "ok", ReceivedAt: &now, FreshUntil: &fresh, Data: map[string]any{"unit": "USD", "remaining": 2.0}}}}}
			c := defaultAccountOpsConfig()
			c.Groups = []AccountOpsGroup{{ID: "manual-r4", Name: "Synthetic wallets", AccountIDs: []int64{a.ID, b.ID}}}
			c.BalanceThresholds = []AccountOpsBalanceRule{{AccountID: a.ID, Enabled: true, Threshold: 5, Unit: "USD"}, {AccountID: b.ID, Enabled: true, Threshold: 5, Unit: "USD"}}
			svc := NewAccountOpsService(nil, nil, nil)
			event, state := svc.assessThresholdGroup(context.Background(), accountOpsGroupAssignment(a, c), []accountOpsThresholdMember{{id: a.ID, account: a}, {id: b.ID, account: b}}, "balance_threshold", c, now)
			require.Equal(t, tc.state, state)
			if tc.state == "unknown" {
				require.Nil(t, event.Details)
				c.Enabled, c.GroupsInitialized, c.Recipient = true, true, "synthetic@example.invalid"
				c.Groups[0].ID = "manual-r4-wallet"
				raw, err := json.Marshal(c)
				require.NoError(t, err)
				sender, repo := &accountOpsSenderStub{}, &accountOpsRepoStub{}
				delivery := NewAccountOpsService(&accountOpsSettingsStub{raw: string(raw)}, repo, sender)
				delivery.accounts = &opsMultiAccountsStub{accounts: map[int64]*Account{a.ID: a, b.ID: b}}
				event.GroupID, event.Identity, event.Phase = c.Groups[0].ID, c.Groups[0].ID, "alert"
				delivery.deliverNotification(context.Background(), event)
				require.Zero(t, sender.calls)
				require.Equal(t, "deferred", repo.state)
			}
		})
	}
}

func TestR4NewAPICacheIsolatesEffectiveRoute(t *testing.T) {
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == upstreamNewAPIStatusPath {
			_, _ = io.WriteString(w, `{"data":{"quota_per_unit":500000}}`)
			return
		}
		if r.URL.Path != upstreamNewAPIUsagePath {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		calls.Add(1)
		quota := 500000
		if r.Header.Get("X-Route") == "new-wallet" {
			quota = 50000000
		}
		_, _ = fmt.Fprintf(w, `{"code":true,"data":{"object":"token_usage","total_available":500000,"unlimited_quota":false,"user_quota":{"total_available":%d}}}`, quota)
	}))
	defer server.Close()
	a := &Account{ID: 9401, Type: AccountTypeAPIKey, Platform: PlatformOpenAI, Status: StatusActive, Credentials: map[string]any{"base_url": server.URL, "api_key": "synthetic-r4-key", credKeyHeaderOverrideEnabled: true, credKeyHeaderOverrides: map[string]any{"x-route": "old-wallet"}}, Extra: map[string]any{UpstreamBillingProbeExtraKey: &UpstreamBillingProbeSnapshot{Status: "ok", UpstreamKind: UpstreamKindNewAPI}}}
	probe := newUpstreamBillingProbeTestService(&upstreamBillingProbeAccountRepo{accounts: map[int64]*Account{a.ID: a}}, upstreamBalanceNewAPIHTTP{server.Client()}, &upstreamBillingProbeSettingRepo{})
	now := time.Now().UTC()
	probe.now = func() time.Time { return now }
	first, err := probe.ProbeAccount(context.Background(), a.ID)
	require.NoError(t, err)
	require.Equal(t, 1.0, first.Balance.Data["wallet_balance"])
	a.Credentials = map[string]any{"base_url": server.URL, "api_key": "synthetic-r4-key", credKeyHeaderOverrideEnabled: true, credKeyHeaderOverrides: map[string]any{"x-route": "new-wallet"}}
	delete(a.Extra, UpstreamBillingProbeExtraKey)
	now = now.Add(time.Second)
	second, err := probe.ProbeAccount(context.Background(), a.ID)
	require.NoError(t, err)
	require.NotEqual(t, 1.0, second.Balance.Data["wallet_balance"], "a new route must not persist the old wallet as a fresh success")
	now = now.Add(upstreamNewAPIMinInterval)
	third, err := probe.ProbeAccount(context.Background(), a.ID)
	require.NoError(t, err)
	require.Equal(t, 100.0, third.Balance.Data["wallet_balance"])
	require.Equal(t, int32(2), calls.Load())
}

func TestR4NewAPIStatusCacheIsolatesHeaderAndProxyRoute(t *testing.T) {
	upstream := &upstreamKindRouteStub{routes: map[string]upstreamKindRoute{upstreamNewAPIStatusPath: {status: http.StatusOK, body: `{"data":{"quota_per_unit":250000}}`}}}
	probe := newUpstreamBillingProbeTestService(&upstreamBillingProbeAccountRepo{}, upstream, &upstreamBillingProbeSettingRepo{})
	a := &Account{ID: 9501, Type: AccountTypeAPIKey, Platform: PlatformOpenAI, Credentials: map[string]any{credKeyHeaderOverrideEnabled: true, credKeyHeaderOverrides: map[string]any{"x-route": "first"}}}
	target := upstreamBillingProbeTarget{apiKey: "synthetic-r4-key", proxyURL: "http://first.example.invalid"}
	probe.newAPIStatus(context.Background(), a, target, "https://example.invalid")
	probe.newAPIStatus(context.Background(), a, target, "https://example.invalid")
	a.Credentials[credKeyHeaderOverrides] = map[string]any{"x-route": "second"}
	probe.newAPIStatus(context.Background(), a, target, "https://example.invalid")
	target.proxyURL = "http://second.example.invalid"
	probe.newAPIStatus(context.Background(), a, target, "https://example.invalid")
	require.Len(t, upstream.requested(), 3)
}

func TestR4NewAPISameIdentityWaiterCancels(t *testing.T) {
	entered, release := make(chan struct{}), make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		close(entered)
		<-release
		_, _ = io.WriteString(w, `{"code":true,"data":{"object":"token_usage","total_available":500000}}`)
	}))
	defer server.Close()
	a := &Account{ID: 9901, Type: AccountTypeAPIKey, Platform: PlatformOpenAI, Credentials: map[string]any{"base_url": server.URL, "api_key": "synthetic-r4-shared-key"}}
	probe := newUpstreamBillingProbeTestService(&upstreamBillingProbeAccountRepo{}, upstreamBalanceNewAPIHTTP{server.Client()}, &upstreamBillingProbeSettingRepo{})
	target := upstreamBillingProbeTarget{apiKey: "synthetic-r4-shared-key"}
	firstDone := make(chan struct{})
	go func() { defer close(firstDone); probe.fetchNewAPIBalance(context.Background(), a, target, server.URL) }()
	<-entered
	ctx, cancel := context.WithTimeout(context.Background(), 25*time.Millisecond)
	defer cancel()
	secondDone := make(chan struct{})
	go func() { defer close(secondDone); probe.fetchNewAPIBalance(ctx, a, target, server.URL) }()
	blocked := false
	select {
	case <-secondDone:
	case <-time.After(150 * time.Millisecond):
		blocked = true
	}
	close(release)
	<-firstDone
	<-secondDone
	require.False(t, blocked)
}

func TestR4NewAPIBalanceCacheSeparatesActualProxyRoute(t *testing.T) {
	upstream := &upstreamKindRouteStub{routes: map[string]upstreamKindRoute{upstreamNewAPIUsagePath: {status: http.StatusOK, body: `{"code":true,"data":{"object":"token_usage","total_available":500000}}`}}}
	probe := newUpstreamBillingProbeTestService(&upstreamBillingProbeAccountRepo{}, upstream, &upstreamBillingProbeSettingRepo{})
	a := &Account{ID: 9902, Type: AccountTypeAPIKey, Platform: PlatformOpenAI, Credentials: map[string]any{"base_url": "https://example.invalid", "api_key": "synthetic-r4-route"}}
	target := upstreamBillingProbeTarget{apiKey: "synthetic-r4-route", proxyURL: "http://first.example.invalid"}
	first := probe.fetchNewAPIBalance(context.Background(), a, target, "https://example.invalid")
	require.Equal(t, http.StatusOK, first.statusCode)
	target.proxyURL = "http://second.example.invalid"
	second := probe.fetchNewAPIBalance(context.Background(), a, target, "https://example.invalid")
	require.Empty(t, second.body)
	require.Equal(t, "rate_limited", second.failure)
	require.Len(t, upstream.requested(), 1)
}
func TestR4NewAPIWaitingProbeCancels(t *testing.T) {
	entered, release := make(chan struct{}), make(chan struct{})
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == upstreamNewAPIStatusPath {
			_, _ = io.WriteString(w, `{"data":{"quota_per_unit":500000}}`)
			return
		}
		if calls.Add(1) == 1 {
			close(entered)
			<-release
		}
		_, _ = io.WriteString(w, `{"code":true,"data":{"object":"token_usage","total_available":500000}}`)
	}))
	defer server.Close()
	a := &Account{ID: 9601, Type: AccountTypeAPIKey, Platform: PlatformOpenAI, Status: StatusActive, Credentials: map[string]any{"base_url": server.URL, "api_key": "synthetic-r4-one"}, Extra: map[string]any{UpstreamBillingProbeExtraKey: &UpstreamBillingProbeSnapshot{Status: "ok", UpstreamKind: UpstreamKindNewAPI}}}
	b := *a
	b.ID = 9602
	b.Credentials = map[string]any{"base_url": server.URL, "api_key": "synthetic-r4-two"}
	b.Extra = mergeMap(nil, a.Extra)
	probe := newUpstreamBillingProbeTestService(&upstreamBillingProbeAccountRepo{accounts: map[int64]*Account{a.ID: a, b.ID: &b}}, upstreamBalanceNewAPIHTTP{server.Client()}, &upstreamBillingProbeSettingRepo{})
	firstDone := make(chan struct{})
	go func() { defer close(firstDone); _, _ = probe.ProbeAccount(context.Background(), a.ID) }()
	<-entered
	ctx, cancel := context.WithTimeout(context.Background(), 25*time.Millisecond)
	defer cancel()
	secondDone := make(chan struct{})
	go func() { defer close(secondDone); _, _ = probe.ProbeAccount(ctx, b.ID) }()
	blocked := false
	select {
	case <-secondDone:
	case <-time.After(150 * time.Millisecond):
		blocked = true
	}
	close(release)
	<-firstDone
	<-secondDone
	require.False(t, blocked, "a canceled probe must not wait for a peer's network request")
}
