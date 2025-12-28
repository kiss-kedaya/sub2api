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

// mockAccountRepoForMultiplatform 多平台测试用的 mock
type mockAccountRepoForMultiplatform struct {
	accounts          []Account
	accountsByID      map[int64]*Account
	listPlatformsFunc func(ctx context.Context, platforms []string) ([]Account, error)
}

func (m *mockAccountRepoForMultiplatform) GetByID(ctx context.Context, id int64) (*Account, error) {
	if acc, ok := m.accountsByID[id]; ok {
		return acc, nil
	}
	return nil, errors.New("account not found")
}

func (m *mockAccountRepoForMultiplatform) ListSchedulableByPlatforms(ctx context.Context, platforms []string) ([]Account, error) {
	if m.listPlatformsFunc != nil {
		return m.listPlatformsFunc(ctx, platforms)
	}
	// 过滤符合平台的账户
	var result []Account
	platformSet := make(map[string]bool)
	for _, p := range platforms {
		platformSet[p] = true
	}
	for _, acc := range m.accounts {
		if platformSet[acc.Platform] && acc.IsSchedulable() {
			result = append(result, acc)
		}
	}
	return result, nil
}

func (m *mockAccountRepoForMultiplatform) ListSchedulableByGroupIDAndPlatforms(ctx context.Context, groupID int64, platforms []string) ([]Account, error) {
	return m.ListSchedulableByPlatforms(ctx, platforms)
}

// Stub methods to implement AccountRepository interface
func (m *mockAccountRepoForMultiplatform) Create(ctx context.Context, account *Account) error {
	return nil
}
func (m *mockAccountRepoForMultiplatform) GetByCRSAccountID(ctx context.Context, crsAccountID string) (*Account, error) {
	return nil, nil
}
func (m *mockAccountRepoForMultiplatform) Update(ctx context.Context, account *Account) error {
	return nil
}
func (m *mockAccountRepoForMultiplatform) Delete(ctx context.Context, id int64) error { return nil }
func (m *mockAccountRepoForMultiplatform) List(ctx context.Context, params pagination.PaginationParams) ([]Account, *pagination.PaginationResult, error) {
	return nil, nil, nil
}
func (m *mockAccountRepoForMultiplatform) ListWithFilters(ctx context.Context, params pagination.PaginationParams, platform, accountType, status, search string) ([]Account, *pagination.PaginationResult, error) {
	return nil, nil, nil
}
func (m *mockAccountRepoForMultiplatform) ListByGroup(ctx context.Context, groupID int64) ([]Account, error) {
	return nil, nil
}
func (m *mockAccountRepoForMultiplatform) ListActive(ctx context.Context) ([]Account, error) {
	return nil, nil
}
func (m *mockAccountRepoForMultiplatform) ListByPlatform(ctx context.Context, platform string) ([]Account, error) {
	return nil, nil
}
func (m *mockAccountRepoForMultiplatform) UpdateLastUsed(ctx context.Context, id int64) error {
	return nil
}
func (m *mockAccountRepoForMultiplatform) BatchUpdateLastUsed(ctx context.Context, updates map[int64]time.Time) error {
	return nil
}
func (m *mockAccountRepoForMultiplatform) SetError(ctx context.Context, id int64, errorMsg string) error {
	return nil
}
func (m *mockAccountRepoForMultiplatform) SetSchedulable(ctx context.Context, id int64, schedulable bool) error {
	return nil
}
func (m *mockAccountRepoForMultiplatform) BindGroups(ctx context.Context, accountID int64, groupIDs []int64) error {
	return nil
}
func (m *mockAccountRepoForMultiplatform) ListSchedulable(ctx context.Context) ([]Account, error) {
	return nil, nil
}
func (m *mockAccountRepoForMultiplatform) ListSchedulableByGroupID(ctx context.Context, groupID int64) ([]Account, error) {
	return nil, nil
}
func (m *mockAccountRepoForMultiplatform) ListSchedulableByPlatform(ctx context.Context, platform string) ([]Account, error) {
	return nil, nil
}
func (m *mockAccountRepoForMultiplatform) ListSchedulableByGroupIDAndPlatform(ctx context.Context, groupID int64, platform string) ([]Account, error) {
	return nil, nil
}
func (m *mockAccountRepoForMultiplatform) SetRateLimited(ctx context.Context, id int64, resetAt time.Time) error {
	return nil
}
func (m *mockAccountRepoForMultiplatform) SetOverloaded(ctx context.Context, id int64, until time.Time) error {
	return nil
}
func (m *mockAccountRepoForMultiplatform) ClearRateLimit(ctx context.Context, id int64) error {
	return nil
}
func (m *mockAccountRepoForMultiplatform) UpdateSessionWindow(ctx context.Context, id int64, start, end *time.Time, status string) error {
	return nil
}
func (m *mockAccountRepoForMultiplatform) UpdateExtra(ctx context.Context, id int64, updates map[string]any) error {
	return nil
}
func (m *mockAccountRepoForMultiplatform) BulkUpdate(ctx context.Context, ids []int64, updates AccountBulkUpdate) (int64, error) {
	return 0, nil
}

