//go:build unit

package service_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

// RevokeUserSessionsForTwoFactorReset 是 #641 命令行与 #642 管理端接口共用的清理实现。
// 这里用记录型替身逐条钉住它的语义；与真实 Redis 的端到端行为由
// cmd/server 的命令行测试（miniredis）和 twofa_admin_reset_hardening_test 覆盖。

type cleanupRefreshCache struct {
	service.RefreshTokenCache
	hashes     []string
	hashesErr  error
	tokens     map[string]*service.RefreshTokenData
	tokenErrs  map[string]error
	deleteAll  []int64
	deleteOne  []string
	deleteErr  error
	deleteAllE error
	// 已签发 access token 的撤销记录（实现 service.AccessTokenRevocationStore）。
	accessRevoked []int64
	accessCutoff  []time.Time
	accessTTL     []time.Duration
	accessErr     error
}

func (c *cleanupRefreshCache) RevokeAccessTokensBefore(_ context.Context, userID int64, before time.Time, ttl time.Duration) error {
	c.accessRevoked = append(c.accessRevoked, userID)
	c.accessCutoff = append(c.accessCutoff, before)
	c.accessTTL = append(c.accessTTL, ttl)
	return c.accessErr
}

func (c *cleanupRefreshCache) AccessTokensRevokedBefore(context.Context, int64) (time.Time, error) {
	return time.Time{}, nil
}

func (c *cleanupRefreshCache) GetUserTokenHashes(context.Context, int64) ([]string, error) {
	return c.hashes, c.hashesErr
}

func (c *cleanupRefreshCache) GetRefreshToken(_ context.Context, hash string) (*service.RefreshTokenData, error) {
	if err := c.tokenErrs[hash]; err != nil {
		return nil, err
	}
	data, ok := c.tokens[hash]
	if !ok {
		return nil, service.ErrRefreshTokenNotFound
	}
	return data, nil
}

func (c *cleanupRefreshCache) DeleteRefreshToken(_ context.Context, hash string) error {
	c.deleteOne = append(c.deleteOne, hash)
	return c.deleteErr
}

func (c *cleanupRefreshCache) DeleteUserRefreshTokens(_ context.Context, userID int64) error {
	c.deleteAll = append(c.deleteAll, userID)
	return c.deleteAllE
}

type cleanupStepUp struct {
	calls       int
	sessionKeys []string
	err         error
}

func (s *cleanupStepUp) RevokeStepUpGrantsForUser(_ context.Context, _ int64, sessionKeys []string) (int64, error) {
	s.calls++
	s.sessionKeys = append([]string(nil), sessionKeys...)
	return 0, s.err
}

type cleanupState struct {
	setupDeleted, attemptsCleared int
	setupErr, attemptsErr         error
}

func (s *cleanupState) DeleteSetupSession(context.Context, int64) error {
	s.setupDeleted++
	return s.setupErr
}

func (s *cleanupState) ClearVerifyAttempts(context.Context, int64) error {
	s.attemptsCleared++
	return s.attemptsErr
}

func ownedToken(userID int64, family string) *service.RefreshTokenData {
	return &service.RefreshTokenData{UserID: userID, FamilyID: family}
}

