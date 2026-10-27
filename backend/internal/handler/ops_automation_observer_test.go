package handler

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestAutomationStreamSuccessRequiresProtocolTerminal(t *testing.T) {
	for _, tc := range []struct {
		name, payload   string
		success, failed bool
	}{
		{"EOF after tokens", "data: {\"type\":\"response.output_text.delta\",\"delta\":\"hello\"}\n\n", false, false},
		{"DONE alone", "data: [DONE]\n\n", false, false},
		{"responses terminal", "data: {\"type\":\"response.completed\",\"response\":{\"status\":\"completed\"}}\n\n", true, false},
		{"terminal then error", "data: {\"type\":\"message_stop\"}\n\ndata: {\"type\":\"error\",\"error\":{\"message\":\"EOF\"}}\n\n", true, true},
		{"chat terminal", "data: {\"choices\":[{\"finish_reason\":\"tool_calls\"}]}\n\n", true, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			w := &automationObserverWriter{}
			for _, part := range []string{tc.payload[:len(tc.payload)/2], tc.payload[len(tc.payload)/2:]} {
				w.observe([]byte(part))
			}
			require.Equal(t, tc.success, w.terminal)
			require.Equal(t, tc.failed, w.failed)
		})
	}
}
func TestAutomationNonstreamRejectsErrorEnvelopeAndEmptySuccess(t *testing.T) {
	require.False(t, automationNonstreamSuccess([]byte(`{"error":{"message":"failure"}}`)))
	require.False(t, automationNonstreamSuccess([]byte(`{"object":"response","status":"incomplete","output":[{}]}`)))
	require.True(t, automationNonstreamSuccess([]byte(`{"object":"response","status":"completed","output":[{}]}`)))
}
