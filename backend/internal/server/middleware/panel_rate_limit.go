package middleware

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net"
	"net/http"
	"strconv"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/middleware"
	"github.com/Wei-Shaw/sub2api/internal/pkg/adminaccess"
	"github.com/Wei-Shaw/sub2api/internal/service"

	"github.com/gin-gonic/gin"
	"github.com/redis/go-redis/v9"
)

// panelRateLimitWindow 面板限流固定窗口时长（所有档位均按每分钟计数）。
const panelRateLimitWindow = time.Minute

const panelHeavyRateLimitChecked = "panel_heavy_rate_limit_checked"

// panelRateLimitAllower 抽象底层限流原语，便于单测注入。
type panelRateLimitAllower interface {
	Allow(ctx context.Context, key string, limit int, window time.Duration) (middleware.AllowResult, error)
}

// PanelRateLimiter 面板（管理面 /api/v1）API 限流器。
//
// 设计要点：
//   - 认证接口按「用户 ID」维度计数，受限管理密钥按 Key ID 独立计数：与客户端 IP 完全无关，反向代理/共享出口
//     （所有请求源 IP 坍缩为 127.0.0.1 等）不会互相误伤。
//   - 公开接口按安全客户端 IP 计数：仅统计全局单播地址，回环/内网/链路本地
//     地址（反代内部转发地址）直接跳过，避免误拦整条反代链路的流量。
//   - 配置走进程内缓存（60s TTL），热路径零 DB 访问。
//   - Redis 异常一律 fail-open：限流是保护措施，不能反过来把面板打挂。
type PanelRateLimiter struct {
	limiter        panelRateLimitAllower
	settingService *service.SettingService
}

// NewPanelRateLimiter 创建面板限流器。
func NewPanelRateLimiter(redisClient *redis.Client, settingService *service.SettingService) *PanelRateLimiter {
	return &PanelRateLimiter{
		limiter:        middleware.NewRateLimiter(redisClient),
		settingService: settingService,
	}
}

// Global 认证面板接口的全局按用户限流（宽松档，覆盖所有登录后端点）。
// 受限管理密钥不享管理员豁免，变更类请求额外消耗严格档额度。
func (p *PanelRateLimiter) Global() gin.HandlerFunc {
	return p.userScoped("global", func(s service.PanelRateLimitSettings) int { return s.UserRPM })
}

// Heavy 重查询接口的按用户限流（严格档，覆盖 usage/dashboard 等聚合统计端点）。
// 与 Global 叠加计数：一次重查询同时消耗两档额度。
func (p *PanelRateLimiter) Heavy() gin.HandlerFunc {
	return p.userScoped("heavy", func(s service.PanelRateLimitSettings) int { return s.HeavyRPM })
}

// apiKeySecretReadRPM 是 GET /user/api-keys/:id/secret-seal 的固定限流：
// 每用户每分钟 20 次。这个接口会把加密后的凭据交给浏览器，加上限流是为了
// 避免它被当成「暴力验证登录密码」的辅助通道（密码派生只在浏览器发生，
// 服务端能做的就是控制密文的可取次数）。
const apiKeySecretReadRPM = 20

// apiKeySecretWriteRPM 是 PUT /user/api-keys/:id/secret-seal 的固定限流：每用户每分钟 60 次。
// 封存要带登录密码做 bcrypt 比对，但批量轮换后连续封存最多 50 把是正常用法，
// 套用创建档的 10 次/分钟会让第 11 把起永远封不上。口令猜测由按用户的失败预算
// （每分钟最多 5 次错误，见 service.APIKeyPasswordAttemptLimiter）兜底，这里只是请求量上限。
const apiKeySecretWriteRPM = 60

// apiKeyVerificationRPM 是创建 Key / 轮换 / 二次查看授权 / Passkey 校验接口的固定限流：
// 每用户每分钟 10 次。这些接口要验证登录密码（bcrypt）与动态码，放开等于提供
// 一个在线爆破入口；10 次/分钟足够正常用户，也把猜测成本压住。
const apiKeyVerificationRPM = 10

// keyUpdatePeekLimit 是判定 regenerate_key 时最多预读的请求体字节数。
const keyUpdatePeekLimit = 1 << 20

