//go:build unit

package service_test

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"mime/quotedprintable"
	"strings"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

// 独立审计（AUD-1）对 #642「管理端破玻璃重置用户 2FA」的加固回归。每个用例对应审计的一条发现：
// 会话/预算清理与 #641 命令行一致、Passkey 预读在写库之前、写库之后的善后失败不变成 5xx、
// 自重置被拒、reason 纯文本清洗、邮件与审计里不带邮箱明文/操作者。

const hardeningTargetID = int64(3249)

// decodeAdmin2FAResetMail 取出假 SMTP 收到的最后一封信的 HTML 正文（解 quoted-printable）。
func decodeAdmin2FAResetMail(t *testing.T, fixture *admin2FAResetFixture) string {
	t.Helper()
	raw, _ := fixture.smtp.lastBody.Load().(string)
	require.NotEmpty(t, raw, "no mail was captured")
	_, body, found := strings.Cut(raw, "\n\n")
	require.True(t, found, raw)
	decoded, err := io.ReadAll(quotedprintable.NewReader(strings.NewReader(body)))
	require.NoError(t, err)
	return string(decoded)
}

// 发现 1（P2）：重置后必须像 #641 命令行一样撤销该用户全部会话、清掉绑定预算与绑定会话。
// 修前：refresh token 仍然有效、totp:attempts 仍是 5，忘码输错 5 次的用户重新绑定会被卡 15 分钟。
func TestAdminResetUserTOTPClearsSessionsAndAttemptBudgetLikeTheCLI(t *testing.T) {
	fixture := newAdmin2FAResetFixture(t, admin2FAResetFixtureOptions{})
	ctx := context.Background()
	uid := fixture.user.ID

	// 忘码输错 5 次：预算已满；绑定到一半的会话也还在。
	for i := 0; i < 5; i++ {
		_, err := fixture.rawCache.IncrementVerifyAttempts(ctx, uid)
		require.NoError(t, err)
	}
	require.NoError(t, fixture.rawCache.SetSetupSession(ctx, uid,
		&service.TotpSetupSession{Secret: "pending", SetupToken: "setup-token", CreatedAt: time.Now()}, time.Minute))
	// 第二台设备的会话，以及一个旁观者（必须原样保留）。
	require.NoError(t, fixture.refreshCache.StoreRefreshToken(ctx, "hash-b", &service.RefreshTokenData{UserID: uid, FamilyID: "family-b"}, time.Hour))
	require.NoError(t, fixture.refreshCache.AddToUserTokenSet(ctx, uid, "hash-b", time.Hour))
	require.NoError(t, fixture.refreshCache.StoreRefreshToken(ctx, "hash-other", &service.RefreshTokenData{UserID: 99, FamilyID: "family-other"}, time.Hour))
	require.NoError(t, fixture.refreshCache.AddToUserTokenSet(ctx, 99, "hash-other", time.Hour))
	require.NoError(t, fixture.rawCache.SetStepUpGrant(ctx, 99, "family-other", service.FactorTOTP, service.StepUpGrantTTL))
	_, err := fixture.rawCache.IncrementVerifyAttempts(ctx, 99)
	require.NoError(t, err)

	result, err := fixture.svc.AdminResetUserTOTP(ctx, admin2FAResetInput("用户忘记动态口令，输错 5 次被锁"))
	require.NoError(t, err)
	require.True(t, result.RevokedStepUpGrants)

	for _, hash := range []string{"hash-a", "hash-b"} {
		_, err := fixture.refreshCache.GetRefreshToken(ctx, hash)
		require.ErrorIs(t, err, service.ErrRefreshTokenNotFound, "refresh token %s must be revoked", hash)
	}
	hashes, err := fixture.refreshCache.GetUserTokenHashes(ctx, uid)
	require.NoError(t, err)
	require.Empty(t, hashes, "the user's session index must be gone")

	attempts, err := fixture.rawCache.GetVerifyAttempts(ctx, uid)
	require.NoError(t, err)
	require.Zero(t, attempts, "re-binding must not be blocked by the old attempt budget")
	session, err := fixture.rawCache.GetSetupSession(ctx, uid)
	require.NoError(t, err)
	require.Nil(t, session, "a half-finished enrolment of the old secret must not survive")

	// 旁观者的会话、授权、预算都不能动。
	_, err = fixture.refreshCache.GetRefreshToken(ctx, "hash-other")
	require.NoError(t, err)
	granted, err := fixture.rawCache.HasStepUpGrant(ctx, 99, "family-other", service.FactorTOTP)
	require.NoError(t, err)
	require.True(t, granted)
	otherAttempts, err := fixture.rawCache.GetVerifyAttempts(ctx, 99)
	require.NoError(t, err)
	require.Equal(t, 1, otherAttempts)
}

