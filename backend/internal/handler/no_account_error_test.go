//go:build unit

package handler

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"

	"github.com/Wei-Shaw/sub2api/internal/service"
)

type fakeDiagnoser struct {
	calls []fakeDiagnoseCall
	resp  service.ModelAvailabilityDiagnosis
}

type fakeDiagnoseCall struct {
	GroupID  *int64
	Model    string
	Platform string
}

func (f *fakeDiagnoser) DiagnoseModelAvailabilityForPlatform(
	_ context.Context,
	groupID *int64,
	model, platform string,
) service.ModelAvailabilityDiagnosis {
	f.calls = append(f.calls, fakeDiagnoseCall{
		GroupID:  groupID,
		Model:    model,
		Platform: platform,
	})
	return f.resp
}

func ptrInt64(v int64) *int64 { return &v }

// newTestGinContextWithRequest wraps the bare newTestGinContext helper
// (defined in openai_gateway_cyber_test.go) by additionally attaching a stub
// *http.Request so the classifier can extract c.Request.Context().
func newTestGinContextWithRequest() *gin.Context {
	c := newTestGinContext()
	c.Request = httptest.NewRequest(http.MethodPost, "/test", nil)
	return c
}

func TestClassifyNoAccountError_NilDiagnoser_Falls503(t *testing.T) {
	c := newTestGinContextWithRequest()
	apiKey := &service.APIKey{GroupID: ptrInt64(7)}

	cls := classifyNoAccountErrorFromGin(c, nil, apiKey, "gpt-5", "gpt-5", service.PlatformOpenAI)

	require.Equal(t, http.StatusServiceUnavailable, cls.Status)
	require.Equal(t, "api_error", cls.ErrType)
	require.False(t, cls.ModelNotFound)
}

func TestClassifySelectionFailureError_RateLimitedPool(t *testing.T) {
	fallback := noAccountErrorClassification{Status: http.StatusServiceUnavailable, ErrType: "api_error", Message: "Service temporarily unavailable"}

	got := classifySelectionFailureError(
		fmt.Errorf("no available accounts supporting model: gpt-5.6-sol (total=3 eligible=0 model_rate_limited=3)"),
		fallback,
	)

	require.Equal(t, http.StatusTooManyRequests, got.Status)
	require.Equal(t, "rate_limit_error", got.ErrType)
	require.Contains(t, got.Message, "rate-limited")
	require.Equal(t, fallback, classifySelectionFailureError(fmt.Errorf("model_rate_limited=0"), fallback))
	require.Equal(t, fallback, classifySelectionFailureError(fmt.Errorf("no available accounts"), fallback))
}

func TestClassifySelectionFailureError_DeterministicModelCooldown(t *testing.T) {
	fallback := noAccountErrorClassification{Status: http.StatusServiceUnavailable, ErrType: "api_error", Message: "Service temporarily unavailable"}

	t.Run("all upstream accounts reject the model", func(t *testing.T) {
		got := classifySelectionFailureError(
			fmt.Errorf("no available OpenAI accounts (pool=2, filtered: model_not_supported=2)"),
			fallback,
		)
		require.Equal(t, http.StatusNotFound, got.Status)
		require.Equal(t, "model_not_found", got.ErrType)
		require.True(t, got.ModelNotFound)
	})

	t.Run("all upstream plans gate the model", func(t *testing.T) {
		got := classifySelectionFailureError(
			fmt.Errorf("no available OpenAI accounts (pool=2, filtered: model_plan_gated=2)"),
			fallback,
		)
		require.Equal(t, http.StatusForbidden, got.Status)
		require.Equal(t, "permission_error", got.ErrType)
		require.True(t, got.ModelNotFound)
	})

	t.Run("partial deterministic exclusion remains a capacity error", func(t *testing.T) {
		got := classifySelectionFailureError(
			fmt.Errorf("no available OpenAI accounts (pool=2, filtered: excluded=1 model_not_supported=1)"),
			fallback,
		)
		require.Equal(t, fallback, got)
	})

	t.Run("requested model cannot inject a retry classification", func(t *testing.T) {
		got := classifySelectionFailureError(
			fmt.Errorf("no available OpenAI accounts supporting model: fake-model_rate_limited=99 (pool=1, filtered: model_not_supported=1)"),
			fallback,
		)
		require.Equal(t, http.StatusNotFound, got.Status)
		require.Equal(t, "model_not_found", got.ErrType)
	})
}

