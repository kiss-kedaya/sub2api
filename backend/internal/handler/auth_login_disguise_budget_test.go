package handler

import (
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/pquerna/otp/totp"
	"github.com/stretchr/testify/require"
)

// 审计 AUD-6：登录首步伪装（login_shape_disguise_enabled）里，伪装会话（密码错）与真实会话（密码对）
// 的限速桶必须相通，否则「先把伪装桶打满，再试候选密码」就能从 429 / 401、有没有 Set-Cookie 清除头读出密码是否正确。
//
// 这里的夹具用生产的限速实现（middleware.TwoFactorRateLimiter 的 Redis 滑动窗口、repository.TotpCache 的凭证校验计数）
// 跑在 miniredis 上，不是同口径的内存替身。全部邮箱 / 密码都是假数据。

// budgetStep2 对给定的第一步会话提交一次第二步请求（password 用于 /login/2fa 的密码复核）。
type budgetStep2 func(h *disguiseHarness, v challengeVars, password string) *httptest.ResponseRecorder

// 生产的滑动窗口限速器用 time.Now().UnixNano() 当 ZSET 成员：Linux 上纳秒级时钟下两次请求不会撞成员，
// Windows 的时钟分辨率约 0.5ms，连续两次很快的请求（不带 bcrypt 的 passkey finish）会落在同一刻、
// 被合并成一个成员，桶就「打不满」。每次请求前停 1ms，让本地跑和 CI 一样稳定。
func spaceOutRequests() { time.Sleep(time.Millisecond) }

func totpBudgetStep2(email string) budgetStep2 {
	return func(h *disguiseHarness, v challengeVars, password string) *httptest.ResponseRecorder {
		spaceOutRequests()
		return h.post("/api/v1/auth/login/2fa", login2FABody(v.tempToken, email, password, "000000"))
	}
}

func passkeyBudgetStep2(t *testing.T) budgetStep2 {
	return func(h *disguiseHarness, v challengeVars, _ string) *httptest.ResponseRecorder {
		spaceOutRequests()
		return h.post("/api/v1/auth/login/2fa/passkey/finish",
			passkeyFinishBody(v.tempToken, v.sessionToken, craftAssertion(t, v, correctOrigin, correctRPHash, correctChallenge)))
	}
}

type budgetSubject struct {
	name  string
	email string
	step2 budgetStep2
}

// 第二步的两个入口各自的账号：TOTP 单因子 / TOTP+Passkey 走 /login/2fa，单 Passkey 走 passkey/finish。
func budgetSubjects(t *testing.T, h *disguiseHarness) []budgetSubject {
	totpEmail, bothEmail := h.emailOf(disguiseUserTOTPID), h.emailOf(disguiseUserBothID)
	return []budgetSubject{
		{"totp account", totpEmail, totpBudgetStep2(totpEmail)},
		{"totp+passkey account", bothEmail, totpBudgetStep2(bothEmail)},
		{"passkey account", h.emailOf(disguiseUserPasskeyID), passkeyBudgetStep2(t)},
	}
}

func requireRetryAfterSeconds(t *testing.T, w *httptest.ResponseRecorder) {
	t.Helper()
	seconds, err := strconv.Atoi(w.Header().Get("Retry-After"))
	require.NoError(t, err, "429 必须带 Retry-After")
	require.GreaterOrEqual(t, seconds, 1)
	require.LessOrEqual(t, seconds, 60)
}

// 第二步：把桶打满之后，「候选错密码」与「候选对密码」必须得到同样的响应（状态码、错误码、Retry-After 是否存在）。
// 打满的方式有两种：用必错的密码（伪装会话），或者用对的密码（真实会话）——两个方向都不能有差别。
func TestDisguiseSecondStepBudgetOracleIsClosed(t *testing.T) {
	subjects := budgetSubjects(t, newDisguiseHarnessWithRedis(t, "true"))
	for _, primePassword := range []string{"primer-wrong-password", disguiseTestPassword} {
		for _, subject := range subjects {
			t.Run(subject.name+"/prime with "+primePassword, func(t *testing.T) {
				h := newDisguiseHarnessWithRedis(t, "true")
				primer := h.firstStep(subject.email, primePassword)
				for i := 0; i < 7; i++ { // 同一个会话反复提交：额度照耗，会话不因失败而销毁
					subject.step2(h, primer, primePassword)
				}

				wrong := subject.step2(h, h.firstStep(subject.email, "candidate-wrong-password"), "candidate-wrong-password")
				right := subject.step2(h, h.firstStep(subject.email, disguiseTestPassword), disguiseTestPassword)

				require.Equal(t, http.StatusTooManyRequests, wrong.Code, wrong.Body.String())
				requireRetryAfterSeconds(t, wrong)
				requireRetryAfterSeconds(t, right)
				require.Equal(t, wrong.Code, right.Code, "候选对密码与候选错密码必须不可区分")
				require.Equal(t, wrong.Body.String(), right.Body.String(), "候选对密码与候选错密码必须不可区分")
			})
		}
	}
}