// 变异：去掉遗留会话键 u<uid> 的撤销。没有会话 ID 的旧 JWT 用 u<uid> 作为会话键，
// 它的 step-up 授权没有对应的 refresh 会话可列举，只能靠固定的遗留键兜底。
func TestAdminResetUserTOTPRevokesLegacySessionKeyGrants(t *testing.T) {
	fixture := newAdmin2FAResetFixture(t, admin2FAResetFixtureOptions{})
	ctx := context.Background()
	legacySession := "u3249"

	for _, factor := range service.AllFactors() {
		require.NoError(t, fixture.rawCache.SetStepUpGrant(ctx, hardeningTargetID, legacySession, factor, service.StepUpGrantTTL))
		granted, err := fixture.rawCache.HasStepUpGrant(ctx, hardeningTargetID, legacySession, factor)
		require.NoError(t, err)
		require.True(t, granted)
	}

	_, err := fixture.svc.AdminResetUserTOTP(ctx, admin2FAResetInput("用户忘记动态口令"))
	require.NoError(t, err)

	for _, factor := range service.AllFactors() {
		granted, err := fixture.rawCache.HasStepUpGrant(ctx, hardeningTargetID, legacySession, factor)
		require.NoError(t, err)
		require.False(t, granted, "legacy u<uid> grant of factor %s must be revoked", factor)
	}
}

// 变异：去掉 RevokeTwoFactorGrantsForUser。登录用的 5 分钟 2FA 凭证（two_factor_grant）
// 不会随 TOTP 字段清空而失效；用户重新绑定后，旧浏览器上的旧凭证又能跳过新的 TOTP。
func TestAdminResetUserTOTPRevokesLoginTwoFactorGrants(t *testing.T) {
	fixture := newAdmin2FAResetFixture(t, admin2FAResetFixtureOptions{})
	ctx := context.Background()
	binding := service.TwoFactorGrantBinding{IP: "203.0.113.9", UserAgent: "unit-test-agent"}

	target, err := fixture.totp.IssueTwoFactorGrant(ctx, hardeningTargetID, service.TwoFactorGrantScopeLogin, service.FactorTOTP, binding)
	require.NoError(t, err)
	bystander, err := fixture.totp.IssueTwoFactorGrant(ctx, 99, service.TwoFactorGrantScopeLogin, service.FactorTOTP, binding)
	require.NoError(t, err)

	_, err = fixture.svc.AdminResetUserTOTP(ctx, admin2FAResetInput("用户忘记动态口令"))
	require.NoError(t, err)

	_, err = fixture.totp.ConsumeTwoFactorGrant(ctx, target.Token, hardeningTargetID, service.FactorTOTP, binding)
	require.Error(t, err, "the target's 5-minute login grant must be revoked by the reset")
	// 同一份装配里旁观者的凭证仍然有效：证明上面的失败是撤销造成的，不是装配问题。
	_, err = fixture.totp.ConsumeTwoFactorGrant(ctx, bystander.Token, 99, service.FactorTOTP, binding)
	require.NoError(t, err)
}

