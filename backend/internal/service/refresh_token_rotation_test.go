//go:build unit

package service_test

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"log/slog"
	"regexp"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/repository" //nolint:depguard // 用真实的 Redis 实现验证 TTL / JSON / 墓碑语义
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9" //nolint:depguard // 同上：需要真实 refresh token 存储
	"github.com/stretchr/testify/require"
)

// refresh token 轮换的服务层测试：跑在 miniredis 上，用的是生产的 Redis 实现（TTL、JSON 编码、
// 家族集合都是真的），时间用「改写 Redis 里的记录」来推进，不 sleep。全部邮箱 / 令牌都是假数据。

const (
	rotIPv4A          = "203.0.113.10"
	rotIPv4SameNet    = "203.0.113.200"
	rotIPv4B          = "198.51.100.77"
	rotIPv6           = "2001:db8:abcd:12::1"
	rotUAChrome130Win = "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/130.0.0.0 Safari/537.36"
	rotUAChrome131Win = "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/131.0.0.0 Safari/537.36"
	rotUAFirefoxLinux = "Mozilla/5.0 (X11; Linux x86_64; rv:132.0) Gecko/20100101 Firefox/132.0"
)

type rotFixture struct {
	t     *testing.T
	mini  *miniredis.Miniredis
	repo  *emailBindUserRepoStub
	cache service.RefreshTokenCache
	cfg   *config.Config
	svc   *service.AuthService
	user  *service.User
}

// newRotFixture 构造认证服务：每枚令牌滑动 14 天、会话上限 90 天、宽限取配置默认值（30 秒）、
// 绑定 loose + rebind（都是新的生产默认值）。mutate 改配置，wrap 给 Redis 存储套一层故障注入
// （包装器要自己转发 service.RefreshTokenAtomicStore，否则服务层看不到原子能力，会关掉宽限）。
func newRotFixture(t *testing.T, mutate func(*config.Config), wrap func(service.RefreshTokenCache) service.RefreshTokenCache) *rotFixture {
	t.Helper()
	mini := miniredis.RunT(t)
	rdb := redis.NewClient(&redis.Options{Addr: mini.Addr()})
	t.Cleanup(func() { _ = rdb.Close() })

	var cache service.RefreshTokenCache = repository.NewRefreshTokenCache(rdb)
	if wrap != nil {
		cache = wrap(cache)
	}
	user := &service.User{
		ID:           501,
		Email:        "rotation@example.com",
		PasswordHash: "hash-v1",
		Role:         service.RoleUser,
		Status:       service.StatusActive,
		Concurrency:  1,
	}
	repo := newEmailBindUserRepoStub(user)
	cfg := &config.Config{
		JWT: config.JWTConfig{
			Secret:                         "test-rotation-secret-0123456789abcdef",
			ExpireHour:                     24,
			AccessTokenExpireMinutes:       60,
			RefreshTokenExpireDays:         14,
			SessionMaxLifetimeDays:         90,
			RefreshTokenReuseLeewaySeconds: config.DefaultRefreshTokenReuseLeewaySeconds,
		},
		Security: config.SecurityConfig{
			SessionBindingMode:       config.SessionBindingModeLoose,
			SessionBindingOnMismatch: config.SessionBindingOnMismatchRebind,
		},
	}
	if mutate != nil {
		mutate(cfg)
	}
	svc := service.NewAuthService(nil, repo, nil, cache, cfg, nil, nil, nil, nil, nil, nil, nil, nil)
	return &rotFixture{t: t, mini: mini, repo: repo, cache: cache, cfg: cfg, svc: svc, user: user}
}

func rotCtx(ip, ua string) context.Context {
	return service.WithSessionBinding(context.Background(), &service.SessionBinding{IP: ip, UserAgent: ua})
}

func (f *rotFixture) login(ip, ua string) *service.TokenPair {
	f.t.Helper()
	pair, err := f.svc.GenerateTokenPair(rotCtx(ip, ua), f.user, "")
	require.NoError(f.t, err)
	return pair
}

func (f *rotFixture) refresh(ip, ua, refreshToken string) (*service.TokenPairWithUser, error) {
	return f.svc.RefreshTokenPair(rotCtx(ip, ua), refreshToken)
}

func rotRecordKey(refreshToken string) string {
	sum := sha256.Sum256([]byte(refreshToken))
	return "refresh_token:" + hex.EncodeToString(sum[:])
}

// record 读出令牌在 Redis 里的记录；不存在返回 nil。
func (f *rotFixture) record(refreshToken string) *service.RefreshTokenData {
	f.t.Helper()
	raw, err := f.mini.Get(rotRecordKey(refreshToken))
	if err != nil {
		return nil
	}
	var data service.RefreshTokenData
	require.NoError(f.t, json.Unmarshal([]byte(raw), &data))
	return &data
}

// rewrite 改写令牌记录（保持剩余 TTL），用来模拟时间流逝或构造旧格式记录。
func (f *rotFixture) rewrite(refreshToken string, mutate func(map[string]any)) {
	f.t.Helper()
	key := rotRecordKey(refreshToken)
	raw, err := f.mini.Get(key)
	require.NoError(f.t, err, "record must exist before it can be rewritten")
	// token_version 是接近 2^63 的整数：用 json.Number 保持精度，否则转成 float64 会让它和用户对不上。
	fields := map[string]any{}
	dec := json.NewDecoder(strings.NewReader(raw))
	dec.UseNumber()
	require.NoError(f.t, dec.Decode(&fields))
	mutate(fields)
	out, err := json.Marshal(fields)
	require.NoError(f.t, err)
	ttl := f.mini.TTL(key)
	require.NoError(f.t, f.mini.Set(key, string(out)))
	if ttl > 0 {
		f.mini.SetTTL(key, ttl)
	}
}

func rotTime(t time.Time) string { return t.UTC().Format(time.RFC3339Nano) }

// ageConsumedAt 把墓碑的消费时间往前拨 d，模拟「消费之后已经过了 d」。
func (f *rotFixture) ageConsumedAt(refreshToken string, d time.Duration) {
	f.t.Helper()
	f.rewrite(refreshToken, func(m map[string]any) {
		consumedAt, err := time.Parse(time.RFC3339Nano, m["consumed_at"].(string))
		require.NoError(f.t, err)
		m["consumed_at"] = rotTime(consumedAt.Add(-d))
	})
}

func (f *rotFixture) familyMembers(familyID string) []string {
	f.t.Helper()
	members, err := f.mini.Members("token_family:" + familyID)
	if err != nil {
		return nil
	}
	return members
}

