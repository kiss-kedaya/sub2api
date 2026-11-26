//go:build unit

package service_test

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

// 独立安全评审（PR #691）提出的竞态与规避路径的回归测试。每个用例对应评审里的一条探针：
// 修之前探针断言「危险结果成立」，修之后这里断言安全结果。

// interleaveCache 转发全部能力（包括 service.RefreshTokenAtomicStore），并在关键步骤前后插入动作，
// 用确定性的交错模拟「撤销恰好落在两步之间」「并发请求抢先消费」「请求在中途被取消」。
type interleaveCache struct {
	service.RefreshTokenCache
	mu            sync.Mutex
	afterNewToken func() // 新令牌（非墓碑）存好之后，只触发一次
	beforeConsume func() // 原子消费旧令牌之前，只触发一次
	beforeReserve func() // 占宽限重发名额之前，只触发一次

	// getBarrier：让前 need 个 GetRefreshToken 都读到同一份快照之后再一起放行。
	barrierNeed, barrierArrived int
	barrierRelease              chan struct{}
}

func takeHook(mu *sync.Mutex, hook *func()) func() {
	mu.Lock()
	defer mu.Unlock()
	h := *hook
	*hook = nil
	return h
}

func (c *interleaveCache) inner() service.RefreshTokenAtomicStore {
	store, _ := c.RefreshTokenCache.(service.RefreshTokenAtomicStore)
	return store
}

func (c *interleaveCache) StoreRefreshToken(ctx context.Context, tokenHash string, data *service.RefreshTokenData, ttl time.Duration) error {
	err := c.RefreshTokenCache.StoreRefreshToken(ctx, tokenHash, data, ttl)
	if !data.Consumed && err == nil {
		if hook := takeHook(&c.mu, &c.afterNewToken); hook != nil {
			hook()
		}
	}
	return err
}

func (c *interleaveCache) GetRefreshToken(ctx context.Context, tokenHash string) (*service.RefreshTokenData, error) {
	data, err := c.RefreshTokenCache.GetRefreshToken(ctx, tokenHash)
	c.mu.Lock()
	if c.barrierNeed == 0 {
		c.mu.Unlock()
		return data, err
	}
	c.barrierArrived++
	if c.barrierArrived == c.barrierNeed {
		close(c.barrierRelease)
	}
	release := c.barrierRelease
	c.mu.Unlock()
	<-release // 已经放行之后到达的调用直接通过（通道已关闭）
	return data, err
}

func (c *interleaveCache) ConsumeRefreshToken(ctx context.Context, tokenHash string, tomb *service.RefreshTokenData, ttl time.Duration) (service.RefreshConsumeOutcome, error) {
	if hook := takeHook(&c.mu, &c.beforeConsume); hook != nil {
		hook()
	}
	return c.inner().ConsumeRefreshToken(ctx, tokenHash, tomb, ttl)
}

func (c *interleaveCache) ReserveLeewayReissue(ctx context.Context, tokenHash string, maxReissues int) (service.RefreshLeewayOutcome, int, error) {
	if hook := takeHook(&c.mu, &c.beforeReserve); hook != nil {
		hook()
	}
	return c.inner().ReserveLeewayReissue(ctx, tokenHash, maxReissues)
}

func (c *interleaveCache) armGetBarrier(need int) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.barrierNeed, c.barrierArrived, c.barrierRelease = need, 0, make(chan struct{})
}

func (c *interleaveCache) disarmGetBarrier() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.barrierNeed = 0
}

func newInterleaveFixture(t *testing.T, mutate func(*config.Config)) (*rotFixture, *interleaveCache) {
	t.Helper()
	var ic *interleaveCache
	f := newRotFixture(t, mutate, func(inner service.RefreshTokenCache) service.RefreshTokenCache {
		ic = &interleaveCache{RefreshTokenCache: inner}
		return ic
	})
	return f, ic
}

// ---------------------------------------------------------------------------
// P1 / P1b：撤销落在在途刷新上，墓碑不能被盲写回去、宽限不能把它变回凭证
// ---------------------------------------------------------------------------

// 普通轮换进行到「新令牌已存好、旧令牌还没标记已消费」时用户登出（撤销整个家族）：
// 旧令牌的墓碑不会被写回（原子消费发现记录已不存在），刚发的新令牌被丢弃，已登出的会话不会复活。
func TestRefreshRotationRevokeBetweenNewTokenAndConsumeDoesNotResurrectTheSession(t *testing.T) {
	f, ic := newInterleaveFixture(t, nil)
	pair0 := f.login(rotIPv4A, rotUAChrome130Win)
	famID := f.record(pair0.RefreshToken).FamilyID

	ic.beforeConsume = func() {
		// 登出落在这里：新令牌已存好，旧令牌还没标记已消费。
		require.NoError(t, f.svc.RevokeSessionFamily(context.Background(), famID))
	}
	_, err := f.refresh(rotIPv4A, rotUAChrome130Win, pair0.RefreshToken)
	require.ErrorIs(t, err, service.ErrRefreshTokenInvalid, "the in-flight refresh of a revoked session must not hand out a token")

	require.Nil(t, f.record(pair0.RefreshToken), "the old token's tombstone must not be written back after the revoke")
	require.Zero(t, f.liveRefreshRecords(), "the token issued in flight was discarded: nothing of the session is left")
	_, err = f.refresh(rotIPv4A, rotUAChrome130Win, pair0.RefreshToken)
	require.ErrorIs(t, err, service.ErrRefreshTokenInvalid, "and the old token cannot be replayed inside the leeway window")
}

