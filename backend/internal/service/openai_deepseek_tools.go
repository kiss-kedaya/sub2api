package service

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/tidwall/gjson"
)

// DeepSeek standalone tool endpoints. These are raw JSON endpoints authenticated
// with the same Bearer key as the model API — not chat/completions shapes — so
// they must not be routed through any protocol conversion.
const (
	DeepSeekToolsPathSearch  = "/v1/tools/search"
	DeepSeekToolsPathFetch   = "/v1/tools/fetch"
	DeepSeekToolsLabelSearch = "deepseek-tools-search"
	DeepSeekToolsLabelFetch  = "deepseek-tools-fetch"
)

// derefString 安全解引用可空字符串。
func derefString(value *string) string {
	if value == nil {
		return ""
	}
	return *value
}

// HasIndependentDeepSeekSearchProvider reports whether Search/Fetch uses any
// group-specific connection setting. A failure of that provider must not affect
// the selected model account's health or retry the same provider on other accounts.
func HasIndependentDeepSeekSearchProvider(group *Group) bool {
	return group != nil && (strings.TrimSpace(derefString(group.SearchProviderBaseURL)) != "" ||
		strings.TrimSpace(derefString(group.SearchProviderAPIKey)) != "")
}

// ForwardDeepSeekTools proxies a standalone DeepSeek Search/Fetch call with the
// selected account credential. Like ForwardAlphaSearch it deliberately does not
// bind the evolving request/response schema: the client body is forwarded as-is
// and the upstream response is passed through untouched.
//
// 仅当上游返回 HTTP 200 且实际包含搜索结果 / 抓取正文时返回非 nil 的
// *OpenAIForwardResult（WebSearchCalls=1）。空结果、无效响应和错误不计费。
func (s *OpenAIGatewayService) ForwardDeepSeekTools(
	ctx context.Context, c *gin.Context, account *Account, group *Group, path string, modelLabel string, body []byte,
) (*OpenAIForwardResult, error) {
	return s.forwardDeepSeekTools(ctx, c, account, group, path, modelLabel, body, 0)
}

