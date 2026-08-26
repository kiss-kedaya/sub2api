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
// 中止（ErrBalanceWithholdingFailed）即使无 observed usage 也保留 ForwardResult，
// 让统一计费任务记录诊断信息并完成幂等结算。若上游未返回 usage，
// actual=0 必须释放预扣，不得把 hold 伪造成实际消费。
func TestPartialStreamUsageResult_KeepsTopUpAbort(t *testing.T) {
	c := newPartialStreamUsageContext(t)
	resp := newPartialStreamResp()
	// 无 observed usage（终帧未到）但补扣中止：必须保留。
	sr := &streamingResult{usage: &ClaudeUsage{}}
	abortErr := wrapStreamOutputHoldTopUpFailure(ErrBalanceWithholdingFailed)

	kept := partialStreamUsageResult(c, resp, sr, "claude-opus", "claude-opus", time.Now(), abortErr)
	require.NotNil(t, kept, "top-up abort must keep the result so delivered output settles at hold")
	require.Equal(t, "rid-partial", kept.RequestID)
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
