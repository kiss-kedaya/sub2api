//go:build unit

package service

import (
	"bytes"
	"compress/gzip"
	"context"
	"errors"
	"fmt"
	"slices"
	"sort"
	"sync"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/pkg/ctxkey"
	"github.com/stretchr/testify/require"
)

// 评审修订涉及的行为：请求级样本去重、快速熔断、误判修正（响应体改判）、冷却到期后的快照刷新、
// token 刷新不得清掉断路器的冷却。

// circuitCtxReq 在网关 ctx 上加一个客户端请求 ID（RequestLogger 中间件写入的那个）。
func circuitCtxReq(id int64, platform, name, requestID string) context.Context {
	return context.WithValue(circuitCtxFor(id, platform, name), ctxkey.RequestID, requestID)
}

func (h *circuitHarness) windowOf(id int64) (total, failed int) {
	return h.b.stateFor(id).window.sum(uint64(h.b.now().UnixNano() / h.b.bucketNanos))
}

// ---- 窗口：撤销与最近 N 个桶 ---------------------------------------------------

func TestCircuitWindowSubRevertsOnlyAFailureInTheSameEpoch(t *testing.T) {
	var w circuitWindow
	const e = uint64(5_000_000)
	w.add(e, true)
	w.add(e, true)
	w.add(e, false)
	w.sub(e)
	total, failed := w.sum(e)
	require.Equal(t, 2, total)
	require.Equal(t, 1, failed)

	// 槽位已被后来的 epoch 轮转：被撤销的样本早已滑出窗口，什么也不做。
	w.add(e+circuitBuckets, true)
	w.sub(e)
	total, failed = w.sum(e + circuitBuckets)
	require.Equal(t, 1, total)
	require.Equal(t, 1, failed)

	// 没有失败可撤：不下溢，成功样本不会被吃掉。
	var ok circuitWindow
	ok.add(e, false)
	ok.sub(e)
	total, failed = ok.sum(e)
	require.Equal(t, 1, total)
	require.Zero(t, failed)

	var empty circuitWindow
	require.NotPanics(t, func() { empty.sub(e) })
}

func TestCircuitWindowSumRecentOnlyCountsTheLastNEpochs(t *testing.T) {
	var w circuitWindow
	const e = uint64(5_000_000)
	w.add(e-3, true)
	w.add(e-1, true)
	w.add(e, false)

	total, failed := w.sumRecent(e, 2)
	require.Equal(t, 2, total, "epochs e-1 and e")
	require.Equal(t, 1, failed)
	total, failed = w.sumRecent(e, 1)
	require.Equal(t, 1, total)
	require.Zero(t, failed)
	total, failed = w.sum(e)
	require.Equal(t, 3, total)
	require.Equal(t, 2, failed)
}

// ---- 同一请求同一账号只算一个样本 -------------------------------------------------

func TestCircuitBreakerOneSamplePerRequestAndAccount(t *testing.T) {
	h := newCircuitHarness(t, nil)
	// 一个客户端请求在账号 1 上被重试 30 次、次次失败：只算 1 个样本。
	ctx := circuitCtxReq(1, "anthropic", "a", "req-1")
	for i := 0; i < 30; i++ {
		h.b.ObserveUpstreamAttempt(ctx, 1, 502, nil)
	}
	require.False(t, h.b.IsOpen(1), "one request retried 30 times is one sample, not thirty")
	require.Zero(t, h.store.callCount())
	st := h.b.Stats()
	require.Equal(t, int64(30), st.Observed)
	require.Equal(t, int64(29), st.Superseded)
	total, failed := h.windowOf(1)
	require.Equal(t, 1, total)
	require.Equal(t, 1, failed)
}

func TestCircuitBreakerLowTrafficAccountIsNotTrippedByRetryingClients(t *testing.T) {
	// 低流量账号一分钟只有几个真实请求：3 个坏请求各被客户端重试 25 次（75 次失败）、2 个成功请求。
	// 不去重的话是 75/77 = 97.4%、样本 >= 20，账号会被摘掉；按请求去重后是 3 失败 + 2 成功。
	h := newCircuitHarness(t, nil)
	for i := 0; i < 3; i++ {
		ctx := circuitCtxReq(1, "anthropic", "a", fmt.Sprintf("bad-%d", i))
		for j := 0; j < 25; j++ {
			h.b.ObserveUpstreamAttempt(ctx, 1, 503, nil)
		}
	}
	for i := 0; i < 2; i++ {
		h.b.ObserveUpstreamAttempt(circuitCtxReq(1, "anthropic", "a", fmt.Sprintf("ok-%d", i)), 1, 200, nil)
	}
	require.False(t, h.b.IsOpen(1))
	require.Zero(t, h.store.callCount())
	total, failed := h.windowOf(1)
	require.Equal(t, 5, total)
	require.Equal(t, 3, failed)
}

func TestCircuitBreakerSuccessfulRetryCancelsTheEarlierFailure(t *testing.T) {
	h := newCircuitHarness(t, nil)
	for i := 0; i < 40; i++ {
		ctx := circuitCtxReq(1, "anthropic", "a", fmt.Sprintf("r-%d", i))
		h.b.ObserveUpstreamAttempt(ctx, 1, 502, nil) // 第一次失败
		h.b.ObserveUpstreamAttempt(ctx, 1, 200, nil) // 同账号重试成功
	}
	require.False(t, h.b.IsOpen(1), "a transient error absorbed by a same-account retry is not a failure of the request")
	require.Zero(t, h.store.callCount())
	total, failed := h.windowOf(1)
	require.Equal(t, 40, total)
	require.Zero(t, failed)
	require.Zero(t, h.b.reqSamples.count(), "a success clears the remembered failure")
}

func TestCircuitBreakerLastAttemptOfARequestWins(t *testing.T) {
	// 第一次成功、第二次失败（例如上游在流里出错后客户端又重试）：以最后一次为准。
	h := newCircuitHarness(t, nil)
	ctx := circuitCtxReq(1, "anthropic", "a", "r-1")
	h.b.ObserveUpstreamAttempt(ctx, 1, 200, nil)
	h.b.ObserveUpstreamAttempt(ctx, 1, 502, nil)
	h.b.ObserveUpstreamAttempt(ctx, 1, 502, nil)
	total, failed := h.windowOf(1)
	require.Equal(t, 2, total, "the success stays (it was never recorded as a failure), the two failures collapse into one")
	require.Equal(t, 1, failed)
}

