package handler

import (
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/tidwall/gjson"
)

const invalidStreamFieldTypeMessage = "invalid stream field type"

func parseOpenAICompatibleStream(c *gin.Context, body []byte) (bool, bool) {
	streamResult := service.RequestBodyTop(c, body).Get("stream")
	if streamResult.Exists() && streamResult.Type != gjson.True && streamResult.Type != gjson.False {
		return false, false
	}
	return streamResult.Bool(), true
}
