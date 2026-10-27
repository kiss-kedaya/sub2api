// Adapted from ranxi2001/sub2api @ 9620d315, LGPL-3.0-or-later.
package handler

import (
	"mime"
	"net/http"
	"strings"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
)

// MiniMaxH3Videos exposes the custom relay's JSON OpenAI Videos API.
func (h *OpenAIGatewayHandler) MiniMaxH3Videos(c *gin.Context) {
	endpoint, taskID := service.MiniMaxH3EndpointCreate, ""
	if c.Request.Method != http.MethodPost {
		endpoint = service.MiniMaxH3EndpointStatus
		taskID = service.MiniMaxH3TaskKey(c.Param("request_id"))
		if strings.HasSuffix(c.Request.URL.Path, "/content") {
			endpoint = service.MiniMaxH3EndpointContent
		}
	} else {
		mediaType, _, err := mime.ParseMediaType(c.GetHeader("Content-Type"))
		if err != nil || mediaType != "application/json" {
			h.errorResponse(c, http.StatusUnsupportedMediaType, "invalid_request_error", "MiniMax-H3 requires application/json")
			return
		}
	}
	setActualUpstreamEndpoint(c, "/v1/videos")
	h.handleGrokMedia(c, endpoint, taskID)
}