func TestClassifySelectionFailureErrorFromGin_MarksTerminalModelFailure(t *testing.T) {
	c := newTestGinContextWithRequest()
	fallback := noAccountErrorClassification{Status: http.StatusServiceUnavailable, ErrType: "api_error", Message: "Service temporarily unavailable"}

	got := classifySelectionFailureErrorFromGin(
		c,
		fmt.Errorf("no available OpenAI accounts (pool=1, filtered: model_plan_gated=1)"),
		fallback,
	)

	require.Equal(t, http.StatusForbidden, got.Status)
	require.True(t, service.HasOpsClientBusinessLimited(c))
	require.Equal(t, service.OpsClientBusinessLimitedReasonLocalModelConfiguration, service.OpsClientBusinessLimitedReason(c))
}

func TestClassifyNoAccountError_NilAPIKey_Falls503(t *testing.T) {
	c := newTestGinContextWithRequest()
	fd := &fakeDiagnoser{resp: service.ModelAvailabilityDiagnosis{HasAccountsInPool: true, HasModelSupport: false}}

	cls := classifyNoAccountErrorFromGin(c, fd, nil, "gpt-5", "gpt-5", service.PlatformOpenAI)

	require.Equal(t, http.StatusServiceUnavailable, cls.Status)
	require.False(t, cls.ModelNotFound)
	require.Empty(t, fd.calls, "diagnoser must not be consulted when apiKey missing")
}

func TestClassifyNoAccountError_NilGroupID_Falls503(t *testing.T) {
	c := newTestGinContextWithRequest()
	fd := &fakeDiagnoser{resp: service.ModelAvailabilityDiagnosis{HasAccountsInPool: true, HasModelSupport: false}}
	apiKey := &service.APIKey{GroupID: nil}

	cls := classifyNoAccountErrorFromGin(c, fd, apiKey, "gpt-5", "gpt-5", service.PlatformOpenAI)

	require.Equal(t, http.StatusServiceUnavailable, cls.Status)
	require.False(t, cls.ModelNotFound)
	require.Empty(t, fd.calls, "diagnoser must not be consulted when group not bound")
}

func TestClassifyNoAccountError_EmptyModel_Falls503(t *testing.T) {
	c := newTestGinContextWithRequest()
	fd := &fakeDiagnoser{resp: service.ModelAvailabilityDiagnosis{HasAccountsInPool: true, HasModelSupport: false}}
	apiKey := &service.APIKey{GroupID: ptrInt64(7)}

	cls := classifyNoAccountErrorFromGin(c, fd, apiKey, "   ", "", service.PlatformOpenAI)

	require.Equal(t, http.StatusServiceUnavailable, cls.Status)
	require.False(t, cls.ModelNotFound)
	require.Empty(t, fd.calls)
}

