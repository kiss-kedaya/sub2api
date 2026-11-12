package service

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"time"
)

// Upstream balance reads for an already-identified Sub2API / New API upstream
// must not surface a failure: the account list keeps the last successful value
// and the read is retried until it succeeds. This file holds the bounded retry
// used inside one probe round plus the helpers that decide how the next round
// is scheduled.
//
// Two constraints shape the design:
//
//   - A manual probe runs on the admin request path, so it must never sleep.
//     Only rounds marked by withUpstreamBalanceRetry may wait between attempts;
//     a manual probe records the attempt and lets the scheduler retry.
//   - Retrying must be bounded and cancellable. One round spends at most
//     upstreamBalanceRetryAttempts reads with 1s/3s/9s back-off, and the next
//     scheduled round retries again, so the read continues until the upstream
//     answers instead of looping forever inside a single call.
const (
	// upstreamBalanceRetryAttempts bounds the reads one round spends on an
	// identified upstream before the account is handed back to the scheduler.
	upstreamBalanceRetryAttempts = 3
)

// The delays are variables so tests can shrink them; production always uses
// 1s, 3s, 9s.
var (
	// upstreamBalanceRetryBaseDelay is the first back-off (1s, then 3s, 9s).
	upstreamBalanceRetryBaseDelay = time.Second
	// upstreamBalanceRetryMaxDelay caps a single back-off so a permanently
	// broken upstream is retried at a calm cadence instead of being hammered.
	upstreamBalanceRetryMaxDelay = 9 * time.Second
	// upstreamBalanceRejectedKeyDelay is used when the upstream answered 401,
	// 403 or 429. A rejected key is retried as well — the operator asked for
	// "until it succeeds" — but at a slow cadence, because a relay commonly
	// counts failed authentication attempts against the caller's IP.
	upstreamBalanceRejectedKeyDelay = time.Minute
)

// upstreamBalanceRetryDelay is the back-off before retry attempt (1-based):
// 1s, 3s, 9s, then capped at upstreamBalanceRetryMaxDelay.
func upstreamBalanceRetryDelay(attempt int) time.Duration {
	if attempt < 1 {
		attempt = 1
	}
	delay := upstreamBalanceRetryBaseDelay
	for i := 1; i < attempt; i++ {
		if delay >= upstreamBalanceRetryMaxDelay {
			return upstreamBalanceRetryMaxDelay
		}
		delay *= 3
	}
	if delay > upstreamBalanceRetryMaxDelay {
		return upstreamBalanceRetryMaxDelay
	}
	return delay
}

// upstreamBalanceRetryContextKey marks the probe rounds that are allowed to
// wait between balance read attempts. Manual probes run on the admin request
// path and are deliberately not marked.
type upstreamBalanceRetryContextKey struct{}

// withUpstreamBalanceRetry marks ctx as a background probe round.
func withUpstreamBalanceRetry(ctx context.Context) context.Context {
	return context.WithValue(ctx, upstreamBalanceRetryContextKey{}, true)
}

// upstreamBalanceRetryAllowed reports whether this round may sleep between
// balance read attempts.
func upstreamBalanceRetryAllowed(ctx context.Context) bool {
	allowed, _ := ctx.Value(upstreamBalanceRetryContextKey{}).(bool)
	return allowed
}

// upstreamBalanceWait sleeps for delay unless ctx is cancelled first. It
// reports false when the round was cancelled, so Stop() never leaves a retry
// sleeping behind the service.
func upstreamBalanceWait(ctx context.Context, delay time.Duration) bool {
	if delay <= 0 {
		return ctx.Err() == nil
	}
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-timer.C:
		return true
	}
}

// upstreamBalanceReadResult parses one balance read, or explains why it cannot
// be parsed. A transport failure and a non-2xx answer are failures even when
// the body happens to hold something JSON-shaped.
func upstreamBalanceReadResult(
	result upstreamBillingProbeHTTPResult,
	parse func([]byte) (map[string]any, error),
) (map[string]any, error) {
	if result.failure != "" {
		return nil, errors.New(result.failure)
	}
	if result.statusCode < 200 || result.statusCode >= 300 {
		return nil, fmt.Errorf("http status %d", result.statusCode)
	}
	if len(result.body) == 0 {
		return nil, errors.New("empty_response")
	}
	return parse(result.body)
}

