//go:build unit

package service

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

const loginDisguiseTestSecret = "login-disguise-test-secret-at-least-32-bytes"

func newTestLoginDisguise(t *testing.T) *LoginDisguise {
	t.Helper()
	d := NewLoginDisguise(loginDisguiseTestSecret)
	require.NotNil(t, d)
	return d
}

func syntheticDisguiseEmail(i int) string {
	return fmt.Sprintf("synthetic-%d@example.test", i)
}

func TestNewLoginDisguiseRequiresSecret(t *testing.T) {
	require.Nil(t, NewLoginDisguise(""))
	require.Nil(t, NewLoginDisguise("   "))
	require.NotNil(t, NewLoginDisguise("x"))
}

// 同一个邮箱永远得到同一种形状：重复探测、大小写 / 首尾空白、另一个进程实例（同密钥）都一样。
func TestLoginDisguiseShapeIsDeterministicPerEmail(t *testing.T) {
	d := newTestLoginDisguise(t)
	other := NewLoginDisguise(loginDisguiseTestSecret)
	for i := 0; i < 500; i++ {
		email := syntheticDisguiseEmail(i)
		want := d.Shape(email, true)
		for j := 0; j < 3; j++ {
			require.Equal(t, want, d.Shape(email, true))
		}
		require.Equal(t, want, d.Shape("  "+email+"  ", true), "首尾空白不得改变形状")
		require.Equal(t, want, d.Shape(upperASCII(email), true), "大小写不得改变形状")
		require.Equal(t, want, other.Shape(email, true), "同一密钥的另一个实例必须给出同一形状")
	}
}

func upperASCII(s string) string {
	out := []byte(s)
	for i, c := range out {
		if c >= 'a' && c <= 'z' {
			out[i] = c - 'a' + 'A'
		}
	}
	return string(out)
}

// 换密钥后形状整体变化（密钥确实参与了 HMAC），不能被邮箱单独推出。
func TestLoginDisguiseShapeDependsOnSecret(t *testing.T) {
	a := newTestLoginDisguise(t)
	b := NewLoginDisguise(loginDisguiseTestSecret + "-rotated")
	differing := 0
	for i := 0; i < 2000; i++ {
		if !slices.Equal(a.Shape(syntheticDisguiseEmail(i), true), b.Shape(syntheticDisguiseEmail(i), true)) {
			differing++
		}
	}
	// 两个独立的 90/5/5 分布，约 19% 的邮箱形状不同；只要远大于 0 就说明密钥参与了计算。
	require.Greater(t, differing, 200)
}

// 形状集合 = 现网真实挑战会出现的全部形状；占比 = 常量里的默认值。
func TestLoginDisguiseShapeDistributionMatchesWeights(t *testing.T) {
	d := newTestLoginDisguise(t)
	const n = 30000
	counts := map[string]int{}
	for i := 0; i < n; i++ {
		shape := d.Shape(syntheticDisguiseEmail(i), true)
		require.NotEmpty(t, shape)
		counts[fmt.Sprint(FactorStrings(shape))]++
	}
	require.Len(t, counts, 3, "必须覆盖 [totp] [passkey] [totp passkey] 三种形状")
	for key, weight := range map[string]uint32{
		"[totp]":         loginDisguiseShapeTOTPWeight,
		"[passkey]":      loginDisguiseShapePasskeyWeight,
		"[totp passkey]": loginDisguiseShapeTOTPPasskeyWeight,
	} {
		got := float64(counts[key]) / n * 100
		require.InDelta(t, float64(weight), got, 1.0, "形状 %s 的占比应接近 %d%%，实际 %.2f%%", key, weight, got)
	}
}

// 与 TotpService.LoginRequiredFactors 的取值集合逐一对照：真实挑战只可能是这三种（空集合直接签发令牌，没有挑战）。
func TestLoginDisguiseShapesAreRealPossibleShapes(t *testing.T) {
	d := newTestLoginDisguise(t)
	realShapes := map[string]bool{
		fmt.Sprint([]Factor{FactorTOTP}):                true,
		fmt.Sprint([]Factor{FactorPasskey}):             true,
		fmt.Sprint([]Factor{FactorTOTP, FactorPasskey}): true,
	}
	for i := 0; i < 3000; i++ {
		shape := d.Shape(syntheticDisguiseEmail(i), true)
		require.True(t, realShapes[fmt.Sprint(shape)], "伪装出现了现网不会有的形状 %v", shape)
		require.Equal(t, sortFactorsForChallenge(shape), shape, "因子顺序必须是 totp → passkey")
	}
}

