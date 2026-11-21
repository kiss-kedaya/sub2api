package service

import (
	"context"
	"errors"
	"strings"
	"sync"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/ipquality"
)

// IP whitelist assist defaults. The endpoint is rendered by two modals and is
// clicked by humans, so the budget is intentionally small: one database scan
// and at most one ip-api batch per user per cache window.
const (
	ipWhitelistAssistRecentLimit  = 5
	ipWhitelistAssistDefaultTTL   = 30 * time.Second
	ipWhitelistAssistLookupBudget = 3 * time.Second
	ipWhitelistAssistRecentBudget = 2 * time.Second
	ipWhitelistAssistRateWindow   = time.Minute
	ipWhitelistAssistRateLimit    = 30
	ipWhitelistAssistCacheSweep   = 4096
)

// ErrIPWhitelistAssistRateLimited is returned when one user keeps refreshing
// the assist card faster than the rate window allows. Cached answers are served
// before this check, so normal double clicks never hit it.
var ErrIPWhitelistAssistRateLimited = errors.New("ip whitelist assist rate limited")

// IPGeo is the location data shown next to an IP. Empty fields mean ip-api had
// no answer; the frontend renders that as "地区未知".
type IPGeo struct {
	Country string `json:"country"`
	Region  string `json:"region"`
	City    string `json:"city"`
	ISP     string `json:"isp"`
	Hosting bool   `json:"hosting"`
}

// RecentClientIP is one aggregated client address with its request count.
type RecentClientIP struct {
	IP    string
	Count int
}

// IPWhitelistAssistRepository aggregates the addresses a user recently
// connected from. Implementations must bound the scan (LIMIT + time window).
type IPWhitelistAssistRepository interface {
	ListRecentClientIPs(ctx context.Context, userID int64, limit int) ([]RecentClientIP, error)
}

// IPWhitelistAssistRecentIP is one recent address rendered in the "常用 IP"
// list.
type IPWhitelistAssistRecentIP struct {
	IP    string `json:"ip"`
	Count int    `json:"count"`
	Geo   IPGeo  `json:"geo"`
}

// IPWhitelistAssistResult is the payload of GET /user/ip-whitelist-assist.
type IPWhitelistAssistResult struct {
	CurrentIP  string                      `json:"current_ip"`
	CurrentGeo IPGeo                       `json:"current_geo"`
	RecentIPs  []IPWhitelistAssistRecentIP `json:"recent_ips"`
}

type ipWhitelistAssistCacheEntry struct {
	result    IPWhitelistAssistResult
	expiresAt time.Time
}

type ipWhitelistAssistRateBucket struct {
	windowStart time.Time
	count       int
}

// geoLookuper matches ipquality.LookupGeo and is a field so tests can replace
// the third-party lookup without network access.
type geoLookuper func(ctx context.Context, ips []string) map[string]ipquality.Geo

// IPWhitelistAssistService powers the "fill in my current IP" helper. It caches
// per-user answers for a short TTL, accepts at most one bounded database scan
// and one geo lookup per user per window, and degrades to "IP only, empty geo"
// whenever the repository or ip-api misbehaves.
type IPWhitelistAssistService struct {
	repo   IPWhitelistAssistRepository
	lookup geoLookuper
	now    func() time.Time

	cacheTTL     time.Duration
	lookupBudget time.Duration
	recentBudget time.Duration
	rateWindow   time.Duration
	rateLimit    int

	mu      sync.Mutex
	cache   map[int64]ipWhitelistAssistCacheEntry
	buckets map[int64]*ipWhitelistAssistRateBucket
}

// NewIPWhitelistAssistService wires the production dependencies: the bounded
// recent-IP repository and ipquality's cached ip-api batch lookup.
func NewIPWhitelistAssistService(repo IPWhitelistAssistRepository) *IPWhitelistAssistService {
	return &IPWhitelistAssistService{
		repo:         repo,
		lookup:       ipquality.LookupGeo,
		now:          time.Now,
		cacheTTL:     ipWhitelistAssistDefaultTTL,
		lookupBudget: ipWhitelistAssistLookupBudget,
		recentBudget: ipWhitelistAssistRecentBudget,
		rateWindow:   ipWhitelistAssistRateWindow,
		rateLimit:    ipWhitelistAssistRateLimit,
		cache:        map[int64]ipWhitelistAssistCacheEntry{},
		buckets:      map[int64]*ipWhitelistAssistRateBucket{},
	}
}

