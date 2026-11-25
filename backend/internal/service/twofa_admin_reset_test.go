//go:build unit

package service_test

import (
	"context"
	"errors"
	"fmt"
	"net"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/repository" //nolint:depguard // 破玻璃重置要验证真实的 Redis 授权撤销
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9" //nolint:depguard // 同上：需要真实 TotpCache 才能断言授权被撤销
	"github.com/stretchr/testify/require"
)

// admin2FAResetRepo 是只实现破玻璃重置所需方法的用户仓储桩：
// GetByID + ResetTotpForAdmin（实现 service.TotpAdminResetter）。
type admin2FAResetRepo struct {
	service.UserRepository
	user       *service.User
	getErr     error
	resetCalls int
	resetErr   error
	// afterReset 在写库成功之后立即执行（模拟管理员在提交瞬间断开连接）。
	afterReset func()
}

func (r *admin2FAResetRepo) GetByID(context.Context, int64) (*service.User, error) {
	if r.getErr != nil {
		return nil, r.getErr
	}
	copied := *r.user
	return &copied, nil
}

func (r *admin2FAResetRepo) ResetTotpForAdmin(context.Context, int64) error {
	r.resetCalls++
	if r.resetErr != nil {
		return r.resetErr
	}
	// 模拟真实写库结果：重置后用户不再启用 TOTP。
	r.user.TotpEnabled = false
	r.user.TotpSecretEncrypted = nil
	r.user.TotpEnabledAt = nil
	if r.afterReset != nil {
		r.afterReset()
	}
	return nil
}

// admin2FAResetPasskeys 只实现 ListByUserID（PasskeyFactorBound 用得到）。
type admin2FAResetPasskeys struct {
	service.PasskeyRepository
	records []service.PasskeyCredentialRecord
	err     error
}

func (p *admin2FAResetPasskeys) ListByUserID(context.Context, int64) ([]service.PasskeyCredentialRecord, error) {
	if p.err != nil {
		return nil, p.err
	}
	return p.records, nil
}

// admin2FAResetAuditCapture 捕获服务层同步写入的审计记录。
type admin2FAResetAuditCapture struct {
	service.AuditLogRepository
	entries   []*service.AuditLog
	insertErr error
}

func (c *admin2FAResetAuditCapture) Insert(_ context.Context, entry *service.AuditLog) error {
	if c.insertErr != nil {
		return c.insertErr
	}
	copied := *entry
	c.entries = append(c.entries, &copied)
	return nil
}

// admin2FAResetSMTP 是一个最小 SMTP 服务器：只数收到了几封信，并留下最后一封的正文。
type admin2FAResetSMTP struct {
	listener net.Listener
	messages atomic.Int64
	lastBody atomic.Value
}

func startAdmin2FAResetSMTP(t *testing.T) *admin2FAResetSMTP {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	server := &admin2FAResetSMTP{listener: listener}
	t.Cleanup(func() { _ = listener.Close() })
	go func() {
		for {
			conn, err := listener.Accept()
			if err != nil {
				return
			}
			go server.handle(conn)
		}
	}()
	return server
}

func (s *admin2FAResetSMTP) handle(conn net.Conn) {
	defer func() { _ = conn.Close() }()
	writer := newLineWriter(conn)
	writer.write("220 test.local ESMTP\r\n")
	reader := newLineReader(conn)
	var data strings.Builder
	inData := false
	for {
		line, err := reader.readLine()
		if err != nil {
			return
		}
		if inData {
			if line == "." {
				inData = false
				s.messages.Add(1)
				s.lastBody.Store(data.String())
				writer.write("250 OK\r\n")
				continue
			}
			data.WriteString(line)
			data.WriteString("\n")
			continue
		}
		upper := strings.ToUpper(line)
		switch {
		case strings.HasPrefix(upper, "EHLO"), strings.HasPrefix(upper, "HELO"):
			writer.write("250-test.local\r\n250 OK\r\n")
		case strings.HasPrefix(upper, "AUTH"):
			// 认证过程本身就是一封信之前的多轮交互；这里统一当成功。
			writer.write("235 Authentication successful\r\n")
		case strings.HasPrefix(upper, "DATA"):
			inData = true
			writer.write("354 End data with <CR><LF>.<CR><LF>\r\n")
		case strings.HasPrefix(upper, "QUIT"):
			writer.write("221 Bye\r\n")
			return
		default:
			writer.write("250 OK\r\n")
		}
	}
}

type smtpLineWriter struct{ conn net.Conn }

func newLineWriter(conn net.Conn) *smtpLineWriter { return &smtpLineWriter{conn: conn} }

func (w *smtpLineWriter) write(line string) { _, _ = w.conn.Write([]byte(line)) }

