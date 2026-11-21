package service

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/ipquality"
)

type assistRepoStub struct {
	ips   []RecentClientIP
	err   error
	calls int
}

func (s *assistRepoStub) ListRecentClientIPs(_ context.Context, _ int64, limit int) ([]RecentClientIP, error) {
	s.calls++
	if s.err != nil {
		return nil, s.err
	}
	if len(s.ips) > limit {
		return s.ips[:limit], nil
	}
	return s.ips, nil
}

func newAssistServiceForTest(repo IPWhitelistAssistRepository, lookup geoLookuper) *IPWhitelistAssistService {
	svc := NewIPWhitelistAssistService(repo)
	svc.lookup = lookup
	svc.now = func() time.Time { return time.Date(2026, 11, 22, 0, 0, 0, 0, time.UTC) }
	return svc
}

func TestIPWhitelistAssistGetReturnsCurrentAndRecentIPs(t *testing.T) {
	repo := &assistRepoStub{ips: []RecentClientIP{
		{IP: "203.0.113.7", Count: 12},
		{IP: "198.51.100.9", Count: 3},
	}}
	lookupCalls := 0
	var looked []string
	svc := newAssistServiceForTest(repo, func(_ context.Context, ips []string) map[string]ipquality.Geo {
		lookupCalls++
		looked = append([]string(nil), ips...)
		return map[string]ipquality.Geo{
			"203.0.113.7":  {Country: "China", Region: "Guangdong", City: "Shenzhen", ISP: "China Telecom"},
			"198.51.100.9": {Country: "United States", City: "Ashburn", Hosting: true},
		}
	})

	result, err := svc.Get(context.Background(), 42, "203.0.113.7")
	if err != nil {
		t.Fatalf("Get returned error: %v", err)
	}
	if result.CurrentIP != "203.0.113.7" {
		t.Fatalf("current_ip = %q", result.CurrentIP)
	}
	if result.CurrentGeo.Country != "China" || result.CurrentGeo.Region != "Guangdong" ||
		result.CurrentGeo.City != "Shenzhen" || result.CurrentGeo.ISP != "China Telecom" {
		t.Fatalf("current_geo = %#v", result.CurrentGeo)
	}
	if len(result.RecentIPs) != 2 || result.RecentIPs[0].IP != "203.0.113.7" || result.RecentIPs[0].Count != 12 {
		t.Fatalf("recent_ips = %#v", result.RecentIPs)
	}
	if !result.RecentIPs[1].Geo.Hosting {
		t.Fatalf("hosting flag not propagated: %#v", result.RecentIPs[1])
	}
	if lookupCalls != 1 {
		t.Fatalf("lookup calls = %d", lookupCalls)
	}
	// The current IP is looked up once even though it also appears in recent_ips.
	seen := map[string]int{}
	for _, ip := range looked {
		seen[ip]++
	}
	if seen["203.0.113.7"] != 1 {
		t.Fatalf("duplicate lookup for current ip: %#v", looked)
	}
}

func TestIPWhitelistAssistGetDegradesWhenGeoIsEmpty(t *testing.T) {
	repo := &assistRepoStub{ips: []RecentClientIP{{IP: "192.0.2.10", Count: 2}}}
	svc := newAssistServiceForTest(repo, func(_ context.Context, _ []string) map[string]ipquality.Geo {
		// ip-api failure: LookupGeo returns no entries and no error.
		return map[string]ipquality.Geo{}
	})

	result, err := svc.Get(context.Background(), 7, "192.0.2.10")
	if err != nil {
		t.Fatalf("Get must not fail when geo is empty: %v", err)
	}
	if result.CurrentIP != "192.0.2.10" {
		t.Fatalf("current_ip = %q", result.CurrentIP)
	}
	if result.CurrentGeo != (IPGeo{}) {
		t.Fatalf("current_geo should be empty, got %#v", result.CurrentGeo)
	}
	if len(result.RecentIPs) != 1 || result.RecentIPs[0].Geo != (IPGeo{}) {
		t.Fatalf("recent geo should be empty, got %#v", result.RecentIPs)
	}
}