func TestClassifyNoAccountError_ModelNotSupported_Returns404(t *testing.T) {
	c := newTestGinContextWithRequest()
	fd := &fakeDiagnoser{resp: service.ModelAvailabilityDiagnosis{HasAccountsInPool: true, HasModelSupport: false}}
	apiKey := &service.APIKey{GroupID: ptrInt64(42)}

	cls := classifyNoAccountErrorFromGin(c, fd, apiKey, "gpt-5.1-codex-mini", "gpt-5.1-codex-mini", service.PlatformOpenAI)

	require.Equal(t, http.StatusNotFound, cls.Status)
	require.Equal(t, "model_not_found", cls.ErrType)
	require.True(t, cls.ModelNotFound)
	require.Contains(t, cls.Message, "gpt-5.1-codex-mini", "message must surface the requested model")

	require.Len(t, fd.calls, 1)
	require.Equal(t, "gpt-5.1-codex-mini", fd.calls[0].Model)
	require.Equal(t, service.PlatformOpenAI, fd.calls[0].Platform)
	require.NotNil(t, fd.calls[0].GroupID)
	require.Equal(t, int64(42), *fd.calls[0].GroupID)
	require.True(t, service.HasOpsClientBusinessLimited(c))
	require.Equal(t, service.OpsClientBusinessLimitedReasonLocalModelConfiguration, service.OpsClientBusinessLimitedReason(c))
}

func TestClassifyOpenAICompatibleNoAccountError_GrokUsesGrokPlatform(t *testing.T) {
	c := newTestGinContextWithRequest()
	fd := &fakeDiagnoser{resp: service.ModelAvailabilityDiagnosis{HasAccountsInPool: true, HasModelSupport: false}}
	groupID := int64(43)
	apiKey := &service.APIKey{
		GroupID: &groupID,
		Group: &service.Group{
			ID:       groupID,
			Platform: service.PlatformGrok,
		},
	}

	cls := classifyOpenAICompatibleNoAccountErrorFromGin(c, fd, apiKey, "grok-4.5", "grok-4.5")

	require.Equal(t, http.StatusNotFound, cls.Status)
	require.Equal(t, "model_not_found", cls.ErrType)
	require.True(t, cls.ModelNotFound)
	require.Len(t, fd.calls, 1)
	require.Equal(t, service.PlatformGrok, fd.calls[0].Platform)
	require.True(t, service.HasOpsClientBusinessLimited(c))
	require.Equal(t, service.OpsClientBusinessLimitedReasonLocalModelConfiguration, service.OpsClientBusinessLimitedReason(c))

	logErr := openAICompatibleSelectionErrorForLog(
		fmt.Errorf("no available OpenAI accounts supporting model: grok-4.5"),
		service.PlatformGrok,
	)
	require.EqualError(t, logErr, "no available Grok accounts supporting model: grok-4.5")
}

func TestClassifyNoAccountError_PureClassifierDoesNotMarkGinContext(t *testing.T) {
	c := newTestGinContextWithRequest()
	fd := &fakeDiagnoser{resp: service.ModelAvailabilityDiagnosis{HasAccountsInPool: true, HasModelSupport: false}}
	apiKey := &service.APIKey{GroupID: ptrInt64(7)}

	cls := classifyNoAccountError(c.Request.Context(), fd, apiKey, "gpt-5", "gpt-5", service.PlatformOpenAI)

	require.True(t, cls.ModelNotFound)
	require.False(t, service.HasOpsClientBusinessLimited(c))
	require.Empty(t, service.OpsClientBusinessLimitedReason(c))
}

func TestClassifyNoAccountError_HasModelSupport_KeepsRoutingMessageGenerationToCaller(t *testing.T) {
	c := newTestGinContextWithRequest()
	fd := &fakeDiagnoser{resp: service.ModelAvailabilityDiagnosis{HasAccountsInPool: true, HasModelSupport: true}}
	apiKey := &service.APIKey{GroupID: ptrInt64(7)}

	cls := classifyNoAccountErrorFromGin(c, fd, apiKey, "gpt-5", "gpt-5", service.PlatformOpenAI)

	require.Equal(t, http.StatusServiceUnavailable, cls.Status, "model exists somewhere — caller stays on 503")
	require.Equal(t, "api_error", cls.ErrType)
	require.False(t, cls.ModelNotFound)
}

