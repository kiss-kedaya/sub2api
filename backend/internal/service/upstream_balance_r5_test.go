package service

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/tlsfingerprint"
	"github.com/stretchr/testify/require"
)

type reviewBalanceTransport struct {
	panicOnce       atomic.Bool
	calls           atomic.Int32
	entered         chan struct{}
	denyFirstTenant bool
}
type reviewPanicBody struct{}

func (reviewPanicBody) Read([]byte) (int, error) { panic("synthetic response reader panic") }
func (reviewPanicBody) Close() error             { return nil }
func (u *reviewBalanceTransport) Do(r *http.Request, _ string, _ int64, _ int) (*http.Response, error) {
	status, body := 404, `{}`
	if r.URL.Path == upstreamNewAPIUsagePath {
		if u.calls.Add(1) == 1 && u.entered != nil {
			close(u.entered)
			<-r.Context().Done()
			return nil, r.Context().Err()
		}
		if u.panicOnce.CompareAndSwap(true, false) {
			return &http.Response{StatusCode: 200, Header: make(http.Header), Body: reviewPanicBody{}}, nil
		}
		route := ""
		for name, values := range r.Header {
			if strings.EqualFold(name, "X-Route") && len(values) > 0 {
				route = values[0]
			}
		}
		if u.denyFirstTenant && route == "tenant-a" {
			return &http.Response{StatusCode: 403, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(`{"success":false}`))}, nil
		}
		body = `{"code":true,"data":{"object":"token_usage","total_available":500000,"unlimited_quota":false,"user_quota":{"total_available":1000000}}}`
		status = 200
	}
	if r.URL.Path == upstreamNewAPIStatusPath {
		status, body = 200, `{"data":{"quota_per_unit":500000}}`
	}
	return &http.Response{StatusCode: status, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(body))}, nil
}
func (u *reviewBalanceTransport) DoWithTLS(r *http.Request, p string, id int64, n int, _ *tlsfingerprint.Profile) (*http.Response, error) {
	return u.Do(r, p, id, n)
}

// This is a recovered handler fault, not a permanent upstream outage: after the
// first panic every subsequent HTTP response is valid and immediate.
func TestReviewNewAPIPanicDoesNotPoisonNextProbe(t *testing.T) {
	a := &Account{ID: 99101, Type: AccountTypeAPIKey, Platform: PlatformOpenAI, Status: StatusActive, Schedulable: true,
		Credentials: map[string]any{"base_url": "https://synthetic.example.invalid", "api_key": "synthetic-review-key"},
		Extra:       map[string]any{UpstreamBillingProbeExtraKey: &UpstreamBillingProbeSnapshot{Status: "ok", UpstreamKind: UpstreamKindNewAPI}}}
	upstream := &reviewBalanceTransport{}
	upstream.panicOnce.Store(true)
	b := *a
	b.ID++
	b.Credentials = map[string]any{"base_url": "https://synthetic.example.invalid", "api_key": "synthetic-review-other-key"}
	probe := newUpstreamBillingProbeTestService(&upstreamBillingProbeAccountRepo{accounts: map[int64]*Account{a.ID: a, b.ID: &b}}, upstream, &upstreamBillingProbeSettingRepo{})
	now := time.Now().UTC()
	probe.now = func() time.Time { return now }
	var recovered any
	func() {
		defer func() { recovered = recover() }()
		_, _ = probe.ProbeAccount(context.Background(), a.ID)
	}()
	require.NotNil(t, recovered)
	now = now.Add(2 * time.Hour)
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	snapshot, err := probe.ProbeAccount(ctx, a.ID)
	require.NoError(t, err)
	require.NotNil(t, snapshot)
	require.NotNil(t, snapshot.Balance)
	now = now.Add(upstreamNewAPIMinInterval)
	other, otherErr := probe.ProbeAccount(context.Background(), b.ID)
	require.NoError(t, otherErr)
	require.NotNil(t, other.Balance)
	t.Logf("two hours after recovered panic: same_key_balance=%s reason=%s other_key_balance=%s reason=%s upstream_calls=%d context=%v", snapshot.Balance.Status, snapshot.Balance.LastError, other.Balance.Status, other.Balance.LastError, upstream.calls.Load(), ctx.Err())
	require.Equal(t, "ok", snapshot.Balance.Status, "a recovered panic must not leave a permanent in-progress entry")
	require.Equal(t, "ok", other.Balance.Status)
	require.Equal(t, int32(3), upstream.calls.Load())
}

