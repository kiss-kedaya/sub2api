//go:build unit

package service

import (
	"context"
	"errors"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/config"
	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
	"github.com/stretchr/testify/require"
)

// createGroupRepoStub 只覆盖创建路径用到的两个分组查询；其余方法沿用
// groupRepoStub 的 panic 兜底，避免测试静默调用没准备好的依赖。
type createGroupRepoStub struct {
	*groupRepoStub
	active  []Group
	byID    map[int64]*Group
	listErr error
}

func (s *createGroupRepoStub) ListActive(context.Context) ([]Group, error) {
	if s.listErr != nil {
		return nil, s.listErr
	}
	return s.active, nil
}

func (s *createGroupRepoStub) GetByID(_ context.Context, id int64) (*Group, error) {
	group, ok := s.byID[id]
	if !ok {
		return nil, ErrGroupNotFound
	}
	return group, nil
}

func newCreateGroupService() (*APIKeyService, *createLimitAPIKeyRepoStub, *createGroupRepoStub) {
	repo, cache := newCreateLimitStubs()
	groupRepo := &createGroupRepoStub{groupRepoStub: &groupRepoStub{}, byID: map[int64]*Group{}}
	svc := &APIKeyService{
		apiKeyRepo: repo,
		userRepo:   &userRepoStub{user: &User{ID: 7}},
		groupRepo:  groupRepo,
		cache:      cache,
		cfg:        &config.Config{},
	}
	return svc, repo, groupRepo
}

// 实例里已经有可分配分组时，创建无分组 key 必须被明确错误码拒绝，
// 因为这种 key 在网关侧没有路由，创建出来就是废 key。
func TestAPIKeyServiceCreateRejectsGrouplessKeyWhenInstanceHasGroups(t *testing.T) {
	svc, repo, groupRepo := newCreateGroupService()
	groupRepo.active = []Group{{ID: 3, Name: "DeepSeek", Platform: "deepseek", Status: StatusActive}}
	groupRepo.byID[3] = &groupRepo.active[0]

	_, err := svc.Create(context.Background(), 7, CreateAPIKeyRequest{Name: "no group"})
	require.ErrorIs(t, err, ErrAPIKeyGroupRequired)
	require.Empty(t, repo.created, "被拒绝的请求不能落库")

	var appErr *infraerrors.ApplicationError
	require.True(t, errors.As(err, &appErr))
	require.Equal(t, "API_KEY_GROUP_REQUIRED", appErr.Reason)
	require.Equal(t, int32(400), appErr.Code)
}

// 全新实例（一个活跃分组都没有）允许创建无分组 key，避免初始化阶段完全建不了 key。
func TestAPIKeyServiceCreateAllowsGrouplessKeyWhenInstanceHasNoGroups(t *testing.T) {
	svc, repo, _ := newCreateGroupService()

	key, err := svc.Create(context.Background(), 7, CreateAPIKeyRequest{Name: "bootstrap key"})
	require.NoError(t, err)
	require.NotEmpty(t, key.Key)
	require.Nil(t, key.Group)
	require.Len(t, repo.created, 1)
}

// 分组查询失败时不能静默放行：宁可创建失败，也不要再落一把无分组废 key。
func TestAPIKeyServiceCreateFailsWhenGroupLookupFails(t *testing.T) {
	svc, repo, groupRepo := newCreateGroupService()
	groupRepo.listErr = errors.New("db down")

	_, err := svc.Create(context.Background(), 7, CreateAPIKeyRequest{Name: "no group"})
	require.Error(t, err)
	require.Empty(t, repo.created)
}

// 创建回执要带上完整 group（与列表接口 WithGroup 预加载同构），
// 前端创建后立刻点「使用密钥」才不会误报「尚未分配分组」。
func TestAPIKeyServiceCreateResponseCarriesBoundGroup(t *testing.T) {
	svc, _, groupRepo := newCreateGroupService()
	group := Group{
		ID:               3,
		Name:             "DeepSeek",
		Platform:         "deepseek",
		SubscriptionType: SubscriptionTypeStandard,
		Status:           StatusActive,
	}
	groupRepo.active = []Group{group}
	groupRepo.byID[3] = &group

	groupID := int64(3)
	key, err := svc.Create(context.Background(), 7, CreateAPIKeyRequest{Name: "grouped", GroupID: &groupID})
	require.NoError(t, err)
	require.NotNil(t, key.Group)
	require.Equal(t, int64(3), key.Group.ID)
	require.Equal(t, "DeepSeek", key.Group.Name)
	require.Equal(t, "deepseek", key.Group.Platform)
	require.NotNil(t, key.GroupID)
	require.Equal(t, int64(3), *key.GroupID)
}

// 轮换/编辑回执同样要带分组，否则前端拿到明文回执后马上点「使用密钥」仍会误判。
func TestAPIKeyServiceUpdateResponseCarriesBoundGroup(t *testing.T) {
	svc, _, groupRepo := newCreateGroupService()
	group := Group{ID: 5, Name: "Grok", Platform: "grok", Status: StatusActive}
	groupRepo.byID[5] = &group
	svc.apiKeyRepo.(*createLimitAPIKeyRepoStub).apiKey = &APIKey{
		ID: 1, UserID: 7, Key: "rotate-me", Status: StatusActive,
	}

	groupIDs := []int64{5}
	updated, err := svc.Update(context.Background(), 1, 7, UpdateAPIKeyRequest{GroupIDs: &groupIDs})
	require.NoError(t, err)
	require.NotNil(t, updated.Group)
	require.Equal(t, int64(5), updated.Group.ID)
	require.Equal(t, "grok", updated.Group.Platform)
}
