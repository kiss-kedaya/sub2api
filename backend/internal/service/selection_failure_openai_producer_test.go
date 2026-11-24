//go:build unit

package service

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/Wei-Shaw/sub2api/internal/config"
)

// 真实的 OpenAI 调度生产者必须把「这个池子是怎么空的」作为结构化计数随错误带出：
// handler 的 404 / 403 / 429 分类只读这些计数，不读错误文本。

func openAIProducerFailure(t *testing.T, model string, excluded map[int64]struct{}, accounts ...Account) error {
	t.Helper()
	resetOpenAIAdvancedSchedulerSettingCacheForTest()
	groupID := int64(101300)
	svc := &OpenAIGatewayService{
		accountRepo:        schedulerTestOpenAIAccountRepo{accounts: accounts},
		cache:              &schedulerTestGatewayCache{},
		cfg:                &config.Config{},
		rateLimitService:   newOpenAIAdvancedSchedulerRateLimitService("true"),
		concurrencyService: NewConcurrencyService(schedulerTestConcurrencyCache{}),
	}
	selection, _, err := svc.SelectAccountWithScheduler(context.Background(), &groupID, "", "", model, excluded, OpenAIUpstreamTransportAny, false)
	require.Error(t, err)
	require.ErrorIs(t, err, ErrNoAvailableAccounts)
	require.Nil(t, selection)
	return err
}

func openAIProducerAccount(id int64, extra map[string]any, mapping map[string]any) Account {
	account := Account{
		ID: id, Platform: PlatformOpenAI, Type: AccountTypeAPIKey, Status: StatusActive,
		Schedulable: true, Concurrency: 1, Extra: extra,
	}
	if mapping != nil {
		account.Credentials = map[string]any{"model_mapping": mapping}
	}
	return account
}

func modelCooldownExtra(model, reason string) map[string]any {
	limit := map[string]any{"rate_limit_reset_at": time.Now().Add(5 * time.Minute).UTC().Format(time.RFC3339)}
	if reason != "" {
		limit["reason"] = reason
	}
	return map[string]any{"model_rate_limits": map[string]any{model: limit}}
}

func TestOpenAISchedulerNoAvailableError_CarriesStructuredCounts(t *testing.T) {
	const model = "gpt-5.4-mini"
	for _, tc := range []struct {
		name     string
		accounts []Account
		excluded map[int64]struct{}
		want     SelectionFailureCounts
	}{
		{
			name:     "no account maps the model",
			accounts: []Account{openAIProducerAccount(1, nil, map[string]any{"gpt-4o": "gpt-4o"}), openAIProducerAccount(2, nil, map[string]any{"gpt-4o-mini": "gpt-4o-mini"})},
			want:     SelectionFailureCounts{Pool: 2, ModelNotSupported: 2},
		},
		{
			name: "every account is plan gated for the model",
			accounts: []Account{
				openAIProducerAccount(1, modelCooldownExtra(model, upstreamCodexPlanGatedModelReason), nil),
				openAIProducerAccount(2, modelCooldownExtra(model, upstreamCodexPlanGatedModelReason), nil),
			},
			want: SelectionFailureCounts{Pool: 2, ModelPlanGated: 2},
		},
		{
			name:     "every account is cooling down for the model",
			accounts: []Account{openAIProducerAccount(1, modelCooldownExtra(model, ""), nil)},
			want:     SelectionFailureCounts{Pool: 1, ModelRateLimited: 1},
		},
		{
			name: "a mix keeps each reason separate",
			accounts: []Account{
				openAIProducerAccount(1, nil, map[string]any{"gpt-4o": "gpt-4o"}),
				openAIProducerAccount(2, modelCooldownExtra(model, upstreamCodexPlanGatedModelReason), nil),
				openAIProducerAccount(3, modelCooldownExtra(model, ""), nil),
				openAIProducerAccount(4, nil, nil),
			},
			excluded: map[int64]struct{}{4: {}},
			want:     SelectionFailureCounts{Pool: 4, ModelNotSupported: 1, ModelPlanGated: 1, ModelRateLimited: 1},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := openAIProducerFailure(t, model, tc.excluded, tc.accounts...)

			got, ok := SelectionFailureCountsFromError(err)
			require.True(t, ok, err.Error())
			require.Equal(t, tc.want, got, err.Error())
		})
	}
}

// 网关（Anthropic / Gemini 分组）的负载感知选号同理：计数由调度统计折算，不依赖文本。
func TestGatewaySchedulerNoAvailableError_CarriesStructuredCounts(t *testing.T) {
	const model = "claude-x"
	mapped := func(id int64, key string) Account {
		return Account{ID: id, Platform: PlatformAnthropic, Type: AccountTypeAPIKey, Priority: 1, Status: StatusActive,
			Schedulable: true, Concurrency: 5, Credentials: map[string]any{"model_mapping": map[string]any{key: key}}}
	}
	cooling := Account{ID: 3, Platform: PlatformAnthropic, Type: AccountTypeAPIKey, Priority: 1, Status: StatusActive,
		Schedulable: true, Concurrency: 5, Extra: modelCooldownExtra(model, "")}

	for _, tc := range []struct {
		name       string
		accounts   []Account
		want       SelectionFailureCounts
		legacyOnly bool // 负载感知路径在统计前已剔除「不支持该模型」的账号，混合池的口径只在旧路径下有意义
	}{
		{"no account maps the model", []Account{mapped(1, "claude-a"), mapped(2, "claude-b")}, SelectionFailureCounts{Pool: 2, ModelNotSupported: 2}, false},
		{"every account is cooling down", []Account{cooling}, SelectionFailureCounts{Pool: 1, ModelRateLimited: 1}, false},
		{"a mix", []Account{mapped(1, "claude-a"), cooling}, SelectionFailureCounts{Pool: 2, ModelNotSupported: 1, ModelRateLimited: 1}, true},
	} {
		for _, loadBatch := range []bool{true, false} {
			if tc.legacyOnly && loadBatch {
				continue
			}
			name := tc.name + map[bool]string{true: "/load-aware", false: "/legacy"}[loadBatch]
			t.Run(name, func(t *testing.T) {
				svc := newLoadAwareNoAccountService(tc.accounts, nil, nil)
				svc.cfg.Gateway.Scheduling.LoadBatchEnabled = loadBatch

				_, err := svc.SelectAccountWithLoadAwareness(context.Background(), nil, "", model, nil, "", 0)

				require.ErrorIs(t, err, ErrNoAvailableAccounts)
				got, ok := SelectionFailureCountsFromError(err)
				require.True(t, ok, err.Error())
				require.Equal(t, tc.want, got, err.Error())
			})
		}
	}
}
