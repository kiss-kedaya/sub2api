package service

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"strings"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/antigravity"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

const (
	antigravityStickySessionTTL = time.Hour
	antigravityMaxRetries       = 5
	antigravityRetryBaseDelay   = 1 * time.Second
	antigravityRetryMaxDelay    = 16 * time.Second
)

// getSessionID 从 gin.Context 获取 session_id（用于日志追踪）
func getSessionID(c *gin.Context) string {
	if c == nil {
		return ""
	}
	return c.GetHeader("session_id")
}

// logPrefix 生成统一的日志前缀
func logPrefix(sessionID, accountName string) string {
	if sessionID != "" {
		return fmt.Sprintf("[antigravity-Forward] session=%s account=%s", sessionID, accountName)
	}
	return fmt.Sprintf("[antigravity-Forward] account=%s", accountName)
}

// Antigravity 直接支持的模型（精确匹配透传）
var antigravitySupportedModels = map[string]bool{
	"claude-opus-4-5-thinking":   true,
	"claude-sonnet-4-5":          true,
	"claude-sonnet-4-5-thinking": true,
	"gemini-2.5-flash":           true,
	"gemini-2.5-flash-lite":      true,
	"gemini-2.5-flash-thinking":  true,
	"gemini-3-flash":             true,
	"gemini-3-pro-low":           true,
	"gemini-3-pro-high":          true,
	"gemini-3-pro-image":         true,
}

// Antigravity 前缀映射表（按前缀长度降序排列，确保最长匹配优先）
// 用于处理模型版本号变化（如 -20251111, -thinking, -preview 等后缀）
var antigravityPrefixMapping = []struct {
	prefix string
	target string
}{
	// 长前缀优先
	{"gemini-3-pro-image", "gemini-3-pro-image"}, // gemini-3-pro-image-preview 等
	{"claude-3-5-sonnet", "claude-sonnet-4-5"},   // 旧版 claude-3-5-sonnet-xxx
	{"claude-sonnet-4-5", "claude-sonnet-4-5"},   // claude-sonnet-4-5-xxx
	{"claude-haiku-4-5", "claude-sonnet-4-5"},    // claude-haiku-4-5-xxx → sonnet
	{"claude-opus-4-5", "claude-opus-4-5-thinking"},
	{"claude-3-haiku", "claude-sonnet-4-5"}, // 旧版 claude-3-haiku-xxx → sonnet
	{"claude-sonnet-4", "claude-sonnet-4-5"},
	{"claude-haiku-4", "claude-sonnet-4-5"}, // → sonnet
	{"claude-opus-4", "claude-opus-4-5-thinking"},
	{"gemini-3-pro", "gemini-3-pro-high"}, // gemini-3-pro, gemini-3-pro-preview 等
}

// AntigravityGatewayService 处理 Antigravity 平台的 API 转发
type AntigravityGatewayService struct {
	accountRepo      AccountRepository
	tokenProvider    *AntigravityTokenProvider
	rateLimitService *RateLimitService
	httpUpstream     HTTPUpstream
}

func NewAntigravityGatewayService(
	accountRepo AccountRepository,
	_ GatewayCache,
	tokenProvider *AntigravityTokenProvider,
	rateLimitService *RateLimitService,
	httpUpstream HTTPUpstream,
) *AntigravityGatewayService {
	return &AntigravityGatewayService{
		accountRepo:      accountRepo,
		tokenProvider:    tokenProvider,
		rateLimitService: rateLimitService,
		httpUpstream:     httpUpstream,
	}
}

// GetTokenProvider 返回 token provider
func (s *AntigravityGatewayService) GetTokenProvider() *AntigravityTokenProvider {
	return s.tokenProvider
}

