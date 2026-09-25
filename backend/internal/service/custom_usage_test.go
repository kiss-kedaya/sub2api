package service

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"math"
	"net"
	"net/http"
	"net/netip"
	"reflect"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

type customUsageTestRepo struct {
	mu                     sync.Mutex
	accounts               map[int64]*Account
	reads, batches, writes int
	fail                   error
}

func (r *customUsageTestRepo) GetByID(_ context.Context, id int64) (*Account, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.reads++
	if r.fail != nil {
		return nil, r.fail
	}
	a := r.accounts[id]
	if a == nil {
		return nil, ErrAccountNotFound
	}
	out := *a
	out.Extra = shallowCopyMap(a.Extra)
	out.Credentials = shallowCopyMap(a.Credentials)
	return &out, nil
}
func (r *customUsageTestRepo) GetByIDs(ctx context.Context, ids []int64) ([]*Account, error) {
	r.mu.Lock()
	r.batches++
	r.mu.Unlock()
	var out []*Account
	for _, id := range ids {
		a, err := r.GetByID(ctx, id)
		if errors.Is(err, ErrAccountNotFound) {
			continue
		}
		if err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, nil
}
func (r *customUsageTestRepo) BulkUpdate(_ context.Context, ids []int64, u AccountBulkUpdate) (int64, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.writes++
	var n int64
	for _, id := range ids {
		a := r.accounts[id]
		if a == nil {
			continue
		}
		if expected := u.CustomUsageExpected; expected != nil && (!reflect.DeepEqual(expected.Credentials, a.Credentials) || !reflect.DeepEqual(expected.Config, a.Extra[CustomUsageExtraKey])) {
			continue
		}
		a.Credentials = mergeMap(a.Credentials, u.Credentials)
		a.Extra = mergeMap(a.Extra, u.Extra)
		n++
	}
	return n, nil
}

type customUsageRoundTrip func(*http.Request) (*http.Response, error)

func (f customUsageRoundTrip) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }
func customUsageFixture(t *testing.T) (*CustomUsageService, *customUsageTestRepo, CustomUsageConfig) {
	t.Helper()
	repo := &customUsageTestRepo{accounts: map[int64]*Account{1: {ID: 1, Type: AccountTypeAPIKey, Platform: PlatformOpenAI, Credentials: map[string]any{"api_key": "sk-inherited-credential", "base_url": "https://billing.example.com", "model_mapping": map[string]any{"x": "y"}}, Extra: map[string]any{"quota_used": 7.0}}}}
	s := NewCustomUsageService(repo)
	c := customUsageDefaults()
	c.Enabled = true
	c.Request.URL = "{{baseUrl}}/balance"
	c.Request.Headers = map[string]string{"Authorization": "Bearer {{apiKey}}"}
	c.Extractor.Remaining = &CustomUsageNumber{Path: "data.remaining"}
	c.Extractor.Unit = &CustomUsageText{Value: "USD"}
	return s, repo, c
}
func customUsageResponse(body string, status int) *http.Response {
	return &http.Response{StatusCode: status, Body: io.NopCloser(strings.NewReader(body)), Header: http.Header{}}
}