// Verify interface implementation
var _ AccountRepository = (*mockAccountRepoForMultiplatform)(nil)

// mockGatewayCacheForMultiplatform 多平台测试用的 cache mock
type mockGatewayCacheForMultiplatform struct {
	sessionBindings map[string]int64
}

func (m *mockGatewayCacheForMultiplatform) GetSessionAccountID(ctx context.Context, sessionHash string) (int64, error) {
	if id, ok := m.sessionBindings[sessionHash]; ok {
		return id, nil
	}
	return 0, errors.New("not found")
}

func (m *mockGatewayCacheForMultiplatform) SetSessionAccountID(ctx context.Context, sessionHash string, accountID int64, ttl time.Duration) error {
	if m.sessionBindings == nil {
		m.sessionBindings = make(map[string]int64)
	}
	m.sessionBindings[sessionHash] = accountID
	return nil
}

func (m *mockGatewayCacheForMultiplatform) RefreshSessionTTL(ctx context.Context, sessionHash string, ttl time.Duration) error {
	return nil
}

func ptr[T any](v T) *T {
	return &v
}

func TestGatewayService_SelectAccountForModelWithExclusions_OnlyAnthropic(t *testing.T) {
	ctx := context.Background()

	repo := &mockAccountRepoForMultiplatform{
		accounts: []Account{
			{ID: 1, Platform: PlatformAnthropic, Priority: 1, Status: StatusActive, Schedulable: true},
			{ID: 2, Platform: PlatformAnthropic, Priority: 2, Status: StatusActive, Schedulable: true},
		},
		accountsByID: map[int64]*Account{},
	}
	for i := range repo.accounts {
		repo.accountsByID[repo.accounts[i].ID] = &repo.accounts[i]
	}

	cache := &mockGatewayCacheForMultiplatform{}

	svc := &GatewayService{
		accountRepo: repo,
		cache:       cache,
	}

	acc, err := svc.selectAccountForModelWithPlatforms(ctx, nil, "", "claude-3-5-sonnet-20241022", nil, []string{PlatformAnthropic, PlatformAntigravity})
	require.NoError(t, err)
	require.NotNil(t, acc)
	require.Equal(t, int64(1), acc.ID, "应选择优先级最高的账户")
}

func TestGatewayService_SelectAccountForModelWithExclusions_OnlyAntigravity(t *testing.T) {
	ctx := context.Background()

	repo := &mockAccountRepoForMultiplatform{
		accounts: []Account{
			{ID: 1, Platform: PlatformAntigravity, Priority: 1, Status: StatusActive, Schedulable: true},
		},
		accountsByID: map[int64]*Account{},
	}
	for i := range repo.accounts {
		repo.accountsByID[repo.accounts[i].ID] = &repo.accounts[i]
	}

	cache := &mockGatewayCacheForMultiplatform{}

	svc := &GatewayService{
		accountRepo: repo,
		cache:       cache,
	}

	acc, err := svc.selectAccountForModelWithPlatforms(ctx, nil, "", "claude-3-5-sonnet-20241022", nil, []string{PlatformAnthropic, PlatformAntigravity})
	require.NoError(t, err)
	require.NotNil(t, acc)
	require.Equal(t, int64(1), acc.ID)
	require.Equal(t, PlatformAntigravity, acc.Platform)
}