// 发现 2（P2）：Passkey 读取失败必须发生在写库之前：整体不写，管理员重试可以成功，
// 而不是「TOTP 已清、邮件没发、审计没写，重试只拿到 400 未启用」。
func TestAdminResetUserTOTPPasskeyReadFailureWritesNothing(t *testing.T) {
	fixture := newAdmin2FAResetFixture(t, admin2FAResetFixtureOptions{
		enableSMTP: true,
		passkeyErr: errors.New("passkey store unavailable"),
	})
	ctx := context.Background()
	uid := fixture.user.ID
	require.NoError(t, fixture.rawCache.SetStepUpGrant(ctx, uid, "family-a", service.FactorTOTP, service.StepUpGrantTTL))
	for i := 0; i < 5; i++ {
		_, err := fixture.rawCache.IncrementVerifyAttempts(ctx, uid)
		require.NoError(t, err)
	}

	_, err := fixture.svc.AdminResetUserTOTP(ctx, admin2FAResetInput("用户忘记动态口令"))
	require.Error(t, err)
	require.ErrorContains(t, err, "passkey")

	// 整体没写：库、会话、授权、预算、邮件、审计都原样。
	require.Zero(t, fixture.repo.resetCalls)
	require.True(t, fixture.user.TotpEnabled)
	require.NotNil(t, fixture.user.TotpSecretEncrypted)
	require.Empty(t, fixture.audit.entries)
	require.Zero(t, fixture.smtp.messages.Load())
	_, err = fixture.refreshCache.GetRefreshToken(ctx, "hash-a")
	require.NoError(t, err, "a failed reset must not log the user out")
	granted, err := fixture.rawCache.HasStepUpGrant(ctx, uid, "family-a", service.FactorTOTP)
	require.NoError(t, err)
	require.True(t, granted)
	attempts, err := fixture.rawCache.GetVerifyAttempts(ctx, uid)
	require.NoError(t, err)
	require.Equal(t, 5, attempts)

	// Passkey 存储恢复后管理员重试就能成功（修前这里会撞上「未启用 2FA」）。
	fixture.passkeys.err = nil
	result, err := fixture.svc.AdminResetUserTOTP(ctx, admin2FAResetInput("用户忘记动态口令"))
	require.NoError(t, err)
	require.Empty(t, result.IncompleteSteps)
	require.Equal(t, 1, fixture.repo.resetCalls)
	require.Len(t, fixture.audit.entries, 1)
}

// 发现 2（P2）：写库之后的善后（撤销、发邮件、写审计）任何一步失败都不能变成 5xx，
// 要在返回体里列出没完成的步骤；审计行在写库成功之后一定会尝试落库。
func TestAdminResetUserTOTPPostCommitFailuresAreReportedNotFatal(t *testing.T) {
	failingRevoker := func(fixture *admin2FAResetFixture, notifications *service.NotificationEmailService) *service.Admin2FAResetService {
		return service.NewAdmin2FAResetService(service.Admin2FAResetDeps{
			UserRepo:      fixture.repo,
			Totp:          fixture.totp,
			RefreshCache:  fixture.refreshCache,
			StepUp:        disablingStepUpRevoker{},
			Notifications: notifications,
			Audit:         service.NewAuditLogService(fixture.audit, nil),
		})
	}

	cases := []struct {
		name       string
		opts       admin2FAResetFixtureOptions
		build      func(*admin2FAResetFixture) *service.Admin2FAResetService
		wantSteps  []string
		wantAudit  bool
		wantMail   int64
		wantRevoke bool
	}{
		{
			name:       "notification email cannot be sent",
			opts:       admin2FAResetFixtureOptions{},
			build:      func(f *admin2FAResetFixture) *service.Admin2FAResetService { return f.svc },
			wantSteps:  []string{service.Admin2FAResetStepNotification},
			wantAudit:  true,
			wantRevoke: true,
		},
		{
			name:       "session revocation fails",
			opts:       admin2FAResetFixtureOptions{enableSMTP: true},
			build:      func(f *admin2FAResetFixture) *service.Admin2FAResetService { return failingRevoker(f, f.notifications) },
			wantSteps:  []string{service.Admin2FAResetStepSessionRevocation},
			wantAudit:  true,
			wantMail:   1,
			wantRevoke: false,
		},
		{
			name:       "audit insert fails",
			opts:       admin2FAResetFixtureOptions{enableSMTP: true, auditErr: errors.New("audit store rejected the row")},
			build:      func(f *admin2FAResetFixture) *service.Admin2FAResetService { return f.svc },
			wantSteps:  []string{service.Admin2FAResetStepAudit},
			wantAudit:  false,
			wantMail:   1,
			wantRevoke: true,
		},
		{
			name:  "everything after the write fails",
			opts:  admin2FAResetFixtureOptions{auditErr: errors.New("audit store rejected the row")},
			build: func(f *admin2FAResetFixture) *service.Admin2FAResetService { return failingRevoker(f, nil) },
			wantSteps: []string{
				service.Admin2FAResetStepSessionRevocation,
				service.Admin2FAResetStepNotification,
				service.Admin2FAResetStepAudit,
			},
			wantAudit:  false,
			wantRevoke: false,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			fixture := newAdmin2FAResetFixture(t, tc.opts)
			svc := tc.build(fixture)

			result, err := svc.AdminResetUserTOTP(context.Background(), admin2FAResetInput("用户忘记动态口令"))
			require.NoError(t, err, "a committed reset must never turn into an error response")
			require.NotNil(t, result)
			require.Equal(t, tc.wantSteps, result.IncompleteSteps)
			require.Equal(t, tc.wantRevoke, result.RevokedStepUpGrants)

			// 重置本身已经提交。
			require.Equal(t, 1, fixture.repo.resetCalls)
			require.False(t, fixture.user.TotpEnabled)
			require.False(t, result.TotpEnabledAfter)
			if tc.wantMail > 0 {
				require.Equal(t, tc.wantMail, fixture.smtp.messages.Load())
			}

			if !tc.wantAudit {
				require.Empty(t, fixture.audit.entries)
				return
			}
			// 失败路径也必须有审计行，并且如实写明没做完的步骤。
			require.Len(t, fixture.audit.entries, 1)
			extra := fixture.audit.entries[0].Extra
			require.Equal(t, strings.Join(tc.wantSteps, ","), extra["incomplete_steps"])
			require.Equal(t, tc.wantRevoke, extra["revoked_step_up_grants"])
			require.Equal(t, !adminResetStepsContain(tc.wantSteps, service.Admin2FAResetStepNotification), extra["notification_queued"])
		})
	}
}

