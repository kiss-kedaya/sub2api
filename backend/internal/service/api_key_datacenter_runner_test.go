//go:build unit

package service

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/require"
)

// autoBlockRepoStub is a DB-free APIKeyRepository for the periodic
// datacenter-IP runner. It embeds the shared no-op stub so the interface stays
// satisfied without hand-writing every method.
type autoBlockRepoStub struct {
	quotaBaseAPIKeyRepoStub
	keys []APIKey
	// loadKeys, when set, is what GetByID returns instead of keys; it models a
	// concurrent edit between the listing and the per-key reload.
	loadKeys  []APIKey
	listErr   error
	loadErr   map[int64]error
	recent    map[int64][]string
	recentErr map[int64]error
	updateErr map[int64]error

	updatedKeys   []APIKey
	updatedFields []APIKeyUpdateFields
}

func (s *autoBlockRepoStub) ListAutoBlockDatacenterIPsKeys(_ context.Context, _ int) ([]APIKey, error) {
	if s.listErr != nil {
		return nil, s.listErr
	}
	out := make([]APIKey, len(s.keys))
	copy(out, s.keys)
	return out, nil
}

func (s *autoBlockRepoStub) GetByID(_ context.Context, id int64) (*APIKey, error) {
	if err := s.loadErr[id]; err != nil {
		return nil, err
	}
	source := s.keys
	if s.loadKeys != nil {
		source = s.loadKeys
	}
	for i := range source {
		if source[i].ID == id {
			clone := source[i]
			return &clone, nil
		}
	}
	return nil, ErrAPIKeyNotFound
}

func (s *autoBlockRepoStub) ListRecentIPAddresses(_ context.Context, apiKeyID int64, _ int) ([]string, error) {
	if err := s.recentErr[apiKeyID]; err != nil {
		return nil, err
	}
	out := append([]string(nil), s.recent[apiKeyID]...)
	return out, nil
}

func (s *autoBlockRepoStub) Update(_ context.Context, key *APIKey, fields APIKeyUpdateFields) error {
	if err := s.updateErr[key.ID]; err != nil {
		return err
	}
	clone := *key
	clone.IPBlacklist = append([]string(nil), key.IPBlacklist...)
	s.updatedKeys = append(s.updatedKeys, clone)
	s.updatedFields = append(s.updatedFields, fields)
	return nil
}

// newAutoBlockRunner wires a runner over the stub and a deterministic
// classifier, standing in for the third-party ip-api lookup.
func newAutoBlockRunner(repo *autoBlockRepoStub, classify datacenterIPClassifier) (*APIKeyDatacenterAutoBlockService, *APIKeyService) {
	svc := &APIKeyService{apiKeyRepo: repo, datacenterClassifier: classify}
	return NewAPIKeyDatacenterAutoBlockService(svc), svc
}

func TestAPIKeyDatacenterAutoBlockSkipsKeysWithSwitchOff(t *testing.T) {
	repo := &autoBlockRepoStub{
		keys:   []APIKey{{ID: 1, UserID: 7, AutoBlockDatacenterIPs: false}},
		recent: map[int64][]string{1: {"1.1.1.1"}},
	}
	classifyCalls := 0
	runner, _ := newAutoBlockRunner(repo, func(context.Context, []string) map[string]bool {
		classifyCalls++
		return map[string]bool{"1.1.1.1": true}
	})

	results, err := runner.RunDue(context.Background())
	require.NoError(t, err)
	require.Empty(t, results)
	require.Zero(t, classifyCalls)
	require.Empty(t, repo.updatedKeys)
}

// 列表返回后用户把开关关掉：runner 必须重新读取并放弃写入。
func TestAPIKeyDatacenterAutoBlockHonorsSwitchTurnedOffAfterListing(t *testing.T) {
	repo := &autoBlockRepoStub{
		keys:     []APIKey{{ID: 1, UserID: 7, AutoBlockDatacenterIPs: true}},
		loadKeys: []APIKey{{ID: 1, UserID: 7, AutoBlockDatacenterIPs: false}},
		recent:   map[int64][]string{1: {"1.1.1.1"}},
	}
	runner, _ := newAutoBlockRunner(repo, func(context.Context, []string) map[string]bool {
		return map[string]bool{"1.1.1.1": true}
	})

	results, err := runner.RunDue(context.Background())
	require.NoError(t, err)
	require.Len(t, results, 1)
	require.Empty(t, results[0].Added)
	require.Empty(t, repo.updatedKeys)
}

