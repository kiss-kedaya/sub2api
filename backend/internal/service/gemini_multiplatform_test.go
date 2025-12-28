//go:build unit

package service

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/pagination"
	"github.com/stretchr/testify/require"
)

// mockAccountRepoForGemini Gemini 测试用的 mock
type mockAccountRepoForGemini struct {
	accounts     []Account
	accountsByID map[int64]*Account
}

func (m *mockAccountRepoForGemini) GetByID(ctx context.Context, id int64) (*Account, error) {
	if acc, ok := m.accountsByID[id]; ok {
		return acc, nil
	}
	return nil, errors.New("account not found")
}

func (m *mockAccountRepoForGemini) ListSchedulableByPlatforms(ctx context.Context, platforms []string) ([]Account, error) {
	platformSet := make(map[string]bool)
	for _, p := range platforms {
		platformSet[p] = true
	}
	var result []Account
	for _, acc := range m.accounts {
		if platformSet[acc.Platform] && acc.IsSchedulable() {
			result = append(result, acc)
		}
	}
	return result, nil
}

func (m *mockAccountRepoForGemini) ListSchedulableByGroupIDAndPlatforms(ctx context.Context, groupID int64, platforms []string) ([]Account, error) {
	return m.ListSchedulableByPlatforms(ctx, platforms)
}

// Stub methods to implement AccountRepository interface
func (m *mockAccountRepoForGemini) Create(ctx context.Context, account *Account) error { return nil }
func (m *mockAccountRepoForGemini) GetByCRSAccountID(ctx context.Context, crsAccountID string) (*Account, error) {
	return nil, nil
}
func (m *mockAccountRepoForGemini) Update(ctx context.Context, account *Account) error { return nil }
func (m *mockAccountRepoForGemini) Delete(ctx context.Context, id int64) error         { return nil }
func (m *mockAccountRepoForGemini) List(ctx context.Context, params pagination.PaginationParams) ([]Account, *pagination.PaginationResult, error) {
	return nil, nil, nil
}
func (m *mockAccountRepoForGemini) ListWithFilters(ctx context.Context, params pagination.PaginationParams, platform, accountType, status, search string) ([]Account, *pagination.PaginationResult, error) {
	return nil, nil, nil
}
func (m *mockAccountRepoForGemini) ListByGroup(ctx context.Context, groupID int64) ([]Account, error) {
	return nil, nil
}
func (m *mockAccountRepoForGemini) ListActive(ctx context.Context) ([]Account, error) { return nil, nil }
func (m *mockAccountRepoForGemini) ListByPlatform(ctx context.Context, platform string) ([]Account, error) {
	return nil, nil
}
func (m *mockAccountRepoForGemini) UpdateLastUsed(ctx context.Context, id int64) error { return nil }
func (m *mockAccountRepoForGemini) BatchUpdateLastUsed(ctx context.Context, updates map[int64]time.Time) error {
	return nil
}
func (m *mockAccountRepoForGemini) SetError(ctx context.Context, id int64, errorMsg string) error {
	return nil
}
func (m *mockAccountRepoForGemini) SetSchedulable(ctx context.Context, id int64, schedulable bool) error {
	return nil
}
func (m *mockAccountRepoForGemini) BindGroups(ctx context.Context, accountID int64, groupIDs []int64) error {
	return nil
}
func (m *mockAccountRepoForGemini) ListSchedulable(ctx context.Context) ([]Account, error) {
	return nil, nil
}
func (m *mockAccountRepoForGemini) ListSchedulableByGroupID(ctx context.Context, groupID int64) ([]Account, error) {
	return nil, nil
}
func (m *mockAccountRepoForGemini) ListSchedulableByPlatform(ctx context.Context, platform string) ([]Account, error) {
	var result []Account
	for _, acc := range m.accounts {
		if acc.Platform == platform && acc.IsSchedulable() {
			result = append(result, acc)
		}
	}
	return result, nil
}
func (m *mockAccountRepoForGemini) ListSchedulableByGroupIDAndPlatform(ctx context.Context, groupID int64, platform string) ([]Account, error) {
	// 测试时不区分 groupID，直接按 platform 过滤
	return m.ListSchedulableByPlatform(ctx, platform)
}
func (m *mockAccountRepoForGemini) SetRateLimited(ctx context.Context, id int64, resetAt time.Time) error {
	return nil
}
func (m *mockAccountRepoForGemini) SetOverloaded(ctx context.Context, id int64, until time.Time) error {
	return nil
}
func (m *mockAccountRepoForGemini) ClearRateLimit(ctx context.Context, id int64) error { return nil }
func (m *mockAccountRepoForGemini) UpdateSessionWindow(ctx context.Context, id int64, start, end *time.Time, status string) error {
	return nil
}
func (m *mockAccountRepoForGemini) UpdateExtra(ctx context.Context, id int64, updates map[string]any) error {
	return nil
}
func (m *mockAccountRepoForGemini) BulkUpdate(ctx context.Context, ids []int64, updates AccountBulkUpdate) (int64, error) {
	return 0, nil
}