// 管理员在写库提交的瞬间断开连接（请求上下文被取消）：重置已经发生，旧会话照样要撤、
// 用户照样要被通知、审计照样要落库——善后不能跟随请求取消。
func TestAdminResetUserTOTPFinishesAfterCommitEvenIfTheRequestIsCancelled(t *testing.T) {
	fixture := newAdmin2FAResetFixture(t, admin2FAResetFixtureOptions{enableSMTP: true})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	fixture.repo.afterReset = cancel

	result, err := fixture.svc.AdminResetUserTOTP(ctx, admin2FAResetInput("用户忘记动态口令"))
	require.NoError(t, err)
	require.Error(t, ctx.Err(), "the request context was cancelled right after the commit")

	require.Empty(t, result.IncompleteSteps)
	require.True(t, result.RevokedStepUpGrants)
	require.True(t, result.NotificationQueued)
	_, err = fixture.refreshCache.GetRefreshToken(context.Background(), "hash-a")
	require.ErrorIs(t, err, service.ErrRefreshTokenNotFound)
	require.Equal(t, int64(1), fixture.smtp.messages.Load())
	require.Len(t, fixture.audit.entries, 1)
}

func adminResetStepsContain(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}

// 发现 4（P2）：不允许重置自己。过 step-up 本身就要 2FA，真丢了自己的码这条路用不了，
// 只会便宜了窗口内的会话劫持；自己丢码走服务器命令行。
func TestAdminResetUserTOTPRejectsResettingYourself(t *testing.T) {
	fixture := newAdmin2FAResetFixture(t, admin2FAResetFixtureOptions{enableSMTP: true})

	in := admin2FAResetInput("我自己忘了动态口令")
	in.ActorUserID = in.TargetUserID
	_, err := fixture.svc.AdminResetUserTOTP(context.Background(), in)
	require.ErrorIs(t, err, service.ErrAdmin2FAResetSelfTarget)

	// 自重置的错误先于原因校验：不是「原因没写」，而是「这件事不允许」。
	in.Reason = ""
	_, err = fixture.svc.AdminResetUserTOTP(context.Background(), in)
	require.ErrorIs(t, err, service.ErrAdmin2FAResetSelfTarget)

	require.Zero(t, fixture.repo.resetCalls)
	require.True(t, fixture.user.TotpEnabled)
	require.Empty(t, fixture.audit.entries)
	require.Zero(t, fixture.smtp.messages.Load())

	// 其他管理员作为操作者重置这个用户不受影响。
	in = admin2FAResetInput("用户忘记动态口令")
	in.ActorUserID = 1
	_, err = fixture.svc.AdminResetUserTOTP(context.Background(), in)
	require.NoError(t, err)
}

