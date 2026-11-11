package service

import (
	"context"
	"io"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/tlsfingerprint"
	"github.com/stretchr/testify/require"
)

// upstreamKindRouteStub answers by request path, so a test states which
// endpoint returns what instead of relying on call order. Unlisted paths
// answer 404. Every requested path is recorded in order.
type upstreamKindRouteStub struct {
	mu     sync.Mutex
	routes map[string]upstreamKindRoute
	paths  []string
}

type upstreamKindRoute struct {
	status int
	body   string
}

func (u *upstreamKindRouteStub) Do(req *http.Request, _ string, _ int64, _ int) (*http.Response, error) {
	u.mu.Lock()
	defer u.mu.Unlock()
	u.paths = append(u.paths, req.URL.Path)
	route, ok := u.routes[req.URL.Path]
	if !ok {
		route = upstreamKindRoute{status: http.StatusNotFound, body: `{"error":"not found"}`}
	}
	return &http.Response{
		StatusCode: route.status,
		Header:     http.Header{"Content-Type": []string{"application/json"}},
		Body:       io.NopCloser(strings.NewReader(route.body)),
	}, nil
}

func (u *upstreamKindRouteStub) DoWithTLS(req *http.Request, proxyURL string, accountID int64, concurrency int, _ *tlsfingerprint.Profile) (*http.Response, error) {
	return u.Do(req, proxyURL, accountID, concurrency)
}

func (u *upstreamKindRouteStub) requested() []string {
	u.mu.Lock()
	defer u.mu.Unlock()
	return append([]string(nil), u.paths...)
}

const (
	upstreamKindTestBillingPath = "/v1/sub2api/billing"
	upstreamKindTestUsagePath   = "/v1/usage"
	upstreamKindTestSub2APIRate = `{"object":"sub2api.key_billing","schema_version":1,"billing_scope":"token","group_rate_multiplier":0.8,"resolved_rate_multiplier":0.8,"peak_rate_enabled":false,"effective_rate_multiplier":0.8,"observed_at":"2026-07-13T01:00:00Z"}`
	upstreamKindTestSub2APIUse  = `{"isValid":true,"planName":"wallet","remaining":42,"unit":"USD","balance":42}`
	upstreamKindTestNewAPIUsage = `{"code":true,"message":"ok","data":{"object":"token_usage","name":"my-key","total_granted":5000000,"total_used":1200000,"total_available":3800000,"unlimited_quota":false,"expires_at":0,"user_quota":{"total_available":12345678,"total_used":8765432}}}`
)

// runUpstreamKindProbe probes one account that nobody configured: no enabled
// flag, no balance-threshold rule. That is the case the feature exists for.
func runUpstreamKindProbe(t *testing.T, account *Account, upstream *upstreamKindRouteStub) *UpstreamBillingProbeSnapshot {
	t.Helper()
	repo := &upstreamBillingProbeAccountRepo{accounts: map[int64]*Account{account.ID: account}}
	svc := newUpstreamBillingProbeTestService(repo, upstream, &upstreamBillingProbeSettingRepo{})
	svc.now = func() time.Time { return time.Date(2026, time.July, 13, 1, 30, 0, 0, time.UTC) }
	snapshot, err := svc.ProbeAccount(context.Background(), account.ID)
	require.NoError(t, err)
	require.NotNil(t, snapshot)
	persisted := decodeUpstreamBillingProbeSnapshot(account.Extra)
	require.NotNil(t, persisted)
	require.Equal(t, snapshot.UpstreamKind, persisted.UpstreamKind, "the kind must be stored, not just returned")
	return snapshot
}

