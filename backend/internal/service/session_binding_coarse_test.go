//go:build unit

package service_test

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/service"
)

// 粗粒度指纹：UA 只取「浏览器家族 + 系统家族」（去掉版本号），IP 只在两边 IP 家族相同时才比较网段。

func coarseLabel(ip, ua string) string {
	return service.BindingLabel((&service.SessionBinding{IP: ip, UserAgent: ua}).CoarseFingerprint())
}

func TestCoarseFingerprintUserAgentFamilies(t *testing.T) {
	cases := []struct {
		name string
		ua   string
		want string
	}{
		{"chrome windows", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/130.0.0.0 Safari/537.36", "chrome/windows"},
		{"edge windows", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/130.0.0.0 Safari/537.36 Edg/130.0.2849.56", "edge/windows"},
		{"firefox linux", "Mozilla/5.0 (X11; Linux x86_64; rv:132.0) Gecko/20100101 Firefox/132.0", "firefox/linux"},
		{"safari ios (like Mac OS X)", "Mozilla/5.0 (iPhone; CPU iPhone OS 17_5 like Mac OS X) AppleWebKit/605.1.15 (KHTML, like Gecko) Version/17.5 Mobile/15E148 Safari/604.1", "safari/ios"},
		{"chrome ios", "Mozilla/5.0 (iPhone; CPU iPhone OS 17_5 like Mac OS X) AppleWebKit/605.1.15 (KHTML, like Gecko) CriOS/130.0.6723.37 Mobile/15E148 Safari/604.1", "chrome/ios"},
		{"chrome android (also says Linux)", "Mozilla/5.0 (Linux; Android 13; Pixel 7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/130.0.0.0 Mobile Safari/537.36", "chrome/android"},
		{"samsung browser", "Mozilla/5.0 (Linux; Android 13; SM-S911B) AppleWebKit/537.36 (KHTML, like Gecko) SamsungBrowser/25.0 Chrome/121.0.0.0 Mobile Safari/537.36", "samsung/android"},
		{"wechat in-app browser", "Mozilla/5.0 (Linux; Android 13; PGT-AN10 Build/HONORPGT) AppleWebKit/537.36 (KHTML, like Gecko) Version/4.0 Chrome/116.0.0.0 Mobile Safari/537.36 MicroMessenger/8.0.47.2560", "wechat/android"},
		{"safari macos", "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/605.1.15 (KHTML, like Gecko) Version/17.5 Safari/605.1.15", "safari/macos"},
		{"opera", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/130.0.0.0 Safari/537.36 OPR/115.0.0.0", "opera/windows"},
		{"chromeos", "Mozilla/5.0 (X11; CrOS x86_64 14541.0.0) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/130.0.0.0 Safari/537.36", "chrome/chromeos"},
		{"curl", "curl/8.4.0", "curl/other"},
		{"node", "node", "node/other"},
		{"python requests", "python-requests/2.31.0", "python-requests/other"},
		{"go http client", "Go-http-client/2.0", "go-http-client/other"},
		{"empty user agent", "", "none/none"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			require.Equal(t, tc.want+"/ipv4", coarseLabel("203.0.113.10", tc.ua))
		})
	}
}

// 标签里不能出现 IP 原文、版本号或完整 UA。
func TestBindingLabelCarriesNoIPVersionOrFullUserAgent(t *testing.T) {
	binding := &service.SessionBinding{
		IP:        "203.0.113.10",
		UserAgent: "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/130.0.6723.92 Safari/537.36",
	}
	coarse := binding.CoarseFingerprint()
	label := service.BindingLabel(coarse)
	for _, forbidden := range []string{"203.0.113", "130", "537", "Mozilla", "AppleWebKit", "10.0"} {
		require.NotContains(t, coarse, forbidden)
		require.NotContains(t, label, forbidden)
	}
	require.Equal(t, "chrome/windows/ipv4", label)
	require.Equal(t, "unknown", service.BindingLabel(""), "a legacy token has no coarse fingerprint")
	require.Equal(t, "unknown", service.BindingLabel("garbage"))
	require.Empty(t, (&service.SessionBinding{}).CoarseFingerprint(), "nothing to bind on")
	require.Empty(t, (*service.SessionBinding)(nil).CoarseFingerprint())
}

func TestSessionBindingMatches(t *testing.T) {
	const (
		chrome130 = "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/130.0.0.0 Safari/537.36"
		chrome131 = "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/131.0.0.0 Safari/537.36"
		firefox   = "Mozilla/5.0 (X11; Linux x86_64; rv:132.0) Gecko/20100101 Firefox/132.0"
		loose     = config.SessionBindingModeLoose
		strict    = config.SessionBindingModeStrict
	)
	issue := func(mode, ip, ua string) (hash, coarse string) {
		b := &service.SessionBinding{IP: ip, UserAgent: ua}
		hash = b.HashForMode(mode)
		if hash != "" {
			coarse = b.CoarseFingerprint()
		}
		return hash, coarse
	}

	cases := []struct {
		name         string
		mode         string
		issuedIP     string
		issuedUA     string
		requestIP    string
		requestUA    string
		stripCoarse  bool // 模拟升级前签发的令牌：只有旧算法的哈希
		customCoarse string
		want         bool
	}{
		{"same client", loose, "203.0.113.10", chrome130, "203.0.113.10", chrome130, false, "", true},
		{"same /24", loose, "203.0.113.10", chrome130, "203.0.113.250", chrome130, false, "", true},
		{"browser version bump", loose, "203.0.113.10", chrome130, "203.0.113.10", chrome131, false, "", true},
		{"ipv4 to ipv6", loose, "203.0.113.10", chrome130, "2001:db8:abcd:12::1", chrome130, false, "", true},
		{"ipv6 to ipv4", loose, "2001:db8:abcd:12::1", chrome130, "203.0.113.10", chrome130, false, "", true},
		{"ipv6 /64 drift", loose, "2001:db8:abcd:12::1", chrome130, "2001:db8:abcd:12:ffff::9", chrome130, false, "", true},
		{"other ipv6 /64", loose, "2001:db8:abcd:12::1", chrome130, "2001:db8:abcd:99::1", chrome130, false, "", false},
		{"other /24 same family", loose, "203.0.113.10", chrome130, "198.51.100.10", chrome130, false, "", false},
		{"other browser", loose, "203.0.113.10", chrome130, "203.0.113.10", firefox, false, "", false},
		{"other browser and family switch", loose, "203.0.113.10", chrome130, "2001:db8::1", firefox, false, "", false},
		{"legacy token same client", loose, "203.0.113.10", chrome130, "203.0.113.77", chrome130, true, "", true},
		{"legacy token version bump (no coarse to forgive it)", loose, "203.0.113.10", chrome130, "203.0.113.10", chrome131, true, "", false},
		{"legacy token family switch (no coarse to forgive it)", loose, "203.0.113.10", chrome130, "2001:db8::1", chrome130, true, "", false},
		{"garbage coarse never forgives a hash mismatch", loose, "203.0.113.10", chrome130, "203.0.113.10", chrome131, false, "not|a|coarse", false},
		{"strict same client", strict, "203.0.113.10", chrome130, "203.0.113.10", chrome130, false, "", true},
		{"strict ignores the coarse form: version bump", strict, "203.0.113.10", chrome130, "203.0.113.10", chrome131, false, "", false},
		{"strict ignores the coarse form: same /24", strict, "203.0.113.10", chrome130, "203.0.113.11", chrome130, false, "", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			hash, coarse := issue(tc.mode, tc.issuedIP, tc.issuedUA)
			if tc.stripCoarse {
				coarse = ""
			}
			if tc.customCoarse != "" {
				coarse = tc.customCoarse
			}
			current := &service.SessionBinding{IP: tc.requestIP, UserAgent: tc.requestUA}
			require.Equal(t, tc.want, current.Matches(tc.mode, hash, coarse))
		})
	}

	t.Run("nothing to compare means let through", func(t *testing.T) {
		hash, coarse := issue(loose, "203.0.113.10", chrome130)
		require.True(t, (*service.SessionBinding)(nil).Matches(loose, hash, coarse), "no fingerprint on this request")
		require.True(t, (&service.SessionBinding{}).Matches(loose, hash, coarse), "no fingerprint on this request")
		require.True(t, (&service.SessionBinding{IP: "203.0.113.99", UserAgent: firefox}).Matches(loose, "", ""), "the token carries no binding")
		require.True(t, (&service.SessionBinding{IP: "203.0.113.99", UserAgent: firefox}).Matches(config.SessionBindingModeOff, hash, coarse), "binding is off")
	})
}

// 新旧版本混跑：粗粒度指纹是额外字段，旧字段（binding_hash / bnd）的算法没变——
// 这里用独立的重新计算把算法钉死，旧实例拿到新实例签发的令牌仍然能正确比对。
func TestBindingHashAlgorithmIsUnchangedForOldInstances(t *testing.T) {
	legacyHash := func(ipPart, ua string) string {
		sum := sha256.Sum256([]byte(ipPart + "\n" + ua))
		return hex.EncodeToString(sum[:16])
	}
	b := &service.SessionBinding{IP: "203.0.113.10", UserAgent: "ua"}
	require.Equal(t, legacyHash("203.0.113.0/24", "ua"), b.HashForMode(config.SessionBindingModeLoose))
	require.Equal(t, legacyHash("203.0.113.10", "ua"), b.HashForMode(config.SessionBindingModeStrict))
	v6 := &service.SessionBinding{IP: "2001:db8:abcd:12::1", UserAgent: "ua"}
	require.Equal(t, legacyHash("2001:db8:abcd:12::/64", "ua"), v6.HashForMode(config.SessionBindingModeLoose))
}

// 数据兼容：升级前 Redis 里的旧记录与旧 JWT 都没有新字段，解码后新字段是零值，行为回退到旧逻辑。
func TestRefreshTokenDataAndClaimsStayCompatibleWithPreUpgradeJSON(t *testing.T) {
	legacy := `{"user_id":7,"token_version":42,"family_id":"fam","binding_hash":"abc","consumed":true,"created_at":"2026-11-01T00:00:00Z","expires_at":"2026-12-01T00:00:00Z"}`
	var data service.RefreshTokenData
	require.NoError(t, json.Unmarshal([]byte(legacy), &data))
	require.True(t, data.Consumed)
	require.True(t, data.ConsumedAt.IsZero(), "an old tombstone has no consume time: the old replay-revokes-family behavior applies")
	require.Zero(t, data.LeewayReissues)
	require.Empty(t, data.BindingCoarse)
	require.True(t, data.SessionStartedAt.IsZero())
	require.True(t, data.SessionStart().Equal(time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC)), "session start falls back to CreatedAt")

	// 新字段只在有值时才写进 JWT，旧版本解码新 JWT 时忽略它们。
	empty, err := json.Marshal(&service.JWTClaims{UserID: 1})
	require.NoError(t, err)
	require.NotContains(t, string(empty), `"bcs"`)
	withCoarse, err := json.Marshal(&service.JWTClaims{UserID: 1, BindingCoarse: "chrome|windows|4|abcd1234"})
	require.NoError(t, err)
	require.Contains(t, string(withCoarse), `"bcs":"chrome|windows|4|abcd1234"`)
}