func TestGatewayService_SelectAccountForModelWithExclusions_MixedPlatforms_SamePriority(t *testing.T) {
	ctx := context.Background()
	now := time.Now()

	repo := &mockAccountRepoForMultiplatform{
		accounts: []Account{
			{ID: 1, Platform: PlatformAnthropic, Priority: 1, Status: StatusActive, Schedulable: true, LastUsedAt: ptr(now.Add(-1 * time.Hour))},
			{ID: 2, Platform: PlatformAntigravity, Priority: 1, Status: StatusActive, Schedulable: true, LastUsedAt: ptr(now.Add(-2 * time.Hour))},
		},
		accountsByID: map[int64]*Account{},
	}
	for i := range repo.accounts {
		repo.accountsByID[repo.accounts[i].ID] = &repo.accounts[i]
	}

	cache := &mockGatewayCacheForMultiplatform{}

	svc := &GatewayService{
		accountRepo: repo,
		cache:       cache,
	}

	acc, err := svc.selectAccountForModelWithPlatforms(ctx, nil, "", "claude-3-5-sonnet-20241022", nil, []string{PlatformAnthropic, PlatformAntigravity})
	require.NoError(t, err)
	require.NotNil(t, acc)
	require.Equal(t, int64(2), acc.ID, "应选择最久未用的账户（Antigravity）")
}

func TestGatewayService_SelectAccountForModelWithExclusions_MixedPlatforms_DiffPriority(t *testing.T) {
	ctx := context.Background()

	repo := &mockAccountRepoForMultiplatform{
		accounts: []Account{
			{ID: 1, Platform: PlatformAnthropic, Priority: 2, Status: StatusActive, Schedulable: true},
			{ID: 2, Platform: PlatformAntigravity, Priority: 1, Status: StatusActive, Schedulable: true},
		},
		accountsByID: map[int64]*Account{},
	}
	for i := range repo.accounts {
		repo.accountsByID[repo.accounts[i].ID] = &repo.accounts[i]
	}

	cache := &mockGatewayCacheForMultiplatform{}

	svc := &GatewayService{
		accountRepo: repo,
		cache:       cache,
	}

	acc, err := svc.selectAccountForModelWithPlatforms(ctx, nil, "", "claude-3-5-sonnet-20241022", nil, []string{PlatformAnthropic, PlatformAntigravity})
	require.NoError(t, err)
	require.NotNil(t, acc)
	require.Equal(t, int64(2), acc.ID, "应选择优先级更高的账户（Antigravity, priority=1）")
}

func TestGatewayService_SelectAccountForModelWithExclusions_ModelNotSupported(t *testing.T) {
	ctx := context.Background()

	repo := &mockAccountRepoForMultiplatform{
		accounts: []Account{
			// Anthropic 账户配置了模型映射，只支持 other-model
			// 注意：model_mapping 需要是 map[string]any 格式
			{
				ID: 1, Platform: PlatformAnthropic, Priority: 1, Status: StatusActive, Schedulable: true,
				Credentials: map[string]any{"model_mapping": map[string]any{"other-model": "x"}},
			},
			// Antigravity 账户支持所有 claude 模型
			{ID: 2, Platform: PlatformAntigravity, Priority: 2, Status: StatusActive, Schedulable: true},
		},
		accountsByID: map[int64]*Account{},
	}
	for i := range repo.accounts {
		repo.accountsByID[repo.accounts[i].ID] = &repo.accounts[i]
	}

	cache := &mockGatewayCacheForMultiplatform{}

	svc := &GatewayService{
		accountRepo: repo,
		cache:       cache,
	}

	acc, err := svc.selectAccountForModelWithPlatforms(ctx, nil, "", "claude-3-5-sonnet-20241022", nil, []string{PlatformAnthropic, PlatformAntigravity})
	require.NoError(t, err)
	require.NotNil(t, acc)
	require.Equal(t, int64(2), acc.ID, "Anthropic 不支持该模型，应选择 Antigravity")
}

func TestGatewayService_SelectAccountForModelWithExclusions_NoAvailableAccounts(t *testing.T) {
	ctx := context.Background()

	repo := &mockAccountRepoForMultiplatform{
		accounts:     []Account{},
		accountsByID: map[int64]*Account{},
	}

	cache := &mockGatewayCacheForMultiplatform{}

	svc := &GatewayService{
		accountRepo: repo,
		cache:       cache,
	}

	acc, err := svc.selectAccountForModelWithPlatforms(ctx, nil, "", "claude-3-5-sonnet-20241022", nil, []string{PlatformAnthropic, PlatformAntigravity})
	require.Error(t, err)
	require.Nil(t, acc)
	require.Contains(t, err.Error(), "no available accounts")
}

