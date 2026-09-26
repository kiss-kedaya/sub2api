package handler

// /v1/messages 调度闸门回归。
//
// 历史背景：sanitizeGroupMessagesDispatchFields 对非 openai/composite 平台强制
// AllowMessagesDispatch=false，曾让 CN 分组恒 403，故补了平台豁免。闸门最初存在
// 是因为 /v1/messages 走原生 Anthropic 直通，只对开放该能力的组合有意义。
//
// 现在 Anthropic 兼容平台会把 /v1/messages 按账号能力转成上游 Responses / Chat
// Completions，这条入口不再依赖分组开关，因此闸门整体放行；下面的用例锁定这个
// 语义，避免重启开关时把 grok/CN/composite 的既有豁免一起弄丢。

import (
	"net/http/httptest"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestAllowOpenAICompatibleMessagesDispatch_CNProvidersExempt(t *testing.T) {
	require.True(t, allowOpenAICompatibleMessagesDispatch(nil, nil), "无 key 保持放行")

	for _, platform := range []string{service.PlatformKimi, service.PlatformZhipu, service.PlatformDeepseek, service.PlatformMiniMax, service.PlatformGrok} {
		apiKey := &service.APIKey{Group: &service.Group{Platform: platform, AllowMessagesDispatch: false}}
		require.True(t, allowOpenAICompatibleMessagesDispatch(nil, apiKey),
			"%s 分组必须豁免 allow_messages_dispatch 闸门", platform)
	}

	// openai 分组：开关已不再拦截，两种取值都放行。
	openaiOff := &service.APIKey{Group: &service.Group{Platform: service.PlatformOpenAI, AllowMessagesDispatch: false}}
	require.True(t, allowOpenAICompatibleMessagesDispatch(nil, openaiOff),
		"openai 分组不再受 allow_messages_dispatch 拦截")
	openaiOn := &service.APIKey{Group: &service.Group{Platform: service.PlatformOpenAI, AllowMessagesDispatch: true}}
	require.True(t, allowOpenAICompatibleMessagesDispatch(nil, openaiOn))

	namedGrok := &service.APIKey{Group: &service.Group{Platform: service.PlatformOpenAI, Name: "Grok分组", AllowMessagesDispatch: false}}
	require.True(t, allowOpenAICompatibleMessagesDispatch(nil, namedGrok),
		"OpenAI 平台但名叫 Grok 的分组必须放行 Claude Code /v1/messages")
}

func TestAllowOpenAICompatibleMessagesDispatch_OpenAIGroupResolvedGrok(t *testing.T) {
	gin.SetMode(gin.TestMode)
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest("POST", "/v1/messages", nil)
	c.Request = c.Request.WithContext(service.WithResolvedTargetPlatform(c.Request.Context(), service.PlatformGrok))
	apiKey := &service.APIKey{Group: &service.Group{Platform: service.PlatformOpenAI, AllowMessagesDispatch: false}}
	require.True(t, allowOpenAICompatibleMessagesDispatch(c, apiKey),
		"Claude Code 打 grok-* 时，OpenAI 平台的 Grok 分组不能再 403")
}

func TestAllowOpenAICompatibleMessagesDispatch_CompositeResolvedTargets(t *testing.T) {
	gin.SetMode(gin.TestMode)

	newCompositeCtx := func(model string) *gin.Context {
		c, _ := gin.CreateTestContext(httptest.NewRecorder())
		c.Request = httptest.NewRequest("POST", "/v1/messages", nil)
		apiKey := &service.APIKey{Group: &service.Group{Platform: service.PlatformComposite, AllowMessagesDispatch: false}}
		ensureCompositeTargetPlatform(c, apiKey, model)
		return c
	}

	// composite 分组不再受开关约束：无论解析到哪个目标都放行。
	for _, model := range []string{"grok-4.3", "kimi-k2-thinking", "glm-5.2", "deepseek-v3.2", "gpt-5.5"} {
		c := newCompositeCtx(model)
		require.True(t, allowOpenAICompatibleMessagesDispatch(c, &service.APIKey{
			Group: &service.Group{Platform: service.PlatformComposite, AllowMessagesDispatch: false},
		}), "model=%s", model)
	}

	// 未解析出目标平台也放行，闸门不再是 403 来源。
	cNone, _ := gin.CreateTestContext(httptest.NewRecorder())
	cNone.Request = httptest.NewRequest("POST", "/v1/messages", nil)
	require.True(t, allowOpenAICompatibleMessagesDispatch(cNone,
		&service.APIKey{Group: &service.Group{Platform: service.PlatformComposite, AllowMessagesDispatch: false}}))
}

// composite 解析到 grok/CN 目标时，Group 级调度映射（gpt-5.x 默认值为 openai
// 专属）不得注入，模型改写完全交给账号级 model_mapping。
func TestResolveOpenAIMessagesDispatchMappedModel_CompositeCNTargetsSkipGroupMapping(t *testing.T) {
	gin.SetMode(gin.TestMode)

	for _, model := range []string{"kimi-k2-thinking", "glm-5.2", "deepseek-v3.2", "grok-4.3"} {
		c, _ := gin.CreateTestContext(httptest.NewRecorder())
		c.Request = httptest.NewRequest("POST", "/v1/messages", nil)
		apiKey := &service.APIKey{Group: &service.Group{Platform: service.PlatformComposite}}
		ensureCompositeTargetPlatform(c, apiKey, model)

		require.Empty(t, resolveOpenAIMessagesDispatchMappedModel(c, apiKey, "claude-sonnet-4-5-20250929"), "model=%s", model)
	}
}

func TestAllowOpenAICompatibleMessagesDispatch_SmartRoutingResolvedOpenAI(t *testing.T) {
	gin.SetMode(gin.TestMode)
	primaryID := int64(1)
	secondID := int64(2)
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest("POST", "/v1/messages", nil)
	apiKey := &service.APIKey{
		GroupID:       &primaryID,
		RouteGroupIDs: []int64{primaryID, secondID},
		Group:         &service.Group{ID: primaryID, Platform: service.PlatformAnthropic, AllowMessagesDispatch: false},
	}
	ensureCompositeTargetPlatform(c, apiKey, "gpt-5")
	require.True(t, allowOpenAICompatibleMessagesDispatch(c, apiKey),
		"闸门整体放行后，Anthropic 主分组也不再被 /v1/messages 开关拦截")
}
