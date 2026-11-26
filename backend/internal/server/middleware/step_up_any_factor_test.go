package middleware

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

// 本文件覆盖 step-up 的「任选其一」规则（老板 2026-11-26：同时绑定 TOTP 与 Passkey 的账号，
// 验证其中任意一个因子即可，不再强制两个）：
//
//   - 规则的唯一实现是 service.StepUpSatisfiedFactors，middleware 只负责读取已绑定因子的授权；
//   - 只绑定一个因子的账号、没有任何因子的账号行为与改动前完全一致；
//   - 未绑定因子的授权永远不算，存储/策略读取故障、过期授权、授权缺失一律 fail-closed；
//   - 登录不受影响（见 handler/login_factor_policy_test.go：登录仍要求全部已绑定因子）。

var (
	boundTOTP    = []service.Factor{service.FactorTOTP}
	boundPasskey = []service.Factor{service.FactorPasskey}
	boundBoth    = []service.Factor{service.FactorTOTP, service.FactorPasskey}
)

func grantedFactors(factors ...service.Factor) map[service.Factor]bool {
	granted := make(map[service.Factor]bool, len(factors))
	for _, factor := range factors {
		granted[factor] = true
	}
	return granted
}

type anyFactorRow struct {
	name string
	stub factorStepUpStub
	// 放行时 X-StepUp-Factors 的期望值；留空表示应当拒绝。
	wantFactors string
	// 拒绝时的期望 HTTP 状态与机器码。
	wantStatus int
	wantCode   string
}