func TestUpstreamKindProbeRecognisesSub2API(t *testing.T) {
	upstream := &upstreamKindRouteStub{routes: map[string]upstreamKindRoute{
		upstreamKindTestBillingPath: {http.StatusOK, upstreamKindTestSub2APIRate},
		upstreamKindTestUsagePath:   {http.StatusOK, upstreamKindTestSub2APIUse},
	}}

	snapshot := runUpstreamKindProbe(t, newUpstreamBalanceProbeTestAccount(71, nil), upstream)

	require.Equal(t, UpstreamKindSub2API, snapshot.UpstreamKind)
	require.Equal(t, UpstreamBillingProbeStatusOK, snapshot.Status)
	require.NotNil(t, snapshot.Balance)
	require.Equal(t, UpstreamBillingProbeStatusOK, snapshot.Balance.Status)
	require.Equal(t, UpstreamBalanceSourceSub2APIUsage, snapshot.Balance.Source)
	require.Equal(t, 42.0, snapshot.Balance.Data["remaining"])
	// A recognised Sub2API upstream is never asked about the other software's paths.
	require.Equal(t, []string{upstreamKindTestBillingPath, upstreamKindTestUsagePath}, upstream.requested())
}

// An older Sub2API has /v1/usage but not /v1/sub2api/billing.
func TestUpstreamKindProbeRecognisesSub2APIWithoutBillingEndpoint(t *testing.T) {
	upstream := &upstreamKindRouteStub{routes: map[string]upstreamKindRoute{
		upstreamKindTestUsagePath: {http.StatusOK, upstreamKindTestSub2APIUse},
	}}

	snapshot := runUpstreamKindProbe(t, newUpstreamBalanceProbeTestAccount(72, nil), upstream)

	require.Equal(t, UpstreamKindSub2API, snapshot.UpstreamKind)
	require.Equal(t, UpstreamBillingProbeStatusUnsupported, snapshot.Status, "there is still no rate to read")
	require.Equal(t, UpstreamBillingProbeStatusOK, snapshot.Balance.Status)
	// A readable balance keeps the normal interval, not the stretched one.
	requireNormalProbeInterval(t, snapshot)
}

// The probe delay is jittered by up to a fifth of the interval (capped at five
// minutes), so intervals are asserted as ranges: 30 minutes ± 5 for a normal
// one, and at least 8 × 25 minutes for a stretched one.
func requireNormalProbeInterval(t *testing.T, snapshot *UpstreamBillingProbeSnapshot) {
	t.Helper()
	delay := snapshot.NextProbeAt.Sub(snapshot.LastAttemptAt)
	require.GreaterOrEqual(t, delay, 25*time.Minute)
	require.LessOrEqual(t, delay, 35*time.Minute)
}

func requireStretchedProbeInterval(t *testing.T, snapshot *UpstreamBillingProbeSnapshot) {
	t.Helper()
	delay := snapshot.NextProbeAt.Sub(snapshot.LastAttemptAt)
	require.GreaterOrEqual(t, delay, time.Duration(upstreamBillingProbeUnsupportedDelayFactor)*25*time.Minute)
}

func TestUpstreamKindProbeRecognisesNewAPI(t *testing.T) {
	upstream := &upstreamKindRouteStub{routes: map[string]upstreamKindRoute{
		upstreamNewAPIUsagePath: {http.StatusOK, upstreamKindTestNewAPIUsage},
	}}

	snapshot := runUpstreamKindProbe(t, newUpstreamBalanceProbeTestAccount(73, nil), upstream)

	require.Equal(t, UpstreamKindNewAPI, snapshot.UpstreamKind)
	require.Equal(t, UpstreamBillingProbeStatusUnsupported, snapshot.Status, "new-api serves no rate this probe reads")
	require.Nil(t, snapshot.SyncedRateMultiplier)
	require.NotNil(t, snapshot.Balance)
	require.Equal(t, UpstreamBillingProbeStatusOK, snapshot.Balance.Status)
	require.Equal(t, UpstreamBalanceSourceNewAPITokenUsage, snapshot.Balance.Source)
	data := snapshot.Balance.Data
	// 500000 quota points are one dollar under new-api's default ratio.
	require.InDelta(t, 7.6, data["remaining"], 1e-9)
	require.InDelta(t, 2.4, data["quota_used"], 1e-9)
	require.InDelta(t, 10.0, data["quota_limit"], 1e-9)
	require.InDelta(t, 24.691356, data["wallet_balance"], 1e-6)
	require.Equal(t, "USD", data["unit"])
	require.Equal(t, true, data["unit_assumed"], "the ratio is new-api's default, not something the site reported")
	require.Equal(t, "my-key", data["plan_name"])
	require.NotContains(t, data, "expires_at", "expires_at 0 means the token never expires")
	requireNormalProbeInterval(t, snapshot)
	require.Equal(t, []string{upstreamKindTestBillingPath, upstreamKindTestUsagePath, upstreamNewAPIUsagePath}, upstream.requested())
}

