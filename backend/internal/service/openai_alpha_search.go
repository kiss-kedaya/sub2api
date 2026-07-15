package service

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/tidwall/gjson"
)

const (
	chatgptCodexAlphaSearchURL   = "https://chatgpt.com/backend-api/codex/alpha/search"
	openAIPlatformAlphaSearchURL = "https://api.openai.com/v1/alpha/search"
)

// ForwardAlphaSearch proxies Codex standalone web search without binding the
// evolving alpha request or response schema.
//
// 返回值约定：仅当上游返回 2xx（一次真实成功的搜索）时返回非 nil 的
// *OpenAIForwardResult（WebSearchCalls=1，供按次计费）；上游错误被原样透传
// 给客户端时返回 (nil, nil)，不产生计费。
func (s *OpenAIGatewayService) ForwardAlphaSearch(ctx context.Context, c *gin.Context, account *Account, body []byte) (*OpenAIForwardResult, error) {
	if s == nil || c == nil || account == nil {
		return nil, fmt.Errorf("service, context, and account are required")
	}
	modelResult := gjson.GetBytes(body, "model")
	requestedModel := strings.TrimSpace(modelResult.String())
	if modelResult.Type != gjson.String || requestedModel == "" {
		return nil, fmt.Errorf("model is required")
	}

	upstreamModel := normalizeOpenAIModelForUpstream(account, account.GetMappedModel(requestedModel))
	if upstreamModel != "" && upstreamModel != requestedModel {
		body = ReplaceModelInBody(body, upstreamModel)
	}
	sanitizedBody, err := sanitizeOpenAIAlphaSearchBody(body)
	if err != nil {
		return nil, fmt.Errorf("sanitize alpha search request body: %w", err)
	}
	body = sanitizedBody

	token, _, err := s.GetAccessToken(ctx, account)
	if err != nil {
		return nil, err
	}

	proxyURL := ""
	if account.ProxyID != nil && account.Proxy != nil {
		proxyURL = account.Proxy.URL()
	}
	if err := s.ensureOpenAIAlphaSearchAuthMetadata(ctx, account, token, proxyURL); err != nil {
		return nil, err
	}

	req, err := s.buildOpenAIAlphaSearchRequest(ctx, c, account, body, token)
	if err != nil {
		return nil, err
	}

	upstreamStart := time.Now()
	resp, err := s.httpUpstream.Do(req, proxyURL, account.ID, account.Concurrency)
	SetOpsLatencyMs(c, OpsUpstreamLatencyMsKey, time.Since(upstreamStart).Milliseconds())
	if err != nil {
		return nil, s.handleOpenAIUpstreamTransportError(ctx, c, account, err, true)
	}
	defer func() { _ = resp.Body.Close() }()

	respBody, err := ReadUpstreamResponseBody(resp.Body, s.cfg, c, openAITooLargeError)
	if err != nil {
		return nil, fmt.Errorf("read alpha search response: %w", err)
	}

	if resp.StatusCode >= http.StatusBadRequest {
		upstreamMessage := sanitizeUpstreamErrorMessage(strings.TrimSpace(extractUpstreamErrorMessage(respBody)))
		if s.shouldFailoverOpenAIUpstreamResponse(resp.StatusCode, upstreamMessage, respBody) {
			resp.Body = io.NopCloser(bytes.NewReader(respBody))
			// alpha/search 是独立的工具端点，单次 401 不能证明账号的模型调用
			// 凭据全局失效。若沿用通用 401 逻辑，PAT 会因没有 refresh_token
			// 被永久标记为 error；历史导入且缺少 auth_mode 标记的 at- token 也会
			// 漏过 PAT 类型判断。这里仍允许本次请求换号，但不修改任何账号状态；
			// 真正的凭据失效由普通 Responses 请求或 whoami 校验判定。
			if shouldApplyOpenAIAlphaSearchAccountErrorSideEffects(resp.StatusCode) {
				s.handleFailoverSideEffects(ctx, resp, account, respBody, upstreamModel)
			}
			return nil, &UpstreamFailoverError{
				StatusCode:             resp.StatusCode,
				ResponseBody:           respBody,
				RetryableOnSameAccount: account.IsPoolMode() && account.IsPoolModeRetryableStatus(resp.StatusCode),
			}
		}
	}

	if !account.IsShadow() {
		s.UpdateCodexUsageSnapshotFromHeaders(ctx, account.ID, resp.Header)
	}
	writeOpenAIPassthroughResponseHeaders(c.Writer.Header(), resp.Header, s.responseHeaderFilter)
	contentType := resp.Header.Get("Content-Type")
	if contentType == "" {
		contentType = "application/json"
	}
	c.Data(resp.StatusCode, contentType, respBody)
	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		// 非 2xx（错误/重定向）已原样透传给客户端：不是一次成功的搜索，不计费。
		return nil, nil
	}
	return &OpenAIForwardResult{
		RequestID:      strings.TrimSpace(resp.Header.Get("x-request-id")),
		Model:          requestedModel,
		UpstreamModel:  upstreamModel,
		Duration:       time.Since(upstreamStart),
		WebSearchCalls: 1,
	}, nil
}