// KeyVerification 是「创建 / 轮换 / 二次查看 API Key 的三选一身份校验」固定档按用户限流。
// 与面板限流设置无关：这是密码/动态码校验入口的安全护栏，始终存在；
// 底层 Redis 出错时 fail-open（保护措施不能反过来打挂面板）。
func (p *PanelRateLimiter) KeyVerification() gin.HandlerFunc {
	return p.fixedUserTier("api_key_verify", apiKeyVerificationRPM, nil)
}

// KeyVerificationOnRotation 与 KeyVerification 同档同桶（每用户共用 10 次/分钟），
// 但只给带 regenerate_key=true 的 PUT /keys/:id 计数：改名 / 启停 / 配额这类高频编辑
// 不下发明文，不能被这个档位挡住。
func (p *PanelRateLimiter) KeyVerificationOnRotation() gin.HandlerFunc {
	return p.fixedUserTier("api_key_verify", apiKeyVerificationRPM, requestRegeneratesKey)
}

// SecretSealRead 浏览器端封存密文读取的固定档按用户限流。
// 与 Global/Heavy 不同，这里不读面板限流设置：这是一条安全护栏，
// 应当始终存在；底层 Redis 出错时仍然 fail-open（保护措施不能反过来打挂面板）。
func (p *PanelRateLimiter) SecretSealRead() gin.HandlerFunc {
	return p.fixedUserTier("api_key_secret", apiKeySecretReadRPM, nil)
}

// SecretSealWrite 浏览器端封存密文写入（PUT，带登录密码校验）的固定档按用户限流。
func (p *PanelRateLimiter) SecretSealWrite() gin.HandlerFunc {
	return p.fixedUserTier("api_key_secret_write", apiKeySecretWriteRPM, nil)
}

// admin2FAResetPerMinute / admin2FAResetPerHour 是破玻璃重置他人 2FA 的固定限流：
// 每个管理员每分钟 3 次、每小时 20 次。这个接口一次降低一个账号的安全强度，没有批量
// 用法；正常处理工单一次一个。上限是给「会话被劫持后批量清空别人 2FA」设的天花板。
const (
	admin2FAResetPerMinute = 3
	admin2FAResetPerHour   = 20
)

// Admin2FAReset 是破玻璃重置他人 2FA 的固定档按管理员限流（每分钟 3 次 + 每小时 20 次，
// 两档都要过）。与面板限流设置无关：这是安全护栏，始终存在；底层 Redis 出错时 fail-open
// （该路由本身要过 step-up，Redis 不可用时门控已经 fail-closed）。
func (p *PanelRateLimiter) Admin2FAReset() gin.HandlerFunc {
	return func(c *gin.Context) {
		if p.fixedUserAllow(c, "admin_2fa_reset", admin2FAResetPerMinute, panelRateLimitWindow, nil) &&
			p.fixedUserAllow(c, "admin_2fa_reset_hourly", admin2FAResetPerHour, time.Hour, nil) {
			c.Next()
		}
	}
}

// fixedUserTier 是「固定档按用户限流」的公共实现：不读面板限流设置、始终存在；
// 没有认证主体时放行；底层 Redis 出错时 fail-open。counts 非 nil 时只给返回 true 的请求计数。
func (p *PanelRateLimiter) fixedUserTier(bucket string, rpm int, counts func(*gin.Context) bool) gin.HandlerFunc {
	return func(c *gin.Context) {
		if p.fixedUserAllow(c, bucket, rpm, panelRateLimitWindow, counts) {
			c.Next()
		}
	}
}