// anyFactorRows 是老板要求的矩阵：{只绑 TOTP, 只绑 Passkey, 两者都绑} ×
// {没有凭证, 只有 TOTP 凭证, 只有 Passkey 凭证, 两个都有, 过期凭证, 未绑定因子的凭证, 存储故障, Passkey 服务不可用}。
func anyFactorRows() []anyFactorRow {
	errRedis := errors.New("redis down")
	return []anyFactorRow{
		// ---------- 只绑定 TOTP（改动前后一致）----------
		{name: "totp-only / no credential", stub: factorStepUpStub{required: boundTOTP},
			wantStatus: 403, wantCode: "STEP_UP_REQUIRED"},
		{name: "totp-only / totp credential", stub: factorStepUpStub{required: boundTOTP, granted: grantedFactors(service.FactorTOTP)},
			wantFactors: "totp"},
		{name: "totp-only / passkey credential only (unbound factor)", stub: factorStepUpStub{required: boundTOTP, granted: grantedFactors(service.FactorPasskey)},
			wantStatus: 403, wantCode: "STEP_UP_REQUIRED"},
		{name: "totp-only / totp credential plus leftover passkey credential", stub: factorStepUpStub{required: boundTOTP, granted: grantedFactors(service.FactorTOTP, service.FactorPasskey)},
			wantFactors: "totp"},
		{name: "totp-only / expired credential", stub: factorStepUpStub{required: boundTOTP, granted: grantedFactors(service.FactorTOTP), expired: grantedFactors(service.FactorTOTP)},
			wantStatus: 403, wantCode: "STEP_UP_REQUIRED"},
		{name: "totp-only / grant store error", stub: factorStepUpStub{required: boundTOTP, grantErr: map[service.Factor]error{service.FactorTOTP: errRedis}},
			wantStatus: 503, wantCode: "STEP_UP_UNAVAILABLE"},
		// 未绑定因子的状态与结果完全无关：连它的读取故障都不会被碰到。
		{name: "totp-only / unbound passkey store error is never read", stub: factorStepUpStub{required: boundTOTP, granted: grantedFactors(service.FactorTOTP), grantErr: map[service.Factor]error{service.FactorPasskey: errRedis}},
			wantFactors: "totp"},

		// ---------- 只绑定 Passkey（改动前后一致）----------
		{name: "passkey-only / no credential", stub: factorStepUpStub{required: boundPasskey},
			wantStatus: 403, wantCode: "STEP_UP_REQUIRED"},
		{name: "passkey-only / passkey credential", stub: factorStepUpStub{required: boundPasskey, granted: grantedFactors(service.FactorPasskey)},
			wantFactors: "passkey"},
		{name: "passkey-only / totp credential only (unbound factor)", stub: factorStepUpStub{required: boundPasskey, granted: grantedFactors(service.FactorTOTP)},
			wantStatus: 403, wantCode: "STEP_UP_REQUIRED"},
		{name: "passkey-only / passkey credential plus leftover totp credential", stub: factorStepUpStub{required: boundPasskey, granted: grantedFactors(service.FactorTOTP, service.FactorPasskey)},
			wantFactors: "passkey"},
		{name: "passkey-only / unbound totp store error is never read", stub: factorStepUpStub{required: boundPasskey, granted: grantedFactors(service.FactorPasskey), grantErr: map[service.Factor]error{service.FactorTOTP: errRedis}},
			wantFactors: "passkey"},
		{name: "passkey-only / expired credential", stub: factorStepUpStub{required: boundPasskey, granted: grantedFactors(service.FactorPasskey), expired: grantedFactors(service.FactorPasskey)},
			wantStatus: 403, wantCode: "STEP_UP_REQUIRED"},
		{name: "passkey-only / grant store error", stub: factorStepUpStub{required: boundPasskey, grantErr: map[service.Factor]error{service.FactorPasskey: errRedis}},
			wantStatus: 503, wantCode: "STEP_UP_UNAVAILABLE"},
		// Passkey 服务全局不可用：拿不到 Passkey 凭证，唯一的因子过不了——仍然锁死，不会降级。
		{name: "passkey-only / passkey service unavailable (no credential can exist)", stub: factorStepUpStub{required: boundPasskey},
			wantStatus: 403, wantCode: "STEP_UP_REQUIRED"},

		// ---------- 两者都绑定（本次改动）----------
		{name: "both / no credential", stub: factorStepUpStub{required: boundBoth},
			wantStatus: 403, wantCode: "STEP_UP_REQUIRED"},
		{name: "both / totp credential only", stub: factorStepUpStub{required: boundBoth, granted: grantedFactors(service.FactorTOTP)},
			wantFactors: "totp"},
		{name: "both / passkey credential only", stub: factorStepUpStub{required: boundBoth, granted: grantedFactors(service.FactorPasskey)},
			wantFactors: "passkey"},
		{name: "both / both credentials", stub: factorStepUpStub{required: boundBoth, granted: grantedFactors(service.FactorTOTP, service.FactorPasskey)},
			wantFactors: "totp,passkey"},
		{name: "both / totp expired, passkey valid", stub: factorStepUpStub{required: boundBoth, granted: grantedFactors(service.FactorTOTP, service.FactorPasskey), expired: grantedFactors(service.FactorTOTP)},
			wantFactors: "passkey"},
		{name: "both / totp valid, passkey expired", stub: factorStepUpStub{required: boundBoth, granted: grantedFactors(service.FactorTOTP, service.FactorPasskey), expired: grantedFactors(service.FactorPasskey)},
			wantFactors: "totp"},
		{name: "both / both expired", stub: factorStepUpStub{required: boundBoth, granted: grantedFactors(service.FactorTOTP, service.FactorPasskey), expired: grantedFactors(service.FactorTOTP, service.FactorPasskey)},
			wantStatus: 403, wantCode: "STEP_UP_REQUIRED"},
		// 未知因子名的凭证不是任何已绑定因子的凭证。
		{name: "both / credential of an unknown factor only", stub: factorStepUpStub{required: boundBoth, granted: grantedFactors(service.Factor("sms"))},
			wantStatus: 403, wantCode: "STEP_UP_REQUIRED"},
		// 存储故障：读不出来既不当成「已授权」也不当成「没授权」，一律 503，哪怕另一个因子是好的。
		{name: "both / totp valid, passkey grant store error", stub: factorStepUpStub{required: boundBoth, granted: grantedFactors(service.FactorTOTP), grantErr: map[service.Factor]error{service.FactorPasskey: errRedis}},
			wantStatus: 503, wantCode: "STEP_UP_UNAVAILABLE"},
		{name: "both / totp grant store error, passkey valid", stub: factorStepUpStub{required: boundBoth, granted: grantedFactors(service.FactorPasskey), grantErr: map[service.Factor]error{service.FactorTOTP: errRedis}},
			wantStatus: 503, wantCode: "STEP_UP_UNAVAILABLE"},
		{name: "both / policy read error even though both credentials exist", stub: factorStepUpStub{factorErr: errors.New("db down"), granted: grantedFactors(service.FactorTOTP, service.FactorPasskey)},
			wantStatus: 503, wantCode: "STEP_UP_UNAVAILABLE"},
		// Passkey 服务全局不可用（Passkey 凭证拿不到）：TOTP 已验证就通过（新规则）；TOTP 也没验证则照旧拒绝。
		{name: "both / passkey service unavailable, totp verified", stub: factorStepUpStub{required: boundBoth, granted: grantedFactors(service.FactorTOTP)},
			wantFactors: "totp"},
		{name: "both / passkey service unavailable, totp not verified", stub: factorStepUpStub{required: boundBoth},
			wantStatus: 403, wantCode: "STEP_UP_REQUIRED"},

		// ---------- 没有任何因子 ----------
		// 空集合永远不算满足：即使有人往存储里塞了两个因子的授权，也走 STEP_UP_TOTP_NOT_ENABLED。
		{name: "no factor / credentials of both factors lying around", stub: factorStepUpStub{required: nil, granted: grantedFactors(service.FactorTOTP, service.FactorPasskey)},
			wantStatus: 403, wantCode: "STEP_UP_TOTP_NOT_ENABLED"},
	}
}