func TestGatewayService_SelectAccountForModelWithExclusions_AllExcluded(t *testing.T) {
	ctx := context.Background()

	repo := &mockAccountRepoForMultiplatform{
		accounts: []Account{
			{ID: 1, Platform: PlatformAnthropic, Priority: 1, Status: StatusActive, Schedulable: true},
			{ID: 2, Platform: PlatformAntigravity, Priority: 1, Status: StatusActive, Schedulable: true},
		},
		accountsByID: map[int64]*Account{},
	}
	for i := range repo.accounts {
		repo.accountsByID[repo.accounts[i].ID] = &repo.accounts[i]
	}

	cache := &mockGatewayCacheForMultiplatform{}

	svc := &GatewayService{
		accountRepo: repo,
		cache:       cache,
	}

	excludedIDs := map[int64]struct{}{1: {}, 2: {}}
	acc, err := svc.selectAccountForModelWithPlatforms(ctx, nil, "", "claude-3-5-sonnet-20241022", excludedIDs, []string{PlatformAnthropic, PlatformAntigravity})
	require.Error(t, err)
	require.Nil(t, acc)
}

func TestGatewayService_SelectAccountForModelWithExclusions_Schedulability(t *testing.T) {
	ctx := context.Background()
	now := time.Now()

	tests := []struct {
		name       string
		accounts   []Account
		expectedID int64
	}{
		{
			name: "过载账户被跳过",
			accounts: []Account{
				{ID: 1, Platform: PlatformAnthropic, Priority: 1, Status: StatusActive, Schedulable: true, OverloadUntil: ptr(now.Add(1 * time.Hour))},
				{ID: 2, Platform: PlatformAntigravity, Priority: 2, Status: StatusActive, Schedulable: true},
			},
			expectedID: 2,
		},
		{
			name: "限流账户被跳过",
			accounts: []Account{
				{ID: 1, Platform: PlatformAnthropic, Priority: 1, Status: StatusActive, Schedulable: true, RateLimitResetAt: ptr(now.Add(1 * time.Hour))},
				{ID: 2, Platform: PlatformAntigravity, Priority: 2, Status: StatusActive, Schedulable: true},
			},
			expectedID: 2,
		},
		{
			name: "非active账户被跳过",
			accounts: []Account{
				{ID: 1, Platform: PlatformAnthropic, Priority: 1, Status: "error", Schedulable: true},
				{ID: 2, Platform: PlatformAntigravity, Priority: 2, Status: StatusActive, Schedulable: true},
			},
			expectedID: 2,
		},
		{
			name: "schedulable=false被跳过",
			accounts: []Account{
				{ID: 1, Platform: PlatformAnthropic, Priority: 1, Status: StatusActive, Schedulable: false},
				{ID: 2, Platform: PlatformAntigravity, Priority: 2, Status: StatusActive, Schedulable: true},
			},
			expectedID: 2,
		},
		{
			name: "过期的过载账户可调度",
			accounts: []Account{
				{ID: 1, Platform: PlatformAnthropic, Priority: 1, Status: StatusActive, Schedulable: true, OverloadUntil: ptr(now.Add(-1 * time.Hour))},
				{ID: 2, Platform: PlatformAntigravity, Priority: 2, Status: StatusActive, Schedulable: true},
			},
			expectedID: 1,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			repo := &mockAccountRepoForMultiplatform{
				accounts:     tt.accounts,
				accountsByID: map[int64]*Account{},
			}
			for i := range repo.accounts {
				repo.accountsByID[repo.accounts[i].ID] = &repo.accounts[i]
			}

			cache := &mockGatewayCacheForMultiplatform{}

			svc := &GatewayService{
				accountRepo: repo,
				cache:       cache,
			}

			acc, err := svc.selectAccountForModelWithPlatforms(ctx, nil, "", "claude-3-5-sonnet-20241022", nil, []string{PlatformAnthropic, PlatformAntigravity})
			require.NoError(t, err)
			require.NotNil(t, acc)
			require.Equal(t, tt.expectedID, acc.ID)
		})
	}
}

