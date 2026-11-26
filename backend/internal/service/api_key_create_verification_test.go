//go:build unit

package service

import (
	"context"
	"encoding/json"
	"fmt"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"

	"github.com/pquerna/otp/totp"
	"github.com/stretchr/testify/require"
)

// 「创建 API Key 时是否需要验证身份」的用户开关（api_key_create_verification.go）。
//
// 硬性判据：没有设置过开关（没有行 / 没注入仓储）的账号，行为与改造前逐字节一致——
// api_key_verification_*_test.go 的既有用例一个都不改就全过。这里测的是新增的部分：
// 账号类型 × 模式 × 创建时带不带校验，以及改开关本身的鉴权（只在变宽松时要校验）。

const (
	cvUserID   = int64(7)
	cvPassword = "correct-password"
)

// ---- 假实现 ----------------------------------------------------------------

// cvSwap 是一次成功的写入（旧值 → 新值，"" 表示没有行）以及是谁写的。
type cvSwap struct{ Writer, From, To string }

// cvPrefs 是 UserSecurityPreferenceRepository 的内存实现，语义与迁移 343 的表一致：
// 没有行 = ""，写入是带期望值的比较并交换。
type cvPrefs struct {
	mu   sync.Mutex
	rows map[int64]string

	getErr error
	casErr error
	// afterGet 在每次 Get 读到值之后、把这个（已经读到的）旧值交还给调用方之前执行：
	// 模拟「读完之后、写入之前，别的请求改了设置」的交错。参数是第几次 Get（从 1 开始）。
	afterGet func(call int)

	getCalls int
	swaps    []cvSwap
}

func newCVPrefs(stored string) *cvPrefs {
	p := &cvPrefs{rows: map[int64]string{}}
	p.set(cvUserID, stored)
	return p
}

func (p *cvPrefs) set(userID int64, value string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if value == "" || value == APIKeyCreateVerificationAuto {
		delete(p.rows, userID)
		return
	}
	p.rows[userID] = value
}

// label 返回账号当前的模式标签（没有行 = auto）。
func (p *cvPrefs) label(userID int64) string {
	p.mu.Lock()
	defer p.mu.Unlock()
	return apiKeyCreateVerificationModeLabel(p.rows[userID])
}

func (p *cvPrefs) swapLog() []cvSwap {
	p.mu.Lock()
	defer p.mu.Unlock()
	return append([]cvSwap(nil), p.swaps...)
}

func (p *cvPrefs) GetAPIKeyCreateVerificationMode(_ context.Context, userID int64) (string, error) {
	p.mu.Lock()
	p.getCalls++
	call := p.getCalls
	err := p.getErr
	value := p.rows[userID]
	hook := p.afterGet
	p.mu.Unlock()
	if err != nil {
		return "", err
	}
	if hook != nil {
		hook(call)
	}
	return value, nil
}

func (p *cvPrefs) CompareAndSetAPIKeyCreateVerificationMode(ctx context.Context, userID int64, expected, next string) (bool, error) {
	return p.casAs(ctx, "", userID, expected, next)
}

func (p *cvPrefs) casAs(_ context.Context, writer string, userID int64, expected, next string) (bool, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.casErr != nil {
		return false, p.casErr
	}
	if p.rows[userID] != expected {
		return false, nil
	}
	if next == "" {
		delete(p.rows, userID)
	} else {
		p.rows[userID] = next
	}
	p.swaps = append(p.swaps, cvSwap{Writer: writer, From: expected, To: next})
	return true, nil
}

// cvTaggedPrefs 让每个并发写者带着自己的名字写进同一份存储，事后能按写者检查转移记录。
type cvTaggedPrefs struct {
	*cvPrefs
	writer string
}

func (p cvTaggedPrefs) CompareAndSetAPIKeyCreateVerificationMode(ctx context.Context, userID int64, expected, next string) (bool, error) {
	return p.casAs(ctx, p.writer, userID, expected, next)
}

type cvPasskeyRepo struct {
	PasskeyRepository
	count int
}

func (r cvPasskeyRepo) ListByUserID(context.Context, int64) ([]PasskeyCredentialRecord, error) {
	records := make([]PasskeyCredentialRecord, r.count)
	for i := range records {
		records[i] = PasskeyCredentialRecord{ID: int64(i + 1), UserID: cvUserID, Name: "fixture-passkey"}
	}
	return records, nil
}

type cvPasskeySessions struct{ PasskeySessionStore }

// cvPasswordHash 只算一次 bcrypt：矩阵里有几百个用例，每个都重新哈希会白白多花几十秒。
var (
	cvHashOnce sync.Once
	cvHash     string
)

func cvPasswordHash() string {
	cvHashOnce.Do(func() {
		seed := &User{}
		if err := seed.SetPassword(cvPassword); err != nil {
			panic(err)
		}
		cvHash = seed.PasswordHash
	})
	return cvHash
}

// ---- 账号类型与真值表 ------------------------------------------------------

// cvAccount 的 required 是产品规格里的真值表，手写、不由被测代码算出来：
// 该账号在 auto / on / off 三种设置下，创建 Key 前是不是必须先校验身份。
type cvAccount struct {
	name     string
	signup   string
	totp     bool
	passkeys int
	// primary 是对这个账号可用的一个因子；一个可用因子都没有的账号为空串。
	primary  string
	required map[string]bool
}

func (a cvAccount) hasFactor() bool { return a.primary != "" }

func cvRequired(auto, on, off bool) map[string]bool {
	return map[string]bool{
		APIKeyCreateVerificationAuto: auto,
		APIKeyCreateVerificationOn:   on,
		APIKeyCreateVerificationOff:  off,
	}
}