// anyFactorEntry 是同一条判定的几个入口：组级中间件 / handler 级 EnforceStepUp（读开关）、
// EnforceStepUpAlways（不读开关）、要求新鲜度的变体（EnforceFreshStepUp，窗口放宽到 2 分钟，默认 4 分钟剩余 = 1 分钟前签发）。
type anyFactorEntry struct {
	name string
	run  func(c *gin.Context, stub factorStepUpStub) bool
}

func anyFactorEntries() []anyFactorEntry {
	user := &service.User{ID: 3, TotpEnabled: true}
	reader := stubStepUpUserReader{user: user}
	return []anyFactorEntry{
		{"EnforceStepUp (switch on)", func(c *gin.Context, stub factorStepUpStub) bool {
			return enforceStepUp(c, stub, reader, stepUpEnabled)
		}},
		{"EnforceStepUpAlways (no switch read)", func(c *gin.Context, stub factorStepUpStub) bool {
			return enforceStepUp(c, stub, reader, nil)
		}},
		{"EnforceFreshStepUp (2 minute window)", func(c *gin.Context, stub factorStepUpStub) bool {
			return enforceStepUpWithin(c, stub, reader, stepUpEnabled, 2*time.Minute)
		}},
	}
}

func TestStepUpAnyFactorMatrix(t *testing.T) {
	for _, entry := range anyFactorEntries() {
		for _, row := range anyFactorRows() {
			t.Run(entry.name+"/"+row.name, func(t *testing.T) {
				c, rec := newStepUpTestContext(t)
				c.Set(string(ContextKeyUser), AuthSubject{UserID: 3})

				ok := entry.run(c, row.stub)
				if row.wantFactors != "" {
					require.True(t, ok, "应当放行: %s", rec.Body.String())
					require.False(t, c.IsAborted())
					require.Equal(t, "1", rec.Header().Get(StepUpSkippedHeader))
					require.Equal(t, row.wantFactors, rec.Header().Get(StepUpFactorsHeader))
					require.Empty(t, rec.Body.String(), "放行时不得写响应体")
					return
				}
				require.False(t, ok, "应当拒绝")
				require.True(t, c.IsAborted())
				require.Equal(t, row.wantStatus, rec.Code, rec.Body.String())
				require.Contains(t, rec.Body.String(), `"code":"`+row.wantCode+`"`)
				// 拒绝的请求没有跳过任何验证：不得带「已跳过」响应头。
				require.Empty(t, rec.Header().Get(StepUpSkippedHeader))
				require.Empty(t, rec.Header().Get(StepUpRemainingHeader))
				require.Empty(t, rec.Header().Get(StepUpFactorsHeader))
			})
		}
	}
}