// 同上，换成「撤销全部会话」（用户集合）。
func TestRefreshRotationRevokeAllSessionsBetweenNewTokenAndConsumeDoesNotResurrectTheSession(t *testing.T) {
	f, ic := newInterleaveFixture(t, nil)
	pair0 := f.login(rotIPv4A, rotUAChrome130Win)

	ic.beforeConsume = func() {
		require.NoError(t, f.svc.RevokeAllUserSessions(context.Background(), f.user.ID))
	}
	_, err := f.refresh(rotIPv4A, rotUAChrome130Win, pair0.RefreshToken)
	require.ErrorIs(t, err, service.ErrRefreshTokenInvalid)
	require.Zero(t, f.liveRefreshRecords())
}

// 宽限重发在飞行中，撤销落在「占名额」之前：占名额失败（墓碑已不存在），不写任何东西、不发令牌。
func TestRefreshRotationRevokeBeforeLeewayReserveWritesNothingBack(t *testing.T) {
	f, ic := newInterleaveFixture(t, nil)
	pair0 := f.login(rotIPv4A, rotUAChrome130Win)
	famID := f.record(pair0.RefreshToken).FamilyID
	_, err := f.refresh(rotIPv4A, rotUAChrome130Win, pair0.RefreshToken)
	require.NoError(t, err)

	ic.beforeReserve = func() {
		require.NoError(t, f.svc.RevokeSessionFamily(context.Background(), famID))
	}
	_, err = f.refresh(rotIPv4A, rotUAChrome130Win, pair0.RefreshToken)
	require.ErrorIs(t, err, service.ErrRefreshTokenInvalid)
	require.Nil(t, f.record(pair0.RefreshToken), "the tombstone must not be re-created by the reserve")
	require.Zero(t, f.liveRefreshRecords())

	_, err = f.refresh(rotIPv4A, rotUAChrome130Win, pair0.RefreshToken)
	require.ErrorIs(t, err, service.ErrRefreshTokenInvalid, "the revoked family does not come back through the tombstone")
}

// 宽限重发在飞行中，撤销落在「新令牌已发、收尾确认之前」：确认墓碑已不存在，丢弃刚发的新令牌。
func TestRefreshRotationRevokeAfterLeewayReserveDiscardsTheIssuedToken(t *testing.T) {
	f, ic := newInterleaveFixture(t, nil)
	pair0 := f.login(rotIPv4A, rotUAChrome130Win)
	famID := f.record(pair0.RefreshToken).FamilyID
	_, err := f.refresh(rotIPv4A, rotUAChrome130Win, pair0.RefreshToken)
	require.NoError(t, err)

	ic.afterNewToken = func() {
		// 名额已占、新令牌刚存好——撤销在这时到来；它删掉的家族集合里还没有这枚新令牌。
		require.NoError(t, f.svc.RevokeSessionFamily(context.Background(), famID))
	}
	_, err = f.refresh(rotIPv4A, rotUAChrome130Win, pair0.RefreshToken)
	require.ErrorIs(t, err, service.ErrRefreshTokenInvalid)
	require.Zero(t, f.liveRefreshRecords(), "the token issued after the revoke must not survive it")
}

// ---------------------------------------------------------------------------
// P2：重发上限按原子计数执行
// ---------------------------------------------------------------------------

// 所有请求持有同一份快照（计数 0）时，以前全部放行；现在占名额是 Redis 里的原子计数，最多 3 个成功。
func TestRefreshRotationReissueCapHoldsWhenEveryRequestSawTheSameSnapshot(t *testing.T) {
	f, ic := newInterleaveFixture(t, nil)
	pair0 := f.login(rotIPv4A, rotUAChrome130Win)
	_, err := f.refresh(rotIPv4A, rotUAChrome130Win, pair0.RefreshToken)
	require.NoError(t, err)

	const parallel = 12
	ic.armGetBarrier(parallel)
	errs := make([]error, parallel)
	var wg sync.WaitGroup
	for i := 0; i < parallel; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, errs[i] = f.refresh(rotIPv4A, rotUAChrome130Win, pair0.RefreshToken)
		}()
	}
	wg.Wait()
	ic.disarmGetBarrier()

	ok := 0
	for _, e := range errs {
		if e == nil {
			ok++
		}
	}
	require.LessOrEqual(t, ok, 3, "at most 3 reissues may ever be minted from one consumed token")
	require.Positive(t, f.svc.AuthStatsSnapshot()["refresh_reuse"], "the over-cap requests are replays")
}

// ---------------------------------------------------------------------------
// P3：请求被取消，收尾仍然做完
// ---------------------------------------------------------------------------