var (
	// 只有登录密码：auto 不强制（旧行为）；on 就是输登录密码；off 不强制。
	cvPasswordOnly = cvAccount{name: "password only", signup: "email", primary: APIKeyFactorPassword, required: cvRequired(false, true, false)}
	// 第三方登录注册、没设过本地密码、没开 2FA：一个可用因子都没有，on 无法满足 → 一律不要求（不能锁死）。
	cvNoFactor         = cvAccount{name: "oauth without any factor", signup: "github", primary: "", required: cvRequired(false, false, false)}
	cvWithTOTP         = cvAccount{name: "email with totp", signup: "email", totp: true, primary: APIKeyFactorTOTP, required: cvRequired(true, true, false)}
	cvWithPasskey      = cvAccount{name: "email with passkey", signup: "email", passkeys: 1, primary: APIKeyFactorPasskey, required: cvRequired(true, true, false)}
	cvWithBoth         = cvAccount{name: "email with totp and passkey", signup: "email", totp: true, passkeys: 2, primary: APIKeyFactorPasskey, required: cvRequired(true, true, false)}
	cvOAuthWithTOTP    = cvAccount{name: "oauth with totp only", signup: "github", totp: true, primary: APIKeyFactorTOTP, required: cvRequired(true, true, false)}
	cvOAuthWithPasskey = cvAccount{name: "oauth with passkey only", signup: "github", passkeys: 1, primary: APIKeyFactorPasskey, required: cvRequired(true, true, false)}

	cvAccounts = []cvAccount{cvPasswordOnly, cvNoFactor, cvWithTOTP, cvWithPasskey, cvWithBoth, cvOAuthWithTOTP, cvOAuthWithPasskey}
)

var cvModes = []string{APIKeyCreateVerificationAuto, APIKeyCreateVerificationOn, APIKeyCreateVerificationOff}

type cvEnv struct {
	svc   *APIKeyVerificationService
	prefs *cvPrefs
	store *apiKeyVerifyStoreStub
	users *apiKeyVerifyUserRepoStub
	user  *User
	acct  cvAccount
}

func cvUser(acct cvAccount) *User {
	user := &User{ID: cvUserID, Email: "user@example.com", Status: StatusActive, SignupSource: acct.signup, PasswordHash: cvPasswordHash()}
	if acct.totp {
		secret := keyVerifyTOTPSecret
		user.TotpEnabled = true
		user.TotpSecretEncrypted = &secret
	}
	return user
}

// newCVEnv 组装一个只依赖假实现的校验服务，开关初始为 stored（"" / auto 都表示没有行）。
func newCVEnv(t *testing.T, acct cvAccount, stored string) *cvEnv {
	t.Helper()
	user := cvUser(acct)
	users := &apiKeyVerifyUserRepoStub{user: user}
	store := newAPIKeyVerifyStoreStub()
	totpSvc := NewTotpService(users, apiKeyVerifyEncryptorStub{}, store, nil, nil, nil)
	var passkeys *PasskeyService
	if acct.passkeys > 0 {
		var err error
		passkeys, err = NewPasskeyService(&config.Config{WebAuthn: config.WebAuthnConfig{
			Enabled:       true,
			RPDisplayName: "Sub2API",
			RPID:          "sub2api.example.com",
			RPOrigins:     []string{"https://sub2api.example.com"},
		}}, cvPasskeyRepo{count: acct.passkeys}, cvPasskeySessions{}, users)
		require.NoError(t, err)
	}
	svc := NewAPIKeyVerificationService(users, &apiKeyVerifyKeyRepoStub{key: &APIKey{ID: 42, UserID: cvUserID}},
		&apiKeyVerifySecretRepoStub{secret: apiKeyVerifyTestSecret("")}, totpSvc, passkeys, store)
	prefs := newCVPrefs(stored)
	svc.SetCreateVerificationPreferences(prefs)
	return &cvEnv{svc: svc, prefs: prefs, store: store, users: users, user: user, acct: acct}
}

// credential 返回对这个账号「正确」的校验材料（TOTP 码和 Passkey 票据每次都是新的、各只能用一次）。
func (e *cvEnv) credential(t *testing.T, method string) *APIKeyVerificationInput {
	t.Helper()
	switch method {
	case APIKeyFactorPassword:
		return &APIKeyVerificationInput{Method: method, Password: cvPassword}
	case APIKeyFactorTOTP:
		code, err := totp.GenerateCode(keyVerifyTOTPSecret, time.Now())
		require.NoError(t, err)
		return &APIKeyVerificationInput{Method: method, TOTPCode: code}
	case APIKeyFactorPasskey:
		token := fmt.Sprintf("cv-passkey-ticket-%d", time.Now().UnixNano())
		e.mintPasskeyTicket(t, token, cvUserID, time.Minute)
		return &APIKeyVerificationInput{Method: method, PasskeySessionToken: token}
	default:
		t.Fatalf("no credential for method %q", method)
		return nil
	}
}

func (e *cvEnv) mintPasskeyTicket(t *testing.T, token string, userID int64, ttl time.Duration) {
	t.Helper()
	require.NoError(t, e.store.SaveAPIKeyPasskeyTicket(context.Background(), apiKeyVerificationDigest(token), &APIKeyPasskeyVerificationRecord{
		UserID:    userID,
		Factor:    APIKeyFactorPasskey,
		ExpiresAt: time.Now().Add(ttl).Unix(),
	}, time.Minute))
}

// cvWrongCredential 返回一份「格式对但内容错」的校验材料，以及服务应该返回的错误。
func cvWrongCredential(method string) (*APIKeyVerificationInput, error) {
	switch method {
	case APIKeyFactorPassword:
		return &APIKeyVerificationInput{Method: method, Password: "wrong-password"}, ErrAPIKeyPasswordInvalid
	case APIKeyFactorTOTP:
		return &APIKeyVerificationInput{Method: method, TOTPCode: "000000"}, ErrAPIKeyTOTPInvalid
	default:
		return &APIKeyVerificationInput{Method: method, PasskeySessionToken: "cv-never-minted-ticket"}, ErrAPIKeyPasskeyFailed
	}
}