// The account stores the model endpoint (…/v1); new-api's usage endpoint lives
// at the site root and its router requires the trailing slash.
func TestUpstreamSiteRootURL(t *testing.T) {
	for base, want := range map[string]string{
		"https://relay.example/v1":          "https://relay.example/api/usage/token/",
		"https://relay.example/v1/":         "https://relay.example/api/usage/token/",
		"https://relay.example":             "https://relay.example/api/usage/token/",
		"https://relay.example/openai/v1":   "https://relay.example/api/usage/token/",
		"https://relay.example:8443/v1?x=1": "https://relay.example:8443/api/usage/token/",
		"http://relay.example/v1#frag":      "http://relay.example/api/usage/token/",
	} {
		require.Equal(t, want, upstreamSiteRootURL(base, upstreamNewAPIUsagePath), base)
	}
}

func TestParseUpstreamNewAPIUsageResponse(t *testing.T) {
	t.Run("unlimited token reports zero available and must not read as empty", func(t *testing.T) {
		data, err := parseUpstreamNewAPIUsageResponse([]byte(`{"code":true,"data":{"object":"token_usage","total_granted":0,"total_used":900000,"total_available":0,"unlimited_quota":true,"expires_at":0}}`))
		require.NoError(t, err)
		require.Equal(t, true, data["unlimited"])
		require.NotContains(t, data, "remaining")
		require.NotContains(t, data, "quota_limit")
		require.InDelta(t, 1.8, data["quota_used"], 1e-9)
	})
	t.Run("user_quota is absent when the token may not query it", func(t *testing.T) {
		data, err := parseUpstreamNewAPIUsageResponse([]byte(`{"code":true,"data":{"object":"token_usage","total_granted":1000000,"total_used":0,"total_available":1000000,"unlimited_quota":false,"expires_at":0}}`))
		require.NoError(t, err)
		require.InDelta(t, 2.0, data["remaining"], 1e-9)
		require.NotContains(t, data, "wallet_balance")
	})
	t.Run("expiry is a unix timestamp", func(t *testing.T) {
		data, err := parseUpstreamNewAPIUsageResponse([]byte(`{"code":true,"data":{"object":"token_usage","total_available":500000,"total_used":0,"total_granted":500000,"unlimited_quota":false,"expires_at":1783900800}}`))
		require.NoError(t, err)
		require.Equal(t, "2026-07-13T00:00:00Z", data["expires_at"])
	})
	for name, body := range map[string]string{
		"not json":              `<html>login</html>`,
		"other object":          `{"code":true,"data":{"object":"list","total_available":1}}`,
		"missing data":          `{"code":true,"message":"ok"}`,
		"sub2api usage payload": upstreamKindTestSub2APIUse,
		"no balance at all":     `{"code":true,"data":{"object":"token_usage","unlimited_quota":false}}`,
	} {
		t.Run("rejects "+name, func(t *testing.T) {
			_, err := parseUpstreamNewAPIUsageResponse([]byte(body))
			require.Error(t, err)
		})
	}
}