// Match 三态：一致 / 靠忽略 IP 家族才一致 / 不一致。后者要被调用方单独计数。
func TestSessionBindingMatchTellsIPFamilySwitchApart(t *testing.T) {
	const (
		chrome130 = "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/130.0.0.0 Safari/537.36"
		chrome131 = "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/131.0.0.0 Safari/537.36"
		firefox   = "Mozilla/5.0 (X11; Linux x86_64; rv:132.0) Gecko/20100101 Firefox/132.0"
		loose     = config.SessionBindingModeLoose
	)
	issued := &service.SessionBinding{IP: "203.0.113.10", UserAgent: chrome130}
	hash, coarse := issued.HashForMode(loose), issued.CoarseFingerprint()

	cases := []struct {
		name string
		b    *service.SessionBinding
		want service.BindingMatch
	}{
		{"same client", &service.SessionBinding{IP: "203.0.113.10", UserAgent: chrome130}, service.BindingMatched},
		{"same /24 + version bump", &service.SessionBinding{IP: "203.0.113.99", UserAgent: chrome131}, service.BindingMatched},
		{"ipv4 -> ipv6, same browser/OS", &service.SessionBinding{IP: "2001:db8::1", UserAgent: chrome130}, service.BindingMatchedAcrossIPFamily},
		{"ipv4 -> ipv6 + version bump", &service.SessionBinding{IP: "2001:db8::1", UserAgent: chrome131}, service.BindingMatchedAcrossIPFamily},
		{"other /24, same family", &service.SessionBinding{IP: "198.51.100.10", UserAgent: chrome130}, service.BindingMismatch},
		{"ipv4 -> ipv6 + other browser/OS", &service.SessionBinding{IP: "2001:db8::1", UserAgent: firefox}, service.BindingMismatch},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			require.Equal(t, tc.want, tc.b.Match(loose, hash, coarse))
			require.Equal(t, tc.want != service.BindingMismatch, tc.b.Matches(loose, hash, coarse), "Matches is Match != mismatch")
		})
	}
}

