package service

import (
	"context"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/stretchr/testify/require"
)

// newOpenAIImageSizeTestAccount 构造 OpenAI 生图账号，tiers 为空表示不限档位。
func newOpenAIImageSizeTestAccount(t *testing.T, id int64, priority int, groupID int64, tiers ...any) Account {
	t.Helper()
	account := Account{
		ID:          id,
		Name:        "image-size-account",
		Platform:    PlatformOpenAI,
		Type:        AccountTypeAPIKey,
		Status:      StatusActive,
		Schedulable: true,
		Concurrency: 1,
		Priority:    priority,
		GroupIDs:    []int64{groupID},
		Credentials: map[string]any{"api_key": "sk-test"},
	}
	if len(tiers) > 0 {
		account.Extra = map[string]any{AccountExtraOpenAIImageSizeTiers: append([]any(nil), tiers...)}
	}
	return account
}

func newOpenAIImageSizeSchedulerTestService(accounts []Account) *OpenAIGatewayService {
	resetOpenAIAdvancedSchedulerSettingCacheForTest()
	return &OpenAIGatewayService{
		accountRepo:        schedulerTestOpenAIAccountRepo{accounts: accounts},
		cache:              &schedulerTestGatewayCache{},
		cfg:                &config.Config{},
		rateLimitService:   newOpenAIAdvancedSchedulerRateLimitService("true"),
		concurrencyService: NewConcurrencyService(schedulerTestConcurrencyCache{}),
	}
}

func TestAccountOpenAIImageSizeTiers(t *testing.T) {
	for _, tt := range []struct {
		name  string
		extra map[string]any
		want  []string
	}{
		{name: "no extra means unlimited", extra: nil, want: nil},
		{name: "empty list means unlimited", extra: map[string]any{AccountExtraOpenAIImageSizeTiers: []any{}}, want: nil},
		{name: "all three means unlimited", extra: map[string]any{AccountExtraOpenAIImageSizeTiers: []any{"1K", "2K", "4K"}}, want: nil},
		{name: "partial list is canonical ordered and case folded", extra: map[string]any{AccountExtraOpenAIImageSizeTiers: []any{"2k", " 1K"}}, want: []string{ImageBillingSize1K, ImageBillingSize2K}},
		{name: "unknown tiers are ignored", extra: map[string]any{AccountExtraOpenAIImageSizeTiers: []any{"8K"}}, want: nil},
		{name: "string slice from snapshot projection", extra: map[string]any{AccountExtraOpenAIImageSizeTiers: []string{"4K"}}, want: []string{ImageBillingSize4K}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			account := Account{Extra: tt.extra}
			require.Equal(t, tt.want, account.OpenAIImageSizeTiers())
			for _, tier := range []string{ImageBillingSize1K, ImageBillingSize2K, ImageBillingSize4K} {
				require.Equal(t, tt.want == nil || containsString(tt.want, tier), account.SupportsOpenAIImageSizeTier(tier))
			}
		})
	}
}

func containsString(values []string, target string) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}

func TestAccountOpenAIImageTestSize(t *testing.T) {
	unlimited := Account{}
	require.Empty(t, unlimited.OpenAIImageTestSize())

	oneKOnly := Account{Extra: map[string]any{AccountExtraOpenAIImageSizeTiers: []any{"1K"}}}
	require.Equal(t, "1024x1024", oneKOnly.OpenAIImageTestSize())

	twoKAndOneK := Account{Extra: map[string]any{AccountExtraOpenAIImageSizeTiers: []any{"2K", "1K"}}}
	require.Equal(t, "1024x1024", twoKAndOneK.OpenAIImageTestSize(), "test size uses the lowest allowed tier")

	fourKOnly := Account{Extra: map[string]any{AccountExtraOpenAIImageSizeTiers: []any{"4K"}}}
	require.Equal(t, "3840x2160", fourKOnly.OpenAIImageTestSize())
}