func TestUpstreamKindProbeRecognisesOpenAICompatibleBilling(t *testing.T) {
	upstream := &upstreamKindRouteStub{routes: map[string]upstreamKindRoute{
		upstreamLegacySubscriptionPath: {http.StatusOK, `{"object":"billing_subscription","hard_limit_usd":20,"soft_limit_usd":20,"system_hard_limit_usd":20,"access_until":0}`},
		upstreamLegacyUsagePath:        {http.StatusOK, `{"object":"list","total_usage":750}`},
	}}

	snapshot := runUpstreamKindProbe(t, newUpstreamBalanceProbeTestAccount(74, nil), upstream)

	require.Equal(t, UpstreamKindOpenAICompatible, snapshot.UpstreamKind)
	require.Equal(t, UpstreamBalanceSourceLegacySubscription, snapshot.Balance.Source)
	data := snapshot.Balance.Data
	// hard_limit_usd is the granted total; total_usage is in cents.
	require.InDelta(t, 20.0, data["quota_limit"], 1e-9)
	require.InDelta(t, 7.5, data["quota_used"], 1e-9)
	require.InDelta(t, 12.5, data["remaining"], 1e-9)
	require.NotContains(t, data, "unit_assumed")
}

func TestParseUpstreamLegacySubscriptionResponse(t *testing.T) {
	t.Run("without usage the granted total is not presented as the remaining balance", func(t *testing.T) {
		data, err := parseUpstreamLegacySubscriptionResponse([]byte(`{"hard_limit_usd":20}`), nil)
		require.NoError(t, err)
		require.InDelta(t, 20.0, data["quota_limit"], 1e-9)
		require.NotContains(t, data, "remaining")
	})
	t.Run("the unlimited sentinel is not a balance", func(t *testing.T) {
		data, err := parseUpstreamLegacySubscriptionResponse([]byte(`{"hard_limit_usd":100000000}`), []byte(`{"total_usage":12345}`))
		require.NoError(t, err)
		require.Equal(t, true, data["unlimited"])
		require.NotContains(t, data, "remaining")
		require.NotContains(t, data, "quota_limit")
	})
	t.Run("usage above the limit clamps to zero", func(t *testing.T) {
		data, err := parseUpstreamLegacySubscriptionResponse([]byte(`{"hard_limit_usd":5}`), []byte(`{"total_usage":900}`))
		require.NoError(t, err)
		require.Equal(t, 0.0, data["remaining"])
	})
	t.Run("rejects a payload without hard_limit_usd", func(t *testing.T) {
		_, err := parseUpstreamLegacySubscriptionResponse([]byte(`{"object":"list","data":[]}`), nil)
		require.Error(t, err)
	})
}

func TestUpstreamKindProbeMarksUnknownUpstreamCustom(t *testing.T) {
	// A relay that answers every unknown path with 200 and its own envelope must
	// not be mistaken for any known software.
	generic := upstreamKindRoute{http.StatusOK, `{"success":false,"message":"unknown route"}`}
	upstream := &upstreamKindRouteStub{routes: map[string]upstreamKindRoute{
		upstreamKindTestBillingPath:    generic,
		upstreamKindTestUsagePath:      generic,
		upstreamNewAPIUsagePath:        generic,
		upstreamLegacySubscriptionPath: generic,
	}}

	snapshot := runUpstreamKindProbe(t, newUpstreamBalanceProbeTestAccount(75, nil), upstream)

	require.Equal(t, UpstreamKindCustom, snapshot.UpstreamKind)
	require.NotNil(t, snapshot.Balance)
	require.Equal(t, UpstreamBillingProbeStatusUnsupported, snapshot.Balance.Status)
	require.Nil(t, snapshot.Balance.Data)
}

func TestUpstreamKindProbeCustomUpstreamKeepsStretchedDelay(t *testing.T) {
	upstream := &upstreamKindRouteStub{}

	snapshot := runUpstreamKindProbe(t, newUpstreamBalanceProbeTestAccount(76, nil), upstream)

	require.Equal(t, UpstreamKindCustom, snapshot.UpstreamKind)
	require.Equal(t, UpstreamBillingProbeStatusUnsupported, snapshot.Status)
	// Nothing readable: the stretched delay keeps a custom upstream from taking
	// a slot in every cycle.
	requireStretchedProbeInterval(t, snapshot)
}