func (s *OpenAIGatewayService) forwardDeepSeekTools(
	ctx context.Context, c *gin.Context, account *Account, group *Group, path string, modelLabel string, body []byte, responseLimit int64,
) (*OpenAIForwardResult, error) {
	if s == nil || c == nil || account == nil {
		return nil, fmt.Errorf("service, context, and account are required")
	}
	if path != DeepSeekToolsPathSearch && path != DeepSeekToolsPathFetch {
		return nil, fmt.Errorf("deepseek tools: unsupported path %q", path)
	}
	baseURL := strings.TrimRight(strings.TrimSpace(account.GetOpenAIFormatBaseURL()), "/")
	credential := strings.TrimSpace(account.GetCNAPIKey())
	// 分组可以单独配一套搜索服务：模型走别家账号时，搜索仍可由指定的
	// API + key 提供，不与模型账号的额度混用。未配置时回落到账号自带值。
	if group != nil {
		if v := strings.TrimRight(strings.TrimSpace(derefString(group.SearchProviderBaseURL)), "/"); v != "" {
			baseURL = v
		}
		if v := strings.TrimSpace(derefString(group.SearchProviderAPIKey)); v != "" {
			credential = v
		}
	}
	if baseURL == "" {
		return nil, fmt.Errorf("deepseek tools: account %d has no base url", account.ID)
	}
	if credential == "" {
		return nil, fmt.Errorf("deepseek tools: account %d has no api key", account.ID)
	}
	// 分组可以自填搜索服务地址，必须拒绝私网/回环目标：否则管理员（或被
	// 篡改的分组配置）能把这条路径变成任意 host:port 的探测原语。
	if group != nil && derefString(group.SearchProviderBaseURL) != "" && !accountTestAllowPrivateTargets.Load() {
		if blocked, err := isPrivateOrLoopbackHost(ctx, hostOfURL(baseURL)); err == nil && blocked {
			return nil, errTestTargetNotAllowed
		}
	}
	if modelLabel == "" {
		modelLabel = DeepSeekToolsLabelSearch
		if path == DeepSeekToolsPathFetch {
			modelLabel = DeepSeekToolsLabelFetch
		}
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, buildOpenAIEndpointURL(baseURL, path), bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("deepseek tools: build request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Authorization", "Bearer "+credential)

	proxyURL := ""
	if account.ProxyID != nil && account.Proxy != nil {
		proxyURL = account.Proxy.URL()
	}

	upstreamStart := time.Now()
	resp, err := s.doOpenAIUpstream(req, proxyURL, account)
	if err != nil {
		if HasIndependentDeepSeekSearchProvider(group) {
			if ctx.Err() != nil {
				return nil, ctx.Err()
			}
			setOpsUpstreamError(c, 0, sanitizeUpstreamErrorMessage(err.Error()), "")
			c.JSON(http.StatusBadGateway, gin.H{"error": gin.H{
				"type": "upstream_error", "message": "DeepSeek search provider request failed",
			}})
			return nil, nil
		}
		return nil, s.handleOpenAIUpstreamTransportError(ctx, c, account, err, true)
	}
	defer func() { _ = resp.Body.Close() }()

	var reader io.Reader = resp.Body
	if responseLimit > 0 {
		reader = io.LimitReader(reader, responseLimit+1)
	}
	respBody, err := ReadUpstreamResponseBody(reader, s.cfg, c, openAITooLargeError)
	if err != nil {
		return nil, fmt.Errorf("read deepseek tools response: %w", err)
	}
	if responseLimit > 0 && int64(len(respBody)) > responseLimit {
		return nil, fmt.Errorf("deepseek hosted tool response exceeds size limit")
	}

	if resp.StatusCode >= http.StatusBadRequest && !HasIndependentDeepSeekSearchProvider(group) {
		upstreamMessage := sanitizeUpstreamErrorMessage(strings.TrimSpace(extractUpstreamErrorMessage(respBody)))
		if s.shouldFailoverOpenAIUpstreamResponse(account, resp.StatusCode, upstreamMessage, respBody) {
			resp.Body = io.NopCloser(bytes.NewReader(respBody))
			retryableOnSameAccount := PoolModeSameAccountRetry(account, resp.StatusCode, resp.Header, respBody)
			return nil, &UpstreamFailoverError{
				StatusCode:             resp.StatusCode,
				ResponseBody:           respBody,
				RetryableOnSameAccount: retryableOnSameAccount,
			}
		}
	}

	writeOpenAIPassthroughResponseHeaders(c.Writer.Header(), resp.Header, s.responseHeaderFilter)
	contentType := resp.Header.Get("Content-Type")
	if contentType == "" {
		contentType = "application/json"
	}
	c.Data(resp.StatusCode, contentType, respBody)
	if resp.StatusCode != http.StatusOK || !deepSeekToolsHasBillableResult(path, respBody) {
		// 官方仅对 200 且有实际结果的请求计费。仍将上游响应原样交给客户端。
		return nil, nil
	}
	return &OpenAIForwardResult{
		RequestID:        strings.TrimSpace(resp.Header.Get("x-request-id")),
		UpstreamHeaders:  resp.Header,
		Model:            modelLabel,
		Duration:         time.Since(upstreamStart),
		WebSearchCalls:   1,
		FixedToolBilling: true,
	}, nil
}

func deepSeekToolsHasBillableResult(path string, body []byte) bool {
	if !gjson.ValidBytes(body) {
		return false
	}
	if path == DeepSeekToolsPathSearch {
		results := gjson.GetBytes(body, "results")
		return results.IsArray() && len(results.Array()) > 0
	}
	content := gjson.GetBytes(body, "content")
	return path == DeepSeekToolsPathFetch && content.Type == gjson.String && strings.TrimSpace(content.String()) != ""
}