type smtpLineReader struct {
	buf  []byte
	conn net.Conn
}

func newLineReader(conn net.Conn) *smtpLineReader { return &smtpLineReader{conn: conn} }

func (r *smtpLineReader) readLine() (string, error) {
	for {
		if index := strings.IndexByte(string(r.buf), '\n'); index >= 0 {
			line := strings.TrimRight(string(r.buf[:index]), "\r")
			r.buf = r.buf[index+1:]
			return line, nil
		}
		chunk := make([]byte, 4096)
		n, err := r.conn.Read(chunk)
		if err != nil {
			return "", err
		}
		r.buf = append(r.buf, chunk[:n]...)
	}
}

// admin2FAResetSettingsRepo 是最小设置仓储（通知邮件服务与邮件服务读设置用）。
type admin2FAResetSettingsRepo struct {
	service.SettingRepository
	values map[string]string
}

func (r *admin2FAResetSettingsRepo) Get(_ context.Context, key string) (*service.Setting, error) {
	if value, ok := r.values[key]; ok {
		return &service.Setting{Key: key, Value: value}, nil
	}
	return nil, service.ErrSettingNotFound
}

func (r *admin2FAResetSettingsRepo) GetValue(ctx context.Context, key string) (string, error) {
	setting, err := r.Get(ctx, key)
	if err != nil {
		return "", err
	}
	return setting.Value, nil
}

func (r *admin2FAResetSettingsRepo) Set(_ context.Context, key, value string) error {
	r.values[key] = value
	return nil
}

func (r *admin2FAResetSettingsRepo) GetMultiple(ctx context.Context, keys []string) (map[string]string, error) {
	result := make(map[string]string, len(keys))
	for _, key := range keys {
		if value, err := r.GetValue(ctx, key); err == nil {
			result[key] = value
		}
	}
	return result, nil
}

func (r *admin2FAResetSettingsRepo) SetMultiple(_ context.Context, values map[string]string) error {
	for key, value := range values {
		r.values[key] = value
	}
	return nil
}

func (r *admin2FAResetSettingsRepo) GetAll(context.Context) (map[string]string, error) {
	return r.values, nil
}

func (r *admin2FAResetSettingsRepo) Delete(_ context.Context, key string) error {
	delete(r.values, key)
	return nil
}

type admin2FAResetFixtureOptions struct {
	passkeys   []service.PasskeyCredentialRecord
	enableSMTP bool
	// plainCache 用一层只声明 TotpCache 接口的包装，模拟「缓存实现不支持
	// 按用户撤销 2FA 凭证」的装配，用来验证撤销失败时如实上报而不是谎报成功。
	plainCache bool
	// passkeyErr 让读取 Passkey 失败（重置前预检）。
	passkeyErr error
	// auditErr 让服务层审计行写入失败。
	auditErr error
}

// admin2FAResetFixture 组装一次可用的破玻璃重置环境。
type admin2FAResetFixture struct {
	svc       *service.Admin2FAResetService
	repo      *admin2FAResetRepo
	totp      *service.TotpService
	totpCache service.TotpCache
	// rawCache 是具体的 *repository.TotpCache：只有它实现了 step-up 授权撤销。
	rawCache     *repository.TotpCache
	refreshCache service.RefreshTokenCache
	stepUp       service.StepUpGrantRevoker
	audit        *admin2FAResetAuditCapture
	smtp         *admin2FAResetSMTP
	user         *service.User
	settings     *admin2FAResetSettingsRepo
	passkeys     *admin2FAResetPasskeys
	// notifications 是装配进 svc 的通知邮件服务（没开 SMTP 时没有发信能力）。
	notifications *service.NotificationEmailService
}