// fixedUserAllow 对「当前认证用户 + bucket」做一次固定窗口计数。返回 true 表示放行
// （包括没有限流器/没有认证主体/不计数/Redis 出错这些放行分支）；返回 false 表示已写
// 429 并中止请求，调用方不要再继续。
func (p *PanelRateLimiter) fixedUserAllow(c *gin.Context, bucket string, limit int, window time.Duration, counts func(*gin.Context) bool) bool {
	if p == nil || p.limiter == nil {
		return true
	}
	subject, ok := GetAuthSubjectFromContext(c)
	if !ok || subject.UserID <= 0 {
		return true
	}
	if counts != nil && !counts(c) {
		return true
	}
	scopePrefix := ":user:"
	if c.GetString("auth_method") == service.AuditAuthMethodUserAPIKey {
		// 个人接口密钥独立计数，避免失控脚本占满同一账号交互式会话的校验配额（例如重置密钥）。
		scopePrefix = ":personal-key:"
	}
	key := "panel:" + bucket + scopePrefix + strconv.FormatInt(subject.UserID, 10)
	result, err := p.limiter.Allow(c.Request.Context(), key, limit, window)
	if err != nil {
		slog.Warn("panel fixed-tier rate limit check failed, allowing request", "bucket", bucket, "error", err)
		return true
	}
	if !result.Allowed {
		abortPanelRateLimited(c, result.RetryAfter)
		return false
	}
	return true
}

// requestRegeneratesKey 报告 PUT /keys/:id 的请求体是否带 regenerate_key=true。
//
// 只为决定「要不要计入身份校验限流档」而预读请求体，读完立刻原样拼回（handler 与审计
// 照常解析）。无法证明「不是轮换」的请求（超过预读上限、读取失败）按轮换计数：宁可多计，
// 也不让大请求体绕过限流；JSON 本身不合法的请求 handler 会按 400 拒绝，不计数。
//
// 解码口径必须和 handler 的 ShouldBindJSON 完全一致：只解码第一个 JSON 值，后面多出来的
// 字节不算错。用 json.Unmarshal 的话，「合法 JSON 后面拼一段垃圾」会在这里判错而不计数，
// handler 却照常解码并轮换——等于给限流开了后门。
func requestRegeneratesKey(c *gin.Context) bool {
	orig := c.Request.Body
	if orig == nil || orig == http.NoBody {
		return false
	}
	raw, err := io.ReadAll(io.LimitReader(orig, keyUpdatePeekLimit+1))
	c.Request.Body = &restoredBody{Reader: io.MultiReader(bytes.NewReader(raw), orig), closer: orig}
	if err != nil || len(raw) > keyUpdatePeekLimit {
		return true
	}
	var probe struct {
		RegenerateKey bool `json:"regenerate_key"`
	}
	if json.NewDecoder(bytes.NewReader(raw)).Decode(&probe) != nil {
		return false
	}
	return probe.RegenerateKey
}

func (p *PanelRateLimiter) userScoped(scope string, limitOf func(service.PanelRateLimitSettings) int) gin.HandlerFunc {
	return func(c *gin.Context) {
		if p == nil || p.limiter == nil {
			c.Next()
			return
		}
		isUserAPIKey := c.GetString("auth_method") == service.AuditAuthMethodUserAPIKey
		var settings service.PanelRateLimitSettings
		if p.settingService != nil {
			settings = p.settingService.GetPanelRateLimitSettingsCached(c.Request.Context())
		} else if !isUserAPIKey {
			c.Next()
			return
		}
		if !settings.Enabled && !isUserAPIKey {
			c.Next()
			return
		}
		subject, ok := GetAuthSubjectFromContext(c)
		if !ok || subject.UserID <= 0 {
			// 无认证主体（认证中间件缺位时的防御分支）：放行，避免误伤
			c.Next()
			return
		}
		principal := adminaccess.FromContext(c.Request.Context())
		isMachineKey := principal != nil || isUserAPIKey
		if settings.ExemptAdmin && !isMachineKey {
			if role, hasRole := GetUserRoleFromContext(c); hasRole && role == service.RoleAdmin {
				c.Next()
				return
			}
		}

		identity := "user:" + strconv.FormatInt(subject.UserID, 10)
		if principal != nil {
			identity = "admin-key:" + principal.KeyID
		} else if isUserAPIKey {
			// 个人接口密钥使用独立限流桶，既保证自动化脚本无法饿死同一账号的浏览器交互，
			// 也无法通过混用会话绕过机器密钥限流。
			identity = "personal-key:" + strconv.FormatInt(subject.UserID, 10)
		}
		type rateLimitCheck struct {
			scope string
			limit int
		}
		primaryLimit := limitOf(settings)
		heavyLimit := settings.HeavyRPM
		if isUserAPIKey {
			// 即使全局面板限流开关关闭，个人接口密钥也始终受保底限流约束。
			effectiveRead := service.UserPersonalAPIKeyFallbackReadRPM
			effectiveWrite := service.UserPersonalAPIKeyFallbackWriteRPM
			if settings.Enabled {
				if settings.UserRPM > 0 {
					effectiveRead = settings.UserRPM
				}
				if settings.HeavyRPM > 0 {
					effectiveWrite = settings.HeavyRPM
				}
			}
			if effectiveWrite > effectiveRead {
				effectiveWrite = effectiveRead
			}
			if scope == "heavy" {
				primaryLimit = effectiveWrite
			} else {
				primaryLimit = effectiveRead
			}
			heavyLimit = effectiveWrite
		}
		checks := []rateLimitCheck{{scope, primaryLimit}}
		if scope == "global" && isMachineKey && c.Request.Method != http.MethodGet &&
			c.Request.Method != http.MethodHead && c.Request.Method != http.MethodOptions {
			checks = append(checks, rateLimitCheck{"heavy", heavyLimit})
		}
		for _, check := range checks {
			if check.limit <= 0 || (isMachineKey && check.scope == "heavy" && c.GetBool(panelHeavyRateLimitChecked)) {
				continue
			}
			if isMachineKey && check.scope == "heavy" {
				// Global 已为机器密钥写入计数，路由再挂 Heavy 时不能重复消耗额度。
				c.Set(panelHeavyRateLimitChecked, true)
			}
			key := "panel:" + check.scope + ":" + identity
			result, err := p.limiter.Allow(c.Request.Context(), key, check.limit, panelRateLimitWindow)
			if err != nil {
				// fail-open：Redis 异常不阻断面板访问。
				slog.Warn("panel rate limit check failed, allowing request", "scope", check.scope, "error", err)
				continue
			}
			if !result.Allowed {
				abortPanelRateLimited(c, result.RetryAfter)
				return
			}
		}
		c.Next()
	}
}