// Get returns the assist payload for one user. currentIP is resolved by the
// caller with the same trusted-client-IP logic the API key ACL uses, so the
// value shown here is exactly the value the whitelist will be checked against.
func (s *IPWhitelistAssistService) Get(ctx context.Context, userID int64, currentIP string) (IPWhitelistAssistResult, error) {
	if s == nil {
		return IPWhitelistAssistResult{}, nil
	}
	currentIP = strings.TrimSpace(currentIP)
	now := s.now()

	s.mu.Lock()
	if entry, ok := s.cache[userID]; ok && now.Before(entry.expiresAt) {
		s.mu.Unlock()
		return entry.result, nil
	}
	if !s.allowLocked(userID, now) {
		s.mu.Unlock()
		return IPWhitelistAssistResult{}, ErrIPWhitelistAssistRateLimited
	}
	s.mu.Unlock()

	result := s.compute(ctx, userID, currentIP)

	s.mu.Lock()
	s.storeLocked(userID, result, now)
	s.mu.Unlock()
	return result, nil
}

func (s *IPWhitelistAssistService) compute(ctx context.Context, userID int64, currentIP string) IPWhitelistAssistResult {
	recent := s.recentClientIPs(ctx, userID)

	lookupIPs := make([]string, 0, len(recent)+1)
	if currentIP != "" {
		lookupIPs = append(lookupIPs, currentIP)
	}
	for _, item := range recent {
		if ip := strings.TrimSpace(item.IP); ip != "" && ip != currentIP {
			lookupIPs = append(lookupIPs, ip)
		}
	}

	geos := map[string]ipquality.Geo{}
	if len(lookupIPs) > 0 && s.lookup != nil {
		lookupCtx, cancel := context.WithTimeout(ctx, s.lookupBudget)
		geos = s.lookup(lookupCtx, lookupIPs)
		cancel()
	}

	result := IPWhitelistAssistResult{
		CurrentIP:  currentIP,
		CurrentGeo: geoFor(geos, currentIP),
		RecentIPs:  make([]IPWhitelistAssistRecentIP, 0, len(recent)),
	}
	for _, item := range recent {
		result.RecentIPs = append(result.RecentIPs, IPWhitelistAssistRecentIP{
			IP:    item.IP,
			Count: item.Count,
			Geo:   geoFor(geos, item.IP),
		})
	}
	return result
}

func (s *IPWhitelistAssistService) recentClientIPs(ctx context.Context, userID int64) []RecentClientIP {
	if s.repo == nil {
		return nil
	}
	recentCtx, cancel := context.WithTimeout(ctx, s.recentBudget)
	defer cancel()
	ips, err := s.repo.ListRecentClientIPs(recentCtx, userID, ipWhitelistAssistRecentLimit)
	if err != nil {
		// Recent history is a convenience, never a hard dependency: degrade to
		// an empty list so the current IP can still be filled in.
		return nil
	}
	if len(ips) > ipWhitelistAssistRecentLimit {
		ips = ips[:ipWhitelistAssistRecentLimit]
	}
	return ips
}

func (s *IPWhitelistAssistService) allowLocked(userID int64, now time.Time) bool {
	if s.rateLimit <= 0 || s.rateWindow <= 0 {
		return true
	}
	if s.buckets == nil {
		s.buckets = map[int64]*ipWhitelistAssistRateBucket{}
	}
	bucket, ok := s.buckets[userID]
	if !ok || now.Sub(bucket.windowStart) >= s.rateWindow {
		bucket = &ipWhitelistAssistRateBucket{windowStart: now}
		s.buckets[userID] = bucket
	}
	bucket.count++
	return bucket.count <= s.rateLimit
}

func (s *IPWhitelistAssistService) storeLocked(userID int64, result IPWhitelistAssistResult, now time.Time) {
	if s.cache == nil {
		s.cache = map[int64]ipWhitelistAssistCacheEntry{}
	}
	s.cache[userID] = ipWhitelistAssistCacheEntry{result: result, expiresAt: now.Add(s.cacheTTL)}
	if len(s.cache) > ipWhitelistAssistCacheSweep {
		for id, entry := range s.cache {
			if !now.Before(entry.expiresAt) {
				delete(s.cache, id)
			}
		}
		for id, bucket := range s.buckets {
			if now.Sub(bucket.windowStart) >= s.rateWindow {
				delete(s.buckets, id)
			}
		}
	}
}

func geoFor(geos map[string]ipquality.Geo, ip string) IPGeo {
	geo, ok := geos[ip]
	if !ok {
		return IPGeo{}
	}
	return IPGeo{
		Country: geo.Country,
		Region:  geo.Region,
		City:    geo.City,
		ISP:     geo.ISP,
		Hosting: geo.Hosting,
	}
}