// Two tenant routes can have the same provider, URL, and tenant-local user ID.
// Routing headers were already supported before these changes.
func TestReviewGroupNeverBorrowsFromDifferentEffectiveRoute(t *testing.T) {
	now := time.Now().UTC()
	fresh := now.Add(time.Hour)
	account := func(id int64, route string) *Account {
		return &Account{ID: id, Name: fmt.Sprint(id), Type: AccountTypeAPIKey, Platform: PlatformOpenAI, Status: StatusActive, Schedulable: true,
			Credentials: map[string]any{"base_url": "https://synthetic.example.invalid", "api_key": fmt.Sprintf("synthetic-key-%d", id), credKeyHeaderOverrideEnabled: true, credKeyHeaderOverrides: map[string]any{"x-route": route}},
			Extra:       map[string]any{accountOpsProviderExtraKey: "new_api", UpstreamBillingUserIDExtraKey: int64(7), CustomUsageExtraKey: map[string]any{"enabled": false}}}
	}
	a, b := account(99201, "tenant-a"), account(99202, "tenant-b")
	a.Extra[UpstreamBillingProbeExtraKey] = &UpstreamBillingProbeSnapshot{Status: "ok", UpstreamKind: UpstreamKindNewAPI}
	upstream := &reviewBalanceTransport{denyFirstTenant: true}
	probe := newUpstreamBillingProbeTestService(&upstreamBillingProbeAccountRepo{accounts: map[int64]*Account{a.ID: a, b.ID: b}}, upstream, &upstreamBillingProbeSettingRepo{})
	probe.now = func() time.Time { return now }
	first, err := probe.ProbeAccount(context.Background(), a.ID)
	require.NoError(t, err)
	a.Extra[UpstreamBillingProbeExtraKey] = first
	t.Logf("tenant A upstream response: balance=%s error=%s HTTP=%d", first.Balance.Status, first.Balance.LastError, first.Balance.HTTPStatus)
	b.Extra[UpstreamBillingProbeExtraKey] = &UpstreamBillingProbeSnapshot{Status: "ok", UpstreamKind: UpstreamKindNewAPI, Balance: &UpstreamBalanceSnapshot{Status: "ok", ReceivedAt: &now, FreshUntil: &fresh, Data: map[string]any{"remaining": 2.0, "wallet_balance": 2.0, "wallet_status": "available", "unit": "USD"}}}
	c := defaultAccountOpsConfig()
	c.Enabled = true
	c.GroupsInitialized = true
	c.Groups = []AccountOpsGroup{{ID: "manual-review-wallets", Name: "Synthetic independent tenants", AccountIDs: []int64{a.ID, b.ID}}}
	c.BalanceThresholds = []AccountOpsBalanceRule{{AccountID: a.ID, Enabled: true, Threshold: 5, Unit: "USD"}, {AccountID: b.ID, Enabled: true, Threshold: 5, Unit: "USD"}}
	c.Recipient = "synthetic-review@example.invalid"
	svc := NewAccountOpsService(nil, nil, nil)
	svc.accounts = &opsMultiAccountsStub{accounts: map[int64]*Account{a.ID: a, b.ID: b}}
	group, members, err := svc.loadThresholdGroupMembers(context.Background(), c.Groups[0].ID, "balance_threshold", c)
	require.NoError(t, err)
	event, state := svc.assessThresholdGroup(context.Background(), group, members, "balance_threshold", c, now)
	var balance any
	if event.Details != nil {
		balance = event.Details.Balance
	}
	raw, err := json.Marshal(c)
	require.NoError(t, err)
	sender, repo := &accountOpsSenderStub{}, &accountOpsRepoStub{}
	delivery := NewAccountOpsService(&accountOpsSettingsStub{raw: string(raw)}, repo, sender)
	delivery.accounts = svc.accounts
	event.Phase = "alert"
	delivery.deliverNotification(context.Background(), event)
	t.Logf("group assessment: state=%s representative=%d details=%v notification_calls=%d delivery_state=%s", state, event.AccountID, balance, sender.calls, repo.state)
	require.Equal(t, "unknown", state, "the first tenant has no observation; tenant B's balance cannot confirm tenant A's threshold")
	require.Zero(t, sender.calls)
}

func TestReviewCanceledOwnerDoesNotBlockFutureProbe(t *testing.T) {
	a := &Account{ID: 99301, Type: AccountTypeAPIKey, Platform: PlatformOpenAI, Status: StatusActive, Credentials: map[string]any{"base_url": "https://synthetic.example.invalid", "api_key": "synthetic-review-cancel"}, Extra: map[string]any{UpstreamBillingProbeExtraKey: &UpstreamBillingProbeSnapshot{Status: "ok", UpstreamKind: UpstreamKindNewAPI}}}
	upstream := &reviewBalanceTransport{entered: make(chan struct{})}
	probe := newUpstreamBillingProbeTestService(&upstreamBillingProbeAccountRepo{accounts: map[int64]*Account{a.ID: a}}, upstream, &upstreamBillingProbeSettingRepo{})
	now := time.Now().UTC()
	probe.now = func() time.Time { return now }
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { defer close(done); _, _ = probe.ProbeAccount(ctx, a.ID) }()
	<-upstream.entered
	cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("owner failed to return after cancellation")
	}
	now = now.Add(2 * time.Hour)
	snapshot, err := probe.ProbeAccount(context.Background(), a.ID)
	require.NoError(t, err)
	require.NotNil(t, snapshot.Balance)
	require.Equal(t, "ok", snapshot.Balance.Status)
	require.Equal(t, int32(2), upstream.calls.Load())
}

