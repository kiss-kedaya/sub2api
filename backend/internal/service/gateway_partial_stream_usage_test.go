package service

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func newPartialStreamUsageContext(t *testing.T) *gin.Context {
	t.Helper()
	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/messages", http.NoBody)
	return c
}

func newPartialStreamResp() *http.Response {
	return &http.Response{Header: http.Header{"X-Request-Id": []string{"rid-partial"}}}
}

// TestPartialStreamUsageResult_KeepsTopUpAbort 证明 Anthropic/Gemini 流式补扣失败主动
// 中止（ErrBalanceWithholdingFailed）保留 ForwardResult 与估算/已观测到的 usage，
// 让统一计费任务按已交付输出完成结算，绝不退回已交付内容的费用。
func TestPartialStreamUsageResult_KeepsTopUpAbort(t *testing.T) {
	c := newPartialStreamUsageContext(t)
	resp := newPartialStreamResp()
	// 无 observed usage（终帧未到）但补扣中止：必须保留。
	sr := &streamingResult{usage: &ClaudeUsage{}}
	abortErr := wrapStreamOutputHoldTopUpFailure(ErrBalanceWithholdingFailed)

	kept := partialStreamUsageResult(c, resp, sr, "claude-opus", "claude-opus", time.Now(), abortErr)
	require.NotNil(t, kept, "top-up abort must keep the result so delivered output settles cleanly")
	require.Equal(t, "rid-partial", kept.RequestID)

	// 带估算已交付输出与输入的补扣中止：必须完整保留 usage 供计费结算。
	srEstimated := &streamingResult{usage: &ClaudeUsage{InputTokens: 20, CacheReadInputTokens: 380, OutputTokens: 128}}
	keptEstimated := partialStreamUsageResult(c, resp, srEstimated, "claude-opus", "claude-opus", time.Now(), abortErr)
	require.NotNil(t, keptEstimated)
	require.Equal(t, 20, keptEstimated.Usage.InputTokens)
	require.Equal(t, 380, keptEstimated.Usage.CacheReadInputTokens)
	require.Equal(t, 128, keptEstimated.Usage.OutputTokens)
}

// TestPartialStreamUsageResult_DropsFailoverAndZeroUsage 证明可重放的 failover 错误恒
// 返回 nil（防重试成功后双重计费），以及普通无 usage 错误仍丢弃（无已交付计费单位）。
func TestPartialStreamUsageResult_DropsFailoverAndZeroUsage(t *testing.T) {
	c := newPartialStreamUsageContext(t)
	resp := newPartialStreamResp()

	// failover 优先于一切：即使带 usage 也不暴露部分结果。
	srWithUsage := &streamingResult{usage: &ClaudeUsage{InputTokens: 10}}
	kept := partialStreamUsageResult(c, resp, srWithUsage, "m", "m", time.Now(),
		&UpstreamFailoverError{StatusCode: http.StatusBadGateway})
	require.Nil(t, kept, "a replayable attempt must not expose partial usage")

	// 普通错误 + 无 observed usage：丢弃（无已交付计费单位可结算）。
	srZero := &streamingResult{usage: &ClaudeUsage{}}
	kept = partialStreamUsageResult(c, resp, srZero, "m", "m", time.Now(), errors.New("stream read error"))
	require.Nil(t, kept)

	// streamResult==nil：无可结算对象，返回 nil（即使是补扣哨兵错误）。
	kept = partialStreamUsageResult(c, resp, nil, "m", "m", time.Now(),
		wrapStreamOutputHoldTopUpFailure(ErrBalanceWithholdingFailed))
	require.Nil(t, kept)
}

// TestPartialStreamUsageResult_KeepsObservedUsageOnGenericError 保持既有 #5148 行为：
// 已观测到 usage 的普通中断（非 failover）继续保留结果计费。
func TestPartialStreamUsageResult_KeepsObservedUsageOnGenericError(t *testing.T) {
	c := newPartialStreamUsageContext(t)
	resp := newPartialStreamResp()
	sr := &streamingResult{usage: &ClaudeUsage{InputTokens: 13, OutputTokens: 4}}

	kept := partialStreamUsageResult(c, resp, sr, "m", "m", time.Now(), errors.New("missing terminal event"))
	require.NotNil(t, kept)
	require.Equal(t, 13, kept.Usage.InputTokens)
	require.Equal(t, 4, kept.Usage.OutputTokens)
}
