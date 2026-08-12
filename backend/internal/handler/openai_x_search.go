package handler

import (
	"encoding/json"
	"strings"

	"github.com/Wei-Shaw/sub2api/internal/pkg/xai"
	"github.com/gin-gonic/gin"
)

type grokStandaloneSearchRequest struct {
	Query                    string   `json:"query"`
	Input                    string   `json:"input"`
	MaxResults               *int     `json:"max_results"`
	AllowedXHandles          []string `json:"allowed_x_handles"`
	ExcludedXHandles         []string `json:"excluded_x_handles"`
	FromDate                 string   `json:"from_date"`
	ToDate                   string   `json:"to_date"`
	EnableImageUnderstanding *bool    `json:"enable_image_understanding"`
	EnableVideoUnderstanding *bool    `json:"enable_video_understanding"`
}

// XSearch marks the standalone endpoint so WebSearch can use native x_search
// while retaining its dedicated per-call billing contract.
func (h *GatewayHandler) XSearch(c *gin.Context) {
	c.Set("grok_x_search_endpoint", true)
	h.WebSearch(c)
}

func resolveGrokStandaloneSearchModel() string {
	return xai.ResolveDefaultTextModel(xai.RuntimeModelMappingOptions().DefaultText)
}

func buildGrokXSearchResponsesBody(req grokStandaloneSearchRequest, model string) ([]byte, error) {
	input := strings.TrimSpace(req.Query)
	if input == "" {
		input = strings.TrimSpace(req.Input)
	}
	tool := map[string]any{"type": "x_search"}
	if len(req.AllowedXHandles) > 0 {
		tool["allowed_x_handles"] = req.AllowedXHandles
	}
	if len(req.ExcludedXHandles) > 0 {
		tool["excluded_x_handles"] = req.ExcludedXHandles
	}
	if strings.TrimSpace(req.FromDate) != "" {
		tool["from_date"] = strings.TrimSpace(req.FromDate)
	}
	if strings.TrimSpace(req.ToDate) != "" {
		tool["to_date"] = strings.TrimSpace(req.ToDate)
	}
	if req.EnableImageUnderstanding != nil {
		tool["enable_image_understanding"] = *req.EnableImageUnderstanding
	}
	if req.EnableVideoUnderstanding != nil {
		tool["enable_video_understanding"] = *req.EnableVideoUnderstanding
	}
	return json.Marshal(map[string]any{
		"model":       xai.ResolveDefaultTextModel(model),
		"input":       input,
		"tools":       []map[string]any{tool},
		"tool_choice": "required",
		"store":       false,
		"stream":      false,
	})
}