// Verify interface implementation
var _ AccountRepository = (*mockAccountRepoForGemini)(nil)

// mockGatewayCacheForGemini Gemini 测试用的 cache mock
type mockGatewayCacheForGemini struct {
	sessionBindings map[string]int64
}

func (m *mockGatewayCacheForGemini) GetSessionAccountID(ctx context.Context, sessionHash string) (int64, error) {
	if id, ok := m.sessionBindings[sessionHash]; ok {
		return id, nil
	}
	return 0, errors.New("not found")
}

func (m *mockGatewayCacheForGemini) SetSessionAccountID(ctx context.Context, sessionHash string, accountID int64, ttl time.Duration) error {
	if m.sessionBindings == nil {
		m.sessionBindings = make(map[string]int64)
	}
	m.sessionBindings[sessionHash] = accountID
	return nil
}

func (m *mockGatewayCacheForGemini) RefreshSessionTTL(ctx context.Context, sessionHash string, ttl time.Duration) error {
	return nil
}

func TestGeminiMessagesCompatService_SelectAccountForModelWithExclusions_OnlyGemini(t *testing.T) {
	ctx := context.Background()

	repo := &mockAccountRepoForGemini{
		accounts: []Account{
			{ID: 1, Platform: PlatformGemini, Priority: 1, Status: StatusActive, Schedulable: true},
			{ID: 2, Platform: PlatformGemini, Priority: 2, Status: StatusActive, Schedulable: true},
		},
		accountsByID: map[int64]*Account{},
	}
	for i := range repo.accounts {
		repo.accountsByID[repo.accounts[i].ID] = &repo.accounts[i]
	}

	cache := &mockGatewayCacheForGemini{}

	svc := &GeminiMessagesCompatService{
		accountRepo: repo,
		cache:       cache,
	}

	acc, err := svc.SelectAccountForModelWithExclusions(ctx, nil, "", "gemini-2.5-flash", nil)
	require.NoError(t, err)
	require.NotNil(t, acc)
	require.Equal(t, int64(1), acc.ID, "应选择优先级最高的账户")
}

func TestGeminiMessagesCompatService_SelectAccountForModelWithExclusions_OnlyAntigravity(t *testing.T) {
	ctx := context.Background()

	repo := &mockAccountRepoForGemini{
		accounts: []Account{
			{ID: 1, Platform: PlatformAntigravity, Priority: 1, Status: StatusActive, Schedulable: true},
		},
		accountsByID: map[int64]*Account{},
	}
	for i := range repo.accounts {
		repo.accountsByID[repo.accounts[i].ID] = &repo.accounts[i]
	}

	cache := &mockGatewayCacheForGemini{}

	svc := &GeminiMessagesCompatService{
		accountRepo: repo,
		cache:       cache,
	}

	acc, err := svc.SelectAccountForModelWithExclusions(ctx, nil, "", "gemini-2.5-flash", nil)
	require.NoError(t, err)
	require.NotNil(t, acc)
	require.Equal(t, int64(1), acc.ID)
	require.Equal(t, PlatformAntigravity, acc.Platform)
}

func TestGeminiMessagesCompatService_SelectAccountForModelWithExclusions_ExcludesAnthropic(t *testing.T) {
	ctx := context.Background()

	repo := &mockAccountRepoForGemini{
		accounts: []Account{
			{ID: 1, Platform: PlatformAnthropic, Priority: 1, Status: StatusActive, Schedulable: true},
			{ID: 2, Platform: PlatformGemini, Priority: 2, Status: StatusActive, Schedulable: true},
			{ID: 3, Platform: PlatformAntigravity, Priority: 3, Status: StatusActive, Schedulable: true},
		},
		accountsByID: map[int64]*Account{},
	}
	for i := range repo.accounts {
		repo.accountsByID[repo.accounts[i].ID] = &repo.accounts[i]
	}

	cache := &mockGatewayCacheForGemini{}

	svc := &GeminiMessagesCompatService{
		accountRepo: repo,
		cache:       cache,
	}

	acc, err := svc.SelectAccountForModelWithExclusions(ctx, nil, "", "gemini-2.5-flash", nil)
	require.NoError(t, err)
	require.NotNil(t, acc)
	// Anthropic 不在 [gemini, antigravity] 平台列表中，应被过滤
	require.Equal(t, int64(2), acc.ID, "Anthropic 平台应被排除，选择 Gemini")
}