// 发现 5（P2）：reason 入库前清洗成纯文本。HTML 标记、零宽字符、U+202E、NUL 都不能原样入库；
// NUL 尤其会让 JSONB 拒收，重置成功但服务层审计行丢失。
func TestAdminResetUserTOTPSanitizesTheReason(t *testing.T) {
	fixture := newAdmin2FAResetFixture(t, admin2FAResetFixtureOptions{enableSMTP: true})

	dirty := "用户忘记<b>动态</b>口令\x00\u200b\u202e &lt;img src=x onerror=alert(1)&gt;"
	result, err := fixture.svc.AdminResetUserTOTP(context.Background(), admin2FAResetInput(dirty))
	require.NoError(t, err)

	const clean = "用户忘记动态口令"
	require.Equal(t, clean, result.Reason)
	require.Len(t, fixture.audit.entries, 1, "the audit row must land even though the raw reason contained a NUL")
	entry := fixture.audit.entries[0]
	require.Equal(t, clean, entry.Extra["reason"])

	// JSONB 不接受 \u0000；双向覆写字符也不能留在审计里。
	encoded, err := json.Marshal(entry.Extra)
	require.NoError(t, err)
	require.NotContains(t, string(encoded), `\u0000`)
	require.NotContains(t, string(encoded), "\u202e")
	require.NotContains(t, string(encoded), "\u200b")

	body := decodeAdmin2FAResetMail(t, fixture)
	require.Contains(t, body, clean)
	require.NotContains(t, body, "<b>")
	require.NotContains(t, body, "onerror")
}

// 清洗之后什么都不剩的原因按「原因必填」拒绝，不写库。
func TestAdminResetUserTOTPRejectsReasonThatSanitizesToNothing(t *testing.T) {
	fixture := newAdmin2FAResetFixture(t, admin2FAResetFixtureOptions{})

	for _, reason := range []string{"<b></b>", "\u200b\u202e", "\x00", " \x00 \n", "&lt;i&gt;&lt;/i&gt;", "<script>", "javascript:"} {
		_, err := fixture.svc.AdminResetUserTOTP(context.Background(), admin2FAResetInput(reason))
		require.ErrorIs(t, err, service.ErrAdmin2FAResetReasonRequired, "reason=%q", reason)
	}
	require.Zero(t, fixture.repo.resetCalls)
	require.Empty(t, fixture.audit.entries)
}

// 长度按清洗后的纯文本计：入库的就是清洗后的内容，标记不占名额。
func TestAdminResetUserTOTPReasonLengthIsMeasuredAfterSanitizing(t *testing.T) {
	fixture := newAdmin2FAResetFixture(t, admin2FAResetFixtureOptions{})

	markup := strings.Repeat("<i>", 100)
	_, err := fixture.svc.AdminResetUserTOTP(context.Background(),
		admin2FAResetInput(markup+strings.Repeat("忘", service.Admin2FAResetReasonMaxLength+1)))
	require.ErrorIs(t, err, service.ErrAdmin2FAResetReasonTooLong)
	require.Zero(t, fixture.repo.resetCalls)

	result, err := fixture.svc.AdminResetUserTOTP(context.Background(),
		admin2FAResetInput(markup+strings.Repeat("忘", service.Admin2FAResetReasonMaxLength)))
	require.NoError(t, err)
	require.Equal(t, strings.Repeat("忘", service.Admin2FAResetReasonMaxLength), result.Reason)
}

