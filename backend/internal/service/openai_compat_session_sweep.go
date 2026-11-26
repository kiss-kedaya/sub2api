package service

import (
	"time"
)

// OpenAI 兼容会话绑定（openaiCompatSessionResponses / openaiCompatAnthropicDigestSessions）的回收。
//
// 这两张 sync.Map 的条目都带 ExpiresAt，但过期条目只有在「同一个 key 再被读到」时才会被删：
// 没有再访问的会话永远留在内存里，条目数随「不同会话数」线性增长，直到进程重启。
// 现在每次写入时顺手检查一次：距上次清理超过 openAICompatSessionSweepInterval 就扫一遍，
// 删掉过期条目；扫完仍超过 openAICompatSessionMaxEntries 时再随机丢弃多余的（宁可让个别会话
// 少一次续接命中，也不能无界增长）。没有后台 goroutine，也就没有需要 Stop 的东西。

const (
	openAICompatSessionSweepInterval = 30 * time.Second
	// 每张表的条目上限。一个绑定几百字节，6 万多条在几十 MB 以内。
	openAICompatSessionMaxEntries = 65536
)

// maybeSweepOpenAICompatSessions 在写入绑定之后调用；绝大多数调用只做一次原子比较。
func (s *OpenAIGatewayService) maybeSweepOpenAICompatSessions() {
	if s == nil {
		return
	}
	now := time.Now()
	next := s.openaiCompatSessionNextSweep.Load()
	if now.UnixNano() < next {
		return
	}
	if !s.openaiCompatSessionNextSweep.CompareAndSwap(next, now.Add(openAICompatSessionSweepInterval).UnixNano()) {
		return
	}
	s.sweepOpenAICompatSessions(now, openAICompatSessionMaxEntries)
}

// sweepOpenAICompatSessions 清理过期条目，并把每张表限制在 maxEntries 以内。返回删除的条目数。
func (s *OpenAIGatewayService) sweepOpenAICompatSessions(now time.Time, maxEntries int) (removed int) {
	if s == nil {
		return 0
	}
	kept := 0
	s.openaiCompatSessionResponses.Range(func(key, value any) bool {
		binding, ok := value.(openAICompatSessionResponseBinding)
		if !ok || (!binding.ExpiresAt.IsZero() && now.After(binding.ExpiresAt)) {
			s.openaiCompatSessionResponses.Delete(key)
			removed++
			return true
		}
		kept++
		if kept > maxEntries {
			s.openaiCompatSessionResponses.Delete(key)
			removed++
			kept--
		}
		return true
	})
	kept = 0
	s.openaiCompatAnthropicDigestSessions.Range(func(key, value any) bool {
		binding, ok := value.(openAICompatAnthropicDigestBinding)
		if !ok || (!binding.ExpiresAt.IsZero() && now.After(binding.ExpiresAt)) {
			s.openaiCompatAnthropicDigestSessions.Delete(key)
			removed++
			return true
		}
		kept++
		if kept > maxEntries {
			s.openaiCompatAnthropicDigestSessions.Delete(key)
			removed++
			kept--
		}
		return true
	})
	return removed
}
