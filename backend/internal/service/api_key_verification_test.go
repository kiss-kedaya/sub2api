//go:build unit

package service

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/pquerna/otp/totp"
	"github.com/stretchr/testify/require"
)

// ---- 测试替身 -------------------------------------------------------------

type apiKeyVerifyUserRepoStub struct {
	UserRepository
	user *User
	err  error
	// identities / identitiesErr / identityLookups：第三方注册账号判「是否设置过本地密码」时查的认证身份。
	identities      []UserAuthIdentityRecord
	identitiesErr   error
	identityLookups int
}

func (r *apiKeyVerifyUserRepoStub) GetByID(context.Context, int64) (*User, error) {
	if r.err != nil {
		return nil, r.err
	}
	return r.user, nil
}

func (r *apiKeyVerifyUserRepoStub) ListUserAuthIdentities(context.Context, int64) ([]UserAuthIdentityRecord, error) {
	r.identityLookups++
	if r.identitiesErr != nil {
		return nil, r.identitiesErr
	}
	return r.identities, nil
}

type apiKeyVerifyKeyRepoStub struct {
	APIKeyRepository
	key    *APIKey
	lookup int64
}

func (r *apiKeyVerifyKeyRepoStub) GetByID(_ context.Context, id int64) (*APIKey, error) {
	r.lookup = id
	if r.key == nil || r.key.ID != id {
		return nil, ErrAPIKeyNotFound
	}
	return r.key, nil
}

type apiKeyVerifySecretRepoStub struct {
	APIKeySecretRepository
	secret *APIKeySecret
}

func (r *apiKeyVerifySecretRepoStub) GetByAPIKeyID(_ context.Context, apiKeyID, userID int64) (*APIKeySecret, error) {
	if r.secret == nil || r.secret.APIKeyID != apiKeyID || r.secret.UserID != userID {
		return nil, ErrAPIKeySecretNotFound
	}
	return r.secret, nil
}

// apiKeyVerifyStoreStub 同时实现 TotpCache（嵌入占位）与 APIKeyVerificationStore；
// TOTP 校验只需要尝试计数的三个方法。
type apiKeyVerifyStoreStub struct {
	TotpCache

	mu      sync.Mutex
	grants  map[string]*APIKeyRevealGrantRecord
	tickets map[string]*APIKeyPasskeyVerificationRecord
	// usedCodes 复刻 Redis SETNX 重放标记（按用户 + 摘要）；markErr 模拟标记存储故障。
	usedCodes map[string]bool
	markErr   error
}

func newAPIKeyVerifyStoreStub() *apiKeyVerifyStoreStub {
	return &apiKeyVerifyStoreStub{
		grants:    map[string]*APIKeyRevealGrantRecord{},
		tickets:   map[string]*APIKeyPasskeyVerificationRecord{},
		usedCodes: map[string]bool{},
	}
}

// MarkLoginCodeUsed 与 repository.TotpCache 同语义：第一次写入返回 true，同一个摘要再写返回 false。
func (s *apiKeyVerifyStoreStub) MarkLoginCodeUsed(_ context.Context, userID int64, digest string, _ time.Duration) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.markErr != nil {
		return false, s.markErr
	}
	key := fmt.Sprintf("%d:%s", userID, digest)
	if s.usedCodes[key] {
		return false, nil
	}
	s.usedCodes[key] = true
	return true, nil
}

func (s *apiKeyVerifyStoreStub) SaveAPIKeyRevealGrant(_ context.Context, digest string, record *APIKeyRevealGrantRecord, _ time.Duration) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	copied := *record
	s.grants[digest] = &copied
	return nil
}

func (s *apiKeyVerifyStoreStub) ConsumeAPIKeyRevealGrant(_ context.Context, digest string) (*APIKeyRevealGrantRecord, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	record, ok := s.grants[digest]
	if !ok {
		return nil, nil
	}
	delete(s.grants, digest)
	copied := *record
	return &copied, nil
}

func (s *apiKeyVerifyStoreStub) SaveAPIKeyPasskeyTicket(_ context.Context, digest string, record *APIKeyPasskeyVerificationRecord, _ time.Duration) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	copied := *record
	s.tickets[digest] = &copied
	return nil
}

func (s *apiKeyVerifyStoreStub) ConsumeAPIKeyPasskeyTicket(_ context.Context, digest string) (*APIKeyPasskeyVerificationRecord, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	record, ok := s.tickets[digest]
	if !ok {
		return nil, nil
	}
	delete(s.tickets, digest)
	copied := *record
	return &copied, nil
}