// A rejected key must not be replayed against more paths: relays count
// rejected keys against the caller's IP.
func TestUpstreamKindProbeStopsWhenTheKeyIsRejected(t *testing.T) {
	for _, status := range []int{http.StatusUnauthorized, http.StatusForbidden, http.StatusTooManyRequests} {
		upstream := &upstreamKindRouteStub{routes: map[string]upstreamKindRoute{
			upstreamKindTestUsagePath: {status, `{"error":"rejected"}`},
			upstreamNewAPIUsagePath:   {http.StatusOK, upstreamKindTestNewAPIUsage},
		}}

		snapshot := runUpstreamKindProbe(t, newUpstreamBalanceProbeTestAccount(77, nil), upstream)

		require.Empty(t, snapshot.UpstreamKind, "status %d: a rejected key identifies nothing", status)
		require.Equal(t, []string{upstreamKindTestBillingPath, upstreamKindTestUsagePath}, upstream.requested(), "status %d", status)
	}
}

func TestUpstreamKindProbeKnownNewAPIAsksOnlyItsOwnEndpoint(t *testing.T) {
	extra := map[string]any{UpstreamBillingProbeExtraKey: map[string]any{
		"status": "unsupported", "last_attempt_at": "2026-07-13T01:00:00Z", "next_probe_at": "2026-07-13T01:30:00Z",
		"upstream_kind": UpstreamKindNewAPI,
	}}
	upstream := &upstreamKindRouteStub{routes: map[string]upstreamKindRoute{
		upstreamNewAPIUsagePath: {http.StatusOK, upstreamKindTestNewAPIUsage},
	}}

	snapshot := runUpstreamKindProbe(t, newUpstreamBalanceProbeTestAccount(78, extra), upstream)

	require.Equal(t, UpstreamKindNewAPI, snapshot.UpstreamKind)
	require.Equal(t, UpstreamBillingProbeStatusOK, snapshot.Balance.Status)
	require.Equal(t, []string{upstreamKindTestBillingPath, upstreamNewAPIUsagePath}, upstream.requested())
}

// A timeout says nothing about which software the upstream runs.
func TestUpstreamKindProbeKeepsKindAcrossATransientFailure(t *testing.T) {
	extra := map[string]any{UpstreamBillingProbeExtraKey: map[string]any{
		"status": "unsupported", "last_attempt_at": "2026-07-13T01:00:00Z", "next_probe_at": "2026-07-13T01:30:00Z",
		"upstream_kind": UpstreamKindNewAPI,
		"balance": map[string]any{
			"status": "ok", "data": map[string]any{"is_valid": true, "remaining": 7.6},
			"received_at": "2026-07-13T01:00:00Z", "fresh_until": "2026-07-13T02:00:00Z", "last_attempt_at": "2026-07-13T01:00:00Z",
			"source": UpstreamBalanceSourceNewAPITokenUsage,
		},
	}}
	upstream := &upstreamKindRouteStub{routes: map[string]upstreamKindRoute{
		upstreamNewAPIUsagePath: {http.StatusBadGateway, `bad gateway`},
	}}

	snapshot := runUpstreamKindProbe(t, newUpstreamBalanceProbeTestAccount(79, extra), upstream)

	require.Equal(t, UpstreamKindNewAPI, snapshot.UpstreamKind)
	require.Equal(t, UpstreamBillingProbeStatusFailed, snapshot.Balance.Status)
	require.Equal(t, 7.6, snapshot.Balance.Data["remaining"], "the last good balance stays readable")
}

