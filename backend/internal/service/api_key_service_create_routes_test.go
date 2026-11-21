//go:build unit

package service

import (
	"context"
	"errors"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/stretchr/testify/require"
)

// createRoutesAPIKeyRepoStub 记录智能路由落库结果，用来断言服务层不对分组数做截断。
type createRoutesAPIKeyRepoStub struct {
	*createLimitAPIKeyRepoStub
	createID   int64
	replaceErr error
	replaced   map[int64][]int64
}

func (s *createRoutesAPIKeyRepoStub) Create(ctx context.Context, key *APIKey) error {
	key.ID = s.createID
	s.created = append(s.created, key)
	return nil
}

func (s *createRoutesAPIKeyRepoStub) ReplaceGroupRoutes(_ context.Context, apiKeyID int64, groupIDs []int64) error {
	if s.replaceErr != nil {
		return s.replaceErr
	}
	s.replaced[apiKeyID] = append([]int64(nil), groupIDs...)
	return nil
}

func (s *createRoutesAPIKeyRepoStub) ListGroupRoutes(context.Context, []int64) (map[int64][]int64, error) {
	return map[int64][]int64{}, nil
}

func newCreateRoutesService() (*APIKeyService, *createRoutesAPIKeyRepoStub, *createGroupRepoStub) {
	repo := &createRoutesAPIKeyRepoStub{
		createLimitAPIKeyRepoStub: &createLimitAPIKeyRepoStub{apiKeyRepoStub: &apiKeyRepoStub{}},
		createID:                  42,
		replaced:                  map[int64][]int64{},
	}
	groupRepo := &createGroupRepoStub{groupRepoStub: &groupRepoStub{}, byID: map[int64]*Group{}}
	svc := &APIKeyService{
		apiKeyRepo: repo,
		userRepo:   &userRepoStub{user: &User{ID: 7}},
		groupRepo:  groupRepo,
		cfg:        &config.Config{},
	}
	return svc, repo, groupRepo
}

func addOpenAIGroups(groupRepo *createGroupRepoStub, count int) []int64 {
	groupRepo.active = make([]Group, count)
	ids := make([]int64, count)
	for i := range groupRepo.active {
		groupRepo.active[i] = Group{
			ID:       int64(i + 1),
			Name:     "group",
			Platform: "openai",
			Status:   StatusActive,
		}
	}
	for i := range groupRepo.active {
		ids[i] = groupRepo.active[i].ID
		groupRepo.byID[groupRepo.active[i].ID] = &groupRepo.active[i]
	}
	return ids
}

// issue #494：可用分组超过 10 个（线上是 36 个）时，创建智能路由密钥
// 必须把全部分组按顺序交给 repo，不允许在前端或服务层静默截断。
func TestAPIKeyServiceCreateBindsEveryAvailableGroup(t *testing.T) {
	for _, count := range []int{11, 36, 50} {
		t.Run(map[int]string{11: "eleven-groups", 36: "thirty-six-groups", 50: "fifty-groups"}[count], func(t *testing.T) {
			svc, repo, groupRepo := newCreateRoutesService()
			ids := addOpenAIGroups(groupRepo, count)

			key, err := svc.Create(context.Background(), 7, CreateAPIKeyRequest{Name: "Infinite Canvas", GroupIDs: ids})
			require.NoError(t, err)
			require.Len(t, repo.created, 1)
			require.Equal(t, ids, repo.replaced[42], "every available group must reach the route store in order")
			require.Equal(t, ids, key.RouteGroupIDs)
			require.NotNil(t, key.GroupID)
			require.Equal(t, ids[0], *key.GroupID)
			require.NotNil(t, key.Group)
			require.Equal(t, ids[0], key.Group.ID)
		})
	}
}

// 单个分组不需要路由表（主分组已经在 api_keys.group_id 上），回执仍要带分组。
func TestAPIKeyServiceCreateWithSingleGroupKeepsPrimaryGroup(t *testing.T) {
	svc, repo, groupRepo := newCreateRoutesService()
	ids := addOpenAIGroups(groupRepo, 1)

	key, err := svc.Create(context.Background(), 7, CreateAPIKeyRequest{Name: "one group", GroupIDs: ids})
	require.NoError(t, err)
	require.Nil(t, repo.replaced[42], "a single group is stored on api_keys.group_id only")
	require.NotNil(t, key.GroupID)
	require.Equal(t, ids[0], *key.GroupID)
}

// 权限边界：只要列表里有一个分组用户无权绑定，整次创建都要被拒绝，
// 不能部分落库，也不能悄悄跳过该分组后继续建 key。
func TestAPIKeyServiceCreateRejectsGroupNotBindableByUser(t *testing.T) {
	svc, repo, groupRepo := newCreateRoutesService()
	groupRepo.active = []Group{
		{ID: 1, Name: "public", Platform: "openai", Status: StatusActive},
		{ID: 2, Name: "exclusive", Platform: "openai", Status: StatusActive, IsExclusive: true},
	}
	for i := range groupRepo.active {
		groupRepo.byID[groupRepo.active[i].ID] = &groupRepo.active[i]
	}
	svc.userRepo = &userRepoStub{user: &User{ID: 7, RestrictPublicGroups: true, AllowedGroups: []int64{1}}}

	_, err := svc.Create(context.Background(), 7, CreateAPIKeyRequest{Name: "mixed", GroupIDs: []int64{1, 2}})
	require.ErrorIs(t, err, ErrGroupNotAllowed)
	require.Empty(t, repo.created, "a rejected create must not persist the key")
	require.Empty(t, repo.replaced)
}

func TestAPIKeyServiceCreateRejectsDuplicateGroupIDsBeforePersisting(t *testing.T) {
	svc, repo, groupRepo := newCreateRoutesService()
	ids := addOpenAIGroups(groupRepo, 2)

	_, err := svc.Create(context.Background(), 7, CreateAPIKeyRequest{Name: "dup", GroupIDs: []int64{ids[0], ids[0]}})
	require.Error(t, err)
	require.Empty(t, repo.created)
	require.Empty(t, repo.replaced)
}

// 事务边界：路由替换失败时，刚创建的密钥必须被补偿删除，
// 不能留下「有 key、没路由」的半截数据。
func TestAPIKeyServiceCreateRollsBackKeyWhenRouteReplaceFails(t *testing.T) {
	svc, repo, groupRepo := newCreateRoutesService()
	ids := addOpenAIGroups(groupRepo, 12)
	repo.replaceErr = errors.New(`pq: new row for relation "api_key_group_routes" violates check constraint "api_key_group_routes_position_check"`)

	_, err := svc.Create(context.Background(), 7, CreateAPIKeyRequest{Name: "Infinite Canvas", GroupIDs: ids})
	require.Error(t, err)
	require.Len(t, repo.created, 1)
	require.Equal(t, []int64{42}, repo.deletedIDs, "the created key must be rolled back after a route failure")
}