func TestGeminiMessagesCompatService_SelectAccountForModelWithExclusions_MixedPlatforms_SamePriority(t *testing.T) {
	ctx := context.Background()
	now := time.Now()

	repo := &mockAccountRepoForGemini{
		accounts: []Account{
			{ID: 1, Platform: PlatformGemini, Priority: 1, Status: StatusActive, Schedulable: true, LastUsedAt: ptr(now.Add(-1 * time.Hour))},
			{ID: 2, Platform: PlatformAntigravity, Priority: 1, Status: StatusActive, Schedulable: true, LastUsedAt: ptr(now.Add(-2 * time.Hour))},
		},
		accountsByID: map[int64]*Account{},
	}
	for i := range repo.accounts {
		repo.accountsByID[repo.accounts[i].ID] = &repo.accounts[i]
	}

	cache := &mockGatewayCacheForGemini{}

	svc := &GeminiMessagesCompatService{
		accountRepo: repo,
		cache:       cache,
	}

	acc, err := svc.SelectAccountForModelWithExclusions(ctx, nil, "", "gemini-2.5-flash", nil)
	require.NoError(t, err)
	require.NotNil(t, acc)
	require.Equal(t, int64(2), acc.ID, "应选择最久未用的账户（Antigravity）")
}

func TestGeminiMessagesCompatService_SelectAccountForModelWithExclusions_OAuthPreferred(t *testing.T) {
	ctx := context.Background()

	repo := &mockAccountRepoForGemini{
		accounts: []Account{
			{ID: 1, Platform: PlatformGemini, Type: AccountTypeApiKey, Priority: 1, Status: StatusActive, Schedulable: true, LastUsedAt: nil},
			{ID: 2, Platform: PlatformGemini, Type: AccountTypeOAuth, Priority: 1, Status: StatusActive, Schedulable: true, LastUsedAt: nil},
		},
		accountsByID: map[int64]*Account{},
	}
	for i := range repo.accounts {
		repo.accountsByID[repo.accounts[i].ID] = &repo.accounts[i]
	}

	cache := &mockGatewayCacheForGemini{}

	svc := &GeminiMessagesCompatService{
		accountRepo: repo,
		cache:       cache,
	}

	acc, err := svc.SelectAccountForModelWithExclusions(ctx, nil, "", "gemini-2.5-flash", nil)
	require.NoError(t, err)
	require.NotNil(t, acc)
	require.Equal(t, int64(2), acc.ID, "同优先级且都未使用时，应优先选择 OAuth 账户")
	require.Equal(t, AccountTypeOAuth, acc.Type)
}

func TestGeminiMessagesCompatService_SelectAccountForModelWithExclusions_OAuthPreferred_MixedPlatforms(t *testing.T) {
	ctx := context.Background()

	repo := &mockAccountRepoForGemini{
		accounts: []Account{
			{ID: 1, Platform: PlatformGemini, Type: AccountTypeApiKey, Priority: 1, Status: StatusActive, Schedulable: true, LastUsedAt: nil},
			{ID: 2, Platform: PlatformAntigravity, Type: AccountTypeOAuth, Priority: 1, Status: StatusActive, Schedulable: true, LastUsedAt: nil},
		},
		accountsByID: map[int64]*Account{},
	}
	for i := range repo.accounts {
		repo.accountsByID[repo.accounts[i].ID] = &repo.accounts[i]
	}

	cache := &mockGatewayCacheForGemini{}

	svc := &GeminiMessagesCompatService{
		accountRepo: repo,
		cache:       cache,
	}

	acc, err := svc.SelectAccountForModelWithExclusions(ctx, nil, "", "gemini-2.5-flash", nil)
	require.NoError(t, err)
	require.NotNil(t, acc)
	require.Equal(t, int64(2), acc.ID, "跨平台时，同样优先选择 OAuth 账户")
}

