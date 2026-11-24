//go:build unit

package service

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/Wei-Shaw/sub2api/internal/config"
)

// forgedModelNames are caller-controlled model names that try to pass for the
// scheduler's own diagnostics: open/close the trailing parenthesis, carry
// pool=/model_not_supported= style counters, a newline, or full-width brackets.
var forgedModelNames = []string{
	"x (pool=1 model_not_supported=1",
	"x (pool=1 model_not_supported=1)",
	"x) (pool=2 model_plan_gated=2",
	"x model_rate_limited=9 (pool=9 model_rate_limited=9",
	"x\n(pool=1 model_not_supported=1)",
	"x（pool=1 model_not_supported=1）",
	"x=(pool=1",
}

// trailingGroup is what a text parser that looks at "the last parenthesised
// group" would read.
func trailingGroup(text string) string {
	return text[strings.LastIndex(text, " ("):]
}

func TestNoAvailableAccountsFromStats_CarriesStructuredCounts(t *testing.T) {
	t.Run("pool is Total, counters mirror the stats", func(t *testing.T) {
		err := noAvailableAccountsFromStats("gpt-x", "group=7 platform=openai",
			selectionFailureStats{Total: 4, Excluded: 1, ModelUnsupported: 2, ModelRateLimited: 1})

		require.ErrorIs(t, err, ErrNoAvailableAccounts)
		counts, ok := SelectionFailureCountsFromError(err)
		require.True(t, ok)
		require.Equal(t, SelectionFailureCounts{Pool: 4, ModelNotSupported: 2, ModelRateLimited: 1}, counts)
		// 文本只给日志/ops：模型名在括号外，括号内是调度器自己的摘要。
		require.True(t, strings.HasPrefix(err.Error(), "no available accounts supporting model: gpt-x (group=7 platform=openai total=4 "), err.Error())
		require.Contains(t, err.Error(), "pool=4 model_not_supported=2 model_rate_limited=1")
	})

	t.Run("pool falls back to the sum of buckets when Total is unset", func(t *testing.T) {
		err := noAvailableAccountsFromStats("", "", selectionFailureStats{Eligible: 1, Unschedulable: 2, ModelUnsupported: 3})

		counts, ok := SelectionFailureCountsFromError(err)
		require.True(t, ok)
		require.Equal(t, SelectionFailureCounts{Pool: 6, ModelNotSupported: 3}, counts)
		require.False(t, strings.Contains(err.Error(), "supporting model"), "no model, no model clause: %s", err.Error())
	})
}

func TestNoAvailableOpenAISelectionError_CarriesStructuredCounts(t *testing.T) {
	stats := openAISelectionFilterStats{pool: 5, reasons: map[string]int{
		"model_not_supported": 1, "model_plan_gated": 2, "model_rate_limited": 1, "quota_auto_pause_7d": 1,
	}}

	err := noAvailableOpenAISelectionError("gpt-5.4", false, stats, "selection_order_empty")

	require.ErrorIs(t, err, ErrNoAvailableAccounts)
	counts, ok := SelectionFailureCountsFromError(err)
	require.True(t, ok)
	require.Equal(t, SelectionFailureCounts{Pool: 5, ModelNotSupported: 1, ModelPlanGated: 2, ModelRateLimited: 1}, counts)
	require.Equal(t, "no available OpenAI accounts supporting model: gpt-5.4 (pool=5, filtered: model_not_supported=1 model_plan_gated=2 model_rate_limited=1 quota_auto_pause_7d=1, selection_order_empty)", err.Error())

	t.Run("compact blocked keeps the legacy sentinel with no counters", func(t *testing.T) {
		err := noAvailableOpenAISelectionError("gpt-5.4", true, stats, "")
		require.ErrorIs(t, err, ErrNoAvailableCompactAccounts)
		_, ok := SelectionFailureCountsFromError(err)
		require.False(t, ok)
	})
}