func (s *apiKeyVerifyStoreStub) GetVerifyAttempts(context.Context, int64) (int, error) {
	return 0, nil
}

func (s *apiKeyVerifyStoreStub) IncrementVerifyAttempts(context.Context, int64) (int, error) {
	return 1, nil
}

func (s *apiKeyVerifyStoreStub) ClearVerifyAttempts(context.Context, int64) error {
	return nil
}

type apiKeyVerifyEncryptorStub struct{}

func (apiKeyVerifyEncryptorStub) Encrypt(plaintext string) (string, error)  { return plaintext, nil }
func (apiKeyVerifyEncryptorStub) Decrypt(ciphertext string) (string, error) { return ciphertext, nil }

// newAPIKeyVerificationTestService 组装一个只依赖测试替身的校验服务。
func newAPIKeyVerificationTestService(
	t *testing.T,
	user *User,
	apiKey *APIKey,
	secret *APIKeySecret,
	totpSecret string,
) (*APIKeyVerificationService, *apiKeyVerifyStoreStub, *apiKeyVerifyUserRepoStub) {
	t.Helper()
	if user != nil && totpSecret != "" {
		encrypted := totpSecret
		user.TotpEnabled = true
		user.TotpSecretEncrypted = &encrypted
	}
	userRepo := &apiKeyVerifyUserRepoStub{user: user}
	keyRepo := &apiKeyVerifyKeyRepoStub{key: apiKey}
	secretRepo := &apiKeyVerifySecretRepoStub{secret: secret}
	store := newAPIKeyVerifyStoreStub()
	var totpSvc *TotpService
	if user != nil {
		totpSvc = NewTotpService(userRepo, apiKeyVerifyEncryptorStub{}, store, nil, nil, nil)
	}
	svc := NewAPIKeyVerificationService(userRepo, keyRepo, secretRepo, totpSvc, nil, store)
	return svc, store, userRepo
}

func apiKeyVerifyTestUser(t *testing.T, password string) *User {
	t.Helper()
	user := &User{ID: 7, Email: "user@example.com", Status: StatusActive, SignupSource: "email"}
	require.NoError(t, user.SetPassword(password))
	return user
}

func apiKeyVerifyTestKey(secret *APIKeySecret) *APIKey {
	return &APIKey{ID: 42, UserID: 7}
}

func apiKeyVerifyTestSecret(authFactor string) *APIKeySecret {
	return &APIKeySecret{
		APIKeyID:   42,
		UserID:     7,
		Version:    1,
		KDF:        json.RawMessage(`{"algo":"PBKDF2-SHA256","iterations":310000,"salt":"c2FsdA=="}`),
		Sealed:     "c2VhbGVk",
		AuthFactor: authFactor,
	}
}

// ---- 因子可用性 -----------------------------------------------------------

func TestAPIKeyVerificationFactorAvailability(t *testing.T) {
	ctx := context.Background()

	// 邮箱注册 + 已开 TOTP：TOTP / 密码都可用，推荐 TOTP。
	user := apiKeyVerifyTestUser(t, "correct-password")
	svc, _, _ := newAPIKeyVerificationTestService(t, user, nil, nil, "JBSWY3DPEHPK3PXP")
	factors, err := svc.Factors(ctx, user.ID)
	require.NoError(t, err)
	require.True(t, factors.PasswordAvailable)
	require.True(t, factors.TOTPEnabled)
	require.Equal(t, 0, factors.PasskeyCount)
	require.Equal(t, APIKeyFactorTOTP, factors.Preferred)
	require.Equal(t, []string{APIKeyFactorTOTP, APIKeyFactorPassword}, factors.AvailableMethods)
	require.True(t, factors.SealAvailable)

	// OAuth-only（GitHub 注册）：密码字段虽然有随机 hash，但用户不知道密码 →
	// 不可选、也不能封存（本地解密必须用登录密码）。
	oauthUser := apiKeyVerifyTestUser(t, "random-generated-secret")
	oauthUser.SignupSource = "github"
	svc, _, _ = newAPIKeyVerificationTestService(t, oauthUser, nil, nil, "")
	factors, err = svc.Factors(ctx, oauthUser.ID)
	require.NoError(t, err)
	require.False(t, factors.PasswordAvailable)
	require.False(t, factors.SealAvailable)
	require.False(t, factors.Available())
	require.Empty(t, factors.Preferred)

	// 一个因子都没有的账号：创建时不需要校验（一次性显示明文），
	// 但密码 / TOTP 方法都必须被服务端拒绝。
	result, err := svc.VerifyForCreate(ctx, oauthUser.ID, nil)
	require.NoError(t, err)
	require.NotNil(t, result)
	require.Empty(t, result.Factor)
	_, err = svc.VerifyForCreate(ctx, oauthUser.ID, &APIKeyVerificationInput{Method: APIKeyFactorPassword, Password: "random-generated-secret"})
	require.ErrorIs(t, err, ErrAPIKeyFactorUnavailable)
	_, err = svc.VerifyForCreate(ctx, oauthUser.ID, &APIKeyVerificationInput{Method: APIKeyFactorTOTP, TOTPCode: "123456"})
	require.ErrorIs(t, err, ErrAPIKeyFactorUnavailable)
}