// requireRelaxRequired 断言「放宽但没带校验」：机器码是 VERIFY_REQUIRED，文案专门说关保护这一步。
func requireRelaxRequired(t *testing.T, err error) {
	t.Helper()
	require.Error(t, err)
	require.Equal(t, 400, infraerrors.Code(err))
	require.Equal(t, "VERIFY_REQUIRED", infraerrors.Reason(err))
	require.Contains(t, infraerrors.Message(err), "turn this protection off")
}

// ---- 1. 有效要求真值表：创建 / 单条轮换 / 批量轮换共用同一条判定 -------------

func TestCreateVerificationRequiredTruthTableAcrossAccountsAndModes(t *testing.T) {
	ctx := context.Background()
	for _, acct := range cvAccounts {
		for _, mode := range cvModes {
			t.Run(acct.name+"/"+mode, func(t *testing.T) {
				env := newCVEnv(t, acct, mode)
				want := acct.required[mode]

				factors, err := env.svc.Factors(ctx, cvUserID)
				require.NoError(t, err)
				require.Equal(t, want, factors.CreateVerificationRequired)
				require.Equal(t, mode, factors.CreateVerificationMode)
				require.Equal(t, acct.hasFactor(), factors.CreateVerificationConfigurable,
					"the switch is only meaningful when the account has something to verify with")
				require.Equal(t, acct.hasFactor(), factors.Available())

				// 创建 与 会回执明文的编辑（PUT /keys/:id；批量轮换走创建同一个入口）：同一条判定。
				for name, verify := range map[string]func() (*APIKeyVerificationResult, error){
					"create": func() (*APIKeyVerificationResult, error) { return env.svc.VerifyForCreate(ctx, cvUserID, nil) },
					"edit":   func() (*APIKeyVerificationResult, error) { return env.svc.VerifyForKeyEdit(ctx, cvUserID, 42, nil) },
				} {
					result, err := verify()
					if want {
						require.ErrorIs(t, err, ErrAPIKeyVerificationRequired, name)
						require.Nil(t, result, name)
						continue
					}
					require.NoError(t, err, name)
					require.Empty(t, result.Factor, name)
					require.Equal(t, mode == APIKeyCreateVerificationOff, result.SkippedByUserSetting,
						"%s: only an explicit off is reported as skipped by the user's own setting", name)
				}
			})
		}
	}
}

// 没有设置过开关的账号（没有行）与「根本没有这个功能」时完全一致：旧规则一个字都没变。
func TestCreateVerificationWithoutAnyRowKeepsTheLegacyRule(t *testing.T) {
	ctx := context.Background()
	legacy := map[string]bool{
		"password only":               false,
		"oauth without any factor":    false,
		"email with totp":             true,
		"email with passkey":          true,
		"email with totp and passkey": true,
		"oauth with totp only":        true,
		"oauth with passkey only":     true,
	}
	for _, acct := range cvAccounts {
		t.Run(acct.name, func(t *testing.T) {
			require.Contains(t, legacy, acct.name)
			withRepo := newCVEnv(t, acct, "")
			factors, err := withRepo.svc.Factors(ctx, cvUserID)
			require.NoError(t, err)
			require.Equal(t, legacy[acct.name], factors.CreateVerificationRequired)
			require.Equal(t, APIKeyCreateVerificationAuto, factors.CreateVerificationMode)

			// 没注入仓储（旧测试 / 嵌入方）：同样按 auto，而且不会碰任何存储。
			withRepo.svc.SetCreateVerificationPreferences(nil)
			factors, err = withRepo.svc.Factors(ctx, cvUserID)
			require.NoError(t, err)
			require.Equal(t, legacy[acct.name], factors.CreateVerificationRequired)
			require.Equal(t, APIKeyCreateVerificationAuto, factors.CreateVerificationMode)
		})
	}
}

// 请求里主动带了 verification.method 就按因子真验证：与模式无关（off 也不例外）。
// 对的通过并记因子，错的直接拒绝，账号没有的因子一律 VERIFY_FACTOR_UNAVAILABLE。
func TestCreateVerificationSuppliedCredentialsAreAlwaysReallyVerified(t *testing.T) {
	ctx := context.Background()
	for _, acct := range cvAccounts {
		for _, mode := range cvModes {
			t.Run(acct.name+"/"+mode, func(t *testing.T) {
				env := newCVEnv(t, acct, mode)
				if !acct.hasFactor() {
					// 一个可用因子都没有：任何因子都是 VERIFY_FACTOR_UNAVAILABLE，包括 off 时。
					for _, method := range []string{APIKeyFactorPassword, APIKeyFactorTOTP, APIKeyFactorPasskey} {
						_, err := env.svc.VerifyForCreate(ctx, cvUserID, &APIKeyVerificationInput{Method: method, Password: cvPassword, TOTPCode: "123456", PasskeySessionToken: "x"})
						require.ErrorIs(t, err, ErrAPIKeyFactorUnavailable, method)
					}
					return
				}
				wrong, wantErr := cvWrongCredential(acct.primary)
				_, err := env.svc.VerifyForCreate(ctx, cvUserID, wrong)
				require.ErrorIs(t, err, wantErr, "a wrong credential is rejected even when the switch is off")

				result, err := env.svc.VerifyForCreate(ctx, cvUserID, env.credential(t, acct.primary))
				require.NoError(t, err)
				require.Equal(t, acct.primary, result.Factor)
				require.False(t, result.SkippedByUserSetting, "a request that really verified is not a skipped one")
			})
		}
	}
}