func TestCircuitBreakerDistinctRequestsAndAccountsAreIndependentSamples(t *testing.T) {
	h := newCircuitHarness(t, nil)
	for i := 0; i < 20; i++ {
		h.b.ObserveUpstreamAttempt(circuitCtxReq(1, "anthropic", "a", fmt.Sprintf("r-%d", i)), 1, 502, nil)
	}
	require.True(t, h.b.IsOpen(1), "20 different requests that each failed are 20 samples")

	// 同一个请求依次失败在两个不同账号上：每个账号各记一个样本（去重键是「请求 + 账号」）。
	h2 := newCircuitHarness(t, nil)
	for i := 0; i < 19; i++ {
		id := fmt.Sprintf("r-%d", i)
		h2.b.ObserveUpstreamAttempt(circuitCtxReq(1, "anthropic", "a", id), 1, 502, nil)
		h2.b.ObserveUpstreamAttempt(circuitCtxReq(2, "anthropic", "b", id), 2, 502, nil)
	}
	for _, acct := range []int64{1, 2} {
		total, failed := h2.windowOf(acct)
		require.Equal(t, 19, total, "account %d", acct)
		require.Equal(t, 19, failed, "account %d", acct)
	}
	require.Zero(t, h2.b.Stats().Superseded)
}

func TestCircuitBreakerWithoutRequestIDDoesNotDeduplicate(t *testing.T) {
	h := newCircuitHarness(t, nil)
	h.feed(1, "anthropic", 502, 20) // circuitCtxFor 没有 request id
	require.True(t, h.b.IsOpen(1))
	require.Zero(t, h.b.Stats().Superseded)
	require.Zero(t, h.b.reqSamples.count())
}

func TestCircuitBreakerRequestSamplesAreBoundedAndExpire(t *testing.T) {
	var c circuitRequestSamples
	now := time.Unix(1_800_000_000, 0).UnixNano()
	for i := 0; i < circuitRequestShards*circuitRequestShardMax*2; i++ {
		c.recordFailure(fmt.Sprintf("req-%d", i), 1, 7, now)
	}
	require.LessOrEqual(t, c.count(), circuitRequestShards*circuitRequestShardMax, "the remembered failures never grow past the cap")
	// 满了之后新的请求不再去重（fail open）：不 panic、不覆盖已有条目。
	_, had := c.recordFailure("req-new-after-full", 1, 7, now)
	require.False(t, had)

	c.purge(now + int64(circuitRequestTTL) + int64(time.Second))
	require.Zero(t, c.count(), "expired entries are purged even without new traffic")
}

func TestCircuitBreakerStatsReportTheRememberedRequestSamples(t *testing.T) {
	h := newCircuitHarness(t, nil)
	require.Zero(t, h.b.Stats().RequestSamples)
	h.b.ObserveUpstreamAttempt(circuitCtxReq(1, "anthropic", "a", "r-1"), 1, 502, nil)
	h.b.ObserveUpstreamAttempt(circuitCtxReq(1, "anthropic", "a", "r-2"), 1, 502, nil)
	h.b.ObserveUpstreamAttempt(circuitCtxReq(1, "anthropic", "a", "r-1"), 1, 502, nil) // 同一个请求再失败：仍是一条
	require.Equal(t, int64(2), h.b.Stats().RequestSamples)
	h.b.ObserveUpstreamAttempt(circuitCtxReq(1, "anthropic", "a", "r-2"), 1, 200, nil) // 成功撤销那条失败记录
	require.Equal(t, int64(1), h.b.Stats().RequestSamples)
}

func TestCircuitBreakerSweepPurgesRequestSamples(t *testing.T) {
	h := newCircuitHarness(t, nil)
	h.b.ObserveUpstreamAttempt(circuitCtxReq(1, "anthropic", "a", "r-1"), 1, 502, nil)
	require.Equal(t, 1, h.b.reqSamples.count())
	h.clock.Advance(4 * time.Minute)
	h.feed(2, "anthropic", 200, 1) // 触发一次顺手清理
	require.Zero(t, h.b.reqSamples.count())
}

func TestCircuitBreakerRequestDedupIsRaceFree(t *testing.T) {
	h := newCircuitHarness(t, nil)
	const goroutines, perG = 16, 400
	var wg sync.WaitGroup
	for g := 0; g < goroutines; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			for i := 0; i < perG; i++ {
				id := fmt.Sprintf("g%d-r%d", g, i)
				acct := int64(1 + i%3)
				ctx := circuitCtxReq(acct, "anthropic", "a", id)
				h.b.ObserveUpstreamAttempt(ctx, acct, 502, nil)
				h.b.ObserveUpstreamAttempt(ctx, acct, 502, nil)
				h.b.ObserveUpstreamAttempt(ctx, acct, 200, nil)
			}
		}(g)
	}
	wg.Wait()
	// 每个请求最终是一次成功：窗口里没有失败，也没有残留的请求级样本。
	for acct := int64(1); acct <= 3; acct++ {
		_, failed := h.windowOf(acct)
		require.Zero(t, failed, "account %d", acct)
		require.False(t, h.b.IsOpen(acct), "account %d", acct)
	}
	require.Zero(t, h.b.reqSamples.count())
}

// ---- 快速熔断 -------------------------------------------------------------------