func TestRevokeUserSessionsForTwoFactorResetRevokesEverythingForTheUser(t *testing.T) {
	refresh := &cleanupRefreshCache{
		hashes: []string{"a", "b", "c", "expired"},
		tokens: map[string]*service.RefreshTokenData{
			"a": ownedToken(7, "family-1"),
			"b": ownedToken(7, "family-2"),
			"c": ownedToken(7, "family-1"), // 同一会话族的轮换令牌，会话键只传一次
		},
	}
	stepUp, state := &cleanupStepUp{}, &cleanupState{}

	started := time.Now().UTC()
	require.NoError(t, service.RevokeUserSessionsForTwoFactorReset(context.Background(), 7, refresh, stepUp, state))

	require.Equal(t, []int64{7}, refresh.deleteAll, "a trustworthy index is dropped as a whole")
	require.Empty(t, refresh.deleteOne)
	require.Equal(t, 1, stepUp.calls)
	require.Equal(t, []string{"family-1", "family-2"}, stepUp.sessionKeys, "the legacy u<uid> key is added by the grant store itself")
	require.Equal(t, 1, state.setupDeleted)
	require.Equal(t, 1, state.attemptsCleared)

	// 已签发的 access JWT：撤销时间点是「此刻」（重置完成的时刻），保留期要长于 access token 的最长有效期。
	require.Equal(t, []int64{7}, refresh.accessRevoked)
	require.Len(t, refresh.accessCutoff, 1)
	require.False(t, refresh.accessCutoff[0].Before(started), "cutoff %s must not predate the call (%s)", refresh.accessCutoff[0], started)
	require.WithinDuration(t, time.Now().UTC(), refresh.accessCutoff[0], time.Minute)
	require.GreaterOrEqual(t, refresh.accessTTL[0], 24*time.Hour)
}

// 索引里混进了别的用户的令牌：只删能证明属于该用户的令牌，绝不整组删除，
// 也不替那个令牌的主人撤销授权；仍然报错，调用方据此提示「没清干净」。
func TestRevokeUserSessionsForTwoFactorResetNeverTrustsAForeignIndexEntry(t *testing.T) {
	refresh := &cleanupRefreshCache{
		hashes: []string{"own", "foreign"},
		tokens: map[string]*service.RefreshTokenData{
			"own":     ownedToken(7, "family-own"),
			"foreign": ownedToken(8, "family-foreign"),
		},
	}
	stepUp, state := &cleanupStepUp{}, &cleanupState{}

	err := service.RevokeUserSessionsForTwoFactorReset(context.Background(), 7, refresh, stepUp, state)
	require.Error(t, err)

	require.Empty(t, refresh.deleteAll, "a polluted index must not authorize deleting the whole user set")
	require.Equal(t, []string{"own"}, refresh.deleteOne)
	require.Equal(t, []string{"family-own"}, stepUp.sessionKeys, "the other user's session must not be revoked")
	require.Equal(t, 1, state.setupDeleted)
	require.Equal(t, 1, state.attemptsCleared)
	// access token 的撤销只按用户 ID 记一个时间点，不依赖会话索引，也不会波及别的用户。
	require.Equal(t, []int64{7}, refresh.accessRevoked)
}

// 读不到索引/令牌：没法证明归属，一个令牌都不删，但授权与预算照常清理，错误如实返回。
func TestRevokeUserSessionsForTwoFactorResetLookupFailureStillClearsGrantsAndBudget(t *testing.T) {
	lookup := errors.New("redis unavailable")

	indexFails := &cleanupRefreshCache{hashesErr: lookup}
	stepUp, state := &cleanupStepUp{}, &cleanupState{}
	err := service.RevokeUserSessionsForTwoFactorReset(context.Background(), 7, indexFails, stepUp, state)
	require.ErrorIs(t, err, lookup)
	require.Empty(t, indexFails.deleteAll)
	require.Empty(t, indexFails.deleteOne)
	require.Equal(t, 1, stepUp.calls)
	require.Empty(t, stepUp.sessionKeys)
	require.Equal(t, 1, state.setupDeleted)
	require.Equal(t, 1, state.attemptsCleared)

	tokenFails := &cleanupRefreshCache{
		hashes:    []string{"ok", "broken"},
		tokens:    map[string]*service.RefreshTokenData{"ok": ownedToken(7, "family-ok")},
		tokenErrs: map[string]error{"broken": lookup},
	}
	stepUp = &cleanupStepUp{}
	err = service.RevokeUserSessionsForTwoFactorReset(context.Background(), 7, tokenFails, stepUp, &cleanupState{})
	require.ErrorIs(t, err, lookup)
	require.Empty(t, tokenFails.deleteAll)
	require.Equal(t, []string{"ok"}, tokenFails.deleteOne)
	require.Equal(t, []string{"family-ok"}, stepUp.sessionKeys)
}

