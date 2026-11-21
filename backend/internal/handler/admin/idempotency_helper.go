package admin

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

type idempotencyStoreUnavailableMode int

const (
	idempotencyStoreUnavailableFailClose idempotencyStoreUnavailableMode = iota
	idempotencyStoreUnavailableFailOpen
)

func executeAdminIdempotent(
	c *gin.Context,
	scope string,
	payload any,
	ttl time.Duration,
	execute func(context.Context) (any, error),
) (*service.IdempotencyExecuteResult, error) {
	return executeAdminIdempotentWithResponseOptions(c, scope, payload, ttl, 0, false, execute)
}

func executeAdminIdempotentWithResponseOptions(
	c *gin.Context,
	scope string,
	payload any,
	ttl time.Duration,
	maxStoredResponseLen int,
	preserveStoredResponse bool,
	execute func(context.Context) (any, error),
) (*service.IdempotencyExecuteResult, error) {
	coordinator := service.DefaultIdempotencyCoordinator()
	if coordinator == nil {
		data, err := execute(c.Request.Context())
		if err != nil {
			return nil, err
		}
		return &service.IdempotencyExecuteResult{Data: data}, nil
	}

	return coordinator.Execute(c.Request.Context(), service.IdempotencyExecuteOptions{
		Scope:                  scope,
		ActorScope:             adminActorScope(c),
		Method:                 c.Request.Method,
		Route:                  c.FullPath(),
		IdempotencyKey:         c.GetHeader("Idempotency-Key"),
		Payload:                payload,
		RequireKey:             true,
		TTL:                    ttl,
		MaxStoredResponseLen:   maxStoredResponseLen,
		PreserveStoredResponse: preserveStoredResponse,
	}, execute)
}

func adminActorScope(c *gin.Context) string {
	actorScope := "admin:0"
	if subject, ok := middleware2.GetAuthSubjectFromContext(c); ok {
		actorScope = "admin:" + strconv.FormatInt(subject.UserID, 10)
	}
	return actorScope
}

func executeAdminIdempotentJSON(
	c *gin.Context,
	scope string,
	payload any,
	ttl time.Duration,
	execute func(context.Context) (any, error),
) {
	executeAdminIdempotentJSONWithMode(c, scope, payload, ttl, idempotencyStoreUnavailableFailClose, 0, false, execute)
}

func executeAdminIdempotentJSONWithResponseStorage(
	c *gin.Context,
	scope string,
	payload any,
	ttl time.Duration,
	maxStoredResponseLen int,
	preserveStoredResponse bool,
	execute func(context.Context) (any, error),
) {
	executeAdminIdempotentJSONWithMode(c, scope, payload, ttl, idempotencyStoreUnavailableFailClose, maxStoredResponseLen, preserveStoredResponse, execute)
}

func executeAdminIdempotentJSONFailOpenOnStoreUnavailable(
	c *gin.Context,
	scope string,
	payload any,
	ttl time.Duration,
	execute func(context.Context) (any, error),
) {
	executeAdminIdempotentJSONWithMode(c, scope, payload, ttl, idempotencyStoreUnavailableFailOpen, 0, false, execute)
}

func executeAdminIdempotentJSONWithMode(
	c *gin.Context,
	scope string,
	payload any,
	ttl time.Duration,
	mode idempotencyStoreUnavailableMode,
	maxStoredResponseLen int,
	preserveStoredResponse bool,
	execute func(context.Context) (any, error),
) {
	executeAdminIdempotentJSONWithProjection(c, scope, payload, ttl, mode, maxStoredResponseLen, preserveStoredResponse, execute, nil)
}

// executeAdminIdempotentJSONWithResponse 在实际执行及缓存重放之后统一约束响应。
func executeAdminIdempotentJSONWithResponse(c *gin.Context, scope string, payload any, ttl time.Duration, execute func(context.Context) (any, error), present func(any) any) {
	executeAdminIdempotentJSONWithProjection(c, scope, payload, ttl, idempotencyStoreUnavailableFailClose, 0, false, execute, present)
}

