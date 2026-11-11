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

var lookupURL = "http://ip-api.com/batch?fields=status,query,hosting,proxy"

type cacheEntry struct {
	datacenter bool
	expiresAt  time.Time
}

var (
	mu         sync.Mutex
	cache      = map[string]cacheEntry{}
	ttl        = 24 * time.Hour
	httpClient = &http.Client{Timeout: 8 * time.Second}
)

type batchResult struct {
	Status  string `json:"status"`
	Query   string `json:"query"`
	Hosting bool   `json:"hosting"`
	Proxy   bool   `json:"proxy"`
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
			datacenter := result.Hosting || result.Proxy
			cache[result.Query] = cacheEntry{datacenter: datacenter, expiresAt: now.Add(ttl)}
			if datacenter {
				out[result.Query] = true
			}
		}
		mu.Unlock()
	}
	return out
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