// 请求在「新令牌已存好」之后被取消（客户端断开）：旧令牌照样被消费，新令牌照样登记进用户/家族集合，
// 「撤销全部会话」能杀掉它——不会留下旧令牌继续有效、新令牌够不着的状态。
func TestRefreshRotationFinishesWhenTheRequestIsCanceledAfterTheNewTokenIsStored(t *testing.T) {
	f, ic := newInterleaveFixture(t, nil)
	pair0 := f.login(rotIPv4A, rotUAChrome130Win)
	famID := f.record(pair0.RefreshToken).FamilyID

	ctx, cancel := context.WithCancel(rotCtx(rotIPv4A, rotUAChrome130Win))
	defer cancel()
	ic.afterNewToken = cancel
	got, err := f.svc.RefreshTokenPair(ctx, pair0.RefreshToken)
	require.NoError(t, err, "the rotation completes even though the client went away")

	old := f.record(pair0.RefreshToken)
	require.NotNil(t, old)
	require.True(t, old.Consumed, "the old token must be consumed, not left valid")

	newRec := f.record(got.RefreshToken)
	require.NotNil(t, newRec)
	require.Len(t, f.familyMembers(famID), 2, "login token + the new token are registered in the family set")
	userMembers, err := f.mini.Members("user_refresh_tokens:501")
	require.NoError(t, err)
	require.Len(t, userMembers, 2)

	require.NoError(t, f.svc.RevokeAllUserSessions(context.Background(), f.user.ID))
	require.NoError(t, f.svc.RevokeSessionFamily(context.Background(), famID))
	_, err = f.refresh(rotIPv4A, rotUAChrome130Win, got.RefreshToken)
	require.ErrorIs(t, err, service.ErrRefreshTokenInvalid, "the token issued by the canceled request is revocable")
}

// 登录（首次签发）同理：令牌存好之后客户端断开，令牌照样登记进用户/家族集合，
// 「撤销全部会话」够得着它——登记用的是脱离请求取消的 ctx，不是请求 ctx。
func TestRefreshRotationLoginRegistersTheTokenEvenIfTheRequestIsCanceledAfterStore(t *testing.T) {
	f, ic := newInterleaveFixture(t, nil)

	ctx, cancel := context.WithCancel(rotCtx(rotIPv4A, rotUAChrome130Win))
	defer cancel()
	ic.afterNewToken = cancel
	pair, err := f.svc.GenerateTokenPair(ctx, f.user, "")
	require.NoError(t, err)

	rec := f.record(pair.RefreshToken)
	require.NotNil(t, rec)
	require.Len(t, f.familyMembers(rec.FamilyID), 1, "registered in the family set")
	userMembers, err := f.mini.Members("user_refresh_tokens:501")
	require.NoError(t, err)
	require.Len(t, userMembers, 1, "registered in the user set")

	require.NoError(t, f.svc.RevokeAllUserSessions(context.Background(), f.user.ID))
	f.requireSessionRevoked(pair.RefreshToken)
}

// 宽限内的重试在占名额之前请求就被取消：占名额、发新令牌、登记、收尾确认都不跟随取消，重试照样做完——
// 不会留下「名额占了、令牌没发 / 发了但够不着」的半截状态。
func TestRefreshRotationLeewayRetryCompletesWhenTheRequestIsCanceledBeforeTheReserve(t *testing.T) {
	f, ic := newInterleaveFixture(t, nil)
	pair0 := f.login(rotIPv4A, rotUAChrome130Win)
	famID := f.record(pair0.RefreshToken).FamilyID
	_, err := f.refresh(rotIPv4A, rotUAChrome130Win, pair0.RefreshToken)
	require.NoError(t, err)

	ctx, cancel := context.WithCancel(rotCtx(rotIPv4A, rotUAChrome130Win))
	defer cancel()
	ic.beforeReserve = cancel
	retry, err := f.svc.RefreshTokenPair(ctx, pair0.RefreshToken)
	require.NoError(t, err, "the retry completes even though the client went away")

	require.EqualValues(t, 1, f.record(pair0.RefreshToken).LeewayReissues, "the reserve was counted atomically")
	require.NotNil(t, f.record(retry.RefreshToken))
	require.Len(t, f.familyMembers(famID), 3, "login token + first rotation + the retry are all registered in the family set")

	require.NoError(t, f.svc.RevokeSessionFamily(context.Background(), famID))
	f.requireSessionRevoked(retry.RefreshToken)
}

// 撤销本身也不跟随请求的取消：重放检测触发时，即使这个请求的 ctx 在检测前已经取消，家族照样被撤销。
func TestRefreshRotationReplayRevokeSurvivesACanceledRequest(t *testing.T) {
	f := newRotFixture(t, func(c *config.Config) { c.JWT.RefreshTokenReuseLeewaySeconds = 0 }, nil)
	pair0 := f.login(rotIPv4A, rotUAChrome130Win)
	first, err := f.refresh(rotIPv4A, rotUAChrome130Win, pair0.RefreshToken)
	require.NoError(t, err)

	// 读令牌用请求 ctx；检测到重放之后的撤销要用脱离取消的 ctx——这里让 ctx 在读完令牌之后才被取消。
	ctx, cancel := context.WithCancel(rotCtx(rotIPv4A, rotUAChrome130Win))
	defer cancel()
	ic := &cancelAfterGetCache{RefreshTokenCache: f.cache, cancel: cancel}
	svc := service.NewAuthService(nil, f.repo, nil, ic, f.cfg, nil, nil, nil, nil, nil, nil, nil, nil)
	_, err = svc.RefreshTokenPair(ctx, pair0.RefreshToken)
	require.ErrorIs(t, err, service.ErrRefreshTokenReuse)
	f.requireSessionRevoked(first.RefreshToken)
}