// 发现 3（P2）：邮件里的「重新绑定」必须指向前端真实存在的页面（/profile，个人资料页的
// 2FA 卡片；/settings/security 在前端没有路由，会落 404），基址优先用站点前端地址
// （frontend_url），取不到才回退 api_base_url。
func TestAdminResetUserTOTPRebindLinkPointsAtTheFrontendProfilePage(t *testing.T) {
	cases := []struct {
		name     string
		settings map[string]string
		want     string
		wantNot  []string
	}{
		{
			name:     "frontend_url wins over api_base_url",
			settings: map[string]string{service.SettingKeyFrontendURL: "https://app.example.test", service.SettingKeyAPIBaseURL: "https://api.example.test"},
			want:     "https://app.example.test/profile",
			wantNot:  []string{"api.example.test"},
		},
		{
			name:     "api_base_url is only the fallback",
			settings: map[string]string{service.SettingKeyFrontendURL: "", service.SettingKeyAPIBaseURL: "https://api.example.test/"},
			want:     "https://api.example.test/profile",
		},
		{
			name:     "a path prefix and a trailing slash are kept tidy",
			settings: map[string]string{service.SettingKeyFrontendURL: "https://app.example.test/console/"},
			want:     "https://app.example.test/console/profile",
		},
		{
			name:     "an unsafe frontend_url is ignored",
			settings: map[string]string{service.SettingKeyFrontendURL: "javascript:alert(1)", service.SettingKeyAPIBaseURL: "https://api.example.test"},
			want:     "https://api.example.test/profile",
			wantNot:  []string{"javascript"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			fixture := newAdmin2FAResetFixture(t, admin2FAResetFixtureOptions{enableSMTP: true})
			for key, value := range tc.settings {
				fixture.settings.values[key] = value
			}

			result, err := fixture.svc.AdminResetUserTOTP(context.Background(), admin2FAResetInput("用户忘记动态口令"))
			require.NoError(t, err)
			require.True(t, result.NotificationQueued)

			body := decodeAdmin2FAResetMail(t, fixture)
			require.Contains(t, body, `href="`+tc.want+`"`)
			require.NotContains(t, body, "/settings/security", "that front-end route does not exist")
			for _, forbidden := range tc.wantNot {
				require.NotContains(t, body, forbidden)
			}
		})
	}
}

// 没配任何站点地址时不能编一个链接：邮件照发，重新绑定入口写 "-"。
func TestAdminResetUserTOTPWithoutAnySiteURLStillNotifiesWithoutALink(t *testing.T) {
	fixture := newAdmin2FAResetFixture(t, admin2FAResetFixtureOptions{enableSMTP: true})
	delete(fixture.settings.values, service.SettingKeyFrontendURL)

	result, err := fixture.svc.AdminResetUserTOTP(context.Background(), admin2FAResetInput("用户忘记动态口令"))
	require.NoError(t, err)
	require.True(t, result.NotificationQueued)
	body := decodeAdmin2FAResetMail(t, fixture)
	require.NotContains(t, body, "/profile")
	require.NotContains(t, body, "/settings/security")
}

// 发现 6④：邮件发给目标用户，目标账号可能正是被攻击者控制的那个——正文只写「管理员」
// + 原因 + 时间，不带操作管理员的邮箱。
func TestAdminResetUserTOTPEmailNeverNamesTheOperator(t *testing.T) {
	fixture := newAdmin2FAResetFixture(t, admin2FAResetFixtureOptions{enableSMTP: true})

	in := admin2FAResetInput("用户忘记动态口令")
	in.ActorEmail = "ops-admin@example.test"
	_, err := fixture.svc.AdminResetUserTOTP(context.Background(), in)
	require.NoError(t, err)

	raw, _ := fixture.smtp.lastBody.Load().(string)
	body := decodeAdmin2FAResetMail(t, fixture)
	for _, text := range []string{raw, body} {
		require.NotContains(t, text, "ops-admin@example.test")
		require.NotContains(t, text, "Operator")
	}
	require.Contains(t, body, "An administrator reset")
	require.Contains(t, body, "用户忘记动态口令")
	require.Contains(t, body, "Reset time:")
	require.Contains(t, body, " UTC")
}

// 发现 6③：审计里不再存目标邮箱明文，只存不可逆指纹（与认证审计的 email_hash 同一套）。
func TestAdminResetUserTOTPAuditStoresTargetEmailFingerprintOnly(t *testing.T) {
	fixture := newAdmin2FAResetFixture(t, admin2FAResetFixtureOptions{})

	_, err := fixture.svc.AdminResetUserTOTP(context.Background(), admin2FAResetInput("用户忘记动态口令"))
	require.NoError(t, err)
	require.Len(t, fixture.audit.entries, 1)
	entry := fixture.audit.entries[0]

	hash, ok := entry.Extra["target_email_hash"].(string)
	require.True(t, ok)
	require.True(t, strings.HasPrefix(hash, "sha256:"), hash)
	require.Equal(t, service.HashAuditEmail(fixture.user.Email), hash)
	require.NotContains(t, entry.Extra, "target_user_email")
	dump := dumpAuditEntry(entry)
	require.NotContains(t, dump, fixture.user.Email)
	require.NotContains(t, dump, "user-3249")
	encoded, err := json.Marshal(entry.Extra)
	require.NoError(t, err)
	require.NotContains(t, string(encoded), fixture.user.Email)
}
