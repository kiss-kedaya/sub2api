package middleware

import (
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

// 单条轮换走 PUT /keys/:id，和改名 / 启停 / 配额共用同一个路径：
// 校验档限流只能数「带 regenerate_key=true」的请求，其余编辑一次都不能被挡。

func newKeyUpdateRouter(limiter *PanelRateLimiter, userID int64, seen *[]string) *gin.Engine {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.PUT("/keys/:id",
		func(c *gin.Context) {
			c.Set(string(ContextKeyUser), AuthSubject{UserID: userID})
			c.Next()
		},
		limiter.KeyVerificationOnRotation(),
		func(c *gin.Context) {
			// handler 必须读到完整、未被预读吃掉的请求体。
			raw, _ := io.ReadAll(c.Request.Body)
			if seen != nil {
				*seen = append(*seen, string(raw))
			}
			c.Status(http.StatusOK)
		})
	return router
}

func putKeyUpdate(router *gin.Engine, body string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodPut, "/keys/1", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	return rec
}

func TestKeyVerificationOnRotationCountsOnlyRegenerateRequests(t *testing.T) {
	limiter := &PanelRateLimiter{limiter: &fakePanelAllower{counts: map[string]int64{}}}
	router := newKeyUpdateRouter(limiter, 7, nil)

	// 改名 / 启停 / 配额等编辑不下发明文：打 50 次也不能被限流。
	for i := 0; i < 50; i++ {
		rec := putKeyUpdate(router, `{"name":"renamed","status":"inactive","quota":3}`)
		require.Equal(t, http.StatusOK, rec.Code, "plain edit %d must never be rate limited", i+1)
	}
	// regenerate_key=false 同样不算轮换。
	rec := putKeyUpdate(router, `{"regenerate_key":false}`)
	require.Equal(t, http.StatusOK, rec.Code)

	// 轮换请求每用户 10 次/分钟；第 11 次 429 并带 Retry-After。
	for i := 0; i < apiKeyVerificationRPM; i++ {
		rec = putKeyUpdate(router, `{"regenerate_key":true,"verification":{"method":"password","password":"x"}}`)
		require.Equal(t, http.StatusOK, rec.Code, "rotation %d must pass", i+1)
	}
	rec = putKeyUpdate(router, `{"regenerate_key":true}`)
	require.Equal(t, http.StatusTooManyRequests, rec.Code)
	require.NotEmpty(t, rec.Header().Get("Retry-After"))

	// 轮换额度耗尽后，普通编辑仍然畅通。
	rec = putKeyUpdate(router, `{"name":"still-fine"}`)
	require.Equal(t, http.StatusOK, rec.Code)
}

func TestKeyVerificationOnRotationSharesBucketWithKeyVerification(t *testing.T) {
	allower := &fakePanelAllower{counts: map[string]int64{}}
	limiter := &PanelRateLimiter{limiter: allower}

	gin.SetMode(gin.TestMode)
	router := gin.New()
	subject := func(c *gin.Context) {
		c.Set(string(ContextKeyUser), AuthSubject{UserID: 7})
		c.Next()
	}
	ok := func(c *gin.Context) { c.Status(http.StatusOK) }
	router.POST("/create", subject, limiter.KeyVerification(), ok)
	router.PUT("/keys/:id", subject, limiter.KeyVerificationOnRotation(), ok)

	// 创建占掉 10 次，轮换立刻 429：校验入口共用同一个猜测预算，不能各开 10 次。
	for i := 0; i < apiKeyVerificationRPM; i++ {
		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/create", nil))
		require.Equal(t, http.StatusOK, rec.Code)
	}
	rec := putKeyUpdate(router, `{"regenerate_key":true}`)
	require.Equal(t, http.StatusTooManyRequests, rec.Code)
	require.Contains(t, allower.counts, "panel:api_key_verify:user:7")
}

func TestKeyVerificationOnRotationRestoresBodyForHandler(t *testing.T) {
	limiter := &PanelRateLimiter{limiter: &fakePanelAllower{counts: map[string]int64{}}}
	var seen []string
	router := newKeyUpdateRouter(limiter, 7, &seen)

	body := `{"regenerate_key":true,"verification":{"method":"totp","totp_code":"123456"}}`
	require.Equal(t, http.StatusOK, putKeyUpdate(router, body).Code)
	plain := `{"name":"renamed"}`
	require.Equal(t, http.StatusOK, putKeyUpdate(router, plain).Code)
	require.Equal(t, []string{body, plain}, seen)
}

// 合法 JSON 后面拼垃圾字节：gin 的 ShouldBindJSON 只解码第一个值，照样能解出
// regenerate_key=true 并轮换；限流不能因为「整体不是合法 JSON」就放过它。
func TestKeyVerificationOnRotationCountsTrailingBytesLikeTheHandlerDecodes(t *testing.T) {
	allower := &fakePanelAllower{counts: map[string]int64{}}
	limiter := &PanelRateLimiter{limiter: allower}

	gin.SetMode(gin.TestMode)
	router := gin.New()
	var handlerSawRotation []bool
	router.PUT("/keys/:id",
		func(c *gin.Context) {
			c.Set(string(ContextKeyUser), AuthSubject{UserID: 7})
			c.Next()
		},
		limiter.KeyVerificationOnRotation(),
		func(c *gin.Context) {
			var req struct {
				RegenerateKey bool `json:"regenerate_key"`
			}
			// 与 handler.Update 同一个绑定函数：证明这类请求在 handler 眼里就是一次轮换。
			handlerSawRotation = append(handlerSawRotation, c.ShouldBindJSON(&req) == nil && req.RegenerateKey)
			c.Status(http.StatusOK)
		})

	for i := 0; i < apiKeyVerificationRPM; i++ {
		rec := putKeyUpdate(router, `{"regenerate_key":true} trailing-bytes-after-the-first-value`)
		require.Equal(t, http.StatusOK, rec.Code, "request %d", i+1)
	}
	require.Equal(t, []bool{true, true, true, true, true, true, true, true, true, true}, handlerSawRotation)
	require.EqualValues(t, apiKeyVerificationRPM, allower.counts["panel:api_key_verify:user:7"])

	rec := putKeyUpdate(router, `{"regenerate_key":true} trailing-bytes-after-the-first-value`)
	require.Equal(t, http.StatusTooManyRequests, rec.Code)
}

func TestKeyVerificationOnRotationCountsWhatItCannotRuleOut(t *testing.T) {
	allower := &fakePanelAllower{counts: map[string]int64{}}
	limiter := &PanelRateLimiter{limiter: allower}
	router := newKeyUpdateRouter(limiter, 7, nil)

	// JSON 不合法：handler 会按 400 拒绝，不下发明文，不计数。
	require.Equal(t, http.StatusOK, putKeyUpdate(router, `{not json`).Code)
	require.Empty(t, allower.counts)

	// 超过预读上限：无法证明不是轮换，按轮换计数（regenerate_key 排在大字段后面也躲不掉）。
	huge := `{"name":"` + strings.Repeat("a", keyUpdatePeekLimit+1) + `","regenerate_key":true}`
	require.Equal(t, http.StatusOK, putKeyUpdate(router, huge).Code)
	require.EqualValues(t, 1, allower.counts["panel:api_key_verify:user:7"])
}

func TestKeyVerificationOnRotationFailOpenAndNilSafe(t *testing.T) {
	// Redis 出错 fail-open，与 KeyVerification 一致。
	limiter := &PanelRateLimiter{limiter: &fakePanelAllower{err: errors.New("redis down")}}
	router := newKeyUpdateRouter(limiter, 7, nil)
	require.Equal(t, http.StatusOK, putKeyUpdate(router, `{"regenerate_key":true}`).Code)

	// nil 限流器（测试装配）直接放行。
	var nilLimiter *PanelRateLimiter
	router = newKeyUpdateRouter(nilLimiter, 7, nil)
	require.Equal(t, http.StatusOK, putKeyUpdate(router, `{"regenerate_key":true}`).Code)

	// 没有认证主体（认证中间件缺位）放行，也不预读请求体。
	gin.SetMode(gin.TestMode)
	anon := gin.New()
	anon.PUT("/keys/:id",
		(&PanelRateLimiter{limiter: &fakePanelAllower{counts: map[string]int64{}}}).KeyVerificationOnRotation(),
		func(c *gin.Context) { c.Status(http.StatusOK) })
	require.Equal(t, http.StatusOK, putKeyUpdate(anon, `{"regenerate_key":true}`).Code)
}

// PUT secret-seal 要带登录密码做 bcrypt 比对，原先只受全局 240 次/分钟约束。
func TestPanelRateLimiterSecretSealWriteLimitsPerUser(t *testing.T) {
	gin.SetMode(gin.TestMode)
	allower := &fakePanelAllower{counts: map[string]int64{}}
	limiter := &PanelRateLimiter{limiter: allower}

	router := gin.New()
	subject := func(userID int64) gin.HandlerFunc {
		return func(c *gin.Context) {
			c.Set(string(ContextKeyUser), AuthSubject{UserID: userID})
			c.Next()
		}
	}
	ok := func(c *gin.Context) { c.Status(http.StatusOK) }
	router.PUT("/u7/seal", subject(7), limiter.SecretSealWrite(), ok)
	router.PUT("/u8/seal", subject(8), limiter.SecretSealWrite(), ok)

	// 批量轮换后连续封存最多 50 把必须放得过去。
	for i := 0; i < 50; i++ {
		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, httptest.NewRequest(http.MethodPut, "/u7/seal", nil))
		require.Equal(t, http.StatusOK, rec.Code, "seal %d must pass", i+1)
	}
	for i := 50; i < apiKeySecretWriteRPM; i++ {
		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, httptest.NewRequest(http.MethodPut, "/u7/seal", nil))
		require.Equal(t, http.StatusOK, rec.Code)
	}
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, httptest.NewRequest(http.MethodPut, "/u7/seal", nil))
	require.Equal(t, http.StatusTooManyRequests, rec.Code)
	require.NotEmpty(t, rec.Header().Get("Retry-After"))

	// 另一个用户有独立额度；写档不占读档（GET）的桶，也不占校验档的桶。
	rec = httptest.NewRecorder()
	router.ServeHTTP(rec, httptest.NewRequest(http.MethodPut, "/u8/seal", nil))
	require.Equal(t, http.StatusOK, rec.Code)
	require.Contains(t, allower.counts, "panel:api_key_secret_write:user:7")
	require.NotContains(t, allower.counts, "panel:api_key_secret:user:7")
	require.NotContains(t, allower.counts, "panel:api_key_verify:user:7")
}

func TestPanelRateLimiterSecretSealWriteNilSafe(t *testing.T) {
	gin.SetMode(gin.TestMode)
	var limiter *PanelRateLimiter
	router := gin.New()
	router.PUT("/seal", func(c *gin.Context) {
		c.Set(string(ContextKeyUser), AuthSubject{UserID: 7})
		c.Next()
	}, limiter.SecretSealWrite(), func(c *gin.Context) { c.Status(http.StatusOK) })

	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, httptest.NewRequest(http.MethodPut, "/seal", nil))
	require.Equal(t, http.StatusOK, rec.Code)
}