func executeAdminIdempotentJSONWithProjection(
	c *gin.Context,
	scope string,
	payload any,
	ttl time.Duration,
	mode idempotencyStoreUnavailableMode,
	maxStoredResponseLen int,
	preserveStoredResponse bool,
	execute func(context.Context) (any, error),
	present func(any) any,
) {
	if present == nil {
		present = func(data any) any { return data }
	}
	result, err := executeAdminIdempotentWithResponseOptions(c, scope, payload, ttl, maxStoredResponseLen, preserveStoredResponse, execute)
	if err != nil {
		if infraerrors.Code(err) == infraerrors.Code(service.ErrIdempotencyStoreUnavail) {
			strategy := "fail_close"
			if mode == idempotencyStoreUnavailableFailOpen {
				strategy = "fail_open"
			}
			service.RecordIdempotencyStoreUnavailable(c.FullPath(), scope, "handler_"+strategy)
			logger.LegacyPrintf("handler.idempotency", "[Idempotency] store unavailable: method=%s route=%s scope=%s strategy=%s", c.Request.Method, c.FullPath(), scope, strategy)
			if mode == idempotencyStoreUnavailableFailOpen {
				data, fallbackErr := execute(c.Request.Context())
				if fallbackErr != nil {
					response.ErrorFrom(c, fallbackErr)
					return
				}
				c.Header("X-Idempotency-Degraded", "store-unavailable")
				response.Success(c, present(data))
				return
			}
		}
		if retryAfter := service.RetryAfterSecondsFromError(err); retryAfter > 0 {
			c.Header("Retry-After", strconv.Itoa(retryAfter))
		}
		response.ErrorFrom(c, err)
		return
	}
	if result != nil && result.Replayed {
		c.Header("X-Idempotency-Replayed", "true")
	}
	response.Success(c, present(result.Data))
}

// executeAdminIdempotentJSONWithReplayAwareResponse 用于「首次响应和重放响应必须不同」
// 的接口（典型：一次性明文回执）。
//
// 与 executeAdminIdempotentJSONWithProjection 的区别只在最后一步：present 能拿到
// replayed 标志，按「首次 / 重放」决定响应体。存储不可用时的 observability、Retry-After
// 与 fail-close 语义完全复用，不允许调用方各自复制一份（漏了就会缺指标/缺日志）。
//
// present 返回错误时按「幂等存储不可用」处理：重放数据无法安全解码时绝不半截响应。
func executeAdminIdempotentJSONWithReplayAwareResponse(
	c *gin.Context,
	scope string,
	payload any,
	ttl time.Duration,
	maxStoredResponseLen int,
	preserveStoredResponse bool,
	execute func(context.Context) (any, error),
	present func(replayed bool, data any) (any, error),
) {
	if present == nil {
		present = func(_ bool, data any) (any, error) { return data, nil }
	}
	result, err := executeAdminIdempotentWithResponseOptions(c, scope, payload, ttl, maxStoredResponseLen, preserveStoredResponse, execute)
	if err != nil {
		if infraerrors.Code(err) == infraerrors.Code(service.ErrIdempotencyStoreUnavail) {
			service.RecordIdempotencyStoreUnavailable(c.FullPath(), scope, "handler_fail_close")
			logger.LegacyPrintf("handler.idempotency", "[Idempotency] store unavailable: method=%s route=%s scope=%s strategy=fail_close", c.Request.Method, c.FullPath(), scope)
		}
		if retryAfter := service.RetryAfterSecondsFromError(err); retryAfter > 0 {
			c.Header("Retry-After", strconv.Itoa(retryAfter))
		}
		response.ErrorFrom(c, err)
		return
	}
	replayed := result != nil && result.Replayed
	if replayed {
		c.Header("X-Idempotency-Replayed", "true")
	}
	var payloadData any
	if result != nil {
		payloadData = result.Data
	}
	data, presentErr := present(replayed, payloadData)
	if presentErr != nil {
		response.ErrorFrom(c, service.ErrIdempotencyStoreUnavail)
		return
	}
	response.Success(c, data)
}
