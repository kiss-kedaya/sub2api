package handler

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/service"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

// loginTOTPSettingRepo 是密码登录测试用的设置仓储：
// values 提供正常值；errKey 指向的单个 key 读取失败（模拟设置存储抖动/单行读失败）。
type loginTOTPSettingRepo struct {
	values map[string]string
	errKey string
}

func (r *loginTOTPSettingRepo) Get(_ context.Context, key string) (*service.Setting, error) {
	value, err := r.GetValue(context.Background(), key)
	if err != nil {
		return nil, err
	}
	return &service.Setting{Key: key, Value: value}, nil
}

func (r *loginTOTPSettingRepo) GetValue(_ context.Context, key string) (string, error) {
	if r.errKey != "" && key == r.errKey {
		return "", errors.New("settings store unavailable")
	}
	if value, ok := r.values[key]; ok {
		return value, nil
	}
	return "", service.ErrSettingNotFound
}

func (r *loginTOTPSettingRepo) Set(context.Context, string, string) error { return nil }

func (r *loginTOTPSettingRepo) GetMultiple(_ context.Context, keys []string) (map[string]string, error) {
	out := make(map[string]string, len(keys))
	for _, key := range keys {
		if value, ok := r.values[key]; ok {
			out[key] = value
		}
	}
	return out, nil
}

func (r *loginTOTPSettingRepo) SetMultiple(context.Context, map[string]string) error { return nil }

func (r *loginTOTPSettingRepo) GetAll(context.Context) (map[string]string, error) {
	return r.values, nil
}

func (r *loginTOTPSettingRepo) Delete(context.Context, string) error { return nil }

// loginTOTPUserRepo 只提供密码登录所需的按邮箱/按 ID 读取。
type loginTOTPUserRepo struct {
	service.UserRepository
	user *service.User
}

func (r *loginTOTPUserRepo) GetByEmail(context.Context, string) (*service.User, error) {
	return r.user, nil
}

func (r *loginTOTPUserRepo) GetByID(context.Context, int64) (*service.User, error) {
	return r.user, nil
}

func newLoginTOTPTestRouter(t *testing.T, repo *loginTOTPSettingRepo, user *service.User) *gin.Engine {
	t.Helper()
	gin.SetMode(gin.TestMode)

	cfg := &config.Config{}
	cfg.JWT.Secret = "test-jwt-secret-at-least-32-bytes-long"
	cfg.JWT.AccessTokenExpireMinutes = 60

	settingSvc := service.NewSettingService(repo, cfg)
	authSvc := service.NewAuthService(nil, &loginTOTPUserRepo{user: user}, nil, nil, cfg,
		settingSvc, nil, nil, nil, nil, nil, nil, nil)
	totpSvc := service.NewTotpService(nil, nil, newTotpCacheStub(), settingSvc, nil, nil)
	h := NewAuthHandler(cfg, authSvc, nil, settingSvc, nil, nil, totpSvc, nil)

	r := gin.New()
	r.POST("/api/v1/auth/login", h.Login)
	return r
}

type loginTOTPResponse struct {
	Code int            `json:"code"`
	Data map[string]any `json:"data"`
}

func postLoginTOTP(t *testing.T, router *gin.Engine) loginTOTPResponse {
	t.Helper()
	body := `{"email":"totp@example.com","password":"correct-horse"}`
	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/login", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	router.ServeHTTP(w, req)

	var resp loginTOTPResponse
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp), w.Body.String())
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	return resp
}

// 密码登录必须与第三方 OAuth / passkey / pending OAuth 兑换共用
// SettingService.LoginRequiresTOTP 判定：设置读取失败时要 fail-closed 下发
// 2FA 挑战，而不是用 IsTotpEnabled 的 fail-open 结果直接签发会话。
func newLoginTOTPUser(email string) *service.User {
	user := &service.User{
		ID:          7,
		Email:       email,
		Role:        service.RoleUser,
		Status:      service.StatusActive,
		TotpEnabled: true,
		Concurrency: 1,
	}
	_ = user.SetPassword("correct-horse")
	return user
}

func TestPasswordLoginRequiresTOTPFailsClosedOnSettingReadError(t *testing.T) {
	user := newLoginTOTPUser("totp@example.com")

	t.Run("totp_enabled_challenges", func(t *testing.T) {
		repo := &loginTOTPSettingRepo{values: map[string]string{service.SettingKeyTotpEnabled: "true"}}
		resp := postLoginTOTP(t, newLoginTOTPTestRouter(t, repo, user))

		require.Equal(t, 0, resp.Code)
		require.Equal(t, true, resp.Data["requires_2fa"], "已启用 TOTP 的账号必须进入挑战流程")
		require.NotEmpty(t, resp.Data["temp_token"])
		require.Empty(t, resp.Data["access_token"], "2FA 未完成前不得签发会话")
	})

	// 绕过方向：功能开关读取失败时，旧实现 IsTotpEnabled 返回 false 会直接发 token。
	t.Run("totp_setting_read_error_fails_closed", func(t *testing.T) {
		repo := &loginTOTPSettingRepo{errKey: service.SettingKeyTotpEnabled}
		resp := postLoginTOTP(t, newLoginTOTPTestRouter(t, repo, user))

		require.Equal(t, 0, resp.Code)
		require.Equal(t, true, resp.Data["requires_2fa"], "设置读取失败不得静默关闭二次验证")
		require.NotEmpty(t, resp.Data["temp_token"])
		require.Empty(t, resp.Data["access_token"], "设置读取失败时不得直接签发会话")
	})

	// 不误伤：全局开关确实关闭时，已启用 TOTP 的老账号按既有行为直接登录。
	// 这里用合成邮箱的账号，登录成功路径才不会触发依赖 entClient 的身份回填。
	t.Run("totp_feature_disabled_keeps_login", func(t *testing.T) {
		reservedUser := newLoginTOTPUser("dingtalk-totp-test@dingtalk-connect.invalid")
		repo := &loginTOTPSettingRepo{values: map[string]string{service.SettingKeyTotpEnabled: "false"}}
		resp := postLoginTOTP(t, newLoginTOTPTestRouter(t, repo, reservedUser))

		require.Equal(t, 0, resp.Code)
		require.NotEqual(t, true, resp.Data["requires_2fa"])
		require.NotEmpty(t, resp.Data["access_token"])
	})
}