// Passkey 服务没有配置时，单 Passkey 形状无法给出 assertion 选项，按 TOTP 单因子处理，且仍然稳定。
func TestLoginDisguiseShapeWithoutPasskeyService(t *testing.T) {
	d := newTestLoginDisguise(t)
	for i := 0; i < 3000; i++ {
		email := syntheticDisguiseEmail(i)
		with := d.Shape(email, true)
		without := d.Shape(email, false)
		require.Equal(t, without, d.Shape(email, false))
		if slices.Equal(with, []Factor{FactorPasskey}) {
			require.Equal(t, []Factor{FactorTOTP}, without)
			continue
		}
		require.Equal(t, with, without, "只有单 Passkey 形状受 Passkey 服务可用性影响")
	}
}

func TestLoginDisguisePasskeyCredentialsStayInsideRealDistribution(t *testing.T) {
	d := newTestLoginDisguise(t)
	allowedLengths := map[int]bool{}
	for _, c := range loginDisguiseCredentialIDLengths {
		allowedLengths[c.value] = true
	}
	allowedCounts := map[int]bool{}
	for _, c := range loginDisguiseCredentialCounts {
		allowedCounts[c.value] = true
	}
	allowedTransports := map[string]bool{}
	for _, c := range loginDisguiseTransportSets {
		allowedTransports[fmt.Sprint(c.value)] = true
	}

	lengthSeen := map[int]int{}
	countSeen := map[int]int{}
	transportSeen := map[string]int{}
	for i := 0; i < 4000; i++ {
		email := syntheticDisguiseEmail(i)
		credentials := d.PasskeyCredentials(email)
		require.True(t, allowedCounts[len(credentials)], "个数 %d 不在分布里", len(credentials))
		countSeen[len(credentials)]++
		ids := map[string]bool{}
		for _, credential := range credentials {
			require.True(t, allowedLengths[len(credential.ID)], "ID 字节数 %d 不在分布里", len(credential.ID))
			lengthSeen[len(credential.ID)]++
			require.False(t, ids[string(credential.ID)], "同一个邮箱的凭据 ID 不能重复")
			ids[string(credential.ID)] = true
			require.True(t, allowedTransports[fmt.Sprint(credential.Transport)], "transports %v 不在分布里", credential.Transport)
			transportSeen[fmt.Sprint(credential.Transport)]++
			require.Empty(t, credential.PublicKey, "诱饵凭据没有公钥，永远验不过")
		}
		// 同一个邮箱稳定：重复生成、大小写 / 空白变化都不改变任何一个字节。
		again := d.PasskeyCredentials("  " + upperASCII(email) + " ")
		require.Equal(t, credentials, again)
	}
	require.Len(t, lengthSeen, len(allowedLengths), "每种 ID 长度都应出现过")
	require.Len(t, countSeen, len(allowedCounts), "每种个数都应出现过")
	require.Len(t, transportSeen, len(allowedTransports), "每种 transports 组合都应出现过")
	// 凭据 ID 之间互不相同（HMAC 输出，不是常量）。
	a := d.PasskeyCredentials("a@example.test")
	b := d.PasskeyCredentials("b@example.test")
	require.NotEqual(t, a[0].ID[:16], b[0].ID[:16])
}

func TestLoginDisguiseUserIDScopeStaysInRange(t *testing.T) {
	d := newTestLoginDisguise(t)
	for _, space := range []int64{1, 2, 1000, 16384} {
		for i := 0; i < 2000; i++ {
			scope := d.UserIDScope(syntheticDisguiseEmail(i), space)
			require.GreaterOrEqual(t, scope, int64(1))
			require.LessOrEqual(t, scope, space)
			require.Equal(t, scope, d.UserIDScope(upperASCII(syntheticDisguiseEmail(i)), space))
		}
	}
	require.Equal(t, int64(1), d.UserIDScope("x@example.test", 0), "非法上界按 1 处理")
}

func TestNextPowerOfTwo(t *testing.T) {
	for in, want := range map[int64]int64{
		-5: 1, 0: 1, 1: 1, 2: 2, 3: 4, 4: 4, 5: 8, 1000: 1024, 1024: 1024, 1025: 2048, 14000: 16384, 16384: 16384, 16385: 32768,
	} {
		require.Equal(t, want, nextPowerOfTwo(in), "nextPowerOfTwo(%d)", in)
	}
}