// 路由级中间件：只有放行才会进入后面的 handler，拒绝时 handler 一次都不执行（敏感操作没有发生）。
func TestStepUpAnyFactorMiddlewareOnlyRunsTheHandlerWhenSatisfied(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, row := range anyFactorRows() {
		t.Run(row.name, func(t *testing.T) {
			executed := 0
			router := gin.New()
			router.Use(func(c *gin.Context) {
				c.Set(string(ContextKeyUser), AuthSubject{UserID: 3})
				c.Set(ContextKeySessionID, "session-a")
			})
			router.PUT("/api/v1/admin/settings",
				stepUpAuth(row.stub, stubStepUpUserReader{user: &service.User{ID: 3, TotpEnabled: true}}, stepUpEnabled),
				func(c *gin.Context) {
					executed++
					c.Status(http.StatusNoContent)
				})
			rec := httptest.NewRecorder()
			router.ServeHTTP(rec, httptest.NewRequest(http.MethodPut, "/api/v1/admin/settings", nil))

			if row.wantFactors != "" {
				require.Equal(t, http.StatusNoContent, rec.Code, rec.Body.String())
				require.Equal(t, 1, executed)
				return
			}
			require.Equal(t, row.wantStatus, rec.Code, rec.Body.String())
			require.Zero(t, executed, "拒绝时敏感操作不得执行")
		})
	}
}

// 挑战体：403 STEP_UP_REQUIRED 明确告诉客户端「已绑定因子任选其一」。
func TestStepUpChallengePayloadTellsTheClientAnyOneFactorIsEnough(t *testing.T) {
	for _, tc := range []struct {
		name        string
		bound       []service.Factor
		wantFactors string
	}{
		{"both bound", boundBoth, "totp,passkey"},
		{"totp only", boundTOTP, "totp"},
		{"passkey only", boundPasskey, "passkey"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c, rec := newStepUpTestContext(t)
			c.Set(string(ContextKeyUser), AuthSubject{UserID: 3})

			ok := enforceStepUp(c, factorStepUpStub{required: tc.bound}, stubStepUpUserReader{user: &service.User{ID: 3}}, stepUpEnabled)
			require.False(t, ok)
			require.Equal(t, http.StatusForbidden, rec.Code)

			var body struct {
				Code     string            `json:"code"`
				Message  string            `json:"message"`
				Metadata map[string]string `json:"metadata"`
			}
			require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &body))
			require.Equal(t, "STEP_UP_REQUIRED", body.Code)
			require.NotEmpty(t, body.Message)
			require.Equal(t, "any", body.Metadata["step_up_mode"])
			require.Equal(t, tc.wantFactors, body.Metadata["factors"])
		})
	}

	// 其它拒绝（没有任何因子 / 故障）不是「再验证一次」的挑战，不带 step_up_mode。
	c, rec := newStepUpTestContext(t)
	c.Set(string(ContextKeyUser), AuthSubject{UserID: 3})
	require.False(t, enforceStepUp(c, factorStepUpStub{}, stubStepUpUserReader{user: &service.User{ID: 3}}, stepUpEnabled))
	require.NotContains(t, rec.Body.String(), "step_up_mode")
}

// 要求新鲜度时拒绝的挑战同样是任选其一，并且带「需要新鲜验证」的提示。
func TestStepUpFreshChallengeIsAlsoAnyOneFactor(t *testing.T) {
	grants := factorAgedGrants{
		service.FactorTOTP:    service.StepUpGrantTTL - 3*time.Minute,
		service.FactorPasskey: service.StepUpGrantTTL - 4*time.Minute,
	}
	passed, rec := enforceFreshForUser1(t, grants, freshTestWindow)
	require.False(t, passed)
	require.Contains(t, rec.Body.String(), `"step_up_mode":"any"`)
	require.Contains(t, rec.Body.String(), `"factors":"totp,passkey"`)
	require.Contains(t, rec.Body.String(), "fresh")
}