// liveRefreshRecords 返回 Redis 里 refresh_token:* 记录（含墓碑）的数量。
func (f *rotFixture) liveRefreshRecords() int {
	n := 0
	for _, key := range f.mini.Keys() {
		if strings.HasPrefix(key, "refresh_token:") {
			n++
		}
	}
	return n
}

// requireSessionRevoked 断言整个会话已被撤销：所给令牌都查不到了。
func (f *rotFixture) requireSessionRevoked(tokens ...string) {
	f.t.Helper()
	for _, token := range tokens {
		_, err := f.refresh(rotIPv4A, rotUAChrome130Win, token)
		require.ErrorIs(f.t, err, service.ErrRefreshTokenInvalid, "token must be gone after the family was revoked")
	}
}

// ---------------------------------------------------------------------------
// 重试容错（leeway）
// ---------------------------------------------------------------------------

// 响应丢失后的重试：宽限内再次出示已轮换的旧令牌，不撤销会话、再发一对新令牌；
// 第一次轮换签发的新令牌仍然有效，客户端不管存了哪一枚都能继续用。
func TestRefreshRotationLeewayRetryReissuesWithoutRevokingFamily(t *testing.T) {
	f := newRotFixture(t, nil, nil)
	pair0 := f.login(rotIPv4A, rotUAChrome130Win)

	first, err := f.refresh(rotIPv4A, rotUAChrome130Win, pair0.RefreshToken)
	require.NoError(t, err)

	retry, err := f.refresh(rotIPv4A, rotUAChrome130Win, pair0.RefreshToken)
	require.NoError(t, err, "a retry inside the leeway window is a legitimate retry, not a replay")
	require.NotEqual(t, first.RefreshToken, retry.RefreshToken)

	firstRec, retryRec := f.record(first.RefreshToken), f.record(retry.RefreshToken)
	require.NotNil(t, firstRec)
	require.NotNil(t, retryRec)
	require.Equal(t, firstRec.FamilyID, retryRec.FamilyID, "same family")
	require.True(t, firstRec.SessionStartedAt.Equal(retryRec.SessionStartedAt), "same session start: the absolute cap must not move")
	require.False(t, firstRec.Consumed)
	require.False(t, retryRec.Consumed)

	// 客户端不管存的是第一次轮换发的，还是重试发的，都能继续用。
	next1, err := f.refresh(rotIPv4A, rotUAChrome130Win, first.RefreshToken)
	require.NoError(t, err, "the token issued by the first rotation must stay valid")
	_, err = f.refresh(rotIPv4A, rotUAChrome130Win, retry.RefreshToken)
	require.NoError(t, err, "the token issued by the retry must be valid too")
	_, err = f.refresh(rotIPv4A, rotUAChrome130Win, next1.RefreshToken)
	require.NoError(t, err)

	tomb := f.record(pair0.RefreshToken)
	require.NotNil(t, tomb)
	require.True(t, tomb.Consumed)
	require.Equal(t, 1, tomb.LeewayReissues, "the reissue count lives on the tombstone")

	stats := f.svc.AuthStatsSnapshot()
	require.EqualValues(t, 1, stats["refresh_reuse_leeway_retry"])
	require.EqualValues(t, 5, stats["refresh_ok"])
	require.Zero(t, stats["refresh_reuse"])
}

// 宽限内的重试不延长宽限窗口：窗口从「消费」那一刻起算，重试不会让它重新计时。
func TestRefreshRotationLeewayWindowIsAnchoredAtConsumeTime(t *testing.T) {
	f := newRotFixture(t, nil, nil)
	leeway := time.Duration(config.DefaultRefreshTokenReuseLeewaySeconds) * time.Second
	pair0 := f.login(rotIPv4A, rotUAChrome130Win)
	_, err := f.refresh(rotIPv4A, rotUAChrome130Win, pair0.RefreshToken)
	require.NoError(t, err)
	consumedAt := f.record(pair0.RefreshToken).ConsumedAt
	require.False(t, consumedAt.IsZero(), "rotation must stamp ConsumedAt on the tombstone")

	f.ageConsumedAt(pair0.RefreshToken, leeway-time.Second)
	_, err = f.refresh(rotIPv4A, rotUAChrome130Win, pair0.RefreshToken)
	require.NoError(t, err, "1s before the end of the leeway window is still inside it")
	after := f.record(pair0.RefreshToken)
	require.True(t, after.ConsumedAt.Equal(consumedAt.Add(-(leeway - time.Second))), "a retry must not move ConsumedAt")

	f.ageConsumedAt(pair0.RefreshToken, 2*time.Second) // 现在是窗口结束后 1 秒
	_, err = f.refresh(rotIPv4A, rotUAChrome130Win, pair0.RefreshToken)
	require.ErrorIs(t, err, service.ErrRefreshTokenReuse, "1s after the end of the leeway window is a replay")
}

// 超过宽限的重放仍然撤销整个家族（安全底线）：重放者拿到的、受害者手里的新令牌都作废。
func TestRefreshRotationReplayAfterLeewayRevokesFamily(t *testing.T) {
	f := newRotFixture(t, nil, nil)
	pair0 := f.login(rotIPv4A, rotUAChrome130Win)
	first, err := f.refresh(rotIPv4A, rotUAChrome130Win, pair0.RefreshToken)
	require.NoError(t, err)
	retry, err := f.refresh(rotIPv4A, rotUAChrome130Win, pair0.RefreshToken)
	require.NoError(t, err)

	f.ageConsumedAt(pair0.RefreshToken, 2*time.Minute)

	_, err = f.refresh(rotIPv4A, rotUAChrome130Win, pair0.RefreshToken)
	require.ErrorIs(t, err, service.ErrRefreshTokenReuse)
	f.requireSessionRevoked(first.RefreshToken, retry.RefreshToken)
	require.EqualValues(t, 1, f.svc.AuthStatsSnapshot()["refresh_reuse"])
}

// 宽限设为 0 = 关闭：回到旧行为，任何重放（包括紧接着的重试）都撤销整个家族。
func TestRefreshRotationLeewayDisabledRevokesOnAnyReplay(t *testing.T) {
	f := newRotFixture(t, func(c *config.Config) { c.JWT.RefreshTokenReuseLeewaySeconds = 0 }, nil)
	pair0 := f.login(rotIPv4A, rotUAChrome130Win)
	first, err := f.refresh(rotIPv4A, rotUAChrome130Win, pair0.RefreshToken)
	require.NoError(t, err)

	_, err = f.refresh(rotIPv4A, rotUAChrome130Win, pair0.RefreshToken)
	require.ErrorIs(t, err, service.ErrRefreshTokenReuse)
	f.requireSessionRevoked(first.RefreshToken)
}

