//go:build unit

package service

import (
	"bytes"
	"log"
	"log/slog"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

type statsLogBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *statsLogBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *statsLogBuffer) lines() []string {
	b.mu.Lock()
	defer b.mu.Unlock()
	var out []string
	for _, line := range strings.Split(b.buf.String(), "\n") {
		if strings.Contains(line, "auth session summary") {
			out = append(out, line)
		}
	}
	return out
}

func captureStatsLog(t *testing.T) *statsLogBuffer {
	t.Helper()
	prevLogger, prevWriter, prevFlags := slog.Default(), log.Writer(), log.Flags()
	buf := &statsLogBuffer{}
	slog.SetDefault(slog.New(slog.NewTextHandler(buf, &slog.HandlerOptions{Level: slog.LevelDebug})))
	t.Cleanup(func() {
		slog.SetDefault(prevLogger)
		log.SetOutput(prevWriter)
		log.SetFlags(prevFlags)
	})
	return buf
}

// 指纹重绑的审计限频：同一个 key（用户 + 会话家族）每个窗口只放行一条，窗口过后再放行；
// 不同 key 互不影响；表满且都没过期时不再放行新 key（内存有上限），过期的会被清掉腾出位置。
func TestAuthSessionStatsRebindAuditIsRateLimitedPerKey(t *testing.T) {
	st := newAuthSessionStats(time.Hour)
	t0 := time.Date(2026, 11, 26, 12, 0, 0, 0, time.UTC)

	require.True(t, st.allowRebindAudit("1:famA", t0))
	require.False(t, st.allowRebindAudit("1:famA", t0.Add(time.Minute)), "same user+family inside the window")
	require.False(t, st.allowRebindAudit("1:famA", t0.Add(rebindAuditWindow-time.Second)))
	require.True(t, st.allowRebindAudit("1:famB", t0.Add(time.Minute)), "another family of the same user is separate")
	require.True(t, st.allowRebindAudit("2:famA", t0.Add(time.Minute)), "another user is separate")
	require.True(t, st.allowRebindAudit("1:famA", t0.Add(rebindAuditWindow)), "the window passed: one more row is allowed")
	require.False(t, st.allowRebindAudit("1:famA", t0.Add(rebindAuditWindow+time.Second)), "and the new window is limited again")
}

func TestAuthSessionStatsRebindAuditTableIsBounded(t *testing.T) {
	st := newAuthSessionStats(time.Hour)
	t0 := time.Date(2026, 11, 26, 12, 0, 0, 0, time.UTC)
	for i := 0; i < rebindAuditMaxKeys; i++ {
		require.True(t, st.allowRebindAudit(strconv.Itoa(i), t0))
	}
	require.False(t, st.allowRebindAudit("one-more", t0.Add(time.Second)), "a full table of unexpired keys admits no new key")
	require.Len(t, st.rebindAudit, rebindAuditMaxKeys, "memory stays bounded")
	require.True(t, st.allowRebindAudit("after-expiry", t0.Add(rebindAuditWindow)), "expired keys are purged to make room")
	require.LessOrEqual(t, len(st.rebindAudit), rebindAuditMaxKeys)
}

// 第一条事件之后安排一次汇总：只打一条 INFO，里面是累计计数、没有用户 ID；
// 没有新事件时不再打；之后有新事件会再安排下一次（计数是增量，不累计历史）。
func TestAuthSessionStatsSummaryIsOneInfoLineOnlyWhenSomethingChanged(t *testing.T) {
	logs := captureStatsLog(t)
	// 间隔取 250ms：下面紧接着的 snapshot 要在汇总之前读到计数，间隔太短会在慢机器上撞车。
	st := newAuthSessionStats(250 * time.Millisecond)

	st.inc(statRefreshOK)
	st.inc(statRefreshOK)
	st.inc(statRefreshOK)
	st.inc(statRefreshInvalid)
	st.inc(statRefreshReuseLeewayRetry)
	require.Equal(t, map[string]int64{"refresh_ok": 3, "refresh_invalid": 1, "refresh_reuse_leeway_retry": 1}, st.snapshot())

	require.Eventually(t, func() bool { return len(logs.lines()) == 1 }, 5*time.Second, 10*time.Millisecond)
	line := logs.lines()[0]
	require.Contains(t, line, "level=INFO")
	require.Contains(t, line, "refresh_ok=3")
	require.Contains(t, line, "refresh_invalid=1")
	require.Contains(t, line, "refresh_reuse_leeway_retry=1")
	require.NotContains(t, line, "refresh_reuse=", "counters that did not change are not listed")
	require.NotContains(t, line, "user_id")
	require.Empty(t, st.snapshot(), "the summary drains the counters")

	// 没有新事件：再过两个多汇总间隔也不会再打。
	time.Sleep(600 * time.Millisecond)
	require.Len(t, logs.lines(), 1, "an interval without changes must stay silent")

	// 新事件重新安排下一次汇总，里面只有新增量。
	st.inc(statRefreshBindingRebind)
	require.Eventually(t, func() bool { return len(logs.lines()) == 2 }, 5*time.Second, 10*time.Millisecond)
	second := logs.lines()[1]
	require.Contains(t, second, "refresh_binding_rebind=1")
	require.NotContains(t, second, "refresh_ok")
}

// 汇总没有变化时（例如事件刚好被上一次汇总取走）不打日志。
func TestAuthSessionStatsFlushWithoutChangesLogsNothing(t *testing.T) {
	logs := captureStatsLog(t)
	st := newAuthSessionStats(time.Hour)
	st.flush()
	require.Empty(t, logs.lines())
}

// 限频：每种事件一个窗口内最多 authWarnBurst 条，超出的只进 warn_suppressed；窗口过后重新放行。
func TestAuthSessionStatsWarnLimiterIsPerKindAndPerWindow(t *testing.T) {
	st := newAuthSessionStats(time.Hour)
	now := time.Now()
	for i := 0; i < authWarnBurst; i++ {
		require.True(t, st.allowWarn("reuse", now.Add(time.Duration(i)*time.Millisecond)))
	}
	require.False(t, st.allowWarn("reuse", now.Add(time.Second)), "over the burst inside the window")
	require.True(t, st.allowWarn("rebind", now.Add(time.Second)), "another kind has its own budget")
	require.True(t, st.allowWarn("reuse", now.Add(authWarnWindow+time.Second)), "a new window restarts the budget")
	require.EqualValues(t, 1, st.snapshot()["warn_suppressed"])
}