func TestCustomUsagePublicAddresses(t *testing.T) {
	for _, tc := range []struct {
		ip string
		ok bool
	}{
		{"8.8.8.8", true}, {"2606:4700:4700::1111", true}, {"::ffff:8.8.8.8", true},
		{"127.0.0.1", false}, {"10.1.2.3", false}, {"172.16.0.1", false}, {"192.168.1.1", false}, {"169.254.169.254", false}, {"100.100.100.200", false}, {"0.1.1.1", false}, {"198.18.1.1", false}, {"224.0.0.1", false}, {"240.0.0.1", false}, {"192.0.2.1", false}, {"::", false}, {"::1", false}, {"::ffff:127.0.0.1", false}, {"fc00::1", false}, {"fe80::1", false}, {"64:ff9b::a9fe:a9fe", false}, {"2002:7f00:1::", false}, {"2001:db8::1", false}, {"2001::1", false}, {"ff02::1", false}, {"3fff::1", false},
	} {
		t.Run(tc.ip, func(t *testing.T) { require.Equal(t, tc.ok, customUsagePublicIP(netip.MustParseAddr(tc.ip))) })
	}
	for _, raw := range []string{"http://127.0.0.1/", "http://[::1]/", "http://localhost./", "http://2130706433/", "http://0177.0.0.1/", "http://metadata.google.internal/", "file:///etc/passwd", "https://user:pass@billing.example.com/", "https://billing.example.com/#fragment", "https://billing.example.com:0/", "https://billing.example.com:65536/", "https://billing.example.com\\@127.0.0.1/"} {
		t.Run(raw, func(t *testing.T) { _, err := customUsageValidateURL(raw); require.Error(t, err) })
	}
	_, err := customUsageValidateURL("http://billing.example.com:8080/balance")
	require.NoError(t, err)
}
func TestCustomUsageDialRevalidatesAndPinsDNS(t *testing.T) {
	var lookups, dials int
	var destination string
	n := customUsageNetwork{lookup: func(context.Context, string) ([]net.IPAddr, error) {
		lookups++
		if lookups == 1 {
			return []net.IPAddr{{IP: net.ParseIP("8.8.8.8")}}, nil
		}
		return []net.IPAddr{{IP: net.ParseIP("127.0.0.1")}}, nil
	}, dial: func(_ context.Context, _ string, addr string) (net.Conn, error) {
		dials++
		destination = addr
		a, b := net.Pipe()
		require.NoError(t, b.Close())
		return a, nil
	}}
	conn, err := n.dialContext(t.Context(), "tcp", "billing.example.com:443")
	require.NoError(t, err)
	require.NoError(t, conn.Close())
	require.Equal(t, "8.8.8.8:443", destination)
	_, err = n.dialContext(t.Context(), "tcp", "billing.example.com:443")
	require.Error(t, err)
	require.Equal(t, 1, dials)
	require.Equal(t, 2, lookups)
	for _, ips := range [][]net.IPAddr{nil, {{IP: net.ParseIP("8.8.8.8")}, {IP: net.ParseIP("10.0.0.1")}}, {{IP: net.ParseIP("::ffff:169.254.169.254")}}} {
		n.lookup = func(context.Context, string) ([]net.IPAddr, error) { return ips, nil }
		_, err = n.dialContext(t.Context(), "tcp", "billing.example.com:443")
		require.Error(t, err)
		require.Equal(t, 1, dials)
	}
}
func TestCustomUsageConfigValidation(t *testing.T) {
	zero := 0.0
	neg := -1.0
	inf := math.Inf(1)
	for _, tc := range []struct {
		name   string
		change func(*CustomUsageConfig)
	}{
		{"timeout zero", func(c *CustomUsageConfig) { c.TimeoutSeconds = 0 }}, {"timeout high", func(c *CustomUsageConfig) { c.TimeoutSeconds = 31 }},
		{"interval short", func(c *CustomUsageConfig) { c.IntervalMinutes = 4 }}, {"interval negative", func(c *CustomUsageConfig) { c.IntervalMinutes = -1 }},
		{"method", func(c *CustomUsageConfig) { c.Request.Method = "POST" }}, {"template", func(c *CustomUsageConfig) { c.Template = "javascript" }},
		{"expression", func(c *CustomUsageConfig) { c.Extractor.Remaining.Path = "data.a[?(@.secret)]" }}, {"malformed path", func(c *CustomUsageConfig) { c.Extractor.Remaining.Path = "data.remaining]" }},
		{"zero divisor", func(c *CustomUsageConfig) { c.Extractor.Remaining.Divisor = &zero }}, {"negative divisor", func(c *CustomUsageConfig) { c.Extractor.Remaining.Divisor = &neg }}, {"infinite divisor", func(c *CustomUsageConfig) { c.Extractor.Remaining.Divisor = &inf }},
		{"host override", func(c *CustomUsageConfig) { c.Request.Headers["Host"] = "127.0.0.1" }}, {"duplicate header", func(c *CustomUsageConfig) { c.Request.Headers["authorization"] = "new" }},
		{"header injection", func(c *CustomUsageConfig) { c.Request.Headers["Authorization"] = "Bearer ok\r\nHost: internal" }}, {"header name", func(c *CustomUsageConfig) { c.Request.Headers["bad name"] = "x" }},
		{"unknown variable", func(c *CustomUsageConfig) { c.Request.URL = "{{baseUrl}}/{{secret}}" }}, {"credential authority", func(c *CustomUsageConfig) { c.Request.URL = "https://{{apiKey}}.example.com/usage" }},
		{"private base", func(c *CustomUsageConfig) { c.BaseURL = "http://127.0.0.1" }}, {"base query", func(c *CustomUsageConfig) { c.BaseURL = "https://billing.example.com?key=x" }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s, repo, c := customUsageFixture(t)
			tc.change(&c)
			_, err := s.PutConfig(t.Context(), 1, c)
			require.Error(t, err)
			require.Zero(t, repo.writes)
		})
	}
}
func TestCustomUsageParseNumbers(t *testing.T) {
	for _, tc := range []struct {
		name, body string
		want       float64
		bad        bool
	}{
		{"number", `{"data":{"remaining":12.5}}`, 12.5, false}, {"string", `{"data":{"remaining":"12.5"}}`, 12.5, false}, {"real zero", `{"data":{"remaining":0}}`, 0, false},
		{"missing", `{"data":{}}`, 0, true}, {"negative", `{"data":{"remaining":-1}}`, 0, true}, {"nan", `{"data":{"remaining":"NaN"}}`, 0, true}, {"infinity", `{"data":{"remaining":"Inf"}}`, 0, true}, {"overflow", `{"data":{"remaining":1e999}}`, 0, true}, {"boolean", `{"data":{"remaining":false}}`, 0, true}, {"null", `{"data":{"remaining":null}}`, 0, true}, {"object", `{"data":{"remaining":{}}}`, 0, true}, {"invalid", `not-json`, 0, true}, {"trailing", `{"data":{"remaining":1}} {}`, 0, true}, {"business error", `{"success":false,"data":{"remaining":0}}`, 0, true}, {"error envelope", `{"error":{"message":"secret"},"data":{"remaining":0}}`, 0, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, _, c := customUsageFixture(t)
			r, err := parseCustomUsage([]byte(tc.body), c, nil)
			if tc.bad {
				require.Error(t, err)
				require.Nil(t, r.Remaining)
			} else {
				require.NoError(t, err)
				require.NotNil(t, r.Remaining)
				require.Equal(t, tc.want, *r.Remaining)
			}
		})
	}
	_, _, c := customUsageFixture(t)
	divisor := 100.0
	c.Extractor.Remaining = &CustomUsageNumber{Path: "$.data.items[0].quota", Divisor: &divisor}
	r, err := parseCustomUsage([]byte(`{"data":{"items":[{"quota":"250"}]}}`), c, nil)
	require.NoError(t, err)
	require.Equal(t, 2.5, *r.Remaining)
	c.Extractor.PlanName = &CustomUsageText{Path: "secret"}
	_, err = parseCustomUsage([]byte(`{"data":{"items":[{"quota":250}]},"secret":"sk-private"}`), c, []string{"sk-private"})
	require.Error(t, err)
}
func TestCustomUsageOfficialTemplates(t *testing.T) {
	for _, tc := range []struct {
		template, base, path, body, unit string
		remaining                        float64
	}{
		{"newapi", "https://billing.example.com/v1", "/api/user/self", `{"success":true,"message":"","data":{"quota":1000000,"used_quota":500000}}`, "USD", 2},
		{"sub2api", "https://billing.example.com/v1", "/v1/usage", `{"mode":"unrestricted","isValid":true,"planName":"wallet","remaining":12.5,"unit":"USD","balance":12.5}`, "USD", 12.5},
		{"sub2api", "https://billing.example.com", "/v1/usage", `{"mode":"quota_limited","isValid":true,"quota":{"limit":10,"used":3,"remaining":7},"remaining":7,"unit":"USD"}`, "USD", 7},
	} {
		t.Run(tc.template+tc.base, func(t *testing.T) {
			_, repo, c := customUsageFixture(t)
			c.Template = tc.template
			c.BaseURL = tc.base
			c.UserID = "123"
			repo.accounts[1].Credentials["access_token"] = "management-token"
			c.Request = CustomUsageRequest{}
			c.Extractor = CustomUsageExtractor{}
			p, err := prepareCustomUsage(repo.accounts[1], c, customUsageSecrets{})
			require.NoError(t, err)
			require.Equal(t, tc.path, p.request.URL.Path)
			r, err := parseCustomUsage([]byte(tc.body), p.config, p.sensitive)
			require.NoError(t, err)
			require.Equal(t, tc.remaining, *r.Remaining)
			require.Equal(t, tc.unit, r.Unit)
		})
	}
}
func TestCustomUsageSecretStorageAndMerge(t *testing.T) {
	s, repo, c := customUsageFixture(t)
	c.APIKey = "sk-custom-private"
	c.AccessToken = "access-private"
	c.Request.Headers["X-Vendor"] = "vendor-private"
	c.Request.URL = "{{baseUrl}}/usage?secret=query-private&token={{apiKey}}"
	view, err := s.PutConfig(t.Context(), 1, c)
	require.NoError(t, err)
	require.True(t, view.HasAPIKey)
	require.True(t, view.HasAccessToken)
	public, _ := json.Marshal(view)
	extra, _ := json.Marshal(repo.accounts[1].Extra)
	for _, secret := range []string{"sk-custom-private", "access-private", "vendor-private", "query-private"} {
		require.NotContains(t, string(public), secret)
		require.NotContains(t, string(extra), secret)
	}
	require.Equal(t, 7.0, repo.accounts[1].Extra["quota_used"])
	require.Equal(t, "sk-inherited-credential", repo.accounts[1].GetCredential("api_key"))
	require.Contains(t, repo.accounts[1].Credentials, "model_mapping")
	view, err = s.PutConfig(t.Context(), 1, view.CustomUsageConfig)
	require.NoError(t, err)
	stored, secrets, _ := readCustomUsage(repo.accounts[1])
	require.Equal(t, "sk-custom-private", secrets.APIKey)
	require.Equal(t, "access-private", secrets.AccessToken)
	require.Equal(t, "vendor-private", stored.Request.Headers["X-Vendor"])
	require.Contains(t, stored.Request.URL, "query-private")
	p, err := prepareCustomUsage(repo.accounts[1], stored, secrets)
	require.NoError(t, err)
	require.Equal(t, "Bearer sk-custom-private", p.request.Header.Get("Authorization"))
	c = view.CustomUsageConfig
	c.ClearAPIKey = true
	c.ClearAccessToken = true
	view, err = s.PutConfig(t.Context(), 1, c)
	require.NoError(t, err)
	require.False(t, view.HasAPIKey)
	require.False(t, view.HasAccessToken)
	stored, secrets, _ = readCustomUsage(repo.accounts[1])
	p, err = prepareCustomUsage(repo.accounts[1], stored, secrets)
	require.NoError(t, err)
	require.Equal(t, "Bearer sk-inherited-credential", p.request.Header.Get("Authorization"))
}
func TestCustomUsageCacheManualBatchDraftAndFailure(t *testing.T) {
	s, repo, c := customUsageFixture(t)
	var calls int
	status := 200
	body := `{"data":{"remaining":42}}`
	s.client.Transport = customUsageRoundTrip(func(*http.Request) (*http.Response, error) { calls++; return customUsageResponse(body, status), nil })
	clock := time.Now()
	s.now = func() time.Time { return clock }
	_, err := s.PutConfig(t.Context(), 1, c)
	require.NoError(t, err)
	r, err := s.Query(t.Context(), 1, false, nil)
	require.NoError(t, err)
	require.Nil(t, r.Remaining)
	require.True(t, r.Stale)
	require.Zero(t, calls)
	items, err := s.Batch(t.Context(), []int64{1, 1, 2})
	require.NoError(t, err)
	require.Len(t, items, 2)
	require.True(t, items[1].Configured)
	require.Equal(t, 0, items[1].IntervalMinutes)
	require.Equal(t, "account_not_found", items[2].Error)
	require.Zero(t, calls)
	r, err = s.Query(t.Context(), 1, true, nil)
	require.NoError(t, err)
	require.Equal(t, 42.0, *r.Remaining)
	require.Equal(t, 1, calls)
	_, err = s.Query(t.Context(), 1, true, nil)
	require.NoError(t, err)
	require.Equal(t, 1, calls)
	clock = clock.Add(16 * time.Second)
	status = 500
	body = "request Authorization sk-private"
	r, err = s.Query(t.Context(), 1, true, nil)
	require.NoError(t, err)
	require.Equal(t, "upstream_error", r.Error)
	require.True(t, r.Stale)
	require.Equal(t, 42.0, *r.Remaining)
	require.Equal(t, 2, calls)
	clock = clock.Add(16 * time.Second)
	status = 200
	body = `{"data":{"remaining":99}}`
	draft := c
	draft.APIKey = "sk-draft-only"
	draft.Request.URL = "https://other.example.com/usage"
	r, err = s.Query(t.Context(), 1, true, &draft)
	require.NoError(t, err)
	require.Equal(t, 99.0, *r.Remaining)
	items, err = s.Batch(t.Context(), []int64{1})
	require.NoError(t, err)
	require.Equal(t, 42.0, *items[1].Remaining)
	require.Equal(t, 1, repo.writes)
	_, secrets, _ := readCustomUsage(repo.accounts[1])
	require.Empty(t, secrets.APIKey)
	repo.mu.Lock()
	repo.accounts[1].Credentials["api_key"] = "rotated-key"
	repo.mu.Unlock()
	items, err = s.Batch(t.Context(), []int64{1})
	require.NoError(t, err)
	require.Nil(t, items[1].Remaining)
	clock = clock.Add(16 * time.Second)
	status = 401
	r, err = s.Query(t.Context(), 1, true, nil)
	require.NoError(t, err)
	require.Nil(t, r.Remaining)
	require.Empty(t, r.Unit)
	encoded, _ := json.Marshal(r)
	require.NotContains(t, string(encoded), "remaining")
	require.NotContains(t, string(encoded), "Authorization")
}
func TestCustomUsageIntervalRefreshAndCooldown(t *testing.T) {
	s, _, c := customUsageFixture(t)
	c.IntervalMinutes = 5
	clock := time.Now()
	s.now = func() time.Time { return clock }
	var calls int
	s.client.Transport = customUsageRoundTrip(func(*http.Request) (*http.Response, error) {
		calls++
		return customUsageResponse(`{"data":{"remaining":3}}`, 200), nil
	})
	_, err := s.PutConfig(t.Context(), 1, c)
	require.NoError(t, err)
	_, err = s.Query(t.Context(), 1, false, nil)
	require.NoError(t, err)
	require.Equal(t, 1, calls)
	clock = clock.Add(4 * time.Minute)
	_, err = s.Query(t.Context(), 1, false, nil)
	require.NoError(t, err)
	require.Equal(t, 1, calls)
	clock = clock.Add(time.Minute)
	items, err := s.Batch(t.Context(), []int64{1})
	require.NoError(t, err)
	require.True(t, items[1].Stale)
	require.Equal(t, 1, calls)
	_, err = s.Query(t.Context(), 1, false, nil)
	require.NoError(t, err)
	require.Equal(t, 2, calls)
	_, err = s.PutConfig(t.Context(), 1, c)
	require.NoError(t, err)
	r, err := s.Query(t.Context(), 1, true, nil)
	require.NoError(t, err)
	require.Equal(t, "cooldown", r.Error)
	require.Equal(t, 2, calls)
}
func TestCustomUsageSingleflightAndBoundedCache(t *testing.T) {
	s, _, c := customUsageFixture(t)
	_, err := s.PutConfig(t.Context(), 1, c)
	require.NoError(t, err)
	entered := make(chan struct{})
	release := make(chan struct{})
	var calls atomic.Int32
	s.client.Transport = customUsageRoundTrip(func(*http.Request) (*http.Response, error) {
		if calls.Add(1) == 1 {
			close(entered)
		}
		<-release
		return customUsageResponse(`{"data":{"remaining":9}}`, 200), nil
	})
	var wg sync.WaitGroup
	results := make(chan CustomUsageResult, 16)
	wg.Add(1)
	go func() { defer wg.Done(); r, _ := s.Query(t.Context(), 1, true, nil); results <- r }()
	<-entered
	for i := 0; i < 15; i++ {
		wg.Add(1)
		go func() { defer wg.Done(); r, _ := s.Query(t.Context(), 1, true, nil); results <- r }()
	}
	close(release)
	wg.Wait()
	close(results)
	require.EqualValues(t, 1, calls.Load())
	for r := range results {
		require.Equal(t, 9.0, *r.Remaining)
	}
	s.mu.Lock()
	for i := int64(2); i < 2000; i++ {
		s.entryLocked(i)
	}
	require.LessOrEqual(t, len(s.cache), customUsageCacheLimit)
	s.mu.Unlock()
}
func TestCustomUsageRequestFailuresAndRedaction(t *testing.T) {
	for _, tc := range []struct {
		name           string
		status         int
		body, expected string
	}{
		{"redirect", 302, "private-body", "redirect_blocked"}, {"forbidden", 403, "Bearer private-key", "upstream_error"}, {"oversize", 200, strings.Repeat("x", upstreamBillingProbeMaxBodyBytes+1), "response_too_large"}, {"malformed", 200, "<html>private-key</html>", "invalid_response"}, {"missing", 200, `{"secret":"private-key"}`, "invalid_response"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s, _, c := customUsageFixture(t)
			_, err := s.PutConfig(t.Context(), 1, c)
			require.NoError(t, err)
			var calls int
			s.client.Transport = customUsageRoundTrip(func(*http.Request) (*http.Response, error) {
				calls++
				resp := customUsageResponse(tc.body, tc.status)
				resp.Header.Set("Location", "http://169.254.169.254/latest/meta-data/")
				return resp, nil
			})
			r, err := s.Query(t.Context(), 1, true, nil)
			require.NoError(t, err)
			require.Equal(t, tc.expected, r.Error)
			require.Nil(t, r.Remaining)
			require.Equal(t, 1, calls)
			encoded, _ := json.Marshal(r)
			require.NotContains(t, string(encoded), "private-key")
		})
	}
}
func TestCustomUsageCancelledAndTimeout(t *testing.T) {
	s, _, c := customUsageFixture(t)
	c.TimeoutSeconds = 1
	_, err := s.PutConfig(t.Context(), 1, c)
	require.NoError(t, err)
	s.client.Transport = customUsageRoundTrip(func(r *http.Request) (*http.Response, error) { <-r.Context().Done(); return nil, r.Context().Err() })
	start := time.Now()
	r, err := s.Query(t.Context(), 1, true, nil)
	require.NoError(t, err)
	require.Equal(t, "request_failed", r.Error)
	require.Less(t, time.Since(start), 3*time.Second)
}
func TestCustomUsageGenericUpdateCannotForgeManagedState(t *testing.T) {
	repo := &upstreamBillingProbeAccountRepo{accounts: map[int64]*Account{1: {ID: 1, Type: AccountTypeAPIKey, Platform: PlatformOpenAI, Status: StatusActive, Credentials: map[string]any{"api_key": "keep", CustomUsageCredentialsKey: map[string]any{"api_key": "private"}}, Extra: map[string]any{CustomUsageExtraKey: map[string]any{"enabled": false}}}}}
	s := &adminServiceImpl{accountRepo: repo}
	updated, err := s.UpdateAccount(t.Context(), 1, &UpdateAccountInput{Credentials: map[string]any{"base_url": "https://billing.example.com", CustomUsageCredentialsKey: map[string]any{"api_key": "forged"}}, Extra: map[string]any{CustomUsageExtraKey: map[string]any{"enabled": true}}})
	require.NoError(t, err)
	require.Equal(t, map[string]any{"enabled": false}, updated.Extra[CustomUsageExtraKey])
	require.Equal(t, map[string]any{"api_key": "private"}, updated.Credentials[CustomUsageCredentialsKey])
	require.NoError(t, s.UpdateAccountExtra(t.Context(), 1, map[string]any{CustomUsageExtraKey: map[string]any{"enabled": true}, "unrelated": true}))
	require.Equal(t, map[string]any{"enabled": false}, repo.accounts[1].Extra[CustomUsageExtraKey])
	_, err = s.BulkUpdateAccounts(t.Context(), &BulkUpdateAccountsInput{AccountIDs: []int64{1}, Credentials: map[string]any{CustomUsageCredentialsKey: "forged"}, Extra: map[string]any{CustomUsageExtraKey: "forged"}})
	require.NoError(t, err)
	for _, u := range repo.bulkUpdates {
		require.NotContains(t, u.Credentials, CustomUsageCredentialsKey)
		require.NotContains(t, u.Extra, CustomUsageExtraKey)
	}
}
func TestCustomUsageAuditNeverStoresHeadersOrURLSecrets(t *testing.T) {
	body := []byte(`{"config":{"api_key":"sk-secret","access_token":"access-secret","request":{"url":"https://billing.example.com?odd=hidden-query","headers":{"X-Vendor":"hidden-header"}}}}`)
	got := RedactAuditBody(body, "application/json")
	for _, secret := range []string{"sk-secret", "access-secret", "hidden-query", "hidden-header"} {
		require.NotContains(t, got, secret)
	}
}

