//go:build unit

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

// step-up 验证接口必须签发 5 分钟窗口（与链上充值 crypto grant 一致），并在
// 响应体里回传 expires_at / expires_in 供前端展示与倒计时。
func TestStepUpEndpointIssuesFiveMinuteWindow(t *testing.T) {
	user := activeCryptoAdmin()
	harness := newCryptoRouteHarness(t, user)
	cache := repository.NewTotpCache(harness.redis)
	svc := service.NewTotpService(&cryptoRouteUserRepo{user: user}, cryptoRouteEncryptor{}, cache, harness.settings, nil, nil)

	router := gin.New()
	router.POST("/api/v1/user/totp/step-up", func(c *gin.Context) {
		c.Set(string(middleware.ContextKeyUser), middleware.AuthSubject{UserID: user.ID})
		c.Set(middleware.ContextKeySessionID, "session-a")
	}, handler.NewTotpHandler(svc).StepUp)

	code, err := totp.GenerateCode(cryptoRouteTotpSecret, time.Now())
	require.NoError(t, err)
	body, err := json.Marshal(map[string]string{"code": code})
	require.NoError(t, err)

	before := time.Now()
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/v1/user/totp/step-up", strings.NewReader(string(body)))
	req.Header.Set("Content-Type", "application/json")
	router.ServeHTTP(rec, req)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())

	var envelope struct {
		Code int `json:"code"`
		Data struct {
			Verified  bool  `json:"verified"`
			ExpiresAt int64 `json:"expires_at"`
			ExpiresIn int64 `json:"expires_in"`
		} `json:"data"`
	}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &envelope), rec.Body.String())
	require.True(t, envelope.Data.Verified)
	require.EqualValues(t, 300, envelope.Data.ExpiresIn, "窗口必须是 5 分钟")
	require.WithinDuration(t, before.Add(5*time.Minute), time.Unix(envelope.Data.ExpiresAt, 0), 5*time.Second)

	// Redis 里的真实 TTL 也是 5 分钟，而不是只改了响应体。
	remaining, ok, err := cache.StepUpGrantRemaining(context.Background(), user.ID, "session-a", service.FactorTOTP)
	require.NoError(t, err)
	require.True(t, ok)
	require.Greater(t, remaining, 4*time.Minute+59*time.Second)
	require.LessOrEqual(t, remaining, 5*time.Minute)
}