func TestCircuitBreakerFastTrip(t *testing.T) {
	t.Run("a sudden total failure is caught although the long healthy history dilutes the 5 minute rate", func(t *testing.T) {
		h := newCircuitHarness(t, nil)
		h.feed(1, "anthropic", 200, 5000)
		h.clock.Advance(120 * time.Second)
		h.feed(1, "anthropic", 502, 99)
		require.False(t, h.b.IsOpen(1), "99 failures: below the fast threshold, and 99/5099 is far below 95%")
		h.feed(1, "anthropic", 502, 1)
		require.True(t, h.b.IsOpen(1))

		require.Equal(t, 1, h.store.tripCount())
		trip := h.store.trips[0]
		require.True(t, trip.FastTrip)
		require.Equal(t, 100, trip.Attempts)
		require.Equal(t, 100, trip.Failures)
		require.Equal(t, 60*time.Second, trip.Window, "the evidence covers the last two 30s buckets")
		require.Equal(t, "circuit_breaker: 100.0% of 100 upstream attempts failed in 1m", trip.Reason)
		require.True(t, IsAccountCircuitBreakerReason(trip.Reason))
		require.Equal(t, int64(1), h.b.Stats().FastTrips)
	})
	t.Run("one success among the recent samples prevents it", func(t *testing.T) {
		h := newCircuitHarness(t, nil)
		h.feed(1, "anthropic", 200, 5000)
		h.clock.Advance(120 * time.Second)
		h.feed(1, "anthropic", 502, 60)
		h.feed(1, "anthropic", 200, 1)
		h.feed(1, "anthropic", 502, 60)
		require.False(t, h.b.IsOpen(1))
		require.Zero(t, h.store.callCount())
	})
	t.Run("failures older than the last two buckets do not count", func(t *testing.T) {
		h := newCircuitHarness(t, nil)
		h.feed(1, "anthropic", 200, 5000)
		h.clock.Advance(120 * time.Second)
		h.feed(1, "anthropic", 502, 99)
		h.clock.Advance(60 * time.Second) // 2 个桶之后：那 99 次失败已经不在「最近 2 个桶」里
		h.feed(1, "anthropic", 502, 1)
		require.False(t, h.b.IsOpen(1))
	})
	t.Run("the threshold is configurable and 0 turns it off", func(t *testing.T) {
		h := newCircuitHarness(t, func(s *AccountCircuitBreakerSettings) { s.FastTripMinRequests = 30 })
		h.feed(1, "anthropic", 200, 5000)
		h.clock.Advance(120 * time.Second)
		h.feed(1, "anthropic", 502, 29)
		require.False(t, h.b.IsOpen(1))
		h.feed(1, "anthropic", 502, 1)
		require.True(t, h.b.IsOpen(1))

		off := newCircuitHarness(t, func(s *AccountCircuitBreakerSettings) { s.FastTripMinRequests = 0 })
		off.feed(1, "anthropic", 200, 5000)
		off.clock.Advance(120 * time.Second)
		off.feed(1, "anthropic", 502, 500)
		require.False(t, off.b.IsOpen(1), "with the fast trip off only the 5 minute rate applies")
	})
	t.Run("the normal rate rule still reports the 5 minute window", func(t *testing.T) {
		h := newCircuitHarness(t, nil)
		h.feed(1, "anthropic", 502, 20)
		require.True(t, h.b.IsOpen(1))
		require.False(t, h.store.trips[0].FastTrip)
		require.Equal(t, 5*time.Minute, h.store.trips[0].Window)
		require.Zero(t, h.b.Stats().FastTrips)
	})
}

func TestCircuitBreakerFastTripIsSubjectToTheValveToo(t *testing.T) {
	// 12 个账号都有很长的健康历史，然后同时整个坏掉（本机网络故障）：5 分钟窗口的失败率都还很低，
	// 但最近 2 个桶里一次成功都没有。这些账号是「在失败」，不是健康证据，安全阀必须拦住逐个熔断。
	h := newCircuitHarness(t, nil)
	for id := int64(1); id <= 12; id++ {
		h.feed(id, "anthropic", 200, 3000)
	}
	h.clock.Advance(120 * time.Second)
	for r := 0; r < 100; r++ {
		for id := int64(1); id <= 12; id++ {
			h.feed(id, "anthropic", 502, 1)
		}
	}
	require.Zero(t, h.store.tripCount(), "every participating account is failing right now: nothing is tripped")
	require.GreaterOrEqual(t, h.logs.count("account_circuit_breaker.valve_blocked"), 1)

	// 对照：只有一个账号坏了（其余照常有成功）：快速熔断照常生效。
	g := newCircuitHarness(t, nil)
	for id := int64(1); id <= 12; id++ {
		g.feed(id, "anthropic", 200, 3000)
	}
	g.clock.Advance(120 * time.Second)
	for r := 0; r < 100; r++ {
		for id := int64(2); id <= 12; id++ {
			g.feed(id, "anthropic", 200, 1)
		}
		g.feed(1, "anthropic", 502, 1)
	}
	require.True(t, g.b.IsOpen(1))
	require.Equal(t, 1, g.store.tripCount())
}

func TestCircuitBreakerEarliestRecovery(t *testing.T) {
	h := newCircuitHarness(t, nil)
	_, ok := h.b.EarliestRecovery()
	require.False(t, ok, "nothing is paused")

	h.feed(1, "anthropic", 502, 20) // 冷却到 t0+60s
	h.clock.Advance(10 * time.Second)
	h.feed(2, "openai", 502, 20) // 冷却到 t0+70s
	d, ok := h.b.EarliestRecovery()
	require.True(t, ok)
	require.Equal(t, 50*time.Second, d, "the soonest end among all paused accounts")

	h.clock.Advance(55 * time.Second) // 账号 1 已恢复，账号 2 还剩 5 秒
	d, ok = h.b.EarliestRecovery()
	require.True(t, ok)
	require.Equal(t, 5*time.Second, d)

	h.clock.Advance(10 * time.Second)
	_, ok = h.b.EarliestRecovery()
	require.False(t, ok)

	var nilBreaker *AccountCircuitBreaker
	_, ok = nilBreaker.EarliestRecovery()
	require.False(t, ok)
	off := newCircuitHarness(t, func(s *AccountCircuitBreakerSettings) { s.Enabled = false })
	_, ok = off.b.EarliestRecovery()
	require.False(t, ok)
}

// ---- 误判修正：响应体改判 ----------------------------------------------------------

var (
	grokPolicyBody                 = []byte(`{"error":{"code":"content_policy_violation","message":"Content violates usage guidelines"}}`)
	grokPolicyTextBody             = []byte(`Request blocked by content moderation`)
	grokSuspendedBody              = []byte(`{"error":{"code":"account_suspended","message":"Account suspended"}}`)
	antigravityCapacity            = []byte(`{"error":{"code":503,"message":"The model is overloaded.","status":"UNAVAILABLE","details":[{"@type":"type.googleapis.com/google.rpc.ErrorInfo","reason":"MODEL_CAPACITY_EXHAUSTED","metadata":{"model":"claude-sonnet-4-5"}}]}}`)
	antigravityOther               = []byte(`{"error":{"code":503,"message":"upstream connect error","status":"UNAVAILABLE"}}`)
	anthropicOrgDisabledBody       = []byte(`{"type":"error","error":{"type":"invalid_request_error","message":"This organization has been disabled."}}`)
	anthropicOrgDisabledMixedCase  = []byte(`{"error":{"message":"THIS Organization Has Been DISABLED. Please contact support."}}`)
	anthropicOrgDisabledOnHoldBody = []byte(clientPrivacyOrgOnHoldBody)
	anthropicNormal400Body         = []byte(`{"type":"error","error":{"type":"invalid_request_error","message":"messages: roles must alternate"}}`)
)

