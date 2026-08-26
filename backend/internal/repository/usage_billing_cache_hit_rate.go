package repository

import (
	"context"
	"time"
)

// preauthCacheHitRateWindow bounds the history used to estimate how much of a
// key's input is served from prompt cache. A rolling 7-day window balances
// responsiveness to changing usage against enough samples to be stable.
const preauthCacheHitRateWindow = 7 * 24 * time.Hour

// preauthCacheHitRateMinRequests requires a minimum sample size before trusting
// a computed hit rate; below this the caller falls back to the conservative
// estimate so a brand-new or barely-used key is never under-held on thin data.
const preauthCacheHitRateMinRequests = 20

// RecentCacheHitRate returns the fraction of billable input tokens that were
// served from prompt cache (cache_read) for this API key over the recent
// window, along with ok=false when there is insufficient history to trust it.
//
// Ratio = sum(cache_read_tokens) / sum(input_tokens + cache_read_tokens).
// Only rows with positive input+cache_read participate. The query is served by
// idx_usage_logs_api_key_created_at. This feeds preauthorization hold
// estimation ONLY; settlement always charges actual usage, so an inaccurate
// ratio can never cause an over/under charge — at worst it makes the reserved
// hold slightly high or low, which finalize reconciles against real cost.
func (r *usageBillingRepository) RecentCacheHitRate(ctx context.Context, apiKeyID int64) (float64, bool, error) {
	if r == nil || r.db == nil || apiKeyID <= 0 {
		return 0, false, nil
	}
	const q = `
SELECT
    COALESCE(SUM(cache_read_tokens), 0) AS cache_read,
    COALESCE(SUM(input_tokens + cache_read_tokens), 0) AS total_input,
    COUNT(*) AS reqs
FROM usage_logs
WHERE api_key_id = $1
  AND created_at > $2
  AND (input_tokens + cache_read_tokens) > 0`
	var cacheRead, totalInput, reqs int64
	if err := r.db.QueryRowContext(ctx, q, apiKeyID, time.Now().Add(-preauthCacheHitRateWindow)).
		Scan(&cacheRead, &totalInput, &reqs); err != nil {
		return 0, false, err
	}
	if reqs < preauthCacheHitRateMinRequests || totalInput <= 0 {
		return 0, false, nil
	}
	rate := float64(cacheRead) / float64(totalInput)
	if rate < 0 {
		rate = 0
	}
	if rate > 1 {
		rate = 1
	}
	return rate, true, nil
}