func TestOpenAIImageSizeVetoReason(t *testing.T) {
	tierPass := func(tier string) context.Context {
		return context.WithValue(context.Background(), openAIImageSizePassCtxKey{}, &openAIImageSizePass{tier: tier})
	}
	unlimited := Account{ID: 1}
	lowOnly := Account{ID: 2, Extra: map[string]any{AccountExtraOpenAIImageSizeTiers: []any{"1K", "2K"}}}
	highOnly := Account{ID: 3, Extra: map[string]any{AccountExtraOpenAIImageSizeTiers: []any{"4K"}}}

	ctx := tierPass(ImageBillingSize4K)
	vetoed, reason := openAIImageSizeVetoReason(ctx, &unlimited)
	require.False(t, vetoed)
	require.Empty(t, reason)
	vetoed, reason = openAIImageSizeVetoReason(ctx, &highOnly)
	require.False(t, vetoed, "4K-only account serves a 4K request")
	vetoed, reason = openAIImageSizeVetoReason(ctx, &lowOnly)
	require.True(t, vetoed)
	require.Equal(t, openAIImageSizeTierUnsupportedReason, reason)

	// 请求尺寸无法识别时按偏好层级过滤：rank 0 只放不限档位的账号。
	preferencePass := &openAIImageSizePass{rank: 0, queuedIDs: map[int64]struct{}{}}
	preferencePass.nextRank.Store(openAIImageSizePreferenceRanks)
	prefCtx := context.WithValue(context.Background(), openAIImageSizePassCtxKey{}, preferencePass)
	vetoed, _ = openAIImageSizeVetoReason(prefCtx, &unlimited)
	require.False(t, vetoed)
	vetoed, _ = openAIImageSizeVetoReason(prefCtx, &highOnly)
	require.True(t, vetoed)
	require.Equal(t, int32(1), preferencePass.nextRank.Load(), "the 4K rank is remembered for the next pass")

	// 已粘在账号上的请求不受偏好层级约束。
	stickyCtx := withOpenAIImageSizeStickyAccount(prefCtx, highOnly.ID)
	vetoed, _ = openAIImageSizeVetoReason(stickyCtx, &highOnly)
	require.False(t, vetoed)
}

func TestClassifyImageBillingTierMatchesSizeRouting(t *testing.T) {
	// 档位口径必须与计费一致：最长边 <=1024=1K，<=2048=2K，更大=4K。
	require.Equal(t, ImageBillingSize1K, mustTier(t, "1024x1024"))
	require.Equal(t, ImageBillingSize2K, mustTier(t, "1536x1024"))
	require.Equal(t, ImageBillingSize4K, mustTier(t, "2560x1440"))
	require.Equal(t, ImageBillingSize4K, mustTier(t, "3840x2160"))
	_, ok := ClassifyImageBillingTier("auto")
	require.False(t, ok)
	_, ok = ClassifyImageBillingTier("")
	require.False(t, ok)
}

func mustTier(t *testing.T, size string) string {
	t.Helper()
	tier, ok := ClassifyImageBillingTier(size)
	require.True(t, ok)
	return tier
}

