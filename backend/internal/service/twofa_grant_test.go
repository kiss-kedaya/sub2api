//go:build unit

package service

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// plainTotpCacheForGrantTests 故意不实现 TwoFactorGrantStore，用来验证服务在
// 凭证存储未装配时 fail-closed。
type plainTotpCacheForGrantTests struct {
	TotpCache
}

// twoFactorGrantTestCache 是只实现凭证存储端口的测试替身。
// 其余 TotpCache 方法通过嵌入接口占位，凭证相关测试不会调到它们。
type twoFactorGrantTestCache struct {
	TotpCache

	mu       sync.Mutex
	records  map[string]*TwoFactorGrantRecord
	ttls     map[string]time.Duration
	attempts map[string]int64
	revoked  []int64
	failSave bool
	failLoad bool
}

func newTwoFactorGrantTestCache() *twoFactorGrantTestCache {
	return &twoFactorGrantTestCache{
		records:  map[string]*TwoFactorGrantRecord{},
		ttls:     map[string]time.Duration{},
		attempts: map[string]int64{},
	}
}

func (c *twoFactorGrantTestCache) SaveTwoFactorGrant(_ context.Context, digest string, record *TwoFactorGrantRecord, ttl time.Duration) error {
	if c.failSave {
		return context.DeadlineExceeded
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	copied := *record
	c.records[digest] = &copied
	c.ttls[digest] = ttl
	return nil
}

func (c *twoFactorGrantTestCache) LoadTwoFactorGrant(_ context.Context, digest string) (*TwoFactorGrantRecord, error) {
	if c.failLoad {
		return nil, context.DeadlineExceeded
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	record, ok := c.records[digest]
	if !ok {
		return nil, nil
	}
	copied := *record
	return &copied, nil
}

func (c *twoFactorGrantTestCache) DeleteTwoFactorGrant(_ context.Context, digest string) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	delete(c.records, digest)
	delete(c.ttls, digest)
	return nil
}

func (c *twoFactorGrantTestCache) RevokeTwoFactorGrantsForUser(_ context.Context, userID int64) (int64, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.revoked = append(c.revoked, userID)
	var revoked int64
	for digest, record := range c.records {
		if record.UserID == userID {
			delete(c.records, digest)
			delete(c.ttls, digest)
			revoked++
		}
	}
	return revoked, nil
}

func (c *twoFactorGrantTestCache) IncrementTwoFactorGrantVerifyAttempt(_ context.Context, userID int64, ipHash string, _ time.Duration) (int64, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	key := twoFactorGrantRateKeyForTest(userID, ipHash)
	c.attempts[key]++
	return c.attempts[key], nil
}

func twoFactorGrantRateKeyForTest(userID int64, ipHash string) string {
	return fmt.Sprintf("%d:%s", userID, ipHash)
}

func newTwoFactorGrantTestService(t *testing.T, cache TotpCache) *TotpService {
	t.Helper()
	return NewTotpService(nil, nil, cache, nil, nil, nil)
}

func twoFactorGrantTestBinding() TwoFactorGrantBinding {
	return TwoFactorGrantBinding{IP: "203.0.113.7", UserAgent: "unit-test-agent/1.0"}
}

// 凭证必须是 32 字节 crypto/rand 的不透明 base64url 串；服务端只保存 SHA-256 摘要。
func TestTwoFactorGrantIssueIsOpaqueAndHashed(t *testing.T) {
	cache := newTwoFactorGrantTestCache()
	svc := newTwoFactorGrantTestService(t, cache)

	grant, err := svc.IssueTwoFactorGrant(context.Background(), 42, TwoFactorGrantScopeLogin, FactorTOTP, twoFactorGrantTestBinding())
	require.NoError(t, err)
	require.NotEmpty(t, grant.Token)
	require.Equal(t, TwoFactorGrantScopeLogin, grant.Scope)
	require.InDelta(t, float64(TwoFactorGrantTTL/time.Second), float64(grant.Remaining/time.Second), 2)

	digest := twoFactorGrantDigest(grant.Token)
	require.NotContains(t, digest, grant.Token)
	record, ok := cache.records[digest]
	require.True(t, ok, "凭证必须以 sha256(token) 作为 key 存储")
	for key := range cache.records {
		require.NotContains(t, key, grant.Token, "存储 key 不能包含 token 明文")
	}
	require.Equal(t, int64(42), record.UserID)
	require.Equal(t, TwoFactorGrantScopeLogin, record.Scope)
	require.NotEqual(t, twoFactorGrantTestBinding().IP, record.IPHash, "IP 不能明文入库")
	require.NotEqual(t, twoFactorGrantTestBinding().UserAgent, record.UAHash, "UA 不能明文入库")
	require.LessOrEqual(t, record.ExpiresAt-record.IssuedAt, int64(TwoFactorGrantTTL/time.Second))

	encoded, err := json.Marshal(record)
	require.NoError(t, err)
	require.NotContains(t, string(encoded), grant.Token)
}

func TestTwoFactorGrantConsumeBindsUserAndClient(t *testing.T) {
	cache := newTwoFactorGrantTestCache()
	svc := newTwoFactorGrantTestService(t, cache)
	ctx := context.Background()
	grant, err := svc.IssueTwoFactorGrant(ctx, 42, TwoFactorGrantScopePassword, FactorTOTP, twoFactorGrantTestBinding())
	require.NoError(t, err)

	// 用户不匹配。
	_, err = svc.ConsumeTwoFactorGrant(ctx, grant.Token, 43, FactorTOTP, twoFactorGrantTestBinding())
	require.ErrorIs(t, err, errTwoFactorGrantUser)
	require.Equal(t, TwoFactorGrantReasonUserMismatch, TwoFactorGrantRejectReason(err))

	// IP 不匹配。
	wrongIP := twoFactorGrantTestBinding()
	wrongIP.IP = "203.0.113.8"
	_, err = svc.ConsumeTwoFactorGrant(ctx, grant.Token, 42, FactorTOTP, wrongIP)
	require.ErrorIs(t, err, errTwoFactorGrantBinding)
	require.Equal(t, TwoFactorGrantReasonBindingMismatch, TwoFactorGrantRejectReason(err))
	require.True(t, IsTwoFactorGrantExpiredOrMismatched(err))

	// UA 不匹配。
	wrongUA := twoFactorGrantTestBinding()
	wrongUA.UserAgent = "another-agent"
	_, err = svc.ConsumeTwoFactorGrant(ctx, grant.Token, 42, FactorTOTP, wrongUA)
	require.ErrorIs(t, err, errTwoFactorGrantBinding)

	// 正确绑定才通过。
	used, err := svc.ConsumeTwoFactorGrant(ctx, grant.Token, 42, FactorTOTP, twoFactorGrantTestBinding())
	require.NoError(t, err)
	require.Equal(t, TwoFactorGrantScopePassword, used.Scope)
	require.Greater(t, used.Remaining, 4*time.Minute)
}

// 固定 5 分钟窗口：到期即失效，不能靠重签续期。
func TestTwoFactorGrantFixedFiveMinuteWindow(t *testing.T) {
	cache := newTwoFactorGrantTestCache()
	svc := newTwoFactorGrantTestService(t, cache)
	ctx := context.Background()
	grant, err := svc.IssueTwoFactorGrant(ctx, 42, TwoFactorGrantScopeLogin, FactorTOTP, twoFactorGrantTestBinding())
	require.NoError(t, err)

	// 模拟 5:01 之后（测试替身不会自己过期，直接改记录的绝对到期时间）。
	digest := twoFactorGrantDigest(grant.Token)
	cache.records[digest].IssuedAt = time.Now().Add(-6 * time.Minute).Unix()
	cache.records[digest].ExpiresAt = time.Now().Add(-time.Second).Unix()

	_, err = svc.ConsumeTwoFactorGrant(ctx, grant.Token, 42, FactorTOTP, twoFactorGrantTestBinding())
	require.ErrorIs(t, err, errTwoFactorGrantExpired)
	require.Equal(t, TwoFactorGrantReasonExpired, TwoFactorGrantRejectReason(err))
	require.True(t, IsTwoFactorGrantExpiredOrMismatched(err))
}

// 重签只允许一次，且新凭证与旧凭证共享同一个绝对到期时间（不续期）。
func TestTwoFactorGrantReissueOnceWithoutExtendingExpiry(t *testing.T) {
	cache := newTwoFactorGrantTestCache()
	svc := newTwoFactorGrantTestService(t, cache)
	ctx := context.Background()
	grant, err := svc.IssueTwoFactorGrant(ctx, 42, TwoFactorGrantScopeLogin, FactorTOTP, twoFactorGrantTestBinding())
	require.NoError(t, err)
	firstExpiry := time.Unix(cache.records[twoFactorGrantDigest(grant.Token)].ExpiresAt, 0)

	used, err := svc.ConsumeTwoFactorGrant(ctx, grant.Token, 42, FactorTOTP, twoFactorGrantTestBinding())
	require.NoError(t, err)
	require.NotEqual(t, grant.Token, used.Token, "第一次成功校验必须重签一次并替换 Cookie")
	require.Equal(t, firstExpiry, used.ExpiresAt, "重签不得延长绝对到期时间")

	rotatedRecord := cache.records[twoFactorGrantDigest(used.Token)]
	require.NotNil(t, rotatedRecord)
	require.True(t, rotatedRecord.Reissued)
	require.Equal(t, firstExpiry.Unix(), rotatedRecord.ExpiresAt)

	// 第二次使用：不再重签，token 保持原样。
	again, err := svc.ConsumeTwoFactorGrant(ctx, used.Token, 42, FactorTOTP, twoFactorGrantTestBinding())
	require.NoError(t, err)
	require.Equal(t, used.Token, again.Token)
	require.Equal(t, firstExpiry, again.ExpiresAt)

	// 旧 token 在重签后已被删除，重放旧 cookie 必须失败。
	_, err = svc.ConsumeTwoFactorGrant(ctx, grant.Token, 42, FactorTOTP, twoFactorGrantTestBinding())
	require.ErrorIs(t, err, errTwoFactorGrantUnknown)
}

func TestTwoFactorGrantRejectsTamperedRecord(t *testing.T) {
	cache := newTwoFactorGrantTestCache()
	svc := newTwoFactorGrantTestService(t, cache)
	ctx := context.Background()
	grant, err := svc.IssueTwoFactorGrant(ctx, 42, TwoFactorGrantScopeLogin, FactorTOTP, twoFactorGrantTestBinding())
	require.NoError(t, err)
	digest := twoFactorGrantDigest(grant.Token)

	// 记录声明的窗口超过 5 分钟：视为损坏，fail-closed。
	cache.records[digest].ExpiresAt = cache.records[digest].IssuedAt + int64(10*time.Minute/time.Second)
	_, err = svc.ConsumeTwoFactorGrant(ctx, grant.Token, 42, FactorTOTP, twoFactorGrantTestBinding())
	require.ErrorIs(t, err, errTwoFactorGrantExpired)

	cache.records[digest].IssuedAt = time.Now().Add(-2 * time.Minute).Unix()
	cache.records[digest].ExpiresAt = time.Now().Add(time.Minute).Unix()
	cache.records[digest].Scope = "admin"
	_, err = svc.ConsumeTwoFactorGrant(ctx, grant.Token, 42, FactorTOTP, twoFactorGrantTestBinding())
	require.ErrorIs(t, err, errTwoFactorGrantUnknown)
}

// Redis 不可用 / 存储缺失 / 读失败必须 fail-closed：返回错误，绝不静默放行。
func TestTwoFactorGrantFailsClosedWhenStoreUnavailable(t *testing.T) {
	ctx := context.Background()
	binding := twoFactorGrantTestBinding()

	// 测试替身没有实现凭证存储端口。
	plain := &plainTotpCacheForGrantTests{}
	svc := newTwoFactorGrantTestService(t, plain)
	_, err := svc.IssueTwoFactorGrant(ctx, 42, TwoFactorGrantScopeLogin, FactorTOTP, binding)
	require.ErrorIs(t, err, ErrTwoFactorGrantUnavailable)
	_, err = svc.ConsumeTwoFactorGrant(ctx, "opaque-token", 42, FactorTOTP, binding)
	require.ErrorIs(t, err, ErrTwoFactorGrantUnavailable)
	require.Equal(t, TwoFactorGrantReasonRedisUnavailable, TwoFactorGrantRejectReason(err))

	// 读失败。
	failingLoad := newTwoFactorGrantTestCache()
	failingLoad.failLoad = true
	svc = newTwoFactorGrantTestService(t, failingLoad)
	_, err = svc.ConsumeTwoFactorGrant(ctx, "opaque-token", 42, FactorTOTP, binding)
	require.ErrorIs(t, err, ErrTwoFactorGrantUnavailable)

	// 写失败（签发）。
	failingSave := newTwoFactorGrantTestCache()
	failingSave.failSave = true
	svc = newTwoFactorGrantTestService(t, failingSave)
	_, err = svc.IssueTwoFactorGrant(ctx, 42, TwoFactorGrantScopeLogin, FactorTOTP, binding)
	require.ErrorIs(t, err, ErrTwoFactorGrantUnavailable)

	// 重签写失败同样 fail-closed。
	okCache := newTwoFactorGrantTestCache()
	svc = newTwoFactorGrantTestService(t, okCache)
	grant, err := svc.IssueTwoFactorGrant(ctx, 42, TwoFactorGrantScopeLogin, FactorTOTP, binding)
	require.NoError(t, err)
	okCache.failSave = true
	_, err = svc.ConsumeTwoFactorGrant(ctx, grant.Token, 42, FactorTOTP, binding)
	require.ErrorIs(t, err, ErrTwoFactorGrantUnavailable)
}

// 凭证校验每用户/IP 10 次/分钟：第 11 次直接拒绝（fail-closed）。
func TestTwoFactorGrantVerifyRateLimitedPerUserAndIP(t *testing.T) {
	cache := newTwoFactorGrantTestCache()
	svc := newTwoFactorGrantTestService(t, cache)
	ctx := context.Background()
	grant, err := svc.IssueTwoFactorGrant(ctx, 42, TwoFactorGrantScopeLogin, FactorTOTP, twoFactorGrantTestBinding())
	require.NoError(t, err)

	binding := twoFactorGrantTestBinding()
	ipHash, _ := binding.AuditFingerprints()
	rateKey := twoFactorGrantRateKeyForTest(42, ipHash)
	cache.attempts[rateKey] = twoFactorGrantVerifyLimit

	_, err = svc.ConsumeTwoFactorGrant(ctx, grant.Token, 42, FactorTOTP, binding)
	require.ErrorIs(t, err, errTwoFactorGrantRateLimited)
	require.Equal(t, TwoFactorGrantReasonRateLimited, TwoFactorGrantRejectReason(err))
	require.False(t, IsTwoFactorGrantExpiredOrMismatched(err), "限速不能清 Cookie")

	// 另一个 IP 的计数互不影响。
	otherBinding := binding
	otherBinding.IP = "203.0.113.9"
	_, err = svc.ConsumeTwoFactorGrant(ctx, grant.Token, 42, FactorTOTP, otherBinding)
	require.ErrorIs(t, err, errTwoFactorGrantBinding, "先通过限速，再因绑定不匹配被拒")
}

func TestTwoFactorGrantRejectsMissingOrOversizedToken(t *testing.T) {
	cache := newTwoFactorGrantTestCache()
	svc := newTwoFactorGrantTestService(t, cache)
	ctx := context.Background()
	binding := twoFactorGrantTestBinding()

	_, err := svc.ConsumeTwoFactorGrant(ctx, "", 42, FactorTOTP, binding)
	require.ErrorIs(t, err, errTwoFactorGrantUnknown)
	_, err = svc.ConsumeTwoFactorGrant(ctx, "token", 0, FactorTOTP, binding)
	require.ErrorIs(t, err, errTwoFactorGrantUnknown)

	oversized := make([]byte, twoFactorGrantMaxTokenLen+1)
	for i := range oversized {
		oversized[i] = 'a'
	}
	_, err = svc.ConsumeTwoFactorGrant(ctx, string(oversized), 42, FactorTOTP, binding)
	require.ErrorIs(t, err, errTwoFactorGrantUnknown)

	// 没有可信 IP 不签发（不能退化成无绑定凭证）。
	_, err = svc.IssueTwoFactorGrant(ctx, 42, TwoFactorGrantScopeLogin, FactorTOTP, TwoFactorGrantBinding{UserAgent: "ua"})
	require.ErrorIs(t, err, ErrTwoFactorGrantUnavailable)
}

func TestTwoFactorGrantRevokeForUser(t *testing.T) {
	cache := newTwoFactorGrantTestCache()
	svc := newTwoFactorGrantTestService(t, cache)
	ctx := context.Background()
	for i := 0; i < 2; i++ {
		_, err := svc.IssueTwoFactorGrant(ctx, 42, TwoFactorGrantScopeLogin, FactorTOTP, twoFactorGrantTestBinding())
		require.NoError(t, err)
	}
	_, err := svc.IssueTwoFactorGrant(ctx, 43, TwoFactorGrantScopeLogin, FactorTOTP, twoFactorGrantTestBinding())
	require.NoError(t, err)

	require.NoError(t, svc.RevokeTwoFactorGrantsForUser(ctx, 42))
	require.Equal(t, []int64{42}, cache.revoked)
	require.Len(t, cache.records, 1, "只删除目标用户的凭证")
}

func TestTwoFactorGrantCookieMaxAge(t *testing.T) {
	require.Equal(t, 300, TwoFactorGrantCookieMaxAge(TwoFactorGrantTTL))
	require.Equal(t, 300, TwoFactorGrantCookieMaxAge(10*time.Minute), "不能超过 5 分钟上限")
	require.Equal(t, 2, TwoFactorGrantCookieMaxAge(1500*time.Millisecond), "向上取整")
	require.Equal(t, 1, TwoFactorGrantCookieMaxAge(0))
	require.Equal(t, 1, TwoFactorGrantCookieMaxAge(-time.Minute))
}

// 拒绝原因枚举必须覆盖设计要求的六个值。
func TestTwoFactorGrantRejectReasonEnum(t *testing.T) {
	require.Equal(t, "expired", TwoFactorGrantReasonExpired)
	require.Equal(t, "mismatch_user", TwoFactorGrantReasonUserMismatch)
	require.Equal(t, "mismatch_ipua", TwoFactorGrantReasonBindingMismatch)
	require.Equal(t, "unknown", TwoFactorGrantReasonUnknown)
	require.Equal(t, "redis_unavailable", TwoFactorGrantReasonRedisUnavailable)
	require.Equal(t, "rate_limited", TwoFactorGrantReasonRateLimited)
	require.Equal(t, "", TwoFactorGrantRejectReason(nil))
}