// 旧墓碑（升级前写的）没有 ConsumedAt：按原行为处理，宽限开着也撤销整个家族。
func TestRefreshRotationLegacyTombstoneWithoutConsumedAtRevokesFamily(t *testing.T) {
	f := newRotFixture(t, nil, nil)
	pair0 := f.login(rotIPv4A, rotUAChrome130Win)
	first, err := f.refresh(rotIPv4A, rotUAChrome130Win, pair0.RefreshToken)
	require.NoError(t, err)

	f.rewrite(pair0.RefreshToken, func(m map[string]any) {
		delete(m, "consumed_at")
		delete(m, "leeway_reissues")
	})

	_, err = f.refresh(rotIPv4A, rotUAChrome130Win, pair0.RefreshToken)
	require.ErrorIs(t, err, service.ErrRefreshTokenReuse)
	f.requireSessionRevoked(first.RefreshToken)
}

// 每个已消费令牌的宽限重发有上限（3 次，计数写在墓碑上）；超出按重放处理并撤销家族。
func TestRefreshRotationLeewayReissueIsCapped(t *testing.T) {
	f := newRotFixture(t, nil, nil)
	pair0 := f.login(rotIPv4A, rotUAChrome130Win)
	first, err := f.refresh(rotIPv4A, rotUAChrome130Win, pair0.RefreshToken)
	require.NoError(t, err)

	tokens := []string{first.RefreshToken}
	for i := 1; i <= 3; i++ {
		retry, retryErr := f.refresh(rotIPv4A, rotUAChrome130Win, pair0.RefreshToken)
		require.NoError(t, retryErr, "reissue %d of 3 must be allowed", i)
		tokens = append(tokens, retry.RefreshToken)
		require.Equal(t, i, f.record(pair0.RefreshToken).LeewayReissues)
	}

	_, err = f.refresh(rotIPv4A, rotUAChrome130Win, pair0.RefreshToken)
	require.ErrorIs(t, err, service.ErrRefreshTokenReuse, "the 4th reissue from one consumed token is a replay")
	f.requireSessionRevoked(tokens...)
}

// 宽限路径不能绕过任何校验：密码改了 / 账号被禁用 / 令牌已过期，宽限内的重试照样被拒。
func TestRefreshRotationLeewayRetryStillChecksUserAndExpiry(t *testing.T) {
	t.Run("password changed", func(t *testing.T) {
		f := newRotFixture(t, nil, nil)
		pair0 := f.login(rotIPv4A, rotUAChrome130Win)
		first, err := f.refresh(rotIPv4A, rotUAChrome130Win, pair0.RefreshToken)
		require.NoError(t, err)

		changed := *f.user
		changed.PasswordHash = "hash-v2"
		require.NoError(t, f.repo.Update(context.Background(), &changed, service.UserUpdateFields{PasswordHash: true}))

		_, err = f.refresh(rotIPv4A, rotUAChrome130Win, pair0.RefreshToken)
		require.ErrorIs(t, err, service.ErrTokenRevoked)
		f.requireSessionRevoked(first.RefreshToken)
	})
	t.Run("user disabled", func(t *testing.T) {
		f := newRotFixture(t, nil, nil)
		pair0 := f.login(rotIPv4A, rotUAChrome130Win)
		_, err := f.refresh(rotIPv4A, rotUAChrome130Win, pair0.RefreshToken)
		require.NoError(t, err)

		disabled := *f.user
		disabled.Status = service.StatusDisabled
		require.NoError(t, f.repo.Update(context.Background(), &disabled, service.UserUpdateFields{}))

		_, err = f.refresh(rotIPv4A, rotUAChrome130Win, pair0.RefreshToken)
		require.ErrorIs(t, err, service.ErrUserNotActive)
	})
	t.Run("token already expired", func(t *testing.T) {
		f := newRotFixture(t, nil, nil)
		pair0 := f.login(rotIPv4A, rotUAChrome130Win)
		_, err := f.refresh(rotIPv4A, rotUAChrome130Win, pair0.RefreshToken)
		require.NoError(t, err)

		f.rewrite(pair0.RefreshToken, func(m map[string]any) { m["expires_at"] = rotTime(time.Now().Add(-time.Second)) })
		_, err = f.refresh(rotIPv4A, rotUAChrome130Win, pair0.RefreshToken)
		require.ErrorIs(t, err, service.ErrRefreshTokenExpired)
	})
}

// ---------------------------------------------------------------------------
// 先发后消费
// ---------------------------------------------------------------------------

// flakyRefreshCache 在真实存储外面套一层故障注入：让接下来几次「写入新令牌 / 原子消费旧令牌 /
// 占宽限名额 / 删除单个令牌 / 登记用户与家族集合」失败。它自己实现并转发 RefreshTokenAtomicStore——
// 服务层靠类型断言发现原子能力，嵌入接口类型的包装器不转发就会把它藏起来。
type flakyRefreshCache struct {
	service.RefreshTokenCache
	failNewStore  atomic.Int32
	failTombstone atomic.Int32 // 原子消费（ConsumeRefreshToken）失败
	failReserve   atomic.Int32 // 占宽限名额（ReserveLeewayReissue）失败
	failDelete    atomic.Int32
	failSetAdds   atomic.Int32
}

var errCacheBoom = errors.New("redis boom")

func (c *flakyRefreshCache) inner() service.RefreshTokenAtomicStore {
	store, _ := c.RefreshTokenCache.(service.RefreshTokenAtomicStore)
	return store
}

func (c *flakyRefreshCache) ConsumeRefreshToken(ctx context.Context, tokenHash string, tomb *service.RefreshTokenData, ttl time.Duration) (service.RefreshConsumeOutcome, error) {
	if c.failTombstone.Add(-1) >= 0 {
		return service.RefreshConsumeMissing, errCacheBoom
	}
	return c.inner().ConsumeRefreshToken(ctx, tokenHash, tomb, ttl)
}

func (c *flakyRefreshCache) ReserveLeewayReissue(ctx context.Context, tokenHash string, maxReissues int) (service.RefreshLeewayOutcome, int, error) {
	if c.failReserve.Add(-1) >= 0 {
		return service.RefreshLeewayMissing, 0, errCacheBoom
	}
	return c.inner().ReserveLeewayReissue(ctx, tokenHash, maxReissues)
}

func (c *flakyRefreshCache) StoreRefreshToken(ctx context.Context, tokenHash string, data *service.RefreshTokenData, ttl time.Duration) error {
	if !data.Consumed && c.failNewStore.Add(-1) >= 0 {
		return errCacheBoom
	}
	return c.RefreshTokenCache.StoreRefreshToken(ctx, tokenHash, data, ttl)
}

