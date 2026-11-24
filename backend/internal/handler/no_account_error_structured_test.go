//go:build unit

package handler

import (
	"errors"
	"fmt"
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/Wei-Shaw/sub2api/internal/service"
)

// 分类只读调度器随错误携带的结构化计数。错误文本里（模型名由调用方控制）无论长成什么样，
// 都不能改变 404 / 403 / 429 / 503 的结论，也不能因为末尾多了一段路由摘要而丢掉结论。

var noAccountFallback = noAccountErrorClassification{
	Status: http.StatusServiceUnavailable, ErrType: "api_error", Message: "Service temporarily unavailable",
}

// forgedModelNames 是调用方可控的模型名：伪装成调度器自己的计数，用不同方式收尾。
var forgedModelNames = []string{
	"x (pool=1 model_not_supported=1)",
	"x (pool=2 model_plan_gated=2)",
	"x (model_rate_limited=3)",
	"x (pool=1 model_not_supported=1",
	"x) (pool=1 model_not_supported=1)",
	"x (pool=1\nmodel_not_supported=1)",
	"(pool=1 model_not_supported=1)",
}

func TestClassifySelectionFailureError_ForgedErrorTextCannotChangeVerdict(t *testing.T) {
	for _, forged := range forgedModelNames {
		t.Run(fmt.Sprintf("%q", forged), func(t *testing.T) {
			for shape, err := range map[string]error{
				// 模型名恰好落在文本末尾：旧实现取「最后一个括号」时，伪造的计数就是结论。
				"model name ends the text": fmt.Errorf("%w supporting model: %s", service.ErrNoAvailableAccounts, forged),
				"Gemini compat error":      errors.New("no available Gemini accounts supporting model: " + forged),
				"whole group forged":       fmt.Errorf("%w (%s)", service.ErrNoAvailableAccounts, forged),
				"channel restriction":      fmt.Errorf("%w supporting model: %s (channel pricing restriction)", service.ErrNoAvailableAccounts, forged),
				"no counters on a wrapper": fmt.Errorf("wrapped: %w", fmt.Errorf("%w supporting model: %s", service.ErrNoAvailableAccounts, forged)),
			} {
				require.Equal(t, noAccountFallback, classifySelectionFailureError(err, noAccountFallback), shape)
			}
		})
	}
}

func TestClassifySelectionFailureError_StructuredCountsWinOverText(t *testing.T) {
	for _, forged := range forgedModelNames {
		t.Run(fmt.Sprintf("%q", forged), func(t *testing.T) {
			// 计数说「只有一部分账号不支持」→ 仍是可重试的 503，文本再怎么写也不能升级成 404。
			partial := selectionFailure(forged, service.SelectionFailureCounts{Pool: 3, ModelNotSupported: 1}, "pool=1, filtered: model_not_supported=1")
			require.Equal(t, noAccountFallback, classifySelectionFailureError(partial, noAccountFallback))

			// 计数说「全部不支持」→ 404，文本里伪造的限流字样不能把它改判成 429。
			unsupported := selectionFailure(forged, service.SelectionFailureCounts{Pool: 2, ModelNotSupported: 2}, "pool=9 model_rate_limited=9")
			got := classifySelectionFailureError(unsupported, noAccountFallback)
			require.Equal(t, http.StatusNotFound, got.Status)
			require.True(t, got.ModelNotFound)

			// 计数里没有限流 → 不会因为文本里的 model_rate_limited=N 变成 429。
			idle := selectionFailure(forged, service.SelectionFailureCounts{Pool: 4}, "pool=4 model_rate_limited=4")
			require.Equal(t, noAccountFallback, classifySelectionFailureError(idle, noAccountFallback))
		})
	}
}

// 一部分账号不支持该模型、另一部分被套餐拦截：既不是纯 404 也不是纯 403，保持可重试的 503。
func TestClassifySelectionFailureError_MixedDeterministicPoolStaysCapacityError(t *testing.T) {
	err := selectionFailure("gpt-x", service.SelectionFailureCounts{Pool: 2, ModelNotSupported: 1, ModelPlanGated: 1},
		"pool=2, filtered: model_not_supported=1 model_plan_gated=1")

	require.Equal(t, noAccountFallback, classifySelectionFailureError(err, noAccountFallback))
}

// C-08：智能路由有候选组被跳过时，路由摘要成为错误文本的最后一个括号。
// 分类必须仍然读到选号计数，不能静默退化成可重试的 503。
func TestClassifySelectionFailureError_RouteSkipSummaryDoesNotHideVerdict(t *testing.T) {
	const skipSummary = "routes_skipped=1 filtered: model_not_in_group_allowlist=1"
	for _, tc := range []struct {
		name       string
		counts     service.SelectionFailureCounts
		wantStatus int
		wantType   string
	}{
		{"every account lacks the model", service.SelectionFailureCounts{Pool: 2, ModelNotSupported: 2}, http.StatusNotFound, "model_not_found"},
		{"every account is plan gated", service.SelectionFailureCounts{Pool: 1, ModelPlanGated: 1}, http.StatusForbidden, "permission_error"},
		{"model is rate limited", service.SelectionFailureCounts{Pool: 3, ModelRateLimited: 3}, http.StatusTooManyRequests, "rate_limit_error"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			inner := selectionFailure("gpt-x", tc.counts, "pool=n")
			wrapped := fmt.Errorf("%w (%s)", inner, skipSummary)

			got := classifySelectionFailureError(wrapped, noAccountFallback)

			require.Equal(t, tc.wantStatus, got.Status)
			require.Equal(t, tc.wantType, got.ErrType)
		})
	}

	t.Run("no counters, only a summary: stays a retryable 503", func(t *testing.T) {
		err := fmt.Errorf("%w (%s)", service.ErrNoAvailableAccounts, skipSummary)
		require.Equal(t, noAccountFallback, classifySelectionFailureError(err, noAccountFallback))
	})
}