// Success creates a proof; configuration changes cannot relabel that observation.
func TestR5WalletProofBindsObservationAndNotification(t *testing.T) {
	for _, mode := range []string{"same authenticated wallet", "legacy snapshot", "different key", "changed header", "changed proxy", "missing proxy", "old fingerprint", "changed before delivery"} {
		t.Run(mode, func(t *testing.T) {
			now := time.Now().UTC()
			a := &Account{ID: 99501, Name: "synthetic", Type: AccountTypeAPIKey, Platform: PlatformOpenAI, Status: StatusActive, Schedulable: true,
				Credentials: map[string]any{"base_url": "https://synthetic.example.invalid", "api_key": "synthetic-shared-wallet", credKeyHeaderOverrideEnabled: true, credKeyHeaderOverrides: map[string]any{"x-route": "tenant-a"}},
				Extra:       map[string]any{accountOpsProviderExtraKey: "new_api", UpstreamBillingUserIDExtraKey: int64(7), UpstreamBillingProbeExtraKey: &UpstreamBillingProbeSnapshot{Status: "ok", UpstreamKind: UpstreamKindNewAPI}}}
			proxyID := int64(1)
			a.ProxyID = &proxyID
			a.Proxy = &Proxy{ID: proxyID, Protocol: "http", Host: "first-proxy.example.invalid", Port: 8080}
			upstream := &reviewBalanceTransport{}
			probe := newUpstreamBillingProbeTestService(&upstreamBillingProbeAccountRepo{accounts: map[int64]*Account{a.ID: a}}, upstream, &upstreamBillingProbeSettingRepo{})
			probe.now = func() time.Time { return now }
			snapshot, err := probe.ProbeAccount(context.Background(), a.ID)
			require.NoError(t, err)
			a.Extra[UpstreamBillingProbeExtraKey] = snapshot
			require.NotEmpty(t, opsBalanceWalletIdentity(a), "successful route must prove its wallet")
			b := *a
			b.ID++
			b.Credentials = mergeMap(nil, a.Credentials)
			b.Extra = mergeMap(nil, a.Extra)
			// The anchor retains its proof but temporarily cannot read its balance.
			anchorSnapshot := *snapshot
			anchorBalance := *snapshot.Balance
			anchorBalance.Status = "failed"
			anchorSnapshot.Balance = &anchorBalance
			a.Extra[UpstreamBillingProbeExtraKey] = &anchorSnapshot
			switch mode {
			case "legacy snapshot":
				delete(anchorBalance.Data, "wallet_route_identity")
			case "different key":
				b.Credentials["api_key"] = "synthetic-other-key"
			case "changed header":
				b.Credentials[credKeyHeaderOverrides] = map[string]any{"x-route": "tenant-b"}
			case "changed proxy":
				id := int64(1)
				b.ProxyID = &id
				b.Proxy = &Proxy{ID: id, Protocol: "http", Host: "proxy.example.invalid", Port: 8080}
			case "missing proxy":
				b.Proxy = nil
			case "old fingerprint":
				anchorBalance.Data["wallet_route_identity"] = "legacy-provider-site-user-fingerprint"
			}
			c := defaultAccountOpsConfig()
			c.Enabled = true
			c.GroupsInitialized = true
			c.Recipient = "synthetic@example.invalid"
			c.Groups = []AccountOpsGroup{{ID: "manual-r5-wallet", Name: "synthetic", AccountIDs: []int64{a.ID, b.ID}}}
			c.BalanceThresholds = []AccountOpsBalanceRule{{AccountID: a.ID, Enabled: true, Threshold: 5, Unit: "USD"}}
			svc := NewAccountOpsService(nil, nil, nil)
			svc.accounts = &opsMultiAccountsStub{accounts: map[int64]*Account{a.ID: a, b.ID: &b}}
			group, members, err := svc.loadThresholdGroupMembers(context.Background(), c.Groups[0].ID, "balance_threshold", c)
			require.NoError(t, err)
			event, state := svc.assessThresholdGroup(context.Background(), group, members, "balance_threshold", c, now)
			raw, err := json.Marshal(c)
			require.NoError(t, err)
			sender, repo := &accountOpsSenderStub{}, &accountOpsRepoStub{}
			delivery := NewAccountOpsService(&accountOpsSettingsStub{raw: string(raw)}, repo, sender)
			delivery.accounts = svc.accounts
			event.Phase = "alert"
			if mode == "changed before delivery" {
				require.Equal(t, "active", state)
				b.Credentials[credKeyHeaderOverrides] = map[string]any{"x-route": "tenant-b"}
			}
			delivery.deliverNotification(context.Background(), event)
			if mode == "same authenticated wallet" {
				require.Equal(t, "active", state)
				require.Equal(t, 1, sender.calls)
				require.Equal(t, "sent", repo.state)
			} else {
				if mode != "changed before delivery" {
					require.Equal(t, "unknown", state)
				}
				require.Zero(t, sender.calls)
				require.Equal(t, "deferred", repo.state)
			}
			public, _ := json.Marshal(opsBalanceWalletIdentity(&b))
			require.NotContains(t, string(public), "synthetic-shared-wallet")
			require.NotContains(t, string(public), "tenant-a")
		})
	}
}