// 纯函数：有效要求的唯一判定。
func TestAPIKeyCreateVerificationRequiredPureFunction(t *testing.T) {
	factors := func(password, totpOn bool, passkeys int) *APIKeyVerificationFactors {
		f := &APIKeyVerificationFactors{PasswordAvailable: password, TOTPEnabled: totpOn, PasskeyCount: passkeys}
		if passkeys > 0 {
			f.AvailableMethods = append(f.AvailableMethods, APIKeyFactorPasskey)
		}
		if totpOn {
			f.AvailableMethods = append(f.AvailableMethods, APIKeyFactorTOTP)
		}
		if password {
			f.AvailableMethods = append(f.AvailableMethods, APIKeyFactorPassword)
		}
		return f
	}
	cases := []struct {
		name string
		mode string
		f    *APIKeyVerificationFactors
		want bool
	}{
		{"on with password only", "on", factors(true, false, 0), true},
		{"on with totp only", "on", factors(false, true, 0), true},
		{"on with passkey only", "on", factors(false, false, 1), true},
		{"on with nothing cannot be satisfied", "on", factors(false, false, 0), false},
		{"off with everything", "off", factors(true, true, 3), false},
		{"auto with password only", "auto", factors(true, false, 0), false},
		{"auto with totp", "auto", factors(false, true, 0), true},
		{"auto with passkey", "auto", factors(true, false, 1), true},
		{"auto with nothing", "auto", factors(false, false, 0), false},
		{"unknown mode behaves as auto (2fa)", "banana", factors(true, true, 0), true},
		{"unknown mode behaves as auto (password only)", "banana", factors(true, false, 0), false},
		{"empty mode behaves as auto", "", factors(true, true, 0), true},
		{"nil factors never require", "on", nil, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			require.Equal(t, tc.want, apiKeyCreateVerificationRequired(tc.mode, tc.f))
		})
	}
}

// 库里出现不认识的值（不会发生：迁移里有 CHECK）时按 auto 处理，不让创建 Key 出错。
func TestCreateVerificationUnknownStoredValueIsTreatedAsAuto(t *testing.T) {
	ctx := context.Background()
	for _, acct := range cvAccounts {
		t.Run(acct.name, func(t *testing.T) {
			env := newCVEnv(t, acct, "")
			env.prefs.rows[cvUserID] = "banana"
			factors, err := env.svc.Factors(ctx, cvUserID)
			require.NoError(t, err)
			require.Equal(t, APIKeyCreateVerificationAuto, factors.CreateVerificationMode)
			require.Equal(t, acct.required[APIKeyCreateVerificationAuto], factors.CreateVerificationRequired)
		})
	}
}

func TestAPIKeyVerificationFactorsJSONExposesTheSwitchFields(t *testing.T) {
	env := newCVEnv(t, cvPasswordOnly, APIKeyCreateVerificationOn)
	factors, err := env.svc.Factors(context.Background(), cvUserID)
	require.NoError(t, err)
	raw, err := json.Marshal(factors)
	require.NoError(t, err)
	var decoded map[string]any
	require.NoError(t, json.Unmarshal(raw, &decoded))
	require.Equal(t, true, decoded["create_verification_required"])
	require.Equal(t, "on", decoded["create_verification_mode"])
	require.Equal(t, true, decoded["create_verification_configurable"])
	// 老字段原样保留（旧页面只认它们）。
	for _, key := range []string{"password_available", "totp_enabled", "passkey_count", "preferred", "available_methods", "seal_available"} {
		require.Contains(t, decoded, key)
	}
}

// ---- 2. 改开关本身的鉴权：只在「变宽松」时要校验 ---------------------------

func TestSetCreateVerificationModeMatrix(t *testing.T) {
	ctx := context.Background()
	for _, acct := range cvAccounts {
		if !acct.hasFactor() {
			continue // 没有任何因子的账号单独测
		}
		for _, from := range cvModes {
			for _, to := range cvModes {
				for _, attempt := range []string{"no verification", "wrong verification", "correct verification"} {
					t.Run(fmt.Sprintf("%s/%s->%s/%s", acct.name, from, to, attempt), func(t *testing.T) {
						env := newCVEnv(t, acct, from)
						relaxed := acct.required[from] && !acct.required[to]

						var (
							in      *APIKeyVerificationInput
							wantErr error
						)
						switch attempt {
						case "wrong verification":
							in, wantErr = cvWrongCredential(acct.primary)
						case "correct verification":
							in = env.credential(t, acct.primary)
						}

						change, err := env.svc.SetCreateVerificationMode(ctx, cvUserID, to, in)

						switch {
						case wantErr != nil:
							require.ErrorIs(t, err, wantErr)
						case attempt == "no verification" && relaxed:
							requireRelaxRequired(t, err)
						default:
							require.NoError(t, err)
						}
						if err != nil {
							// fail-closed：任何失败都不写库。
							require.Nil(t, change)
							require.Equal(t, from, env.prefs.label(cvUserID))
							require.Empty(t, env.prefs.swapLog())
							return
						}
						require.Equal(t, from, change.From)
						require.Equal(t, to, change.To)
						require.Equal(t, relaxed, change.Relaxed)
						require.Equal(t, from != to, change.Changed)
						wantFactor := ""
						if attempt == "correct verification" {
							wantFactor = acct.primary
						}
						require.Equal(t, wantFactor, change.Factor)
						require.Equal(t, to, env.prefs.label(cvUserID))
						if from == to {
							require.Empty(t, env.prefs.swapLog(), "an unchanged setting must not be written")
						} else {
							require.Len(t, env.prefs.swapLog(), 1)
						}
					})
				}
			}
		}
	}
}

