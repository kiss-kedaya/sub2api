package handler

import (
	"context"
	"strconv"
	"time"

	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
	"github.com/Wei-Shaw/sub2api/internal/pkg/logger"
	"github.com/Wei-Shaw/sub2api/internal/pkg/response"
	middleware2 "github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"

	"github.com/gin-gonic/gin"
)

func executeUserIdempotentJSON(
	c *gin.Context,
	scope string,
	payload any,
	ttl time.Duration,
	execute func(context.Context) (any, error),
) {
	if result := runUserIdempotentJSON(c, scope, payload, ttl, execute); result != nil {
		response.Success(c, result.Data)
	}
}

// runUserIdempotentJSONWithReplayGate 与 runUserIdempotentJSON 相同，但在
// 「命中已成功的历史回执」时会先调用 replayAllowed：返回 false 就拒绝重放，
// 绝不把存储的响应交出去。
//
// 用于「回执里有凭据 / 明文」的写接口（创建 API Key 会返回一次明文）：
// 这类接口的幂等指纹刻意不含身份校验材料（密码 / 动态码 / Passkey 票据
// 不能落库），于是同一个 Idempotency-Key 在 TTL 内被重放时会命中历史记录。
// 没有 replayAllowed 时那是「跳过身份校验白拿回执」的绕过口。
func runUserIdempotentJSONWithReplayGate(
	c *gin.Context,
	scope string,
	payload any,
	ttl time.Duration,
	replayAllowed func() (bool, error),
	execute func(context.Context) (any, error),
) *service.IdempotencyExecuteResult {
	coordinator := service.DefaultIdempotencyCoordinator()
	if coordinator == nil || replayAllowed == nil {
		// 没有幂等协调器时本来就不会命中历史回执；没有 gate 时退回旧语义
		// （调用方在传入 gate 的场景都会同时装配协调器）。
		return runUserIdempotentJSON(c, scope, payload, ttl, execute)
	}

	actorScope := "user:0"
	if subject, ok := middleware2.GetAuthSubjectFromContext(c); ok {
		actorScope = "user:" + strconv.FormatInt(subject.UserID, 10)
	}

	result, err := coordinator.Execute(c.Request.Context(), service.IdempotencyExecuteOptions{
		Scope:          scope,
		ActorScope:     actorScope,
		Method:         c.Request.Method,
		Route:          c.FullPath(),
		IdempotencyKey: c.GetHeader("Idempotency-Key"),
		Payload:        payload,
		RequireKey:     true,
		TTL:            ttl,
		// 命中历史成功回执时先问 replayAllowed：幂等协调器只在
		// PreserveStoredResponse 下把存储响应交回来，其余情况交给 execute
		// 重新走业务（其中包含本请求自己的身份校验）。
		ReplayGuard: replayAllowed,
	}, execute)
	if err != nil {
		if infraerrors.Code(err) == infraerrors.Code(service.ErrIdempotencyStoreUnavail) {
			service.RecordIdempotencyStoreUnavailable(c.FullPath(), scope, "handler_fail_close")
			logger.LegacyPrintf("handler.idempotency", "[Idempotency] store unavailable: method=%s route=%s scope=%s strategy=fail_close", c.Request.Method, c.FullPath(), scope)
		}
		if retryAfter := service.RetryAfterSecondsFromError(err); retryAfter > 0 {
			c.Header("Retry-After", strconv.Itoa(retryAfter))
		}
		response.ErrorFrom(c, err)
		return nil
	}
	if result != nil && result.Replayed {
		c.Header("X-Idempotency-Replayed", "true")
	}
	return result
}

// runUserIdempotentJSON returns the result that may be persisted for replay.
// A caller can use a separate, request-local response for the initial success.
func runUserIdempotentJSON(
	c *gin.Context,
	scope string,
	payload any,
	ttl time.Duration,
	execute func(context.Context) (any, error),
) *service.IdempotencyExecuteResult {
	coordinator := service.DefaultIdempotencyCoordinator()
	if coordinator == nil {
		data, err := execute(c.Request.Context())
		if err != nil {
			response.ErrorFrom(c, err)
			return nil
		}
		return &service.IdempotencyExecuteResult{Data: data}
	}

	actorScope := "user:0"
	if subject, ok := middleware2.GetAuthSubjectFromContext(c); ok {
		actorScope = "user:" + strconv.FormatInt(subject.UserID, 10)
	}

	result, err := coordinator.Execute(c.Request.Context(), service.IdempotencyExecuteOptions{
		Scope:          scope,
		ActorScope:     actorScope,
		Method:         c.Request.Method,
		Route:          c.FullPath(),
		IdempotencyKey: c.GetHeader("Idempotency-Key"),
		Payload:        payload,
		RequireKey:     true,
		TTL:            ttl,
	}, execute)
	if err != nil {
		if infraerrors.Code(err) == infraerrors.Code(service.ErrIdempotencyStoreUnavail) {
			service.RecordIdempotencyStoreUnavailable(c.FullPath(), scope, "handler_fail_close")
			logger.LegacyPrintf("handler.idempotency", "[Idempotency] store unavailable: method=%s route=%s scope=%s strategy=fail_close", c.Request.Method, c.FullPath(), scope)
		}
		if retryAfter := service.RetryAfterSecondsFromError(err); retryAfter > 0 {
			c.Header("Retry-After", strconv.Itoa(retryAfter))
		}
		response.ErrorFrom(c, err)
		return nil
	}
	if result != nil && result.Replayed {
		c.Header("X-Idempotency-Replayed", "true")
	}
	return result
}
