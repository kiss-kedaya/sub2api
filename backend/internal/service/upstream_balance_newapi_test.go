package service

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/tlsfingerprint"

	"github.com/stretchr/testify/require"
)

type upstreamBalanceNewAPIHTTP struct{ client *http.Client }

func (u upstreamBalanceNewAPIHTTP) Do(r *http.Request, _ string, _ int64, _ int) (*http.Response, error) {
	return u.client.Do(r)
}
func (u upstreamBalanceNewAPIHTTP) DoWithTLS(r *http.Request, proxy string, id int64, concurrency int, _ *tlsfingerprint.Profile) (*http.Response, error) {
	return u.Do(r, proxy, id, concurrency)
}

func TestUpstreamBalanceNewAPIDocumentChecklist(t *testing.T) {
	for _, tc := range []struct {
		name         string
		status       int
		body         string
		key          float64
		wallet       any
		walletStatus string
	}{
		{"authorized wallet", 200, `{"code":true,"data":{"object":"token_usage","total_available":1650000,"total_used":350000,"total_granted":2000000,"unlimited_quota":false,"user_quota":{"total_available":9800000,"total_used":220000}}}`, 3.3, 19.6, "available"},
		{"wallet permission missing", 200, `{"code":true,"data":{"object":"token_usage","total_available":500000,"total_used":0,"unlimited_quota":false}}`, 1, nil, "forbidden"},
		{"exhausted key is readable", 200, `{"code":true,"data":{"object":"token_usage","total_available":0,"total_used":500000,"unlimited_quota":false,"expires_at":1}}`, 0, nil, "forbidden"},
		{"invalid key", 401, `{"success":false,"message":"invalid synthetic key"}`, 0, nil, ""},
		{"IP whitelist", 403, `{"success":false}`, 0, nil, ""},
		{"rate limit", 429, `{"success":false}`, 0, nil, ""},
		{"server error", 500, `{"success":false}`, 0, nil, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var usageCalls, statusCalls atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == upstreamNewAPIStatusPath {
					statusCalls.Add(1)
					require.Empty(t, r.Header.Get("Authorization"))
					_, _ = io.WriteString(w, `{"data":{"quota_per_unit":500000}}`)
					return
				}
				if r.URL.Path == "/api/usage/token" {
					http.Redirect(w, r, upstreamNewAPIUsagePath, http.StatusMovedPermanently)
					return
				}
				require.Equal(t, upstreamNewAPIUsagePath, r.URL.Path)
				require.Equal(t, http.MethodGet, r.Method)
				require.Equal(t, "Bearer synthetic-balance-key", r.Header.Get("Authorization"))
				usageCalls.Add(1)
				w.WriteHeader(tc.status)
				_, _ = io.WriteString(w, tc.body)
			}))
			defer server.Close()
			a := &Account{ID: 8201, Type: AccountTypeAPIKey, Platform: PlatformOpenAI, Status: StatusActive, Credentials: map[string]any{"api_key": "synthetic-balance-key", "base_url": server.URL}, Extra: map[string]any{}}
			repo := &upstreamBillingProbeAccountRepo{accounts: map[int64]*Account{a.ID: a}}
			svc := newUpstreamBillingProbeTestService(repo, upstreamBalanceNewAPIHTTP{server.Client()}, &upstreamBillingProbeSettingRepo{})
			now := time.Date(2026, 11, 18, 0, 0, 0, 0, time.UTC)
			svc.now = func() time.Time { return now }
			target := upstreamBillingProbeTarget{apiKey: "synthetic-balance-key"}
			result := svc.fetchNewAPIBalance(context.Background(), a, target, server.URL)
			require.Equal(t, tc.status, result.statusCode)
			if tc.status == 200 {
				data, err := svc.parseNewAPIBalance(context.Background(), a, target, server.URL, result.body)
				require.NoError(t, err)
				require.Equal(t, tc.key, data["remaining"])
				require.Equal(t, tc.walletStatus, data["wallet_status"])
				require.Equal(t, tc.wallet, data["wallet_balance"])
				require.Equal(t, false, data["unit_assumed"])
				_, err = svc.parseNewAPIBalance(context.Background(), a, target, server.URL, result.body)
				require.NoError(t, err)
				require.Equal(t, int32(1), statusCalls.Load())
			} else {
				_, err := upstreamBalanceReadResult(result, func(body []byte) (map[string]any, error) { return parseUpstreamNewAPIUsageResponse(body) })
				require.Error(t, err)
				require.NotEmpty(t, upstreamBalanceFailureStatus(result))
			}
			// Repeated clicks reuse the same sample and never extend its observation time.
			now = now.Add(30 * time.Second)
			cached := svc.fetchNewAPIBalance(context.Background(), a, target, server.URL)
			require.Equal(t, result.observedAt, cached.observedAt)
			require.Equal(t, int32(1), usageCalls.Load())
		})
	}
}