// 比较的是「有效要求」而不是标签：两个典型例子。
func TestSetCreateVerificationModeComparesEffectiveRequirementNotLabels(t *testing.T) {
	ctx := context.Background()

	// 仅有密码的账号：auto → off 不是放宽（两边都不强制），不需要校验；
	// 但 on → auto 是放宽（输密码的要求没了），必须校验。
	passwordOnly := cvPasswordOnly
	env := newCVEnv(t, passwordOnly, APIKeyCreateVerificationAuto)
	change, err := env.svc.SetCreateVerificationMode(ctx, cvUserID, APIKeyCreateVerificationOff, nil)
	require.NoError(t, err)
	require.False(t, change.Relaxed)
	require.Equal(t, APIKeyCreateVerificationOff, env.prefs.label(cvUserID))

	env = newCVEnv(t, passwordOnly, APIKeyCreateVerificationOn)
	_, err = env.svc.SetCreateVerificationMode(ctx, cvUserID, APIKeyCreateVerificationAuto, nil)
	requireRelaxRequired(t, err)
	require.Equal(t, APIKeyCreateVerificationOn, env.prefs.label(cvUserID))

	// 开了 2FA 的账号：auto 本来就是强制的，所以 auto → off 是放宽；off → auto 是变严，不需要校验。
	withTOTP := cvWithTOTP
	env = newCVEnv(t, withTOTP, APIKeyCreateVerificationAuto)
	_, err = env.svc.SetCreateVerificationMode(ctx, cvUserID, APIKeyCreateVerificationOff, nil)
	requireRelaxRequired(t, err)
	env = newCVEnv(t, withTOTP, APIKeyCreateVerificationOff)
	change, err = env.svc.SetCreateVerificationMode(ctx, cvUserID, APIKeyCreateVerificationAuto, nil)
	require.NoError(t, err)
	require.False(t, change.Relaxed)
	require.Equal(t, APIKeyCreateVerificationAuto, env.prefs.label(cvUserID))
	require.Empty(t, env.prefs.rows, "back to auto deletes the row")
}

// 没有任何可用因子的账号：on / off 都没有意义，不落库；auto 只用来清掉残留的旧行。
func TestSetCreateVerificationModeForAccountWithoutAnyFactor(t *testing.T) {
	ctx := context.Background()
	acct := cvNoFactor
	require.False(t, acct.hasFactor())

	for _, stored := range cvModes {
		for _, to := range []string{APIKeyCreateVerificationOn, APIKeyCreateVerificationOff} {
			t.Run(stored+"->"+to, func(t *testing.T) {
				env := newCVEnv(t, acct, stored)
				change, err := env.svc.SetCreateVerificationMode(ctx, cvUserID, to, nil)
				require.ErrorIs(t, err, ErrAPIKeyCreateVerificationNotConfigurable)
				require.Equal(t, 409, infraerrors.Code(err))
				require.Nil(t, change)
				require.Equal(t, stored, env.prefs.label(cvUserID))
				require.Empty(t, env.prefs.swapLog())
			})
		}
		t.Run(stored+"->auto", func(t *testing.T) {
			env := newCVEnv(t, acct, stored)
			change, err := env.svc.SetCreateVerificationMode(ctx, cvUserID, APIKeyCreateVerificationAuto, nil)
			require.NoError(t, err)
			require.False(t, change.Relaxed, "with nothing to verify with there is nothing to relax")
			require.Equal(t, APIKeyCreateVerificationAuto, env.prefs.label(cvUserID))
			require.Empty(t, env.prefs.rows)
		})
	}

	// 残留的 on 不会把人锁死：创建照样放行。
	env := newCVEnv(t, acct, APIKeyCreateVerificationOn)
	result, err := env.svc.VerifyForCreate(ctx, cvUserID, nil)
	require.NoError(t, err)
	require.Empty(t, result.Factor)
	require.False(t, result.SkippedByUserSetting)

	// 主动带校验材料：账号没有这个因子，照旧 VERIFY_FACTOR_UNAVAILABLE（auto 的清理请求也一样）。
	_, err = env.svc.SetCreateVerificationMode(ctx, cvUserID, APIKeyCreateVerificationAuto,
		&APIKeyVerificationInput{Method: APIKeyFactorPassword, Password: cvPassword})
	require.ErrorIs(t, err, ErrAPIKeyFactorUnavailable)
}

func TestSetCreateVerificationModeNormalizesAndRejectsInvalidModes(t *testing.T) {
	ctx := context.Background()
	acct := cvPasswordOnly

	env := newCVEnv(t, acct, "")
	change, err := env.svc.SetCreateVerificationMode(ctx, cvUserID, "  ON ", nil)
	require.NoError(t, err)
	require.Equal(t, APIKeyCreateVerificationOn, change.To)
	require.Equal(t, APIKeyCreateVerificationOn, env.prefs.label(cvUserID))

	for _, bad := range []string{"", "   ", "maybe", "true", "1", "onn", "auto,on", "\x00"} {
		_, err := env.svc.SetCreateVerificationMode(ctx, cvUserID, bad, nil)
		require.ErrorIs(t, err, ErrAPIKeyCreateVerificationModeInvalid, "%q", bad)
		require.Equal(t, 400, infraerrors.Code(err))
		require.Equal(t, APIKeyCreateVerificationOn, env.prefs.label(cvUserID), "an invalid mode must not change anything")
	}
	_, ok := NormalizeAPIKeyCreateVerificationMode("Off")
	require.True(t, ok)
	_, ok = NormalizeAPIKeyCreateVerificationMode("nope")
	require.False(t, ok)
}

// ---- 3. 并发：读到旧值的请求不能把别人刚收紧的设置改回宽松 ----------------

// 确定性的交错：请求 X 读到 off（不要求校验）、目标 auto（对仅有密码的账号也不要求）→ 在它写入之前，
// 另一个请求把设置收紧成 on。如果 X 拿读到的旧值直接去写，on 就被悄悄改回 auto，等于没验证身份关掉了保护。
// 带期望值的写入会失败，X 重新读到 on，发现这次变成了放宽，于是 VERIFY_REQUIRED。
func TestSetCreateVerificationModeStaleReadCannotBypassTheRelaxRule(t *testing.T) {
	ctx := context.Background()
	env := newCVEnv(t, cvPasswordOnly, APIKeyCreateVerificationOff)
	env.prefs.afterGet = func(call int) {
		if call == 1 {
			env.prefs.set(cvUserID, APIKeyCreateVerificationOn) // 别的请求收紧了设置
		}
	}

	change, err := env.svc.SetCreateVerificationMode(ctx, cvUserID, APIKeyCreateVerificationAuto, nil)
	requireRelaxRequired(t, err)
	require.Nil(t, change)
	require.Equal(t, APIKeyCreateVerificationOn, env.prefs.label(cvUserID), "the tightened setting must survive")
	require.Empty(t, env.prefs.swapLog(), "the stale write must not have gone through")
	require.GreaterOrEqual(t, env.prefs.getCalls, 2, "after losing the race the request re-reads and re-judges")
}