func gzipBytesForCircuitTest(t *testing.T, raw []byte) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := gzip.NewWriter(&buf)
	_, err := zw.Write(raw)
	require.NoError(t, err)
	require.NoError(t, zw.Close())
	return buf.Bytes()
}

func TestRefineFailedAttemptTable(t *testing.T) {
	cases := []struct {
		name     string
		platform string
		status   int
		body     []byte
		want     attemptOutcome
	}{
		{"grok content policy 403", PlatformGrok, 403, grokPolicyBody, attemptOK},
		{"grok content policy text 403", PlatformGrok, 403, grokPolicyTextBody, attemptOK},
		{"unlabelled platform is still refined", "", 403, grokPolicyBody, attemptOK},
		{"grok account suspended stays a failure", PlatformGrok, 403, grokSuspendedBody, attemptFailed},
		{"grok plain forbidden stays a failure", PlatformGrok, 403, []byte(`{"error":"Forbidden"}`), attemptFailed},
		{"a truncated body that cannot be recognised stays a failure", PlatformGrok, 403, []byte(`{"error":{"code":"content_poli`), attemptFailed},
		{"policy-looking 403 on another platform is not refined", PlatformAnthropic, 403, grokPolicyBody, attemptFailed},
		{"antigravity capacity 503", PlatformAntigravity, 503, antigravityCapacity, attemptIgnored},
		{"unlabelled platform capacity 503", "", 503, antigravityCapacity, attemptIgnored},
		{"antigravity other 503 stays a failure", PlatformAntigravity, 503, antigravityOther, attemptFailed},
		{"capacity-looking 503 on another platform is not refined", PlatformAnthropic, 503, antigravityCapacity, attemptFailed},
		{"other statuses are never refined", PlatformGrok, 502, grokPolicyBody, attemptFailed},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			require.Equal(t, tc.want, refineFailedAttempt(tc.platform, tc.status, tc.body))
		})
	}
}

func TestRefineOKAttemptTable(t *testing.T) {
	markerAtPeekEdge := append(bytes.Repeat([]byte("a"), UpstreamAttemptBodyPeekLimit-len(anthropicOrgDisabledMarker)), anthropicOrgDisabledMarker...)
	markerSplitAtPeekEdge := append(bytes.Repeat([]byte("a"), UpstreamAttemptBodyPeekLimit-len(anthropicOrgDisabledMarker)+1), anthropicOrgDisabledMarker[:len(anthropicOrgDisabledMarker)-1]...)
	rawCompressed := gzipBytesForCircuitTest(t, anthropicOrgDisabledBody)

	cases := []struct {
		name     string
		platform string
		status   int
		body     []byte
		want     attemptOutcome
	}{
		{"anthropic 400 org disabled", PlatformAnthropic, 400, anthropicOrgDisabledBody, attemptFailed},
		{"anthropic 400 org disabled mixed case", PlatformAnthropic, 400, anthropicOrgDisabledMixedCase, attemptFailed},
		{"anthropic 400 org on hold full payload", PlatformAnthropic, 400, anthropicOrgDisabledOnHoldBody, attemptFailed},
		{"anthropic 400 org disabled plain text", PlatformAnthropic, 400, []byte("This organization has been disabled"), attemptFailed},
		{"unlabelled platform 400 org disabled", "", 400, anthropicOrgDisabledBody, attemptFailed},
		{"anthropic 400 marker finishes exactly at peek limit", PlatformAnthropic, 400, markerAtPeekEdge, attemptFailed},
		{"anthropic 400 marker truncated at peek edge stays OK", PlatformAnthropic, 400, markerSplitAtPeekEdge, attemptOK},
		{"anthropic 400 normal invalid request stays OK", PlatformAnthropic, 400, anthropicNormal400Body, attemptOK},
		{"anthropic 400 empty body stays OK", PlatformAnthropic, 400, nil, attemptOK},
		{"anthropic 400 raw compressed body stays OK", PlatformAnthropic, 400, rawCompressed, attemptOK},
		{"anthropic 400 invalid utf8 binary stays OK", PlatformAnthropic, 400, []byte{0xff, 0xfe, 0x00, 0x80, 0x1f, 0x8b}, attemptOK},
		{"anthropic 400 non-matching similar wording stays OK", PlatformAnthropic, 400, []byte(`{"error":{"message":"account has been disabled"}}`), attemptOK},
		{"anthropic 200 mentioning org disabled stays OK", PlatformAnthropic, 200, anthropicOrgDisabledBody, attemptOK},
		{"anthropic 404 mentioning org disabled stays OK", PlatformAnthropic, 404, anthropicOrgDisabledBody, attemptOK},
		{"anthropic 422 mentioning org disabled stays OK", PlatformAnthropic, 422, anthropicOrgDisabledBody, attemptOK},
		{"openai 400 org disabled is not refined", PlatformOpenAI, 400, anthropicOrgDisabledBody, attemptOK},
		{"grok 400 org disabled is not refined", PlatformGrok, 400, anthropicOrgDisabledBody, attemptOK},
		{"gemini 400 org disabled is not refined", PlatformGemini, 400, anthropicOrgDisabledBody, attemptOK},
		{"antigravity 400 org disabled is not refined", PlatformAntigravity, 400, anthropicOrgDisabledBody, attemptOK},
		{"deepseek 400 org disabled is not refined", PlatformDeepseek, 400, anthropicOrgDisabledBody, attemptOK},
		{"composite 400 org disabled is not refined", PlatformComposite, 400, anthropicOrgDisabledBody, attemptOK},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			require.Equal(t, tc.want, refineOKAttempt(tc.platform, tc.status, tc.body))
			require.Equal(t, tc.want, refineAttemptOutcome(attemptOK, tc.platform, tc.status, tc.body))
		})
	}
}