func TestClassifyNoAccountError_ModelSupportedOnlyByRateLimitedAccount_Returns503(t *testing.T) {
	c := newTestGinContextWithRequest()
	// The diagnoser's configured-state lookup still sees the model-supporting
	// account even though normal scheduling has excluded it during cooldown.
	fd := &fakeDiagnoser{resp: service.ModelAvailabilityDiagnosis{HasAccountsInPool: true, HasModelSupport: true}}
	apiKey := &service.APIKey{GroupID: ptrInt64(7)}

	cls := classifyNoAccountErrorFromGin(c, fd, apiKey, "claude-opus-4-8", "claude-opus-4-8", service.PlatformAnthropic)

	require.Equal(t, http.StatusServiceUnavailable, cls.Status)
	require.Equal(t, "api_error", cls.ErrType)
	require.False(t, cls.ModelNotFound, "temporary account cooldown must remain retryable")
}

func TestClassifyNoAccountError_NoAccountsInPool_Stays503(t *testing.T) {
	c := newTestGinContextWithRequest()
	fd := &fakeDiagnoser{resp: service.ModelAvailabilityDiagnosis{HasAccountsInPool: false, HasModelSupport: false}}
	apiKey := &service.APIKey{GroupID: ptrInt64(7)}

	cls := classifyNoAccountErrorFromGin(c, fd, apiKey, "gpt-5", "gpt-5", service.PlatformOpenAI)

	require.Equal(t, http.StatusServiceUnavailable, cls.Status, "empty pool is a service-availability issue, not a model issue")
	require.False(t, cls.ModelNotFound)
}

func TestClassifyNoAccountError_DisplayModelOverridesRoutingForMessage(t *testing.T) {
	c := newTestGinContextWithRequest()
	fd := &fakeDiagnoser{resp: service.ModelAvailabilityDiagnosis{HasAccountsInPool: true, HasModelSupport: false}}
	apiKey := &service.APIKey{GroupID: ptrInt64(7)}

	cls := classifyNoAccountErrorFromGin(c, fd, apiKey, "gpt-5", "claude-3-fancy", service.PlatformOpenAI)

	require.True(t, cls.ModelNotFound)
	require.Contains(t, cls.Message, "claude-3-fancy", "user-facing message must reference the model the user asked for, not the post-mapping routing model")
	require.Len(t, fd.calls, 1)
	require.Equal(t, "gpt-5", fd.calls[0].Model, "diagnosis must run against the routing model (post group dispatch mapping)")
}

func TestClassifyNoAccountError_FromGin_NilContextStillSafe(t *testing.T) {
	fd := &fakeDiagnoser{resp: service.ModelAvailabilityDiagnosis{HasAccountsInPool: true, HasModelSupport: false}}
	apiKey := &service.APIKey{GroupID: ptrInt64(7)}

	cls := classifyNoAccountErrorFromGin(nil, fd, apiKey, "gpt-5", "gpt-5", service.PlatformOpenAI)

	require.Equal(t, http.StatusNotFound, cls.Status, "even with a nil gin context the classifier must still run and yield a coherent response")
	require.True(t, cls.ModelNotFound)
	require.False(t, service.HasOpsClientBusinessLimited(nil))
	require.Empty(t, service.OpsClientBusinessLimitedReason(nil))
}

// 权威的 404 model_not_found 不能被"账号被限流"的 429 盖掉。
//
// 选号失败的错误串同时携带多种过滤原因，例如
// "pool=9, filtered: model_not_supported=8 model_rate_limited=1"：8 个账号根本不支持该模型，
// 剩下 1 个恰好处于模型级冷却。此时 classifyNoAccountError 已通过持久化判据确认整个分组
// 没有账号能服务该模型（ModelNotFound=true），改判成 429 "All available accounts are
// currently rate-limited" 是错误诊断——重试永远不会成功，而把 429 当限流的客户端会反复
// 重试并吞掉 body（Codex 只显示 "exceeded retry limit"），恰好丢掉唯一说明真实原因的信息。
func TestClassifySelectionFailureError_ModelNotFoundIsNotOverriddenByRateLimited(t *testing.T) {
	modelNotFound := noAccountErrorClassification{
		Status:        http.StatusNotFound,
		ErrType:       "model_not_found",
		Message:       `Model "gpt-5.3-codex" is not supported by any configured account in this group`,
		ModelNotFound: true,
	}

	got := classifySelectionFailureError(
		fmt.Errorf("no available OpenAI accounts supporting model: gpt-5.3-codex "+
			"(pool=9, filtered: model_not_supported=8 model_rate_limited=1)"),
		modelNotFound,
	)

	require.Equal(t, modelNotFound, got,
		"分组里没有任何账号能服务该模型时，模型级冷却不该把 404 改判成 429")
}