// cancelAfterGetCache 在读完令牌之后取消请求 ctx。
type cancelAfterGetCache struct {
	service.RefreshTokenCache
	cancel context.CancelFunc
}

func (c *cancelAfterGetCache) GetRefreshToken(ctx context.Context, tokenHash string) (*service.RefreshTokenData, error) {
	data, err := c.RefreshTokenCache.GetRefreshToken(ctx, tokenHash)
	c.cancel()
	return data, err
}

// ---------------------------------------------------------------------------
// P8：宽限 + rebind 不能被「抢在受害者之前换一次」规避
// ---------------------------------------------------------------------------

// 受害者先轮换；攻击者拿着刚被轮换掉的旧令牌，在宽限窗口内从别的网络 + 别的 UA 出示：
// 以前在 rebind 下直接放行、会话分叉成两条各自轮换的链；现在宽限只给消费它的那个客户端，
// 别的客户端出示一律按重放处理——撤销整个家族，两条链都作废。
func TestRefreshRotationReplayFromAnotherClientInsideLeewayRevokesTheFamily(t *testing.T) {
	f := newRotFixture(t, nil, nil)
	pair0 := f.login(rotIPv4A, rotUAChrome130Win)
	victim, err := f.refresh(rotIPv4A, rotUAChrome130Win, pair0.RefreshToken) // 受害者先轮换
	require.NoError(t, err)

	f.ageConsumedAt(pair0.RefreshToken, 5*time.Second) // 仍在宽限窗口内
	_, err = f.refresh(rotIPv4B, rotUAFirefoxLinux, pair0.RefreshToken)
	require.ErrorIs(t, err, service.ErrRefreshTokenReuse, "another network + another browser inside the window is a replay, not a retry")

	f.requireSessionRevoked(victim.RefreshToken)
	stats := f.svc.AuthStatsSnapshot()
	require.EqualValues(t, 1, stats["refresh_leeway_fingerprint_reject"])
	require.Zero(t, stats["refresh_binding_rebind"], "no rebind and no reissue inside the leeway window")
	require.Zero(t, stats["refresh_reuse_leeway_retry"])
}

// 反过来：攻击者先出示当前令牌，只换了 IP（浏览器/系统家族照抄，rebind 放行首次出示）；
// 受害者随后带着旧令牌回来仍落在窗口内——它的指纹和「消费者（攻击者）」不一致，按重放处理，两条链都作废。
func TestRefreshRotationVictimsLateRetryAfterAnAttackerRebindRevokesTheFamily(t *testing.T) {
	f := newRotFixture(t, nil, nil)
	pair0 := f.login(rotIPv4A, rotUAChrome130Win)
	attacker, err := f.refresh(rotIPv4B, rotUAChrome130Win, pair0.RefreshToken) // 首次出示：只换 IP，rebind 放行
	require.NoError(t, err)
	require.EqualValues(t, 1, f.svc.AuthStatsSnapshot()["refresh_binding_rebind"])

	_, err = f.refresh(rotIPv4A, rotUAChrome130Win, pair0.RefreshToken) // 受害者带着旧令牌回来
	require.ErrorIs(t, err, service.ErrRefreshTokenReuse)
	f.requireSessionRevoked(attacker.RefreshToken)
	require.EqualValues(t, 1, f.svc.AuthStatsSnapshot()["refresh_leeway_fingerprint_reject"])
}

// 墓碑上记的是「消费者」的指纹（不是令牌原来的绑定）——宽限路径比对的就是它。
func TestRefreshRotationTombstoneRecordsTheConsumersFingerprint(t *testing.T) {
	f := newRotFixture(t, nil, nil)
	pair0 := f.login(rotIPv4A, rotUAChrome130Win)
	_, err := f.refresh(rotIPv4B, rotUAChrome130Win, pair0.RefreshToken)
	require.NoError(t, err)

	tomb := f.record(pair0.RefreshToken)
	consumer := &service.SessionBinding{IP: rotIPv4B, UserAgent: rotUAChrome130Win}
	require.True(t, tomb.Consumed)
	require.Equal(t, consumer.HashForMode(config.SessionBindingModeLoose), tomb.BindingHash)
	require.Equal(t, consumer.CoarseFingerprint(), tomb.BindingCoarse)
}