// 未指定尺寸的请求按 不限档位 → 4K → 2K → 1K 逐层选号，跳过没有账号的层级。
func TestSelectOpenAIAccountByImageSizePasses_PreferenceOrder(t *testing.T) {
	unlimited := Account{ID: 1}
	fourKOnly := Account{ID: 2, Extra: map[string]any{AccountExtraOpenAIImageSizeTiers: []any{"4K"}}}
	twoKOnly := Account{ID: 3, Extra: map[string]any{AccountExtraOpenAIImageSizeTiers: []any{"2K"}}}
	oneKOnly := Account{ID: 4, Extra: map[string]any{AccountExtraOpenAIImageSizeTiers: []any{"1K"}}}
	all := []*Account{&unlimited, &fourKOnly, &twoKOnly, &oneKOnly}

	var admittedPerPass [][]int64
	_, _, err := selectOpenAIAccountByImageSizePasses(WithOpenAIImageSizeRouting(context.Background(), ""), func(ctx context.Context) (*AccountSelectionResult, OpenAIAccountScheduleDecision, error) {
		admitted := make([]int64, 0, len(all))
		for _, account := range all {
			if vetoed, _ := openAIImageSizeVetoReason(ctx, account); !vetoed {
				admitted = append(admitted, account.ID)
			}
		}
		admittedPerPass = append(admittedPerPass, admitted)
		return nil, OpenAIAccountScheduleDecision{}, ErrNoAvailableAccounts
	})
	require.ErrorIs(t, err, ErrNoAvailableAccounts)
	require.Equal(t, [][]int64{{1}, {2}, {3}, {4}}, admittedPerPass)
}

func TestSelectOpenAIAccountByImageSizePasses_ExplicitTierFilters(t *testing.T) {
	lowOnly := Account{ID: 1, Extra: map[string]any{AccountExtraOpenAIImageSizeTiers: []any{"1K", "2K"}}}
	unlimited := Account{ID: 2}

	admitted := make([]int64, 0, 2)
	_, _, err := selectOpenAIAccountByImageSizePasses(WithOpenAIImageSizeRouting(context.Background(), "3840x2160"), func(ctx context.Context) (*AccountSelectionResult, OpenAIAccountScheduleDecision, error) {
		for _, account := range []*Account{&lowOnly, &unlimited} {
			if vetoed, _ := openAIImageSizeVetoReason(ctx, account); !vetoed {
				admitted = append(admitted, account.ID)
			}
		}
		return nil, OpenAIAccountScheduleDecision{}, ErrNoAvailableAccounts
	})
	require.ErrorIs(t, err, ErrNoAvailableAccounts)
	require.Equal(t, []int64{2}, admitted, "only the unrestricted account may serve a 4K request")
}

// 较早层级只能排队时继续尝试后续层级，都抢不到槽位时返回最早的排队结果。
func TestSelectOpenAIAccountByImageSizePasses_ReturnsEarliestWaitPlan(t *testing.T) {
	oneKOnly := Account{ID: 1, Extra: map[string]any{AccountExtraOpenAIImageSizeTiers: []any{"1K"}}}
	calls := 0
	selection, _, err := selectOpenAIAccountByImageSizePasses(WithOpenAIImageSizeRouting(context.Background(), "auto"), func(ctx context.Context) (*AccountSelectionResult, OpenAIAccountScheduleDecision, error) {
		calls++
		_, _ = openAIImageSizeVetoReason(ctx, &oneKOnly)
		if calls == 1 {
			return &AccountSelectionResult{Account: &Account{ID: 10}, WaitPlan: &AccountWaitPlan{AccountID: 10}}, OpenAIAccountScheduleDecision{}, nil
		}
		if calls == 2 {
			return &AccountSelectionResult{Account: &Account{ID: 11}, WaitPlan: &AccountWaitPlan{AccountID: 11}}, OpenAIAccountScheduleDecision{}, nil
		}
		return nil, OpenAIAccountScheduleDecision{}, ErrNoAvailableAccounts
	})
	require.NoError(t, err)
	require.NotNil(t, selection)
	require.Equal(t, int64(10), selection.Account.ID)
	require.Equal(t, 2, calls, "the unrestricted pass jumps straight to the 1K-only rank")
}

