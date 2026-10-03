package service

import "net/http"

func (s *OpenAIGatewayService) SetPluginManager(manager *PluginManager) {
	s.pluginManager = manager
}

// doOpenAIUpstream 只在 OpenAI OAuth 能力绑定已启用时把真实请求交给插件。
// 插件返回标准 http.Response，响应解析、错误映射、SSE 和计费仍由现有核心链处理。
func (s *OpenAIGatewayService) doOpenAIUpstream(request *http.Request, proxyURL string, account *Account) (*http.Response, error) {
	logGrokUpstreamHTTPDump(request, proxyURL, account)
	if s.pluginManager != nil {
		response, handled, err := s.pluginManager.RoundTripOpenAIOAuth(request.Context(), request, proxyURL, account)
		if handled {
			captureOpsHeadersFromRequest(request, response)
			return response, err
		}
	}
	response, err := s.httpUpstream.Do(request, proxyURL, account.ID, account.Concurrency)
	captureOpsHeadersFromRequest(request, response)
	return response, err
}

// captureOpsHeadersFromRequest 把一次上游调用的出站请求头与回程响应头写进
// 错误记录用的头快照。取不到 gin.Context（非 HTTP 请求链路）时静默跳过。
func captureOpsHeadersFromRequest(request *http.Request, response *http.Response) {
	if request == nil {
		return
	}
	CaptureOpsUpstreamHeaders(request.Context(), request, response)
}

// doOpenAIAccountTestUpstream 让 OpenAI OAuth 账号测试与真实转发使用同一插件路径。
// API Key 和未命中插件的账号保持各自原有的 HTTPUpstream 行为。
func (s *AccountTestService) doOpenAIAccountTestUpstream(
	request *http.Request,
	proxyURL string,
	account *Account,
	useTLSFallback bool,
) (*http.Response, error) {
	if s.pluginManager != nil {
		response, handled, err := s.pluginManager.RoundTripOpenAIOAuth(request.Context(), request, proxyURL, account)
		if handled {
			return response, err
		}
	}
	if useTLSFallback {
		return s.httpUpstream.DoWithTLS(
			request,
			proxyURL,
			account.ID,
			account.Concurrency,
			s.tlsFPProfileService.ResolveTLSProfile(account),
		)
	}
	return s.httpUpstream.Do(request, proxyURL, account.ID, account.Concurrency)
}