func TestR5StatusMetadataScope(t *testing.T) {
	upstream := &upstreamKindRouteStub{routes: map[string]upstreamKindRoute{upstreamNewAPIStatusPath: {status: 200, body: `{"data":{"quota_per_unit":250000}}`}}}
	probe := newUpstreamBillingProbeTestService(&upstreamBillingProbeAccountRepo{}, upstream, &upstreamBillingProbeSettingRepo{})
	a := &Account{Type: AccountTypeAPIKey, Platform: PlatformOpenAI, Credentials: map[string]any{"api_key": "synthetic-one", credKeyHeaderOverrideEnabled: true, credKeyHeaderOverrides: map[string]any{"x-route": "tenant-a", "authorization": "synthetic-overridden"}}}
	target := upstreamBillingProbeTarget{apiKey: "synthetic-one", proxyURL: "http://proxy-a.example.invalid"}
	probe.newAPIStatus(context.Background(), a, target, "https://synthetic.example.invalid")
	a.Credentials["api_key"] = "synthetic-two"
	target.apiKey = "synthetic-two"
	probe.newAPIStatus(context.Background(), a, target, "https://synthetic.example.invalid")
	require.Len(t, upstream.requested(), 1, "public metadata ignores the key and strips Authorization")
	a.Credentials[credKeyHeaderOverrides] = map[string]any{"x-route": "tenant-b"}
	probe.newAPIStatus(context.Background(), a, target, "https://synthetic.example.invalid")
	target.proxyURL = "http://proxy-b.example.invalid"
	probe.newAPIStatus(context.Background(), a, target, "https://synthetic.example.invalid")
	require.Len(t, upstream.requested(), 3, "tenant headers and actual proxy routes isolate status metadata")
}

func TestR5PanicReleasesSlotAndInvalidatesOldResult(t *testing.T) {
	a := &Account{Type: AccountTypeAPIKey, Platform: PlatformOpenAI, Credentials: map[string]any{"base_url": "https://synthetic.example.invalid", "api_key": "synthetic-one"}}
	upstream := &reviewBalanceTransport{}
	probe := newUpstreamBillingProbeTestService(&upstreamBillingProbeAccountRepo{}, upstream, &upstreamBillingProbeSettingRepo{})
	now := time.Now().UTC()
	probe.now = func() time.Time { return now }
	target := upstreamBillingProbeTarget{apiKey: a.GetCredential("api_key")}
	first := probe.fetchNewAPIBalance(context.Background(), a, target, a.GetCredential("base_url"))
	require.Equal(t, 200, first.statusCode)
	now = now.Add(upstreamNewAPIMinInterval)
	upstream.panicOnce.Store(true)
	require.Panics(t, func() { probe.fetchNewAPIBalance(context.Background(), a, target, a.GetCredential("base_url")) })
	entry, _ := probe.balanceCache.Load(upstreamSiteRootURL(a.GetCredential("base_url"), "/"))
	cached, ok := entry.(*upstreamNewAPICachedRead)
	require.True(t, ok)
	cached.mu.Lock()
	require.Nil(t, cached.done)
	cached.mu.Unlock()
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	result := probe.fetchNewAPIBalance(ctx, a, target, a.GetCredential("base_url"))
	require.Equal(t, "request_failed", result.failure)
	require.Empty(t, result.body, "panic must not return an older successful wallet")
	now = now.Add(upstreamNewAPIMinInterval)
	require.Equal(t, 200, probe.fetchNewAPIBalance(context.Background(), a, target, a.GetCredential("base_url")).statusCode)
}
