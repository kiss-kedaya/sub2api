package routes

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/handler"
	"github.com/Wei-Shaw/sub2api/internal/repository"
	"github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/pquerna/otp/totp"
	"github.com/stretchr/testify/require"
)

type serverAuthorizationUserRepo struct {
	cryptoRouteUserRepo
	beforeDisable func()
}

func TestUserTotpDisableHTTPRequiresCurrentAuthenticator(t *testing.T) {
	for _, attempt := range []struct {
		name   string
		body   string
		status int
	}{
		{name: "password only", body: `{"password":"known-test-password"}`, status: http.StatusBadRequest},
		{name: "email only", body: `{"email_code":"123456"}`, status: http.StatusBadRequest},
		{name: "client approval", body: `{"enabled":false,"verified":true}`, status: http.StatusBadRequest},
		{name: "server recovery forbidden", body: `{"server_authorization_code":"` + strings.Repeat("a", 64) + `"}`, status: http.StatusForbidden},
		{name: "current authenticator", status: http.StatusOK},
	} {
		t.Run(attempt.name, func(t *testing.T) {
			t.Setenv("CRYPTO_ADMIN_TEST_MODE", "false")
			user := activeCryptoAdmin()
			user.Role = service.RoleUser
			require.NoError(t, user.SetPassword("known-test-password"))
			h := newCryptoRouteHarness(t, user)
			repo := &cryptoRouteUserRepo{user: user}
			cache := repository.NewTotpCache(h.redis)
			svc := service.NewTotpService(repo, cryptoRouteEncryptor{}, cache, h.settings, nil, nil)
			router := gin.New()
			router.POST("/api/v1/user/totp/disable", func(c *gin.Context) {
				c.Set(string(middleware.ContextKeyUser), middleware.AuthSubject{UserID: user.ID})
			}, handler.NewTotpHandler(svc).Disable)
			body := attempt.body
			if attempt.status == http.StatusOK {
				code, err := totp.GenerateCode(cryptoRouteTotpSecret, time.Now())
				require.NoError(t, err)
				encoded, err := json.Marshal(map[string]string{"totp_code": code})
				require.NoError(t, err)
				body = string(encoded)
			}
			recorder := httptest.NewRecorder()
			req := httptest.NewRequest(http.MethodPost, "/api/v1/user/totp/disable", strings.NewReader(body))
			req.Header.Set("Content-Type", "application/json")
			router.ServeHTTP(recorder, req)
			require.Equal(t, attempt.status, recorder.Code, recorder.Body.String())
			require.Equal(t, attempt.status != http.StatusOK, user.TotpEnabled)
		})
	}
}

func (r *serverAuthorizationUserRepo) DisableTotpWithExpectedRole(ctx context.Context, id int64, role string, secret *string) error {
	if r.beforeDisable != nil {
		r.beforeDisable()
	}
	return r.cryptoRouteUserRepo.DisableTotpWithExpectedRole(ctx, id, role, secret)
}

func TestAdminTotpDisableHTTPWithServerAuthorization(t *testing.T) {
	for _, scenario := range []string{"success and replay", "wrong code", "other administrator code", "client user id", "mixed credentials", "rebound before consumption", "rebound before write", "demoted"} {
		t.Run(scenario, func(t *testing.T) {
			t.Setenv("CRYPTO_ADMIN_TEST_MODE", "false")
			user := activeCryptoAdmin()
			h := newCryptoRouteHarness(t, user)
			repo := &serverAuthorizationUserRepo{cryptoRouteUserRepo: cryptoRouteUserRepo{user: user}}
			cache := repository.NewTotpCache(h.redis)
			store, ok := cache.(service.Admin2FAApprovalStore)
			require.True(t, ok)
			// Recovery does not require the old authenticator code or a payment
			// encryption key. The server authorizes exactly one existing secret.
			svc := service.NewTotpService(repo, nil, cache, h.settings, nil, nil)
			code, _, err := service.NewAdmin2FAApprovalService(repo, store).Issue(t.Context(), user.ID)
			require.NoError(t, err)
			body := map[string]string{"server_authorization_code": code}
			want := http.StatusOK
			switch scenario {
			case "other administrator code":
				// The authenticated identity cannot consume a code issued for another
				// valid administrator, even if it was obtained outside this session.
				other := activeCryptoAdmin()
				other.ID = 75
				otherRepo := &cryptoRouteUserRepo{user: other}
				otherCode, _, issueErr := service.NewAdmin2FAApprovalService(otherRepo, store).Issue(t.Context(), other.ID)
				require.NoError(t, issueErr)
				body["server_authorization_code"] = otherCode
				want = http.StatusForbidden
			case "client user id":
				body["user_id"] = "75 OR 1=1"
				want = http.StatusBadRequest
			case "wrong code":
				body["server_authorization_code"] = "g" + code[1:]
				want = http.StatusForbidden
			case "mixed credentials":
				body["totp_code"] = "123456"
				want = http.StatusBadRequest
			case "rebound before consumption":
				replacement := "new-encrypted-authenticator-generation"
				user.TotpSecretEncrypted = &replacement
				want = http.StatusForbidden
			case "rebound before write":
				repo.beforeDisable = func() {
					replacement := "new-encrypted-authenticator-generation"
					user.TotpSecretEncrypted = &replacement
				}
				want = http.StatusConflict
			case "demoted":
				user.Role = service.RoleUser
				want = http.StatusForbidden
			}
			router := gin.New()
			totpHandler := handler.NewTotpHandler(svc)
			router.POST("/api/v1/user/totp/disable", func(c *gin.Context) {
				c.Set(string(middleware.ContextKeyUser), middleware.AuthSubject{UserID: user.ID})
				c.Next()
			}, totpHandler.Disable)
			send := func(payload map[string]string) *httptest.ResponseRecorder {
				encoded, err := json.Marshal(payload)
				require.NoError(t, err)
				request := httptest.NewRequest(http.MethodPost, "/api/v1/user/totp/disable", strings.NewReader(string(encoded)))
				request.Header.Set("Content-Type", "application/json")
				recorder := httptest.NewRecorder()
				router.ServeHTTP(recorder, request)
				require.Contains(t, recorder.Header().Get("Cache-Control"), "no-store")
				require.NotContains(t, recorder.Body.String(), code)
				return recorder
			}
			response := send(body)
			require.Equal(t, want, response.Code, response.Body.String())
			if scenario == "mixed credentials" {
				require.True(t, user.TotpEnabled)
				response = send(map[string]string{"server_authorization_code": code})
				require.Equal(t, http.StatusOK, response.Code, response.Body.String())
			}
			if scenario == "success and replay" {
				require.False(t, user.TotpEnabled)
				replay := send(body)
				require.Equal(t, http.StatusForbidden, replay.Code, replay.Body.String())
			} else if scenario != "mixed credentials" {
				require.True(t, user.TotpEnabled)
			}
		})
	}
}