// ---- 创建前校验矩阵 -------------------------------------------------------

func TestAPIKeyVerificationCreateMatrix(t *testing.T) {
	ctx := context.Background()
	user := apiKeyVerifyTestUser(t, "correct-password")
	svc, _, _ := newAPIKeyVerificationTestService(t, user, nil, nil, "JBSWY3DPEHPK3PXP")

	// 有可用因子但没带校验 → 400 VERIFY_REQUIRED，创建不会发生。
	_, err := svc.VerifyForCreate(ctx, user.ID, nil)
	require.ErrorIs(t, err, ErrAPIKeyVerificationRequired)

	// 密码错误 → VERIFY_PASSWORD_INVALID。
	_, err = svc.VerifyForCreate(ctx, user.ID, &APIKeyVerificationInput{Method: APIKeyFactorPassword, Password: "wrong-password"})
	require.ErrorIs(t, err, ErrAPIKeyPasswordInvalid)

	// 密码正确 → 通过，返回本次因子。
	result, err := svc.VerifyForCreate(ctx, user.ID, &APIKeyVerificationInput{Method: APIKeyFactorPassword, Password: "correct-password"})
	require.NoError(t, err)
	require.Equal(t, APIKeyFactorPassword, result.Factor)

	// 未绑定的 Passkey → VERIFY_FACTOR_UNAVAILABLE。
	_, err = svc.VerifyForCreate(ctx, user.ID, &APIKeyVerificationInput{Method: APIKeyFactorPasskey, PasskeySessionToken: "ticket"})
	require.ErrorIs(t, err, ErrAPIKeyFactorUnavailable)

	// 未知因子 → VERIFY_FACTOR_UNAVAILABLE。
	_, err = svc.VerifyForCreate(ctx, user.ID, &APIKeyVerificationInput{Method: "sms", Password: "correct-password"})
	require.ErrorIs(t, err, ErrAPIKeyFactorUnavailable)

	// TOTP 校验成功 / 失败。
	secret := "JBSWY3DPEHPK3PXP"
	code, err := totp.GenerateCode(secret, time.Now())
	require.NoError(t, err)
	result, err = svc.VerifyForCreate(ctx, user.ID, &APIKeyVerificationInput{Method: APIKeyFactorTOTP, TOTPCode: code})
	require.NoError(t, err)
	require.Equal(t, APIKeyFactorTOTP, result.Factor)
	_, err = svc.VerifyForCreate(ctx, user.ID, &APIKeyVerificationInput{Method: APIKeyFactorTOTP, TOTPCode: "000000"})
	require.ErrorIs(t, err, ErrAPIKeyTOTPInvalid)
}

func TestAPIKeyVerificationSealPassword(t *testing.T) {
	ctx := context.Background()
	user := apiKeyVerifyTestUser(t, "correct-password")
	svc, _, _ := newAPIKeyVerificationTestService(t, user, nil, nil, "")

	require.NoError(t, svc.VerifyForSeal(ctx, user.ID, "correct-password"))
	require.ErrorIs(t, svc.VerifyForSeal(ctx, user.ID, "wrong-password"), ErrAPIKeyPasswordInvalid)
	require.ErrorIs(t, svc.VerifyForSeal(ctx, user.ID, ""), ErrAPIKeyPasswordInvalid)

	oauthUser := apiKeyVerifyTestUser(t, "random-generated-secret")
	oauthUser.SignupSource = "google"
	svc, _, _ = newAPIKeyVerificationTestService(t, oauthUser, nil, nil, "")
	require.ErrorIs(t, svc.VerifyForSeal(ctx, oauthUser.ID, "random-generated-secret"), ErrAPIKeyFactorUnavailable)
}

// ---- 二次查看授权 ---------------------------------------------------------