func TestSelectionFailureCountsFromError_SeesThroughWrappersAndIgnoresText(t *testing.T) {
	counts := SelectionFailureCounts{Pool: 2, ModelNotSupported: 2}
	inner := NewSelectionFailureError(counts, "no available accounts")

	for name, err := range map[string]error{
		"direct":          inner,
		"%w wrapper":      fmt.Errorf("%w (routes_skipped=1 filtered: model_not_in_group_allowlist=1)", inner),
		"double wrapper":  fmt.Errorf("outer: %w", fmt.Errorf("%w (routes_skipped=2)", inner)),
		"errors.Join":     errors.Join(errors.New("other"), inner),
		"wrapped sticky":  fmt.Errorf("%w (group=1 platform=x)", fmt.Errorf("%w", inner)),
		"text says other": fmt.Errorf("%w (pool=99 model_rate_limited=99)", inner),
	} {
		t.Run(name, func(t *testing.T) {
			got, ok := SelectionFailureCountsFromError(err)
			require.True(t, ok)
			require.Equal(t, counts, got, "counters come from the structured part, never from the text")
			require.ErrorIs(t, err, ErrNoAvailableAccounts)
		})
	}

	for name, err := range map[string]error{
		"nil":                   nil,
		"bare sentinel":         ErrNoAvailableAccounts,
		"sentinel plus text":    fmt.Errorf("%w (pool=1 model_not_supported=1)", ErrNoAvailableAccounts),
		"unrelated error":       errors.New("pool=1 model_not_supported=1"),
		"counter-less producer": noAvailableAccountsDetail("m", "group=1 platform=x schedulable_accounts=0"),
	} {
		t.Run("no counters/"+name, func(t *testing.T) {
			_, ok := SelectionFailureCountsFromError(err)
			require.False(t, ok, "text that merely looks like counters must not be picked up")
		})
	}
}

// C-07：用户可控的模型名不能进入「最后一个括号」，且这两个分支本来就没有逐账号计数，
// 所以也不会带结构化计数——分类只能走 503。
func TestNoAvailableAccountsDetail_ForgedModelNameStaysOutsideTrailingGroup(t *testing.T) {
	for _, name := range forgedModelNames {
		t.Run(fmt.Sprintf("%q", name), func(t *testing.T) {
			err := noAvailableAccountsDetail(name, "group=7 platform=anthropic schedulable_accounts=0")

			require.ErrorIs(t, err, ErrNoAvailableAccounts)
			_, ok := SelectionFailureCountsFromError(err)
			require.False(t, ok)
			require.Equal(t, " (group=7 platform=anthropic schedulable_accounts=0)", trailingGroup(err.Error()))
			require.Less(t, strings.Index(err.Error(), name), strings.LastIndex(err.Error(), " (group="),
				"the model name sits before the scheduler's own trailing group")
		})
	}
}

func newLoadAwareNoAccountService(accounts []Account, sessions SessionLimitCache, load map[int64]*AccountLoadInfo) *GatewayService {
	repo := &mockAccountRepoForPlatform{accounts: accounts, accountsByID: map[int64]*Account{}}
	for i := range repo.accounts {
		repo.accountsByID[repo.accounts[i].ID] = &repo.accounts[i]
	}
	cfg := testConfig()
	cfg.Gateway.Scheduling.LoadBatchEnabled = true
	return &GatewayService{
		accountRepo:        repo,
		cache:              &mockGatewayCacheForPlatform{},
		cfg:                cfg,
		sessionLimitCache:  sessions,
		concurrencyService: NewConcurrencyService(&mockConcurrencyCache{loadMap: load}),
	}
}

// 真实生产者：负载感知路径上两个曾把用户模型名放进末尾括号的分支。
func TestSelectAccountWithLoadAwareness_ForgedModelNameCannotCarryCounters(t *testing.T) {
	sessionFull := func() SessionLimitCache {
		return &sessionLimitCheckStub{active: map[int64]map[string]bool{1: {"someone-else": true}}}
	}
	limited := Account{ID: 1, Platform: PlatformAnthropic, Type: AccountTypeAPIKey, Priority: 1, Status: StatusActive,
		Schedulable: true, Concurrency: 5, Extra: map[string]any{"max_sessions": 1}}

	cases := []struct {
		name     string
		svc      func() *GatewayService
		session  string
		wantTail string
	}{
		{
			name:     "no schedulable account in scope",
			svc:      func() *GatewayService { return newLoadAwareNoAccountService(nil, nil, nil) },
			wantTail: "schedulable_accounts=0)",
		},
		{
			name: "wait plan exhausted",
			svc: func() *GatewayService {
				return newLoadAwareNoAccountService([]Account{limited}, sessionFull(),
					map[int64]*AccountLoadInfo{1: {AccountID: 1, LoadRate: 100}})
			},
			session:  "my-session",
			wantTail: "wait_plan_exhausted=true)",
		},
	}
	for _, tc := range cases {
		for _, name := range forgedModelNames {
			t.Run(fmt.Sprintf("%s/%q", tc.name, name), func(t *testing.T) {
				_, err := tc.svc().SelectAccountWithLoadAwareness(context.Background(), nil, tc.session, name, nil, "", 0)

				require.ErrorIs(t, err, ErrNoAvailableAccounts)
				_, ok := SelectionFailureCountsFromError(err)
				require.False(t, ok, "capacity exhaustion has no model verdict: %s", err.Error())
				tail := trailingGroup(err.Error())
				require.True(t, strings.HasPrefix(tail, " (group="), tail)
				require.True(t, strings.HasSuffix(tail, tc.wantTail), tail)
				require.NotContains(t, tail, "model_not_supported", "the trailing group is scheduler-owned only")
			})
		}
	}
}