// 指定档位时只调度允许该档位的账号。
func TestOpenAIImageSizeScheduler_RoutesExplicitTier(t *testing.T) {
	groupID := int64(94001)
	lowOnly := newOpenAIImageSizeTestAccount(t, 90001, 0, groupID, "1K", "2K")
	highOnly := newOpenAIImageSizeTestAccount(t, 90002, 0, groupID, "4K")
	svc := newOpenAIImageSizeSchedulerTestService([]Account{lowOnly, highOnly})

	for _, tt := range []struct {
		size string
		want int64
	}{
		{size: "1024x1024", want: lowOnly.ID},
		{size: "1536x1024", want: lowOnly.ID},
		{size: "2560x1440", want: highOnly.ID},
		{size: "3840x2160", want: highOnly.ID},
	} {
		t.Run(tt.size, func(t *testing.T) {
			ctx := WithOpenAIImageSizeRouting(context.Background(), tt.size)
			selection, _, err := svc.SelectAccountWithSchedulerForImages(ctx, &groupID, "", "gpt-image-2", nil, OpenAIImagesCapabilityBasic)
			require.NoError(t, err)
			require.NotNil(t, selection)
			require.NotNil(t, selection.Account)
			require.Equal(t, tt.want, selection.Account.ID)
		})
	}
}

// 未指定尺寸时优先不限档位的账号，没有时再逐层降级。
func TestOpenAIImageSizeScheduler_UnspecifiedSizePreference(t *testing.T) {
	groupID := int64(94002)
	unlimited := newOpenAIImageSizeTestAccount(t, 90101, 9, groupID)
	fourKOnly := newOpenAIImageSizeTestAccount(t, 90102, 0, groupID, "4K")
	oneKOnly := newOpenAIImageSizeTestAccount(t, 90103, 1, groupID, "1K")
	svc := newOpenAIImageSizeSchedulerTestService([]Account{unlimited, fourKOnly, oneKOnly})

	for _, size := range []string{"", "auto"} {
		t.Run("size="+size, func(t *testing.T) {
			ctx := WithOpenAIImageSizeRouting(context.Background(), size)
			selection, _, err := svc.SelectAccountWithSchedulerForImages(ctx, &groupID, "", "gpt-image-2", nil, OpenAIImagesCapabilityBasic)
			require.NoError(t, err)
			require.NotNil(t, selection)
			require.Equal(t, unlimited.ID, selection.Account.ID, "the unrestricted account wins the first preference level")
		})
	}

	// 没有不限档位的账号时，4K 档位优先于仅 1K。
	svc = newOpenAIImageSizeSchedulerTestService([]Account{fourKOnly, oneKOnly})
	ctx := WithOpenAIImageSizeRouting(context.Background(), "")
	selection, _, err := svc.SelectAccountWithSchedulerForImages(ctx, &groupID, "", "gpt-image-2", nil, OpenAIImagesCapabilityBasic)
	require.NoError(t, err)
	require.NotNil(t, selection)
	require.Equal(t, fourKOnly.ID, selection.Account.ID)
}

// 会话粘性在指定档位时不能越过档位限制。
func TestOpenAIImageSizeScheduler_StickyDoesNotCrossTier(t *testing.T) {
	groupID := int64(94003)
	lowOnly := newOpenAIImageSizeTestAccount(t, 90201, 0, groupID, "1K", "2K")
	highOnly := newOpenAIImageSizeTestAccount(t, 90202, 1, groupID, "4K")
	sessionHash := "image-size-sticky-tier"
	cache := &schedulerTestGatewayCache{
		sessionBindings: map[string]int64{"openai:" + sessionHash: highOnly.ID},
	}
	svc := newOpenAIImageSizeSchedulerTestService([]Account{lowOnly, highOnly})
	svc.cache = cache

	ctx := WithOpenAIImageSizeRouting(context.Background(), "1024x1024")
	selection, _, err := svc.SelectAccountWithSchedulerForImages(ctx, &groupID, sessionHash, "gpt-image-2", nil, OpenAIImagesCapabilityBasic)
	require.NoError(t, err)
	require.NotNil(t, selection)
	require.Equal(t, lowOnly.ID, selection.Account.ID, "the 4K-only sticky account must not serve a 1K request")
	require.Equal(t, lowOnly.ID, cache.sessionBindings["openai:"+sessionHash], "the session rebinds to the account that actually served it")
}

