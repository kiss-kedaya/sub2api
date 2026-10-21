package service

import (
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"
)

// 上游状态码优先于 body 里的 overloaded 语义，不能把 500/502 改成 529。
func TestWrapUpstreamErrorForClient_PreservesStatusForOverloadedBody(t *testing.T) {
	for _, status := range []int{http.StatusInternalServerError, http.StatusBadGateway} {
		got, errType, _, message := WrapUpstreamErrorForClient(status, []byte(`{"error":{"type":"overloaded_error","message":"Overloaded"}}`))
		require.Equal(t, status, got)
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