func newAdmin2FAResetFixture(t *testing.T, opts admin2FAResetFixtureOptions) *admin2FAResetFixture {
	t.Helper()
	ctx := context.Background()

	enabledAt := time.Date(2026, 5, 1, 8, 0, 0, 0, time.UTC)
	secretCipher := "v1:encrypted-secret-should-never-enter-audit"
	user := &service.User{
		ID:                  3249,
		Email:               "user-3249@example.test",
		Username:            "uid3249",
		Role:                service.RoleUser,
		Status:              service.StatusActive,
		TotpEnabled:         true,
		TotpSecretEncrypted: &secretCipher,
		TotpEnabledAt:       &enabledAt,
	}

	miniRedis := miniredis.RunT(t)
	rdb := redis.NewClient(&redis.Options{Addr: miniRedis.Addr()})
	t.Cleanup(func() { _ = rdb.Close() })
	totpCacheStore := repository.NewTotpCacheStore(rdb)
	var totpCache service.TotpCache = totpCacheStore
	refreshCache := repository.NewRefreshTokenCache(rdb)
	// 目标用户有一个活跃会话族 family-a：step-up 授权就挂在这个会话键上。
	require.NoError(t, refreshCache.StoreRefreshToken(ctx, "hash-a", &service.RefreshTokenData{
		UserID: user.ID, FamilyID: "family-a",
	}, time.Hour))
	// 会话索引集合：生产登录流程（auth 服务）与 CLI 恢复路径都靠它列举会话，
	// 少了这一步就列不出 FamilyID，step-up 授权也就撤不掉。
	require.NoError(t, refreshCache.AddToUserTokenSet(ctx, user.ID, "hash-a", time.Hour))

	repo := &admin2FAResetRepo{user: user}
	var cache service.TotpCache = totpCache
	if opts.plainCache {
		cache = plainTotpCache{totpCache}
	}
	totpService := service.NewTotpService(repo, nil, cache, nil, nil, nil)
	passkeys := &admin2FAResetPasskeys{records: opts.passkeys, err: opts.passkeyErr}
	totpService.SetPasskeyRepository(passkeys)

	settings := &admin2FAResetSettingsRepo{values: map[string]string{
		service.SettingKeySiteName:    "TestSite",
		service.SettingKeyFrontendURL: "https://app.example.test",
	}}
	notifications := service.NewNotificationEmailService(settings, nil)
	var smtp *admin2FAResetSMTP
	if opts.enableSMTP {
		smtp = startAdmin2FAResetSMTP(t)
		// 生产默认拒绝回环目标；放行是为了让测试能连本机的假 SMTP。
		// 必须在本用例结束时复位：其它用例（notification_email_service_test）
		// 会在自己的 Cleanup 里把它关掉，谁先跑谁后跑都不能互相污染。
		service.SetSMTPAllowPrivateTargetsForTest(true)
		t.Cleanup(func() { service.SetSMTPAllowPrivateTargetsForTest(false) })
		host, port, err := net.SplitHostPort(smtp.listener.Addr().String())
		require.NoError(t, err)
		require.NoError(t, settings.SetMultiple(ctx, map[string]string{
			service.SettingKeySMTPHost:     host,
			service.SettingKeySMTPPort:     port,
			service.SettingKeySMTPUsername: "user",
			service.SettingKeySMTPPassword: "password",
			service.SettingKeySMTPFrom:     "noreply@example.com",
			service.SettingKeySMTPFromName: "TestSite",
			service.SettingKeySMTPUseTLS:   "false",
		}))
		notifications = service.NewNotificationEmailService(settings, service.NewEmailService(settings, nil))
	}

	auditRepo := &admin2FAResetAuditCapture{insertErr: opts.auditErr}
	auditService := service.NewAuditLogService(auditRepo, nil)

	return &admin2FAResetFixture{
		svc: service.NewAdmin2FAResetService(service.Admin2FAResetDeps{
			UserRepo:      repo,
			Totp:          totpService,
			RefreshCache:  refreshCache,
			StepUp:        totpCacheStore,
			Notifications: notifications,
			Audit:         auditService,
		}),
		repo:          repo,
		totp:          totpService,
		totpCache:     totpCache,
		rawCache:      totpCacheStore,
		refreshCache:  refreshCache,
		stepUp:        totpCacheStore,
		audit:         auditRepo,
		smtp:          smtp,
		user:          user,
		settings:      settings,
		passkeys:      passkeys,
		notifications: notifications,
	}
}

func admin2FAResetInput(reason string) service.Admin2FAResetInput {
	return service.Admin2FAResetInput{
		TargetUserID: 3249,
		Reason:       reason,
		ActorUserID:  1,
		ActorEmail:   "admin@example.test",
		ActorRole:    service.RoleAdmin,
		AuthMethod:   service.AuditAuthMethodJWT,
		ClientIP:     "203.0.113.9",
		UserAgent:    "unit-test-agent",
		RequestID:    "req-2fa-reset",
	}
}

// dumpAuditEntry 把审计记录渲染成可搜索文本（用于「不得出现密钥」断言）。
func dumpAuditEntry(entry *service.AuditLog) string {
	if entry == nil {
		return ""
	}
	var b strings.Builder
	b.WriteString(entry.Action)
	b.WriteString(" ")
	b.WriteString(entry.RequestBody)
	for key, value := range entry.Extra {
		b.WriteString(" ")
		b.WriteString(key)
		b.WriteString("=")
		b.WriteString(fmt.Sprint(value))
	}
	return b.String()
}

