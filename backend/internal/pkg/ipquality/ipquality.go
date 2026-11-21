// Package ipquality classifies client IPs by their hosting/proxy nature.
package ipquality

import (
	"bytes"
	"context"
	"encoding/json"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"
)

var lookupURL = "http://ip-api.com/batch?fields=status,query,hosting,proxy,country,regionName,city,isp&lang=zh-CN"

type cacheEntry struct {
	datacenter bool
	geo        Geo
	expiresAt  time.Time
}

// Geo is the human-readable location ip-api reports for an address. Fields stay
// empty when the provider has no data, so callers can render "unknown" instead
// of failing the whole request.
type Geo struct {
	Country string `json:"country"`
	Region  string `json:"region"`
	City    string `json:"city"`
	ISP     string `json:"isp"`
	// Hosting marks datacenter/cloud ranges. It is also true for proxy
	// endpoints so a VPN/datacenter egress is never presented as a home line.
	Hosting bool `json:"hosting"`
}

var (
	mu         sync.Mutex
	cache      = map[string]cacheEntry{}
	ttl        = 24 * time.Hour
	httpClient = &http.Client{Timeout: 8 * time.Second}
)

type batchResult struct {
	Status     string `json:"status"`
	Query      string `json:"query"`
	Hosting    bool   `json:"hosting"`
	Proxy      bool   `json:"proxy"`
	Country    string `json:"country"`
	RegionName string `json:"regionName"`
	City       string `json:"city"`
	ISP        string `json:"isp"`
}

// ClassifyDatacenterIPs returns the subset of ips that ip-api flags as
// hosting or proxy. Lookups are best-effort: on transport/API failure cached
// answers are used and unknown IPs are treated as residential, so a third
// party outage never locks users out.
func ClassifyDatacenterIPs(ctx context.Context, ips []string) map[string]bool {
	out := map[string]bool{}
	var missing []string
	now := time.Now()
	mu.Lock()
	for _, raw := range ips {
		ip := strings.TrimSpace(raw)
		if ip == "" || net.ParseIP(ip) == nil {
			continue
		}
		if entry, ok := cache[ip]; ok && now.Before(entry.expiresAt) {
			if entry.datacenter {
				out[ip] = true
			}
			continue
		}
		missing = append(missing, ip)
	}
	mu.Unlock()
	if len(missing) == 0 {
		return out
	}
	for _, result := range lookupAndCache(ctx, missing, now) {
		if result.Hosting || result.Proxy {
			out[result.Query] = true
		}
	}
	return out
}

// LookupGeo resolves country/region/city/ISP (plus the hosting flag) for ips. It
// shares the batch endpoint and the cache of ClassifyDatacenterIPs, so a page
// that shows both the datacenter warning and the location costs one ip-api
// round trip. Lookups are best-effort: unresolved addresses are simply absent
// from the map and third-party failures never surface as errors.
func LookupGeo(ctx context.Context, ips []string) map[string]Geo {
	out := map[string]Geo{}
	var missing []string
	seen := make(map[string]struct{}, len(ips))
	now := time.Now()
	mu.Lock()
	for _, raw := range ips {
		ip := strings.TrimSpace(raw)
		if ip == "" || net.ParseIP(ip) == nil {
			continue
		}
		if _, dup := seen[ip]; dup {
			continue
		}
		seen[ip] = struct{}{}
		if entry, ok := cache[ip]; ok && now.Before(entry.expiresAt) {
			out[ip] = entry.geo
			continue
		}
		missing = append(missing, ip)
	}
	mu.Unlock()
	if len(missing) == 0 {
		return out
	}
	for _, result := range lookupAndCache(ctx, missing, now) {
		out[result.Query] = geoOf(result)
	}
	return out
}

// lookupAndCache fetches addresses (in ip-api sized batches of 100) that were
// missing from the cache and stores every successful answer. Transport or API
// failures are swallowed so one provider outage degrades to unknown locations
// instead of failing the caller.
func lookupAndCache(ctx context.Context, missing []string, now time.Time) []batchResult {
	fetched := make([]batchResult, 0, len(missing))
	for start := 0; start < len(missing); start += 100 {
		end := start + 100
		if end > len(missing) {
			end = len(missing)
		}
		results, err := lookup(ctx, missing[start:end])
		if err != nil {
			continue
		}
		mu.Lock()
		for _, result := range results {
			if result.Status != "success" || result.Query == "" {
				continue
			}
			cache[result.Query] = cacheEntry{
				datacenter: result.Hosting || result.Proxy,
				geo:        geoOf(result),
				expiresAt:  now.Add(ttl),
			}
			fetched = append(fetched, result)
		}
		mu.Unlock()
	}
	return fetched
}

// geoFieldMaxRunes bounds provider-controlled strings before they reach an HTTP
// response and the UI. ip-api is a third party; a broken or hostile answer must
// not be able to bloat the payload or wreck the layout.
const geoFieldMaxRunes = 64

func geoOf(result batchResult) Geo {
	return Geo{
		Country: clampGeoField(result.Country),
		Region:  clampGeoField(result.RegionName),
		City:    clampGeoField(result.City),
		ISP:     clampGeoField(result.ISP),
		Hosting: result.Hosting || result.Proxy,
	}
}

func clampGeoField(value string) string {
	trimmed := strings.TrimSpace(value)
	runes := []rune(trimmed)
	if len(runes) <= geoFieldMaxRunes {
		return trimmed
	}
	return string(runes[:geoFieldMaxRunes])
}

func lookup(ctx context.Context, ips []string) ([]batchResult, error) {
	payload, err := json.Marshal(ips)
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, lookupURL, bytes.NewReader(payload))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return nil, errLookupStatus
	}
	var results []batchResult
	if err := json.NewDecoder(resp.Body).Decode(&results); err != nil {
		return nil, err
	}
	return results, nil
}

type lookupStatusError struct{}

func (lookupStatusError) Error() string { return "ip-api lookup failed" }

var errLookupStatus = lookupStatusError{}