// 没有授权被拒时，前面的路由级门控可能已经写过「已跳过验证码」的响应头：必须撤掉，
// 否则前端会提示「刚刚免验证执行了敏感操作」。
func TestStepUpDenialDropsTheSkipHeadersAnEarlierGateWrote(t *testing.T) {
	c, rec := newStepUpTestContext(t)
	c.Set(string(ContextKeyUser), AuthSubject{UserID: 3})
	c.Header(StepUpSkippedHeader, "1")
	c.Header(StepUpRemainingHeader, "120")
	c.Header(StepUpFactorsHeader, "totp")

	require.False(t, enforceStepUp(c, factorStepUpStub{required: boundBoth}, stubStepUpUserReader{user: &service.User{ID: 3}}, stepUpEnabled))
	require.Empty(t, rec.Header().Get(StepUpSkippedHeader))
	require.Empty(t, rec.Header().Get(StepUpRemainingHeader))
	require.Empty(t, rec.Header().Get(StepUpFactorsHeader))
}

// 审计：放行时记录是哪个（些）因子满足了 step-up，拒绝时记录缺哪些因子；只有因子名，没有任何凭证材料。
func TestStepUpAuditRecordsWhichFactorSatisfiedIt(t *testing.T) {
	for _, tc := range []struct {
		name        string
		stub        factorStepUpStub
		wantExtra   string
		wantSatisfy bool
	}{
		{"totp satisfied it", factorStepUpStub{required: boundBoth, granted: grantedFactors(service.FactorTOTP)}, "totp", true},
		{"passkey satisfied it", factorStepUpStub{required: boundBoth, granted: grantedFactors(service.FactorPasskey)}, "passkey", true},
		{"both satisfied it", factorStepUpStub{required: boundBoth, granted: grantedFactors(service.FactorTOTP, service.FactorPasskey)}, "totp,passkey", true},
		{"nothing satisfied it", factorStepUpStub{required: boundBoth}, "totp,passkey", false},
		{"single factor missing", factorStepUpStub{required: boundPasskey}, "passkey", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c, _ := newStepUpTestContext(t)
			c.Set(string(ContextKeyUser), AuthSubject{UserID: 3})

			ok := enforceStepUp(c, tc.stub, stubStepUpUserReader{user: &service.User{ID: 3}}, stepUpEnabled)
			require.Equal(t, tc.wantSatisfy, ok)

			raw, exists := c.Get(auditCtxKeyExtra)
			require.True(t, exists, "step-up 结果必须进入审计附加字段")
			extra, isMap := raw.(map[string]any)
			require.True(t, isMap)
			if tc.wantSatisfy {
				require.Equal(t, tc.wantExtra, extra["two_factor_satisfied_factors"])
				require.NotContains(t, extra, "two_factor_missing_factors")
			} else {
				require.Equal(t, tc.wantExtra, extra["two_factor_missing_factors"])
				require.NotContains(t, extra, "two_factor_satisfied_factors")
			}
		})
	}
}

// 端到端：经过真实审计中间件，落库的那条敏感操作审计记录里带着满足 step-up 的因子。
func TestStepUpSatisfiedFactorReachesThePersistedAuditEntry(t *testing.T) {
	gin.SetMode(gin.TestMode)
	capture := &auditCaptureRepository{}
	auditService := service.NewAuditLogService(capture, nil)
	auditService.Start()

	router := gin.New()
	router.Use(func(c *gin.Context) {
		c.Set(string(ContextKeyUser), AuthSubject{UserID: 3})
		c.Set(string(ContextKeyUserRole), service.RoleAdmin)
		c.Set(ContextKeySessionID, "session-a")
		c.Next()
	})
	router.Use(gin.HandlerFunc(NewAuditLogMiddleware(auditService)))
	stub := factorStepUpStub{required: boundBoth, granted: grantedFactors(service.FactorPasskey)}
	router.PUT("/api/v1/admin/settings",
		stepUpAuth(stub, stubStepUpUserReader{user: &service.User{ID: 3, TotpEnabled: true}}, stepUpEnabled),
		func(c *gin.Context) { c.Status(http.StatusNoContent) })
	router.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodPut, "/api/v1/admin/settings", nil))
	auditService.Stop()

	capture.mu.Lock()
	logs := append([]*service.AuditLog(nil), capture.logs...)
	capture.mu.Unlock()
	require.Len(t, logs, 1)
	require.Equal(t, http.StatusNoContent, logs[0].StatusCode)
	require.Equal(t, "passkey", logs[0].Extra["two_factor_satisfied_factors"])
}