// 已知限制（评审 P5，写成测试免得被误当成屏障）：令牌绑在 IPv4 上时，攻击者换到任意 IPv6 地址、
// 把 UA 写成同样的浏览器家族 + 系统家族（版本号随便写），比较照样通过——只是被单独计数。
func TestSessionBindingKnownLimitAnotherIPFamilyWithASpoofedUserAgentPasses(t *testing.T) {
	loose := config.SessionBindingModeLoose
	victim := &service.SessionBinding{IP: "203.0.113.10", UserAgent: "Mozilla/5.0 (Windows NT 10.0; Win64; x64) Chrome/130.0.0.0 Safari/537.36"}
	attacker := &service.SessionBinding{IP: "2001:db8:dead:beef::1", UserAgent: "Mozilla/5.0 (Windows NT 10.0) Chrome/1 Safari/1"}

	require.Equal(t, service.BindingMatchedAcrossIPFamily, attacker.Match(loose, victim.HashForMode(loose), victim.CoarseFingerprint()),
		"the binding is a drift detector, not a theft barrier: the IP is not compared across IP families")
	// 但不是完全没有门槛：浏览器/系统家族对不上、或同家族换了网段，仍然被拦。
	script := &service.SessionBinding{IP: "2001:db8:dead:beef::1", UserAgent: "python-requests/2.31"}
	require.Equal(t, service.BindingMismatch, script.Match(loose, victim.HashForMode(loose), victim.CoarseFingerprint()))
	otherNet := &service.SessionBinding{IP: "198.51.100.77", UserAgent: victim.UserAgent}
	require.Equal(t, service.BindingMismatch, otherNet.Match(loose, victim.HashForMode(loose), victim.CoarseFingerprint()))
}