func TestCustomUsageConcurrencySlotsAndCancellation(t *testing.T) {
	s, repo, c := customUsageFixture(t)
	for id := int64(2); id <= 5; id++ {
		a := *repo.accounts[1]
		a.ID = id
		a.Extra = shallowCopyMap(a.Extra)
		a.Credentials = shallowCopyMap(a.Credentials)
		repo.accounts[id] = &a
	}
	for id := int64(1); id <= 5; id++ {
		_, err := s.PutConfig(t.Context(), id, c)
		require.NoError(t, err)
	}
	entered := make(chan struct{}, 4)
	release := make(chan struct{})
	var active, peak atomic.Int32
	s.client.Transport = customUsageRoundTrip(func(r *http.Request) (*http.Response, error) {
		n := active.Add(1)
		for old := peak.Load(); n > old && !peak.CompareAndSwap(old, n); old = peak.Load() {
		}
		entered <- struct{}{}
		<-release
		active.Add(-1)
		return customUsageResponse(`{"data":{"remaining":1}}`, 200), nil
	})
	var wg sync.WaitGroup
	for id := int64(1); id <= 4; id++ {
		wg.Add(1)
		go func(id int64) { defer wg.Done(); _, _ = s.Query(t.Context(), id, true, nil) }(id)
	}
	for i := 0; i < 4; i++ {
		<-entered
	}
	r, err := s.Query(t.Context(), 5, true, nil)
	require.NoError(t, err)
	require.Equal(t, "busy", r.Error)
	require.EqualValues(t, 4, peak.Load())
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	r, err = s.Query(ctx, 1, true, nil)
	require.NoError(t, err)
	require.Equal(t, "request_cancelled", r.Error)
	close(release)
	wg.Wait()
}
func TestCustomUsageIdentityChangedInFlight(t *testing.T) {
	for _, kind := range []string{"rotation", "deletion", "disable"} {
		t.Run(kind, func(t *testing.T) {
			s, repo, c := customUsageFixture(t)
			_, err := s.PutConfig(t.Context(), 1, c)
			require.NoError(t, err)
			s.client.Transport = customUsageRoundTrip(func(*http.Request) (*http.Response, error) {
				repo.mu.Lock()
				defer repo.mu.Unlock()
				switch kind {
				case "rotation":
					repo.accounts[1].Credentials["api_key"] = "new-key"
				case "deletion":
					delete(repo.accounts, 1)
				case "disable":
					config, ok := repo.accounts[1].Extra[CustomUsageExtraKey].(map[string]any)
					require.True(t, ok)
					config["enabled"] = false
				}
				return customUsageResponse(`{"data":{"remaining":999}}`, 200), nil
			})
			r, err := s.Query(t.Context(), 1, true, nil)
			require.NoError(t, err)
			require.Nil(t, r.Remaining)
			require.Nil(t, r.UpdatedAt)
			require.Contains(t, []string{"account_changed", "config_changed"}, r.Error)
		})
	}
}
func TestCustomUsageDraftIdentityChangedInFlight(t *testing.T) {
	for _, kind := range []string{"rotation", "deletion"} {
		t.Run(kind, func(t *testing.T) {
			s, repo, config := customUsageFixture(t)
			s.client.Transport = customUsageRoundTrip(func(*http.Request) (*http.Response, error) {
				repo.mu.Lock()
				defer repo.mu.Unlock()
				if kind == "rotation" {
					repo.accounts[1].Credentials["api_key"] = "rotated-draft-key"
				} else {
					delete(repo.accounts, 1)
				}
				return customUsageResponse(`{"data":{"remaining":999}}`, http.StatusOK), nil
			})
			result, err := s.Query(t.Context(), 1, true, &config)
			require.NoError(t, err)
			require.Nil(t, result.Remaining)
			require.Nil(t, result.UpdatedAt)
			require.Contains(t, []string{"account_changed", "config_changed"}, result.Error)
		})
	}
}