// 原因必填：空串、纯空白都必须是 400，且不能碰仓储。
func TestAdminResetUserTOTPRequiresNonEmptyReason(t *testing.T) {
	fixture := newAdmin2FAResetFixture(t, admin2FAResetFixtureOptions{})

	for _, reason := range []string{"", "   ", "\n\t "} {
		_, err := fixture.svc.AdminResetUserTOTP(context.Background(), admin2FAResetInput(reason))
		require.ErrorIs(t, err, service.ErrAdmin2FAResetReasonRequired, "reason=%q", reason)
	}
	require.Zero(t, fixture.repo.resetCalls, "rejected requests must not touch the database")
	require.True(t, fixture.user.TotpEnabled, "totp must stay enabled")
	require.Empty(t, fixture.audit.entries)
}

// 原因超过 500 字（按字符计）→ 400；正好 500 个中文字符可以过。
func TestAdminResetUserTOTPReasonLengthBoundary(t *testing.T) {
	fixture := newAdmin2FAResetFixture(t, admin2FAResetFixtureOptions{})

	tooLong := strings.Repeat("忘", service.Admin2FAResetReasonMaxLength+1)
	_, err := fixture.svc.AdminResetUserTOTP(context.Background(), admin2FAResetInput(tooLong))
	require.ErrorIs(t, err, service.ErrAdmin2FAResetReasonTooLong)
	require.Zero(t, fixture.repo.resetCalls)

	exact := strings.Repeat("忘", service.Admin2FAResetReasonMaxLength)
	_, err = fixture.svc.AdminResetUserTOTP(context.Background(), admin2FAResetInput(exact))
	require.NoError(t, err)
	require.Equal(t, 1, fixture.repo.resetCalls)
}

// 用户不存在（仓储返回 ErrUserNotFound）→ 404 语义，不能写库、不能审计。
func TestAdminResetUserTOTPUserNotFound(t *testing.T) {
	fixture := newAdmin2FAResetFixture(t, admin2FAResetFixtureOptions{})
	fixture.repo.getErr = service.ErrUserNotFound

	_, err := fixture.svc.AdminResetUserTOTP(context.Background(), admin2FAResetInput("用户忘记动态口令"))
	require.ErrorIs(t, err, service.ErrUserNotFound)
	require.Zero(t, fixture.repo.resetCalls)
	require.Empty(t, fixture.audit.entries)
}

// 用户没开 2FA → 400「该用户未启用 2FA」，不写库、不发邮件、不审计。
func TestAdminResetUserTOTPRejectsUserWithoutTwoFactor(t *testing.T) {
	fixture := newAdmin2FAResetFixture(t, admin2FAResetFixtureOptions{enableSMTP: true})
	fixture.user.TotpEnabled = false
	fixture.user.TotpSecretEncrypted = nil

	_, err := fixture.svc.AdminResetUserTOTP(context.Background(), admin2FAResetInput("用户忘记动态口令"))
	require.ErrorIs(t, err, service.ErrAdmin2FAResetTargetNotEnabled)
	require.Zero(t, fixture.repo.resetCalls)
	require.Empty(t, fixture.audit.entries)
	require.Equal(t, int64(0), fixture.smtp.messages.Load(), "no email must be sent for a rejected reset")
}

