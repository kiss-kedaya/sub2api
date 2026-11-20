package handler

import (
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

// Responses WebSocket 帧不经过 HTTP 的 RejectConflictingRequestFields：
// 重复的计费/策略字段同样必须被拒，否则计费按首值、上游按末值绑定。
func TestOpenAIWSConflictingFrameField(t *testing.T) {
	tests := []struct {
		name   string
		body   string
		field  string
		expect bool
	}{
		{
			name:   "duplicate model still rejected",
			body:   `{"type":"response.create","model":"cheap","model":"expensive"}`,
			field:  "model",
			expect: true,
		},
		{
			name:   "duplicate service_tier rejected",
			body:   `{"type":"response.create","model":"gpt-6.1-sol","service_tier":"default","service_tier":"priority"}`,
			field:  "service_tier",
			expect: true,
		},
		{
			name:   "duplicate nested reasoning effort rejected",
			body:   `{"type":"response.create","model":"gpt-6.1-sol","reasoning":{"effort":"low","effort":"high"}}`,
			field:  "reasoning.effort",
			expect: true,
		},
		{
			name:   "normal turn passes",
			body:   `{"type":"response.create","model":"gpt-6.1-sol","previous_response_id":"resp_1","service_tier":"priority","reasoning":{"effort":"high"}}`,
			expect: false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			field, conflict := openAIWSConflictingFrameField([]byte(tt.body))
			require.Equal(t, tt.expect, conflict)
			if tt.expect {
				require.Equal(t, tt.field, field)
			}
		})
	}
}

func TestDuplicateWSFrameFieldMessage(t *testing.T) {
	require.Equal(t, service.ErrDuplicateModelField.Error(), duplicateWSFrameFieldMessage("model"))
	require.Contains(t, duplicateWSFrameFieldMessage("service_tier"), "service_tier")
	require.NotContains(t, duplicateWSFrameFieldMessage("service_tier"), "model")
}