func TestCustomUsagePutConfigDuringQuery(t *testing.T) {
	for _, preview := range []bool{false, true} {
		name := "saved"
		if preview {
			name = "draft"
		}
		t.Run(name, func(t *testing.T) {
			usage, _, config := customUsageFixture(t)
			clock := time.Now()
			usage.now = func() time.Time { return clock }
			config.IntervalMinutes = 0
			_, err := usage.PutConfig(t.Context(), 1, config)
			require.NoError(t, err)
			started := make(chan string, 1)
			release := make(chan struct{})
			var releaseOnce sync.Once
			unblock := func() { releaseOnce.Do(func() { close(release) }) }
			defer unblock()
			var calls atomic.Int32
			usage.client.Transport = customUsageRoundTrip(func(request *http.Request) (*http.Response, error) {
				if calls.Add(1) == 1 {
					started <- request.Header.Get("Authorization")
					select {
					case <-release:
					case <-request.Context().Done():
						return nil, request.Context().Err()
					}
				}
				return customUsageResponse(`{"data":{"remaining":999}}`, http.StatusOK), nil
			})
			var draft *CustomUsageConfig
			if preview {
				draft = &config
			}
			type outcome struct {
				result CustomUsageResult
				err    error
			}
			finished := make(chan outcome, 1)
			go func() {
				result, queryErr := usage.Query(t.Context(), 1, true, draft)
				finished <- outcome{result: result, err: queryErr}
			}()
			select {
			case authorization := <-started:
				require.Equal(t, "Bearer sk-inherited-credential", authorization)
			case <-time.After(5 * time.Second):
				t.Fatal("query did not start")
			}
			updated := config
			updated.APIKey = "rotated-custom-credential"
			_, err = usage.PutConfig(t.Context(), 1, updated)
			require.NoError(t, err)
			unblock()
			select {
			case completed := <-finished:
				require.NoError(t, completed.err)
				require.Equal(t, "config_changed", completed.result.Error)
				require.Nil(t, completed.result.Remaining)
				require.Nil(t, completed.result.UpdatedAt)
			case <-time.After(5 * time.Second):
				t.Fatal("query did not finish")
			}
			items, err := usage.Batch(t.Context(), []int64{1})
			require.NoError(t, err)
			require.Nil(t, items[1].Remaining)
			result, err := usage.Query(t.Context(), 1, true, nil)
			require.NoError(t, err)
			require.Equal(t, "cooldown", result.Error)
			require.EqualValues(t, 1, calls.Load())
			clock = clock.Add(customUsageCooldown + time.Second)
			result, err = usage.Query(t.Context(), 1, false, nil)
			require.NoError(t, err)
			require.Nil(t, result.Remaining)
			require.EqualValues(t, 1, calls.Load())
			result, err = usage.Query(t.Context(), 1, true, nil)
			require.NoError(t, err)
			require.Empty(t, result.Error)
			require.NotNil(t, result.Remaining)
			require.EqualValues(t, 2, calls.Load())
		})
	}
}

