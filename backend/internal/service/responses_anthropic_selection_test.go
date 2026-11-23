//go:build unit

package service

import (
	"context"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/pkg/ctxkey"
	"github.com/stretchr/testify/require"
)

// 线上 group 118 的形态：唯一账号是 anthropic api_key，开启 anthropic_passthrough，
// credentials 里残留一份不完整的 model_mapping（来自上游模型同步），且没有任何
// openai responses 能力位。请求模型 (claude-opus-5-5) 不在残留映射里。
func newAnthropicPassthroughAccountFixture() Account {
	return Account{
		ID:          34786,
		Name:        "claude-passthrough-118",
		Platform:    PlatformAnthropic,
		Type:        AccountTypeAPIKey,
		Priority:    1,
		Status:      StatusActive,
		Schedulable: true,
		Concurrency: 10000,
		GroupIDs:    []int64{118},
		AccountGroups: []AccountGroup{
			{AccountID: 34786, GroupID: 118},
		},
		Extra: map[string]any{"anthropic_passthrough": true},
		Credentials: map[string]any{
			"api_key":  "upstream-key",
			"base_url": "https://api.anthropic.com",
			"model_mapping": map[string]any{
				// 上游模型同步只回填了 10 个键，缺 claude-opus-5-5 / claude-fable-5-2。
				"claude-sonnet-4-5": "claude-sonnet-4-5-20250929",
			},
		},
	}
}

func anthropicPassthroughSelectionService(account Account) *GatewayService {
	groupID := int64(118)
	accountRepo := &mockAccountRepoForPlatform{
		accounts:     []Account{account},
		accountsByID: map[int64]*Account{account.ID: &account},
	}
	groupRepo := &mockGroupRepoForGateway{groups: map[int64]*Group{
		groupID: {ID: groupID, Name: "Claude 嘿嘿嘿", Platform: PlatformAnthropic, Status: StatusActive, Hydrated: true,
			CrossProtocolConversionEnabled: true},
	}}
	return &GatewayService{accountRepo: accountRepo, groupRepo: groupRepo, cfg: testConfig()}
}

func anthropicPassthroughSelectionContext(groupID int64) context.Context {
	return context.WithValue(context.Background(), ctxkey.Group, &Group{
		ID: groupID, Platform: PlatformAnthropic, Status: StatusActive, Hydrated: true,
	})
}

// passthrough 账号「只替换认证」：残留 model_mapping 不得把它排除出候选集。
// 这条用例正是线上 503 的选号层反例：账号支持模型、分组 anthropic、入站
// /v1/responses，选号必须成功（随后由 ForwardAsResponses 转换）。
func TestSelectAccountForModel_AnthropicPassthroughNotFilteredByStaleMapping(t *testing.T) {
	account := newAnthropicPassthroughAccountFixture()
	require.True(t, account.IsModelSupported("claude-opus-5-5"),
		"passthrough 账号必须被视为支持任意模型")
	svc := anthropicPassthroughSelectionService(account)
	groupID := int64(118)

	selected, err := svc.SelectAccountForModelWithExclusions(
		anthropicPassthroughSelectionContext(groupID), &groupID, "", "claude-opus-5-5", nil)
	require.NoError(t, err, "passthrough 账号必须能被选中，不能因残留 model_mapping 报 no available accounts")
	require.NotNil(t, selected)
	require.Equal(t, int64(34786), selected.ID)
}

// 反例：同样的残留映射，但账号没开 passthrough —— 候选应被模型白名单排除，
// 错误里必须带出 model_not_supported 计数（而不是裸 no available accounts）。
func TestSelectAccountForModel_NonPassthroughStaleMappingReportsModelNotSupported(t *testing.T) {
	account := newAnthropicPassthroughAccountFixture()
	account.Extra = nil
	svc := anthropicPassthroughSelectionService(account)
	groupID := int64(118)

	_, err := svc.SelectAccountForModelWithExclusions(
		anthropicPassthroughSelectionContext(groupID), &groupID, "", "claude-opus-5-5", nil)
	require.Error(t, err)
	require.ErrorIs(t, err, ErrNoAvailableAccounts)
	require.Contains(t, err.Error(), "model_not_supported=1",
		"选号失败必须带每个候选被过滤的原因摘要")
}