func (c *flakyRefreshCache) DeleteRefreshToken(ctx context.Context, tokenHash string) error {
	if c.failDelete.Add(-1) >= 0 {
		return errCacheBoom
	}
	return c.RefreshTokenCache.DeleteRefreshToken(ctx, tokenHash)
}

func (c *flakyRefreshCache) AddToUserTokenSet(ctx context.Context, userID int64, tokenHash string, ttl time.Duration) error {
	if c.failSetAdds.Add(-1) >= 0 {
		return errCacheBoom
	}
	return c.RefreshTokenCache.AddToUserTokenSet(ctx, userID, tokenHash, ttl)
}

func (c *flakyRefreshCache) AddToFamilyTokenSet(ctx context.Context, familyID, tokenHash string, ttl time.Duration) error {
	if c.failSetAdds.Add(-1) >= 0 {
		return errCacheBoom
	}
	return c.RefreshTokenCache.AddToFamilyTokenSet(ctx, familyID, tokenHash, ttl)
}

func newFlakyRotFixture(t *testing.T, mutate func(*config.Config)) (*rotFixture, *flakyRefreshCache) {
	t.Helper()
	var flaky *flakyRefreshCache
	f := newRotFixture(t, mutate, func(inner service.RefreshTokenCache) service.RefreshTokenCache {
		flaky = &flakyRefreshCache{RefreshTokenCache: inner}
		return flaky
	})
	return f, flaky
}

// 新令牌存不进去时，旧令牌保持可用：客户端可以安全重试，不会出现「旧的作废了、新的没发出去」。
func TestRefreshRotationOldTokenStaysUsableWhenNewTokenCannotBeStored(t *testing.T) {
	f, flaky := newFlakyRotFixture(t, nil)
	pair0 := f.login(rotIPv4A, rotUAChrome130Win)

	flaky.failNewStore.Store(1)
	_, err := f.refresh(rotIPv4A, rotUAChrome130Win, pair0.RefreshToken)
	require.ErrorIs(t, err, service.ErrServiceUnavailable, "a storage failure is a retryable 503, not a logout")

	rec := f.record(pair0.RefreshToken)
	require.NotNil(t, rec, "the old token must still exist")
	require.False(t, rec.Consumed, "the old token must not be consumed when no new token was issued")
	require.True(t, rec.ConsumedAt.IsZero())

	retried, err := f.refresh(rotIPv4A, rotUAChrome130Win, pair0.RefreshToken)
	require.NoError(t, err, "the safe retry must succeed with the very same token")
	require.NotEmpty(t, retried.RefreshToken)
	require.EqualValues(t, 1, f.svc.AuthStatsSnapshot()["refresh_unavailable"])
}

// 新令牌已经存好之后，无法确认旧令牌被消费（原子消费出错）：保守起见丢弃刚发的新令牌、返回可重试的 503，
// 旧令牌原样可用——不会留下一枚没人持有却有效的令牌，也不会把用户锁在外面。
func TestRefreshRotationFailsClosedWhenTheConsumeCannotBeConfirmed(t *testing.T) {
	f, flaky := newFlakyRotFixture(t, nil)
	pair0 := f.login(rotIPv4A, rotUAChrome130Win)
	require.Equal(t, 1, f.liveRefreshRecords())

	flaky.failTombstone.Store(1)
	_, err := f.refresh(rotIPv4A, rotUAChrome130Win, pair0.RefreshToken)
	require.ErrorIs(t, err, service.ErrServiceUnavailable)
	require.Equal(t, 1, f.liveRefreshRecords(), "the token issued in this attempt was discarded: no live credential nobody holds")
	rec := f.record(pair0.RefreshToken)
	require.NotNil(t, rec)
	require.False(t, rec.Consumed, "the old token is untouched and can be retried safely")

	rotated, err := f.refresh(rotIPv4A, rotUAChrome130Win, pair0.RefreshToken)
	require.NoError(t, err)
	require.NotEmpty(t, rotated.RefreshToken)
	require.EqualValues(t, 1, f.svc.AuthStatsSnapshot()["refresh_unavailable"])
}

// 现有约定不变：令牌登记进用户/家族集合失败不影响主流程（令牌本身已经存好）。
func TestRefreshRotationSucceedsWhenTokenSetRegistrationFails(t *testing.T) {
	f, flaky := newFlakyRotFixture(t, nil)
	pair0 := f.login(rotIPv4A, rotUAChrome130Win)

	flaky.failSetAdds.Store(2) // 用户集合与家族集合都失败
	rotated, err := f.refresh(rotIPv4A, rotUAChrome130Win, pair0.RefreshToken)
	require.NoError(t, err, "a failed set registration must not fail the refresh")
	require.NotNil(t, f.record(rotated.RefreshToken))
	_, err = f.refresh(rotIPv4A, rotUAChrome130Win, rotated.RefreshToken)
	require.NoError(t, err)
}

// ---------------------------------------------------------------------------
// 滑动有效期 + 绝对上限
// ---------------------------------------------------------------------------

// 每枚令牌的有效期是滑动的：轮换后新令牌从「现在」起重新计 14 天，而不是继承旧令牌的剩余寿命。
func TestRefreshRotationSlidingExpiryRestartsOnEveryRotation(t *testing.T) {
	f := newRotFixture(t, nil, nil)
	pair0 := f.login(rotIPv4A, rotUAChrome130Win)

	// 模拟「十几天过去了」：令牌只剩 2 天，会话 12 天前开始。
	f.rewrite(pair0.RefreshToken, func(m map[string]any) {
		m["session_started_at"] = rotTime(time.Now().Add(-12 * 24 * time.Hour))
		m["created_at"] = rotTime(time.Now().Add(-12 * 24 * time.Hour))
		m["expires_at"] = rotTime(time.Now().Add(2 * 24 * time.Hour))
	})
	f.mini.SetTTL(rotRecordKey(pair0.RefreshToken), 2*24*time.Hour)

	rotated, err := f.refresh(rotIPv4A, rotUAChrome130Win, pair0.RefreshToken)
	require.NoError(t, err)

	rec := f.record(rotated.RefreshToken)
	require.NotNil(t, rec)
	remaining := time.Until(rec.ExpiresAt)
	require.Greater(t, remaining, 14*24*time.Hour-time.Minute, "a rotation restarts the 14-day idle window")
	require.LessOrEqual(t, remaining, 14*24*time.Hour)
	require.Greater(t, f.mini.TTL(rotRecordKey(rotated.RefreshToken)), 14*24*time.Hour-time.Minute, "the Redis TTL follows ExpiresAt")
}