func TestCircuitBreakerWantsAttemptBodyOnlyWhereItCanChangeTheVerdict(t *testing.T) {
	h := newCircuitHarness(t, nil)
	grok := circuitCtxFor(1, PlatformGrok, "g")
	anti := circuitCtxFor(2, PlatformAntigravity, "a")
	claude := circuitCtxFor(3, PlatformAnthropic, "c")
	openai := circuitCtxFor(5, PlatformOpenAI, "o")
	unknown := circuitCtx("", "")

	require.True(t, h.b.WantsAttemptBody(grok, 1, 403))
	require.False(t, h.b.WantsAttemptBody(grok, 1, 400))
	require.False(t, h.b.WantsAttemptBody(grok, 1, 503))
	require.False(t, h.b.WantsAttemptBody(grok, 1, 502))
	require.False(t, h.b.WantsAttemptBody(grok, 1, 200))
	require.True(t, h.b.WantsAttemptBody(anti, 2, 503))
	require.False(t, h.b.WantsAttemptBody(anti, 2, 403))
	require.False(t, h.b.WantsAttemptBody(anti, 2, 400))
	require.True(t, h.b.WantsAttemptBody(claude, 3, 400))
	require.False(t, h.b.WantsAttemptBody(claude, 3, 403))
	require.False(t, h.b.WantsAttemptBody(claude, 3, 503))
	require.False(t, h.b.WantsAttemptBody(claude, 3, 200))
	require.False(t, h.b.WantsAttemptBody(openai, 5, 400))
	require.True(t, h.b.WantsAttemptBody(unknown, 4, 400), "without a platform label 400 may still be refinable")
	require.True(t, h.b.WantsAttemptBody(unknown, 4, 403), "without a platform label it may still be refinable")
	require.True(t, h.b.WantsAttemptBody(unknown, 4, 503))

	require.False(t, h.b.WantsAttemptBody(context.Background(), 1, 403), "non-gateway traffic is never inspected")
	require.False(t, h.b.WantsAttemptBody(context.Background(), 3, 400), "non-gateway 400 is never inspected")
	require.False(t, h.b.WantsAttemptBody(grok, 0, 403))
	require.False(t, h.b.WantsAttemptBody(claude, 0, 400))
	var nilBreaker *AccountCircuitBreaker
	require.False(t, nilBreaker.WantsAttemptBody(grok, 1, 403))
	require.False(t, nilBreaker.WantsAttemptBody(claude, 3, 400))
	off := newCircuitHarness(t, func(s *AccountCircuitBreakerSettings) { s.Enabled = false })
	require.False(t, off.b.WantsAttemptBody(grok, 1, 403))
	require.False(t, off.b.WantsAttemptBody(claude, 3, 400))
}

func TestCircuitBreakerAnthropicOrgDisabled400TripsAndDoesNotDilute503(t *testing.T) {
	t.Run("pure anthropic 400 org disabled trips at min_requests", func(t *testing.T) {
		h := newCircuitHarness(t, nil)
		ctx := circuitCtxFor(1, PlatformAnthropic, "relay-1")
		for i := 0; i < 19; i++ {
			h.b.ObserveUpstreamAttemptBody(ctx, 1, 400, nil, anthropicOrgDisabledBody)
		}
		require.False(t, h.b.IsOpen(1))
		h.b.ObserveUpstreamAttemptBody(ctx, 1, 400, nil, anthropicOrgDisabledMixedCase)
		require.True(t, h.b.IsOpen(1))
		require.Equal(t, 1, h.store.tripCount())
		require.Equal(t, 20, h.store.trips[0].Attempts)
		require.Equal(t, 20, h.store.trips[0].Failures)
	})

	t.Run("503 flood mixed with 400 org disabled trips instead of diluting error rate below 95%", func(t *testing.T) {
		h := newCircuitHarness(t, nil)
		ctx := circuitCtxFor(1, PlatformAnthropic, "relay-1")
		h.b.ObserveUpstreamAttempt(ctx, 1, 200, nil)
		for i := 0; i < 10; i++ {
			h.b.ObserveUpstreamAttempt(ctx, 1, 503, nil)
			h.b.ObserveUpstreamAttemptBody(ctx, 1, 400, nil, anthropicOrgDisabledOnHoldBody)
		}
		require.True(t, h.b.IsOpen(1), "10x 503 + 10x 400 org-disabled + 1x 200 = 20/21 (95.2%) must trip")
		require.Equal(t, 1, h.store.tripCount())
		require.False(t, h.store.trips[0].FastTrip)
		require.Equal(t, 21, h.store.trips[0].Attempts)
		require.Equal(t, 20, h.store.trips[0].Failures)
	})

	t.Run("503 flood mixed with 400 org disabled triggers fast-trip despite healthy 5m history", func(t *testing.T) {
		h := newCircuitHarness(t, nil)
		ctx := circuitCtxFor(1, PlatformAnthropic, "relay-1")
		h.feed(1, PlatformAnthropic, 200, 5000)
		h.clock.Advance(120 * time.Second)
		for i := 0; i < 59; i++ {
			h.b.ObserveUpstreamAttempt(ctx, 1, 503, nil)
		}
		for i := 0; i < 40; i++ {
			h.b.ObserveUpstreamAttemptBody(ctx, 1, 400, nil, anthropicOrgDisabledBody)
		}
		require.False(t, h.b.IsOpen(1), "99 failures in recent 2 buckets: one short of fast-trip threshold")
		h.b.ObserveUpstreamAttemptBody(ctx, 1, 400, nil, anthropicOrgDisabledMixedCase)
		require.True(t, h.b.IsOpen(1), "100 failures with 0 successes in recent 2 buckets must fast-trip")
		require.Equal(t, 1, h.store.tripCount())
		require.True(t, h.store.trips[0].FastTrip)
		require.Equal(t, 100, h.store.trips[0].Attempts)
		require.Equal(t, 100, h.store.trips[0].Failures)
		require.Equal(t, int64(1), h.b.Stats().FastTrips)
	})

	t.Run("normal 400 still counts as live upstream and blocks fast-trip", func(t *testing.T) {
		h := newCircuitHarness(t, nil)
		ctx := circuitCtxFor(1, PlatformAnthropic, "relay-1")
		h.feed(1, PlatformAnthropic, 200, 5000)
		h.clock.Advance(120 * time.Second)
		h.feed(1, PlatformAnthropic, 503, 60)
		h.b.ObserveUpstreamAttemptBody(ctx, 1, 400, nil, anthropicNormal400Body)
		h.feed(1, PlatformAnthropic, 503, 60)
		require.False(t, h.b.IsOpen(1), "a normal invalid-request 400 proves the upstream is alive")
		require.Zero(t, h.store.callCount())
	})

	t.Run("same-request retry getting 400 org disabled after 503 stays a failure sample", func(t *testing.T) {
		h := newCircuitHarness(t, nil)
		ctx := circuitCtxReq(1, PlatformAnthropic, "relay-1", "req-retry-1")
		h.b.ObserveUpstreamAttempt(ctx, 1, 503, nil)
		h.b.ObserveUpstreamAttemptBody(ctx, 1, 400, nil, anthropicOrgDisabledBody)
		total, failed := h.windowOf(1)
		require.Equal(t, 1, total)
		require.Equal(t, 1, failed, "400 org disabled must not clear the prior 503 failure as a success")
	})

	t.Run("unlabelled platform 400 org disabled still trips", func(t *testing.T) {
		h := newCircuitHarness(t, nil)
		ctx := circuitCtx("", "")
		for i := 0; i < 20; i++ {
			h.b.ObserveUpstreamAttemptBody(ctx, 1, 400, nil, anthropicOrgDisabledBody)
		}
		require.True(t, h.b.IsOpen(1))
	})
}