// 已知限制（写成测试，免得以后被误当成漏洞或悄悄改变）：指纹和消费者一致的重放——同一个 /24、同样的
// 浏览器家族 + 系统家族——在窗口内无法和合法重试区分，宽限会放行。窗口越短暴露越小，所以默认只有 30 秒。
func TestRefreshRotationKnownLimitSameFingerprintReplayInsideLeewayIsIndistinguishable(t *testing.T) {
	f := newRotFixture(t, nil, nil)
	pair0 := f.login(rotIPv4A, rotUAChrome130Win)
	_, err := f.refresh(rotIPv4A, rotUAChrome130Win, pair0.RefreshToken)
	require.NoError(t, err)

	_, err = f.refresh(rotIPv4SameNet, rotUAChrome131Win, pair0.RefreshToken)
	require.NoError(t, err, "same /24 + same browser/OS family cannot be told apart from a legitimate retry")
}

// 只有首次出示的当前令牌才能 rebind；宽限路径里指纹不一致不 rebind、不重发——哪怕只是 IP 变了。
func TestRefreshRotationLeewayRetryWithOnlyAnIPChangeIsStillAReplay(t *testing.T) {
	f := newRotFixture(t, nil, nil)
	pair0 := f.login(rotIPv4A, rotUAChrome130Win)
	first, err := f.refresh(rotIPv4A, rotUAChrome130Win, pair0.RefreshToken)
	require.NoError(t, err)

	_, err = f.refresh(rotIPv4B, rotUAChrome130Win, pair0.RefreshToken)
	require.ErrorIs(t, err, service.ErrRefreshTokenReuse)
	f.requireSessionRevoked(first.RefreshToken)
}

// ---------------------------------------------------------------------------
// 第二轮评审 N1：宽限路径的「本人」判定是粗指纹精确相等
// （浏览器家族 + 系统家族 + IP 家族 + 网段全部相同，只忽略 UA 版本号）
// ---------------------------------------------------------------------------

// 攻击者抢在受害者之前刷新一次（IP 家族与受害者相反、UA 照抄同样的浏览器/系统家族）：首次出示的当前令牌
// 放行；受害者 30 秒内带着旧令牌回来——IP 家族和「消费者（攻击者）」不同，不是本人重试，按重放处理。
// 反过来受害者先轮换、攻击者拿旧令牌在窗口内出示也是重放。两个顺序各跑若干轮（攻击之前先正常轮换 0–3 次），
// 每一轮都不会出现两条同时可用的链：撤销整个家族，一枚令牌都不剩。
func TestRefreshRotationOppositeIPFamilyWithCopiedUAFamilyNeverForksTheSession(t *testing.T) {
	for _, order := range []string{"attacker first", "victim first"} {
		t.Run(order, func(t *testing.T) {
			for rotations := 0; rotations <= 3; rotations++ {
				f := newRotFixture(t, nil, nil)
				token := f.login(rotIPv4A, rotUAChrome130Win).RefreshToken
				for i := 0; i < rotations; i++ {
					p, err := f.refresh(rotIPv4A, rotUAChrome130Win, token)
					require.NoError(t, err)
					token = p.RefreshToken
				}

				var live string // 攻击/重放发生之前，另一方手里最新的令牌
				if order == "attacker first" {
					atk, err := f.refresh(rotIPv6, rotUAChrome131Win, token) // 相反的 IP 家族，UA 家族照抄
					require.NoError(t, err, "first presentation of the current token is not a leeway retry")
					live = atk.RefreshToken
					_, err = f.refresh(rotIPv4A, rotUAChrome130Win, token) // 受害者带着旧令牌回来
					require.ErrorIs(t, err, service.ErrRefreshTokenReuse, "the victim's late retry is not the consumer: replay")
				} else {
					vic, err := f.refresh(rotIPv4A, rotUAChrome130Win, token)
					require.NoError(t, err)
					live = vic.RefreshToken
					_, err = f.refresh(rotIPv6, rotUAChrome131Win, token) // 攻击者拿着刚被轮换掉的令牌
					require.ErrorIs(t, err, service.ErrRefreshTokenReuse, "the thief is not the consumer: replay")
				}
				f.requireSessionRevoked(live)
				require.Zero(t, f.liveRefreshRecords(), "rotations=%d: no chain of the session may survive the conflict", rotations)
				require.EqualValues(t, 1, f.svc.AuthStatsSnapshot()["refresh_leeway_fingerprint_reject"])
			}
		})
	}
}

