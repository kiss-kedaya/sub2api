package service

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"math"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	coderws "github.com/coder/websocket"
	"github.com/gin-gonic/gin"
	"github.com/tidwall/gjson"
)

// DefaultGrokRealtimeDialTimeout bounds the pre-accept upstream handshake.
// The timeout only covers dialing; an established session is not interrupted.
const DefaultGrokRealtimeDialTimeout = 12 * time.Second

// supportedGrokVoiceHTTPEndpoints are xAI Voice HTTP paths we forward as-is.
var supportedGrokVoiceHTTPEndpoints = map[string]struct{}{
	"tts":           {},
	"stt":           {},
	"custom-voices": {},
}

// ForwardGrokVoice forwards the official xAI Voice HTTP APIs (/tts, /stt, and
// the custom-voices CRUD/audio subresources).
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
	parts := strings.Split(endpoint, "/")
	baseEndpoint := parts[0]
	if _, ok := supportedGrokVoiceHTTPEndpoints[baseEndpoint]; !ok {
		return nil, fmt.Errorf("unsupported grok voice endpoint: %s", endpoint)
	}
	if len(parts) > 1 && baseEndpoint != "custom-voices" {
		return nil, fmt.Errorf("unsupported grok voice endpoint: %s", endpoint)
	}
	if baseEndpoint == "custom-voices" {
		if len(parts) > 3 || (len(parts) == 3 && parts[2] != "audio") {
			return nil, fmt.Errorf("unsupported grok voice endpoint: %s", endpoint)
		}
	}
	for _, part := range parts[1:] {
		if part == "" || part == "." || part == ".." || strings.ContainsAny(part, "?#\\") {
			return nil, fmt.Errorf("invalid grok voice endpoint path")
		}
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
	method := http.MethodPost
	if c != nil && c.Request != nil && strings.TrimSpace(c.Request.Method) != "" {
		method = c.Request.Method
	}
	req, err := http.NewRequestWithContext(upstreamCtx, method, targetURL, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Accept", "application/json, audio/*")
	if strings.TrimSpace(contentType) == "" {
		contentType = "application/json"
	}
	req.Header.Set("Content-Type", contentType)
	// Match media path: CLI identity headers only on the CLI chat proxy.
	// Official api.x.ai voice rejects or mistreats OAuth when CLI headers are stamped.
	if account.IsGrokOAuth() && isGrokCLIProxyTarget(targetURL) {
		applyGrokCLIHeaders(req.Header)
	}
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
	audioUsage := estimateGrokVoiceAudioUsage(baseEndpoint, body, contentType, data, time.Since(started))
	upstreamID := firstNonEmpty(resp.Header.Get("x-request-id"), resp.Header.Get("xai-request-id"))
	return &OpenAIForwardResult{
		// Forced durable money-event id so usage_billing_dedup cannot collapse under a reused client id.
		RequestID:       StableGrokAudioBillingRequestID(upstreamID),
		UpstreamHeaders: resp.Header,
		Model:           baseEndpoint,
		UpstreamModel:   baseEndpoint,
		Duration:        time.Since(started),
		AudioUsage:      audioUsage,
	}, nil
}

// ProxyGrokRealtime relays JSON Realtime events to xAI's native Voice WS.
// Audio is carried as base64 inside JSON events, so preserving the JSON bytes
// is sufficient and avoids translating protocol event types.
func (s *OpenAIGatewayService) ProxyGrokRealtime(ctx context.Context, c *gin.Context, client *coderws.Conn, account *Account, token, model string) (bool, error) {
	if s == nil || client == nil || account == nil {
		return false, fmt.Errorf("realtime service, client, and account are required")
	}
	if account.Platform != PlatformGrok {
		return false, fmt.Errorf("account platform %s is not supported for grok realtime", account.Platform)
	}
	upstream, err := s.OpenGrokRealtime(ctx, account, token, model)
	if err != nil {
		return false, err
	}
	defer func() { _ = upstream.Close() }()
	return s.ProxyGrokRealtimeConn(ctx, c, client, upstream, model)
}

type GrokRealtimeUpstream struct{ conn openAIWSClientConn }

// GrokRealtimeDialError preserves an HTTP status returned before WebSocket
// upgrade so handlers can apply the normal Grok account policy.
type GrokRealtimeDialError struct {
	StatusCode int
	Err        error
}

func (e *GrokRealtimeDialError) Error() string { return e.Err.Error() }
func (e *GrokRealtimeDialError) Unwrap() error { return e.Err }

func (u *GrokRealtimeUpstream) Close() error {
	if u == nil || u.conn == nil {
		return nil
	}
	return u.conn.Close()
}

func (s *OpenAIGatewayService) OpenGrokRealtime(ctx context.Context, account *Account, token, model string) (*GrokRealtimeUpstream, error) {
	if s == nil || account == nil || account.Platform != PlatformGrok {
		return nil, fmt.Errorf("grok realtime account is required")
	}
	base, err := buildGrokVoiceURL(account, s.cfg, "realtime")
	if err != nil {
		return nil, err
	}
	u, err := url.Parse(base)
	if err != nil {
		return nil, err
	}
	if u.Scheme == "http" && s.cfg != nil && s.cfg.Security.URLAllowlist.AllowInsecureHTTP {
		u.Scheme = "ws"
	} else {
		u.Scheme = "wss"
	}
	q := u.Query()
	q.Set("model", firstNonEmpty(model, "grok-voice-latest"))
	u.RawQuery = q.Encode()
	headers := http.Header{"Authorization": []string{"Bearer " + token}}
	if account.IsGrokOAuth() && isGrokCLIProxyTarget(u.String()) {
		applyGrokCLIHeaders(headers)
	}
	account.ApplyHeaderOverrides(headers)
	proxyURL := ""
	if account.ProxyID != nil && account.Proxy != nil {
		proxyURL = account.Proxy.URL()
	}
	conn, status, _, err := s.getOpenAIWSPassthroughDialer().Dial(ctx, u.String(), headers, proxyURL)
	if err != nil {
		return nil, &GrokRealtimeDialError{StatusCode: status, Err: err}
	}
	return &GrokRealtimeUpstream{conn: conn}, nil
}

// HandleGrokRealtimeUpstreamError applies the shared Grok account policy to a
// failed pre-accept WebSocket handshake.
func (s *OpenAIGatewayService) HandleGrokRealtimeUpstreamError(ctx context.Context, account *Account, statusCode int, body []byte) {
	if statusCode <= 0 {
		statusCode = http.StatusBadGateway
	}
	s.handleGrokAccountUpstreamError(ctx, account, statusCode, nil, body)
}

func (s *OpenAIGatewayService) ProxyGrokRealtimeConn(ctx context.Context, c *gin.Context, client *coderws.Conn, upstream *GrokRealtimeUpstream, requestedModel string) (bool, error) {
	if s == nil || client == nil || upstream == nil || upstream.conn == nil {
		return false, fmt.Errorf("realtime connection is required")
	}
	conn := upstream.conn
	// Snapshot the selected group's policy before starting either relay goroutine.
	// Audio observation/billing still consumes the original upstream events.
	returnRequestedModel := ContextShouldReturnRequestedModel(c)
	presentation := newLiveModelPresentation(&LiveCallRecord{RequestedModel: requestedModel})

	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	errCh := make(chan error, 2)
	var audioObserved atomic.Bool
	var relays sync.WaitGroup
	relays.Add(2)

	// Upstream → client
	go func() {
		defer relays.Done()
		for {
			msg, readErr := conn.ReadMessage(ctx)
			if readErr != nil {
				errCh <- readErr
				return
			}
			if grokRealtimeUpstreamEventIsBillable(msg) {
				audioObserved.Store(true)
			}
			if returnRequestedModel {
				model, _ := presentation.responseModel(msg)
				msg = RewriteRequestedModelResponse(msg, "realtime", model)
			}
			if writeErr := client.Write(ctx, coderws.MessageText, msg); writeErr != nil {
				errCh <- writeErr
				return
			}
		}
	}()

	// Client → upstream (JSON events only)
	go func() {
		defer relays.Done()
		for {
			kind, msg, readErr := client.Read(ctx)
			if readErr != nil {
				errCh <- readErr
				return
			}
			if kind != coderws.MessageText && kind != coderws.MessageBinary {
				continue
			}
			if grokRealtimeEventHasAudio(msg) {
				audioObserved.Store(true)
			}
			var raw json.RawMessage
			if unmarshalErr := json.Unmarshal(msg, &raw); unmarshalErr != nil {
				errCh <- fmt.Errorf("invalid realtime event: %w", unmarshalErr)
				return
			}
			if returnRequestedModel {
				if presentationErr := presentation.observeClient(msg); presentationErr != nil {
					errCh <- presentationErr
					return
				}
			}
			if writeErr := conn.WriteJSON(ctx, raw); writeErr != nil {
				errCh <- writeErr
				return
			}
		}
	}()

	_, relayErr := awaitGrokRealtimeAudioObserved(errCh, &audioObserved)
	// Capacity remains owned until both I/O loops have stopped. Returning after
	// the first loop lets admission release a slot while the other still relays.
	cancel()
	_ = conn.Close()
	_ = client.CloseNow()
	relays.Wait()
	return audioObserved.Load(), relayErr
}

// ProbeGrokRealtime performs the upstream WebSocket handshake without sending
// any client-visible events. Handlers use it before accepting the downstream
// upgrade so authentication and endpoint failures remain ordinary HTTP errors.
func (s *OpenAIGatewayService) ProbeGrokRealtime(ctx context.Context, account *Account, token, model string) error {
	if s == nil || account == nil {
		return fmt.Errorf("realtime service and account are required")
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
	if u.Scheme == "http" && s.cfg != nil && s.cfg.Security.URLAllowlist.AllowInsecureHTTP {
		u.Scheme = "ws"
	} else {
		u.Scheme = "wss"
	}
	q := u.Query()
	q.Set("model", firstNonEmpty(model, "grok-voice-latest"))
	u.RawQuery = q.Encode()
	headers := http.Header{"Authorization": []string{"Bearer " + token}}
	if account.IsGrokOAuth() && isGrokCLIProxyTarget(u.String()) {
		applyGrokCLIHeaders(headers)
	}
	account.ApplyHeaderOverrides(headers)
	proxyURL := ""
	if account.ProxyID != nil && account.Proxy != nil {
		proxyURL = account.Proxy.URL()
	}
	dialer := s.getOpenAIWSPassthroughDialer()
	conn, _, _, err := dialer.Dial(ctx, u.String(), headers, proxyURL)
	if err != nil {
		return err
	}
	return conn.Close()
}

func awaitGrokRealtimeAudioObserved(errCh <-chan error, audioObserved *atomic.Bool) (bool, error) {
	err := <-errCh
	if audioObserved == nil {
		return false, err
	}
	return audioObserved.Load(), err
}

func grokRealtimeEventHasAudio(msg []byte) bool {
	if !gjson.ValidBytes(msg) {
		return false
	}
	eventType := strings.ToLower(strings.TrimSpace(gjson.GetBytes(msg, "type").String()))
	if !strings.Contains(eventType, "audio") || strings.Contains(eventType, "transcript") {
		return false
	}
	for _, path := range []string{"audio", "delta", "data"} {
		value := gjson.GetBytes(msg, path)
		if value.Type == gjson.String && strings.TrimSpace(value.String()) != "" {
			return true
		}
	}
	return false
}

// grokRealtimeUpstreamEventIsBillable reports whether an upstream realtime event
// proves the session produced billable assistant output (audio, transcript,
// text-only modalities:["text"], tool call arguments, or completed response output).
func grokRealtimeUpstreamEventIsBillable(msg []byte) bool {
	if grokRealtimeEventHasAudio(msg) {
		return true
	}
	if !gjson.ValidBytes(msg) {
		return false
	}
	hasNonEmptyString := func(paths ...string) bool {
		for _, path := range paths {
			v := gjson.GetBytes(msg, path)
			if v.Type == gjson.String && strings.TrimSpace(v.String()) != "" {
				return true
			}
		}
		return false
	}
	eventType := strings.ToLower(strings.TrimSpace(gjson.GetBytes(msg, "type").String()))
	switch eventType {
	case "response.audio_transcript.delta", "response.audio_transcript.done",
		"response.output_audio_transcript.delta", "response.output_audio_transcript.done":
		return hasNonEmptyString("delta", "transcript")
	case "response.text.delta", "response.text.done",
		"response.output_text.delta", "response.output_text.done":
		return hasNonEmptyString("delta", "text")
	case "response.function_call_arguments.delta", "response.function_call_arguments.done":
		return hasNonEmptyString("delta", "arguments")
	case "conversation.item.input_audio_transcription.completed":
		return hasNonEmptyString("transcript")
	case "response.content_part.done":
		return hasNonEmptyString("part.text", "part.transcript", "part.audio")
	case "response.output_item.done":
		itemType := strings.ToLower(strings.TrimSpace(gjson.GetBytes(msg, "item.type").String()))
		itemRole := strings.ToLower(strings.TrimSpace(gjson.GetBytes(msg, "item.role").String()))
		if itemType == "function_call" && hasNonEmptyString("item.arguments", "item.name") {
			return true
		}
		if itemRole == "assistant" {
			if content := gjson.GetBytes(msg, "item.content"); content.IsArray() && len(content.Array()) > 0 {
				return true
			}
		}
		return false
	case "response.done":
		if output := gjson.GetBytes(msg, "response.output"); output.IsArray() && len(output.Array()) > 0 {
			return true
		}
		if outTokens := gjson.GetBytes(msg, "response.usage.output_tokens"); outTokens.Type == gjson.Number && outTokens.Float() > 0 {
			return true
		}
		if totalTokens := gjson.GetBytes(msg, "response.usage.total_tokens"); totalTokens.Type == gjson.Number && totalTokens.Float() > 0 {
			return true
		}
		return false
	default:
		return false
	}
}

const maxGrokSTTFallbackSeconds = 24 * 3600.0

// GrokTTSText returns the text xAI actually synthesizes from a native /tts
// JSON body. xAI reads the top-level "text" field; "input" and "prompt" are
// compatibility fallbacks only. Preferring a fallback over "text" lets a mixed
// request (short "input" + long "text") underbill the audio upstream produces.
func GrokTTSText(body []byte) string {
	if len(body) == 0 || !gjson.ValidBytes(body) {
		return ""
	}
	for _, key := range []string{"text", "input", "prompt"} {
		value := gjson.GetBytes(body, key)
		if value.Type != gjson.String {
			continue
		}
		if text := strings.TrimSpace(value.String()); text != "" {
			return text
		}
	}
	return ""
}

// estimateGrokVoiceAudioUsage derives billing units from the request/response.
// TTS: million characters of input text; STT: hours approximated from request body size
// when duration is unknown; custom-voices: no units (nil).
func estimateGrokVoiceAudioUsage(endpoint string, reqBody []byte, contentType string, respBody []byte, elapsed time.Duration) *AudioUsage {
	switch strings.TrimSpace(endpoint) {
	case "tts":
		// Bill the same "text" field xAI synthesizes from; fallback to raw body
		// length only when the body carries no recognizable text field.
		chars := len([]rune(GrokTTSText(reqBody)))
		if chars <= 0 {
			chars = len(reqBody)
		}
		if chars <= 0 {
			return nil
		}
		return &AudioUsage{Mode: "tts", DurationOrUnits: float64(chars) / 1_000_000.0}
	case "stt":
		// Prefer response duration when present; do not trust client duration_seconds alone
		// (under-report would underbill). Floor against body-size heuristic and elapsed.
		secs := 0.0
		if gjson.ValidBytes(respBody) {
			for _, path := range []string{"duration", "duration_seconds", "audio_duration", "usage.seconds"} {
				if v := gjson.GetBytes(respBody, path); v.Exists() && v.Type == gjson.Number && v.Float() > 0 {
					secs = v.Float()
					break
				}
			}
		}
		// Multipart / body size heuristic: ~16KB/s for compressed speech (lower bound).
		sizeFloor := 0.0
		if len(reqBody) > 0 {
			sizeFloor = float64(len(reqBody)) / 16000.0
		}
		clientSecs := 0.0
		if gjson.ValidBytes(reqBody) {
			if v := gjson.GetBytes(reqBody, "duration_seconds"); v.Exists() && v.Type == gjson.Number {
				clientSecs = v.Float()
			}
		}
		if secs <= 0 {
			// When upstream omits audio duration, use the largest positive fallback
			// among body-size floor, wall-clock elapsed time, and client-reported duration
			// (clamped) so a short wall-clock elapsed duration never masks sizeFloor.
			if sizeFloor > secs {
				secs = sizeFloor
			}
			if elapsedSecs := elapsed.Seconds(); elapsedSecs > secs && !math.IsNaN(elapsedSecs) && !math.IsInf(elapsedSecs, 0) {
				secs = elapsedSecs
			}
			if clientSecs > secs && !math.IsNaN(clientSecs) && !math.IsInf(clientSecs, 0) {
				secs = clientSecs
			}
			if secs > maxGrokSTTFallbackSeconds {
				secs = maxGrokSTTFallbackSeconds
			}
		}
		if secs <= 0 {
			return nil
		}
		return &AudioUsage{Mode: "stt", DurationOrUnits: secs / 3600.0}
	case "realtime":
		mins := elapsed.Minutes()
		if mins <= 0 {
			return nil
		}
		return &AudioUsage{Mode: "realtime", DurationOrUnits: mins}
	default:
		return nil
	}
}
