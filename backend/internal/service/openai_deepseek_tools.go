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
)

// DeepSeek standalone tool endpoints. These are raw JSON endpoints authenticated
// with the same Bearer key as the model API — not chat/completions shapes — so
// they must not be routed through any protocol conversion.
const (
	DeepSeekToolsPathSearch = "/v1/tools/search"
	DeepSeekToolsPathFetch  = "/v1/tools/fetch"
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

// ForwardDeepSeekTools proxies a standalone DeepSeek Search/Fetch call with the
// selected account credential. Like ForwardAlphaSearch it deliberately does not
// bind the evolving request/response schema: the client body is forwarded as-is
// and the upstream response is passed through untouched.
//
// 返回值约定与 ForwardAlphaSearch 一致：仅当上游返回 2xx（一次真实成功的
// 搜索 / 抓取）时返回非 nil 的 *OpenAIForwardResult（WebSearchCalls=1，供按次
// 计费）；上游错误被原样透传给客户端时返回 (nil, nil)，不产生计费。
func (s *OpenAIGatewayService) ForwardDeepSeekTools(
	ctx context.Context, c *gin.Context, account *Account, group *Group, path string, modelLabel string, body []byte,
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

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, baseURL+path, bytes.NewReader(body))
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
		return nil, s.handleOpenAIUpstreamTransportError(ctx, c, account, err, true)
	}
	defer func() { _ = resp.Body.Close() }()

	respBody, err := ReadUpstreamResponseBody(resp.Body, s.cfg, c, openAITooLargeError)
	if err != nil {
		return nil, fmt.Errorf("read deepseek tools response: %w", err)
	}

	if resp.StatusCode >= http.StatusBadRequest {
		upstreamMessage := sanitizeUpstreamErrorMessage(strings.TrimSpace(extractUpstreamErrorMessage(respBody)))
		if s.shouldFailoverOpenAIUpstreamResponse(account, resp.StatusCode, upstreamMessage, respBody) {
			resp.Body = io.NopCloser(bytes.NewReader(respBody))
			retryableOnSameAccount := PoolModeSameAccountRetry(account, resp.StatusCode, resp.Header, respBody)
			return nil, &UpstreamFailoverError{
				StatusCode:            resp.StatusCode,
				ResponseBody:          respBody,
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
	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		// 非 2xx（错误/重定向）已原样透传给客户端：不是一次成功的调用，不计费。
		return nil, nil
	}
	return &OpenAIForwardResult{
		RequestID:       strings.TrimSpace(resp.Header.Get("x-request-id")),
		UpstreamHeaders: resp.Header,
		Model:           modelLabel,
		Duration:        time.Since(upstreamStart),
		WebSearchCalls:  1,
	}, nil
}