// 宽限内的重试什么算「本人」：同一个客户端、同一个网络——浏览器家族、系统家族、IP 家族、网段（v4 /24、v6 /64）
// 全部相同，UA 版本号不看。同 /24 换主机、浏览器小版本升级通过；换网段、换 IP 家族（WiFi IPv4 <-> 蜂窝 IPv6，
// 这是有意的取舍：回到修复之前的行为，要重新登录）、换浏览器都按重放处理。
func TestRefreshRotationLeewayRetryRequiresTheSameClientAndNetwork(t *testing.T) {
	cases := []struct {
		name             string
		loginIP, loginUA string
		retryIP, retryUA string
		ok               bool
	}{
		{"same client", rotIPv4A, rotUAChrome130Win, rotIPv4A, rotUAChrome130Win, true},
		{"same /24 another host", rotIPv4A, rotUAChrome130Win, rotIPv4SameNet, rotUAChrome130Win, true},
		{"browser minor version upgrade", rotIPv4A, rotUAChrome130Win, rotIPv4A, rotUAChrome131Win, true},
		{"same /24 another host and version upgrade", rotIPv4A, rotUAChrome130Win, rotIPv4SameNet, rotUAChrome131Win, true},
		{"same /64 another host", rotIPv6, rotUAChrome130Win, "2001:db8:abcd:12::99", rotUAChrome130Win, true},
		{"another /24", rotIPv4A, rotUAChrome130Win, rotIPv4B, rotUAChrome130Win, false},
		{"another /64", rotIPv6, rotUAChrome130Win, "2001:db8:abcd:99::1", rotUAChrome130Win, false},
		{"ipv4 to ipv6 (wifi to cellular)", rotIPv4A, rotUAChrome130Win, rotIPv6, rotUAChrome130Win, false},
		{"ipv6 to ipv4", rotIPv6, rotUAChrome130Win, rotIPv4A, rotUAChrome130Win, false},
		{"ip family switch with a copied UA family", rotIPv4A, rotUAChrome130Win, rotIPv6, rotUAChrome131Win, false},
		{"other browser and OS on the same network", rotIPv4A, rotUAChrome130Win, rotIPv4A, rotUAFirefoxLinux, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := newRotFixture(t, nil, nil)
			pair0 := f.login(tc.loginIP, tc.loginUA)
			first, err := f.refresh(tc.loginIP, tc.loginUA, pair0.RefreshToken) // 消费者：和登录时是同一个客户端
			require.NoError(t, err)

			retry, err := f.refresh(tc.retryIP, tc.retryUA, pair0.RefreshToken)
			stats := f.svc.AuthStatsSnapshot()
			if tc.ok {
				require.NoError(t, err)
				require.NotEqual(t, first.RefreshToken, retry.RefreshToken)
				require.EqualValues(t, 1, stats["refresh_leeway_fingerprint_ok"])
				require.Zero(t, stats["refresh_leeway_fingerprint_reject"])
				return
			}
			require.ErrorIs(t, err, service.ErrRefreshTokenReuse)
			f.requireSessionRevoked(first.RefreshToken)
			require.EqualValues(t, 1, stats["refresh_leeway_fingerprint_reject"])
			require.Zero(t, stats["refresh_leeway_fingerprint_ok"])
			require.Zero(t, stats["refresh_reuse_leeway_retry"], "no reissue for a fingerprint that is not the consumer's")
		})
	}
}

// ---------------------------------------------------------------------------
// 第二轮评审 N2：发令牌失败不占宽限名额（先发后占）
// ---------------------------------------------------------------------------

// 宽限内的重试发不出令牌（新令牌存不进去 / 占名额时存储出错）连续失败几次，不消耗名额：
// 之后正常的重试仍然成功，会话保住——不会因为 3 次瞬时故障就被判「名额用完」撤销整个家族。
func TestRefreshRotationLeewayReissueFailuresDoNotConsumeSlots(t *testing.T) {
	cases := []struct {
		name   string
		inject func(*flakyRefreshCache)
	}{
		{"the new token cannot be stored", func(c *flakyRefreshCache) { c.failNewStore.Store(3) }},
		{"the slot reservation errors out", func(c *flakyRefreshCache) { c.failReserve.Store(3) }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f, flaky := newFlakyRotFixture(t, nil)
			pair0 := f.login(rotIPv4A, rotUAChrome130Win)
			first, err := f.refresh(rotIPv4A, rotUAChrome130Win, pair0.RefreshToken)
			require.NoError(t, err)
			before := f.liveRefreshRecords()

			tc.inject(flaky)
			for i := 0; i < 3; i++ {
				_, err = f.refresh(rotIPv4A, rotUAChrome130Win, pair0.RefreshToken)
				require.ErrorIs(t, err, service.ErrServiceUnavailable, "attempt %d: a storage hiccup is a retryable 503", i+1)
			}
			require.Zero(t, f.record(pair0.RefreshToken).LeewayReissues, "failed attempts must not take slots")
			require.Equal(t, before, f.liveRefreshRecords(), "a token issued by a failed attempt must not stay alive")

			retry, err := f.refresh(rotIPv4A, rotUAChrome130Win, pair0.RefreshToken)
			require.NoError(t, err, "the 4th, normal retry is still a retry, not a replay")
			require.Equal(t, 1, f.record(pair0.RefreshToken).LeewayReissues)
			_, err = f.refresh(rotIPv4A, rotUAChrome130Win, first.RefreshToken)
			require.NoError(t, err, "the session was never revoked")
			_, err = f.refresh(rotIPv4A, rotUAChrome130Win, retry.RefreshToken)
			require.NoError(t, err)
			require.Zero(t, f.svc.AuthStatsSnapshot()["refresh_reuse"])
		})
	}
}

// ---------------------------------------------------------------------------
// 并发抢先消费：输家按「宽限内重试」判定
// ---------------------------------------------------------------------------