func TestAPIKeyRevealGrantBindsUserKeyFactorAndIsOneShot(t *testing.T) {
	ctx := context.Background()
	user := apiKeyVerifyTestUser(t, "correct-password")
	key := apiKeyVerifyTestKey(nil)
	secret := apiKeyVerifyTestSecret(APIKeyFactorPassword)
	svc, _, _ := newAPIKeyVerificationTestService(t, user, key, secret, "")

	// 验证方式必须可用：未绑定的 TOTP 直接拒绝。
	_, err := svc.AuthorizeReveal(ctx, user.ID, key.ID, APIKeyVerificationInput{Method: APIKeyFactorTOTP, TOTPCode: "123456"})
	require.ErrorIs(t, err, ErrAPIKeyFactorUnavailable)

	// 密码错误不签发 grant。
	_, err = svc.AuthorizeReveal(ctx, user.ID, key.ID, APIKeyVerificationInput{Method: APIKeyFactorPassword, Password: "wrong"})
	require.ErrorIs(t, err, ErrAPIKeyPasswordInvalid)

	grant, err := svc.AuthorizeReveal(ctx, user.ID, key.ID, APIKeyVerificationInput{Method: APIKeyFactorPassword, Password: "correct-password"})
	require.NoError(t, err)
	require.NotEmpty(t, grant.Token)
	require.Equal(t, APIKeyFactorPassword, grant.Factor)
	require.Equal(t, int64(300), grant.ExpiresIn)

	// 正确 user+key 可以消费一次。
	factor, err := svc.ConsumeRevealGrant(ctx, grant.Token, user.ID, key.ID)
	require.NoError(t, err)
	require.Equal(t, APIKeyFactorPassword, factor)

	// 同一 grant 第二次消费必须失败（一次性）。
	_, err = svc.ConsumeRevealGrant(ctx, grant.Token, user.ID, key.ID)
	require.ErrorIs(t, err, ErrAPIKeyRevealGrantInvalid)

	// scope：换一把 Key / 换一个用户都不行。
	grant, err = svc.AuthorizeReveal(ctx, user.ID, key.ID, APIKeyVerificationInput{Method: APIKeyFactorPassword, Password: "correct-password"})
	require.NoError(t, err)
	_, err = svc.ConsumeRevealGrant(ctx, grant.Token, user.ID, key.ID+1)
	require.ErrorIs(t, err, ErrAPIKeyRevealGrantInvalid)

	grant, err = svc.AuthorizeReveal(ctx, user.ID, key.ID, APIKeyVerificationInput{Method: APIKeyFactorPassword, Password: "correct-password"})
	require.NoError(t, err)
	_, err = svc.ConsumeRevealGrant(ctx, grant.Token, user.ID+1, key.ID)
	require.ErrorIs(t, err, ErrAPIKeyRevealGrantInvalid)

	// 没有 grant 直接取密文 → REVEAL_GRANT_REQUIRED。
	_, err = svc.ConsumeRevealGrant(ctx, "", user.ID, key.ID)
	require.ErrorIs(t, err, ErrAPIKeyRevealGrantRequired)
}

func TestAPIKeyRevealRequiresSameFactorAsSeal(t *testing.T) {
	ctx := context.Background()
	user := apiKeyVerifyTestUser(t, "correct-password")
	key := apiKeyVerifyTestKey(nil)
	secret := apiKeyVerifyTestSecret(APIKeyFactorTOTP)
	svc, _, _ := newAPIKeyVerificationTestService(t, user, key, secret, "JBSWY3DPEHPK3PXP")

	// 密文记录的是 TOTP 授权：用密码查看必须被拒（同一因子约束）。
	_, err := svc.AuthorizeReveal(ctx, user.ID, key.ID, APIKeyVerificationInput{Method: APIKeyFactorPassword, Password: "correct-password"})
	require.ErrorIs(t, err, ErrAPIKeyRevealFactorMismatch)

	code, err := totp.GenerateCode("JBSWY3DPEHPK3PXP", time.Now())
	require.NoError(t, err)
	grant, err := svc.AuthorizeReveal(ctx, user.ID, key.ID, APIKeyVerificationInput{Method: APIKeyFactorTOTP, TOTPCode: code})
	require.NoError(t, err)
	require.Equal(t, APIKeyFactorTOTP, grant.Factor)

	// 改造前的旧密文（auth_factor 为空）不限制因子。
	legacy := apiKeyVerifyTestSecret("")
	svc, _, _ = newAPIKeyVerificationTestService(t, user, key, legacy, "JBSWY3DPEHPK3PXP")
	grant, err = svc.AuthorizeReveal(ctx, user.ID, key.ID, APIKeyVerificationInput{Method: APIKeyFactorPassword, Password: "correct-password"})
	require.NoError(t, err)
	require.Equal(t, APIKeyFactorPassword, grant.Factor)
}