// getMappedModel 获取映射后的模型名
// 逻辑：账户映射 → 直接支持透传 → 前缀映射 → gemini透传 → 默认值
func (s *AntigravityGatewayService) getMappedModel(account *Account, requestedModel string) string {
	// 1. 账户级映射（用户自定义优先）
	if mapped := account.GetMappedModel(requestedModel); mapped != requestedModel {
		return mapped
	}

	// 2. 直接支持的模型透传
	if antigravitySupportedModels[requestedModel] {
		return requestedModel
	}

	// 3. 前缀映射（处理版本号变化，如 -20251111, -thinking, -preview）
	for _, pm := range antigravityPrefixMapping {
		if strings.HasPrefix(requestedModel, pm.prefix) {
			return pm.target
		}
	}

	// 4. Gemini 模型透传（未匹配到前缀的 gemini 模型）
	if strings.HasPrefix(requestedModel, "gemini-") {
		return requestedModel
	}

	// 5. 默认值
	return "claude-sonnet-4-5"
}

// IsModelSupported 检查模型是否被支持
// 所有 claude- 和 gemini- 前缀的模型都能通过映射或透传支持
func (s *AntigravityGatewayService) IsModelSupported(requestedModel string) bool {
	return strings.HasPrefix(requestedModel, "claude-") ||
		strings.HasPrefix(requestedModel, "gemini-")
}

// TestConnectionResult 测试连接结果
type TestConnectionResult struct {
	Text        string // 响应文本
	MappedModel string // 实际使用的模型
}