func TestAPIKeyDatacenterAutoBlockAppendsOnlyNewIPs(t *testing.T) {
	repo := &autoBlockRepoStub{
		keys: []APIKey{{
			ID:                     1,
			UserID:                 7,
			AutoBlockDatacenterIPs: true,
			IPBlacklist:            []string{"9.9.9.9"},
		}},
		recent: map[int64][]string{1: {"1.1.1.1", "9.9.9.9", "8.8.8.8"}},
	}
	runner, _ := newAutoBlockRunner(repo, func(_ context.Context, ips []string) map[string]bool {
		out := map[string]bool{}
		for _, ip := range ips {
			// 1.1.1.1 is new, 9.9.9.9 is already blocked, 8.8.8.8 is residential.
			if ip == "1.1.1.1" || ip == "9.9.9.9" {
				out[ip] = true
			}
		}
		return out
	})

	results, err := runner.RunDue(context.Background())
	require.NoError(t, err)
	require.Len(t, results, 1)
	require.Equal(t, int64(1), results[0].KeyID)
	require.Equal(t, 3, results[0].Checked)
	require.Equal(t, []string{"1.1.1.1"}, results[0].Added)
	require.Empty(t, results[0].Error)

	require.Len(t, repo.updatedKeys, 1)
	require.Equal(t, []string{"9.9.9.9", "1.1.1.1"}, repo.updatedKeys[0].IPBlacklist)
	require.Equal(t, []APIKeyUpdateFields{{IPRules: true}}, repo.updatedFields)
}

// 判定为空（第三方不可达或没有机房 IP）时不动黑名单，也不报错。
func TestAPIKeyDatacenterAutoBlockWritesNothingWhenNothingClassified(t *testing.T) {
	repo := &autoBlockRepoStub{
		keys: []APIKey{{
			ID:                     1,
			UserID:                 7,
			AutoBlockDatacenterIPs: true,
			IPBlacklist:            []string{"9.9.9.9"},
		}},
		recent: map[int64][]string{1: {"1.1.1.1", "2.2.2.2"}},
	}
	runner, _ := newAutoBlockRunner(repo, func(context.Context, []string) map[string]bool {
		return map[string]bool{}
	})

	results, err := runner.RunDue(context.Background())
	require.NoError(t, err)
	require.Len(t, results, 1)
	require.Equal(t, 2, results[0].Checked)
	require.Empty(t, results[0].Added)
	require.Empty(t, results[0].Error)
	require.Empty(t, repo.updatedKeys)
}

// 单个密钥失败（读取或写入）必须只影响它自己，后面的密钥照常处理。
func TestAPIKeyDatacenterAutoBlockContinuesAfterSingleKeyFailure(t *testing.T) {
	repo := &autoBlockRepoStub{
		keys: []APIKey{
			{ID: 1, UserID: 7, AutoBlockDatacenterIPs: true},
			{ID: 2, UserID: 7, AutoBlockDatacenterIPs: true},
			{ID: 3, UserID: 7, AutoBlockDatacenterIPs: true},
		},
		loadErr:   map[int64]error{1: errors.New("db read failed")},
		updateErr: map[int64]error{2: errors.New("db write failed")},
		recent: map[int64][]string{
			2: {"2.2.2.2"},
			3: {"3.3.3.3"},
		},
	}
	runner, _ := newAutoBlockRunner(repo, func(_ context.Context, ips []string) map[string]bool {
		out := map[string]bool{}
		for _, ip := range ips {
			out[ip] = true
		}
		return out
	})

	results, err := runner.RunDue(context.Background())
	require.NoError(t, err)
	require.Len(t, results, 3)
	require.Equal(t, "load_failed", results[0].Error)
	require.Equal(t, "append_failed", results[1].Error)
	require.Empty(t, results[2].Error)
	require.Equal(t, []string{"3.3.3.3"}, results[2].Added)

	require.Len(t, repo.updatedKeys, 1)
	require.Equal(t, int64(3), repo.updatedKeys[0].ID)
}

// 编辑接口必须把这个开关登记成待写列，否则保存后值不会落库。
func TestAPIKeyUpdate_DeclaresAutoBlockDatacenterIPsColumn(t *testing.T) {
	repo := &updateFieldsAPIKeyRepoStub{key: &APIKey{
		ID: 1, UserID: 7, Key: "sk-test", Status: StatusActive,
	}}
	svc := &APIKeyService{apiKeyRepo: repo}
	enabled := true

	_, err := svc.Update(context.Background(), 1, 7, UpdateAPIKeyRequest{AutoBlockDatacenterIPs: &enabled})
	require.NoError(t, err)
	require.Equal(t, []APIKeyUpdateFields{{AutoBlockDatacenterIPs: true}}, repo.updateFields)
}