// ---------- 用真实的 TotpService 把整条链路串起来（EnforceStepUp / EnforceStepUpAlways / EnforceFreshStepUp）----------

type anyFactorUserRepo struct {
	service.UserRepository
	user *service.User
}

func (r anyFactorUserRepo) GetByID(context.Context, int64) (*service.User, error) {
	copied := *r.user
	return &copied, nil
}

func (anyFactorUserRepo) GetUserAvatar(context.Context, int64) (*service.UserAvatar, error) {
	return nil, nil
}

type anyFactorPasskeyRepo struct {
	service.PasskeyRepository
	records []service.PasskeyCredentialRecord
	err     error
}

func (r anyFactorPasskeyRepo) ListByUserID(context.Context, int64) ([]service.PasskeyCredentialRecord, error) {
	return r.records, r.err
}

type anyFactorSettingRepo struct {
	service.SettingRepository
}

func (anyFactorSettingRepo) GetValue(_ context.Context, key string) (string, error) {
	if key == service.SettingKeyStepUpEnabled {
		return "true", nil
	}
	return "", service.ErrSettingNotFound
}

// anyFactorGrantCache 按「用户 + 会话 + 因子」保存授权的剩余有效期，与生产的 Redis 键维度一致。
type anyFactorGrantCache struct {
	service.TotpCache
	grants map[string]time.Duration
}

func anyFactorGrantKey(userID int64, session string, factor service.Factor) string {
	return fmt.Sprintf("%d|%s|%s", userID, session, factor)
}

func (c *anyFactorGrantCache) HasStepUpGrant(_ context.Context, userID int64, session string, factor service.Factor) (bool, error) {
	_, ok := c.grants[anyFactorGrantKey(userID, session, factor)]
	return ok, nil
}

func (c *anyFactorGrantCache) StepUpGrantRemaining(_ context.Context, userID int64, session string, factor service.Factor) (time.Duration, bool, error) {
	remaining, ok := c.grants[anyFactorGrantKey(userID, session, factor)]
	return remaining, ok, nil
}

type anyFactorWorld struct {
	// bound 决定用户行与 Passkey 仓库里的真实绑定状态（由真实的 TotpService.RequiredFactors 读出）。
	totpBound    bool
	passkeyBound bool
	passkeyErr   error
	// grants 是 Redis 里现存的授权（用户 3 / 会话 session-a 之外的键用于验证「别人的授权」不算数）。
	grants map[string]time.Duration
}

func freshGrant(userID int64, session string, factor service.Factor) (string, time.Duration) {
	return anyFactorGrantKey(userID, session, factor), service.StepUpGrantTTL - 10*time.Second
}

func (w anyFactorWorld) services() (*service.TotpService, *service.UserService, *service.SettingService) {
	user := &service.User{ID: 3, TotpEnabled: w.totpBound, Role: service.RoleAdmin, Status: service.StatusActive}
	settings := service.NewSettingService(anyFactorSettingRepo{}, nil)
	totpSvc := service.NewTotpService(anyFactorUserRepo{user: user}, nil, &anyFactorGrantCache{grants: w.grants}, settings, nil, nil)
	if w.passkeyBound || w.passkeyErr != nil {
		repo := anyFactorPasskeyRepo{err: w.passkeyErr}
		if w.passkeyBound {
			repo.records = []service.PasskeyCredentialRecord{{ID: 9, UserID: 3}}
		}
		totpSvc.SetPasskeyRepository(repo)
	}
	return totpSvc, service.NewUserService(anyFactorUserRepo{user: user}, nil, nil, nil), settings
}

