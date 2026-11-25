package service

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestRequestTypeClaudeRefusal(t *testing.T) {
	require.Equal(t, int16(6), int16(RequestTypeClaudeRefusal), "枚举值已写入库，不能改")
	require.True(t, RequestTypeClaudeRefusal.IsValid())
	require.Equal(t, RequestTypeClaudeRefusal, RequestTypeClaudeRefusal.Normalize())
	require.Equal(t, "claude_refusal", RequestTypeClaudeRefusal.String())
	require.Equal(t, RequestTypeClaudeRefusal, RequestTypeFromInt16(6), "读库时 6 不能被归一成 unknown")

	for _, input := range []string{"claude_refusal", "CLAUDE_REFUSAL", "  claude_refusal  "} {
		rt, err := ParseUsageRequestType(input)
		require.NoError(t, err, input)
		require.Equal(t, RequestTypeClaudeRefusal, rt, input)
	}

	_, err := ParseUsageRequestType("refusal")
	require.Error(t, err, "只认 claude_refusal，不认别名")
	_, err = ParseUsageRequestType("bad")
	require.ErrorContains(t, err, "claude_refusal", "非法值的提示里要列出新类型")
}

// 拒答与 stream 正交：legacy 字段沿用调用方给的真实值，不被推导覆盖。
func TestRequestTypeClaudeRefusalKeepsLegacyStreamFields(t *testing.T) {
	stream, ws := ApplyLegacyRequestFields(RequestTypeClaudeRefusal, true, false)
	require.True(t, stream)
	require.False(t, ws)
	stream, ws = ApplyLegacyRequestFields(RequestTypeClaudeRefusal, false, false)
	require.False(t, stream)
	require.False(t, ws)

	for _, wantStream := range []bool{true, false} {
		u := &UsageLog{RequestType: RequestTypeClaudeRefusal, Stream: wantStream}
		require.Equal(t, RequestTypeClaudeRefusal, u.EffectiveRequestType())
		u.SyncRequestTypeAndLegacyFields()
		require.Equal(t, RequestTypeClaudeRefusal, u.RequestType)
		require.Equal(t, wantStream, u.Stream, "拒答不覆盖真实 stream 字段")
		require.False(t, u.OpenAIWSMode)
	}
}

// 在途请求还没有拒答结果：按拒答类型筛选在途面板必须是空，不能误命中流式/同步请求。
func TestInflightRequestTypeMatchesClaudeRefusalNeverMatches(t *testing.T) {
	for _, row := range []UsageInflightSnapshot{{Stream: true}, {Stream: false}, {WebSocket: true}} {
		require.False(t, inflightRequestTypeMatches(row, RequestTypeClaudeRefusal))
	}
}