// TestConnection 测试 Antigravity 账号连接（非流式，无重试、无计费）
// 支持 Claude 和 Gemini 两种协议，根据 modelID 前缀自动选择
func (s *AntigravityGatewayService) TestConnection(ctx context.Context, account *Account, modelID string) (*TestConnectionResult, error) {
	// 获取 token
	if s.tokenProvider == nil {
		return nil, errors.New("antigravity token provider not configured")
	}
	accessToken, err := s.tokenProvider.GetAccessToken(ctx, account)
	if err != nil {
		return nil, fmt.Errorf("获取 access_token 失败: %w", err)
	}

	// 获取 project_id（部分账户类型可能没有）
	projectID := strings.TrimSpace(account.GetCredential("project_id"))

	// 模型映射
	mappedModel := s.getMappedModel(account, modelID)

	// 构建请求体
	var requestBody []byte
	if strings.HasPrefix(modelID, "gemini-") {
		// Gemini 模型：直接使用 Gemini 格式
		requestBody, err = s.buildGeminiTestRequest(projectID, mappedModel)
	} else {
		// Claude 模型：使用协议转换
		requestBody, err = s.buildClaudeTestRequest(projectID, mappedModel)
	}
	if err != nil {
		return nil, fmt.Errorf("构建请求失败: %w", err)
	}

	// 构建 HTTP 请求（非流式）
	req, err := antigravity.NewAPIRequest(ctx, "generateContent", accessToken, requestBody)
	if err != nil {
		return nil, err
	}

	// 代理 URL
	proxyURL := ""
	if account.ProxyID != nil && account.Proxy != nil {
		proxyURL = account.Proxy.URL()
	}

	// 发送请求
	resp, err := s.httpUpstream.Do(req, proxyURL, account.ID, account.Concurrency)
	if err != nil {
		return nil, fmt.Errorf("请求失败: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	// 读取响应
	respBody, err := io.ReadAll(io.LimitReader(resp.Body, 2<<20))
	if err != nil {
		return nil, fmt.Errorf("读取响应失败: %w", err)
	}

	if resp.StatusCode >= 400 {
		return nil, fmt.Errorf("API 返回 %d: %s", resp.StatusCode, string(respBody))
	}

	// 解包 v1internal 响应
	unwrapped, err := s.unwrapV1InternalResponse(respBody)
	if err != nil {
		return nil, fmt.Errorf("解包响应失败: %w", err)
	}

	// 提取响应文本
	text := extractGeminiResponseText(unwrapped)

	return &TestConnectionResult{
		Text:        text,
		MappedModel: mappedModel,
	}, nil
}

// buildGeminiTestRequest 构建 Gemini 格式测试请求
func (s *AntigravityGatewayService) buildGeminiTestRequest(projectID, model string) ([]byte, error) {
	payload := map[string]any{
		"contents": []map[string]any{
			{
				"role": "user",
				"parts": []map[string]any{
					{"text": "hi"},
				},
			},
		},
	}
	payloadBytes, _ := json.Marshal(payload)
	return s.wrapV1InternalRequest(projectID, model, payloadBytes)
}

// buildClaudeTestRequest 构建 Claude 格式测试请求并转换为 Gemini 格式
func (s *AntigravityGatewayService) buildClaudeTestRequest(projectID, mappedModel string) ([]byte, error) {
	claudeReq := &antigravity.ClaudeRequest{
		Model: mappedModel,
		Messages: []antigravity.ClaudeMessage{
			{
				Role:    "user",
				Content: json.RawMessage(`"hi"`),
			},
		},
		MaxTokens: 1024,
		Stream:    false,
	}
	return antigravity.TransformClaudeToGemini(claudeReq, projectID, mappedModel)
}

// extractGeminiResponseText 从 Gemini 响应中提取文本
func extractGeminiResponseText(respBody []byte) string {
	var resp map[string]any
	if err := json.Unmarshal(respBody, &resp); err != nil {
		return ""
	}

	candidates, ok := resp["candidates"].([]any)
	if !ok || len(candidates) == 0 {
		return ""
	}

	candidate, ok := candidates[0].(map[string]any)
	if !ok {
		return ""
	}

	content, ok := candidate["content"].(map[string]any)
	if !ok {
		return ""
	}

	parts, ok := content["parts"].([]any)
	if !ok {
		return ""
	}

	var texts []string
	for _, part := range parts {
		if partMap, ok := part.(map[string]any); ok {
			if text, ok := partMap["text"].(string); ok && text != "" {
				texts = append(texts, text)
			}
		}
	}

	return strings.Join(texts, "")
}

// wrapV1InternalRequest 包装请求为 v1internal 格式
func (s *AntigravityGatewayService) wrapV1InternalRequest(projectID, model string, originalBody []byte) ([]byte, error) {
	var request any
	if err := json.Unmarshal(originalBody, &request); err != nil {
		return nil, fmt.Errorf("解析请求体失败: %w", err)
	}

	wrapped := map[string]any{
		"project":     projectID,
		"requestId":   "agent-" + uuid.New().String(),
		"userAgent":   "sub2api",
		"requestType": "agent",
		"model":       model,
		"request":     request,
	}

	return json.Marshal(wrapped)
}

// unwrapV1InternalResponse 解包 v1internal 响应
func (s *AntigravityGatewayService) unwrapV1InternalResponse(body []byte) ([]byte, error) {
	var outer map[string]any
	if err := json.Unmarshal(body, &outer); err != nil {
		return nil, err
	}

	if resp, ok := outer["response"]; ok {
		return json.Marshal(resp)
	}

	return body, nil
}

// Forward 转发 Claude 协议请求（Claude → Gemini 转换）
func (s *AntigravityGatewayService) Forward(ctx context.Context, c *gin.Context, account *Account, body []byte) (*ForwardResult, error) {
	startTime := time.Now()
	sessionID := getSessionID(c)
	prefix := logPrefix(sessionID, account.Name)

	// 解析 Claude 请求
	var claudeReq antigravity.ClaudeRequest
	if err := json.Unmarshal(body, &claudeReq); err != nil {
		return nil, fmt.Errorf("parse claude request: %w", err)
	}
	if strings.TrimSpace(claudeReq.Model) == "" {
		return nil, fmt.Errorf("missing model")
	}

	originalModel := claudeReq.Model
	mappedModel := s.getMappedModel(account, claudeReq.Model)

	// 获取 access_token
	if s.tokenProvider == nil {
		return nil, errors.New("antigravity token provider not configured")
	}
	accessToken, err := s.tokenProvider.GetAccessToken(ctx, account)
	if err != nil {
		return nil, fmt.Errorf("获取 access_token 失败: %w", err)
	}

	// 获取 project_id（部分账户类型可能没有）
	projectID := strings.TrimSpace(account.GetCredential("project_id"))

	// 代理 URL
	proxyURL := ""
	if account.ProxyID != nil && account.Proxy != nil {
		proxyURL = account.Proxy.URL()
	}

	// 转换 Claude 请求为 Gemini 格式
	geminiBody, err := antigravity.TransformClaudeToGemini(&claudeReq, projectID, mappedModel)
	if err != nil {
		return nil, fmt.Errorf("transform request: %w", err)
	}

	// 构建上游 action
	action := "generateContent"
	if claudeReq.Stream {
		action = "streamGenerateContent?alt=sse"
	}

	// 重试循环
	var resp *http.Response
	for attempt := 1; attempt <= antigravityMaxRetries; attempt++ {
		upstreamReq, err := antigravity.NewAPIRequest(ctx, action, accessToken, geminiBody)
		if err != nil {
			return nil, err
		}

		resp, err = s.httpUpstream.Do(upstreamReq, proxyURL, account.ID, account.Concurrency)
		if err != nil {
			if attempt < antigravityMaxRetries {
				log.Printf("%s status=request_failed retry=%d/%d error=%v", prefix, attempt, antigravityMaxRetries, err)
				sleepAntigravityBackoff(attempt)
				continue
			}
			log.Printf("%s status=request_failed retries_exhausted error=%v", prefix, err)
			return nil, s.writeClaudeError(c, http.StatusBadGateway, "upstream_error", "Upstream request failed after retries")
		}

		if resp.StatusCode >= 400 && s.shouldRetryUpstreamError(resp.StatusCode) {
			respBody, _ := io.ReadAll(io.LimitReader(resp.Body, 2<<20))
			_ = resp.Body.Close()

			if attempt < antigravityMaxRetries {
				log.Printf("%s status=%d retry=%d/%d", prefix, resp.StatusCode, attempt, antigravityMaxRetries)
				sleepAntigravityBackoff(attempt)
				continue
			}
			// 所有重试都失败，标记限流状态
			if resp.StatusCode == 429 {
				s.handleUpstreamError(ctx, prefix, account, resp.StatusCode, resp.Header, respBody)
			}
			// 最后一次尝试也失败
			resp = &http.Response{
				StatusCode: resp.StatusCode,
				Header:     resp.Header.Clone(),
				Body:       io.NopCloser(bytes.NewReader(respBody)),
			}
			break
		}

		break
	}
	defer func() { _ = resp.Body.Close() }()

	// 处理错误响应
	if resp.StatusCode >= 400 {
		respBody, _ := io.ReadAll(io.LimitReader(resp.Body, 2<<20))
		s.handleUpstreamError(ctx, prefix, account, resp.StatusCode, resp.Header, respBody)

		if s.shouldFailoverUpstreamError(resp.StatusCode) {
			return nil, &UpstreamFailoverError{StatusCode: resp.StatusCode}
		}

		return nil, s.writeMappedClaudeError(c, resp.StatusCode, respBody)
	}

	requestID := resp.Header.Get("x-request-id")
	if requestID != "" {
		c.Header("x-request-id", requestID)
	}

	var usage *ClaudeUsage
	var firstTokenMs *int
	if claudeReq.Stream {
		streamRes, err := s.handleClaudeStreamingResponse(c, resp, startTime, originalModel)
		if err != nil {
			return nil, err
		}
		usage = streamRes.usage
		firstTokenMs = streamRes.firstTokenMs
	} else {
		usage, err = s.handleClaudeNonStreamingResponse(c, resp, originalModel)
		if err != nil {
			return nil, err
		}
	}

	return &ForwardResult{
		RequestID:    requestID,
		Usage:        *usage,
		Model:        originalModel, // 使用原始模型用于计费和日志
		Stream:       claudeReq.Stream,
		Duration:     time.Since(startTime),
		FirstTokenMs: firstTokenMs,
	}, nil
}

// ForwardGemini 转发 Gemini 协议请求
func (s *AntigravityGatewayService) ForwardGemini(ctx context.Context, c *gin.Context, account *Account, originalModel string, action string, stream bool, body []byte) (*ForwardResult, error) {
	startTime := time.Now()
	sessionID := getSessionID(c)
	prefix := logPrefix(sessionID, account.Name)

	if strings.TrimSpace(originalModel) == "" {
		return nil, s.writeGoogleError(c, http.StatusBadRequest, "Missing model in URL")
	}
	if strings.TrimSpace(action) == "" {
		return nil, s.writeGoogleError(c, http.StatusBadRequest, "Missing action in URL")
	}
	if len(body) == 0 {
		return nil, s.writeGoogleError(c, http.StatusBadRequest, "Request body is empty")
	}

	switch action {
	case "generateContent", "streamGenerateContent":
		// ok
	case "countTokens":
		// 直接返回空值，不透传上游
		c.JSON(http.StatusOK, map[string]any{"totalTokens": 0})
		return &ForwardResult{
			RequestID:    "",
			Usage:        ClaudeUsage{},
			Model:        originalModel,
			Stream:       false,
			Duration:     time.Since(time.Now()),
			FirstTokenMs: nil,
		}, nil
	default:
		return nil, s.writeGoogleError(c, http.StatusNotFound, "Unsupported action: "+action)
	}

	mappedModel := s.getMappedModel(account, originalModel)

	// 获取 access_token
	if s.tokenProvider == nil {
		return nil, errors.New("antigravity token provider not configured")
	}
	accessToken, err := s.tokenProvider.GetAccessToken(ctx, account)
	if err != nil {
		return nil, fmt.Errorf("获取 access_token 失败: %w", err)
	}

	// 获取 project_id（部分账户类型可能没有）
	projectID := strings.TrimSpace(account.GetCredential("project_id"))

	// 代理 URL
	proxyURL := ""
	if account.ProxyID != nil && account.Proxy != nil {
		proxyURL = account.Proxy.URL()
	}

	// 包装请求
	wrappedBody, err := s.wrapV1InternalRequest(projectID, mappedModel, body)
	if err != nil {
		return nil, err
	}

	// 构建上游 action
	upstreamAction := action
	if action == "generateContent" && stream {
		upstreamAction = "streamGenerateContent"
	}
	if stream || upstreamAction == "streamGenerateContent" {
		upstreamAction += "?alt=sse"
	}

	// 重试循环
	var resp *http.Response
	for attempt := 1; attempt <= antigravityMaxRetries; attempt++ {
		upstreamReq, err := antigravity.NewAPIRequest(ctx, upstreamAction, accessToken, wrappedBody)
		if err != nil {
			return nil, err
		}

		resp, err = s.httpUpstream.Do(upstreamReq, proxyURL, account.ID, account.Concurrency)
		if err != nil {
			if attempt < antigravityMaxRetries {
				log.Printf("%s status=request_failed retry=%d/%d error=%v", prefix, attempt, antigravityMaxRetries, err)
				sleepAntigravityBackoff(attempt)
				continue
			}
			log.Printf("%s status=request_failed retries_exhausted error=%v", prefix, err)
			return nil, s.writeGoogleError(c, http.StatusBadGateway, "Upstream request failed after retries")
		}

		if resp.StatusCode >= 400 && s.shouldRetryUpstreamError(resp.StatusCode) {
			respBody, _ := io.ReadAll(io.LimitReader(resp.Body, 2<<20))
			_ = resp.Body.Close()

			if attempt < antigravityMaxRetries {
				log.Printf("%s status=%d retry=%d/%d", prefix, resp.StatusCode, attempt, antigravityMaxRetries)
				sleepAntigravityBackoff(attempt)
				continue
			}
			// 所有重试都失败，标记限流状态
			if resp.StatusCode == 429 {
				s.handleUpstreamError(ctx, prefix, account, resp.StatusCode, resp.Header, respBody)
			}
			resp = &http.Response{
				StatusCode: resp.StatusCode,
				Header:     resp.Header.Clone(),
				Body:       io.NopCloser(bytes.NewReader(respBody)),
			}
			break
		}

		break
	}
	defer func() { _ = resp.Body.Close() }()

	requestID := resp.Header.Get("x-request-id")
	if requestID != "" {
		c.Header("x-request-id", requestID)
	}

	// 处理错误响应
	if resp.StatusCode >= 400 {
		respBody, _ := io.ReadAll(io.LimitReader(resp.Body, 2<<20))
		s.handleUpstreamError(ctx, prefix, account, resp.StatusCode, resp.Header, respBody)

		if s.shouldFailoverUpstreamError(resp.StatusCode) {
			return nil, &UpstreamFailoverError{StatusCode: resp.StatusCode}
		}

		// 解包并返回错误
		unwrapped, _ := s.unwrapV1InternalResponse(respBody)
		contentType := resp.Header.Get("Content-Type")
		if contentType == "" {
			contentType = "application/json"
		}
		c.Data(resp.StatusCode, contentType, unwrapped)
		return nil, fmt.Errorf("antigravity upstream error: %d", resp.StatusCode)
	}

	var usage *ClaudeUsage
	var firstTokenMs *int

	if stream || upstreamAction == "streamGenerateContent" {
		streamRes, err := s.handleGeminiStreamingResponse(c, resp, startTime)
		if err != nil {
			return nil, err
		}
		usage = streamRes.usage
		firstTokenMs = streamRes.firstTokenMs
	} else {
		usageResp, err := s.handleGeminiNonStreamingResponse(c, resp)
		if err != nil {
			return nil, err
		}
		usage = usageResp
	}

	if usage == nil {
		usage = &ClaudeUsage{}
	}

	return &ForwardResult{
		RequestID:    requestID,
		Usage:        *usage,
		Model:        originalModel,
		Stream:       stream,
		Duration:     time.Since(startTime),
		FirstTokenMs: firstTokenMs,
	}, nil
}

func (s *AntigravityGatewayService) shouldRetryUpstreamError(statusCode int) bool {
	switch statusCode {
	case 429, 500, 502, 503, 504, 529:
		return true
	default:
		return false
	}
}

func (s *AntigravityGatewayService) shouldFailoverUpstreamError(statusCode int) bool {
	switch statusCode {
	case 401, 403, 429, 529:
		return true
	default:
		return statusCode >= 500
	}
}

func sleepAntigravityBackoff(attempt int) {
	sleepGeminiBackoff(attempt) // 复用 Gemini 的退避逻辑
}

func (s *AntigravityGatewayService) handleUpstreamError(ctx context.Context, prefix string, account *Account, statusCode int, headers http.Header, body []byte) {
	// 429 使用 Gemini 格式解析（从 body 解析重置时间）
	if statusCode == 429 {
		resetAt := ParseGeminiRateLimitResetTime(body)
		if resetAt == nil {
			// 解析失败：Gemini 有重试时间用 5 分钟，Claude 没有用 1 分钟
			defaultDur := 1 * time.Minute
			if bytes.Contains(body, []byte("Please retry in")) || bytes.Contains(body, []byte("retryDelay")) {
				defaultDur = 5 * time.Minute
			}
			ra := time.Now().Add(defaultDur)
			log.Printf("%s status=429 rate_limited reset_in=%v (fallback)", prefix, defaultDur)
			_ = s.accountRepo.SetRateLimited(ctx, account.ID, ra)
			return
		}
		resetTime := time.Unix(*resetAt, 0)
		log.Printf("%s status=429 rate_limited reset_at=%v reset_in=%v", prefix, resetTime.Format("15:04:05"), time.Until(resetTime).Truncate(time.Second))
		_ = s.accountRepo.SetRateLimited(ctx, account.ID, resetTime)
		return
	}
	// 其他错误码继续使用 rateLimitService
	if s.rateLimitService == nil {
		return
	}
	shouldDisable := s.rateLimitService.HandleUpstreamError(ctx, account, statusCode, headers, body)
	if shouldDisable {
		log.Printf("%s status=%d marked_error", prefix, statusCode)
	}
}

type antigravityStreamResult struct {
	usage        *ClaudeUsage
	firstTokenMs *int
}

func (s *AntigravityGatewayService) handleGeminiStreamingResponse(c *gin.Context, resp *http.Response, startTime time.Time) (*antigravityStreamResult, error) {
	c.Status(resp.StatusCode)
	c.Header("Cache-Control", "no-cache")
	c.Header("Connection", "keep-alive")
	c.Header("X-Accel-Buffering", "no")

	contentType := resp.Header.Get("Content-Type")
	if contentType == "" {
		contentType = "text/event-stream; charset=utf-8"
	}
	c.Header("Content-Type", contentType)

	flusher, ok := c.Writer.(http.Flusher)
	if !ok {
		return nil, errors.New("streaming not supported")
	}

	reader := bufio.NewReader(resp.Body)
	usage := &ClaudeUsage{}
	var firstTokenMs *int

	for {
		line, err := reader.ReadString('\n')
		if len(line) > 0 {
			trimmed := strings.TrimRight(line, "\r\n")
			if strings.HasPrefix(trimmed, "data:") {
				payload := strings.TrimSpace(strings.TrimPrefix(trimmed, "data:"))
				if payload == "" || payload == "[DONE]" {
					_, _ = io.WriteString(c.Writer, line)
					flusher.Flush()
				} else {
					// 解包 v1internal 响应
					inner, parseErr := s.unwrapV1InternalResponse([]byte(payload))
					if parseErr == nil && inner != nil {
						payload = string(inner)
					}

					// 解析 usage
					var parsed map[string]any
					if json.Unmarshal(inner, &parsed) == nil {
						if u := extractGeminiUsage(parsed); u != nil {
							usage = u
						}
					}

					if firstTokenMs == nil {
						ms := int(time.Since(startTime).Milliseconds())
						firstTokenMs = &ms
					}

					_, _ = fmt.Fprintf(c.Writer, "data: %s\n\n", payload)
					flusher.Flush()
				}
			} else {
				_, _ = io.WriteString(c.Writer, line)
				flusher.Flush()
			}
		}

		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return nil, err
		}
	}

	return &antigravityStreamResult{usage: usage, firstTokenMs: firstTokenMs}, nil
}

func (s *AntigravityGatewayService) handleGeminiNonStreamingResponse(c *gin.Context, resp *http.Response) (*ClaudeUsage, error) {
	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}

	// 解包 v1internal 响应
	unwrapped, _ := s.unwrapV1InternalResponse(respBody)

	var parsed map[string]any
	if json.Unmarshal(unwrapped, &parsed) == nil {
		if u := extractGeminiUsage(parsed); u != nil {
			c.Data(resp.StatusCode, "application/json", unwrapped)
			return u, nil
		}
	}

	c.Data(resp.StatusCode, "application/json", unwrapped)
	return &ClaudeUsage{}, nil
}