// 另一个并发请求（同一客户端）在这次请求签发新令牌之后、消费之前抢先消费了旧令牌：
// 这次请求按宽限内重试处理——保留已经签发的新令牌，占一个重发名额，两条链都有效。
func TestRefreshRotationLostConsumeRaceFromTheSameClientKeepsBothTokens(t *testing.T) {
	f, ic := newInterleaveFixture(t, nil)
	pair0 := f.login(rotIPv4A, rotUAChrome130Win)

	var winnerToken string
	ic.beforeConsume = func() {
		// 并发的另一个请求赢了：它自己的新令牌 + 消费。
		winner, err := f.svc.RefreshTokenPair(rotCtx(rotIPv4A, rotUAChrome130Win), pair0.RefreshToken)
		require.NoError(t, err)
		winnerToken = winner.RefreshToken
	}
	loser, err := f.refresh(rotIPv4A, rotUAChrome130Win, pair0.RefreshToken)
	require.NoError(t, err, "the loser of the consume race is a retry of the same client")
	require.NotEqual(t, winnerToken, loser.RefreshToken)

	_, err = f.refresh(rotIPv4A, rotUAChrome130Win, winnerToken)
	require.NoError(t, err)
	_, err = f.refresh(rotIPv4A, rotUAChrome130Win, loser.RefreshToken)
	require.NoError(t, err)
	require.Equal(t, 1, f.record(pair0.RefreshToken).LeewayReissues, "the loser took a reissue slot")
}

// 输家的指纹和赢家（消费者）不一致：这是别的客户端——丢弃刚签发的新令牌，撤销整个家族。
func TestRefreshRotationLostConsumeRaceFromAnotherClientRevokesTheFamily(t *testing.T) {
	f, ic := newInterleaveFixture(t, nil)
	pair0 := f.login(rotIPv4A, rotUAChrome130Win)

	var winnerToken string
	ic.beforeConsume = func() {
		winner, err := f.svc.RefreshTokenPair(rotCtx(rotIPv4A, rotUAChrome130Win), pair0.RefreshToken)
		require.NoError(t, err)
		winnerToken = winner.RefreshToken
	}
	// 输家来自同一个浏览器/系统家族、别的网段：首次出示的 rebind 放行它，但抢输了消费之后，
	// 它的指纹与赢家（消费者）不一致 → 按重放处理。
	_, err := f.refresh(rotIPv4B, rotUAChrome130Win, pair0.RefreshToken)
	require.ErrorIs(t, err, service.ErrRefreshTokenReuse)
	f.requireSessionRevoked(winnerToken)
	require.Zero(t, f.liveRefreshRecords(), "neither the winner's token nor the loser's discarded token is left alive")
}

// ---------------------------------------------------------------------------
// TC：墓碑的 consumed_at 在「未来」（多实例时钟偏差）
// ---------------------------------------------------------------------------

// 小偏差按「现在」处理（窗口不被拉长）；偏差大到不可信就不给宽限（按重放处理），不会把窗口等量拉长。
func TestRefreshRotationFutureConsumedAtIsClampedNotTrusted(t *testing.T) {
	t.Run("small skew is treated as now", func(t *testing.T) {
		f := newRotFixture(t, nil, nil)
		pair0 := f.login(rotIPv4A, rotUAChrome130Win)
		_, err := f.refresh(rotIPv4A, rotUAChrome130Win, pair0.RefreshToken)
		require.NoError(t, err)
		f.ageConsumedAt(pair0.RefreshToken, -3*time.Second) // consumed_at 在 3 秒之后
		_, err = f.refresh(rotIPv4A, rotUAChrome130Win, pair0.RefreshToken)
		require.NoError(t, err)
	})
	t.Run("large skew does not stretch the window", func(t *testing.T) {
		f := newRotFixture(t, nil, nil)
		pair0 := f.login(rotIPv4A, rotUAChrome130Win)
		first, err := f.refresh(rotIPv4A, rotUAChrome130Win, pair0.RefreshToken)
		require.NoError(t, err)
		f.ageConsumedAt(pair0.RefreshToken, -10*time.Minute) // consumed_at 在 10 分钟之后
		_, err = f.refresh(rotIPv4A, rotUAChrome130Win, pair0.RefreshToken)
		require.ErrorIs(t, err, service.ErrRefreshTokenReuse)
		f.requireSessionRevoked(first.RefreshToken)
	})
}

// ---------------------------------------------------------------------------
// 没有原子存储时不开宽限
// ---------------------------------------------------------------------------

// 缓存没有实现 RefreshTokenAtomicStore（测试替身、将来别的后端）：无法原子地占名额，
// 服务层不开宽限——任何重放都撤销家族（安全方向），不会退回读改写。
func TestRefreshRotationLeewayIsOffWhenTheStoreHasNoAtomicSupport(t *testing.T) {
	user := &service.User{ID: 77, Email: "no-atomic@example.com", PasswordHash: "h", Role: service.RoleUser, Status: service.StatusActive}
	cfg := &config.Config{JWT: config.JWTConfig{
		Secret: "test-no-atomic-secret-0123456789abcdef", ExpireHour: 24, AccessTokenExpireMinutes: 60,
		RefreshTokenExpireDays: 14, SessionMaxLifetimeDays: 90, RefreshTokenReuseLeewaySeconds: 30,
	}}
	svc := service.NewAuthService(nil, newEmailBindUserRepoStub(user), nil, newEmailBindRefreshTokenCacheStub(), cfg, nil, nil, nil, nil, nil, nil, nil, nil)
	ctx := rotCtx(rotIPv4A, rotUAChrome130Win)

	pair0, err := svc.GenerateTokenPair(ctx, user, "")
	require.NoError(t, err)
	first, err := svc.RefreshTokenPair(ctx, pair0.RefreshToken)
	require.NoError(t, err)

	_, err = svc.RefreshTokenPair(ctx, pair0.RefreshToken)
	require.ErrorIs(t, err, service.ErrRefreshTokenReuse, "without atomic support a replay is a replay, even inside the configured leeway")
	_, err = svc.RefreshTokenPair(ctx, first.RefreshToken)
	require.ErrorIs(t, err, service.ErrRefreshTokenInvalid)
}