// 绝对上限：新令牌的 ExpiresAt = min(现在 + 14 天, 会话起点 + 90 天)——临近上限时不能超过上限。
func TestRefreshRotationNewTokenNeverOutlivesTheAbsoluteSessionCap(t *testing.T) {
	f := newRotFixture(t, nil, nil)
	pair0 := f.login(rotIPv4A, rotUAChrome130Win)

	sessionStart := time.Now().Add(-85 * 24 * time.Hour)
	f.rewrite(pair0.RefreshToken, func(m map[string]any) {
		m["session_started_at"] = rotTime(sessionStart)
		m["created_at"] = rotTime(time.Now().Add(-time.Hour))
	})

	rotated, err := f.refresh(rotIPv4A, rotUAChrome130Win, pair0.RefreshToken)
	require.NoError(t, err)

	rec := f.record(rotated.RefreshToken)
	require.WithinDuration(t, sessionStart.Add(90*24*time.Hour), rec.ExpiresAt, time.Second, "capped by session start + 90d, not now + 14d")
	require.WithinDuration(t, sessionStart, rec.SessionStartedAt, time.Second, "the session start is inherited")
	ttl := f.mini.TTL(rotRecordKey(rotated.RefreshToken))
	require.Greater(t, ttl, 5*24*time.Hour-time.Minute)
	require.LessOrEqual(t, ttl, 5*24*time.Hour+time.Minute)
	// 这枚令牌所在的家族集合不会比它先消失。
	require.GreaterOrEqual(t, f.mini.TTL("token_family:"+rec.FamilyID), 14*24*time.Hour-time.Minute,
		"the family/user sets keep a full sliding TTL so they outlive every token they list")
}

// 过了绝对上限必须重新登录，并撤销整个家族。
func TestRefreshRotationRejectsSessionBeyondTheAbsoluteCap(t *testing.T) {
	f := newRotFixture(t, nil, nil)
	pair0 := f.login(rotIPv4A, rotUAChrome130Win)
	f.rewrite(pair0.RefreshToken, func(m map[string]any) {
		m["session_started_at"] = rotTime(time.Now().Add(-91 * 24 * time.Hour))
	})

	_, err := f.refresh(rotIPv4A, rotUAChrome130Win, pair0.RefreshToken)
	require.ErrorIs(t, err, service.ErrRefreshTokenSessionExpired)
	f.requireSessionRevoked(pair0.RefreshToken)
	require.EqualValues(t, 1, f.svc.AuthStatsSnapshot()["refresh_session_expired"])
}

// 配置的会话上限生效：把上限设成 30 天，31 天前开始的会话就过期了。
func TestRefreshRotationSessionCapFollowsConfig(t *testing.T) {
	f := newRotFixture(t, func(c *config.Config) { c.JWT.SessionMaxLifetimeDays = 30 }, nil)
	pair0 := f.login(rotIPv4A, rotUAChrome130Win)
	f.rewrite(pair0.RefreshToken, func(m map[string]any) {
		m["session_started_at"] = rotTime(time.Now().Add(-31 * 24 * time.Hour))
	})
	_, err := f.refresh(rotIPv4A, rotUAChrome130Win, pair0.RefreshToken)
	require.ErrorIs(t, err, service.ErrRefreshTokenSessionExpired)
}

// 升级前的旧记录：没有 session_started_at / consumed_at / binding_coarse。
// 不能因为这次改动把现存会话踢下线：会话起点回退到 CreatedAt，旧记录里 20 天前创建、
// 还剩 10 天的令牌（旧的 30 天会话），在新的 14 天滑动 + 90 天上限下照常续期。
func TestRefreshRotationLegacyRecordsAreNotKickedOut(t *testing.T) {
	f := newRotFixture(t, nil, nil)
	pair0 := f.login(rotIPv4A, rotUAChrome130Win)
	created := time.Now().Add(-20 * 24 * time.Hour)
	f.rewrite(pair0.RefreshToken, func(m map[string]any) {
		delete(m, "session_started_at")
		delete(m, "binding_coarse")
		delete(m, "consumed_at")
		delete(m, "leeway_reissues")
		m["binding_hash"] = "0123456789abcdef0123456789abcdef" // 升级前按严格算法写的指纹，与新算法对不上
		m["created_at"] = rotTime(created)
		m["expires_at"] = rotTime(time.Now().Add(10 * 24 * time.Hour))
	})

	// 换个网络刷新：旧会话（没有 session_started_at）的指纹放行一次，不被误杀。
	rotated, err := f.refresh(rotIPv4B, rotUAFirefoxLinux, pair0.RefreshToken)
	require.NoError(t, err, "a pre-upgrade session must survive the upgrade")

	rec := f.record(rotated.RefreshToken)
	require.NotNil(t, rec)
	require.WithinDuration(t, created, rec.SessionStartedAt, time.Second, "session start falls back to CreatedAt and is now stored explicitly")
	require.Greater(t, time.Until(rec.ExpiresAt), 14*24*time.Hour-time.Minute, "the sliding window restarts from now")
	require.NotEmpty(t, rec.BindingCoarse, "the rotated token carries the new coarse fingerprint")
	require.Zero(t, f.svc.AuthStatsSnapshot()["refresh_binding_rebind"], "legacy sessions skip the fingerprint comparison once instead of counting as a mismatch")
}

// 旧会话如果起点已经超过 90 天（极端旧数据），照样按上限处理——只回退起点，不无限续命。
func TestRefreshRotationLegacySessionWithoutStartFallsBackToCreatedAtForTheCap(t *testing.T) {
	f := newRotFixture(t, nil, nil)
	pair0 := f.login(rotIPv4A, rotUAChrome130Win)
	f.rewrite(pair0.RefreshToken, func(m map[string]any) {
		delete(m, "session_started_at")
		m["created_at"] = rotTime(time.Now().Add(-95 * 24 * time.Hour))
		m["expires_at"] = rotTime(time.Now().Add(time.Hour))
	})
	_, err := f.refresh(rotIPv4A, rotUAChrome130Win, pair0.RefreshToken)
	require.ErrorIs(t, err, service.ErrRefreshTokenSessionExpired)
}

// ---------------------------------------------------------------------------
// 会话绑定：rebind / revoke
// ---------------------------------------------------------------------------

