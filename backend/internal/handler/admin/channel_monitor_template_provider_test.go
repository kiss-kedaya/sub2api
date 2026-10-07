package admin

import (
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestChannelMonitorTemplateAcceptsEverySelectableProvider(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, provider := range []string{"openai", "anthropic", "gemini", "grok", "antigravity", "kimi", "zhipu", "deepseek", "minimax"} {
		t.Run(provider, func(t *testing.T) {
			c, _ := gin.CreateTestContext(httptest.NewRecorder())
			c.Request = httptest.NewRequest("POST", "/", strings.NewReader(`{"name":"example","provider":"`+provider+`","api_mode":"chat_completions"}`))
			c.Request.Header.Set("Content-Type", "application/json")
			var req channelMonitorTemplateCreateRequest
			require.NoError(t, c.ShouldBindJSON(&req))
			require.Equal(t, provider, req.Provider)
		})
	}
}