// 未指定尺寸时会话粘性与 previous_response_id 绑定的账号不受偏好层级约束。
func TestOpenAIImageSizeScheduler_PreferenceKeepsSticky(t *testing.T) {
	groupID := int64(94004)
	unlimited := newOpenAIImageSizeTestAccount(t, 90301, 0, groupID)
	highOnly := newOpenAIImageSizeTestAccount(t, 90302, 1, groupID, "4K")
	sessionHash := "image-size-sticky-preference"
	cache := &schedulerTestGatewayCache{
		sessionBindings: map[string]int64{"openai:" + sessionHash: highOnly.ID},
	}
	svc := newOpenAIImageSizeSchedulerTestService([]Account{unlimited, highOnly})
	svc.cache = cache

	ctx := WithOpenAIImageSizeRouting(context.Background(), "")
	selection, decision, err := svc.SelectAccountWithSchedulerForImages(ctx, &groupID, sessionHash, "gpt-image-2", nil, OpenAIImagesCapabilityBasic)
	require.NoError(t, err)
	require.NotNil(t, selection)
	require.Equal(t, highOnly.ID, selection.Account.ID, "a sticky 4K-only account is not filtered by the preference level")
	require.True(t, decision.StickySessionHit)
}

// 溢出轮次抢到槽位时，会话仍绑定在首轮选号前的账号上则保留该绑定。
func TestOpenAIImageSizeScheduler_SpilloverKeepsExistingBinding(t *testing.T) {
	groupID := int64(94005)
	sessionHash := "image-size-spillover-keep"
	unlimited := newOpenAIImageSizeTestAccount(t, 90401, 0, groupID)
	highOnly := newOpenAIImageSizeTestAccount(t, 90402, 1, groupID, "4K")
	cache := &schedulerTestGatewayCache{
		sessionBindings: map[string]int64{"openai:" + sessionHash: unlimited.ID},
	}
	svc := newOpenAIImageSizeSchedulerTestService([]Account{unlimited, highOnly})
	svc.cache = cache
	svc.concurrencyService = NewConcurrencyService(schedulerTestConcurrencyCache{
		acquireResults: map[int64]bool{unlimited.ID: false, highOnly.ID: true},
	})

	ctx := WithOpenAIImageSizeRouting(context.Background(), "")
	selection, _, err := svc.SelectAccountWithSchedulerForImages(ctx, &groupID, sessionHash, "gpt-image-2", nil, OpenAIImagesCapabilityBasic)
	require.NoError(t, err)
	require.NotNil(t, selection)
	require.Equal(t, highOnly.ID, selection.Account.ID, "the busy unrestricted account is queued, the free 4K account serves the request")
	require.Equal(t, unlimited.ID, cache.sessionBindings["openai:"+sessionHash], "the pre-existing binding is preserved")
}

// 首轮选号前没有绑定，溢出轮次抢到槽位时绑定实际服务的账号。
func TestOpenAIImageSizeScheduler_SpilloverBindsServedAccount(t *testing.T) {
	groupID := int64(94006)
	sessionHash := "image-size-spillover-bind"
	unlimited := newOpenAIImageSizeTestAccount(t, 90501, 0, groupID)
	highOnly := newOpenAIImageSizeTestAccount(t, 90502, 1, groupID, "4K")
	cache := &schedulerTestGatewayCache{}
	svc := newOpenAIImageSizeSchedulerTestService([]Account{unlimited, highOnly})
	svc.cache = cache
	svc.concurrencyService = NewConcurrencyService(schedulerTestConcurrencyCache{
		acquireResults: map[int64]bool{unlimited.ID: false, highOnly.ID: true},
	})

	ctx := WithOpenAIImageSizeRouting(context.Background(), "")
	selection, _, err := svc.SelectAccountWithSchedulerForImages(ctx, &groupID, sessionHash, "gpt-image-2", nil, OpenAIImagesCapabilityBasic)
	require.NoError(t, err)
	require.NotNil(t, selection)
	require.Equal(t, highOnly.ID, selection.Account.ID)
	require.Equal(t, highOnly.ID, cache.sessionBindings["openai:"+sessionHash], "without a prior binding the served account becomes sticky")
}

