package service

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"net/http"
	"sync"
	"time"
)

const upstreamNewAPIMinInterval = time.Minute + time.Second

type upstreamNewAPICachedRead struct {
	mu       sync.Mutex
	key      [32]byte
	next     time.Time
	result   upstreamBillingProbeHTTPResult
	failures int
}

// fetchNewAPIBalance shares one IP budget per site, including manual clicks and
// different keys. Cached bodies remain key-scoped: a wallet is never guessed
// from another key merely because its host matches.
func (s *UpstreamBillingProbeService) fetchNewAPIBalance(ctx context.Context, a *Account, target upstreamBillingProbeTarget, base string) upstreamBillingProbeHTTPResult {
	site := upstreamSiteRootURL(base, "/")
	entry, _ := s.balanceCache.LoadOrStore(site, &upstreamNewAPICachedRead{})
	cached, ok := entry.(*upstreamNewAPICachedRead)
	if !ok {
		return upstreamBillingProbeHTTPResult{failure: "request_failed"}
	}
	cached.mu.Lock()
	defer cached.mu.Unlock()
	now := s.currentTime()
	key := sha256.Sum256([]byte(target.apiKey + "\x00" + target.proxyURL))
	if now.Before(cached.next) {
		if key == cached.key {
			return cached.result
		}
		return upstreamBillingProbeHTTPResult{statusCode: http.StatusTooManyRequests, failure: "rate_limited"}
	}
	result := s.fetchUpstreamProbeBodyFollowingRedirects(ctx, a, target, upstreamSiteRootURL(base, upstreamNewAPIUsagePath), upstreamKindProbeMaxBodyBytes)
	delay := upstreamNewAPIMinInterval
	if result.failure != "" || result.statusCode >= 500 || result.statusCode == http.StatusTooManyRequests {
		cached.failures++
		// Retries use the next scheduled round, never the IP budget again in this round.
		for i := 1; i < cached.failures && delay < 20*time.Minute; i++ {
			delay *= 2
		}
	} else {
		cached.failures = 0
	}
	if requested := retryAfter(result.header, now); requested > delay {
		delay = requested
	}
	result.observedAt = now
	cached.key, cached.result, cached.next = key, result, now.Add(delay)
	return result
}

// newAPIStatus is public metadata. Read it once per site/route, without the key,
// and remember even an unavailable response so a page cannot poll /api/status.
func (s *UpstreamBillingProbeService) newAPIStatus(ctx context.Context, a *Account, target upstreamBillingProbeTarget, base string) upstreamBillingProbeHTTPResult {
	id := upstreamSiteRootURL(base, upstreamNewAPIStatusPath) + "|" + target.proxyURL
	if cached, ok := s.statusCache.Load(id); ok {
		if result, ok := cached.(upstreamBillingProbeHTTPResult); ok {
			return result
		}
	}
	value, _, _ := s.probeGroup.Do("status:"+id, func() (any, error) {
		if cached, ok := s.statusCache.Load(id); ok {
			return cached, nil
		}
		public := target
		public.apiKey = ""
		result := s.fetchUpstreamProbeBody(ctx, a, public, upstreamSiteRootURL(base, upstreamNewAPIStatusPath), upstreamKindProbeMaxBodyBytes)
		s.statusCache.Store(id, result)
		return result, nil
	})
	result, _ := value.(upstreamBillingProbeHTTPResult)
	return result
}

func (s *UpstreamBillingProbeService) parseNewAPIBalance(ctx context.Context, a *Account, target upstreamBillingProbeTarget, base string, body []byte) (map[string]any, error) {
	status := s.newAPIStatus(ctx, a, target, base)
	ratio := 0.0
	if upstreamProbeAnswered(status) {
		var response struct {
			Data struct {
				QuotaPerUnit *float64 `json:"quota_per_unit"`
			} `json:"data"`
		}
		if err := json.Unmarshal(status.body, &response); err == nil {
			if n, ok := finiteUpstreamBalanceNumber(response.Data.QuotaPerUnit); ok && n > 0 {
				ratio = n
			}
		}
	}
	return parseUpstreamNewAPIUsageResponse(body, ratio)
}

func upstreamNewAPIObservedTime(snapshot *UpstreamBalanceSnapshot, result upstreamBillingProbeHTTPResult, intervalMinutes int) {
	if !result.observedAt.IsZero() {
		snapshot.ReceivedAt = probeTimePtr(result.observedAt)
		snapshot.FreshUntil = probeTimePtr(result.observedAt.Add(2 * time.Duration(intervalMinutes) * time.Minute))
	}
}

func upstreamBalanceFailureStatus(result upstreamBillingProbeHTTPResult) string {
	switch result.statusCode {
	case http.StatusUnauthorized:
		return "invalid_key"
	case http.StatusForbidden:
		return "ip_forbidden"
	case http.StatusTooManyRequests:
		return "rate_limited"
	default:
		return upstreamBalanceReadReason(result)
	}
}

// Reject error envelopes before any billing numbers: New API deliberately
// reports these failures as HTTP 200, sometimes beside otherwise valid fields.
func upstreamBillingEnvelopeError(body []byte) error {
	var envelope map[string]json.RawMessage
	if err := json.Unmarshal(body, &envelope); err != nil {
		return err
	}
	if _, ok := envelope["error"]; ok {
		return fmt.Errorf("upstream billing error")
	}
	return nil
}
