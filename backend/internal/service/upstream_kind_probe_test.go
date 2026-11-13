package service

import (
	"context"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/tlsfingerprint"
	"github.com/stretchr/testify/require"
)

// upstreamKindRouteStub answers by request path, so a test states which
// endpoint returns what instead of relying on call order. Unlisted paths
// answer 404. Every requested path is recorded in order. headers carries the
// response headers a recorded answer had, keyed by path.
type upstreamKindRouteStub struct {
	mu      sync.Mutex
	routes  map[string]upstreamKindRoute
	headers map[string]http.Header
	paths   []string
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
	header := http.Header{"Content-Type": []string{"application/json"}}
	for name, values := range u.headers[req.URL.Path] {
		header[name] = values
	}
	return &http.Response{
		StatusCode: route.status,
		Header:     header,
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

	// The fingerprints a new-api site adds on top of the OpenAI-compatible
	// billing endpoints it serves for compatibility.
	upstreamKindTestNewAPIHeaderName  = "X-New-Api-Version"
	upstreamKindTestNewAPIHeaderValue = "bidnew-v1.0.0-rc.47.22-performance-20261111"
	upstreamKindTestNewAPIStatus      = `{"success":true,"data":{"version":"v0.8.6","start_time":1735000000,"quota_per_unit":500000,"display_in_currency":true,"enable_data_export":true}}`

	// This is exactly what account #34761 showed: the legacy billing endpoint
	// answers with new-api's unlimited sentinel, so the account read as
	// "OpenAI billing, unlimited" although the site is new-api.
	upstreamKindTestUnlimitedBilling = `{"object":"billing_subscription","hard_limit_usd":100000000,"soft_limit_usd":100000000,"system_hard_limit_usd":100000000,"access_until":0}`
	upstreamKindTestBillingUsage     = `{"object":"list","total_usage":5294839}`
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
	// No rate endpoint, but the balance endpoint answered: the upstream does
	// support balance queries, so the snapshot must not report "unsupported".
	require.Equal(t, UpstreamBillingProbeStatusOK, snapshot.Status)
	require.Equal(t, UpstreamBillingProbeStatusOK, snapshot.Balance.Status)
	// A readable balance keeps the normal interval, not the stretched one.
	requireNormalProbeInterval(t, snapshot)
}

// The probe delay is jittered by up to a fifth of the interval (capped at five
// minutes), so a normal interval is asserted as 30 minutes ± 5. An unsupported
// or balanceless account uses the fixed ten-minute retry instead.
func requireNormalProbeInterval(t *testing.T, snapshot *UpstreamBillingProbeSnapshot) {
	t.Helper()
	delay := snapshot.NextProbeAt.Sub(snapshot.LastAttemptAt)
	require.GreaterOrEqual(t, delay, 25*time.Minute)
	require.LessOrEqual(t, delay, 35*time.Minute)
}

func requireTenMinuteProbeInterval(t *testing.T, snapshot *UpstreamBillingProbeSnapshot) {
	t.Helper()
	require.Equal(t, upstreamBillingProbeUnsupportedRetryInterval, snapshot.NextProbeAt.Sub(snapshot.LastAttemptAt))
}

func TestUpstreamKindProbeRecognisesNewAPI(t *testing.T) {
	upstream := &upstreamKindRouteStub{routes: map[string]upstreamKindRoute{
		upstreamNewAPIUsagePath: {http.StatusOK, upstreamKindTestNewAPIUsage},
	}}

	snapshot := runUpstreamKindProbe(t, newUpstreamBalanceProbeTestAccount(73, nil), upstream)

	require.Equal(t, UpstreamKindNewAPI, snapshot.UpstreamKind)
	// /v1/sub2api/billing answers 404 on every new-api site, so Status cannot be
	// left "unsupported" once /api/usage/token/ has been parsed: the account page
	// gates the balance on it and used to hide a balance it had just read.
	require.Equal(t, UpstreamBillingProbeStatusOK, snapshot.Status)
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
	t.Run("the unlimited sentinel is not an OpenAI balance", func(t *testing.T) {
		_, err := parseUpstreamLegacySubscriptionResponse([]byte(`{"hard_limit_usd":100000000}`), []byte(`{"total_usage":12345}`))
		require.Error(t, err, "the 1e8 marker identifies new-api and must never be presented as a granted total")
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

func TestUpstreamKindProbeCustomUpstreamRetriesEveryTenMinutes(t *testing.T) {
	upstream := &upstreamKindRouteStub{}

	snapshot := runUpstreamKindProbe(t, newUpstreamBalanceProbeTestAccount(76, nil), upstream)

	require.Equal(t, UpstreamKindCustom, snapshot.UpstreamKind)
	require.Equal(t, UpstreamBillingProbeStatusUnsupported, snapshot.Status)
	// Nothing readable: the fixed ten-minute cadence still re-identifies the
	// upstream, so a site that later exposes a balance endpoint is picked up.
	requireTenMinuteProbeInterval(t, snapshot)
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
	// A recognised upstream never reports a failed read any more: the last good
	// balance stays on screen while the retry rounds continue.
	require.Equal(t, UpstreamBillingProbeStatusOK, snapshot.Balance.Status)
	require.Equal(t, 1, snapshot.Balance.RetryCount)
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

// Account #34761: a new-api site answered the legacy OpenAI billing endpoints
// for compatibility, so it was reported as "OpenAI billing" with the unlimited
// sentinel presented as a balance. The version header it puts on every answer
// has to outrank that payload.
func TestUpstreamKindProbeNewAPIHeaderOutranksBilling(t *testing.T) {
	upstream := &upstreamKindRouteStub{
		routes: map[string]upstreamKindRoute{
			upstreamLegacySubscriptionPath: {http.StatusOK, upstreamKindTestUnlimitedBilling},
			upstreamLegacyUsagePath:        {http.StatusOK, upstreamKindTestBillingUsage},
		},
		headers: map[string]http.Header{
			upstreamLegacySubscriptionPath: {upstreamKindTestNewAPIHeaderName: []string{upstreamKindTestNewAPIHeaderValue}},
		},
	}

	snapshot := runUpstreamKindProbe(t, newUpstreamBalanceProbeTestAccount(83, nil), upstream)

	require.Equal(t, UpstreamKindNewAPI, snapshot.UpstreamKind)
	require.NotNil(t, snapshot.Balance)
	require.Equal(t, UpstreamBillingProbeStatusUnsupported, snapshot.Balance.Status,
		"no balance endpoint answered, so there is no balance to show")
	require.NotEqual(t, UpstreamBalanceSourceLegacySubscription, snapshot.Balance.Source)
	// The billing payload's 1e8 sentinel must not survive as a limit, and it
	// must not become "unlimited". The spend it reported is still real.
	require.NotContains(t, snapshot.Balance.Data, "unlimited")
	require.NotContains(t, snapshot.Balance.Data, "quota_limit")
	require.NotContains(t, snapshot.Balance.Data, "remaining")
	require.InDelta(t, 52948.39, snapshot.Balance.Data["quota_used"], 1e-9)
	require.Contains(t, upstream.requested(), upstreamLegacySubscriptionPath,
		"the billing endpoint did answer, and the fingerprint still outranked it")
}

// The same fingerprint, with a working usage endpoint: the balance must come
// from /api/usage/token/, not from the billing payload that also answered.
func TestUpstreamKindProbeNewAPIHeaderReadsBalanceFromUsage(t *testing.T) {
	upstream := &upstreamKindRouteStub{
		routes: map[string]upstreamKindRoute{
			upstreamNewAPIUsagePath:        {http.StatusOK, upstreamKindTestNewAPIUsage},
			upstreamLegacySubscriptionPath: {http.StatusOK, upstreamKindTestUnlimitedBilling},
			upstreamLegacyUsagePath:        {http.StatusOK, upstreamKindTestBillingUsage},
		},
		headers: map[string]http.Header{
			upstreamLegacySubscriptionPath: {upstreamKindTestNewAPIHeaderName: []string{upstreamKindTestNewAPIHeaderValue}},
		},
	}

	snapshot := runUpstreamKindProbe(t, newUpstreamBalanceProbeTestAccount(84, nil), upstream)

	require.Equal(t, UpstreamKindNewAPI, snapshot.UpstreamKind)
	require.Equal(t, UpstreamBillingProbeStatusOK, snapshot.Balance.Status)
	require.Equal(t, UpstreamBalanceSourceNewAPITokenUsage, snapshot.Balance.Source)
	require.InDelta(t, 7.6, snapshot.Balance.Data["remaining"], 1e-9)
	require.NotContains(t, snapshot.Balance.Data, "unlimited")
	require.NotContains(t, upstream.requested(), upstreamLegacySubscriptionPath,
		"the billing payload is never read once the usage endpoint identified new-api")
}

func TestUpstreamKindProbeOneAPIRequestIDOutranksBilling(t *testing.T) {
	upstream := &upstreamKindRouteStub{
		routes: map[string]upstreamKindRoute{
			upstreamLegacySubscriptionPath: {http.StatusOK, upstreamKindTestUnlimitedBilling},
		},
		headers: map[string]http.Header{
			upstreamLegacySubscriptionPath: {"X-Oneapi-Request-Id": []string{"20261112093000abcdef"}},
		},
	}

	snapshot := runUpstreamKindProbe(t, newUpstreamBalanceProbeTestAccount(85, nil), upstream)

	require.Equal(t, UpstreamKindNewAPI, snapshot.UpstreamKind)
	require.NotContains(t, snapshot.Balance.Data, "unlimited")
}

// /api/status is the fingerprint of last resort: it needs no key, and it still
// identifies new-api when the usage endpoint is gone and no header is set. The
// billing answer carries a normal limit here, so the sentinel fingerprint does
// not fire and /api/status is what decides.
func TestUpstreamKindProbeNewAPIStatusOutranksBilling(t *testing.T) {
	upstream := &upstreamKindRouteStub{routes: map[string]upstreamKindRoute{
		upstreamNewAPIStatusPath:       {http.StatusOK, upstreamKindTestNewAPIStatus},
		upstreamLegacySubscriptionPath: {http.StatusOK, `{"object":"billing_subscription","hard_limit_usd":20,"soft_limit_usd":20,"system_hard_limit_usd":20,"access_until":0}`},
		upstreamLegacyUsagePath:        {http.StatusOK, upstreamKindTestBillingUsage},
	}}

	snapshot := runUpstreamKindProbe(t, newUpstreamBalanceProbeTestAccount(86, nil), upstream)

	require.Equal(t, UpstreamKindNewAPI, snapshot.UpstreamKind)
	require.Equal(t, UpstreamBillingProbeStatusUnsupported, snapshot.Balance.Status)
	require.NotContains(t, snapshot.Balance.Data, "unlimited")
	require.NotContains(t, snapshot.Balance.Data, "quota_limit")
	require.Equal(t, true, snapshot.Balance.Data["balance_unavailable"])
	require.Contains(t, upstream.requested(), upstreamNewAPIStatusPath)
}

// new-api types its error envelopes, so even a missing endpoint is evidence.
func TestUpstreamKindProbeNewAPIErrorEnvelopeOutranksBilling(t *testing.T) {
	upstream := &upstreamKindRouteStub{routes: map[string]upstreamKindRoute{
		upstreamNewAPIUsagePath:        {http.StatusNotFound, `{"error":{"message":"not found","type":"new_api_error"}}`},
		upstreamLegacySubscriptionPath: {http.StatusOK, upstreamKindTestUnlimitedBilling},
		upstreamLegacyUsagePath:        {http.StatusOK, upstreamKindTestBillingUsage},
	}}

	snapshot := runUpstreamKindProbe(t, newUpstreamBalanceProbeTestAccount(87, nil), upstream)

	require.Equal(t, UpstreamKindNewAPI, snapshot.UpstreamKind)
	require.NotContains(t, snapshot.Balance.Data, "unlimited")
}

// A snapshot already written as openai_compatible must be rewritten on the next
// probe: the stored kind is not proof of anything.
func TestUpstreamKindProbeRewritesStaleOpenAIBillingSnapshot(t *testing.T) {
	extra := func() map[string]any {
		return map[string]any{UpstreamBillingProbeExtraKey: map[string]any{
			"status": "ok", "last_attempt_at": "2026-07-13T01:00:00Z", "next_probe_at": "2026-07-13T01:30:00Z",
			"upstream_kind": UpstreamKindOpenAICompatible,
			"balance": map[string]any{
				"status":          "ok",
				"data":            map[string]any{"is_valid": true, "unlimited": true, "quota_used": 52948.39},
				"received_at":     "2026-07-13T01:00:00Z",
				"fresh_until":     "2026-07-13T02:00:00Z",
				"last_attempt_at": "2026-07-13T01:00:00Z",
				"source":          UpstreamBalanceSourceLegacySubscription,
			},
		}}
	}

	t.Run("fingerprint on the billing answer", func(t *testing.T) {
		upstream := &upstreamKindRouteStub{
			routes: map[string]upstreamKindRoute{
				upstreamLegacySubscriptionPath: {http.StatusOK, upstreamKindTestUnlimitedBilling},
				upstreamLegacyUsagePath:        {http.StatusOK, upstreamKindTestBillingUsage},
			},
			headers: map[string]http.Header{
				upstreamLegacySubscriptionPath: {upstreamKindTestNewAPIHeaderName: []string{upstreamKindTestNewAPIHeaderValue}},
			},
		}

		snapshot := runUpstreamKindProbe(t, newUpstreamBalanceProbeTestAccount(88, extra()), upstream)

		require.Equal(t, UpstreamKindNewAPI, snapshot.UpstreamKind)
		require.NotEqual(t, UpstreamBalanceSourceLegacySubscription, snapshot.Balance.Source)
		require.NotContains(t, snapshot.Balance.Data, "unlimited")
	})

	t.Run("usage endpoint answers", func(t *testing.T) {
		upstream := &upstreamKindRouteStub{routes: map[string]upstreamKindRoute{
			upstreamNewAPIUsagePath:        {http.StatusOK, upstreamKindTestNewAPIUsage},
			upstreamLegacySubscriptionPath: {http.StatusOK, upstreamKindTestUnlimitedBilling},
			upstreamLegacyUsagePath:        {http.StatusOK, upstreamKindTestBillingUsage},
		}}

		snapshot := runUpstreamKindProbe(t, newUpstreamBalanceProbeTestAccount(89, extra()), upstream)

		require.Equal(t, UpstreamKindNewAPI, snapshot.UpstreamKind)
		require.Equal(t, UpstreamBalanceSourceNewAPITokenUsage, snapshot.Balance.Source)
		require.InDelta(t, 7.6, snapshot.Balance.Data["remaining"], 1e-9)
		require.NotContains(t, snapshot.Balance.Data, "unlimited")
	})
}

// Account #34747: the legacy billing endpoint answers with new-api's 1e8
// sentinel, but /api/status is 404 and no answer carries a new-api header. The
// sentinel alone has to identify the site, and its limit must not become a
// balance: the spend stays visible, the limit and "unlimited" do not.
func TestUpstreamKindProbeBillingSentinelIdentifiesNewAPI(t *testing.T) {
	upstream := &upstreamKindRouteStub{routes: map[string]upstreamKindRoute{
		upstreamLegacySubscriptionPath: {http.StatusOK, upstreamKindTestUnlimitedBilling},
		upstreamLegacyUsagePath:        {http.StatusOK, upstreamKindTestBillingUsage},
	}}

	snapshot := runUpstreamKindProbe(t, newUpstreamBalanceProbeTestAccount(91, nil), upstream)

	require.Equal(t, UpstreamKindNewAPI, snapshot.UpstreamKind)
	require.NotNil(t, snapshot.Balance)
	require.Equal(t, UpstreamBillingProbeStatusUnsupported, snapshot.Balance.Status)
	require.NotContains(t, snapshot.Balance.Data, "unlimited")
	require.NotContains(t, snapshot.Balance.Data, "quota_limit")
	require.NotContains(t, snapshot.Balance.Data, "remaining")
	require.InDelta(t, 52948.39, snapshot.Balance.Data["quota_used"], 1e-9)
	require.Equal(t, true, snapshot.Balance.Data["balance_unavailable"])
	// No balance, but the account is still re-probed every ten minutes.
	requireTenMinuteProbeInterval(t, snapshot)
	require.NotContains(t, upstream.requested(), upstreamNewAPIStatusPath,
		"the sentinel is enough evidence; /api/status must not be asked")
}

func TestUpstreamLegacyBillingSentinel(t *testing.T) {
	for name, tc := range map[string]struct {
		body string
		want bool
	}{
		"hard limit":   {`{"hard_limit_usd":100000000}`, true},
		"soft limit":   {`{"soft_limit_usd":100000000,"hard_limit_usd":20}`, true},
		"system limit": {`{"system_hard_limit_usd":150000000}`, true},
		"real limit":   {`{"hard_limit_usd":20,"soft_limit_usd":20,"system_hard_limit_usd":20}`, false},
		"html":         {`<html>login</html>`, false},
		"empty":        {``, false},
	} {
		t.Run(name, func(t *testing.T) {
			require.Equal(t, tc.want, upstreamLegacyBillingIsNewAPISentinel([]byte(tc.body)))
		})
	}
}

// Account #34760: /api/usage/token/ answers 302. The probe follows a
// same-origin hop and reads the balance from the document it lands on.
func TestUpstreamKindProbeNewAPIUsageFollowsSameOriginRedirect(t *testing.T) {
	upstream := &upstreamKindRouteStub{
		routes: map[string]upstreamKindRoute{
			upstreamNewAPIUsagePath: {http.StatusFound, ``},
			"/api/usage/token":      {http.StatusOK, upstreamKindTestNewAPIUsage},
		},
		headers: map[string]http.Header{
			upstreamNewAPIUsagePath: {"Location": []string{"/api/usage/token"}},
		},
	}

	snapshot := runUpstreamKindProbe(t, newUpstreamBalanceProbeTestAccount(92, nil), upstream)

	require.Equal(t, UpstreamKindNewAPI, snapshot.UpstreamKind)
	require.NotNil(t, snapshot.Balance)
	require.Equal(t, UpstreamBillingProbeStatusOK, snapshot.Balance.Status)
	require.Equal(t, UpstreamBalanceSourceNewAPITokenUsage, snapshot.Balance.Source)
	require.InDelta(t, 7.6, snapshot.Balance.Data["remaining"], 1e-9)
	require.Contains(t, upstream.requested(), "/api/usage/token")
}

// A redirect to a login page can only answer with a session form, so it is
// treated as "balance unavailable" without following the hop.
func TestUpstreamKindProbeNewAPIUsageLoginRedirectIsUnavailable(t *testing.T) {
	upstream := &upstreamKindRouteStub{
		routes: map[string]upstreamKindRoute{
			upstreamNewAPIUsagePath:        {http.StatusFound, ``},
			upstreamLegacySubscriptionPath: {http.StatusOK, upstreamKindTestUnlimitedBilling},
			upstreamLegacyUsagePath:        {http.StatusOK, upstreamKindTestBillingUsage},
		},
		headers: map[string]http.Header{
			upstreamNewAPIUsagePath:        {"Location": []string{"/login?redirect=%2Fapi%2Fusage%2Ftoken%2F"}},
			upstreamLegacySubscriptionPath: {upstreamKindTestNewAPIHeaderName: []string{upstreamKindTestNewAPIHeaderValue}},
		},
	}

	snapshot := runUpstreamKindProbe(t, newUpstreamBalanceProbeTestAccount(93, nil), upstream)

	require.Equal(t, UpstreamKindNewAPI, snapshot.UpstreamKind)
	require.NotNil(t, snapshot.Balance)
	require.Equal(t, UpstreamBillingProbeStatusUnsupported, snapshot.Balance.Status)
	require.NotContains(t, snapshot.Balance.Data, "unlimited")
	require.NotContains(t, snapshot.Balance.Data, "remaining")
	require.InDelta(t, 52948.39, snapshot.Balance.Data["quota_used"], 1e-9)
	require.NotContains(t, upstream.requested(), "/login", "the probe must not fetch a login page")
}

// An already-recognised new-api upstream whose balance endpoint has disappeared
// retries in ten minutes, not in the 1s/3s/9s recovery loop.
func TestUpstreamKindProbeRecognisedNewAPIWithoutBalanceRetriesInTenMinutes(t *testing.T) {
	extra := map[string]any{UpstreamBillingProbeExtraKey: map[string]any{
		"status": "unsupported", "last_attempt_at": "2026-07-13T01:00:00Z", "next_probe_at": "2026-07-13T02:00:00Z",
		"upstream_kind": UpstreamKindNewAPI,
	}}
	upstream := &upstreamKindRouteStub{}

	snapshot := runUpstreamKindProbe(t, newUpstreamBalanceProbeTestAccount(94, extra), upstream)

	require.NotNil(t, snapshot.Balance)
	require.Equal(t, UpstreamBillingProbeStatusUnsupported, snapshot.Balance.Status)
	requireTenMinuteProbeInterval(t, snapshot)
}

func TestUpstreamProbeRedirectTarget(t *testing.T) {
	current, err := url.Parse("https://relay.example/api/usage/token/")
	require.NoError(t, err)
	header := func(location string) http.Header {
		if location == "" {
			return http.Header{}
		}
		return http.Header{"Location": []string{location}}
	}
	target, ok := upstreamProbeRedirectTarget(current, upstreamBillingProbeHTTPResult{
		statusCode: http.StatusFound,
		header:     header("/api/usage/token"),
	})
	require.True(t, ok)
	require.Equal(t, "https://relay.example/api/usage/token", target.String())

	for name, tc := range map[string]struct {
		status   int
		location string
	}{
		"cross origin":      {http.StatusFound, "https://evil.example/api/usage/token"},
		"scheme downgrade":  {http.StatusFound, "http://relay.example/api/usage/token"},
		"login page":        {http.StatusFound, "/login"},
		"sign-in page":      {http.StatusFound, "/signin?next=/api"},
		"no location":       {http.StatusFound, ""},
		"not a redirect":    {http.StatusOK, "/api/usage/token"},
		"transport failure": {http.StatusFound, "/api/usage/token"},
	} {
		t.Run(name, func(t *testing.T) {
			result := upstreamBillingProbeHTTPResult{statusCode: tc.status, header: header(tc.location)}
			if name == "transport failure" {
				result.failure = "request_failed"
			}
			_, ok := upstreamProbeRedirectTarget(current, result)
			require.False(t, ok)
		})
	}
}

// When the operator saved a custom balance extractor, it is the second source
// for a new-api site whose /api/usage/token/ is gone. The probe's own endpoints
// are always tried first.
func TestUpstreamKindProbeFallsBackToConfiguredExtractor(t *testing.T) {
	account := newUpstreamBalanceProbeTestAccount(95, map[string]any{UpstreamBillingProbeEnabledExtraKey: true})
	upstream := &upstreamKindRouteStub{routes: map[string]upstreamKindRoute{
		upstreamLegacySubscriptionPath: {http.StatusOK, upstreamKindTestUnlimitedBilling},
		upstreamLegacyUsagePath:        {http.StatusOK, upstreamKindTestBillingUsage},
	}}
	repo := &upstreamBillingProbeAccountRepo{accounts: map[int64]*Account{account.ID: account}}
	svc := newUpstreamBillingProbeTestService(repo, upstream, &upstreamBillingProbeSettingRepo{})
	now := time.Date(2026, time.July, 13, 1, 30, 0, 0, time.UTC)
	svc.now = func() time.Time { return now }

	usageRepo := &customUsageTestRepo{accounts: map[int64]*Account{account.ID: account}}
	usage := NewCustomUsageService(usageRepo)
	usage.now = func() time.Time { return now }
	usage.client.Transport = customUsageRoundTrip(func(*http.Request) (*http.Response, error) {
		return customUsageResponse(`{"remaining":12.5}`, http.StatusOK), nil
	})
	cfg := customUsageDefaults()
	cfg.Enabled = true
	cfg.BaseURL = "https://billing.example.com"
	cfg.TimeoutSeconds = 5
	cfg.IntervalMinutes = 10
	cfg.Request.URL = "{{baseUrl}}/balance"
	cfg.Request.Headers = map[string]string{"Authorization": "Bearer {{apiKey}}"}
	cfg.Extractor.Remaining = &CustomUsageNumber{Path: "remaining"}
	cfg.Extractor.Unit = &CustomUsageText{Value: "USD"}
	_, err := usage.PutConfig(context.Background(), account.ID, cfg)
	require.NoError(t, err)

	ops := &AccountOpsService{}
	ops.SetCustomUsageService(usage)
	svc.SetAccountOpsService(ops)

	snapshot, err := svc.probeScheduledAccount(context.Background(), account.ID, 30)
	require.NoError(t, err)
	require.Equal(t, UpstreamKindNewAPI, snapshot.UpstreamKind)
	require.NotNil(t, snapshot.Balance)
	require.Equal(t, UpstreamBillingProbeStatusOK, snapshot.Balance.Status)
	require.Equal(t, UpstreamBalanceSourceCustomUsage, snapshot.Balance.Source)
	require.InDelta(t, 12.5, snapshot.Balance.Data["remaining"], 1e-9)
}

// A recognised new-api upstream whose balance endpoint is still gone keeps the
// spend it already read and says "not provided", rather than flipping to
// "reading" every other round. The ten-minute cadence applies on both rounds.
func TestUpstreamKindProbeKeepsSpendWhenRecognisedNewAPIBalanceStaysUnavailable(t *testing.T) {
	account := newUpstreamBalanceProbeTestAccount(96, nil)
	upstream := &upstreamKindRouteStub{routes: map[string]upstreamKindRoute{
		upstreamLegacySubscriptionPath: {http.StatusOK, upstreamKindTestUnlimitedBilling},
		upstreamLegacyUsagePath:        {http.StatusOK, upstreamKindTestBillingUsage},
	}}

	first := runUpstreamKindProbe(t, account, upstream)
	require.Equal(t, UpstreamKindNewAPI, first.UpstreamKind)
	require.Equal(t, UpstreamBillingProbeStatusUnsupported, first.Balance.Status)
	require.InDelta(t, 52948.39, first.Balance.Data["quota_used"], 1e-9)

	// Second round: the stored kind sends the probe straight to the usage
	// endpoint, which is still 404. The spend must survive and the state must
	// stay "not provided", not become a never-ending "reading".
	second := runUpstreamKindProbe(t, account, upstream)
	require.Equal(t, UpstreamBillingProbeStatusUnsupported, second.Balance.Status)
	require.InDelta(t, 52948.39, second.Balance.Data["quota_used"], 1e-9)
	require.NotContains(t, second.Balance.Data, "remaining")
	requireTenMinuteProbeInterval(t, second)
}

// A stale billing snapshot on a site that really is an OpenAI-billing relay
// stays billing: the fingerprints are checked, and none of them holds.
func TestUpstreamKindProbeKeepsBillingWhenNoFingerprintHolds(t *testing.T) {
	extra := map[string]any{UpstreamBillingProbeExtraKey: map[string]any{
		"status": "ok", "last_attempt_at": "2026-07-13T01:00:00Z", "next_probe_at": "2026-07-13T01:30:00Z",
		"upstream_kind": UpstreamKindOpenAICompatible,
	}}
	upstream := &upstreamKindRouteStub{routes: map[string]upstreamKindRoute{
		upstreamLegacySubscriptionPath: {http.StatusOK, `{"object":"billing_subscription","hard_limit_usd":20,"soft_limit_usd":20,"system_hard_limit_usd":20,"access_until":0}`},
		upstreamLegacyUsagePath:        {http.StatusOK, `{"object":"list","total_usage":750}`},
	}}

	snapshot := runUpstreamKindProbe(t, newUpstreamBalanceProbeTestAccount(90, extra), upstream)

	require.Equal(t, UpstreamKindOpenAICompatible, snapshot.UpstreamKind)
	require.Equal(t, UpstreamBalanceSourceLegacySubscription, snapshot.Balance.Source)
	require.InDelta(t, 12.5, snapshot.Balance.Data["remaining"], 1e-9)
	// Every new-api fingerprint is checked before the billing payload decides,
	// and none of them held here.
	require.Equal(t, []string{
		upstreamKindTestBillingPath,
		upstreamKindTestUsagePath,
		upstreamNewAPIUsagePath,
		upstreamLegacySubscriptionPath,
		upstreamNewAPIStatusPath,
		upstreamLegacyUsagePath,
	}, upstream.requested())
}

func TestUpstreamNewAPIStatusEvidence(t *testing.T) {
	for name, tc := range map[string]struct{ body, want string }{
		"new-api fields": {upstreamKindTestNewAPIStatus, "status:newapi_fields"},
		"one-api fields": {`{"success":true,"data":{"version":"v0.6.11","start_time":1735000000}}`, "status:oneapi_fields"},
	} {
		t.Run("accepts "+name, func(t *testing.T) {
			signal, ok := upstreamNewAPIStatusEvidence([]byte(tc.body))
			require.True(t, ok)
			require.Equal(t, tc.want, signal)
		})
	}
	for name, body := range map[string]string{
		"generic json":       `{"success":false,"message":"unknown route"}`,
		"version only":       `{"data":{"version":"1.0"}}`,
		"html":               `<html><body>login</body></html>`,
		"new-api error body": `{"error":{"type":"new_api_error"}}`,
	} {
		t.Run("rejects "+name, func(t *testing.T) {
			_, ok := upstreamNewAPIStatusEvidence([]byte(body))
			require.False(t, ok)
		})
	}
}

func TestUpstreamNewAPIErrorEvidence(t *testing.T) {
	signal, ok := upstreamNewAPIErrorEvidence([]byte(`{"error":{"message":"not found","type":"new_api_error"}}`))
	require.True(t, ok)
	require.Equal(t, "body:new_api_error", signal)

	for name, body := range map[string]string{
		"plain json": `{"error":"not found"}`,
		"other type": `{"error":{"type":"invalid_request_error"}}`,
		"word only":  `the new_api_error marker in prose`,
		"html":       `<html>new_api_error</html>`,
	} {
		t.Run("rejects "+name, func(t *testing.T) {
			_, ok := upstreamNewAPIErrorEvidence([]byte(body))
			require.False(t, ok)
		})
	}
}

func TestUpstreamNewAPIHeaderEvidence(t *testing.T) {
	lowercase := http.Header{}
	lowercase.Set("x-new-api-version", "v1")
	for name, header := range map[string]http.Header{
		"version":   {"X-New-Api-Version": []string{upstreamKindTestNewAPIHeaderValue}},
		"request":   {"X-Oneapi-Request-Id": []string{"abc"}},
		"lowercase": lowercase,
	} {
		t.Run("accepts "+name, func(t *testing.T) {
			signal, ok := upstreamNewAPIHeaderEvidence(header)
			require.True(t, ok)
			require.Contains(t, signal, "header:")
		})
	}
	_, ok := upstreamNewAPIHeaderEvidence(http.Header{"Content-Type": []string{"application/json"}})
	require.False(t, ok)
	_, ok = upstreamNewAPIHeaderEvidence(nil)
	require.False(t, ok)
}