// 对照：不存在的邮箱（HMAC 形状）在同样的脚本下与「存在的邮箱 + 错密码」的反应相同，
// 预算耗尽之后「邮箱存不存在」也读不出来（枚举旁路）。
func TestDisguiseSecondStepBudgetDoesNotRevealWhetherTheEmailExists(t *testing.T) {
	for _, tc := range []struct {
		name    string
		realID  int64
		fake    []service.Factor
		passkey bool
	}{
		{"totp shape", disguiseUserTOTPID, shapeTOTP, false},
		{"passkey shape", disguiseUserPasskeyID, shapePasskey, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			run := func(pick func(h *disguiseHarness) string) transcriptEntry {
				h := newDisguiseHarnessWithRedis(t, "true")
				email := pick(h)
				step2 := totpBudgetStep2(email)
				if tc.passkey {
					step2 = passkeyBudgetStep2(t)
				}
				primer := h.firstStep(email, "primer-wrong-password")
				for i := 0; i < 7; i++ {
					step2(h, primer, "primer-wrong-password")
				}
				w := step2(h, h.firstStep(email, "candidate-wrong-password"), "candidate-wrong-password")
				require.Equal(t, http.StatusTooManyRequests, w.Code, w.Body.String())
				requireRetryAfterSeconds(t, w)
				return record(w)
			}
			existing := run(func(h *disguiseHarness) string { return h.emailOf(tc.realID) })
			unknown := run(func(h *disguiseHarness) string { return findDisguiseEmail(t, h.cfg.JWT.Secret, tc.fake, 0) })
			require.Equal(t, existing, unknown, "存在的邮箱与不存在的邮箱在预算耗尽后必须表现一致")
		})
	}
}

// 首步：带假的因子凭证 Cookie，先用某种密码把「用户 ID + IP」的凭证校验桶打满，之后「候选错密码」与「候选对密码」
// 都不再下发清除头——此前伪装路径用邮箱散列桶、真实路径用用户 ID 桶，两个桶互不相通，对密码仍有清除头。
func TestDisguiseFirstStepGrantBudgetOracleIsClosed(t *testing.T) {
	bogus := []*http.Cookie{
		{Name: service.TwoFactorGrantCookieName, Value: "bogus-totp-grant"},
		{Name: service.TwoFactorPasskeyGrantCookieName, Value: "bogus-passkey-grant"},
	}
	for _, tc := range []struct {
		name string
		id   int64
	}{
		{"totp account", disguiseUserTOTPID},
		{"passkey account", disguiseUserPasskeyID},
		{"totp+passkey account", disguiseUserBothID},
	} {
		t.Run(tc.name, func(t *testing.T) {
			// 对照：桶没满时，错密码与对密码都清掉账号要求的那些因子的 Cookie（探测脚本不是空转）。
			fresh := func(password string) []string {
				h := newDisguiseHarnessWithRedis(t, "true")
				return setCookieSummary(h.loginWithCookies(h.emailOf(tc.id), password, bogus...))
			}
			require.NotEmpty(t, fresh(disguiseTestWrong))
			require.Equal(t, fresh(disguiseTestWrong), fresh(disguiseTestPassword))

			for _, primePassword := range []string{"primer-wrong-password", disguiseTestPassword} {
				h := newDisguiseHarnessWithRedis(t, "true")
				email := h.emailOf(tc.id)
				for i := 0; i < 12; i++ { // 凭证校验限速是每分钟 10 次，12 次请求足以越过它
					h.loginWithCookies(email, primePassword, bogus...)
				}
				wrong := setCookieSummary(h.loginWithCookies(email, "candidate-wrong-password", bogus...))
				right := setCookieSummary(h.loginWithCookies(email, disguiseTestPassword, bogus...))
				require.Equal(t, wrong, right, "桶打满之后（用 %q 打满），候选对密码与候选错密码的 Set-Cookie 必须一致", primePassword)
				require.Empty(t, right, "桶已满：两边都不再下发清除头")
			}
		})
	}
}