func (s *OpenAIGatewayService) buildOpenAIAlphaSearchRequest(ctx context.Context, c *gin.Context, account *Account, body []byte, token string) (*http.Request, error) {
	targetURL, err := s.openAIAlphaSearchURL(account)
	if err != nil {
		return nil, err
	}
	parsedURL, err := url.Parse(targetURL)
	if err != nil {
		return nil, fmt.Errorf("parse alpha search URL: %w", err)
	}
	if c != nil && c.Request != nil && c.Request.URL != nil {
		query := parsedURL.Query()
		for key, values := range c.Request.URL.Query() {
			for _, value := range values {
				query.Add(key, value)
			}
		}
		parsedURL.RawQuery = query.Encode()
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, parsedURL.String(), bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req = req.WithContext(WithHTTPUpstreamProfile(req.Context(), HTTPUpstreamProfileOpenAI))

	authHeaders, err := s.buildOpenAIAuthenticationHeaders(ctx, account, token)
	if err != nil {
		return nil, fmt.Errorf("build openai authentication headers: %w", err)
	}
	for key, values := range authHeaders {
		for _, value := range values {
			req.Header.Add(key, value)
		}
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")

	if account.Type == AccountTypeOAuth {
		req.Host = "chatgpt.com"
		if err := resolveAndSetOpenAIChatGPTAccountHeaders(ctx, s.accountRepo, req.Header, account); err != nil {
			return nil, fmt.Errorf("resolve chatgpt account headers: %w", err)
		}

		if turnMetadata := openAIAlphaSearchInboundHeader(c, "X-Codex-Turn-Metadata"); turnMetadata != "" {
			req.Header.Set("X-Codex-Turn-Metadata", turnMetadata)
		}
		if version := openAIAlphaSearchInboundHeader(c, "Version"); version != "" {
			req.Header.Set("Version", version)
		} else {
			req.Header.Set("Version", codexCLIVersion)
		}
		if originator := openAIAlphaSearchInboundHeader(c, "Originator"); originator != "" {
			req.Header.Set("Originator", originator)
		} else {
			req.Header.Set("Originator", "codex_cli_rs")
		}
		if customUA := account.GetOpenAIUserAgent(); customUA != "" {
			req.Header.Set("User-Agent", customUA)
		} else if userAgent := openAIAlphaSearchInboundHeader(c, "User-Agent"); userAgent != "" {
			req.Header.Set("User-Agent", userAgent)
		} else {
			req.Header.Set("User-Agent", codexCLIUserAgent)
		}
		if s.cfg != nil && s.cfg.Gateway.ForceCodexCLI {
			req.Header.Set("User-Agent", codexCLIUserAgent)
		}
		s.overrideBrowserUserAgent(ctx, account, req)
		enforceCodexIdentityHeaders(req.Header)
	}

	account.ApplyHeaderOverrides(req.Header)
	stripOpenAIAlphaSearchResponsesHeaders(req.Header)
	return req, nil
}

// stripOpenAIAlphaSearchResponsesHeaders 让独立搜索请求与官方 Codex
// SearchClient 的线协议保持一致。alpha/search 不是 /responses 的子请求：官方
// 客户端仅在 Provider/Auth 基础头之外附加 x-codex-turn-metadata，不发送
// OpenAI-Beta、会话隔离或 Responses Lite 状态头。originator 与 User-Agent
// 属于官方默认客户端头，必须保留。
//
// alpha/search 使用专用构造器生成官方 SearchClient 的最小线协议形态；
// 该函数作为最后一道防线，避免账号 header 覆写或后续改动重新带入
// Responses 专用头，使 PAT 的 alpha/search 被上游按错误认证路径处理。
func stripOpenAIAlphaSearchResponsesHeaders(headers http.Header) {
	if headers == nil {
		return
	}
	for _, key := range []string{
		"OpenAI-Beta",
		"Session_ID",
		"Conversation_ID",
		"X-Codex-Beta-Features",
		"X-Codex-Turn-State",
		responsesLiteHeaderKey,
	} {
		headers.Del(key)
	}
}

func openAIAlphaSearchInboundHeader(c *gin.Context, key string) string {
	if c == nil {
		return ""
	}
	return strings.TrimSpace(c.GetHeader(key))
}

var openAIAlphaSearchUnsupportedBodyFields = [...]string{
	// Codex alpha/search 是 SearchRequest 独立协议，不是 /responses 子请求。
	// 新版 Codex/第三方代理可能把 Responses 公共字段误带到搜索请求里；ChatGPT
	// alpha/search 会对这些字段返回 Unknown parameter（例如 prompt_cache_key）。
	"prompt_cache_key",
	"prompt_cache_retention",
}

func sanitizeOpenAIAlphaSearchBody(body []byte) ([]byte, error) {
	if len(body) == 0 {
		return body, nil
	}
	var obj map[string]json.RawMessage
	if err := json.Unmarshal(body, &obj); err != nil || obj == nil {
		return body, nil
	}
	changed := false
	for _, field := range openAIAlphaSearchUnsupportedBodyFields {
		if _, ok := obj[field]; ok {
			delete(obj, field)
			changed = true
		}
	}
	if !changed {
		return body, nil
	}
	out, err := json.Marshal(obj)
	if err != nil {
		return nil, err
	}
	return out, nil
}

func (s *OpenAIGatewayService) ensureOpenAIAlphaSearchAuthMetadata(ctx context.Context, account *Account, token string, proxyURL string) error {
	if s == nil || account == nil || !account.IsOpenAIPersonalAccessToken() {
		return nil
	}
	if strings.TrimSpace(account.GetChatGPTAccountID()) != "" {
		return nil
	}
	var oauthService *OpenAIOAuthService
	if s.openAITokenProvider != nil {
		oauthService = s.openAITokenProvider.openAIOAuthService
	}
	if oauthService == nil {
		return nil
	}
	tokenInfo, err := oauthService.ValidateCodexPersonalAccessToken(ctx, token, proxyURL)
	if err != nil {
		return fmt.Errorf("validate Codex PAT metadata for alpha/search: %w", err)
	}
	credentials := shallowCopyMap(account.Credentials)
	for key, value := range oauthService.BuildAccountCredentials(tokenInfo) {
		credentials[key] = value
	}
	credentials = NormalizeOpenAIPersonalAccessTokenCredentials(account, tokenInfo, credentials)
	account.Credentials = shallowCopyMap(credentials)
	if s.accountRepo != nil {
		if err := persistAccountCredentials(ctx, s.accountRepo, account, credentials); err != nil {
			return fmt.Errorf("persist Codex PAT metadata for alpha/search: %w", err)
		}
	}
	return nil
}

func shouldApplyOpenAIAlphaSearchAccountErrorSideEffects(statusCode int) bool {
	return statusCode != http.StatusUnauthorized
}

func (s *OpenAIGatewayService) openAIAlphaSearchURL(account *Account) (string, error) {
	if account == nil {
		return "", fmt.Errorf("account is required")
	}
	switch account.Type {
	case AccountTypeOAuth:
		return chatgptCodexAlphaSearchURL, nil
	case AccountTypeAPIKey:
		baseURL := account.GetOpenAIBaseURL()
		if baseURL == "" {
			return openAIPlatformAlphaSearchURL, nil
		}
		validatedURL, err := s.validateUpstreamBaseURL(baseURL)
		if err != nil {
			return "", err
		}
		return buildOpenAIEndpointURL(validatedURL, "/v1/alpha/search"), nil
	default:
		return "", fmt.Errorf("unsupported OpenAI account type: %s", account.Type)
	}
}