// 文本请求不读取生图尺寸，尺寸档位不参与文本选号。
func TestOpenAIImageSizeScheduler_TextRequestIgnoresImageSizeTiers(t *testing.T) {
	groupID := int64(94007)
	highOnly := newOpenAIImageSizeTestAccount(t, 90601, 0, groupID, "4K")
	svc := newOpenAIImageSizeSchedulerTestService([]Account{highOnly})

	// 文本调度不带尺寸上下文，只能选到唯一的 4K 账号。
	selection, _, err := svc.SelectAccountWithScheduler(context.Background(), &groupID, "", "", "gpt-5.4", nil, OpenAIUpstreamTransportAny, false)
	require.NoError(t, err)
	require.NotNil(t, selection)
	require.Equal(t, highOnly.ID, selection.Account.ID)
}

// OpenAIImageSizeTierUnsupported 只在分组内持久可调度且具备模型/能力的账号都不允许该档位时命中。
func TestOpenAIImageSizeTierUnsupported(t *testing.T) {
	groupID := int64(94008)
	lowOnly := newOpenAIImageSizeTestAccount(t, 90701, 0, groupID, "1K", "2K")
	highOnly := newOpenAIImageSizeTestAccount(t, 90702, 0, groupID, "4K")

	svc := &OpenAIGatewayService{accountRepo: openAIImageSizeTestAccountRepo{accounts: []Account{lowOnly}}}
	tier, unsupported := svc.OpenAIImageSizeTierUnsupported(context.Background(), &groupID, "gpt-image-2", "3840x2160", "", OpenAIImagesCapabilityNative)
	require.True(t, unsupported)
	require.Equal(t, ImageBillingSize4K, tier)

	_, unsupported = svc.OpenAIImageSizeTierUnsupported(context.Background(), &groupID, "gpt-image-2", "1024x1024", "", OpenAIImagesCapabilityNative)
	require.False(t, unsupported)
	_, unsupported = svc.OpenAIImageSizeTierUnsupported(context.Background(), &groupID, "gpt-image-2", "auto", "", OpenAIImagesCapabilityNative)
	require.False(t, unsupported)

	svc.accountRepo = openAIImageSizeTestAccountRepo{accounts: []Account{lowOnly, highOnly}}
	_, unsupported = svc.OpenAIImageSizeTierUnsupported(context.Background(), &groupID, "gpt-image-2", "3840x2160", "", OpenAIImagesCapabilityNative)
	require.False(t, unsupported)

	// 没有账号支持该模型时留给 model_not_found 分类。
	textOnly := newOpenAIImageSizeTestAccount(t, 90703, 0, groupID)
	textOnly.Credentials = map[string]any{"api_key": "sk-test", "model_mapping": map[string]any{"gpt-5.4": "gpt-5.4"}}
	svc.accountRepo = openAIImageSizeTestAccountRepo{accounts: []Account{textOnly}}
	_, unsupported = svc.OpenAIImageSizeTierUnsupported(context.Background(), &groupID, "gpt-image-2", "3840x2160", "", OpenAIImagesCapabilityNative)
	require.False(t, unsupported)
}

type openAIImageSizeTestAccountRepo struct {
	AccountRepository
	accounts []Account
}

func (r openAIImageSizeTestAccountRepo) ListModelAvailabilityCandidates(_ context.Context, _ *int64, platforms []string, _ bool) ([]Account, error) {
	out := make([]Account, 0, len(r.accounts))
	for _, account := range r.accounts {
		for _, platform := range platforms {
			if account.Platform == platform {
				out = append(out, account)
				break
			}
		}
	}
	return out, nil
}