func TestIPWhitelistAssistGetDegradesWhenRecentLookupFails(t *testing.T) {
	repo := &assistRepoStub{err: errors.New("query timed out")}
	svc := newAssistServiceForTest(repo, func(_ context.Context, _ []string) map[string]ipquality.Geo {
		return map[string]ipquality.Geo{"203.0.113.7": {City: "Shenzhen"}}
	})

	result, err := svc.Get(context.Background(), 9, "203.0.113.7")
	if err != nil {
		t.Fatalf("Get must degrade on repository failure: %v", err)
	}
	if len(result.RecentIPs) != 0 {
		t.Fatalf("recent_ips should be empty, got %#v", result.RecentIPs)
	}
	if result.CurrentGeo.City != "Shenzhen" {
		t.Fatalf("current geo lost: %#v", result.CurrentGeo)
	}
}

func TestIPWhitelistAssistGetLimitsRecentIPsToFive(t *testing.T) {
	repo := &assistRepoStub{ips: []RecentClientIP{
		{IP: "10.0.0.1", Count: 9}, {IP: "10.0.0.2", Count: 8}, {IP: "10.0.0.3", Count: 7},
		{IP: "10.0.0.4", Count: 6}, {IP: "10.0.0.5", Count: 5}, {IP: "10.0.0.6", Count: 4},
	}}
	svc := newAssistServiceForTest(repo, func(_ context.Context, _ []string) map[string]ipquality.Geo { return nil })

	result, err := svc.Get(context.Background(), 1, "10.0.0.1")
	if err != nil {
		t.Fatalf("Get returned error: %v", err)
	}
	if len(result.RecentIPs) != 5 {
		t.Fatalf("recent_ips = %d, want 5", len(result.RecentIPs))
	}
}

func TestIPWhitelistAssistGetCachesPerUser(t *testing.T) {
	repo := &assistRepoStub{ips: []RecentClientIP{{IP: "203.0.113.7", Count: 1}}}
	lookupCalls := 0
	svc := newAssistServiceForTest(repo, func(_ context.Context, _ []string) map[string]ipquality.Geo {
		lookupCalls++
		return nil
	})

	if _, err := svc.Get(context.Background(), 5, "203.0.113.7"); err != nil {
		t.Fatalf("first Get: %v", err)
	}
	if _, err := svc.Get(context.Background(), 5, "203.0.113.7"); err != nil {
		t.Fatalf("second Get: %v", err)
	}
	if repo.calls != 1 || lookupCalls != 1 {
		t.Fatalf("expected cache hit, repo=%d lookup=%d", repo.calls, lookupCalls)
	}
	// Another user must not share the cached answer.
	if _, err := svc.Get(context.Background(), 6, "203.0.113.7"); err != nil {
		t.Fatalf("other user Get: %v", err)
	}
	if repo.calls != 2 {
		t.Fatalf("cache leaked across users, repo=%d", repo.calls)
	}
}

func TestIPWhitelistAssistGetRateLimitsUncachedRefreshes(t *testing.T) {
	repo := &assistRepoStub{ips: nil}
	svc := newAssistServiceForTest(repo, func(_ context.Context, _ []string) map[string]ipquality.Geo { return nil })
	svc.cacheTTL = -time.Second // never serve from cache, so every call reaches the limiter
	svc.rateWindow = time.Minute
	svc.rateLimit = 2

	for i := 0; i < 2; i++ {
		if _, err := svc.Get(context.Background(), 3, "203.0.113.7"); err != nil {
			t.Fatalf("call %d should be allowed: %v", i+1, err)
		}
	}
	if _, err := svc.Get(context.Background(), 3, "203.0.113.7"); !errors.Is(err, ErrIPWhitelistAssistRateLimited) {
		t.Fatalf("third call error = %v, want rate limited", err)
	}
}