// 同一个交错，但请求 X 带了正确校验：重新判断后允许写入，而且校验只消耗一次
// （同一个请求里已通过的校验继续有效，不会为了重试再吃第二个密码预算 / 动态码）。
func TestSetCreateVerificationModeVerifiedRequestRetriesWithoutVerifyingTwice(t *testing.T) {
	ctx := context.Background()
	env := newCVEnv(t, cvPasswordOnly, APIKeyCreateVerificationOff)
	budget := newPasswordBudgetStub(5)
	env.svc.SetPasswordAttemptLimiter(budget)
	env.prefs.afterGet = func(call int) {
		if call == 1 {
			env.prefs.set(cvUserID, APIKeyCreateVerificationOn)
		}
	}

	change, err := env.svc.SetCreateVerificationMode(ctx, cvUserID, APIKeyCreateVerificationAuto, env.credential(t, APIKeyFactorPassword))
	require.NoError(t, err)
	require.Equal(t, APIKeyCreateVerificationOn, change.From, "the verdict is made against the value that is really stored")
	require.True(t, change.Relaxed)
	require.True(t, change.Changed)
	require.Equal(t, APIKeyCreateVerificationAuto, env.prefs.label(cvUserID))
	require.Equal(t, 1, budget.checks, "one request, one password check")
}

// 一直被抢先：重试次数用尽返回 409，且自己一次都没写成功。
func TestSetCreateVerificationModeGivesUpWithConflictWhenAlwaysRaced(t *testing.T) {
	ctx := context.Background()
	env := newCVEnv(t, cvPasswordOnly, APIKeyCreateVerificationOn)
	env.prefs.afterGet = func(int) {
		// 每次读完都有人把设置翻到另一个值。
		if env.prefs.label(cvUserID) == APIKeyCreateVerificationOn {
			env.prefs.set(cvUserID, APIKeyCreateVerificationOff)
		} else {
			env.prefs.set(cvUserID, APIKeyCreateVerificationOn)
		}
	}
	change, err := env.svc.SetCreateVerificationMode(ctx, cvUserID, APIKeyCreateVerificationAuto, env.credential(t, APIKeyFactorPassword))
	require.ErrorIs(t, err, ErrAPIKeyCreateVerificationConflict)
	require.Equal(t, 409, infraerrors.Code(err))
	require.Nil(t, change)
	require.Empty(t, env.prefs.swapLog())
	require.Equal(t, apiKeyCreateVerificationCASAttempts, env.prefs.getCalls, "bounded retries")
}

// 并发压力（配合 -race）：一个合法用户反复「收紧 → 验证后放宽」，几个没有凭据的请求者反复尝试放宽。
// 不变量：没有凭据的请求者的任何一次成功写入，都不能让有效要求从必须校验变成不要求。
func TestSetCreateVerificationModeConcurrentWritersNeverRelaxWithoutVerification(t *testing.T) {
	ctx := context.Background()
	acct := cvPasswordOnly // 仅有密码：on 必须校验，auto / off 不要求
	env := newCVEnv(t, acct, APIKeyCreateVerificationOff)
	env.prefs.afterGet = func(int) { runtime.Gosched() } // 扩大「读 → 写」之间的窗口

	newWriter := func(name string) *APIKeyVerificationService {
		svc := NewAPIKeyVerificationService(env.users, nil, nil, nil, nil, env.store)
		svc.SetCreateVerificationPreferences(cvTaggedPrefs{cvPrefs: env.prefs, writer: name})
		return svc
	}

	const (
		legitRounds    = 12
		attackerRounds = 120
		attackers      = 3
	)
	var (
		wg           sync.WaitGroup
		mu           sync.Mutex
		attackerHits []string
	)
	wg.Add(1)
	go func() {
		defer wg.Done()
		svc := newWriter("legit")
		for i := 0; i < legitRounds; i++ {
			// 收紧不需要校验；放宽带登录密码。偶尔被抢先（409）就再来一轮。
			_, _ = svc.SetCreateVerificationMode(ctx, cvUserID, APIKeyCreateVerificationOn, nil)
			_, _ = svc.SetCreateVerificationMode(ctx, cvUserID, APIKeyCreateVerificationOff, &APIKeyVerificationInput{Method: APIKeyFactorPassword, Password: cvPassword})
		}
	}()
	for a := 0; a < attackers; a++ {
		wg.Add(1)
		go func(a int) {
			defer wg.Done()
			svc := newWriter(fmt.Sprintf("attacker-%d", a))
			for i := 0; i < attackerRounds; i++ {
				target := APIKeyCreateVerificationAuto
				if i%2 == 0 {
					target = APIKeyCreateVerificationOff
				}
				change, err := svc.SetCreateVerificationMode(ctx, cvUserID, target, nil)
				if err == nil && change.Relaxed {
					mu.Lock()
					attackerHits = append(attackerHits, fmt.Sprintf("attacker-%d reported a relaxation: %+v", a, change))
					mu.Unlock()
				}
			}
		}(a)
	}
	wg.Wait()

	require.Empty(t, attackerHits)
	for _, swap := range env.prefs.swapLog() {
		if !strings.HasPrefix(swap.Writer, "attacker") {
			continue
		}
		from, to := apiKeyCreateVerificationModeLabel(swap.From), apiKeyCreateVerificationModeLabel(swap.To)
		require.False(t, acct.required[from] && !acct.required[to],
			"%s moved %s -> %s without verification", swap.Writer, from, to)
	}
}

