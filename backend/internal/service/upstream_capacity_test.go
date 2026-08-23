package service

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestIsUpstreamCapacityCoolingBody(t *testing.T) {
	tests := []struct {
		name string
		body string
		want bool
	}{
		{
			name: "provider cooling encoded as forbidden",
			body: `{"code":"FORBIDDEN","message":"当前分组内支持该模型的货源均在冷却中。请稍后重试"}`,
			want: true,
		},
		{
			name: "all candidates failed",
			body: `{"error":{"message":"当前分组内所有候选供应商均请求失败"}}`,
			want: true,
		},
		{
			name: "openai overload",
			body: `{"error":{"code":"server_error","message":"Our servers are currently overloaded."}}`,
			want: true,
		},
		{
			name: "provider request blocked",
			body: `{"error":{"message":"Your request was blocked."}}`,
			want: true,
		},
		{
			name: "real credential forbidden",
			body: `{"error":{"code":"FORBIDDEN","message":"invalid api key"}}`,
			want: false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			require.Equal(t, tt.want, IsUpstreamCapacityCoolingBody([]byte(tt.body)))
		})
	}
}