// 任何一步失败都不能挡住后面的步骤，所有错误合并返回。
func TestRevokeUserSessionsForTwoFactorResetAttemptsEveryStepAndJoinsErrors(t *testing.T) {
	errAll, errAccess, errStepUp, errSetup, errAttempts := errors.New("delete all"), errors.New("access"), errors.New("step-up"), errors.New("setup"), errors.New("attempts")
	refresh := &cleanupRefreshCache{
		hashes:     []string{"a"},
		tokens:     map[string]*service.RefreshTokenData{"a": ownedToken(7, "family-1")},
		deleteAllE: errAll,
		accessErr:  errAccess,
	}
	stepUp := &cleanupStepUp{err: errStepUp}
	state := &cleanupState{setupErr: errSetup, attemptsErr: errAttempts}

	err := service.RevokeUserSessionsForTwoFactorReset(context.Background(), 7, refresh, stepUp, state)
	for _, want := range []error{errAll, errAccess, errStepUp, errSetup, errAttempts} {
		require.ErrorIs(t, err, want)
	}
	require.Equal(t, []int64{7}, refresh.deleteAll)
	require.Equal(t, []int64{7}, refresh.accessRevoked)
	require.Equal(t, 1, stepUp.calls)
	require.Equal(t, 1, state.setupDeleted)
	require.Equal(t, 1, state.attemptsCleared)
}

// refresh token 缓存不支持撤销 access token（只暴露 RefreshTokenCache 接口本身）：如实报错而不是谎报「已撤销」，
// 其余步骤照做。
func TestRevokeUserSessionsForTwoFactorResetReportsUnsupportedAccessTokenRevocation(t *testing.T) {
	refresh := &cleanupRefreshCache{}
	stepUp, state := &cleanupStepUp{}, &cleanupState{}

	err := service.RevokeUserSessionsForTwoFactorReset(context.Background(), 7, struct{ service.RefreshTokenCache }{refresh}, stepUp, state)
	require.Error(t, err)
	require.Contains(t, err.Error(), "access token revocation unavailable")
	require.Equal(t, []int64{7}, refresh.deleteAll, "the remaining steps still run")
	require.Empty(t, refresh.accessRevoked)
	require.Equal(t, 1, stepUp.calls)
	require.Equal(t, 1, state.setupDeleted)
	require.Equal(t, 1, state.attemptsCleared)
}

// 缺依赖时如实报错而不是 nil 指针崩溃，其余步骤照做。
func TestRevokeUserSessionsForTwoFactorResetReportsMissingStores(t *testing.T) {
	ctx := context.Background()

	stepUp, state := &cleanupStepUp{}, &cleanupState{}
	require.Error(t, service.RevokeUserSessionsForTwoFactorReset(ctx, 7, nil, stepUp, state))
	require.Equal(t, 1, stepUp.calls)
	require.Equal(t, 1, state.attemptsCleared)

	refresh := &cleanupRefreshCache{}
	state = &cleanupState{}
	require.Error(t, service.RevokeUserSessionsForTwoFactorReset(ctx, 7, refresh, nil, state))
	require.Equal(t, []int64{7}, refresh.deleteAll)
	require.Equal(t, 1, state.setupDeleted)

	stepUp = &cleanupStepUp{}
	require.Error(t, service.RevokeUserSessionsForTwoFactorReset(ctx, 7, refresh, stepUp, nil))
	require.Equal(t, 1, stepUp.calls)

	require.Error(t, service.RevokeUserSessionsForTwoFactorReset(ctx, 0, refresh, stepUp, state), "invalid user id")
}