// 默认 rebind：只是换了网段（同一个浏览器 + 系统）时刷新不掉线，新令牌带当前指纹（重绑），旧家族不撤销。
func TestRefreshRotationBindingMismatchRebindsInsteadOfRevoking(t *testing.T) {
	f := newRotFixture(t, nil, nil)
	pair0 := f.login(rotIPv4A, rotUAChrome130Win)
	login := f.record(pair0.RefreshToken)

	rotated, err := f.refresh(rotIPv4B, rotUAChrome130Win, pair0.RefreshToken)
	require.NoError(t, err, "a changed network with the same browser/OS must not log the user out")

	rec := f.record(rotated.RefreshToken)
	require.Equal(t, login.FamilyID, rec.FamilyID, "same session")
	require.NotEqual(t, login.BindingHash, rec.BindingHash, "the new token is bound to the current fingerprint")
	rebound := &service.SessionBinding{IP: rotIPv4B, UserAgent: rotUAChrome130Win}
	require.Equal(t, rebound.HashForMode(config.SessionBindingModeLoose), rec.BindingHash)
	require.Equal(t, rebound.CoarseFingerprint(), rec.BindingCoarse)
	require.EqualValues(t, 1, f.svc.AuthStatsSnapshot()["refresh_binding_rebind"])

	// 之后从新环境刷新不再算不一致。
	_, err = f.refresh(rotIPv4B, rotUAChrome130Win, rotated.RefreshToken)
	require.NoError(t, err)
	require.EqualValues(t, 1, f.svc.AuthStatsSnapshot()["refresh_binding_rebind"], "only the first refresh from the new client is a rebind")
}

// rebind 只放行「浏览器家族 + 系统家族没变、只是 IP 变了」：家族变了（换了浏览器/系统，
// 或是别的程序拿着令牌）即使在默认的 rebind 模式下也撤销整个会话家族。
func TestRefreshRotationRebindRefusesWhenTheBrowserOrOSFamilyChanged(t *testing.T) {
	cases := []struct {
		name string
		ip   string
		ua   string
	}{
		{"other browser and OS family", rotIPv4B, rotUAFirefoxLinux},
		{"other browser and OS family on the same network", rotIPv4A, rotUAFirefoxLinux},
		{"a script holding the token", rotIPv4B, "curl/8.4.0"},
		{"no user agent at all", rotIPv4B, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := newRotFixture(t, nil, nil) // 默认 rebind
			pair0 := f.login(rotIPv4A, rotUAChrome130Win)
			_, err := f.refresh(tc.ip, tc.ua, pair0.RefreshToken)
			require.ErrorIs(t, err, service.ErrSessionBindingMismatch)
			f.requireSessionRevoked(pair0.RefreshToken)
			stats := f.svc.AuthStatsSnapshot()
			require.EqualValues(t, 1, stats["refresh_binding_revoke"])
			require.Zero(t, stats["refresh_binding_rebind"])
		})
	}
}

// revoke 模式保留旧行为：不一致即撤销整个家族，返回 SESSION_BINDING_MISMATCH。
func TestRefreshRotationBindingMismatchRevokesInRevokeMode(t *testing.T) {
	f := newRotFixture(t, func(c *config.Config) { c.Security.SessionBindingOnMismatch = config.SessionBindingOnMismatchRevoke }, nil)
	pair0 := f.login(rotIPv4A, rotUAChrome130Win)
	other := f.login(rotIPv4A, rotUAChrome130Win) // 另一个会话不受影响

	_, err := f.refresh(rotIPv4B, rotUAFirefoxLinux, pair0.RefreshToken)
	require.ErrorIs(t, err, service.ErrSessionBindingMismatch)
	f.requireSessionRevoked(pair0.RefreshToken)
	require.EqualValues(t, 1, f.svc.AuthStatsSnapshot()["refresh_binding_revoke"])

	_, err = f.refresh(rotIPv4A, rotUAChrome130Win, other.RefreshToken)
	require.NoError(t, err, "revoking one session family must not touch the user's other sessions")
}

// loose 模式下这些变化都不算不一致（即使是 revoke 模式也不撤销）：
// 浏览器小版本升级、同一 /24 内的 IP 漂移、IPv4 <-> IPv6 切换。
func TestRefreshRotationLooseBindingIgnoresVersionBumpsSameNetAndIPFamilySwitch(t *testing.T) {
	cases := []struct {
		name           string
		loginIP, ua    string
		refreshIP, ua2 string
	}{
		{"chrome version bump", rotIPv4A, rotUAChrome130Win, rotIPv4A, rotUAChrome131Win},
		{"same /24 drift", rotIPv4A, rotUAChrome130Win, rotIPv4SameNet, rotUAChrome130Win},
		{"ipv4 to ipv6 (wifi to cellular)", rotIPv4A, rotUAChrome130Win, rotIPv6, rotUAChrome130Win},
		{"ipv6 to ipv4", rotIPv6, rotUAChrome130Win, rotIPv4B, rotUAChrome130Win},
		{"ipv4 to ipv6 plus version bump", rotIPv4A, rotUAChrome130Win, rotIPv6, rotUAChrome131Win},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := newRotFixture(t, func(c *config.Config) { c.Security.SessionBindingOnMismatch = config.SessionBindingOnMismatchRevoke }, nil)
			pair0 := f.login(tc.loginIP, tc.ua)
			_, err := f.refresh(tc.refreshIP, tc.ua2, pair0.RefreshToken)
			require.NoError(t, err, "not a mismatch, so even revoke mode must let it through")
			stats := f.svc.AuthStatsSnapshot()
			require.Zero(t, stats["refresh_binding_rebind"])
			require.Zero(t, stats["refresh_binding_revoke"])
		})
	}
}

// 真正换了客户端（同家族 IP 换网段、浏览器/系统家族变了）才算不一致。
func TestRefreshRotationLooseBindingStillCatchesARealClientChange(t *testing.T) {
	cases := []struct {
		name      string
		refreshIP string
		ua        string
	}{
		{"other /24 same browser", rotIPv4B, rotUAChrome130Win},
		{"other browser same network", rotIPv4A, rotUAFirefoxLinux},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := newRotFixture(t, func(c *config.Config) { c.Security.SessionBindingOnMismatch = config.SessionBindingOnMismatchRevoke }, nil)
			pair0 := f.login(rotIPv4A, rotUAChrome130Win)
			_, err := f.refresh(tc.refreshIP, tc.ua, pair0.RefreshToken)
			require.ErrorIs(t, err, service.ErrSessionBindingMismatch)
		})
	}
}

// strict 模式保持严格：浏览器小版本升级也算不一致（用 revoke 看得见）。
func TestRefreshRotationStrictBindingKeepsFullUserAgent(t *testing.T) {
	f := newRotFixture(t, func(c *config.Config) {
		c.Security.SessionBindingMode = config.SessionBindingModeStrict
		c.Security.SessionBindingOnMismatch = config.SessionBindingOnMismatchRevoke
	}, nil)
	pair0 := f.login(rotIPv4A, rotUAChrome130Win)
	_, err := f.refresh(rotIPv4A, rotUAChrome131Win, pair0.RefreshToken)
	require.ErrorIs(t, err, service.ErrSessionBindingMismatch)
}