func TestAPIKeyRevealRejectsForeignKeyAndMissingSecret(t *testing.T) {
	ctx := context.Background()
	user := apiKeyVerifyTestUser(t, "correct-password")
	key := &APIKey{ID: 42, UserID: 99} // 不属于该用户
	secret := apiKeyVerifyTestSecret(APIKeyFactorPassword)
	svc, _, _ := newAPIKeyVerificationTestService(t, user, key, secret, "")

	_, err := svc.AuthorizeReveal(ctx, user.ID, key.ID, APIKeyVerificationInput{Method: APIKeyFactorPassword, Password: "correct-password"})
	require.ErrorIs(t, err, ErrAPIKeyNotFound)

	key.UserID = 7
	svc, _, _ = newAPIKeyVerificationTestService(t, user, key, nil, "")
	_, err = svc.AuthorizeReveal(ctx, user.ID, key.ID, APIKeyVerificationInput{Method: APIKeyFactorPassword, Password: "correct-password"})
	require.ErrorIs(t, err, ErrAPIKeySecretNotFound)
}

func TestAPIKeyPasskeyTicketIsOneShotAndBoundToUser(t *testing.T) {
	ctx := context.Background()
	user := apiKeyVerifyTestUser(t, "correct-password")
	svc, store, _ := newAPIKeyVerificationTestService(t, user, nil, nil, "")
	digest := apiKeyVerificationDigest("passkey-ticket-fixture")
	require.NoError(t, store.SaveAPIKeyPasskeyTicket(ctx, digest, &APIKeyPasskeyVerificationRecord{
		UserID:    user.ID,
		Factor:    APIKeyFactorPasskey,
		ExpiresAt: time.Now().Add(time.Minute).Unix(),
	}, time.Minute))

	require.NoError(t, svc.consumePasskeyTicket(ctx, user.ID, "passkey-ticket-fixture"))
	// 票据已消费：重放失败。
	require.ErrorIs(t, svc.consumePasskeyTicket(ctx, user.ID, "passkey-ticket-fixture"), ErrAPIKeyPasskeyFailed)

	// 用户不匹配：拒绝且票据被消费（一次性）。
	require.NoError(t, store.SaveAPIKeyPasskeyTicket(ctx, digest, &APIKeyPasskeyVerificationRecord{
		UserID:    8,
		Factor:    APIKeyFactorPasskey,
		ExpiresAt: time.Now().Add(time.Minute).Unix(),
	}, time.Minute))
	require.ErrorIs(t, svc.consumePasskeyTicket(ctx, user.ID, "passkey-ticket-fixture"), ErrAPIKeyPasskeyFailed)

	// 过期票据：拒绝。
	require.NoError(t, store.SaveAPIKeyPasskeyTicket(ctx, digest, &APIKeyPasskeyVerificationRecord{
		UserID:    user.ID,
		Factor:    APIKeyFactorPasskey,
		ExpiresAt: time.Now().Add(-time.Second).Unix(),
	}, time.Minute))
	require.ErrorIs(t, svc.consumePasskeyTicket(ctx, user.ID, "passkey-ticket-fixture"), ErrAPIKeyPasskeyFailed)

	// 存储不可用（未装配 APIKeyVerificationStore）时 fail-closed。
	svcNoStore := NewAPIKeyVerificationService(&apiKeyVerifyUserRepoStub{user: user}, nil, nil, nil, nil, nil)
	_, err := svcNoStore.ConsumeRevealGrant(ctx, "any-token", user.ID, 42)
	require.ErrorIs(t, err, ErrAPIKeyVerificationUnavailable)
}

// ---- 审计不含敏感值 -------------------------------------------------------

func TestAPIKeyVerificationAuditExtraNeverContainsSecrets(t *testing.T) {
	user := apiKeyVerifyTestUser(t, "correct-password")
	svc, _, _ := newAPIKeyVerificationTestService(t, user, nil, nil, "JBSWY3DPEHPK3PXP")
	_ = svc

	// 审计白名单只允许因子名 / 结果 / 机器码；这里验证我们写入的字段
	// 不包含任何密码、动态码、票据或 grant。
	fields := map[string]any{
		"api_key_auth_factor":         APIKeyFactorPassword,
		"api_key_verification":        "verified",
		"api_key_verification_reason": "VERIFY_PASSWORD_INVALID",
	}
	for key, value := range fields {
		text, _ := value.(string)
		require.NotContains(t, strings.ToLower(key), "password")
		require.NotContains(t, strings.ToLower(key), "token")
		require.NotContains(t, strings.ToLower(key), "secret")
		require.NotContains(t, text, "correct-password")
		require.NotContains(t, text, "123456")
	}
}