// rebind 只放行「浏览器家族 + 系统家族没变」：ClientFamilyMatches 不看 IP；升级前的令牌没有粗粒度指纹，
// 无法判断，按过渡期放行；有但解析不了则不放行。
func TestSessionBindingClientFamilyMatches(t *testing.T) {
	chromeWin := &service.SessionBinding{IP: "203.0.113.10", UserAgent: "Mozilla/5.0 (Windows NT 10.0; Win64; x64) Chrome/130.0.0.0 Safari/537.36"}
	stored := chromeWin.CoarseFingerprint()

	require.True(t, (&service.SessionBinding{IP: "198.51.100.1", UserAgent: "Mozilla/5.0 (Windows NT 10.0) Chrome/131.0.0.0 Safari/537.36"}).ClientFamilyMatches(stored), "version and IP do not matter")
	require.True(t, (&service.SessionBinding{IP: "2001:db8::1", UserAgent: chromeWin.UserAgent}).ClientFamilyMatches(stored), "nor does the IP family")
	require.False(t, (&service.SessionBinding{IP: "203.0.113.10", UserAgent: "Mozilla/5.0 (X11; Linux x86_64; rv:132.0) Gecko/20100101 Firefox/132.0"}).ClientFamilyMatches(stored))
	require.False(t, (&service.SessionBinding{IP: "203.0.113.10", UserAgent: "curl/8.4.0"}).ClientFamilyMatches(stored))
	require.False(t, (&service.SessionBinding{IP: "203.0.113.10"}).ClientFamilyMatches(stored), "no user agent is not the same client")

	require.True(t, (&service.SessionBinding{IP: "198.51.100.1", UserAgent: "curl/8.4.0"}).ClientFamilyMatches(""), "legacy token without a coarse fingerprint: cannot tell, transitional allow")
	require.False(t, chromeWin.ClientFamilyMatches("garbage"), "a coarse fingerprint that cannot be parsed is not trusted")
	require.True(t, (*service.SessionBinding)(nil).ClientFamilyMatches(stored), "no binding on this request: nothing to compare")
}