// 成功路径：TOTP 字段清空 + 全部因子 step-up 授权撤销 + 审计写入（含原因、
// 重置前后状态，绝不含 secret）+ 通知邮件发出。
func TestAdminResetUserTOTPSuccessResetsTotpRevokesGrantsAuditsAndNotifies(t *testing.T) {
	fixture := newAdmin2FAResetFixture(t, admin2FAResetFixtureOptions{enableSMTP: true})
	ctx := context.Background()

	// 先给目标用户写两枚 step-up 授权（TOTP + Passkey），证明它们会被撤销。
	for _, factor := range []service.Factor{service.FactorTOTP, service.FactorPasskey} {
		require.NoError(t, fixture.totpCache.SetStepUpGrant(ctx, fixture.user.ID, "family-a", factor, service.StepUpGrantTTL))
		granted, err := fixture.totpCache.HasStepUpGrant(ctx, fixture.user.ID, "family-a", factor)
		require.NoError(t, err)
		require.True(t, granted)
	}
	// 旁观者用户的授权必须原样保留（只写授权键，不需要 refresh 会话索引）。
	require.NoError(t, fixture.totpCache.SetStepUpGrant(ctx, 99, "family-b", service.FactorTOTP, service.StepUpGrantTTL))

	result, err := fixture.svc.AdminResetUserTOTP(ctx, admin2FAResetInput("用户忘记动态口令，经工单核实身份"))
	require.NoError(t, err)
	require.NotNil(t, result)

	// 1) TOTP 字段已清空。
	require.False(t, result.TotpEnabledAfter)
	require.True(t, result.TotpPreviouslyEnabled)
	require.NotNil(t, result.TotpEnabledAt, "审计/响应要保留重置前的启用时间")
	require.Equal(t, 1, fixture.repo.resetCalls)
	require.False(t, fixture.user.TotpEnabled)
	require.Nil(t, fixture.user.TotpSecretEncrypted)
	require.Nil(t, fixture.user.TotpEnabledAt)

	// 2) 所有因子的 step-up 授权都被撤销，旁观者不受影响。
	for _, factor := range service.AllFactors() {
		granted, err := fixture.totpCache.HasStepUpGrant(ctx, fixture.user.ID, "family-a", factor)
		require.NoError(t, err)
		require.False(t, granted, "factor %s grant must be revoked", factor)
	}
	bystander, err := fixture.totpCache.HasStepUpGrant(ctx, 99, "family-b", service.FactorTOTP)
	require.NoError(t, err)
	require.True(t, bystander)
	require.True(t, result.RevokedStepUpGrants)

	// 3) 审计同步写入，字段完整且不含 TOTP 密文。
	require.Len(t, fixture.audit.entries, 1)
	entry := fixture.audit.entries[0]
	require.Equal(t, service.AuditActionAdminUserReset2FA, entry.Action)
	require.NotNil(t, entry.ActorUserID)
	require.Equal(t, int64(1), *entry.ActorUserID)
	require.Equal(t, "admin@example.test", entry.ActorEmail)
	require.Equal(t, "req-2fa-reset", entry.RequestID)
	require.Equal(t, "用户忘记动态口令，经工单核实身份", entry.Extra["reason"])
	require.Equal(t, false, entry.Extra["totp_enabled_after"])
	require.Equal(t, true, entry.Extra["totp_enabled_before"])
	// 审计里不再存邮箱明文，只存不可逆指纹。
	require.Equal(t, service.HashAuditEmail(fixture.user.Email), entry.Extra["target_email_hash"])
	require.NotContains(t, entry.Extra, "target_user_email")
	require.Equal(t, "user", entry.Extra["target_role"])
	require.Equal(t, false, entry.Extra["target_is_admin"])
	require.Equal(t, true, entry.Extra["revoked_step_up_grants"])
	require.Equal(t, true, entry.Extra["notification_queued"])
	require.NotEmpty(t, entry.Extra["totp_enabled_at_before"])
	require.NotEmpty(t, entry.Extra["reset_at"])
	auditDump := dumpAuditEntry(entry)
	require.NotContains(t, auditDump, "encrypted-secret", "totp secret must never be audited")
	require.NotContains(t, auditDump, "v1:", "no secret-looking fragment may leak into the audit row")

	// 4) 通知邮件真的发出，正文含原因/管理员/重绑入口，且不泄露密文。
	require.Equal(t, int64(1), fixture.smtp.messages.Load())
	body, _ := fixture.smtp.lastBody.Load().(string)
	// 邮件只写「管理员」+ 原因 + 时间，不带操作者邮箱；重新绑定入口是前端真实存在的 /profile。
	require.NotContains(t, body, "admin@example.test")
	require.Contains(t, body, "https://app.example.test/profile")
	require.NotContains(t, body, "/settings/security")
	require.NotContains(t, body, "encrypted-secret")
	require.True(t, result.NotificationQueued)
}

// Passkey 必须是「保留」：绑定过的用户重置后只剩 passkey 因子，返回体与审计
// 都要写清楚。
func TestAdminResetUserTOTPKeepsPasskeyFactor(t *testing.T) {
	fixture := newAdmin2FAResetFixture(t, admin2FAResetFixtureOptions{
		passkeys: []service.PasskeyCredentialRecord{{ID: 7, UserID: 3249}},
	})

	result, err := fixture.svc.AdminResetUserTOTP(context.Background(), admin2FAResetInput("用户忘记动态口令"))
	require.NoError(t, err)
	require.True(t, result.PasskeyBound)
	require.Equal(t, []string{"passkey"}, result.RemainingFactors)
	require.Equal(t, true, fixture.audit.entries[0].Extra["passkey_bound"])
	require.Equal(t, "passkey", fixture.audit.entries[0].Extra["remaining_factors"])
}

// 目标用户是管理员：允许重置（管理员自己忘了 2FA 时这是唯一自救路径），
// 但审计必须标注 target_is_admin=true。
func TestAdminResetUserTOTPAllowsAdminTargetAndAuditsIt(t *testing.T) {
	fixture := newAdmin2FAResetFixture(t, admin2FAResetFixtureOptions{})
	fixture.user.Role = service.RoleAdmin

	result, err := fixture.svc.AdminResetUserTOTP(context.Background(), admin2FAResetInput("管理员忘记自己的 2FA"))
	require.NoError(t, err)
	require.True(t, result.TargetIsAdmin)
	require.Equal(t, service.RoleAdmin, result.TargetRole)
	require.Len(t, fixture.audit.entries, 1)
	require.Equal(t, true, fixture.audit.entries[0].Extra["target_is_admin"])
	require.Equal(t, service.RoleAdmin, fixture.audit.entries[0].Extra["target_role"])
}

