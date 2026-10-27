package service

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"unicode/utf8"

	"github.com/gin-gonic/gin"
	"github.com/tidwall/gjson"
)

const (
	deepSeekHostedFetchMaxContentBytes  = 64 << 10
	deepSeekHostedFetchMaxResponseBytes = 1 << 20
)

// Fetch is executed by the configured independent tool provider, never by a
// new HTTP client to the supplied URL. Keep the same credentials, cancellation,
// timeout and success-only billing rules as the standalone Fetch endpoint.
func (s *OpenAIGatewayService) runDeepSeekHostedFetch(ctx context.Context, c *gin.Context, account *Account, group *Group, state *deepSeekHostedSearchState, call *deepSeekHostedCall) string {
	if ctx.Err() != nil {
		return `{"error":"request canceled"}`
	}
	args := gjson.Parse(call.arguments)
	if !gjson.Valid(call.arguments) || !args.IsObject() {
		return `{"error":"fetch requires a public HTTP or HTTPS url"}`
	}
	validFields := true
	args.ForEach(func(key, _ gjson.Result) bool {
		validFields = key.String() == "url"
		return validFields
	})
	rawURL := args.Get("url")
	pageURL := strings.TrimSpace(rawURL.String())
	if !validFields || rawURL.Type != gjson.String || utf8.RuneCountInString(pageURL) > 8192 || !deepSeekHostedSearchPublicURL(pageURL) {
		return `{"error":"fetch requires a public HTTP or HTTPS url without credentials, at most 8192 characters"}`
	}
	parsed, err := url.Parse(pageURL)
	if err != nil {
		return `{"error":"fetch requires a valid page url"}`
	}
	if len(state.domains) > 0 {
		allowed := false
		for _, domain := range state.domains {
			if strings.EqualFold(parsed.Hostname(), domain) || strings.HasSuffix(strings.ToLower(parsed.Hostname()), "."+domain) {
				allowed = true
				break
			}
		}
		if !allowed {
			return `{"error":"fetch url is outside the allowed domains"}`
		}
	}
	call.url = pageURL
	if state.stream {
		if err := state.event(map[string]any{"type": "response.web_search_call.searching", "item_id": call.searchID, "output_index": call.index}); err != nil {
			return `{"error":"client disconnected"}`
		}
	}
	payload, _ := json.Marshal(map[string]string{"url": pageURL})
	fetchCtx, cancel := context.WithTimeout(ctx, deepSeekSearchToolTimeout)
	defer cancel()
	recorder := httptest.NewRecorder()
	shadow := c.Copy()
	shadow.Request = c.Request.Clone(fetchCtx)
	shadow.Writer = &deepSeekHostedRecorder{ResponseRecorder: recorder}
	result, err := s.forwardDeepSeekTools(fetchCtx, shadow, account, group, DeepSeekToolsPathFetch, "", payload, deepSeekHostedFetchMaxResponseBytes)
	if err != nil || recorder.Code != http.StatusOK {
		return `{"error":"fetch provider request failed"}`
	}
	if result != nil && result.WebSearchCalls > 0 {
		state.searchCount += result.WebSearchCalls
	}
	if !deepSeekToolsHasBillableResult(DeepSeekToolsPathFetch, recorder.Body.Bytes()) {
		return `{"error":"fetch provider returned no page content"}`
	}
	content := gjson.GetBytes(recorder.Body.Bytes(), "content").String()
	truncated := len(content) > deepSeekHostedFetchMaxContentBytes
	if truncated {
		end := deepSeekHostedFetchMaxContentBytes
		for end > 0 && !utf8.RuneStart(content[end]) {
			end--
		}
		content = content[:end]
	}
	call.status = "completed"
	encoded, _ := json.Marshal(map[string]any{
		"url": pageURL, "title": truncateString(gjson.GetBytes(recorder.Body.Bytes(), "title").String(), 512),
		"content": content, "truncated": truncated,
	})
	return string(encoded)
}
