package main

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/repository"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/require"
)

// stepUpGrantFactors 故意写成字面量而不是 service.AllFactors()：
// 这里要独立证明 totp 与 passkey 两个因子的授权都被撤销。
var stepUpGrantFactors = []service.Factor{service.FactorTOTP, service.FactorPasskey}

// 目标用户 41：两个带 family 的会话，外加没有会话 ID 的旧 token（u41）；
// 用户 42 是旁观者，它的授权必须原样保留。
var stepUpGrantSessions = []struct {
	userID  int64
	session string
}{{41, "family-a"}, {41, "family-b"}, {41, "u41"}, {42, "family-other"}}

// seedStepUpGrants 通过真实的写入路径（SetStepUpGrant）给每个会话 × 每个因子写一条授权，
// 不手写 Redis 键，这样键布局以后再变，测试仍然按真实布局验证。
func seedStepUpGrants(t *testing.T, rdb *redis.Client) (service.RefreshTokenCache, service.TotpCache) {
	t.Helper()
	ctx := context.Background()
	refreshCache := repository.NewRefreshTokenCache(rdb)
	totpCache := repository.NewTotpCache(rdb)
	for _, session := range []struct {
		userID       int64
		hash, family string
	}{{41, "own-a", "family-a"}, {41, "own-b", "family-b"}, {42, "other", "family-other"}} {
		require.NoError(t, refreshCache.StoreRefreshToken(ctx, session.hash, &service.RefreshTokenData{UserID: session.userID, FamilyID: session.family}, time.Hour))
		require.NoError(t, refreshCache.AddToUserTokenSet(ctx, session.userID, session.hash, time.Hour))
	}
	for _, grant := range stepUpGrantSessions {
		for _, factor := range stepUpGrantFactors {
			require.NoError(t, totpCache.SetStepUpGrant(ctx, grant.userID, grant.session, factor, service.StepUpGrantTTL))
			granted, err := totpCache.HasStepUpGrant(ctx, grant.userID, grant.session, factor)
			require.NoError(t, err)
			require.True(t, granted, "seeded grant must exist: user=%d session=%s factor=%s", grant.userID, grant.session, factor)
		}
	}
	return refreshCache, totpCache
}

// countRemainingTargetGrants 返回用户 41 仍然有效的 step-up 授权条数。
func countRemainingTargetGrants(t *testing.T, totpCache service.TotpCache) int {
	t.Helper()
	remaining := 0
	for _, grant := range stepUpGrantSessions {
		if grant.userID != 41 {
			continue
		}
		for _, factor := range stepUpGrantFactors {
			granted, err := totpCache.HasStepUpGrant(context.Background(), grant.userID, grant.session, factor)
			require.NoError(t, err)
			if granted {
				remaining++
			}
		}
	}
	return remaining
}

// #561 之后 step-up 授权的键带因子维度（totp:stepup:<uid>:<factor>:<session>）。
// 重置 2FA 必须撤销每个因子、每个会话（family 与遗留 u<uid>）的授权，且不碰其他用户。
func TestAdmin2FAResetRevokesStepUpGrantsOfEveryFactor(t *testing.T) {
	server := miniredis.RunT(t)
	rdb := redis.NewClient(&redis.Options{Addr: server.Addr()})
	defer func() { _ = rdb.Close() }()
	refreshCache, totpCache := seedStepUpGrants(t, rdb)

	require.NoError(t, clearAdmin2FAResetSessionsWithCache(context.Background(), refreshCache, rdb, 41))

	for _, grant := range stepUpGrantSessions {
		for _, factor := range stepUpGrantFactors {
			granted, err := totpCache.HasStepUpGrant(context.Background(), grant.userID, grant.session, factor)
			require.NoError(t, err)
			require.Equal(t, grant.userID != 41, granted, "user=%d session=%s factor=%s", grant.userID, grant.session, factor)
		}
	}
}

// failDelHook 让所有 DEL 失败，用来对真实清理函数（而不是桩）验证失败路径。
type failDelHook struct{}

func (failDelHook) DialHook(next redis.DialHook) redis.DialHook { return next }
func (failDelHook) ProcessHook(next redis.ProcessHook) redis.ProcessHook {
	return func(ctx context.Context, cmd redis.Cmder) error {
		if strings.EqualFold(cmd.Name(), "del") {
			return errors.New("injected DEL failure")
		}
		return next(ctx, cmd)
	}
}
func (failDelHook) ProcessPipelineHook(next redis.ProcessPipelineHook) redis.ProcessPipelineHook {
	return next
}

// 命令行输出必须与实际撤销结果一致：打印「已撤销」当且仅当授权真的删掉了；
// 删除失败时只能打印警告，并且授权确实还在（不能谎报）。
func TestExecuteAdmin2FAResetOutputMatchesActualStepUpRevocation(t *testing.T) {
	for _, failDelete := range []bool{false, true} {
		name := map[bool]string{false: "deleted", true: "delete_fails"}[failDelete]
		t.Run(name, func(t *testing.T) {
			server := miniredis.RunT(t)
			rdb := redis.NewClient(&redis.Options{Addr: server.Addr()})
			defer func() { _ = rdb.Close() }()
			refreshCache, totpCache := seedStepUpGrants(t, rdb)

			// 只让授权键的 DEL 失败：刷新令牌走健康的客户端，隔离出被测路径。
			cleanupClient := rdb
			if failDelete {
				cleanupClient = redis.NewClient(&redis.Options{Addr: server.Addr()})
				cleanupClient.AddHook(failDelHook{})
				defer func() { _ = cleanupClient.Close() }()
			}

			db, mock, err := sqlmock.New(sqlmock.QueryMatcherOption(sqlmock.QueryMatcherEqual))
			require.NoError(t, err)
			mock.ExpectBegin()
			mock.ExpectQuery(admin2FAResetSQL).WithArgs(int64(41)).WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow(int64(41)))
			mock.ExpectCommit()
			mock.ExpectClose()

			var out bytes.Buffer
			err = executeAdmin2FAReset(context.Background(), 41, false, &out,
				func() (*config.Config, error) { return &config.Config{}, nil },
				func(*config.Config) (*sql.DB, error) { return db, nil },
				func(ctx context.Context, _ *config.Config, userID int64) error {
					return clearAdmin2FAResetSessionsWithCache(ctx, refreshCache, cleanupClient, userID)
				})
			require.NoError(t, err)
			require.NoError(t, mock.ExpectationsWereMet())

			claimedRevoked := strings.Contains(out.String(), "step-up grants have been revoked")
			remaining := countRemainingTargetGrants(t, totpCache)
			require.Equal(t, !failDelete, claimedRevoked, out.String())
			require.Equal(t, failDelete, remaining > 0, "claimedRevoked=%t remaining=%d", claimedRevoked, remaining)
			if failDelete {
				require.Contains(t, out.String(), "some sessions may remain active")
				require.NotContains(t, out.String(), "injected DEL failure", "cleanup errors must not leak into the operator output")
			}
		})
	}
}