func TestCircuitBreakerNonOrgDisabled400NeverTrips(t *testing.T) {
	rawCompressed := gzipBytesForCircuitTest(t, anthropicOrgDisabledBody)
	cases := []struct {
		name     string
		platform string
		status   int
		body     []byte
		bodyless bool
	}{
		{"anthropic normal 400", PlatformAnthropic, 400, anthropicNormal400Body, false},
		{"anthropic 400 bodyless", PlatformAnthropic, 400, nil, true},
		{"anthropic 400 empty body", PlatformAnthropic, 400, nil, false},
		{"anthropic 400 raw compressed body", PlatformAnthropic, 400, rawCompressed, false},
		{"anthropic 200 with org disabled text", PlatformAnthropic, 200, anthropicOrgDisabledBody, false},
		{"openai 400 with org disabled text", PlatformOpenAI, 400, anthropicOrgDisabledBody, false},
		{"grok 400 with org disabled text", PlatformGrok, 400, anthropicOrgDisabledBody, false},
		{"gemini 400 with org disabled text", PlatformGemini, 400, anthropicOrgDisabledBody, false},
		{"antigravity 400 with org disabled text", PlatformAntigravity, 400, anthropicOrgDisabledBody, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			h := newCircuitHarness(t, nil)
			ctx := circuitCtxFor(1, tc.platform, "acct-1")
			for i := 0; i < 300; i++ {
				if tc.bodyless {
					h.b.ObserveUpstreamAttempt(ctx, 1, tc.status, nil)
				} else {
					h.b.ObserveUpstreamAttemptBody(ctx, 1, tc.status, nil, tc.body)
				}
			}
			require.False(t, h.b.IsOpen(1))
			require.Zero(t, h.store.callCount())
			require.Equal(t, int64(300), h.b.Stats().Observed)
			require.Zero(t, h.b.Stats().Failures)
		})
	}
}

func TestCircuitBreakerGrokContentPolicyRefusalsDoNotTripAnAccount(t *testing.T) {
	h := newCircuitHarness(t, nil)
	ctx := circuitCtxFor(1, PlatformGrok, "grok-1")
	for i := 0; i < 300; i++ {
		h.b.ObserveUpstreamAttemptBody(ctx, 1, 403, nil, grokPolicyBody)
	}
	require.False(t, h.b.IsOpen(1), "the prompt was refused, the account is fine")
	require.Zero(t, h.store.callCount())
	require.Equal(t, int64(300), h.b.Stats().Observed, "counted as a live upstream answer")
	require.Zero(t, h.b.Stats().Failures)

	// 对照：账号被封的 403 照常熔断。
	g := newCircuitHarness(t, nil)
	gctx := circuitCtxFor(1, PlatformGrok, "grok-1")
	for i := 0; i < 20; i++ {
		g.b.ObserveUpstreamAttemptBody(gctx, 1, 403, nil, grokSuspendedBody)
	}
	require.True(t, g.b.IsOpen(1))
}

func TestCircuitBreakerAntigravityCapacityExhaustionIsNotAnAccountFailure(t *testing.T) {
	h := newCircuitHarness(t, nil)
	ctx := circuitCtxFor(1, PlatformAntigravity, "ag-1")
	for i := 0; i < 500; i++ {
		h.b.ObserveUpstreamAttemptBody(ctx, 1, 503, nil, antigravityCapacity)
	}
	require.False(t, h.b.IsOpen(1), "the shared model capacity pool is exhausted; switching or pausing this account changes nothing")
	require.Zero(t, h.b.Stats().Observed)
	require.Equal(t, int64(500), h.b.Stats().IgnoredNoise)

	g := newCircuitHarness(t, nil)
	gctx := circuitCtxFor(1, PlatformAntigravity, "ag-1")
	for i := 0; i < 20; i++ {
		g.b.ObserveUpstreamAttemptBody(gctx, 1, 503, nil, antigravityOther)
	}
	require.True(t, g.b.IsOpen(1), "any other 503 is still a failure")
}

func TestCircuitBreakerBodylessObservationKeepsTheStatusVerdict(t *testing.T) {
	// 没读到响应体（传输层没预读、或响应体为空）：保持状态码的判断，宁可多计一次。
	h := newCircuitHarness(t, nil)
	ctx := circuitCtxFor(1, PlatformGrok, "g")
	for i := 0; i < 20; i++ {
		h.b.ObserveUpstreamAttempt(ctx, 1, 403, nil)
	}
	require.True(t, h.b.IsOpen(1))

	g := newCircuitHarness(t, nil)
	for i := 0; i < 20; i++ {
		g.b.ObserveUpstreamAttemptBody(circuitCtxFor(2, PlatformGrok, "g"), 2, 403, nil, nil)
	}
	require.True(t, g.b.IsOpen(2), "an empty prefix cannot refine anything")
}

// ---- 冷却到期后的调度快照刷新 ---------------------------------------------------------

type circuitTimer struct {
	after time.Duration
	f     func()
}

// circuitTimers 是假的 time.AfterFunc：记下延迟，由测试决定何时触发。
type circuitTimers struct {
	mu      sync.Mutex
	pending []circuitTimer
}

func (c *circuitTimers) AfterFunc(d time.Duration, f func()) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.pending = append(c.pending, circuitTimer{after: d, f: f})
}

func (c *circuitTimers) delays() []time.Duration {
	c.mu.Lock()
	defer c.mu.Unlock()
	out := make([]time.Duration, 0, len(c.pending))
	for _, p := range c.pending {
		out = append(out, p.after)
	}
	return out
}