// ---- 4. 失败预算 / 一次性凭据：与创建 Key 是同一套 -------------------------

func TestSetCreateVerificationModeSharesThePasswordBudgetWithEveryOtherEntry(t *testing.T) {
	ctx := context.Background()
	env := newCVEnv(t, cvPasswordOnly, APIKeyCreateVerificationOn)
	budget := newPasswordBudgetStub(5)
	env.svc.SetPasswordAttemptLimiter(budget)
	wrong := &APIKeyVerificationInput{Method: APIKeyFactorPassword, Password: "wrong-password"}
	right := &APIKeyVerificationInput{Method: APIKeyFactorPassword, Password: cvPassword}

	// 创建 ×2、封存 ×1、改开关 ×2：五次失败来自三个不同的入口，共用一份预算。
	for i := 0; i < 2; i++ {
		_, err := env.svc.VerifyForCreate(ctx, cvUserID, wrong)
		require.ErrorIs(t, err, ErrAPIKeyPasswordInvalid)
	}
	require.ErrorIs(t, env.svc.VerifyForSeal(ctx, cvUserID, "wrong-password"), ErrAPIKeyPasswordInvalid)
	for i := 0; i < 2; i++ {
		_, err := env.svc.SetCreateVerificationMode(ctx, cvUserID, APIKeyCreateVerificationOff, wrong)
		require.ErrorIs(t, err, ErrAPIKeyPasswordInvalid)
	}
	require.Equal(t, map[int64]int{cvUserID: 5}, budget.used)

	// 预算用尽：改开关即使带了正确的密码也只拿到 429（先限流再 bcrypt），带 retry_after，且不写库。
	_, err := env.svc.SetCreateVerificationMode(ctx, cvUserID, APIKeyCreateVerificationOff, right)
	require.ErrorIs(t, err, ErrAPIKeyPasswordRateLimited)
	require.Equal(t, 429, infraerrors.Code(err))
	require.Equal(t, 42, RetryAfterSecondsFromError(err))
	require.Equal(t, APIKeyCreateVerificationOn, env.prefs.label(cvUserID))
	require.Empty(t, env.prefs.swapLog())
	// 反过来也一样：改开关失败吃掉的预算，创建 Key 那边同样看得到。
	_, err = env.svc.VerifyForCreate(ctx, cvUserID, right)
	require.ErrorIs(t, err, ErrAPIKeyPasswordRateLimited)
}

func TestSetCreateVerificationModeClearsThePasswordBudgetOnSuccess(t *testing.T) {
	ctx := context.Background()
	env := newCVEnv(t, cvPasswordOnly, APIKeyCreateVerificationOn)
	budget := newPasswordBudgetStub(5)
	env.svc.SetPasswordAttemptLimiter(budget)

	for i := 0; i < 3; i++ {
		_, err := env.svc.SetCreateVerificationMode(ctx, cvUserID, APIKeyCreateVerificationOff, &APIKeyVerificationInput{Method: APIKeyFactorPassword, Password: "wrong-password"})
		require.ErrorIs(t, err, ErrAPIKeyPasswordInvalid)
	}
	_, err := env.svc.SetCreateVerificationMode(ctx, cvUserID, APIKeyCreateVerificationOff, env.credential(t, APIKeyFactorPassword))
	require.NoError(t, err)
	require.Equal(t, []int64{cvUserID}, budget.resets)
	require.Empty(t, budget.used)
}

func TestSetCreateVerificationModeFailsClosedWhenThePasswordBudgetStoreIsDown(t *testing.T) {
	ctx := context.Background()
	env := newCVEnv(t, cvPasswordOnly, APIKeyCreateVerificationOn)
	budget := newPasswordBudgetStub(5)
	budget.err = fmt.Errorf("redis down")
	env.svc.SetPasswordAttemptLimiter(budget)

	_, err := env.svc.SetCreateVerificationMode(ctx, cvUserID, APIKeyCreateVerificationOff, env.credential(t, APIKeyFactorPassword))
	require.ErrorIs(t, err, ErrAPIKeyVerificationUnavailable)
	require.Equal(t, APIKeyCreateVerificationOn, env.prefs.label(cvUserID))
	require.Empty(t, env.prefs.swapLog())
}