// PublicIP 无需认证的公开接口按客户端 IP 限流。
// 使用与审计日志/会话绑定一致的安全客户端 IP 解析；解析结果为回环/内网/
// 链路本地地址时跳过计数（这类地址通常是反代内部转发地址，按它计数会把
// 整条反代链路的所有真实用户合并进同一个桶造成大面积误拦截）。
func (p *PanelRateLimiter) PublicIP() gin.HandlerFunc {
	return func(c *gin.Context) {
		if p == nil || p.limiter == nil || p.settingService == nil {
			c.Next()
			return
		}
		settings := p.settingService.GetPanelRateLimitSettingsCached(c.Request.Context())
		if !settings.Enabled || settings.PublicIPRPM <= 0 {
			c.Next()
			return
		}
		clientIP := SecurityClientIP(c)
		if !isPubliclyRoutableClientIP(clientIP) {
			c.Next()
			return
		}

		result, err := p.limiter.Allow(c.Request.Context(), "panel:public:ip:"+clientIP, settings.PublicIPRPM, panelRateLimitWindow)
		if err != nil {
			slog.Warn("panel public rate limit check failed, allowing request", "error", err)
			c.Next()
			return
		}
		if !result.Allowed {
			abortPanelRateLimited(c, result.RetryAfter)
			return
		}
		c.Next()
	}
}

// isPubliclyRoutableClientIP 判断地址是否为可作为限流依据的全局单播地址。
// 回环、RFC1918/ULA 内网、链路本地与未指定地址返回 false。
func isPubliclyRoutableClientIP(clientIP string) bool {
	ip := net.ParseIP(clientIP)
	if ip == nil {
		return false
	}
	if ip.IsLoopback() || ip.IsPrivate() || ip.IsUnspecified() ||
		ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() {
		return false
	}
	return ip.IsGlobalUnicast()
}

func abortPanelRateLimited(c *gin.Context, retryAfter time.Duration) {
	if retryAfter <= 0 {
		retryAfter = panelRateLimitWindow
	}
	seconds := int64(retryAfter / time.Second)
	if retryAfter%time.Second > 0 {
		seconds++
	}
	c.Header("Retry-After", strconv.FormatInt(seconds, 10))
	AbortWithError(c, http.StatusTooManyRequests, "RATE_LIMITED", "Too many requests, please slow down and try again later")
}
