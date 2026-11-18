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
	done     chan struct{}
}

// upstreamNewAPIResponseIdentity includes the effective route, not just the
// stored proxy ID: editing a proxy or header can select a different wallet.
func upstreamNewAPIResponseIdentity(a *Account, target upstreamBillingProbeTarget, base string, public bool) [32]byte {
	identity := upstreamBillingProbeIdentity(a)
	headers := make(http.Header)
	headers.Set("Accept", "application/json")
	if target.apiKey != "" {
		headers.Set("Authorization", "Bearer "+target.apiKey)
	}
	a.ApplyHeaderOverrides(headers)
	if public {
		headers.Del("Authorization")
		delete(identity, "api_key")
		delete(identity, credKeyHeaderOverrides)
		delete(identity, credKeyHeaderOverrideEnabled)
	}
	identity["effective_headers"] = headers
	identity["effective_proxy"] = target.proxyURL
	identity["effective_base"] = base
	raw, _ := json.Marshal(identity)
	return sha256.Sum256(raw)
}

// fetchNewAPIBalance shares one IP budget per site. A local deferral carries no
// HTTP status, so unknown sites can still try their working billing protocol.
func (s *UpstreamBillingProbeService) fetchNewAPIBalance(ctx context.Context, a *Account, target upstreamBillingProbeTarget, base string) upstreamBillingProbeHTTPResult {
	site := upstreamSiteRootURL(base, "/")
	entry, _ := s.balanceCache.LoadOrStore(site, &upstreamNewAPICachedRead{})
	cached, ok := entry.(*upstreamNewAPICachedRead)
	if !ok {
		return upstreamBillingProbeHTTPResult{failure: "request_failed"}
	}
	key := upstreamNewAPIResponseIdentity(a, target, base, false)
	for {
		if ctx.Err() != nil {
			return upstreamBillingProbeHTTPResult{failure: "request_failed"}
		}
		cached.mu.Lock()
		now := s.currentTime()
		if cached.done != nil {
			done, same := cached.done, key == cached.key
			cached.mu.Unlock()
			if !same {
				return upstreamBillingProbeHTTPResult{failure: "rate_limited"}
			}
			select {
			case <-ctx.Done():
				return upstreamBillingProbeHTTPResult{failure: "request_failed"}
			case <-done:
				continue
			}
		}
		if now.Before(cached.next) {
			result := upstreamBillingProbeHTTPResult{failure: "rate_limited"}
			if key == cached.key {
				result = cached.result
			}
			cached.mu.Unlock()
			return result
		}
		// Reserve before I/O so peers cannot spend the same site's budget.
		cached.key, cached.next, cached.done = key, now.Add(upstreamNewAPIMinInterval), make(chan struct{})
		cached.mu.Unlock()

		result := s.fetchUpstreamProbeBodyFollowingRedirects(ctx, a, target, upstreamSiteRootURL(base, upstreamNewAPIUsagePath), upstreamKindProbeMaxBodyBytes)
		cached.mu.Lock()
		delay := upstreamNewAPIMinInterval
		if result.failure != "" || result.statusCode >= 500 || result.statusCode == http.StatusTooManyRequests {
			cached.failures++
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
		cached.result, cached.next = result, now.Add(delay)
		close(cached.done)
		cached.done = nil
		cached.mu.Unlock()
		return result
	}
}

// newAPIStatus is public metadata. Cache it per effective route, without the key;
// each waiter can cancel without being held by another caller's network I/O.
func (s *UpstreamBillingProbeService) newAPIStatus(ctx context.Context, a *Account, target upstreamBillingProbeTarget, base string) upstreamBillingProbeHTTPResult {
	id := upstreamNewAPIResponseIdentity(a, target, base, true)
	if cached, ok := s.statusCache.Load(id); ok {
		if result, ok := cached.(upstreamBillingProbeHTTPResult); ok {
			return result
		}
	}
	pending := s.probeGroup.DoChan(fmt.Sprintf("status:%x", id), func() (any, error) {
		if cached, ok := s.statusCache.Load(id); ok {
			return cached, nil
		}
		public := target
		public.apiKey = ""
		result := s.fetchUpstreamProbeBody(ctx, a, public, upstreamSiteRootURL(base, upstreamNewAPIStatusPath), upstreamKindProbeMaxBodyBytes)
		s.statusCache.Store(id, result)
		return result, nil
	})
	select {
	case <-ctx.Done():
		return upstreamBillingProbeHTTPResult{failure: "request_failed"}
	case value := <-pending:
		result, _ := value.Val.(upstreamBillingProbeHTTPResult)
		return result
	}
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
