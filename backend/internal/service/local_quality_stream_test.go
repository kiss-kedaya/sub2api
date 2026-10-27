package service

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestLocalQualityStreamRequiresGenuineCompleteAnswer(t *testing.T) {
	s := &AccountTestService{}
	for _, tc := range []struct {
		name, protocol, stream string
		complete               bool
	}{
		{"Claude EOF", "anthropic", "data: {\"type\":\"content_block_delta\",\"delta\":{\"text\":\"partial\"}}\n", false},
		{"Gemini EOF", "gemini", "data: {\"candidates\":[{\"content\":{\"parts\":[{\"text\":\"partial\"}]}}]}\n", false},
		{"Chat DONE", "chat", "data: {\"choices\":[{\"delta\":{\"content\":\"partial\"}}]}\ndata: [DONE]\n", false},
		{"Chat truncated", "chat", "data: {\"choices\":[{\"delta\":{\"content\":\"partial\"},\"finish_reason\":\"length\"}]}\n", false},
		{"Responses incomplete", "responses", "data: {\"type\":\"response.done\",\"response\":{\"status\":\"incomplete\"}}\n", false},
		{"Claude complete", "anthropic", "data: {\"type\":\"message_delta\",\"delta\":{\"stop_reason\":\"end_turn\"}}\ndata: {\"type\":\"message_stop\"}\n", true},
		{"Chat complete", "chat", "data: {\"choices\":[{\"finish_reason\":\"stop\"}]}\n", true},
		{"Gemini complete", "gemini", "data: {\"candidates\":[{\"finishReason\":\"STOP\"}]}\n", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			w := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(w)
			c.Request = httptest.NewRequest(http.MethodGet, "/", nil)
			e := s.processLocalQualityStream(c, strings.NewReader(tc.stream), tc.protocol)
			if tc.complete {
				require.NoError(t, e)
			} else {
				require.Error(t, e)
				require.NotContains(t, w.Body.String(), `"success":true`)
			}
		})
	}
}