func TestStepUpAnyFactorThroughTheExportedEntryPointsWithARealTotpService(t *testing.T) {
	grantsFor := func(factors ...service.Factor) map[string]time.Duration {
		grants := map[string]time.Duration{}
		for _, factor := range factors {
			key, remaining := freshGrant(3, "session-a", factor)
			grants[key] = remaining
		}
		return grants
	}
	otherSession, otherSessionRemaining := freshGrant(3, "session-b", service.FactorTOTP)
	otherUser, otherUserRemaining := freshGrant(4, "session-a", service.FactorPasskey)

	rows := []struct {
		name       string
		world      anyFactorWorld
		wantStatus int // 0 = 放行
		wantCode   string
	}{
		{"both bound / totp only", anyFactorWorld{totpBound: true, passkeyBound: true, grants: grantsFor(service.FactorTOTP)}, 0, ""},
		{"both bound / passkey only", anyFactorWorld{totpBound: true, passkeyBound: true, grants: grantsFor(service.FactorPasskey)}, 0, ""},
		{"both bound / both", anyFactorWorld{totpBound: true, passkeyBound: true, grants: grantsFor(service.FactorTOTP, service.FactorPasskey)}, 0, ""},
		{"both bound / none", anyFactorWorld{totpBound: true, passkeyBound: true, grants: grantsFor()}, 403, "STEP_UP_REQUIRED"},
		{"both bound / grants of another session", anyFactorWorld{totpBound: true, passkeyBound: true, grants: map[string]time.Duration{otherSession: otherSessionRemaining}}, 403, "STEP_UP_REQUIRED"},
		{"both bound / grants of another user", anyFactorWorld{totpBound: true, passkeyBound: true, grants: map[string]time.Duration{otherUser: otherUserRemaining}}, 403, "STEP_UP_REQUIRED"},
		{"totp only / totp", anyFactorWorld{totpBound: true, grants: grantsFor(service.FactorTOTP)}, 0, ""},
		{"totp only / leftover passkey grant (unbound factor)", anyFactorWorld{totpBound: true, grants: grantsFor(service.FactorPasskey)}, 403, "STEP_UP_REQUIRED"},
		{"passkey only / passkey", anyFactorWorld{passkeyBound: true, grants: grantsFor(service.FactorPasskey)}, 0, ""},
		{"passkey only / leftover totp grant (unbound factor)", anyFactorWorld{passkeyBound: true, grants: grantsFor(service.FactorTOTP)}, 403, "STEP_UP_REQUIRED"},
		{"no factor / grants of both factors", anyFactorWorld{grants: grantsFor(service.FactorTOTP, service.FactorPasskey)}, 403, "STEP_UP_TOTP_NOT_ENABLED"},
		// 策略读取失败（Passkey 仓库故障）：连已经验证过的 TOTP 也不放行。
		{"both bound / passkey repository error", anyFactorWorld{totpBound: true, passkeyErr: errors.New("db down"), grants: grantsFor(service.FactorTOTP, service.FactorPasskey)}, 503, "STEP_UP_UNAVAILABLE"},
	}

	entries := []struct {
		name string
		run  func(c *gin.Context, totpSvc *service.TotpService, users *service.UserService, settings *service.SettingService) bool
	}{
		{"EnforceStepUp", func(c *gin.Context, totpSvc *service.TotpService, users *service.UserService, settings *service.SettingService) bool {
			return EnforceStepUp(c, totpSvc, users, settings)
		}},
		{"EnforceStepUpAlways", func(c *gin.Context, totpSvc *service.TotpService, users *service.UserService, _ *service.SettingService) bool {
			return EnforceStepUpAlways(c, totpSvc, users)
		}},
		{"EnforceFreshStepUp", func(c *gin.Context, totpSvc *service.TotpService, users *service.UserService, settings *service.SettingService) bool {
			return EnforceFreshStepUp(c, totpSvc, users, settings, time.Minute)
		}},
	}

	for _, entry := range entries {
		for _, row := range rows {
			t.Run(entry.name+"/"+row.name, func(t *testing.T) {
				totpSvc, users, settings := row.world.services()
				c, rec := newStepUpTestContext(t)
				c.Set(string(ContextKeyUser), AuthSubject{UserID: 3})
				c.Set(ContextKeySessionID, "session-a")

				ok := entry.run(c, totpSvc, users, settings)
				if row.wantStatus == 0 {
					require.True(t, ok, rec.Body.String())
					require.False(t, c.IsAborted())
					return
				}
				require.False(t, ok)
				require.Equal(t, row.wantStatus, rec.Code, rec.Body.String())
				require.Contains(t, rec.Body.String(), `"code":"`+row.wantCode+`"`)
			})
		}
	}
}