// 真实调用点的顺序：先 classifyNoAccountErrorFromGin，再 classifySelectionFailureError。
// 覆盖这条链路是为了同时锁住 ops 归因——调用点用 ModelNotFound 决定是否标记
// routing capacity limited，一旦 404 被改判成 429，同一个请求会既被标成
// local model configuration 又被标成容量问题，自相矛盾。
func TestClassifySelectionFailureError_CallSiteChainKeepsModelNotFoundAttribution(t *testing.T) {
	c := newTestGinContextWithRequest()
	fd := &fakeDiagnoser{resp: service.ModelAvailabilityDiagnosis{HasAccountsInPool: true, HasModelSupport: false}}
	apiKey := &service.APIKey{GroupID: ptrInt64(43)}

	cls := classifyNoAccountErrorFromGin(c, fd, apiKey, "gpt-5.3-codex", "gpt-5.3-codex", service.PlatformOpenAI)
	cls = classifySelectionFailureError(
		fmt.Errorf("no available OpenAI accounts supporting model: gpt-5.3-codex "+
			"(pool=9, filtered: model_not_supported=8 model_rate_limited=1)"),
		cls,
	)

	require.Equal(t, http.StatusNotFound, cls.Status)
	require.Equal(t, "model_not_found", cls.ErrType)
	require.True(t, cls.ModelNotFound)
	require.Contains(t, cls.Message, "gpt-5.3-codex")
	require.True(t, service.HasOpsClientBusinessLimited(c))
	require.Equal(t, service.OpsClientBusinessLimitedReasonLocalModelConfiguration, service.OpsClientBusinessLimitedReason(c))
}

// 池子里确实存在能服务该模型、只是全部在冷却的账号时，429 改判仍需保留：
// 这种情况 fallback 是 503（HasModelSupport=true），重试是有意义的。
func TestClassifySelectionFailureError_StillUpgradesNonModelNotFoundFallback(t *testing.T) {
	fallback := noAccountErrorClassification{
		Status:  http.StatusServiceUnavailable,
		ErrType: "api_error",
		Message: "Service temporarily unavailable",
	}

	got := classifySelectionFailureError(
		fmt.Errorf("no available accounts supporting model: gpt-5.6-sol (total=3 eligible=0 model_rate_limited=3)"),
		fallback,
	)

	require.Equal(t, http.StatusTooManyRequests, got.Status)
	require.Equal(t, "rate_limit_error", got.ErrType)
	require.False(t, got.ModelNotFound)
}