func TestIPWhitelistAssistGetWithoutCurrentIP(t *testing.T) {
	repo := &assistRepoStub{ips: []RecentClientIP{{IP: "203.0.113.7", Count: 1}}}
	svc := newAssistServiceForTest(repo, func(_ context.Context, ips []string) map[string]ipquality.Geo {
		if len(ips) != 1 || ips[0] != "203.0.113.7" {
			t.Fatalf("unexpected lookup ips: %#v", ips)
		}
		return nil
	})

	result, err := svc.Get(context.Background(), 2, "")
	if err != nil {
		t.Fatalf("Get returned error: %v", err)
	}
	if result.CurrentIP != "" || result.CurrentGeo != (IPGeo{}) {
		t.Fatalf("unexpected current state: %#v", result)
	}
	if len(result.RecentIPs) != 1 {
		t.Fatalf("recent_ips = %#v", result.RecentIPs)
	}
}

// A user who switches networks (home -> VPN) within the cache TTL must not be
// shown the previous network's "current IP": whitelisting it would lock them
// out, which is the failure this endpoint is meant to prevent.
func TestIPWhitelistAssistGetCacheSeparatesClientIPs(t *testing.T) {
	repo := &assistRepoStub{ips: []RecentClientIP{{IP: "203.0.113.7", Count: 1}}}
	svc := newAssistServiceForTest(repo, func(_ context.Context, _ []string) map[string]ipquality.Geo {
		return nil
	})

	if _, err := svc.Get(context.Background(), 5, "203.0.113.7"); err != nil {
		t.Fatalf("first Get: %v", err)
	}
	// Same user, different egress: must recompute instead of replaying the cache.
	result, err := svc.Get(context.Background(), 5, "198.51.100.9")
	if err != nil {
		t.Fatalf("second Get: %v", err)
	}
	if result.CurrentIP != "198.51.100.9" {
		t.Fatalf("stale current_ip served for a new client IP: %q", result.CurrentIP)
	}
	if repo.calls != 2 {
		t.Fatalf("expected a recompute for the new IP, repo calls = %d", repo.calls)
	}
}

func TestIPWhitelistAssistGetRejectsMalformedCurrentIP(t *testing.T) {
	repo := &assistRepoStub{ips: []RecentClientIP{{IP: "203.0.113.7", Count: 1}}}
	svc := newAssistServiceForTest(repo, func(_ context.Context, ips []string) map[string]ipquality.Geo {
		if len(ips) != 1 || ips[0] != "203.0.113.7" {
			t.Fatalf("malformed current ip must not be looked up: %#v", ips)
		}
		return nil
	})

	for i, raw := range []string{"not-an-ip", "203.0.113.7:443", "1.2.3.4 5.6.7.8"} {
		result, err := svc.Get(context.Background(), 11+int64(i), raw)
		if err != nil {
			t.Fatalf("Get(%q) returned error: %v", raw, err)
		}
		if result.CurrentIP != "" || result.CurrentGeo != (IPGeo{}) {
			t.Fatalf("Get(%q) must blank the current ip, got %#v", raw, result)
		}
	}
}

func TestIPWhitelistAssistGetAcceptsIPv6CurrentIP(t *testing.T) {
	repo := &assistRepoStub{}
	svc := newAssistServiceForTest(repo, func(_ context.Context, ips []string) map[string]ipquality.Geo {
		if len(ips) != 1 || ips[0] != "2001:db8::1" {
			t.Fatalf("unexpected lookup ips: %#v", ips)
		}
		return map[string]ipquality.Geo{"2001:db8::1": {Country: "China", City: "Shenzhen"}}
	})

	result, err := svc.Get(context.Background(), 21, "2001:db8::1")
	if err != nil {
		t.Fatalf("Get returned error: %v", err)
	}
	if result.CurrentIP != "2001:db8::1" || result.CurrentGeo.City != "Shenzhen" {
		t.Fatalf("IPv6 current ip lost: %#v", result)
	}
}