// fireAll 触发当前所有待执行的定时器并清空队列（回调里新安排的定时器留在队列里等下一次）。
func (c *circuitTimers) fireAll() int {
	c.mu.Lock()
	pending := c.pending
	c.pending = nil
	c.mu.Unlock()
	for _, p := range pending {
		p.f()
	}
	return len(pending)
}

type circuitRefreshRecorder struct {
	mu    sync.Mutex
	calls []int64
	errs  []error // 按调用顺序返回；用完后返回 nil
	panic bool
}

func (r *circuitRefreshRecorder) refresh(_ context.Context, id int64) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.calls = append(r.calls, id)
	if r.panic {
		panic("refresh boom")
	}
	if len(r.errs) >= len(r.calls) {
		return r.errs[len(r.calls)-1]
	}
	return nil
}

func (r *circuitRefreshRecorder) callIDs() []int64 {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]int64(nil), r.calls...)
}

func newRecoveryHarness(t *testing.T) (*circuitHarness, *circuitTimers, *circuitRefreshRecorder) {
	t.Helper()
	h := newCircuitHarness(t, nil)
	timers := &circuitTimers{}
	rec := &circuitRefreshRecorder{}
	h.b.afterFunc = timers.AfterFunc
	h.b.SetRecoveryRefresher(rec.refresh)
	return h, timers, rec
}

func TestCircuitBreakerSchedulesARecoveryRefreshAfterTheCooldown(t *testing.T) {
	h, timers, rec := newRecoveryHarness(t)
	h.feed(1, "anthropic", 502, 20)
	require.True(t, h.b.IsOpen(1))
	require.Equal(t, []time.Duration{60*time.Second + circuitRecoveryGrace}, timers.delays(),
		"the refresh is due when the cooldown ends plus a small grace so that NOW() in SQL is past until")
	require.Empty(t, rec.callIDs(), "nothing happens before the timer fires")

	require.Equal(t, 1, timers.fireAll())
	require.Equal(t, []int64{1}, rec.callIDs())
	require.Equal(t, int64(1), h.b.Stats().RecoveryRefreshes)
	require.Empty(t, timers.delays())
}

func TestCircuitBreakerRecoveryRefreshDelayTracksTheActualCooldown(t *testing.T) {
	h, timers, _ := newRecoveryHarness(t)
	h.b.cooldown = 90 * time.Second
	h.feed(1, "anthropic", 502, 20)
	require.Equal(t, []time.Duration{90*time.Second + circuitRecoveryGrace}, timers.delays())
}

func TestCircuitBreakerDoesNotScheduleARefreshWhenThePauseWasNotPersisted(t *testing.T) {
	h, timers, rec := newRecoveryHarness(t)
	h.store.setErr(errors.New("db down"))
	h.feed(1, "anthropic", 502, 20)
	require.Empty(t, timers.delays(), "the account was never paused in the database: nothing to refresh later")
	require.Empty(t, rec.callIDs())

	// 没有挂刷新回调（测试替身 / 仓库不支持）：不安排任何定时器。
	g := newCircuitHarness(t, nil)
	gt := &circuitTimers{}
	g.b.afterFunc = gt.AfterFunc
	g.feed(1, "anthropic", 502, 20)
	require.Empty(t, gt.delays())
}

func TestCircuitBreakerRetriesAFailedRecoveryRefreshAFewTimes(t *testing.T) {
	h, timers, rec := newRecoveryHarness(t)
	rec.errs = []error{errors.New("outbox insert failed"), errors.New("outbox insert failed")}
	h.feed(1, "anthropic", 502, 20)

	require.Equal(t, 1, timers.fireAll()) // 第 1 次失败
	require.Equal(t, []time.Duration{circuitPersistRetryDelay}, timers.delays())
	require.Equal(t, 1, timers.fireAll()) // 第 2 次失败
	require.Equal(t, []time.Duration{circuitPersistRetryDelay}, timers.delays())
	require.Equal(t, 1, timers.fireAll()) // 第 3 次成功
	require.Equal(t, []int64{1, 1, 1}, rec.callIDs())
	require.Empty(t, timers.delays())
	st := h.b.Stats()
	require.Equal(t, int64(2), st.RecoveryRefreshFailed)
	require.Equal(t, int64(1), st.RecoveryRefreshes)
	require.Equal(t, 2, h.logs.count("account_circuit_breaker.recovery_refresh_failed"))
}

func TestCircuitBreakerGivesUpOnARecoveryRefreshAfterTheAttemptLimit(t *testing.T) {
	h, timers, rec := newRecoveryHarness(t)
	failure := errors.New("outbox insert failed")
	rec.errs = []error{failure, failure, failure, failure, failure}
	h.feed(1, "anthropic", 502, 20)
	for i := 0; i < circuitRecoveryRefreshAttempts; i++ {
		require.Equal(t, 1, timers.fireAll())
	}
	require.Empty(t, timers.delays(), "no more retries: the account reappears at the next full snapshot rebuild at the latest")
	require.Len(t, rec.callIDs(), circuitRecoveryRefreshAttempts)
}

func TestCircuitBreakerRecoveryRefreshPanicIsContained(t *testing.T) {
	h, timers, rec := newRecoveryHarness(t)
	rec.panic = true
	h.feed(1, "anthropic", 502, 20)
	require.NotPanics(t, func() { timers.fireAll() })
	require.Equal(t, int64(1), h.b.Stats().RecoveryRefreshFailed)
}

// breakerSnapshotRepo 模拟真实仓库在调度快照里的行为：构建调度桶的查询（ListSchedulableByGroupIDAndPlatform）
// 会排除 temp_unschedulable_until 还没到期的账号，所以账号冷却到期时不会自己回到桶里。
type breakerSnapshotRepo struct {
	*batchAccountQueryRepo
	mu        sync.Mutex
	accounts  map[int64]*Account
	now       func() time.Time
	refreshed []int64
	process   func(ctx context.Context, id int64) error // 模拟 outbox worker 处理一条账号变更事件
}

func (r *breakerSnapshotRepo) GetByID(_ context.Context, id int64) (*Account, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	acc, ok := r.accounts[id]
	if !ok {
		return nil, ErrAccountNotFound
	}
	cp := *acc
	return &cp, nil
}