// 模型层无账号支持：404 + 稳定机器码 MODEL_NOT_AVAILABLE，并列出分组当前能服务的
// 模型（来自调度/账号模型映射，不用 upstream_model_metadata）。
func TestClassifyNoAccountError_ModelNotAvailable_CodeAndAvailableModels(t *testing.T) {
	c := newTestGinContextWithRequest()
	fd := &fakeDiagnoser{resp: service.ModelAvailabilityDiagnosis{
		HasAccountsInPool: true,
		HasModelSupport:   false,
		AvailableModels:   []string{"claude-sonnet-4-5", "claude-haiku-4-5"},
	}}
	// 只有确认了请求所属分组（Group 已加载且 ID 与 GroupID 一致）才会列出可用模型，
	// 并按该分组的模型白名单过滤；这里分组没有白名单，列表原样展示。
	apiKey := &service.APIKey{GroupID: ptrInt64(118), Group: &service.Group{ID: 118, Platform: service.PlatformAnthropic}}

	cls := classifyNoAccountErrorFromGin(c, fd, apiKey, "claude-opus-9", "claude-opus-9", service.PlatformAnthropic)

	require.Equal(t, http.StatusNotFound, cls.Status)
	require.Equal(t, "model_not_found", cls.ErrType)
	require.Equal(t, "MODEL_NOT_AVAILABLE", cls.Code)
	require.True(t, cls.ModelNotFound)
	require.Contains(t, cls.Message, "claude-opus-9")
	require.Contains(t, cls.Message, "claude-sonnet-4-5")
	require.Contains(t, cls.Message, "claude-haiku-4-5")
}

func TestClassifyNoAccountError_ModelNotAvailable_NoConfiguredModels(t *testing.T) {
	c := newTestGinContextWithRequest()
	fd := &fakeDiagnoser{resp: service.ModelAvailabilityDiagnosis{HasAccountsInPool: true, HasModelSupport: false}}
	apiKey := &service.APIKey{GroupID: ptrInt64(118)}

	cls := classifyNoAccountErrorFromGin(c, fd, apiKey, "claude-opus-9", "claude-opus-9", service.PlatformAnthropic)

	require.Equal(t, "MODEL_NOT_AVAILABLE", cls.Code)
	require.Contains(t, cls.Message, "claude-opus-9")
	require.Contains(t, cls.Message, "没有可用的模型配置")
}

// 协议不匹配：400 PROTOCOL_MISMATCH，body 带 path / group_platform / hint。
func TestWriteProtocolMismatchError_BodyFields(t *testing.T) {
	gin.SetMode(gin.TestMode)
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", nil)

	writeProtocolMismatchError(c, "/v1/responses", service.PlatformAnthropic)

	require.Equal(t, http.StatusBadRequest, rec.Code)
	var payload struct {
		Error struct {
			Type          string `json:"type"`
			Code          string `json:"code"`
			Message       string `json:"message"`
			Path          string `json:"path"`
			GroupPlatform string `json:"group_platform"`
			Hint          string `json:"hint"`
		} `json:"error"`
	}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &payload))
	require.Equal(t, "invalid_request_error", payload.Error.Type)
	require.Equal(t, "PROTOCOL_MISMATCH", payload.Error.Code)
	require.Equal(t, "/v1/responses", payload.Error.Path)
	require.Equal(t, service.PlatformAnthropic, payload.Error.GroupPlatform)
	require.Contains(t, payload.Error.Message, "跨协议转换")
	require.Contains(t, payload.Error.Hint, "/v1/messages")
}

// 真实无候选（协议/平台匹配、转换开启）时仍返回 503，但诊断信息必须带上
// 协议族、分组平台与转换开关状态，便于区分"没号/全忙"与"入口配错"。
func TestSelectionFailureProtocolDiagnostics_AnthropicGroupResponsesIngress(t *testing.T) {
	gin.SetMode(gin.TestMode)
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", nil)
	groupID := int64(118)
	apiKey := &service.APIKey{GroupID: &groupID, Group: &service.Group{ID: groupID, Platform: service.PlatformAnthropic, CrossProtocolConversionEnabled: true}}

	diag := service.SelectionFailureProtocolDiagnostics(c, apiKey, "/v1/responses")

	require.Contains(t, diag, "inbound=/v1/responses")
	require.Contains(t, diag, "inbound_family=openai")
	require.Contains(t, diag, "group_platform=anthropic")
	require.Contains(t, diag, "protocol_family_match=false")
	require.Contains(t, diag, "cross_protocol_conversion=true")
	require.Contains(t, diag, "servable_via_conversion=true")
}