// 绑定整体关闭（mode=off）：不写指纹也不校验。
func TestRefreshRotationBindingOffNeverMismatches(t *testing.T) {
	f := newRotFixture(t, func(c *config.Config) {
		c.Security.SessionBindingMode = config.SessionBindingModeOff
		c.Security.SessionBindingOnMismatch = config.SessionBindingOnMismatchRevoke
	}, nil)
	pair0 := f.login(rotIPv4A, rotUAChrome130Win)
	require.Empty(t, f.record(pair0.RefreshToken).BindingHash)
	require.Empty(t, f.record(pair0.RefreshToken).BindingCoarse)
	_, err := f.refresh(rotIPv4B, rotUAFirefoxLinux, pair0.RefreshToken)
	require.NoError(t, err)
}

// 新旧版本混跑：新版签发的令牌里 BindingHash 仍是旧算法（旧实例照样能比对），粗粒度指纹是额外字段。
func TestRefreshRotationIssuedBindingHashKeepsTheLegacyAlgorithm(t *testing.T) {
	f := newRotFixture(t, nil, nil)
	pair0 := f.login(rotIPv4A, rotUAChrome130Win)
	rec := f.record(pair0.RefreshToken)
	legacy := &service.SessionBinding{IP: rotIPv4A, UserAgent: rotUAChrome130Win}
	require.Equal(t, legacy.HashForMode(config.SessionBindingModeLoose), rec.BindingHash,
		"an old instance must still be able to compare this value")
	require.NotEmpty(t, rec.BindingCoarse)
	require.NotContains(t, rec.BindingCoarse, rotIPv4A, "the coarse form never carries the raw IP")
	require.NotContains(t, rec.BindingCoarse, "130", "the coarse form never carries a version number")
}

// ---------------------------------------------------------------------------
// 并发
// ---------------------------------------------------------------------------

// 同一令牌被少量并发刷新（多个标签页/设备同时发现 401）：不会误撤销会话，每个并发请求都拿到可用的新令牌。
// 并发数不超过「首次轮换 + 宽限重发上限」，所以不论怎么交错都不该有请求被拒。
func TestRefreshRotationConcurrentSameTokenKeepsTheSession(t *testing.T) {
	for round := 0; round < 25; round++ {
		f := newRotFixture(t, nil, nil)
		pair0 := f.login(rotIPv4A, rotUAChrome130Win)

		const parallel = 3
		results := make([]*service.TokenPairWithUser, parallel)
		errs := make([]error, parallel)
		var ready, done sync.WaitGroup
		start := make(chan struct{})
		for i := 0; i < parallel; i++ {
			ready.Add(1)
			done.Add(1)
			go func() {
				defer done.Done()
				ready.Done()
				<-start
				results[i], errs[i] = f.refresh(rotIPv4A, rotUAChrome130Win, pair0.RefreshToken)
			}()
		}
		ready.Wait()
		close(start)
		done.Wait()

		for i := 0; i < parallel; i++ {
			require.NoError(t, errs[i], "round %d request %d must not be treated as a replay", round, i)
		}
		for i := 0; i < parallel; i++ {
			_, err := f.refresh(rotIPv4A, rotUAChrome130Win, results[i].RefreshToken)
			require.NoError(t, err, "round %d: every token handed out must work", round)
		}
	}
}

// 大量并发刷新同一令牌（压力 + -race）：只检查不变量——没有 panic / 数据竞争，
// 每个请求要么成功，要么是重放拒绝，要么（家族已被撤销后才到达）查不到令牌，且至少有一个成功。
func TestRefreshRotationConcurrentStormHasOnlyExpectedOutcomes(t *testing.T) {
	f := newRotFixture(t, nil, nil)
	pair0 := f.login(rotIPv4A, rotUAChrome130Win)

	const parallel = 24
	errs := make([]error, parallel)
	var ready, done sync.WaitGroup
	start := make(chan struct{})
	for i := 0; i < parallel; i++ {
		ready.Add(1)
		done.Add(1)
		go func() {
			defer done.Done()
			ready.Done()
			<-start
			_, errs[i] = f.refresh(rotIPv4A, rotUAChrome130Win, pair0.RefreshToken)
		}()
	}
	ready.Wait()
	close(start)
	done.Wait()

	succeeded := 0
	for i, err := range errs {
		switch {
		case err == nil:
			succeeded++
		case errors.Is(err, service.ErrRefreshTokenReuse), errors.Is(err, service.ErrRefreshTokenInvalid):
		default:
			t.Fatalf("request %d: unexpected error %v", i, err)
		}
	}
	require.Positive(t, succeeded)
	// 重发上限在 Redis 里原子执行：不论怎么交错，成功的最多是「首次轮换 1 个 + 宽限重发 3 个」。
	require.LessOrEqual(t, succeeded, 1+3, "the reissue cap must hold under concurrency")
	stats := f.svc.AuthStatsSnapshot()
	require.EqualValues(t, succeeded, stats["refresh_ok"])
}

// ---------------------------------------------------------------------------
// 可观测：日志里不能有令牌明文 / 哈希 / IP / 完整 UA
// ---------------------------------------------------------------------------

type syncBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *syncBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *syncBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

// captureAuthLogs 把 slog 与标准 log 的输出收进缓冲，测试结束后恢复（slog.SetDefault 会改标准 log 的输出）。
func captureAuthLogs(t *testing.T) *syncBuffer {
	t.Helper()
	prevLogger, prevWriter, prevFlags := slog.Default(), log.Writer(), log.Flags()
	buf := &syncBuffer{}
	slog.SetDefault(slog.New(slog.NewTextHandler(buf, &slog.HandlerOptions{Level: slog.LevelDebug})))
	t.Cleanup(func() {
		slog.SetDefault(prevLogger)
		log.SetOutput(prevWriter)
		log.SetFlags(prevFlags)
	})
	return buf
}

func sha256Hex(s string) string {
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:])
}

var rawRefreshTokenPattern = regexp.MustCompile(`rt_[0-9a-f]{16}`)