func TestUpstreamBalanceNewAPISiteBudgetTwentyMinutes(t *testing.T) {
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		_, _ = io.WriteString(w, `{"code":true,"data":{"object":"token_usage","total_available":0,"unlimited_quota":false}}`)
	}))
	defer server.Close()
	a := &Account{ID: 8202, Type: AccountTypeAPIKey, Extra: map[string]any{}}
	svc := newUpstreamBillingProbeTestService(&upstreamBillingProbeAccountRepo{}, upstreamBalanceNewAPIHTTP{server.Client()}, &upstreamBillingProbeSettingRepo{})
	now := time.Date(2026, 11, 18, 0, 0, 0, 0, time.UTC)
	svc.now = func() time.Time { return now }
	for minute := 0; minute <= 20; minute++ {
		for click := 0; click < 5; click++ {
			svc.fetchNewAPIBalance(context.Background(), a, upstreamBillingProbeTarget{apiKey: "synthetic-key"}, server.URL)
		}
		now = now.Add(time.Minute)
	}
	require.LessOrEqual(t, calls.Load(), int32(20))
}

func TestUpstreamBalanceNewAPICustomRatioAndBillingErrors(t *testing.T) {
	data, err := parseUpstreamNewAPIUsageResponse([]byte(`{"code":true,"data":{"object":"token_usage","total_available":750000,"unlimited_quota":false}}`), 250000)
	require.NoError(t, err)
	require.Equal(t, 3.0, data["remaining"])
	require.Equal(t, false, data["unit_assumed"])
	for _, body := range []string{`{"error":{"message":"synthetic"},"hard_limit_usd":20}`, `{"hard_limit_usd":20}`} {
		usage := []byte(`{"error":{"message":"synthetic"},"total_usage":10}`)
		_, err := parseUpstreamLegacySubscriptionResponse([]byte(body), usage)
		require.Error(t, err)
	}
}

func TestUpstreamBalanceNewAPIRuleScanRefreshesWithoutRateOptIn(t *testing.T) {
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == upstreamNewAPIStatusPath {
			_, _ = io.WriteString(w, `{"data":{"quota_per_unit":250000}}`)
			return
		}
		require.Equal(t, upstreamNewAPIUsagePath, r.URL.Path)
		calls.Add(1)
		_, _ = io.WriteString(w, `{"code":true,"data":{"object":"token_usage","total_available":750000,"unlimited_quota":false,"user_quota":{"total_available":1000000,"total_used":0}}}`)
	}))
	defer server.Close()
	now := time.Now().UTC()
	stale := now.Add(-2 * time.Hour)
	a := &Account{ID: 8301, Type: AccountTypeAPIKey, Platform: PlatformOpenAI, Status: StatusActive, Credentials: map[string]any{"api_key": "synthetic-key", "base_url": server.URL}, Extra: map[string]any{UpstreamBillingProbeEnabledExtraKey: false, UpstreamBillingProbeExtraKey: &UpstreamBillingProbeSnapshot{Status: "ok", UpstreamKind: UpstreamKindNewAPI, Balance: &UpstreamBalanceSnapshot{Status: "ok", ReceivedAt: &stale, FreshUntil: &stale, Data: map[string]any{"remaining": 9.0}}}}}
	repo := &upstreamBillingProbeAccountRepo{accounts: map[int64]*Account{a.ID: a}}
	probe := newUpstreamBillingProbeTestService(repo, upstreamBalanceNewAPIHTTP{server.Client()}, &upstreamBillingProbeSettingRepo{})
	probe.now = func() time.Time { return now }
	svc := NewAccountOpsService(nil, nil, nil)
	probe.SetAccountOpsService(svc)
	c := defaultAccountOpsConfig()
	c.BalanceThresholds = []AccountOpsBalanceRule{{AccountID: a.ID, Enabled: true, Threshold: 5, Unit: "USD"}}
	members := map[int64]accountOpsThresholdMember{a.ID: {id: a.ID, account: a}}
	require.NotNil(t, svc.balanceProbe)
	require.Equal(t, []int64{a.ID}, thresholdBalanceMemberIDs(c))
	require.True(t, a.IsActive())
	require.False(t, opsConfiguredCustomUsage(a))
	require.Equal(t, "stale", opsBalanceAccount(a, now).BalanceStatus)
	svc.opsRefreshNativeBalances(context.Background(), members, c)
	current := decodeUpstreamBillingProbeSnapshot(members[a.ID].account.Extra)
	require.NotNil(t, current)
	require.Empty(t, current.LastError)
	require.Equal(t, int32(1), calls.Load(), "the rule scan must perform one balance read")
	item := opsBalanceAccount(members[a.ID].account, now)
	require.Equal(t, "ok", item.BalanceStatus)
	require.NotNil(t, item.Balance)
	require.Equal(t, 4.0, *item.Balance)
	require.Equal(t, 3.0, *item.KeyBalance)
	svc.opsRefreshNativeBalances(context.Background(), members, c)
	require.Equal(t, int32(1), calls.Load())
}