// 邮件服务不可用时：重置依然成功，但 result.NotificationQueued=false 且审计如实记录。
// 这是「必须通知用户」与「不能让已提交的重置报错」之间的取舍。
func TestAdminResetUserTOTPSucceedsButReportsMissingNotification(t *testing.T) {
	fixture := newAdmin2FAResetFixture(t, admin2FAResetFixtureOptions{})

	result, err := fixture.svc.AdminResetUserTOTP(context.Background(), admin2FAResetInput("用户忘记动态口令"))
	require.NoError(t, err)
	require.False(t, result.NotificationQueued)
	require.Len(t, fixture.audit.entries, 1)
	require.Equal(t, false, fixture.audit.entries[0].Extra["notification_queued"])
}

// 写库冲突（期间用户自己关了 2FA / 被删除）→ 上层能识别 ErrUserSecurityStateChanged，
// 且不写审计、不发邮件（因为没有真的重置）。
func TestAdminResetUserTOTPReportsConcurrentStateChange(t *testing.T) {
	fixture := newAdmin2FAResetFixture(t, admin2FAResetFixtureOptions{enableSMTP: true})
	fixture.repo.resetErr = service.ErrUserSecurityStateChanged

	_, err := fixture.svc.AdminResetUserTOTP(context.Background(), admin2FAResetInput("用户忘记动态口令"))
	require.Error(t, err)
	require.ErrorIs(t, err, service.ErrUserSecurityStateChanged)
	require.Empty(t, fixture.audit.entries)
	require.Equal(t, int64(0), fixture.smtp.messages.Load())
}

// 撤销能力缺失时必须如实上报：重置本身可以继续（否则用户永远登不上），
// 但 result.RevokedStepUpGrants=false 且审计照记，前端据此提示「可能仍有旧授权」。
func TestAdminResetUserTOTPReportsUnrevokedStepUpGrants(t *testing.T) {
	fixture := newAdmin2FAResetFixture(t, admin2FAResetFixtureOptions{plainCache: true})
	// 只替换 step-up 撤销器为永远失败的实现，其余装配保持真实。
	svc := service.NewAdmin2FAResetService(service.Admin2FAResetDeps{
		UserRepo:      fixture.repo,
		Totp:          service.NewTotpService(fixture.repo, nil, plainTotpCache{fixture.totpCache}, nil, nil, nil),
		RefreshCache:  nil,
		StepUp:        disablingStepUpRevoker{},
		Notifications: nil,
		Audit:         service.NewAuditLogService(fixture.audit, nil),
	})

	result, err := svc.AdminResetUserTOTP(context.Background(), admin2FAResetInput("用户忘记动态口令"))
	require.NoError(t, err)
	require.False(t, result.RevokedStepUpGrants)
	require.Len(t, fixture.audit.entries, 1)
	require.Equal(t, false, fixture.audit.entries[0].Extra["revoked_step_up_grants"])
}

// 重置提交之后，目标用户已经签发的 access token 同样作废（用户端 / 管理端 JWT 中间件查的就是这个撤销时间点）：
// 只删 refresh token 挡不住无状态的 access JWT，它们默认还能用 60 分钟（审计 AUD-6）。
// 旁观者用户不受影响，重置之后新签发的 token 照常可用。
func TestAdminResetUserTOTPRevokesAlreadyIssuedAccessTokens(t *testing.T) {
	fixture := newAdmin2FAResetFixture(t, admin2FAResetFixtureOptions{})
	ctx := context.Background()
	authSvc := service.NewAuthService(nil, nil, nil, fixture.refreshCache, nil, nil, nil, nil, nil, nil, nil, nil, nil)
	issuedBefore := time.Now().UTC().Add(-10 * time.Minute)
	require.False(t, authSvc.IsAccessTokenRevoked(ctx, 3249, issuedBefore), "重置前仍然有效")

	resetStart := time.Now().UTC()
	result, err := fixture.svc.AdminResetUserTOTP(ctx, admin2FAResetInput("用户忘记动态口令"))
	require.NoError(t, err)
	require.True(t, result.RevokedStepUpGrants)
	require.NotContains(t, result.IncompleteSteps, service.Admin2FAResetStepSessionRevocation)

	require.True(t, authSvc.IsAccessTokenRevoked(ctx, 3249, issuedBefore), "重置时刻之前签发的 access token 必须作废")
	require.True(t, authSvc.IsAccessTokenRevoked(ctx, 3249, resetStart.Add(-time.Second)))
	require.False(t, authSvc.IsAccessTokenRevoked(ctx, 3249, time.Now().UTC().Add(5*time.Second)), "重置之后新签发的 token 不受影响")
	require.False(t, authSvc.IsAccessTokenRevoked(ctx, 99, issuedBefore), "旁观者用户不受影响")
}