// C-08：智能路由里有候选组被跳过时，路由层的 routes_skipped=… 摘要被追加到错误文本末尾，
// 选号层的结构化计数必须原样保留，分类才不会静默退化成可重试的 503。
func TestSelectAccountAlongKeyRoutes_SkipSummaryKeepsStructuredCounts(t *testing.T) {
	const model = "claude-x"
	resetAt := time.Now().Add(2 * time.Minute).Format(time.RFC3339)

	cases := []struct {
		name       string
		extra      map[string]any
		mapping    map[string]any
		wantCounts SelectionFailureCounts
	}{
		{
			name:       "no account in the attempted group supports the model",
			mapping:    map[string]any{"claude-other": "claude-other"},
			wantCounts: SelectionFailureCounts{Pool: 1, ModelNotSupported: 1},
		},
		{
			name: "every account in the attempted group is cooling down for the model",
			extra: map[string]any{"model_rate_limits": map[string]any{
				model: map[string]any{"rate_limit_reset_at": resetAt},
			}},
			wantCounts: SelectionFailureCounts{Pool: 1, ModelRateLimited: 1},
		},
	}
	for _, tc := range cases {
		for _, loadBatch := range []bool{true, false} {
			t.Run(fmt.Sprintf("%s/load_batch=%t", tc.name, loadBatch), func(t *testing.T) {
				skipped := gatewayProfitTestGroup(7, PlatformAnthropic)
				skipped.ModelAllowlist = GroupModelsListConfig{Enabled: true, Models: []string{"only-other-model"}}
				attempted := gatewayProfitTestGroup(8, PlatformAnthropic)
				attempted.ModelAllowlist = GroupModelsListConfig{Enabled: true, Models: []string{model}}
				key := smartRouteCoreKey(skipped, attempted)
				account := gatewayProfitTestAccount(1008, PlatformAnthropic, 1, attempted.ID)
				account.Extra = tc.extra
				if tc.mapping != nil {
					account.Credentials = map[string]any{"model_mapping": tc.mapping}
				}
				snapshot, _ := newSmartRouteCoreSnapshot([]*Group{skipped, attempted}, &account)
				cfg := &config.Config{}
				cfg.Gateway.Scheduling.LoadBatchEnabled = loadBatch
				svc := &GatewayService{
					accountRepo: &modelsListAccountRepoStub{}, schedulerSnapshot: snapshot, cfg: cfg,
					concurrencyService: NewConcurrencyService(schedulerTestConcurrencyCache{}),
				}

				_, _, err := svc.SelectAccountAlongKeyRoutes(context.Background(), key, "", model, nil, "", key.UserID, PlatformAnthropic)

				require.ErrorIs(t, err, ErrNoAvailableAccounts)
				// 路由层摘要仍在文本里（运维排障用）……
				require.Contains(t, err.Error(), "routes_skipped=1 filtered: model_not_in_group_allowlist=1")
				require.True(t, strings.HasSuffix(err.Error(), ")"))
				require.True(t, strings.HasPrefix(trailingGroup(err.Error()), " (routes_skipped="),
					"the route summary is the LAST parenthesised group, which a text parser would have read instead of the counters")
				// ……但选号计数不依赖文本，仍然可取。
				counts, ok := SelectionFailureCountsFromError(err)
				require.True(t, ok, err.Error())
				require.Equal(t, tc.wantCounts, counts)
			})
		}
	}
}

func TestSelectAccountAlongKeyRoutes_AllRoutesSkippedHasNoCounters(t *testing.T) {
	skipped := gatewayProfitTestGroup(7, PlatformAnthropic)
	skipped.ModelAllowlist = GroupModelsListConfig{Enabled: true, Models: []string{"only-other-model"}}
	key := smartRouteCoreKey(skipped)
	snapshot, _ := newSmartRouteCoreSnapshot([]*Group{skipped})
	svc := &GatewayService{accountRepo: &modelsListAccountRepoStub{}, schedulerSnapshot: snapshot, cfg: &config.Config{}}

	_, _, err := svc.SelectAccountAlongKeyRoutes(context.Background(), key, "", "claude-x", nil, "", key.UserID, PlatformAnthropic)

	require.ErrorIs(t, err, ErrNoAvailableAccounts)
	require.Contains(t, err.Error(), "routes_skipped=1")
	_, ok := SelectionFailureCountsFromError(err)
	require.False(t, ok, "no group was tried, so there is no model verdict")
}
