//go:build unit

package service

import (
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func countSyncMap(m interface {
	Range(func(key, value any) bool)
}) int {
	n := 0
	m.Range(func(_, _ any) bool { n++; return true })
	return n
}

func TestSweepOpenAICompatSessionsRemovesExpiredAndKeepsLive(t *testing.T) {
	s := &OpenAIGatewayService{}
	now := time.Now()
	for i := 0; i < 1000; i++ {
		s.openaiCompatSessionResponses.Store("old-"+strconv.Itoa(i), openAICompatSessionResponseBinding{ResponseID: "r", ExpiresAt: now.Add(-time.Minute)})
		s.openaiCompatAnthropicDigestSessions.Store("old-"+strconv.Itoa(i), openAICompatAnthropicDigestBinding{PromptCacheKey: "k", ExpiresAt: now.Add(-time.Minute)})
	}
	for i := 0; i < 10; i++ {
		s.openaiCompatSessionResponses.Store("live-"+strconv.Itoa(i), openAICompatSessionResponseBinding{ResponseID: "r", ExpiresAt: now.Add(time.Hour)})
		s.openaiCompatAnthropicDigestSessions.Store("live-"+strconv.Itoa(i), openAICompatAnthropicDigestBinding{PromptCacheKey: "k", ExpiresAt: now.Add(time.Hour)})
	}
	// 零值 ExpiresAt 表示永不过期；类型不对的脏数据直接清掉
	s.openaiCompatSessionResponses.Store("forever", openAICompatSessionResponseBinding{ResponseID: "r"})
	s.openaiCompatSessionResponses.Store("garbage", "not a binding")

	removed := s.sweepOpenAICompatSessions(now, openAICompatSessionMaxEntries)

	require.Equal(t, 2001, removed)
	require.Equal(t, 11, countSyncMap(&s.openaiCompatSessionResponses))
	require.Equal(t, 10, countSyncMap(&s.openaiCompatAnthropicDigestSessions))
}

func TestSweepOpenAICompatSessionsEnforcesEntryCap(t *testing.T) {
	s := &OpenAIGatewayService{}
	now := time.Now()
	for i := 0; i < 100; i++ {
		s.openaiCompatSessionResponses.Store(i, openAICompatSessionResponseBinding{ResponseID: "r", ExpiresAt: now.Add(time.Hour)})
		s.openaiCompatAnthropicDigestSessions.Store(i, openAICompatAnthropicDigestBinding{PromptCacheKey: "k", ExpiresAt: now.Add(time.Hour)})
	}
	s.sweepOpenAICompatSessions(now, 40)
	require.Equal(t, 40, countSyncMap(&s.openaiCompatSessionResponses), "a flood of live unique sessions must not grow the table without bound")
	require.Equal(t, 40, countSyncMap(&s.openaiCompatAnthropicDigestSessions))
}

func TestMaybeSweepOpenAICompatSessionsIsTimeGated(t *testing.T) {
	s := &OpenAIGatewayService{}
	expired := openAICompatSessionResponseBinding{ResponseID: "r", ExpiresAt: time.Now().Add(-time.Hour)}

	s.openaiCompatSessionResponses.Store("a", expired)
	s.maybeSweepOpenAICompatSessions() // 第一次：到点，扫
	require.Zero(t, countSyncMap(&s.openaiCompatSessionResponses))

	s.openaiCompatSessionResponses.Store("b", expired)
	s.maybeSweepOpenAICompatSessions() // 30 秒内再次调用：不扫（热路径只做一次原子比较）
	require.Equal(t, 1, countSyncMap(&s.openaiCompatSessionResponses))

	s.openaiCompatSessionNextSweep.Store(time.Now().Add(-time.Second).UnixNano()) // 时间到了
	s.maybeSweepOpenAICompatSessions()
	require.Zero(t, countSyncMap(&s.openaiCompatSessionResponses))
}

func TestOpenAICompatSessionWritesTriggerTheSweep(t *testing.T) {
	// 写入路径必须真的接了回收：否则没有再被读到的会话永远留在内存里（这就是原来的泄漏）。
	s := &OpenAIGatewayService{}
	for i := 0; i < 50; i++ {
		s.openaiCompatSessionResponses.Store("stale-"+strconv.Itoa(i), openAICompatSessionResponseBinding{ResponseID: "r", ExpiresAt: time.Now().Add(-time.Hour)})
		s.openaiCompatAnthropicDigestSessions.Store("stale-"+strconv.Itoa(i), openAICompatAnthropicDigestBinding{PromptCacheKey: "k", ExpiresAt: time.Now().Add(-time.Hour)})
	}
	account := &Account{ID: 7}

	s.bindOpenAICompatSessionResponseID(nil, nil, account, "session-1", "resp_1")

	require.Equal(t, 1, countSyncMap(&s.openaiCompatSessionResponses), "expired bindings are swept when a new binding is written")
	require.Equal(t, 0, countSyncMap(&s.openaiCompatAnthropicDigestSessions))

	// digest 表的写入路径同样要接上
	s.openaiCompatAnthropicDigestSessions.Store("stale", openAICompatAnthropicDigestBinding{PromptCacheKey: "k", ExpiresAt: time.Now().Add(-time.Hour)})
	s.openaiCompatSessionNextSweep.Store(0)
	s.bindOpenAICompatAnthropicDigestPromptCacheKey(account, 1, "u:abc", "key-1", "")
	require.Equal(t, 1, countSyncMap(&s.openaiCompatAnthropicDigestSessions))
}

func TestModelTransientCooldownIgnoresOversizedModelNames(t *testing.T) {
	c := NewModelTransientCooldown()
	long := strings.Repeat("m", modelTransientMaxModelBytes+1)
	for i := 0; i < 100; i++ {
		c.ObserveFailure(int64(i+1), long)
	}
	require.Zero(t, c.entryCount.Load(), "client-controlled model names beyond the limit must not become long-lived map keys")

	ok := strings.Repeat("m", modelTransientMaxModelBytes)
	c.ObserveFailure(1, ok)
	require.Equal(t, int64(1), c.entryCount.Load())
}