func TestGeminiMessagesCompatService_SelectAccountForModelWithExclusions_NoAvailableAccounts(t *testing.T) {
	ctx := context.Background()

	repo := &mockAccountRepoForGemini{
		accounts:     []Account{},
		accountsByID: map[int64]*Account{},
	}

	cache := &mockGatewayCacheForGemini{}

	svc := &GeminiMessagesCompatService{
		accountRepo: repo,
		cache:       cache,
	}

	acc, err := svc.SelectAccountForModelWithExclusions(ctx, nil, "", "gemini-2.5-flash", nil)
	require.Error(t, err)
	require.Nil(t, acc)
	require.Contains(t, err.Error(), "no available Gemini/Antigravity accounts")
}

func TestGeminiMessagesCompatService_SelectAccountForModelWithExclusions_StickySession(t *testing.T) {
	ctx := context.Background()

	t.Run("粘性会话命中-使用gemini前缀缓存键", func(t *testing.T) {
		repo := &mockAccountRepoForGemini{
			accounts: []Account{
				{ID: 1, Platform: PlatformGemini, Priority: 2, Status: StatusActive, Schedulable: true},
				{ID: 2, Platform: PlatformAntigravity, Priority: 1, Status: StatusActive, Schedulable: true},
			},
			accountsByID: map[int64]*Account{},
		}
		for i := range repo.accounts {
			repo.accountsByID[repo.accounts[i].ID] = &repo.accounts[i]
		}

		// 注意：缓存键使用 "gemini:" 前缀
		cache := &mockGatewayCacheForGemini{
			sessionBindings: map[string]int64{"gemini:session-123": 1},
		}

		svc := &GeminiMessagesCompatService{
			accountRepo: repo,
			cache:       cache,
		}

		acc, err := svc.SelectAccountForModelWithExclusions(ctx, nil, "session-123", "gemini-2.5-flash", nil)
		require.NoError(t, err)
		require.NotNil(t, acc)
		require.Equal(t, int64(1), acc.ID, "应返回粘性会话绑定的账户")
	})

	t.Run("粘性会话不命中无前缀缓存键", func(t *testing.T) {
		repo := &mockAccountRepoForGemini{
			accounts: []Account{
				{ID: 1, Platform: PlatformGemini, Priority: 2, Status: StatusActive, Schedulable: true},
				{ID: 2, Platform: PlatformAntigravity, Priority: 1, Status: StatusActive, Schedulable: true},
			},
			accountsByID: map[int64]*Account{},
		}
		for i := range repo.accounts {
			repo.accountsByID[repo.accounts[i].ID] = &repo.accounts[i]
		}

		// 缓存键没有 "gemini:" 前缀，不应命中
		cache := &mockGatewayCacheForGemini{
			sessionBindings: map[string]int64{"session-123": 1},
		}

		svc := &GeminiMessagesCompatService{
			accountRepo: repo,
			cache:       cache,
		}

		acc, err := svc.SelectAccountForModelWithExclusions(ctx, nil, "session-123", "gemini-2.5-flash", nil)
		require.NoError(t, err)
		require.NotNil(t, acc)
		// 粘性会话未命中，按优先级选择
		require.Equal(t, int64(2), acc.ID, "粘性会话未命中，应按优先级选择 Antigravity")
	})

	t.Run("粘性会话Anthropic账户-降级选择", func(t *testing.T) {
		repo := &mockAccountRepoForGemini{
			accounts: []Account{
				{ID: 1, Platform: PlatformAnthropic, Priority: 2, Status: StatusActive, Schedulable: true},
				{ID: 2, Platform: PlatformGemini, Priority: 1, Status: StatusActive, Schedulable: true},
			},
			accountsByID: map[int64]*Account{},
		}
		for i := range repo.accounts {
			repo.accountsByID[repo.accounts[i].ID] = &repo.accounts[i]
		}

		cache := &mockGatewayCacheForGemini{
			sessionBindings: map[string]int64{"gemini:session-123": 1},
		}

		svc := &GeminiMessagesCompatService{
			accountRepo: repo,
			cache:       cache,
		}

		acc, err := svc.SelectAccountForModelWithExclusions(ctx, nil, "session-123", "gemini-2.5-flash", nil)
		require.NoError(t, err)
		require.NotNil(t, acc)
		// 粘性会话绑定的是 Anthropic 账户，不在 Gemini 平台列表中，应降级选择
		require.Equal(t, int64(2), acc.ID, "粘性会话账户是 Anthropic，应降级选择 Gemini 平台账户")
	})
}

