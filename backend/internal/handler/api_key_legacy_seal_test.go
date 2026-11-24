package handler

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/service"

	"github.com/stretchr/testify/require"
)

// 审计 A-P2-03：PUT secret-seal 的「旧客户端兼容」分支（既没带 password 也没带 auth_factor）原先
// 不需要任何校验：持会话者能覆盖已封存的密文，并把 auth_factor 清成 NULL（NULL 代表旧密文，
// 二次查看不再限制因子）。账号存在可用因子时它必须被拒；没有任何可用因子的账号保持放行。

const legacySealOriginalCiphertext = "b3JpZ2luYWwtc2VhbGVkLXNlY3JldC1maXh0dXJlLTAwMDAwMDA="

func sealedSecretFixture(authFactor string) *service.APIKeySecret {
	return &service.APIKeySecret{
		APIKeyID:   1,
		UserID:     7,
		Version:    1,
		KDF:        json.RawMessage(`{"algo":"PBKDF2-SHA256","iterations":310000,"salt":"MDEyMzQ1Njc4OWFiY2RlZg=="}`),
		Sealed:     legacySealOriginalCiphertext,
		AuthFactor: authFactor,
	}
}

func TestLegacySealCannotOverwriteASealedSecretWhenTheAccountHasFactors(t *testing.T) {
	totpSecret := "JBSWY3DPEHPK3PXP"
	accounts := []struct {
		name string
		user func(t *testing.T) *service.User
	}{
		{"login password factor (email signup)", handlerVerificationUser},
		{"TOTP only (third-party signup)", func(t *testing.T) *service.User {
			user := handlerVerificationUser(t)
			user.SignupSource = "github"
			user.TotpEnabled = true
			user.TotpSecretEncrypted = &totpSecret
			return user
		}},
	}
	for _, account := range accounts {
		t.Run(account.name, func(t *testing.T) {
			engine := newHandlerVerificationEngine(t, account.user(t))
			engine.secretStore.rows[1] = sealedSecretFixture(service.APIKeyFactorTOTP)

			// 旧客户端形态：既没有 password 也没有 auth_factor。
			rec := doJSON(engine.router, http.MethodPut, "/api/v1/user/api-keys/1/secret-seal",
				handlerVerificationSealBody(t, "", ""), nil)
			require.Equal(t, http.StatusBadRequest, rec.Code, rec.Body.String())
			require.Equal(t, "VERIFY_REQUIRED", handlerVerificationErrorReason(t, rec))

			// 已封存的密文与授权因子原样保留：没被覆盖，auth_factor 也没被清成 NULL。
			require.Equal(t, legacySealOriginalCiphertext, engine.secretStore.rows[1].Sealed)
			require.Equal(t, service.APIKeyFactorTOTP, engine.secretStore.rows[1].AuthFactor)
		})
	}
}

func TestLegacySealForANewKeyIsAlsoRejectedWhenTheAccountHasFactors(t *testing.T) {
	engine := newHandlerVerificationEngine(t, handlerVerificationUser(t))

	rec := doJSON(engine.router, http.MethodPut, "/api/v1/user/api-keys/1/secret-seal",
		handlerVerificationSealBody(t, "", ""), nil)
	require.Equal(t, "VERIFY_REQUIRED", handlerVerificationErrorReason(t, rec))
	require.NotContains(t, engine.secretStore.rows, int64(1), "a rejected legacy seal must not leave a row behind")

	// 新客户端（带登录密码 + 授权因子）照常封存，并记录因子。
	rec = doJSON(engine.router, http.MethodPut, "/api/v1/user/api-keys/1/secret-seal",
		handlerVerificationSealBody(t, "correct-password", "password"), nil)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	require.Equal(t, service.APIKeyFactorPassword, engine.secretStore.rows[1].AuthFactor)
}

func TestLegacySealStaysAllowedForAnAccountWithoutAnyFactor(t *testing.T) {
	user := handlerVerificationUser(t)
	user.SignupSource = "github" // 第三方注册、没设过本地密码、没开 2FA：没有任何可用因子
	engine := newHandlerVerificationEngine(t, user)

	rec := doJSON(engine.router, http.MethodPut, "/api/v1/user/api-keys/1/secret-seal",
		handlerVerificationSealBody(t, "", ""), nil)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	require.Contains(t, engine.secretStore.rows, int64(1))
	require.Empty(t, engine.secretStore.rows[1].AuthFactor, "legacy seal records no factor")
}
