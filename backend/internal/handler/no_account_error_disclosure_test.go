//go:build unit

package handler

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"net/http"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/Wei-Shaw/sub2api/internal/service"
)

// schedulerInternals are the tokens of the scheduler's own diagnostics. None of
// them may appear in a "no account" response an API caller receives.
var schedulerInternals = []string{
	"group=", "platform=", "pool=", "total=", "eligible=", "schedulable_accounts",
	"model_not_supported", "model_rate_limited", "profit_threshold", "profit_invalid_account_rate",
	"routes_skipped", "filtered:", "inbound=", "protocol_family_match", "cross_protocol_conversion",
	"servable_via_conversion", "wait_plan_exhausted",
}

func requireNoSchedulerInternals(t *testing.T, message string) {
	t.Helper()
	lower := strings.ToLower(message)
	for _, token := range schedulerInternals {
		require.NotContains(t, lower, token, "client message must not carry scheduler internals: %q", message)
	}
}

func TestClientNoAccountMessage(t *testing.T) {
	capacity := noAccountErrorClassification{Status: http.StatusServiceUnavailable, ErrType: "api_error", Message: "Service temporarily unavailable"}
	rateLimited := noAccountErrorClassification{Status: http.StatusTooManyRequests, ErrType: "rate_limit_error", Message: "All available accounts are currently rate-limited. Please retry later."}
	modelMissing := noAccountErrorClassification{Status: http.StatusNotFound, ErrType: "model_not_found", Code: modelNotAvailableCode, Message: `分组内没有账号支持模型 "x"`, ModelNotFound: true}
	planGated := noAccountErrorClassification{Status: http.StatusForbidden, ErrType: "permission_error", Message: "The requested model is not available under the configured upstream plans.", ModelNotFound: true}
	securityPenalty := noAccountErrorClassification{Status: http.StatusForbidden, ErrType: "security_penalty_group_restricted", Message: "This conversation is restricted to the configured group; review your security records"}

	t.Run("capacity failure gets the stable text", func(t *testing.T) {
		got := clientNoAccountMessage(capacity)
		require.Equal(t, noAvailableAccountsMessage, got)
		// ops filters / monitor taxonomy key off this prefix; it must survive.
		require.Contains(t, strings.ToLower(got), "no available accounts")
		requireNoSchedulerInternals(t, got)
	})
	t.Run("classifier-owned verdicts keep their own text", func(t *testing.T) {
		for _, cls := range []noAccountErrorClassification{rateLimited, modelMissing, planGated, securityPenalty} {
			require.Equal(t, cls.Message, clientNoAccountMessage(cls))
		}
	})
}

// 选号失败的原始错误（含分组/池计数/利润门计数）经分类后，客户端文案不得带内部字段。
func TestClientNoAccountMessage_SchedulerErrorNeverReachesClient(t *testing.T) {
	fallback := noAccountErrorClassification{Status: http.StatusServiceUnavailable, ErrType: "api_error", Message: "Service temporarily unavailable"}
	raw := fmt.Errorf("%w supporting model: gpt-x (group=7 platform=openai total=3 eligible=0 excluded=0 unschedulable=1 platform_filtered=0 model_unsupported=0 model_rate_limited=0 profit_threshold=2 profit_invalid_account_rate=0 pool=3 model_not_supported=0 model_rate_limited=0)", service.ErrNoAvailableAccounts)

	cls := classifySelectionFailureError(raw, fallback)
	got := clientNoAccountMessage(cls)

	require.Equal(t, http.StatusServiceUnavailable, cls.Status)
	require.Equal(t, noAvailableAccountsMessage, got)
	requireNoSchedulerInternals(t, got)
}