func TestGeminiMessagesCompatService_HasAntigravityAccounts(t *testing.T) {
	ctx := context.Background()

	t.Run("有antigravity账户时返回true", func(t *testing.T) {
		repo := &mockAccountRepoForGemini{
			accounts: []Account{
				{ID: 1, Platform: PlatformGemini, Status: StatusActive, Schedulable: true},
				{ID: 2, Platform: PlatformAntigravity, Status: StatusActive, Schedulable: true},
			},
		}

		svc := &GeminiMessagesCompatService{accountRepo: repo}

		has, err := svc.HasAntigravityAccounts(ctx, nil)
		require.NoError(t, err)
		require.True(t, has)
	})

	t.Run("无antigravity账户时返回false", func(t *testing.T) {
		repo := &mockAccountRepoForGemini{
			accounts: []Account{
				{ID: 1, Platform: PlatformGemini, Status: StatusActive, Schedulable: true},
			},
		}

		svc := &GeminiMessagesCompatService{accountRepo: repo}

		has, err := svc.HasAntigravityAccounts(ctx, nil)
		require.NoError(t, err)
		require.False(t, has)
	})

	t.Run("antigravity账户不可调度时返回false", func(t *testing.T) {
		repo := &mockAccountRepoForGemini{
			accounts: []Account{
				{ID: 1, Platform: PlatformAntigravity, Status: StatusActive, Schedulable: false},
			},
		}

		svc := &GeminiMessagesCompatService{accountRepo: repo}

		has, err := svc.HasAntigravityAccounts(ctx, nil)
		require.NoError(t, err)
		require.False(t, has)
	})

	t.Run("带groupID查询", func(t *testing.T) {
		repo := &mockAccountRepoForGemini{
			accounts: []Account{
				{ID: 1, Platform: PlatformAntigravity, Status: StatusActive, Schedulable: true},
			},
		}

		svc := &GeminiMessagesCompatService{accountRepo: repo}

		groupID := int64(1)
		has, err := svc.HasAntigravityAccounts(ctx, &groupID)
		require.NoError(t, err)
		require.True(t, has)
	})
}

// TestGeminiPlatformRouting_DocumentRouteDecision 测试平台路由决策逻辑
// 该测试文档化了 Handler 层应该如何根据 account.Platform 进行分流
func TestGeminiPlatformRouting_DocumentRouteDecision(t *testing.T) {
	tests := []struct {
		name            string
		platform        string
		expectedService string // "gemini" 表示 ForwardNative, "antigravity" 表示 ForwardGemini
	}{
		{
			name:            "Gemini平台走ForwardNative",
			platform:        PlatformGemini,
			expectedService: "gemini",
		},
		{
			name:            "Antigravity平台走ForwardGemini",
			platform:        PlatformAntigravity,
			expectedService: "antigravity",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			account := &Account{Platform: tt.platform}

			// 模拟 Handler 层的路由逻辑
			var serviceName string
			if account.Platform == PlatformAntigravity {
				serviceName = "antigravity"
			} else {
				serviceName = "gemini"
			}

			require.Equal(t, tt.expectedService, serviceName,
				"平台 %s 应该路由到 %s 服务", tt.platform, tt.expectedService)
		})
	}
}

func TestGeminiMessagesCompatService_isModelSupportedByAccount(t *testing.T) {
	svc := &GeminiMessagesCompatService{}

	tests := []struct {
		name     string
		account  *Account
		model    string
		expected bool
	}{
		{
			name:     "Antigravity平台-支持gemini模型",
			account:  &Account{Platform: PlatformAntigravity},
			model:    "gemini-2.5-flash",
			expected: true,
		},
		{
			name:     "Antigravity平台-支持claude模型",
			account:  &Account{Platform: PlatformAntigravity},
			model:    "claude-3-5-sonnet-20241022",
			expected: true,
		},
		{
			name:     "Antigravity平台-不支持gpt模型",
			account:  &Account{Platform: PlatformAntigravity},
			model:    "gpt-4",
			expected: false,
		},
		{
			name:     "Gemini平台-无映射配置-支持所有模型",
			account:  &Account{Platform: PlatformGemini},
			model:    "gemini-2.5-flash",
			expected: true,
		},
		{
			name: "Gemini平台-有映射配置-只支持配置的模型",
			account: &Account{
				Platform:    PlatformGemini,
				Credentials: map[string]any{"model_mapping": map[string]any{"gemini-1.5-pro": "x"}},
			},
			model:    "gemini-2.5-flash",
			expected: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := svc.isModelSupportedByAccount(tt.account, tt.model)
			require.Equal(t, tt.expected, got)
		})
	}
}