func TestGatewayService_SelectAccountForModelWithExclusions_StickySession(t *testing.T) {
	ctx := context.Background()

	t.Run("粘性会话命中", func(t *testing.T) {
		repo := &mockAccountRepoForMultiplatform{
			accounts: []Account{
				{ID: 1, Platform: PlatformAnthropic, Priority: 2, Status: StatusActive, Schedulable: true},
				{ID: 2, Platform: PlatformAntigravity, Priority: 1, Status: StatusActive, Schedulable: true},
			},
			accountsByID: map[int64]*Account{},
		}
		for i := range repo.accounts {
			repo.accountsByID[repo.accounts[i].ID] = &repo.accounts[i]
		}

		cache := &mockGatewayCacheForMultiplatform{
			sessionBindings: map[string]int64{"session-123": 1},
		}

		svc := &GatewayService{
			accountRepo: repo,
			cache:       cache,
		}

		acc, err := svc.selectAccountForModelWithPlatforms(ctx, nil, "session-123", "claude-3-5-sonnet-20241022", nil, []string{PlatformAnthropic, PlatformAntigravity})
		require.NoError(t, err)
		require.NotNil(t, acc)
		require.Equal(t, int64(1), acc.ID, "应返回粘性会话绑定的账户")
	})

	t.Run("粘性会话账户被排除-降级选择", func(t *testing.T) {
		repo := &mockAccountRepoForMultiplatform{
			accounts: []Account{
				{ID: 1, Platform: PlatformAnthropic, Priority: 2, Status: StatusActive, Schedulable: true},
				{ID: 2, Platform: PlatformAntigravity, Priority: 1, Status: StatusActive, Schedulable: true},
			},
			accountsByID: map[int64]*Account{},
		}
		for i := range repo.accounts {
			repo.accountsByID[repo.accounts[i].ID] = &repo.accounts[i]
		}

		cache := &mockGatewayCacheForMultiplatform{
			sessionBindings: map[string]int64{"session-123": 1},
		}

		svc := &GatewayService{
			accountRepo: repo,
			cache:       cache,
		}

		excludedIDs := map[int64]struct{}{1: {}}
		acc, err := svc.selectAccountForModelWithPlatforms(ctx, nil, "session-123", "claude-3-5-sonnet-20241022", excludedIDs, []string{PlatformAnthropic, PlatformAntigravity})
		require.NoError(t, err)
		require.NotNil(t, acc)
		require.Equal(t, int64(2), acc.ID, "粘性会话账户被排除，应选择其他账户")
	})

	t.Run("粘性会话账户不可调度-降级选择", func(t *testing.T) {
		repo := &mockAccountRepoForMultiplatform{
			accounts: []Account{
				{ID: 1, Platform: PlatformAnthropic, Priority: 2, Status: "error", Schedulable: true},
				{ID: 2, Platform: PlatformAntigravity, Priority: 1, Status: StatusActive, Schedulable: true},
			},
			accountsByID: map[int64]*Account{},
		}
		for i := range repo.accounts {
			repo.accountsByID[repo.accounts[i].ID] = &repo.accounts[i]
		}

		cache := &mockGatewayCacheForMultiplatform{
			sessionBindings: map[string]int64{"session-123": 1},
		}

		svc := &GatewayService{
			accountRepo: repo,
			cache:       cache,
		}

		acc, err := svc.selectAccountForModelWithPlatforms(ctx, nil, "session-123", "claude-3-5-sonnet-20241022", nil, []string{PlatformAnthropic, PlatformAntigravity})
		require.NoError(t, err)
		require.NotNil(t, acc)
		require.Equal(t, int64(2), acc.ID, "粘性会话账户不可调度，应选择其他账户")
	})
}

func TestGatewayService_isModelSupportedByAccount(t *testing.T) {
	svc := &GatewayService{}

	tests := []struct {
		name     string
		account  *Account
		model    string
		expected bool
	}{
		{
			name:     "Antigravity平台-支持claude模型",
			account:  &Account{Platform: PlatformAntigravity},
			model:    "claude-3-5-sonnet-20241022",
			expected: true,
		},
		{
			name:     "Antigravity平台-支持gemini模型",
			account:  &Account{Platform: PlatformAntigravity},
			model:    "gemini-2.5-flash",
			expected: true,
		},
		{
			name:     "Antigravity平台-不支持gpt模型",
			account:  &Account{Platform: PlatformAntigravity},
			model:    "gpt-4",
			expected: false,
		},
		{
			name:     "Anthropic平台-无映射配置-支持所有模型",
			account:  &Account{Platform: PlatformAnthropic},
			model:    "claude-3-5-sonnet-20241022",
			expected: true,
		},
		{
			name: "Anthropic平台-有映射配置-只支持配置的模型",
			account: &Account{
				Platform:    PlatformAnthropic,
				Credentials: map[string]any{"model_mapping": map[string]any{"claude-opus-4": "x"}},
			},
			model:    "claude-3-5-sonnet-20241022",
			expected: false,
		},
		{
			name: "Anthropic平台-有映射配置-支持配置的模型",
			account: &Account{
				Platform:    PlatformAnthropic,
				Credentials: map[string]any{"model_mapping": map[string]any{"claude-3-5-sonnet-20241022": "x"}},
			},
			model:    "claude-3-5-sonnet-20241022",
			expected: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := svc.isModelSupportedByAccount(tt.account, tt.model)
			require.Equal(t, tt.expected, got)
		})
	}
}