func (s *AntigravityGatewayService) writeClaudeError(c *gin.Context, status int, errType, message string) error {
	c.JSON(status, gin.H{
		"type":  "error",
		"error": gin.H{"type": errType, "message": message},
	})
	return fmt.Errorf("%s", message)
}

func (s *AntigravityGatewayService) writeMappedClaudeError(c *gin.Context, upstreamStatus int, body []byte) error {
	// 记录上游错误详情便于调试
	log.Printf("[antigravity-Forward] upstream_error status=%d body=%s", upstreamStatus, string(body))

	var statusCode int
	var errType, errMsg string

	switch upstreamStatus {
	case 400:
		statusCode = http.StatusBadRequest
		errType = "invalid_request_error"
		errMsg = "Invalid request"
	case 401:
		statusCode = http.StatusBadGateway
		errType = "authentication_error"
		errMsg = "Upstream authentication failed"
	case 403:
		statusCode = http.StatusBadGateway
		errType = "permission_error"
		errMsg = "Upstream access forbidden"
	case 429:
		statusCode = http.StatusTooManyRequests
		errType = "rate_limit_error"
		errMsg = "Upstream rate limit exceeded"
	case 529:
		statusCode = http.StatusServiceUnavailable
		errType = "overloaded_error"
		errMsg = "Upstream service overloaded"
	default:
		statusCode = http.StatusBadGateway
		errType = "upstream_error"
		errMsg = "Upstream request failed"
	}

	c.JSON(statusCode, gin.H{
		"type":  "error",
		"error": gin.H{"type": errType, "message": errMsg},
	})
	return fmt.Errorf("upstream error: %d", upstreamStatus)
}