// 写库没成功（并发改了状态）时什么都不撤：不会出现「用户被登出了、2FA 却没变」的半成功状态。
func TestAdminResetUserTOTPDoesNotRevokeAccessTokensWhenTheWriteFails(t *testing.T) {
	fixture := newAdmin2FAResetFixture(t, admin2FAResetFixtureOptions{})
	fixture.repo.resetErr = service.ErrUserSecurityStateChanged
	ctx := context.Background()

	_, err := fixture.svc.AdminResetUserTOTP(ctx, admin2FAResetInput("用户忘记动态口令"))
	require.ErrorIs(t, err, service.ErrUserSecurityStateChanged)
	authSvc := service.NewAuthService(nil, nil, nil, fixture.refreshCache, nil, nil, nil, nil, nil, nil, nil, nil, nil)
	require.False(t, authSvc.IsAccessTokenRevoked(ctx, 3249, time.Now().UTC().Add(-time.Minute)))
}

// access token 撤销失败（调用失败 / 缓存根本不支持撤销）：写库已提交，请求不能变成 5xx；
// 失败如实记入 incomplete_steps（session_revocation）与审计，revoked_step_up_grants 为 false，
// 其余善后（refresh token、step-up 授权）照常完成。
func TestAdminResetUserTOTPReportsAccessTokenRevocationFailureWithoutFailingTheRequest(t *testing.T) {
	failing := func(c service.RefreshTokenCache) service.RefreshTokenCache { return accessRevocationFailingCache{c} }
	unsupported := func(c service.RefreshTokenCache) service.RefreshTokenCache {
		return refreshCacheWithoutAccessRevocation{c}
	}
	for name, wrap := range map[string]func(service.RefreshTokenCache) service.RefreshTokenCache{
		"revocation call fails":           failing,
		"cache has no revocation support": unsupported,
	} {
		t.Run(name, func(t *testing.T) {
			fixture := newAdmin2FAResetFixture(t, admin2FAResetFixtureOptions{})
			ctx := context.Background()
			require.NoError(t, fixture.totpCache.SetStepUpGrant(ctx, fixture.user.ID, "family-a", service.FactorTOTP, service.StepUpGrantTTL))
			svc := service.NewAdmin2FAResetService(service.Admin2FAResetDeps{
				UserRepo:      fixture.repo,
				Totp:          fixture.totp,
				RefreshCache:  wrap(fixture.refreshCache),
				StepUp:        fixture.stepUp,
				Notifications: fixture.notifications,
				Audit:         service.NewAuditLogService(fixture.audit, nil),
			})

			result, err := svc.AdminResetUserTOTP(ctx, admin2FAResetInput("用户忘记动态口令"))
			require.NoError(t, err, "写库已提交，善后失败不能变成请求失败")
			require.Equal(t, 1, fixture.repo.resetCalls)
			require.Contains(t, result.IncompleteSteps, service.Admin2FAResetStepSessionRevocation)
			require.False(t, result.RevokedStepUpGrants)

			// 其余善后照常做完：refresh token 已删，step-up 授权已撤。
			_, err = fixture.refreshCache.GetRefreshToken(ctx, "hash-a")
			require.ErrorIs(t, err, service.ErrRefreshTokenNotFound)
			granted, err := fixture.totpCache.HasStepUpGrant(ctx, fixture.user.ID, "family-a", service.FactorTOTP)
			require.NoError(t, err)
			require.False(t, granted)

			require.Len(t, fixture.audit.entries, 1)
			require.Contains(t, fmt.Sprint(fixture.audit.entries[0].Extra["incomplete_steps"]), service.Admin2FAResetStepSessionRevocation)
			require.Equal(t, false, fixture.audit.entries[0].Extra["revoked_step_up_grants"])
		})
	}
}

// accessRevocationFailingCache 支持撤销 access token，但调用总是失败。
type accessRevocationFailingCache struct{ service.RefreshTokenCache }

func (accessRevocationFailingCache) RevokeAccessTokensBefore(context.Context, int64, time.Time, time.Duration) error {
	return errors.New("redis unavailable")
}

func (accessRevocationFailingCache) AccessTokensRevokedBefore(context.Context, int64) (time.Time, error) {
	return time.Time{}, nil
}