func TestCustomUsageUnlimitedAndBusinessError(t *testing.T) {
	for _, body := range []string{
		`{"code":false,"data":{"total_available":0,"total_used":0,"total_granted":0}}`,
		`{"code":401,"data":{"total_available":0,"total_used":0,"total_granted":0}}`,
		`{"code":"permission_denied","data":{"total_available":0,"total_used":0,"total_granted":0}}`,
		`{"code":true,"data":{"unlimited_quota":true,"total_available":0,"total_used":0,"total_granted":0}}`,
	} {
		t.Run(body, func(t *testing.T) {
			c := customUsageDefaults()
			c.Enabled = true
			c.Template = "newapi"
			require.NoError(t, normalizeCustomUsage(&c))
			r, err := parseCustomUsage([]byte(body), c, nil)
			require.Error(t, err)
			require.Nil(t, r.Remaining)
		})
	}
}
func TestCustomUsagePlaceholdersAndClientPolicy(t *testing.T) {
	_, repo, c := customUsageFixture(t)
	c.Request.URL = "{{baseUrl}}/balance?key={{apiKey}}&token={{accessToken}}&uid={{userId}}"
	c.UserID = "user 123"
	c.Request.Headers = map[string]string{"Authorization": "Bearer {{accessToken}}", "X-Key": "{{apiKey}}", "New-Api-User": "{{userId}}"}
	p, err := prepareCustomUsage(repo.accounts[1], c, customUsageSecrets{APIKey: "key+&?", AccessToken: "token+&?"})
	require.NoError(t, err)
	require.Equal(t, "key+&?", p.request.URL.Query().Get("key"))
	require.Equal(t, "token+&?", p.request.URL.Query().Get("token"))
	require.Equal(t, "user 123", p.request.URL.Query().Get("uid"))
	require.Equal(t, "Bearer token+&?", p.request.Header.Get("Authorization"))
	require.Equal(t, "user 123", p.request.Header.Get("New-Api-User"))
	client := newCustomUsageClient()
	require.ErrorIs(t, client.CheckRedirect(nil, nil), http.ErrUseLastResponse)
	transport, ok := client.Transport.(*http.Transport)
	require.True(t, ok)
	require.Nil(t, transport.Proxy)
	require.NotNil(t, transport.DialContext)
	require.True(t, transport.DisableKeepAlives)
	require.True(t, transport.DisableCompression)
	require.EqualValues(t, 16*1024, transport.MaxResponseHeaderBytes)
}
func TestCustomUsageGenericCreateCannotForgeManagedState(t *testing.T) {
	repo := &upstreamBillingProbeAccountRepo{accounts: map[int64]*Account{}}
	svc := &adminServiceImpl{accountRepo: repo}
	created, err := svc.CreateAccount(t.Context(), &CreateAccountInput{SkipDefaultGroupBind: true, Name: "test", Platform: PlatformOpenAI, Type: AccountTypeAPIKey, Credentials: map[string]any{"api_key": "valid-key", CustomUsageCredentialsKey: map[string]any{"api_key": "forged"}}, Extra: map[string]any{CustomUsageExtraKey: map[string]any{"enabled": true}}})
	require.NoError(t, err)
	require.NotContains(t, created.Extra, CustomUsageExtraKey)
	require.NotContains(t, created.Credentials, CustomUsageCredentialsKey)
}