func (s *AntigravityGatewayService) writeGoogleError(c *gin.Context, status int, message string) error {
	statusStr := "UNKNOWN"
	switch status {
	case 400:
		statusStr = "INVALID_ARGUMENT"
	case 404:
		statusStr = "NOT_FOUND"
	case 429:
		statusStr = "RESOURCE_EXHAUSTED"
	case 500:
		statusStr = "INTERNAL"
	case 502, 503:
		statusStr = "UNAVAILABLE"
	}

	c.JSON(status, gin.H{
		"error": gin.H{
			"code":    status,
			"message": message,
			"status":  statusStr,
		},
	})
	return fmt.Errorf("%s", message)
}

// handleClaudeNonStreamingResponse 处理 Claude 非流式响应（Gemini → Claude 转换）
func (s *AntigravityGatewayService) handleClaudeNonStreamingResponse(c *gin.Context, resp *http.Response, originalModel string) (*ClaudeUsage, error) {
	body, err := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	if err != nil {
		return nil, s.writeClaudeError(c, http.StatusBadGateway, "upstream_error", "Failed to read upstream response")
	}

	// 转换 Gemini 响应为 Claude 格式
	claudeResp, agUsage, err := antigravity.TransformGeminiToClaude(body, originalModel)
	if err != nil {
		log.Printf("[antigravity-Forward] transform_error error=%v body=%s", err, string(body))
		return nil, s.writeClaudeError(c, http.StatusBadGateway, "upstream_error", "Failed to parse upstream response")
	}

	c.Data(http.StatusOK, "application/json", claudeResp)

	// 转换为 service.ClaudeUsage
	usage := &ClaudeUsage{
		InputTokens:              agUsage.InputTokens,
		OutputTokens:             agUsage.OutputTokens,
		CacheCreationInputTokens: agUsage.CacheCreationInputTokens,
		CacheReadInputTokens:     agUsage.CacheReadInputTokens,
	}
	return usage, nil
}