// refreshCacheWithoutAccessRevocation 只暴露 service.RefreshTokenCache 接口本身，屏蔽掉 access token 撤销能力。
type refreshCacheWithoutAccessRevocation struct{ service.RefreshTokenCache }

// 装配缺失（仓储不支持 TotpAdminResetter）时必须 fail-closed，而不是静默成功。
func TestAdminResetUserTOTPUnavailableWithoutResetter(t *testing.T) {
	fixture := newAdmin2FAResetFixture(t, admin2FAResetFixtureOptions{})
	// 只暴露 GetByID 的仓储：不实现 service.TotpAdminResetter。
	svc := service.NewAdmin2FAResetService(service.Admin2FAResetDeps{
		UserRepo: &admin2FAResetPlainRepo{inner: fixture.repo},
		Totp:     newTotpServiceForResetterTest(fixture),
		StepUp:   fixture.stepUp,
		Audit:    service.NewAuditLogService(fixture.audit, nil),
	})
	_, err := svc.AdminResetUserTOTP(context.Background(), admin2FAResetInput("用户忘记动态口令"))
	require.ErrorIs(t, err, service.ErrAdmin2FAResetUnavailable)
}

type admin2FAResetPlainRepo struct {
	service.UserRepository
	inner *admin2FAResetRepo
}

func (r *admin2FAResetPlainRepo) GetByID(ctx context.Context, id int64) (*service.User, error) {
	return r.inner.GetByID(ctx, id)
}

// newTotpServiceForResetterTest 复用 fixture 里的 TotpService（同一个 Redis）。
func newTotpServiceForResetterTest(fixture *admin2FAResetFixture) *service.TotpService {
	svc := service.NewTotpService(fixture.repo, nil, fixture.totpCache, nil, nil, nil)
	return svc
}

// plainTotpCache 只暴露 service.TotpCache 接口本身，屏蔽掉具体实现上的
// TwoFactorGrantStore 能力（模拟旧/简化装配）。
type plainTotpCache struct{ service.TotpCache }

// disablingStepUpRevoker 模拟撤销能力缺失：用于验证「撤销失败如实上报」。
type disablingStepUpRevoker struct{}

func (disablingStepUpRevoker) RevokeStepUpGrantsForUser(context.Context, int64, []string) (int64, error) {
	return 0, errors.New("step-up revoke unavailable")
}

// 每次重置都必须是独立的一封邮件：同一用户连续两次重置不能被投递台账吞掉。
func TestAdminResetUserTOTPNotificationNotDeduplicatedAcrossResets(t *testing.T) {
	fixture := newAdmin2FAResetFixture(t, admin2FAResetFixtureOptions{enableSMTP: true})
	ctx := context.Background()

	_, err := fixture.svc.AdminResetUserTOTP(ctx, admin2FAResetInput("第一次重置"))
	require.NoError(t, err)
	require.Equal(t, int64(1), fixture.smtp.messages.Load())

	// 用户重新绑定后又被重置一次。
	fixture.user.TotpEnabled = true
	secret := "v1:second-secret"
	fixture.user.TotpSecretEncrypted = &secret
	_, err = fixture.svc.AdminResetUserTOTP(ctx, admin2FAResetInput("第二次重置"))
	require.NoError(t, err)
	require.Equal(t, int64(2), fixture.smtp.messages.Load(), "each reset must notify the user")
}

// 邮件模板必须中英双语都有，且不可退订（安全通知）。
func TestAdminSecurityResetEmailTemplateIsRegisteredForBothLocales(t *testing.T) {
	svc := service.NewNotificationEmailService(&admin2FAResetSettingsRepo{values: map[string]string{}}, nil)
	for _, locale := range []string{"en", "zh"} {
		tmpl, err := svc.GetTemplate(context.Background(), service.NotificationEmailEventAdminSecurityTwoFactorReset, locale)
		require.NoError(t, err)
		require.NotEmpty(t, tmpl.Subject)
		require.Contains(t, tmpl.HTML, "{{reset_reason}}")
		require.Contains(t, tmpl.HTML, "{{reset_at}}")
		require.False(t, tmpl.IsCustom)
	}
	found := false
	for _, info := range svc.ListEventInfos() {
		if info.Event != service.NotificationEmailEventAdminSecurityTwoFactorReset {
			continue
		}
		found = true
		require.False(t, info.Optional, "security notice must not be unsubscribable")
		for _, placeholder := range []string{"reset_reason", "reset_at", "operator_name", "passkey_retained", "remaining_factors", "rebind_url", "rebind_link_display"} {
			require.Contains(t, info.Placeholders, placeholder)
		}
	}
	require.True(t, found, "the reset notice must be listed in the admin template catalog")
}

// 确保 errors 被使用（后续加错误分支时不至于编译失败）。
var _ = errors.Is