func TestLoginDisguisePasswordProbe(t *testing.T) {
	d := newTestLoginDisguise(t)
	probe := d.PasswordProbe("User@Example.test", "p-1")
	require.Len(t, probe, 64)
	require.NotContains(t, probe, "p-1")
	require.True(t, d.PasswordProbeMatches(" user@example.TEST ", "p-1", probe), "邮箱规范化后必须一致")
	require.False(t, d.PasswordProbeMatches("user@example.test", "p-2", probe))
	require.False(t, d.PasswordProbeMatches("other@example.test", "p-1", probe))
	require.False(t, d.PasswordProbeMatches("user@example.test", "p-1", ""), "没有指纹一律不匹配")
	require.False(t, (*LoginDisguise)(nil).PasswordProbeMatches("user@example.test", "p-1", probe))
	other := NewLoginDisguise(loginDisguiseTestSecret + "-rotated")
	require.NotEqual(t, probe, other.PasswordProbe("user@example.test", "p-1"), "指纹带服务端密钥，不能离线反推")
}

func TestLoginDisguiseUserHandleIsStableAndPrivate(t *testing.T) {
	d := newTestLoginDisguise(t)
	require.Len(t, d.UserHandle("a@example.test"), 32)
	require.Equal(t, d.UserHandle("a@example.test"), d.UserHandle("A@example.test"))
	require.NotEqual(t, d.UserHandle("a@example.test"), d.UserHandle("b@example.test"))
}

// ---- PasskeyService 上的伪装素材：用户 ID 上界 ----

type maxIDReaderStub struct {
	UserRepository
	maxID int64
	err   error
	calls atomic.Int32
}

func (r *maxIDReaderStub) MaxUserID(context.Context) (int64, error) {
	r.calls.Add(1)
	return r.maxID, r.err
}

func TestPasskeyServiceDisguiseUserIDSpace(t *testing.T) {
	ctx := context.Background()

	// 仓储没有实现 UserMaxIDReader：用保守常量。
	require.Equal(t, loginDisguiseUserIDSpaceFallback, (&PasskeyService{userRepo: &factorPolicyUserRepo{}}).disguiseUserIDSpace(ctx))

	// 现网最大用户 ID 14000 → 取 2 的幂 16384，并在 TTL 内缓存。
	reader := &maxIDReaderStub{maxID: 14000}
	svc := &PasskeyService{userRepo: reader}
	require.Equal(t, int64(16384), svc.disguiseUserIDSpace(ctx))
	require.Equal(t, int64(16384), svc.disguiseUserIDSpace(ctx))
	require.EqualValues(t, 1, reader.calls.Load(), "上界应被缓存，不能逐次查库")

	// 太小的最大 ID 不低于下限。
	small := &PasskeyService{userRepo: &maxIDReaderStub{maxID: 3}}
	require.Equal(t, loginDisguiseUserIDSpaceFloor, small.disguiseUserIDSpace(ctx))

	// 读取失败：没有历史值用常量；有历史值沿用历史值。
	failing := &PasskeyService{userRepo: &maxIDReaderStub{err: errors.New("db down")}}
	require.Equal(t, loginDisguiseUserIDSpaceFallback, failing.disguiseUserIDSpace(ctx))
	failing.idSpace.space = 4096
	failing.idSpace.at = failing.idSpace.at.Add(-2 * disguiseIDSpaceTTL)
	require.Equal(t, int64(4096), failing.disguiseUserIDSpace(ctx))
}

// 用户 ID 上界缓存被并发调用时没有数据竞争，并且只查一次库。
func TestPasskeyServiceDisguiseUserIDSpaceIsSafeForConcurrentUse(t *testing.T) {
	reader := &maxIDReaderStub{maxID: 14000}
	svc := &PasskeyService{userRepo: reader}
	var (
		wg         sync.WaitGroup
		mismatches atomic.Int32
	)
	for i := 0; i < 32; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 50; j++ {
				if svc.disguiseUserIDSpace(context.Background()) != 16384 {
					mismatches.Add(1)
				}
			}
		}()
	}
	wg.Wait()
	require.Zero(t, mismatches.Load())
	require.EqualValues(t, 1, reader.calls.Load())
}

// ---- 用户 ID 上界缓存：查询失败 / 变慢（审计 AUD-6） ----

// hungMaxIDReader 模拟读库卡住：一直等到调用方给的 ctx 到期（预算用完）才返回超时错误。
type hungMaxIDReader struct {
	UserRepository
	calls atomic.Int32
}

func (r *hungMaxIDReader) MaxUserID(ctx context.Context) (int64, error) {
	r.calls.Add(1)
	<-ctx.Done()
	return 0, ctx.Err()
}