// handleClaudeStreamingResponse 处理 Claude 流式响应（Gemini SSE → Claude SSE 转换）
func (s *AntigravityGatewayService) handleClaudeStreamingResponse(c *gin.Context, resp *http.Response, startTime time.Time, originalModel string) (*antigravityStreamResult, error) {
	c.Header("Content-Type", "text/event-stream")
	c.Header("Cache-Control", "no-cache")
	c.Header("Connection", "keep-alive")
	c.Header("X-Accel-Buffering", "no")
	c.Status(http.StatusOK)

	flusher, ok := c.Writer.(http.Flusher)
	if !ok {
		return nil, errors.New("streaming not supported")
	}

	processor := antigravity.NewStreamingProcessor(originalModel)
	var firstTokenMs *int
	reader := bufio.NewReader(resp.Body)

	// 辅助函数：转换 antigravity.ClaudeUsage 到 service.ClaudeUsage
	convertUsage := func(agUsage *antigravity.ClaudeUsage) *ClaudeUsage {
		if agUsage == nil {
			return &ClaudeUsage{}
		}
		return &ClaudeUsage{
			InputTokens:              agUsage.InputTokens,
			OutputTokens:             agUsage.OutputTokens,
			CacheCreationInputTokens: agUsage.CacheCreationInputTokens,
			CacheReadInputTokens:     agUsage.CacheReadInputTokens,
		}
	}

	for {
		line, err := reader.ReadString('\n')
		if err != nil && !errors.Is(err, io.EOF) {
			return nil, fmt.Errorf("stream read error: %w", err)
		}

		if len(line) > 0 {
			// 处理 SSE 行，转换为 Claude 格式
			claudeEvents := processor.ProcessLine(strings.TrimRight(line, "\r\n"))

			if len(claudeEvents) > 0 {
				if firstTokenMs == nil {
					ms := int(time.Since(startTime).Milliseconds())
					firstTokenMs = &ms
				}

				if _, writeErr := c.Writer.Write(claudeEvents); writeErr != nil {
					finalEvents, agUsage := processor.Finish()
					if len(finalEvents) > 0 {
						_, _ = c.Writer.Write(finalEvents)
					}
					return &antigravityStreamResult{usage: convertUsage(agUsage), firstTokenMs: firstTokenMs}, writeErr
				}
				flusher.Flush()
			}
		}

		if errors.Is(err, io.EOF) {
			break
		}
	}

	// 发送结束事件
	finalEvents, agUsage := processor.Finish()
	if len(finalEvents) > 0 {
		_, _ = c.Writer.Write(finalEvents)
		flusher.Flush()
	}

	return &antigravityStreamResult{usage: convertUsage(agUsage), firstTokenMs: firstTokenMs}, nil
}
