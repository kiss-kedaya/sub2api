package routes

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/handler"
	adminhandler "github.com/Wei-Shaw/sub2api/internal/handler/admin"
	"github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/alicebob/miniredis/v2"
	"github.com/gin-gonic/gin"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/require"
)

type paymentTabOrderSettingsRepo struct {
	service.SettingRepository
	values map[string]string
}

func (r *paymentTabOrderSettingsRepo) GetValue(_ context.Context, key string) (string, error) {
	return r.values[key], nil
}

func (r *paymentTabOrderSettingsRepo) Get(_ context.Context, _ string) (*service.Setting, error) {
	return nil, nil
}

func (r *paymentTabOrderSettingsRepo) GetAll(_ context.Context) (map[string]string, error) {
	return r.values, nil
}

type paymentTabOrderUserRepo struct {
	service.UserRepository
	user *service.User
}

func (r *paymentTabOrderUserRepo) GetByID(context.Context, int64) (*service.User, error) {
	clone := *r.user
	return &clone, nil
}

func (r *paymentTabOrderUserRepo) GetUserAvatar(context.Context, int64) (*service.UserAvatar, error) {
	return nil, nil
}

// The recharge-centre tab layout is written through the existing admin payment
// config endpoint, so the admin middleware is the only thing standing between a
// normal user's token and a global layout change.
func TestPaymentTabOrderWriteRejectsNonAdmin(t *testing.T) {
	gin.SetMode(gin.TestMode)
	cfg := &config.Config{JWT: config.JWTConfig{Secret: "payment-tab-order-secret", ExpireHour: 1}}
	user := &service.User{
		ID: 7, Email: "user@example.test", Role: service.RoleUser, Status: service.StatusActive,
	}
	authService := service.NewAuthService(nil, nil, nil, nil, cfg, nil, nil, nil, nil, nil, nil, nil, nil)
	userService := service.NewUserService(&paymentTabOrderUserRepo{user: user}, nil, nil, nil)
	settings := service.NewSettingService(&paymentTabOrderSettingsRepo{values: map[string]string{}}, cfg)
	token, err := authService.GenerateToken(context.Background(), user)
	require.NoError(t, err)

	mini := miniredis.RunT(t)
	rdb := redis.NewClient(&redis.Options{Addr: mini.Addr()})
	t.Cleanup(func() { _ = rdb.Close() })
	limiter := middleware.NewPanelRateLimiter(rdb, settings)

	noop := func(c *gin.Context) { c.Next() }
	router := gin.New()
	require.NoError(t, router.SetTrustedProxies(nil))
	RegisterPaymentRoutes(
		router.Group("/api/v1"),
		&handler.PaymentHandler{},
		&handler.PaymentWebhookHandler{},
		&adminhandler.PaymentHandler{},
		middleware.JWTAuthMiddleware(noop),
		middleware.NewAdminAuthMiddleware(authService, userService, settings, nil, cfg),
		middleware.AuditLogMiddleware(noop),
		middleware.StepUpAuthMiddleware(noop),
		settings,
		limiter,
	)

	body := `{"payment_tab_order":["recharge","rechargeCenter"],"payment_default_tab":"recharge"}`
	req := httptest.NewRequest(http.MethodPut, "/api/v1/admin/payment/config", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+token)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	require.Equal(t, http.StatusForbidden, w.Code, w.Body.String())
	require.Contains(t, w.Body.String(), "FORBIDDEN")
}