// 动态码一个只能用一次（与登录共用重放标记）；Passkey 票据一次性、绑用户、会过期。
func TestSetCreateVerificationModeTOTPCodeAndPasskeyTicketAreSingleUse(t *testing.T) {
	ctx := context.Background()

	totpEnv := newCVEnv(t, cvWithTOTP, APIKeyCreateVerificationOn)
	code := totpEnv.credential(t, APIKeyFactorTOTP)
	_, err := totpEnv.svc.SetCreateVerificationMode(ctx, cvUserID, APIKeyCreateVerificationOff, code)
	require.NoError(t, err)
	// 同一个码：再改一次（先收紧回 on）、或者拿去创建 Key，都被拒绝。
	totpEnv.prefs.set(cvUserID, APIKeyCreateVerificationOn)
	_, err = totpEnv.svc.SetCreateVerificationMode(ctx, cvUserID, APIKeyCreateVerificationOff, code)
	require.ErrorIs(t, err, ErrAPIKeyTOTPInvalid)
	_, err = totpEnv.svc.VerifyForCreate(ctx, cvUserID, code)
	require.ErrorIs(t, err, ErrAPIKeyTOTPInvalid)
	require.Equal(t, APIKeyCreateVerificationOn, totpEnv.prefs.label(cvUserID))

	passkeyEnv := newCVEnv(t, cvWithPasskey, APIKeyCreateVerificationOn)
	ticket := passkeyEnv.credential(t, APIKeyFactorPasskey)
	_, err = passkeyEnv.svc.SetCreateVerificationMode(ctx, cvUserID, APIKeyCreateVerificationOff, ticket)
	require.NoError(t, err)
	passkeyEnv.prefs.set(cvUserID, APIKeyCreateVerificationOn)
	_, err = passkeyEnv.svc.SetCreateVerificationMode(ctx, cvUserID, APIKeyCreateVerificationOff, ticket)
	require.ErrorIs(t, err, ErrAPIKeyPasskeyFailed, "a passkey ticket is one-shot")

	passkeyEnv.mintPasskeyTicket(t, "ticket-of-someone-else", cvUserID+1, time.Minute)
	_, err = passkeyEnv.svc.SetCreateVerificationMode(ctx, cvUserID, APIKeyCreateVerificationOff,
		&APIKeyVerificationInput{Method: APIKeyFactorPasskey, PasskeySessionToken: "ticket-of-someone-else"})
	require.ErrorIs(t, err, ErrAPIKeyPasskeyFailed, "a ticket is bound to its owner")
	passkeyEnv.mintPasskeyTicket(t, "expired-ticket", cvUserID, -time.Second)
	_, err = passkeyEnv.svc.SetCreateVerificationMode(ctx, cvUserID, APIKeyCreateVerificationOff,
		&APIKeyVerificationInput{Method: APIKeyFactorPasskey, PasskeySessionToken: "expired-ticket"})
	require.ErrorIs(t, err, ErrAPIKeyPasskeyFailed)
	require.Equal(t, APIKeyCreateVerificationOn, passkeyEnv.prefs.label(cvUserID))
}

// ---- 5. 存储故障一律 fail-closed；二次查看 / 封存不依赖这张表 -----------------

func TestCreateVerificationPreferenceStoreFailuresFailClosed(t *testing.T) {
	ctx := context.Background()
	acct := cvPasswordOnly

	// 读失败：创建（不带校验）不能悄悄当成 auto 放行；Factors 同样报错。
	env := newCVEnv(t, acct, APIKeyCreateVerificationOn)
	env.prefs.getErr = fmt.Errorf("db down")
	result, err := env.svc.VerifyForCreate(ctx, cvUserID, nil)
	require.Error(t, err)
	require.Nil(t, result)
	_, err = env.svc.Factors(ctx, cvUserID)
	require.Error(t, err)
	// 带了校验材料的创建不需要读开关：照常验证通过（数据库抖动不会让带密码的请求平白失败）。
	result, err = env.svc.VerifyForCreate(ctx, cvUserID, env.credential(t, APIKeyFactorPassword))
	require.NoError(t, err)
	require.Equal(t, APIKeyFactorPassword, result.Factor)

	// 改开关：读失败 / 写失败都不能算成功，也不能留下半个状态。
	_, err = env.svc.SetCreateVerificationMode(ctx, cvUserID, APIKeyCreateVerificationOff, env.credential(t, APIKeyFactorPassword))
	require.Error(t, err)
	env.prefs.getErr = nil
	env.prefs.casErr = fmt.Errorf("db down")
	_, err = env.svc.SetCreateVerificationMode(ctx, cvUserID, APIKeyCreateVerificationOff, env.credential(t, APIKeyFactorPassword))
	require.Error(t, err)
	require.Equal(t, APIKeyCreateVerificationOn, env.prefs.label(cvUserID))
	require.Empty(t, env.prefs.swapLog())
}

// 二次查看授权、封存校验只关心「哪些因子可用」，偏好表出问题时它们不受影响（本任务不动这两条）。
func TestRevealAndSealDoNotDependOnThePreferenceStore(t *testing.T) {
	ctx := context.Background()
	env := newCVEnv(t, cvPasswordOnly, APIKeyCreateVerificationOn)
	env.prefs.getErr = fmt.Errorf("db down")

	grant, err := env.svc.AuthorizeReveal(ctx, cvUserID, 42, *env.credential(t, APIKeyFactorPassword))
	require.NoError(t, err)
	require.NotEmpty(t, grant.Token)
	require.NoError(t, env.svc.VerifyForSeal(ctx, cvUserID, cvPassword))
	// 旧客户端的封存兼容分支只看「有没有可用因子」：有因子就要求校验，与开关无关。
	require.ErrorIs(t, env.svc.VerifyForLegacySeal(ctx, cvUserID), ErrAPIKeyVerificationRequired)
	noFactor := newCVEnv(t, cvNoFactor, APIKeyCreateVerificationOff)
	noFactor.prefs.getErr = fmt.Errorf("db down")
	require.NoError(t, noFactor.svc.VerifyForLegacySeal(ctx, cvUserID))
	require.Zero(t, env.prefs.getCalls, "reveal and seal never read the switch")
	require.Zero(t, noFactor.prefs.getCalls, "reveal and seal never read the switch")
}

func TestSetCreateVerificationModeWithoutPreferenceRepositoryIsUnavailable(t *testing.T) {
	ctx := context.Background()
	env := newCVEnv(t, cvPasswordOnly, "")
	env.svc.SetCreateVerificationPreferences(nil)
	_, err := env.svc.SetCreateVerificationMode(ctx, cvUserID, APIKeyCreateVerificationOn, nil)
	require.ErrorIs(t, err, ErrAPIKeyVerificationUnavailable)

	var nilSvc *APIKeyVerificationService
	_, err = nilSvc.SetCreateVerificationMode(ctx, cvUserID, APIKeyCreateVerificationOn, nil)
	require.ErrorIs(t, err, ErrAPIKeyVerificationUnavailable)
	nilSvc.SetCreateVerificationPreferences(newCVPrefs("")) // 不 panic

	env = newCVEnv(t, cvPasswordOnly, "")
	_, err = env.svc.SetCreateVerificationMode(ctx, 0, APIKeyCreateVerificationOn, nil)
	require.ErrorIs(t, err, ErrAPIKeyVerificationUnavailable)
}