// upstreamBalanceRead retries one recognised balance endpoint until it parses
// or the round budget is spent. A rejected key ends the round immediately: the
// upstream already answered, and repeating it is what gets an IP rate limited.
//
// attempts <= 1 performs a single read, which is what a manual probe uses so it
// never blocks the request thread. The caller must gate the retry on
// upstreamBalanceRetryAllowed as well.
func upstreamBalanceRead(
	ctx context.Context,
	attempts int,
	fetch func() upstreamBillingProbeHTTPResult,
	parse func([]byte) (map[string]any, error),
) (upstreamBillingProbeHTTPResult, map[string]any, bool) {
	if attempts < 1 {
		attempts = 1
	}
	if attempts > upstreamBalanceRetryAttempts {
		attempts = upstreamBalanceRetryAttempts
	}
	var result upstreamBillingProbeHTTPResult
	for attempt := 1; attempt <= attempts; attempt++ {
		if attempt > 1 && !upstreamBalanceWait(ctx, upstreamBalanceRetryDelay(attempt-1)) {
			return result, nil, false
		}
		result = fetch()
		if upstreamProbeRejectedKey(result) {
			return result, nil, false
		}
		if data, err := upstreamBalanceReadResult(result, parse); err == nil {
			return result, data, true
		}
	}
	return result, nil, false
}

// upstreamBalanceReadReason names why a balance read produced no data. It uses
// the same vocabulary the probe snapshot already stores so the tooltip keeps
// explaining what happened without the account list showing a failure.
func upstreamBalanceReadReason(result upstreamBillingProbeHTTPResult) string {
	if result.failure != "" {
		return result.failure
	}
	switch {
	case result.statusCode == http.StatusNotFound || result.statusCode == http.StatusMethodNotAllowed:
		return "unsupported"
	case result.statusCode < 200 || result.statusCode >= 300:
		return "http_error"
	}
	return "invalid_response"
}

// upstreamBalanceRetryPendingSnapshot records a failed read of an identified
// Sub2API / New API upstream without surfacing it as a failure.
//
// When an earlier read succeeded its data is kept together with its own
// timestamps and the status stays "ok", so the account list keeps showing the
// last known balance (the stale marker is derived from fresh_until). When
// nothing was ever read the status is "pending", which the UI renders as a
// read in progress rather than "读取失败".
func upstreamBalanceRetryPendingSnapshot(
	previous *UpstreamBalanceSnapshot,
	now time.Time,
	statusCode int,
	reason string,
) *UpstreamBalanceSnapshot {
	snapshot := &UpstreamBalanceSnapshot{
		Status:        UpstreamBillingProbeStatusPending,
		LastAttemptAt: now,
		HTTPStatus:    statusCode,
		LastError:     reason,
	}
	if previous == nil {
		return snapshot
	}
	snapshot.Data = previous.Data
	snapshot.ReceivedAt = previous.ReceivedAt
	snapshot.FreshUntil = previous.FreshUntil
	snapshot.Source = previous.Source
	snapshot.RetryCount = previous.RetryCount + 1
	if previous.Status == UpstreamBillingProbeStatusOK {
		snapshot.Status = UpstreamBillingProbeStatusOK
	}
	return snapshot
}

// upstreamBalanceRetryNextDelay is the cadence the next scheduled round should
// use for an account whose balance read is still failing.
func upstreamBalanceRetryNextDelay(previous *UpstreamBalanceSnapshot, rejectedKey bool) time.Duration {
	if rejectedKey {
		return upstreamBalanceRejectedKeyDelay
	}
	attempt := 1
	if previous != nil && previous.RetryCount > 0 {
		attempt = previous.RetryCount + 1
	}
	return upstreamBalanceRetryDelay(attempt)
}
