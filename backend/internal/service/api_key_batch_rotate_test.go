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

// batchRotateRepoStub 只实现批量轮换用到的仓库方法；其余接口方法由嵌入的
// nil 接口提供（被意外调用会 panic，测试立刻暴露路径变化）。
type batchRotateRepoStub struct {
	APIKeyRepository

	keys      map[int64]*APIKey
	owners    map[int64]int64
	updateErr map[int64]error
	updated   []APIKey
	// updatedFieldKey 记录哪些 Key 走了「轮换凭据」的写入路径（fields.Key），
	// 真实仓库在该路径里同一事务清空明文列、更新哈希并删除封存密文。
	updatedFieldKey []int64
}

func (s *batchRotateRepoStub) GetByID(_ context.Context, id int64) (*APIKey, error) {
	key, ok := s.keys[id]
	if !ok {
		return nil, ErrAPIKeyNotFound
	}
	clone := *key
	return &clone, nil
}

func (s *batchRotateRepoStub) ExistsByKey(context.Context, string) (bool, error) {
	return false, nil
}

func (s *batchRotateRepoStub) VerifyOwnership(_ context.Context, userID int64, ids []int64) ([]int64, error) {
	out := make([]int64, 0, len(ids))
	for _, id := range ids {
		if owner, ok := s.owners[id]; ok && owner == userID {
			out = append(out, id)
		}
	}
	return out, nil
}

func (s *batchRotateRepoStub) Update(_ context.Context, key *APIKey, fields APIKeyUpdateFields) error {
	if err := s.updateErr[key.ID]; err != nil {
		return err
	}
	if fields.Key {
		// 与真实仓库同语义：按新明文重算哈希/前缀；DB 侧会清空明文列，
		// 但仓库不回写内存对象的 Key（轮换回执还要用）。
		key.KeyHash = HashAPIKey(key.Key)
		key.KeyPrefix = APIKeyPrefixFromPlaintext(key.Key)
		s.updatedFieldKey = append(s.updatedFieldKey, key.ID)
	}
	s.updated = append(s.updated, *key)
	return nil
}

func newBatchRotateService(repo *batchRotateRepoStub) *APIKeyService {
	return &APIKeyService{
		apiKeyRepo: repo,
		cfg:        &config.Config{},
	}
}

func newBatchRotateFixture() *batchRotateRepoStub {
	return &batchRotateRepoStub{
		keys: map[int64]*APIKey{
			1: {ID: 1, UserID: 7, KeyHash: HashAPIKey("sk-old-one"), KeyPrefix: "sk-old-o", Status: StatusActive},
			2: {ID: 2, UserID: 7, KeyHash: HashAPIKey("sk-old-two"), KeyPrefix: "sk-old-t", Status: StatusActive},
			3: {ID: 3, UserID: 99, KeyHash: HashAPIKey("sk-other"), KeyPrefix: "sk-other", Status: StatusActive},
		},
		owners:    map[int64]int64{1: 7, 2: 7, 3: 99},
		updateErr: map[int64]error{},
	}
}

func TestBatchRotateRejectsForeignIDWith404AndNoSideEffects(t *testing.T) {
	repo := newBatchRotateFixture()
	svc := newBatchRotateService(repo)

	results, err := svc.BatchRotate(context.Background(), 7, []int64{1, 3})
	require.ErrorIs(t, err, ErrAPIKeyNotFound)
	require.Nil(t, results)
	require.Empty(t, repo.updated, "a foreign id must abort the whole batch before any rotation")
}

func TestBatchRotateRejectsUnknownIDWith404(t *testing.T) {
	repo := newBatchRotateFixture()
	svc := newBatchRotateService(repo)

	_, err := svc.BatchRotate(context.Background(), 7, []int64{1, 404})
	require.ErrorIs(t, err, ErrAPIKeyNotFound)
	require.Empty(t, repo.updated)
}

func TestBatchRotateEnforcesLimit(t *testing.T) {
	repo := newBatchRotateFixture()
	svc := newBatchRotateService(repo)

	ids := make([]int64, 0, MaxBatchRotateAPIKeys+1)
	for i := int64(1); i <= MaxBatchRotateAPIKeys+1; i++ {
		ids = append(ids, i)
	}
	_, err := svc.BatchRotate(context.Background(), 7, ids)
	require.Error(t, err)
	require.Equal(t, "API_KEY_BATCH_ROTATE_TOO_MANY", infraerrors.Reason(err))
	require.Empty(t, repo.updated)
}

func TestBatchRotateRotatesOwnedKeysAndReportsPerItemFailure(t *testing.T) {
	repo := newBatchRotateFixture()
	repo.updateErr[2] = errors.New("database is on fire")
	svc := newBatchRotateService(repo)

	results, err := svc.BatchRotate(context.Background(), 7, []int64{1, 2, 1})
	require.NoError(t, err)
	require.Len(t, results, 2, "duplicate ids are collapsed")

	require.Equal(t, int64(1), results[0].ID)
	require.NoError(t, results[0].Err)
	require.NotEmpty(t, results[0].Key)
	require.NotEqual(t, "sk-old-one", results[0].Key)

	require.Equal(t, int64(2), results[1].ID)
	require.Error(t, results[1].Err)
	require.Empty(t, results[1].Key)

	// 成功的 Key 用新哈希/前缀落库；失败的 Key 没有写入。
	require.Len(t, repo.updated, 1)
	require.Equal(t, int64(1), repo.updated[0].ID)
	require.Equal(t, HashAPIKey(results[0].Key), repo.updated[0].KeyHash)
	require.Equal(t, APIKeyPrefixFromPlaintext(results[0].Key), repo.updated[0].KeyPrefix)
	require.Equal(t, []int64{1}, repo.updatedFieldKey, "rotation must use the credential-replacement write path")
}

func TestBatchRotateRejectsEmptyAndInvalidIDs(t *testing.T) {
	repo := newBatchRotateFixture()
	svc := newBatchRotateService(repo)

	_, err := svc.BatchRotate(context.Background(), 7, nil)
	require.Error(t, err)
	require.Equal(t, "API_KEY_BATCH_ROTATE_EMPTY", infraerrors.Reason(err))

	_, err = svc.BatchRotate(context.Background(), 7, []int64{0})
	require.Error(t, err)
	require.Equal(t, "API_KEY_BATCH_ROTATE_INVALID_ID", infraerrors.Reason(err))
	require.Empty(t, repo.updated)
}