// 透传账号的模型目录天然不完整，不能被 smart-route 的目录门禁整组跳过。
func TestGroupModelsCatalog_PassthroughAccountIsUnknownNotAbsent(t *testing.T) {
	account := newAnthropicPassthroughAccountFixture()
	group := &Group{ID: 118, Platform: PlatformAnthropic, Status: StatusActive, Hydrated: true}
	snapshot, _ := newSmartRouteCoreSnapshot([]*Group{group}, &account)
	groupID := int64(118)

	cat := loadGroupModelsCatalogFromStore(context.Background(), nil, snapshot, &groupID)
	require.True(t, cat.passthrough, "含 passthrough 账号的分组必须打上 passthrough 标记")
	require.Nil(t, cat.byPlatform[PlatformAnthropic],
		"passthrough 账号的残留 model_mapping 不能变成闭集目录")
	require.Equal(t, groupCatalogModelUnknown, catalogHasRequestedModel(cat, "claude-opus-5-5"))

	// 同一分组若换成一个有完整映射的普通账号，目录就是 present/absent 的正常判定。
	mapped := account
	mapped.Extra = nil
	mapped.Credentials["model_mapping"] = map[string]any{
		"claude-opus-5-5": "claude-opus-5-5-20260101",
	}
	snapshotMapped, _ := newSmartRouteCoreSnapshot([]*Group{group}, &mapped)
	catMapped := loadGroupModelsCatalogFromStore(context.Background(), nil, snapshotMapped, &groupID)
	require.False(t, catMapped.passthrough)
	require.Equal(t, groupCatalogModelPresent, catalogHasRequestedModel(catMapped, "claude-opus-5-5"))
}

// smart-routing key 有多个候选组时，兄弟组目录命中该模型也不能把 passthrough 组跳过；
// 否则 Codex Desktop 打 /v1/responses 会在 routing 阶段直接 503，账号一次都不会被尝试。
func TestAPIKeyRouteIterator_DoesNotSkipPassthroughGroupWhenSiblingListsModel(t *testing.T) {
	passthroughGroup := &Group{ID: 118, Name: "Claude 嘿嘿嘿", Platform: PlatformAnthropic, Status: StatusActive, Hydrated: true}
	siblingGroup := &Group{ID: 53, Name: "Claude 备用", Platform: PlatformAnthropic, Status: StatusActive, Hydrated: true}
	key := smartRouteCoreKey(passthroughGroup, siblingGroup)
	groups := map[int64]*Group{passthroughGroup.ID: passthroughGroup, siblingGroup.ID: siblingGroup}
	catalogs := map[int64]groupModelsCatalog{
		118: {passthrough: true, platforms: map[string]struct{}{PlatformAnthropic: {}}},
		53: {byPlatform: map[string][]string{PlatformAnthropic: {"claude-opus-5-5"}},
			platforms: map[string]struct{}{PlatformAnthropic: {}}},
	}

	it := newAPIKeyRouteIterator(
		context.Background(), key, key.CandidateGroupIDs(), "claude-opus-5-5",
		func(_ context.Context, apiKey *APIKey, groupID int64) (*APIKey, error) {
			return cloneAPIKeyWithGroupID(apiKey, groups[groupID]), nil
		},
		nil,
		func(_ context.Context, groupID *int64) groupModelsCatalog { return catalogs[*groupID] },
		func(_ context.Context, _ *int64, model string) (ChannelMappingResult, bool) {
			return ChannelMappingResult{MappedModel: model}, false
		},
		nil,
	)

	first, ok := it.next()
	require.True(t, ok)
	require.Equal(t, int64(118), *first.key.GroupID,
		"passthrough 分组必须先于兄弟组被尝试，不能因目录缺失被 routing 跳过")
}

// 路由层过滤原因必须累计到错误摘要里（禁止裸 no available accounts）。
func TestAPIKeyRouteIterator_SkipSummaryReportsReasons(t *testing.T) {
	g1 := &Group{ID: 7, Platform: PlatformOpenAI, Status: StatusActive, Hydrated: true}
	g2 := &Group{ID: 8, Platform: PlatformOpenAI, Status: StatusActive, Hydrated: true}
	key := smartRouteCoreKey(g1, g2)
	groups := map[int64]*Group{7: g1, 8: g2}
	// OpenAI 组请求 claude 模型：目录里只有 gpt 系模型（absent），平台也不匹配，
	// 两个候选组都会被目录门禁过滤 —— 正是线上"候选被协议/平台过滤"的形态。
	catalogs := map[int64]groupModelsCatalog{
		7: {byPlatform: map[string][]string{PlatformOpenAI: {"gpt-5.5"}}, platforms: map[string]struct{}{PlatformOpenAI: {}}},
		8: {byPlatform: map[string][]string{PlatformOpenAI: {"gpt-5.4"}}, platforms: map[string]struct{}{PlatformOpenAI: {}}},
	}

	it := newAPIKeyRouteIterator(
		context.Background(), key, key.CandidateGroupIDs(), "claude-opus-5-5",
		func(_ context.Context, apiKey *APIKey, groupID int64) (*APIKey, error) {
			return cloneAPIKeyWithGroupID(apiKey, groups[groupID]), nil
		},
		nil,
		func(_ context.Context, groupID *int64) groupModelsCatalog { return catalogs[*groupID] },
		func(_ context.Context, _ *int64, model string) (ChannelMappingResult, bool) {
			return ChannelMappingResult{MappedModel: model}, false
		},
		nil,
	)
	_, ok := it.next()
	require.False(t, ok)
	summary := it.skipSummary()
	require.Contains(t, summary, "routes_skipped=2")
	require.Contains(t, summary, "catalog_model_absent=2")
}