// 读库超时失败时：并发的伪装登录只共用一次查询（不是每个请求各查一次、持锁排队），失败结果被缓存，
// 缓存期内的后续请求既不查库也不等待。此前 N 个并发要等 N × 500ms，且每个请求都再查一次。
func TestPasskeyServiceDisguiseUserIDSpaceFailureIsSharedAndCached(t *testing.T) {
	reader := &hungMaxIDReader{}
	svc := &PasskeyService{userRepo: reader}
	const callers = 8

	start := time.Now()
	var wg sync.WaitGroup
	results := make([]int64, callers)
	for i := 0; i < callers; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			results[i] = svc.disguiseUserIDSpace(context.Background())
		}(i)
	}
	wg.Wait()
	elapsed := time.Since(start)

	require.EqualValues(t, 1, reader.calls.Load(), "并发请求必须共用同一次查询")
	require.Less(t, elapsed, 3*disguiseIDSpaceQueryBudget, "不得持锁串行：%d 个并发的总耗时应接近一次预算，实际 %v", callers, elapsed)
	for _, space := range results {
		require.Equal(t, loginDisguiseUserIDSpaceFallback, space, "没有历史值时用保守常量")
	}

	// 失败结果被缓存：缓存期内不再查库，也不等待。
	again := time.Now()
	for i := 0; i < 50; i++ {
		require.Equal(t, loginDisguiseUserIDSpaceFallback, svc.disguiseUserIDSpace(context.Background()))
	}
	require.EqualValues(t, 1, reader.calls.Load(), "失败结果缓存期内不得重查")
	require.Less(t, time.Since(again), disguiseIDSpaceQueryBudget/2)

	// 缓存期过后才重试一次。
	svc.idSpace.mu.Lock()
	svc.idSpace.failAt = svc.idSpace.failAt.Add(-2 * disguiseIDSpaceFailureTTL)
	svc.idSpace.mu.Unlock()
	require.Equal(t, loginDisguiseUserIDSpaceFallback, svc.disguiseUserIDSpace(context.Background()))
	require.EqualValues(t, 2, reader.calls.Load())
}

// 失败期间沿用上一次成功的值（而不是退回保守常量）；成功结果过期、重查失败时同样如此，失败也只重试一次 / 10 秒。
func TestPasskeyServiceDisguiseUserIDSpaceKeepsLastGoodValueWhileFailing(t *testing.T) {
	reader := &maxIDReaderStub{maxID: 3000}
	svc := &PasskeyService{userRepo: reader}
	require.Equal(t, int64(4096), svc.disguiseUserIDSpace(context.Background()))

	// 成功结果过期，之后读库一直失败。
	svc.idSpace.mu.Lock()
	svc.idSpace.at = svc.idSpace.at.Add(-2 * disguiseIDSpaceTTL)
	svc.idSpace.mu.Unlock()
	reader.err = errors.New("statement timeout")
	for i := 0; i < 20; i++ {
		require.Equal(t, int64(4096), svc.disguiseUserIDSpace(context.Background()), "失败期间沿用上一次成功的值")
	}
	require.EqualValues(t, 2, reader.calls.Load(), "一次成功 + 一次失败重查，失败期内不再重查")

	// 库恢复后，失败缓存期过去就能拿到新值。
	reader.err, reader.maxID = nil, 9000
	svc.idSpace.mu.Lock()
	svc.idSpace.failAt = svc.idSpace.failAt.Add(-2 * disguiseIDSpaceFailureTTL)
	svc.idSpace.mu.Unlock()
	require.Equal(t, int64(16384), svc.disguiseUserIDSpace(context.Background()))
}

// ctxAwareMaxIDReader 像真实的库读一样尊重 ctx：ctx 已经取消 / 到期就返回错误。
type ctxAwareMaxIDReader struct {
	UserRepository
	maxID int64
	calls atomic.Int32
}

func (r *ctxAwareMaxIDReader) MaxUserID(ctx context.Context) (int64, error) {
	r.calls.Add(1)
	if err := ctx.Err(); err != nil {
		return 0, err
	}
	return r.maxID, nil
}

// 合并的查询不跟随发起请求的取消：第一个请求的 ctx 已经取消，查询照样完成并写入缓存，
// 不会把一次取消变成所有请求的「读库失败」（用 4096 与保守常量 16384 区分「读到了」和「退回常量」）。
func TestPasskeyServiceDisguiseUserIDSpaceQuerySurvivesCallerCancellation(t *testing.T) {
	reader := &ctxAwareMaxIDReader{maxID: 3000}
	svc := &PasskeyService{userRepo: reader}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	require.Equal(t, int64(4096), svc.disguiseUserIDSpace(ctx))
	require.Equal(t, int64(4096), svc.disguiseUserIDSpace(context.Background()))
	require.EqualValues(t, 1, reader.calls.Load())
}
