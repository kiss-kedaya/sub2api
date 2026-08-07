package service

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	coderws "github.com/coder/websocket"
	"github.com/gin-gonic/gin"
)

// supportedGrokVoiceHTTPEndpoints are xAI Voice HTTP paths we forward as-is.
var supportedGrokVoiceHTTPEndpoints = map[string]struct{}{
	"tts":           {},
	"stt":           {},
	"custom-voices": {},
}

// ForwardGrokVoice forwards the official xAI Voice HTTP APIs (/tts, /stt, /custom-voices).
// The response is intentionally passed through because TTS returns audio bytes
// while STT returns JSON and xAI may add format-specific headers.
func (s *OpenAIGatewayService) ForwardGrokVoice(ctx context.Context, c *gin.Context, account *Account, endpoint string, body []byte, contentType string) (*OpenAIForwardResult, error) {
	if s == nil || account == nil {
		return nil, fmt.Errorf("grok voice service/account is required")
	}
	if account.Platform != PlatformGrok {
		return nil, fmt.Errorf("account platform %s is not supported for grok voice", account.Platform)
	}
	endpoint = strings.Trim(strings.TrimSpace(endpoint), "/")
	if _, ok := supportedGrokVoiceHTTPEndpoints[endpoint]; !ok {
		return nil, fmt.Errorf("unsupported grok voice endpoint: %s", endpoint)
	}
	token, _, err := s.getRequestCredential(ctx, c, account)
	if err != nil {
		return nil, err
	}
	targetURL, err := buildGrokVoiceURL(account, s.cfg, endpoint)
	if err != nil {
		return nil, err
	}
	upstreamCtx, release := detachUpstreamContext(ctx)
	defer release()
	req, err := http.NewRequestWithContext(upstreamCtx, http.MethodPost, targetURL, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Accept", "application/json, audio/*")
	if strings.TrimSpace(contentType) == "" {
		contentType = "application/json"
	}
	req.Header.Set("Content-Type", contentType)
	// Voice hits api.x.ai (not CLI proxy). Still stamp CLI identity headers for
	// consistency with other Grok outbound calls; transport will not rewrite
	// non-CLI-proxy hosts.
	applyGrokCLIHeaders(req.Header)
	account.ApplyHeaderOverrides(req.Header)

	proxyURL := ""
	if account.ProxyID != nil && account.Proxy != nil {
		proxyURL = account.Proxy.URL()
	}
	started := time.Now()
	resp, err := s.httpUpstream.Do(req, proxyURL, account.ID, account.Concurrency)
	SetOpsLatencyMs(c, OpsUpstreamLatencyMsKey, time.Since(started).Milliseconds())
	if err != nil {
		return nil, s.handleOpenAIUpstreamTransportError(ctx, c, account, err, false)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode >= 400 {
		return s.handleGrokMediaErrorResponse(ctx, resp, c, account, resp.Header.Get("x-request-id"), endpoint)
	}
	data, err := ReadUpstreamResponseBody(resp.Body, s.cfg, c, openAITooLargeError)
	if err != nil {
		return nil, err
	}
	writeGrokMediaResponse(c, resp, data, s.responseHeaderFilter)
	return &OpenAIForwardResult{
		RequestID:     firstNonEmpty(resp.Header.Get("x-request-id"), resp.Header.Get("xai-request-id")),
		Model:         endpoint,
		UpstreamModel: endpoint,
		Duration:      time.Since(started),
	}, nil
}

// ProxyGrokRealtime relays JSON Realtime events to xAI's native Voice WS.
// Audio is carried as base64 inside JSON events, so preserving the JSON bytes
// is sufficient and avoids translating protocol event types.
func (s *OpenAIGatewayService) ProxyGrokRealtime(ctx context.Context, c *gin.Context, client *coderws.Conn, account *Account, token, model string) error {
	if s == nil || client == nil || account == nil {
		return fmt.Errorf("realtime service, client, and account are required")
	}
	if account.Platform != PlatformGrok {
		return fmt.Errorf("account platform %s is not supported for grok realtime", account.Platform)
	}
	base, err := buildGrokVoiceURL(account, s.cfg, "realtime")
	if err != nil {
		return err
	}
	u, err := url.Parse(base)
	if err != nil {
		return err
	}
	u.Scheme = "wss"
	u.RawQuery = "model=" + url.QueryEscape(firstNonEmpty(model, "grok-voice-latest"))
	headers := http.Header{"Authorization": []string{"Bearer " + token}}
	// Stamp CLI identity for consistency (host is api.x.ai; no CLI-proxy rewrite).
	applyGrokCLIHeaders(headers)
	if account != nil {
		account.ApplyHeaderOverrides(headers)
	}

	dialer := s.getOpenAIWSPassthroughDialer()
	proxyURL := ""
	if account.ProxyID != nil && account.Proxy != nil {
		proxyURL = account.Proxy.URL()
	}
	upstream, _, _, err := dialer.Dial(ctx, u.String(), headers, proxyURL)
	if err != nil {
		return err
	}
	defer func() { _ = upstream.Close() }()

	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	errCh := make(chan error, 2)

	// Upstream → client
	go func() {
		for {
			msg, readErr := upstream.ReadMessage(ctx)
			if readErr != nil {
				errCh <- readErr
				return
			}
			if writeErr := client.Write(ctx, coderws.MessageText, msg); writeErr != nil {
				errCh <- writeErr
				return
			}
		}
	}()

	// Client → upstream (JSON events only)
	go func() {
		for {
			kind, msg, readErr := client.Read(ctx)
			if readErr != nil {
				errCh <- readErr
				return
			}
			if kind != coderws.MessageText && kind != coderws.MessageBinary {
				continue
			}
			var raw json.RawMessage
			if unmarshalErr := json.Unmarshal(msg, &raw); unmarshalErr != nil {
				errCh <- fmt.Errorf("invalid realtime event: %w", unmarshalErr)
				return
			}
			if writeErr := upstream.WriteJSON(ctx, raw); writeErr != nil {
				errCh <- writeErr
				return
			}
		}
	}()

	return <-errCh
}