func (r *breakerSnapshotRepo) ListSchedulableByGroupIDAndPlatform(_ context.Context, groupID int64, platform string) ([]Account, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	now := r.now()
	var out []Account
	for _, acc := range r.accounts {
		if acc.Platform != platform || acc.Status != StatusActive || !acc.Schedulable || !slices.Contains(acc.GroupIDs, groupID) {
			continue
		}
		if acc.TempUnschedulableUntil != nil && acc.TempUnschedulableUntil.After(now) {
			continue
		}
		out = append(out, *acc)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out, nil
}

func (r *breakerSnapshotRepo) SetTempUnschedulable(_ context.Context, id int64, until time.Time, reason string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	acc := r.accounts[id]
	if acc != nil && (acc.TempUnschedulableUntil == nil || acc.TempUnschedulableUntil.Before(until)) {
		u := until
		acc.TempUnschedulableUntil = &u
		acc.TempUnschedulableReason = reason
	}
	return nil
}

func (r *breakerSnapshotRepo) RefreshSchedulerAccount(ctx context.Context, id int64) error {
	r.mu.Lock()
	r.refreshed = append(r.refreshed, id)
	r.mu.Unlock()
	return r.process(ctx, id)
}

func TestCircuitBreakerRecoveryPutsTheAccountBackIntoTheSchedulerBucket(t *testing.T) {
	h, timers, _ := newRecoveryHarness(t)
	repo := &breakerSnapshotRepo{
		batchAccountQueryRepo: newBatchAccountQueryRepo(),
		now:                   h.clock.Now,
		accounts: map[int64]*Account{
			1: {ID: 1, Platform: PlatformOpenAI, Status: StatusActive, Schedulable: true, GroupIDs: []int64{11}},
			2: {ID: 2, Platform: PlatformOpenAI, Status: StatusActive, Schedulable: true, GroupIDs: []int64{11}},
		},
	}
	cache := newBulkEventSnapshotCache()
	svc := NewSchedulerSnapshotService(cache, nil, repo, nil, &config.Config{RunMode: config.RunModeStandard})
	// 模拟 outbox worker：处理一条账号变更事件 = 重新读账号并重建它所在的桶。
	repo.process = func(ctx context.Context, id int64) error {
		return svc.handleAccountEvent(ctx, &id, nil, make(map[batchSeenKey]struct{}))
	}
	h.b.persist = func(ctx context.Context, trip AccountCircuitTrip) error {
		if err := repo.SetTempUnschedulable(ctx, trip.AccountID, trip.Until, trip.Reason); err != nil {
			return err
		}
		return repo.process(ctx, trip.AccountID) // SetTempUnschedulable 同一事务里写了 outbox 事件
	}
	h.b.SetRecoveryRefresher(repo.RefreshSchedulerAccount)

	bucket := SchedulerBucket{GroupID: 11, Platform: PlatformOpenAI, Mode: SchedulerModeSingle}
	bucketIDs := func() []int64 {
		_, _, _, writes := cache.bucketState(bucket)
		require.NotEmpty(t, writes, "the bucket has been rebuilt at least once")
		last := writes[len(writes)-1].accounts
		ids := make([]int64, 0, len(last))
		for _, a := range last {
			ids = append(ids, a.ID)
		}
		return ids
	}

	require.NoError(t, repo.process(context.Background(), 1))
	require.Equal(t, []int64{1, 2}, bucketIDs())

	// 账号 1 被熔断：写库的同时快照重建，它离开桶。
	h.feed(1, PlatformOpenAI, 502, 20)
	require.Equal(t, []int64{2}, bucketIDs(), "a paused account leaves the bucket")

	// 冷却到期，但没有任何事件：账号不会自己回到桶里——这正是需要「到期刷新」的原因。
	h.clock.Advance(60*time.Second + circuitRecoveryGrace)
	require.False(t, h.b.IsOpen(1))
	require.Equal(t, []int64{2}, bucketIDs(), "without a refresh event the bucket keeps excluding the account until the next full rebuild")
	require.Empty(t, repo.refreshed)

	// 到期刷新触发：账号 1 回到桶里。
	require.Equal(t, 1, timers.fireAll())
	require.Equal(t, []int64{1}, repo.refreshed)
	require.Equal(t, []int64{1, 2}, bucketIDs(), "the cooldown ended and the account is schedulable again within the grace period")
}

// ---- token 刷新不得清掉断路器的冷却 ----------------------------------------------------

func TestTokenRefreshKeepsACircuitBreakerCooldown(t *testing.T) {
	newService := func() (*TokenRefreshService, *tokenRefreshAccountRepo, *tempUnschedCacheStub) {
		repo := &tokenRefreshAccountRepo{}
		tempCache := &tempUnschedCacheStub{}
		cfg := &config.Config{TokenRefresh: config.TokenRefreshConfig{MaxRetries: 1, RetryBackoffSeconds: 0}}
		return NewTokenRefreshService(repo, nil, nil, nil, nil, &tokenCacheInvalidatorStub{}, nil, cfg, tempCache), repo, tempCache
	}
	refresher := &tokenRefresherStub{credentials: map[string]any{"access_token": "new-token"}}

	t.Run("a breaker cooldown survives a successful refresh", func(t *testing.T) {
		svc, repo, tempCache := newService()
		until := time.Now().Add(time.Minute)
		account := &Account{
			ID: 16, Platform: PlatformGemini, Type: AccountTypeOAuth, TempUnschedulableUntil: &until,
			TempUnschedulableReason: "circuit_breaker: 100.0% of 20 upstream attempts failed in 5m",
		}
		require.NoError(t, svc.refreshWithRetry(context.Background(), account, refresher, refresher, time.Hour))
		require.Equal(t, 1, repo.updateCalls, "the credentials were refreshed")
		require.Zero(t, repo.clearTempCalls, "a valid token says nothing about the upstream recovering")
		require.Zero(t, tempCache.deleteCalls)
	})
	t.Run("any other temporary pause is still cleared (the 401 recovery path)", func(t *testing.T) {
		svc, repo, tempCache := newService()
		until := time.Now().Add(time.Minute)
		account := &Account{
			ID: 17, Platform: PlatformGemini, Type: AccountTypeOAuth, TempUnschedulableUntil: &until,
			TempUnschedulableReason: `{"status_code":401,"error_message":"token expired"}`,
		}
		require.NoError(t, svc.refreshWithRetry(context.Background(), account, refresher, refresher, time.Hour))
		require.Equal(t, 1, repo.clearTempCalls)
		require.Equal(t, 1, tempCache.deleteCalls)
	})
}