func TestClassifyNoAccountError_ModelListFollowsGroupAllowlist(t *testing.T) {
	groupID := int64(118)
	apiKeyWithGroup := func(group *service.Group) *service.APIKey {
		return &service.APIKey{GroupID: &groupID, Group: group}
	}
	classify := func(apiKey *service.APIKey, models []string) noAccountErrorClassification {
		fd := &fakeDiagnoser{resp: service.ModelAvailabilityDiagnosis{HasAccountsInPool: true, HasModelSupport: false, AvailableModels: models}}
		return classifyNoAccountError(newTestGinContextWithRequest().Request.Context(), fd, apiKey, "claude-opus-9", "claude-opus-9", service.PlatformAnthropic)
	}

	t.Run("aliases hidden by the allowlist are not listed", func(t *testing.T) {
		group := &service.Group{ID: groupID, Platform: service.PlatformAnthropic,
			ModelAllowlist: service.GroupModelAllowlist{Enabled: true, Models: []string{"public-a", "claude-sonnet-*"}}}

		cls := classify(apiKeyWithGroup(group), []string{"claude-sonnet-4-5", "hidden-alias", "internal-secret-model", "public-a"})

		require.Equal(t, http.StatusNotFound, cls.Status)
		require.Equal(t, modelNotAvailableCode, cls.Code)
		require.Contains(t, cls.Message, `"public-a"`)
		require.Contains(t, cls.Message, `"claude-sonnet-4-5"`)
		require.NotContains(t, cls.Message, "hidden-alias")
		require.NotContains(t, cls.Message, "internal-secret-model")
	})

	t.Run("allowlist that matches nothing falls back to the no-list sentence", func(t *testing.T) {
		group := &service.Group{ID: groupID, Platform: service.PlatformAnthropic,
			ModelAllowlist: service.GroupModelAllowlist{Enabled: true, Models: []string{"only-this"}}}

		cls := classify(apiKeyWithGroup(group), []string{"hidden-alias"})

		require.Equal(t, modelNotAvailableCode, cls.Code)
		require.NotContains(t, cls.Message, "hidden-alias")
		require.Contains(t, cls.Message, "没有可用的模型配置")
	})

	t.Run("group not established means no list at all", func(t *testing.T) {
		for name, key := range map[string]*service.APIKey{
			"group not hydrated": apiKeyWithGroup(nil),
			"group id mismatch":  apiKeyWithGroup(&service.Group{ID: groupID + 1}),
		} {
			cls := classify(key, []string{"hidden-alias"})
			require.Equal(t, modelNotAvailableCode, cls.Code, name)
			require.NotContains(t, cls.Message, "hidden-alias", name)
			require.Contains(t, cls.Message, "没有可用的模型配置", name)
		}
	})

	t.Run("no allowlist keeps the list but caps it", func(t *testing.T) {
		group := &service.Group{ID: groupID, Platform: service.PlatformAnthropic}
		models := make([]string, 0, maxListedAvailableModels+5)
		for i := 0; i < maxListedAvailableModels+5; i++ {
			models = append(models, fmt.Sprintf("model-%02d", i))
		}

		cls := classify(apiKeyWithGroup(group), models)

		require.Contains(t, cls.Message, `"model-00"`)
		require.Contains(t, cls.Message, strconv.Quote(models[maxListedAvailableModels-1]))
		require.NotContains(t, cls.Message, strconv.Quote(models[maxListedAvailableModels]))
	})
}

// 合同测试：网关处理器源码里，选号失败的内部诊断只能进日志。
//   - 不得把 "No available ..." 文案与动态内容拼接后回给客户端（旧实现把 err.Error() 拼进去）；
//   - SelectionFailureProtocolDiagnostics 的结果只能作为 zap.String 字段使用。
func TestGatewayHandlersKeepSelectionDiagnosticsOutOfClientResponses(t *testing.T) {
	files, err := filepath.Glob("*.go")
	require.NoError(t, err)
	require.NotEmpty(t, files)

	for _, name := range files {
		if strings.HasSuffix(name, "_test.go") {
			continue
		}
		file, err := parser.ParseFile(token.NewFileSet(), name, nil, 0)
		require.NoError(t, err, name)

		var stack []ast.Node
		ast.Inspect(file, func(node ast.Node) bool {
			if node == nil {
				stack = stack[:len(stack)-1]
				return true
			}
			stack = append(stack, node)

			if concat, ok := node.(*ast.BinaryExpr); ok && concat.Op == token.ADD {
				for _, operand := range []ast.Expr{concat.X, concat.Y} {
					lit, ok := operand.(*ast.BasicLit)
					if !ok || lit.Kind != token.STRING {
						continue
					}
					text, err := strconv.Unquote(lit.Value)
					require.NoError(t, err, name)
					require.False(t, strings.HasPrefix(strings.ToLower(text), "no available"),
						"%s: %q is concatenated with dynamic text; client messages must be fixed strings", name, text)
				}
			}

			if call, ok := node.(*ast.CallExpr); ok && isSelectorNamed(call.Fun, "SelectionFailureProtocolDiagnostics") && len(stack) >= 2 {
				parent, ok := stack[len(stack)-2].(*ast.CallExpr)
				require.True(t, ok && isSelectorNamed(parent.Fun, "String"),
					"%s: SelectionFailureProtocolDiagnostics must only feed a zap.String log field", name)
			}
			return true
		})
	}
}

func isSelectorNamed(expr ast.Expr, name string) bool {
	selector, ok := expr.(*ast.SelectorExpr)
	return ok && selector.Sel.Name == name
}
