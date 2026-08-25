package handler

import (
	"errors"
	"net/http"
	"strings"
	"unicode/utf8"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"go.uber.org/zap"
)

const gatewayForwardErrorSummaryMaxBytes = 512

// logGatewayForwardFailure keeps expected upstream request rejections out of
// ERROR so zap does not format a Go stack for every ordinary 400/429 response.
// The constant event name and level also let the configured zap sampler group
// repeated failures while the structured fields retain diagnostic context.
func logGatewayForwardFailure(log *zap.Logger, c *gin.Context, event string, err error, fields ...zap.Field) {
	logGatewayForwardFailureWithWarn(log, c, event, err, false, fields...)
}

func logGatewayForwardFailureWithWarn(log *zap.Logger, c *gin.Context, event string, err error, forceWarn bool, fields ...zap.Field) {
	if log == nil {
		return
	}

	status, summary := gatewayForwardFailureDetails(c, err)
	if status == http.StatusBadRequest || status == http.StatusTooManyRequests {
		fields = append(fields,
			zap.Int("upstream_status", status),
			zap.String("error_summary", summary),
		)
		log.Warn(event, fields...)
		return
	}

	if forceWarn {
		log.Warn(event, append(fields, zap.Error(err))...)
		return
	}
	if status > 0 {
		fields = append(fields, zap.Int("upstream_status", status))
	}
	log.Error(event, append(fields, zap.Error(err))...)
}

func gatewayForwardFailureDetails(c *gin.Context, err error) (int, string) {
	status := 0
	summary := ""

	var failoverErr *service.UpstreamFailoverError
	if errors.As(err, &failoverErr) && failoverErr != nil {
		status = failoverErr.StatusCode
		if len(failoverErr.ResponseBody) > 0 {
			summary = service.ExtractUpstreamErrorMessage(failoverErr.ResponseBody)
		}
	}

	if c != nil {
		if status == 0 {
			if value, ok := c.Get(service.OpsUpstreamStatusCodeKey); ok {
				contextStatus := 0
				switch typed := value.(type) {
				case int:
					contextStatus = typed
				case int64:
					contextStatus = int(typed)
				}
				// A prior failover attempt can leave 400/429 attribution in the
				// request context. Only downgrade an untyped error when the final
				// response was actually committed as a non-5xx response.
				if contextStatus != http.StatusBadRequest && contextStatus != http.StatusTooManyRequests {
					status = contextStatus
				} else if c.Writer != nil && c.Writer.Written() && c.Writer.Status() < http.StatusInternalServerError {
					status = contextStatus
				}
			}
		}
		if value, ok := c.Get(service.OpsUpstreamErrorMessageKey); ok {
			if message, ok := value.(string); ok && message != "" {
				summary = message
			}
		}
	}

	if summary == "" && err != nil {
		summary = err.Error()
	}
	return status, gatewayForwardErrorSummary(summary)
}

func gatewayForwardErrorSummary(value string) string {
	value = strings.Join(strings.Fields(strings.ToValidUTF8(value, "")), " ")
	if len(value) <= gatewayForwardErrorSummaryMaxBytes {
		return value
	}
	value = value[:gatewayForwardErrorSummaryMaxBytes]
	for !utf8.ValidString(value) {
		value = value[:len(value)-1]
	}
	return value
}