func TestCustomUsageSub2APIUnitFallback(t *testing.T) {
	for _, tc := range []struct{ body, unit string }{
		{`{"remaining":12.5,"unit":"USD"}`, "USD"}, {`{"remaining":12.5}`, "USD"}, {`{"remaining":12.5,"unit":""}`, "USD"}, {`{"remaining":12.5,"unit":"credits"}`, "credits"},
	} {
		t.Run(tc.body, func(t *testing.T) {
			c := customUsageDefaults()
			c.Enabled = true
			c.Template = "sub2api"
			require.NoError(t, normalizeCustomUsage(&c))
			r, err := parseCustomUsage([]byte(tc.body), c, nil)
			require.NoError(t, err)
			require.Equal(t, tc.unit, r.Unit)
		})
	}
}
func TestCustomUsageNestedErrorEnvelopes(t *testing.T) {
	for _, tc := range []struct {
		errorValue string
		reject     bool
	}{
		{`{"nested":{"message":"private"}}`, true}, {`[{"secret":"private"}]`, true}, {`[]`, true}, {`{}`, true}, {`"private"`, true}, {`true`, true}, {`0`, true}, {`null`, false}, {`""`, false},
	} {
		t.Run(tc.errorValue, func(t *testing.T) {
			_, _, c := customUsageFixture(t)
			var result CustomUsageResult
			var err error
			require.NotPanics(t, func() {
				result, err = parseCustomUsage([]byte(`{"data":{"remaining":1},"error":`+tc.errorValue+`}`), c, nil)
			})
			if tc.reject {
				require.Error(t, err)
				require.Nil(t, result.Remaining)
			} else {
				require.NoError(t, err)
				require.Equal(t, 1.0, *result.Remaining)
			}
		})
	}
}
func TestCustomUsageNewAPICustomTokenMode(t *testing.T) {
	_, repo, c := customUsageFixture(t)
	c.Template = "custom"
	c.Request.URL = "{{baseUrl}}/api/usage/token/"
	divisor := 500000.0
	c.Extractor = CustomUsageExtractor{Remaining: &CustomUsageNumber{Path: "data.total_available", Divisor: &divisor}, Used: &CustomUsageNumber{Path: "data.total_used", Divisor: &divisor}, Total: &CustomUsageNumber{Path: "data.total_granted", Divisor: &divisor}, Unit: &CustomUsageText{Value: "USD"}}
	p, err := prepareCustomUsage(repo.accounts[1], c, customUsageSecrets{})
	require.NoError(t, err)
	require.Equal(t, "/api/usage/token/", p.request.URL.Path)
	require.Equal(t, "Bearer sk-inherited-credential", p.request.Header.Get("Authorization"))
	body := []byte(`{"code":true,"data":{"total_granted":1500000,"total_available":1000000,"total_used":500000,"unlimited_quota":false}}`)
	r, err := parseCustomUsage(body, p.config, p.sensitive)
	require.NoError(t, err)
	require.Equal(t, 2.0, *r.Remaining)
	require.Equal(t, 1.0, *r.Used)
	require.Equal(t, 3.0, *r.Total)
	_, err = parseCustomUsage([]byte(`{"success":false,"data":{"total_granted":0,"total_available":0,"total_used":0}}`), p.config, nil)
	require.Error(t, err)
}