// 限速键里并入了客户端 IP：伪装请求只能把「自己这个 IP」的桶打满，锁不到别处的真实用户；
// 伪装会话也从不消耗真实账号按用户 ID 计的账号桶（那个桶只有通过密码校验的真实会话才会消耗）。
// 这是有意的取舍（见 auth_login_disguise.go「限速桶的取舍」）：不要为了堵「换 IP 的探测」把伪装会话改成
// 消耗账号桶——那会让任何人不需要密码就能锁死别人的登录。
func TestDisguiseFloodFromOneIPDoesNotLockTheRealAccountElsewhere(t *testing.T) {
	h := newDisguiseHarnessWithRedis(t, "true")
	email := h.emailOf(disguiseUserTOTPID)
	const accountBucket = "rate_limit:2fa:verify:user:21"
	const attackerIP, userIP = "198.51.100.1:5000", "203.0.113.20:6000"

	// 攻击者在 IP X 上拿必错的密码把桶打满。
	h.remoteAddr = attackerIP
	for i := 0; i < 3; i++ {
		session := h.firstStep(email, "attacker-wrong-password")
		for j := 0; j < 4; j++ {
			h.post("/api/v1/auth/login/2fa", login2FABody(session.tempToken, email, "attacker-wrong-password", "000000"))
		}
	}
	require.False(t, h.redis.Exists(accountBucket), "伪装会话不得消耗真实账号按用户 ID 计的账号桶")
	blocked := h.post("/api/v1/auth/login/2fa", login2FABody(h.firstStep(email, "attacker-wrong-password").tempToken, email, "attacker-wrong-password", "000000"))
	require.Equal(t, http.StatusTooManyRequests, blocked.Code)

	// 真实用户在 IP Y 上：不受影响，输对动态码照常登录成功。
	h.remoteAddr = userIP
	session := h.firstStep(email, disguiseTestPassword)
	code, err := totp.GenerateCode(h.secrets[disguiseUserTOTPID], time.Now())
	require.NoError(t, err)
	ok := h.post("/api/v1/auth/login/2fa", login2FABody(session.tempToken, email, disguiseTestPassword, code))
	require.Equal(t, http.StatusOK, ok.Code, ok.Body.String())
	require.Contains(t, ok.Body.String(), "access_token")

	// 同一个 IP X 上的真实会话与被打满的伪装会话共用桶（这正是堵住密码有效性探测的那一半）。
	h.remoteAddr = attackerIP
	sameIP := h.post("/api/v1/auth/login/2fa", login2FABody(h.firstStep(email, disguiseTestPassword).tempToken, email, disguiseTestPassword, "000000"))
	require.Equal(t, http.StatusTooManyRequests, sameIP.Code, "同一个 IP：真实会话与被打满的伪装会话共用桶")
}

// 真实会话在第二步先过「账号 + IP」桶、再走账号桶，成功即把两个桶一起清零；
// 开关关闭时只有旧的账号桶（响应与旧实现逐字节一致的前提之一：不多一次限速调用）。
func TestDisguiseRealSessionBucketsAreConsumedAndClearedTogether(t *testing.T) {
	twoFactorKeys := func(h *disguiseHarness) []string {
		var keys []string
		for _, key := range h.redis.Keys() {
			if strings.HasPrefix(key, "rate_limit:2fa:verify:user:") {
				keys = append(keys, key)
			}
		}
		return keys
	}

	t.Run("switch on", func(t *testing.T) {
		h := newDisguiseHarnessWithRedis(t, "true")
		email := h.emailOf(disguiseUserTOTPID)
		session := h.firstStep(email, disguiseTestPassword)
		w := h.post("/api/v1/auth/login/2fa", login2FABody(session.tempToken, email, disguiseTestPassword, "000000"))
		require.Equal(t, http.StatusUnauthorized, w.Code, w.Body.String())
		keys := twoFactorKeys(h)
		require.Len(t, keys, 2, "账号桶 + 账号与 IP 桶：%v", keys)
		require.Contains(t, keys, "rate_limit:2fa:verify:user:21")

		code, err := totp.GenerateCode(h.secrets[disguiseUserTOTPID], time.Now())
		require.NoError(t, err)
		w = h.post("/api/v1/auth/login/2fa", login2FABody(session.tempToken, email, disguiseTestPassword, code))
		require.Equal(t, http.StatusOK, w.Code, w.Body.String())
		require.Empty(t, twoFactorKeys(h), "成功即清：两个桶一起清零")
	})

	t.Run("switch off", func(t *testing.T) {
		h := newDisguiseHarnessWithRedis(t, "false")
		email := h.emailOf(disguiseUserTOTPID)
		session := h.firstStep(email, disguiseTestPassword)
		w := h.post("/api/v1/auth/login/2fa", login2FABody(session.tempToken, email, disguiseTestPassword, "000000"))
		require.Equal(t, http.StatusUnauthorized, w.Code, w.Body.String())
		require.Equal(t, []string{"rate_limit:2fa:verify:user:21"}, twoFactorKeys(h), "开关关闭：只有旧的账号桶")
	})
}

func TestLoginStepTwoIPBucketKey(t *testing.T) {
	const minKey = int64(1) << 40
	seen := map[int64]string{}
	hashed := loginChallengeRateLimitKey("a@example.test")
	for _, account := range []int64{1, 21, 22, 14000, minKey + 5, hashed} {
		for _, clientIP := range []string{"", "192.0.2.1", "198.51.100.7", "2001:db8::1"} {
			key := loginStepTwoIPBucketKey(account, clientIP)
			require.GreaterOrEqual(t, key, minKey, "不得与自增的真实用户 ID 相撞")
			require.Equal(t, key, loginStepTwoIPBucketKey(account, clientIP), "同一个（账号, IP）必须得到同一个键")
			label := strconv.FormatInt(account, 10) + "|" + clientIP
			if prev, dup := seen[key]; dup {
				t.Fatalf("键冲突：%s 与 %s", prev, label)
			}
			seen[key] = label
		}
	}
	require.NotEqual(t, hashed, loginStepTwoIPBucketKey(hashed, ""))
}