// ---------------------------------------------------------------------------
// 第二轮评审 N5 / 建议 6：重绑审计限频；准入检查在消费令牌之前
// ---------------------------------------------------------------------------

// 同一会话反复换网络（多出口 NAT / 拿着令牌不停换网络的人）：每次都算重绑、计数照常累加，
// 但只有每个窗口的第一次放行审计行（AuditRebind），多出来的计入 refresh_binding_rebind_audit_suppressed。
func TestRefreshRotationRebindAuditIsLimitedPerSession(t *testing.T) {
	f := newRotFixture(t, nil, nil)
	token := f.login(rotIPv4A, rotUAChrome130Win).RefreshToken
	other := f.login(rotIPv4A, rotUAChrome130Win).RefreshToken // 另一个会话

	audited := 0
	for _, ip := range []string{rotIPv4B, "192.0.2.50", "198.18.0.9", "203.0.113.77"} { // 每次都是新的 /24
		p, err := f.refresh(ip, rotUAChrome130Win, token)
		require.NoError(t, err)
		require.True(t, p.BindingRebound)
		token = p.RefreshToken
		if p.AuditRebind {
			audited++
		}
	}
	require.Equal(t, 1, audited, "one audit row per user+session per window")

	p, err := f.refresh(rotIPv4B, rotUAChrome130Win, other)
	require.NoError(t, err)
	require.True(t, p.AuditRebind, "another session of the same user has its own budget")

	stats := f.svc.AuthStatsSnapshot()
	require.EqualValues(t, 5, stats["refresh_binding_rebind"], "the counter keeps counting every rebind")
	require.EqualValues(t, 3, stats["refresh_binding_rebind_audit_suppressed"])
}

// 准入检查（后端模式只允许管理员）在签发/消费之前执行：被拒时令牌原样保留、没有签发任何新令牌，
// 之后（比如后端模式关掉之后）同一枚令牌照常刷新——不会先轮换再报错。
func TestRefreshRotationGuardRejectsBeforeAnythingIsConsumedOrIssued(t *testing.T) {
	f := newRotFixture(t, nil, nil)
	pair0 := f.login(rotIPv4A, rotUAChrome130Win)
	before := f.liveRefreshRecords()
	errDenied := errors.New("denied by the guard")

	_, err := f.svc.RefreshTokenPairGuarded(rotCtx(rotIPv4A, rotUAChrome130Win), pair0.RefreshToken, func(*service.User) error { return errDenied })
	require.ErrorIs(t, err, errDenied)

	rec := f.record(pair0.RefreshToken)
	require.NotNil(t, rec)
	require.False(t, rec.Consumed, "the guard runs before the old token is consumed")
	require.Equal(t, before, f.liveRefreshRecords(), "and before any new token is issued")
	require.EqualValues(t, 1, f.svc.AuthStatsSnapshot()["refresh_rejected"])

	var seen *service.User
	_, err = f.svc.RefreshTokenPairGuarded(rotCtx(rotIPv4A, rotUAChrome130Win), pair0.RefreshToken, func(u *service.User) error { seen = u; return nil })
	require.NoError(t, err, "the very same token still refreshes once the guard lets it through")
	require.NotNil(t, seen)
	require.EqualValues(t, f.user.ID, seen.ID)
}

// ---------------------------------------------------------------------------
// IP 家族切换计数
// ---------------------------------------------------------------------------

// 靠忽略 IP 家族才一致的刷新（IPv4 <-> IPv6）不算不一致、不 rebind，但单独计数。
func TestRefreshRotationCountsRefreshesThatPassOnlyBecauseTheIPFamilyDiffers(t *testing.T) {
	f := newRotFixture(t, nil, nil)
	pair0 := f.login(rotIPv4A, rotUAChrome130Win)
	rotated, err := f.refresh(rotIPv6, rotUAChrome130Win, pair0.RefreshToken)
	require.NoError(t, err)
	stats := f.svc.AuthStatsSnapshot()
	require.EqualValues(t, 1, stats["refresh_ip_family_switch"])
	require.Zero(t, stats["refresh_binding_rebind"])

	// 新令牌已经绑在 IPv6 上：之后同家族刷新不再计数。
	_, err = f.refresh(rotIPv6, rotUAChrome130Win, rotated.RefreshToken)
	require.NoError(t, err)
	require.EqualValues(t, 1, f.svc.AuthStatsSnapshot()["refresh_ip_family_switch"])
}
