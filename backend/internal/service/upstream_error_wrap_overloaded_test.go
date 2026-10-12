package service

import (
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"
)

// 中转上游把 Anthropic 的 529 overloaded 改写成 500/502 时，按 body 语义还原成
// 官方 529，而不是让客户端拿到一个语义不明的 502。
func TestWrapUpstreamErrorForClient_LiftsOverloadedBodyTo529(t *testing.T) {
	for _, status := range []int{http.StatusInternalServerError, http.StatusBadGateway} {
		got, errType, _, message := WrapUpstreamErrorForClient(status, []byte(`{"error":{"type":"overloaded_error","message":"Overloaded"}}`))
		require.Equal(t, 529, got)
		require.Equal(t, "overloaded_error", errType)
		require.Equal(t, "Overloaded", message)
	}

	// 503 表达「暂时不可用」，保持原状态码不按过载改写。
	got, errType, _, _ := WrapUpstreamErrorForClient(
		http.StatusServiceUnavailable,
		[]byte(`{"error":{"type":"overloaded_error","message":"Our servers are currently overloaded. Please try again later."}}`),
	)
	require.Equal(t, http.StatusServiceUnavailable, got)
	require.Equal(t, "overloaded_error", errType)
}