// 把每一种会打 WARN/ERROR 的路径都走一遍，断言日志里：有用户 ID 和粗粒度标签，
// 但没有任何令牌明文、令牌哈希、家族 ID、IP 原文、完整 UA。
func TestRefreshRotationLogsNeverContainTokensHashesIPsOrUserAgents(t *testing.T) {
	logs := captureAuthLogs(t)

	secrets := map[string]string{}
	remember := func(name, value string) { secrets[name] = value }

	// rebind（只换网段）+ 宽限内重试 + 别的客户端在宽限内重放（默认 rebind 模式）
	f := newRotFixture(t, nil, nil)
	pair0 := f.login(rotIPv4A, rotUAChrome130Win)
	remember("login access token", pair0.AccessToken)
	remember("login refresh token", pair0.RefreshToken)
	remember("login refresh token hash", sha256Hex(pair0.RefreshToken))
	remember("family id", f.record(pair0.RefreshToken).FamilyID)

	rebound, err := f.refresh(rotIPv4B, rotUAChrome130Win, pair0.RefreshToken) // rebind，消费者指纹 = B
	require.NoError(t, err)
	remember("rotated refresh token", rebound.RefreshToken)
	remember("rotated refresh token hash", sha256Hex(rebound.RefreshToken))
	retry, err := f.refresh(rotIPv4B, rotUAChrome130Win, pair0.RefreshToken) // 消费者自己重试
	require.NoError(t, err)
	remember("retry refresh token", retry.RefreshToken)
	_, err = f.refresh(rotIPv4A, rotUAChrome130Win, pair0.RefreshToken) // 别的客户端（A）在宽限内重放
	require.ErrorIs(t, err, service.ErrRefreshTokenReuse)

	// 宽限之外的重放
	f2 := newRotFixture(t, nil, nil)
	p2 := f2.login(rotIPv4A, rotUAChrome130Win)
	remember("replay refresh token", p2.RefreshToken)
	_, err = f2.refresh(rotIPv4A, rotUAChrome130Win, p2.RefreshToken)
	require.NoError(t, err)
	f2.ageConsumedAt(p2.RefreshToken, 5*time.Minute)
	_, err = f2.refresh(rotIPv4A, rotUAChrome130Win, p2.RefreshToken)
	require.ErrorIs(t, err, service.ErrRefreshTokenReuse)

	// 家族变了（rebind 模式也撤销）
	f3 := newRotFixture(t, nil, nil)
	p3 := f3.login(rotIPv4A, rotUAChrome130Win)
	remember("family-change refresh token", p3.RefreshToken)
	_, err = f3.refresh(rotIPv4B, rotUAFirefoxLinux, p3.RefreshToken)
	require.ErrorIs(t, err, service.ErrSessionBindingMismatch)

	// binding revoke + session expired（revoke 模式的夹具）
	g := newRotFixture(t, func(c *config.Config) { c.Security.SessionBindingOnMismatch = config.SessionBindingOnMismatchRevoke }, nil)
	gp := g.login(rotIPv4A, rotUAChrome130Win)
	remember("revoke-mode refresh token", gp.RefreshToken)
	_, err = g.refresh(rotIPv4B, rotUAChrome130Win, gp.RefreshToken)
	require.ErrorIs(t, err, service.ErrSessionBindingMismatch)
	hp := g.login(rotIPv4A, rotUAChrome130Win)
	g.rewrite(hp.RefreshToken, func(m map[string]any) { m["session_started_at"] = rotTime(time.Now().Add(-100 * 24 * time.Hour)) })
	_, err = g.refresh(rotIPv4A, rotUAChrome130Win, hp.RefreshToken)
	require.ErrorIs(t, err, service.ErrRefreshTokenSessionExpired)

	// 存储故障（带错误文本的路径）
	h, flaky := newFlakyRotFixture(t, nil)
	hq := h.login(rotIPv4A, rotUAChrome130Win)
	remember("flaky refresh token", hq.RefreshToken)
	flaky.failNewStore.Store(1)
	_, err = h.refresh(rotIPv4A, rotUAChrome130Win, hq.RefreshToken)
	require.ErrorIs(t, err, service.ErrServiceUnavailable)
	flaky.failTombstone.Store(1)
	_, err = h.refresh(rotIPv4A, rotUAChrome130Win, hq.RefreshToken)
	require.ErrorIs(t, err, service.ErrServiceUnavailable)

	out := logs.String()
	require.NotEmpty(t, out)
	for name, secret := range secrets {
		require.NotContains(t, out, secret, "logs must not contain the %s", name)
	}
	require.False(t, rawRefreshTokenPattern.MatchString(out), "logs must not contain anything shaped like a refresh token")
	for _, forbidden := range []string{rotIPv4A, rotIPv4B, rotIPv6, "Chrome/130", "Firefox/132", "AppleWebKit", "X11; Linux"} {
		require.NotContains(t, out, forbidden, "logs must not contain raw IP / full user agent data")
	}

	// 但要有排障需要的东西：用户 ID（只在 WARN 单条事件里）和粗粒度标签。
	require.Contains(t, out, fmt.Sprintf("user_id=%d", f.user.ID))
	require.Contains(t, out, "from=chrome/windows/ipv4")
	require.Contains(t, out, "to=firefox/linux/ipv4")
	require.Contains(t, out, "consumer=chrome/windows/ipv4")
	require.Contains(t, out, "replayer=chrome/windows/ipv4")
	for _, message := range []string{
		"refresh token replay detected",
		"retried within the leeway window",
		"session binding mismatch on refresh: session kept",
		"replayed by a different client inside the leeway window",
		"session binding mismatch on refresh: session family revoked",
		"refresh session reached its absolute lifetime",
		"issuing the new refresh token failed",
		"could not mark the old refresh token consumed",
	} {
		require.Contains(t, out, message)
	}
	for _, line := range strings.Split(out, "\n") {
		if strings.Contains(line, "level=INFO") && strings.Contains(line, "auth session summary") {
			require.NotContains(t, line, "user_id", "the INFO summary never carries user ids")
		}
	}
}

// 单条 WARN 事件是限频的：一阵风暴只留固定条数，超出的只进 warn_suppressed 计数，不刷屏。
func TestRefreshRotationWarnEventsAreRateLimited(t *testing.T) {
	logs := captureAuthLogs(t)
	f := newRotFixture(t, func(c *config.Config) { c.JWT.RefreshTokenReuseLeewaySeconds = 0 }, nil)

	const storms = 40
	for i := 0; i < storms; i++ {
		pair := f.login(rotIPv4A, rotUAChrome130Win)
		_, err := f.refresh(rotIPv4A, rotUAChrome130Win, pair.RefreshToken)
		require.NoError(t, err)
		_, err = f.refresh(rotIPv4A, rotUAChrome130Win, pair.RefreshToken)
		require.ErrorIs(t, err, service.ErrRefreshTokenReuse)
	}

	lines := strings.Count(logs.String(), "refresh token replay detected")
	require.Equal(t, 10, lines, "at most 10 single-event WARN lines per kind per minute")
	stats := f.svc.AuthStatsSnapshot()
	require.EqualValues(t, storms, stats["refresh_reuse"], "every replay is still counted")
	require.EqualValues(t, storms-10, stats["warn_suppressed"])
}