func TestUpstreamBalanceNewAPIRetryAfterServerFailure(t *testing.T) {
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if calls.Add(1) == 1 {
			w.WriteHeader(http.StatusInternalServerError)
			_, _ = io.WriteString(w, `{"success":false}`)
			return
		}
		_, _ = io.WriteString(w, `{"code":true,"data":{"object":"token_usage","total_available":0,"unlimited_quota":false}}`)
	}))
	defer server.Close()
	a := &Account{ID: 8401, Type: AccountTypeAPIKey}
	svc := newUpstreamBillingProbeTestService(&upstreamBillingProbeAccountRepo{}, upstreamBalanceNewAPIHTTP{server.Client()}, &upstreamBillingProbeSettingRepo{})
	now := time.Now().UTC()
	svc.now = func() time.Time { return now }
	target := upstreamBillingProbeTarget{apiKey: "synthetic-key"}
	result := svc.fetchNewAPIBalance(context.Background(), a, target, server.URL)
	require.Equal(t, http.StatusInternalServerError, result.statusCode)
	now = now.Add(time.Minute)
	require.Equal(t, http.StatusInternalServerError, svc.fetchNewAPIBalance(context.Background(), a, target, server.URL).statusCode)
	require.Equal(t, int32(1), calls.Load())
	now = now.Add(time.Second)
	require.Equal(t, http.StatusOK, svc.fetchNewAPIBalance(context.Background(), a, target, server.URL).statusCode)
	require.Equal(t, int32(2), calls.Load())
	// Another key on the same site does not consume the same IP's next budget.
	require.Equal(t, http.StatusTooManyRequests, svc.fetchNewAPIBalance(context.Background(), a, upstreamBillingProbeTarget{apiKey: "synthetic-other-key"}, server.URL).statusCode)
	require.Equal(t, int32(2), calls.Load())
}

func TestUpstreamBalanceNewAPIPermanentRejectionStopsScheduledRetries(t *testing.T) {
	for _, status := range []int{http.StatusUnauthorized, http.StatusForbidden} {
		a := &Account{ID: 8501, Type: AccountTypeAPIKey, Platform: PlatformOpenAI, Credentials: map[string]any{"base_url": "https://example.invalid", "api_key": "synthetic-key"}, Extra: map[string]any{UpstreamBillingProbeExtraKey: &UpstreamBillingProbeSnapshot{Status: "ok", UpstreamKind: UpstreamKindNewAPI}}}
		upstream := &upstreamKindRouteStub{routes: map[string]upstreamKindRoute{upstreamNewAPIUsagePath: {status: status, body: `{"success":false}`}}}
		snapshot := runUpstreamKindProbe(t, a, upstream)
		require.Equal(t, 9999, snapshot.NextProbeAt.Year())
		require.Equal(t, UpstreamBillingProbeStatusFailed, snapshot.Balance.Status)
	}
}

func TestUpstreamBalanceNewAPIRejectsBusinessFailure(t *testing.T) {
	_, err := parseUpstreamNewAPIUsageResponse([]byte(`{"code":false,"data":{"object":"token_usage","total_available":500000,"unlimited_quota":false}}`))
	require.Error(t, err)
}

func TestUpstreamBalanceNewAPIUnlimitedDoesNotPromoteWallet(t *testing.T) {
	data, err := parseUpstreamNewAPIUsageResponse([]byte(`{"code":true,"data":{"object":"token_usage","unlimited_quota":true,"total_available":0,"user_quota":{"total_available":5000000,"total_used":0}}}`))
	require.NoError(t, err)
	require.Equal(t, true, data["unlimited"])
	require.NotContains(t, data, "remaining")
	require.NotContains(t, data, "wallet_balance")
}

func TestUpstreamBalanceNewAPIGroupSkipsMissingRepresentative(t *testing.T) {
	now := time.Now().UTC()
	fresh := now.Add(time.Hour)
	missing := &Account{ID: 8101, Type: AccountTypeAPIKey, Extra: map[string]any{}}
	available := &Account{ID: 8102, Type: AccountTypeAPIKey, Extra: map[string]any{UpstreamBillingProbeExtraKey: &UpstreamBillingProbeSnapshot{Status: "ok", Balance: &UpstreamBalanceSnapshot{Status: "ok", ReceivedAt: &now, FreshUntil: &fresh, Data: map[string]any{"remaining": 2.0, "unit": "USD"}}}}}
	c := defaultAccountOpsConfig()
	c.Groups = []AccountOpsGroup{{ID: "manual-synthetic", Name: "Synthetic wallet", AccountIDs: []int64{missing.ID, available.ID}}}
	c.BalanceThresholds = []AccountOpsBalanceRule{{AccountID: missing.ID, Enabled: true, Threshold: 5, Unit: "USD"}, {AccountID: available.ID, Enabled: true, Threshold: 5, Unit: "USD"}}
	svc := NewAccountOpsService(nil, nil, nil)
	group := accountOpsGroupAssignment(missing, c)
	event, state := svc.assessThresholdGroup(context.Background(), group, []accountOpsThresholdMember{{id: missing.ID, account: missing}, {id: available.ID, account: available}}, "balance_threshold", c, now)
	require.Equal(t, "active", state)
	require.NotNil(t, event.Details)
	require.NotNil(t, event.Details.Balance)
	require.Equal(t, 2.0, *event.Details.Balance)
}