// The software behind a domain can change. When the known endpoint is gone the
// kind is dropped, so the next probe identifies the upstream again.
func TestUpstreamKindProbeDropsKindWhenItsEndpointDisappears(t *testing.T) {
	extra := map[string]any{UpstreamBillingProbeExtraKey: map[string]any{
		"status": "unsupported", "last_attempt_at": "2026-07-13T01:00:00Z", "next_probe_at": "2026-07-13T01:30:00Z",
		"upstream_kind": UpstreamKindNewAPI,
	}}
	account := newUpstreamBalanceProbeTestAccount(80, extra)

	first := runUpstreamKindProbe(t, account, &upstreamKindRouteStub{})
	require.Empty(t, first.UpstreamKind)

	second := runUpstreamKindProbe(t, account, &upstreamKindRouteStub{routes: map[string]upstreamKindRoute{
		upstreamKindTestUsagePath: {http.StatusOK, upstreamKindTestSub2APIUse},
	}})
	require.Equal(t, UpstreamKindSub2API, second.UpstreamKind)
}

func TestUpstreamKindProbeOfficialDomainSendsNothing(t *testing.T) {
	account := newUpstreamBalanceProbeTestAccount(81, nil)
	account.Platform = PlatformAnthropic
	account.Credentials["base_url"] = "https://api.anthropic.com"
	upstream := &upstreamKindRouteStub{}

	snapshot := runUpstreamKindProbe(t, account, upstream)

	require.Equal(t, UpstreamKindOfficial, snapshot.UpstreamKind)
	require.Empty(t, upstream.requested(), "the account key must never be sent to an official API for this")
}

// Identifying an upstream must never move the account's rate.
func TestUpstreamKindProbeDoesNotSyncRateForUnconfiguredAccount(t *testing.T) {
	upstream := &upstreamKindRouteStub{routes: map[string]upstreamKindRoute{
		upstreamKindTestBillingPath: {http.StatusOK, upstreamKindTestSub2APIRate},
		upstreamKindTestUsagePath:   {http.StatusOK, upstreamKindTestSub2APIUse},
	}}
	account := newUpstreamBalanceProbeTestAccount(82, nil)
	before := account.BillingRateMultiplier()

	snapshot := runUpstreamKindProbe(t, account, upstream)

	require.Equal(t, UpstreamKindSub2API, snapshot.UpstreamKind)
	require.Nil(t, snapshot.SyncedRateMultiplier)
	require.Equal(t, before, account.BillingRateMultiplier())
}

func TestUpstreamBillingProbeScheduledStates(t *testing.T) {
	apiKey := func(extra map[string]any) *Account {
		return &Account{Platform: PlatformOpenAI, Type: AccountTypeAPIKey, Extra: extra}
	}
	require.True(t, upstreamBillingProbeScheduled(apiKey(nil)), "never decided: auto-eligible")
	require.True(t, upstreamBillingProbeScheduled(apiKey(map[string]any{"other": 1})), "never decided: auto-eligible")
	require.True(t, upstreamBillingProbeScheduled(apiKey(map[string]any{UpstreamBillingProbeEnabledExtraKey: true})))
	require.False(t, upstreamBillingProbeScheduled(apiKey(map[string]any{UpstreamBillingProbeEnabledExtraKey: false})), "an explicit off is never overridden")
	require.False(t, upstreamBillingProbeScheduled(&Account{Platform: PlatformOpenAI, Type: AccountTypeOAuth}), "only API-key accounts hold a key to present")

	// Rate sync still needs both switches; being auto-eligible grants nothing.
	require.False(t, upstreamBillingRateSyncEnabled(apiKey(nil)))
	require.False(t, upstreamBillingRateSyncEnabled(apiKey(map[string]any{UpstreamBillingRateSyncEnabledExtraKey: true})))
}

func TestNormalizeUpstreamKind(t *testing.T) {
	for _, kind := range []string{UpstreamKindSub2API, UpstreamKindNewAPI, UpstreamKindOpenAICompatible, UpstreamKindOfficial, UpstreamKindCustom} {
		require.Equal(t, kind, normalizeUpstreamKind(kind))
	}
	require.Empty(t, normalizeUpstreamKind("<script>"))
	require.Empty(t, normalizeUpstreamKind(""))
}
