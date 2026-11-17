package service

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"math"
	"net/http"
	"strings"
	"time"
	"unicode"

	"github.com/gin-gonic/gin"
	"github.com/tidwall/gjson"
)

const (
	DeepSeekToolsPathASR     = "/v1/tools/asr"
	DeepSeekToolsPathTTS     = "/v1/tools/tts"
	DeepSeekToolsPathOCR     = "/v1/tools/ocr"
	DeepSeekToolsLabelASR    = "deepseek-tools-asr"
	DeepSeekToolsLabelTTS    = "deepseek-tools-tts"
	DeepSeekToolsLabelOCR    = "deepseek-tools-ocr"
	deepSeekOCRResponseLimit = 64 << 20
)

func isDeepSeekSpeechLabel(model string) bool {
	return model == DeepSeekToolsLabelASR || model == DeepSeekToolsLabelTTS
}

// DeepSeekTTSBillableCharacters counts Han runes twice and all other runes once.
func DeepSeekTTSBillableCharacters(text string) int {
	count := 0
	for _, r := range text {
		count++
		if unicode.Is(unicode.Han, r) {
			count++
		}
	}
	return count
}

// ForwardDeepSeekMedia forwards native ASR/TTS/OCR, using only the selected
// account's connection settings. Search-specific group credentials never apply.
func (s *OpenAIGatewayService) ForwardDeepSeekMedia(ctx context.Context, c *gin.Context, account *Account, path, model string, body []byte) (*OpenAIForwardResult, error) {
	if s == nil || c == nil || account == nil || account.Platform != PlatformDeepseek {
		return nil, fmt.Errorf("deepseek media: service, context and DeepSeek account are required")
	}
	var timeout time.Duration
	switch path {
	case DeepSeekToolsPathASR:
		timeout = 12 * time.Minute
	case DeepSeekToolsPathOCR:
		timeout = 200 * time.Second
	case DeepSeekToolsPathTTS:
		// The official maximum output is 30 minutes. A client disconnect still
		// cancels this request immediately, unlike the recognition APIs below.
		timeout = 32 * time.Minute
	default:
		return nil, fmt.Errorf("deepseek media: unsupported path %q", path)
	}
	upstreamCtx := ctx
	if path != DeepSeekToolsPathTTS {
		upstreamCtx = context.WithoutCancel(ctx)
	}
	upstreamCtx, cancel := context.WithTimeout(upstreamCtx, timeout)
	defer cancel()
	upstreamCtx = WithHTTPUpstreamProfile(upstreamCtx, HTTPUpstreamProfileDeepSeekTools)
	baseURL := strings.TrimRight(strings.TrimSpace(account.GetOpenAIFormatBaseURL()), "/")
	credential := strings.TrimSpace(account.GetCNAPIKey())
	if baseURL == "" || credential == "" {
		return nil, fmt.Errorf("deepseek media: missing account connection settings")
	}
	req, err := http.NewRequestWithContext(upstreamCtx, http.MethodPost, buildOpenAIEndpointURL(baseURL, path), bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("deepseek media: build request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+credential)
	req.Header.Set("Content-Type", c.GetHeader("Content-Type"))
	req.Header.Set("Accept", "application/json, audio/*")
	SetActualOpenAIUpstreamEndpoint(c, path)
	started := time.Now()
	resp, err := s.doOpenAIUpstream(req, resolveAccountProxyURL(account), account)
	if err != nil {
		// A transport failure can happen after the provider accepted paid work.
		// Only explicit error HTTP statuses below are safe to replay.
		return nil, fmt.Errorf("deepseek media: request failed: %w", err)
	}
	defer func() {
		if closeErr := resp.Body.Close(); closeErr != nil {
			slog.Error("deepseek_media.close_failed", "account_id", account.ID, "path", path, "error", sanitizeUpstreamErrorMessage(closeErr.Error()))
		}
	}()
	if requestID := resp.Header.Get("x-request-id"); requestID != "" {
		c.Header("x-request-id", requestID)
	}
	if resp.StatusCode != http.StatusOK {
		data, readErr := ReadUpstreamResponseBody(resp.Body, s.cfg, c, openAITooLargeError)
		if readErr != nil {
			return nil, fmt.Errorf("deepseek media: read error response: %w", readErr)
		}
		message := sanitizeUpstreamErrorMessage(extractUpstreamErrorMessage(data))
		if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices || clientPrivacyErrorValue(gjson.GetBytes(data, "error")) {
			setOpsUpstreamError(c, resp.StatusCode, message, truncateString(string(data), 2048))
		}
		// Validation/moderation errors are request-scoped, even with FailoverOn400.
		if resp.StatusCode >= 400 && resp.StatusCode != http.StatusBadRequest &&
			s.shouldFailoverOpenAIUpstreamResponse(account, resp.StatusCode, message, data) {
			return nil, newOpenAIUpstreamFailoverError(resp.StatusCode, resp.Header, data, message,
				PoolModeSameAccountRetry(account, resp.StatusCode, resp.Header, data))
		}
		writeOpenAIPassthroughResponseHeaders(c.Writer.Header(), resp.Header, s.responseHeaderFilter)
		c.Data(resp.StatusCode, firstNonEmpty(resp.Header.Get("Content-Type"), "application/json"), sanitizeClientUpstreamErrorBody(resp.StatusCode, data))
		return nil, nil
	}
	result := &OpenAIForwardResult{
		RequestID: strings.TrimSpace(resp.Header.Get("x-request-id")), UpstreamHeaders: resp.Header,
		Model: model, UpstreamEndpoint: path, FixedToolBilling: true,
	}
	if path == DeepSeekToolsPathASR {
		data, readErr := ReadUpstreamResponseBody(resp.Body, s.cfg, c, openAITooLargeError)
		if readErr != nil {
			return nil, fmt.Errorf("deepseek asr: read response: %w", readErr)
		}
		if clientPrivacyErrorValue(gjson.GetBytes(data, "error")) {
			setOpsUpstreamError(c, resp.StatusCode, sanitizeUpstreamErrorMessage(extractUpstreamErrorMessage(data)), truncateString(string(data), 2048))
		}
		writeOpenAIPassthroughResponseHeaders(c.Writer.Header(), resp.Header, s.responseHeaderFilter)
		c.Data(resp.StatusCode, firstNonEmpty(resp.Header.Get("Content-Type"), "application/json"), sanitizeClientUpstreamErrorBody(resp.StatusCode, data))
		var payload struct {
			Text       *string         `json:"text"`
			DurationMS float64         `json:"duration_ms"`
			Error      json.RawMessage `json:"error"`
		}
		if json.Unmarshal(data, &payload) != nil || payload.Text == nil || payload.DurationMS <= 0 || len(payload.Error) != 0 {
			return nil, nil
		}
		result.AudioUsage = &AudioUsage{Mode: "stt", DurationOrUnits: math.Ceil(payload.DurationMS/1000) / 3600}
	} else {
		writeOpenAIPassthroughResponseHeaders(c.Writer.Header(), resp.Header, s.responseHeaderFilter)
		c.Header("Content-Type", firstNonEmpty(resp.Header.Get("Content-Type"), "application/json"))
		c.Header("X-Accel-Buffering", "no")
		c.Writer.Header().Del("Content-Length")
		if path == DeepSeekToolsPathTTS && resp.Header.Get("Content-Length") != "" {
			c.Header("Content-Length", resp.Header.Get("Content-Length"))
		}
		c.Status(resp.StatusCode)
		result.Stream = true
		if path == DeepSeekToolsPathTTS {
			hasAudio, clientGone, readErr := streamDeepSeekAudio(ctx, cancel, c, resp.Body)
			if readErr != nil {
				return nil, fmt.Errorf("deepseek tts: interrupted upstream audio: %w", readErr)
			}
			if !hasAudio {
				return nil, nil
			}
			result.ClientDisconnect = clientGone
			result.AudioUsage = &AudioUsage{Mode: "tts", DurationOrUnits: float64(DeepSeekTTSBillableCharacters(gjson.GetBytes(body, "text").String())) / 1_000_000}
		} else {
			pages, clientGone, readErr := streamDeepSeekOCR(c, resp.Body, deepSeekOCRResponseLimit)
			if readErr != nil {
				return nil, fmt.Errorf("deepseek ocr: read response: %w", readErr)
			}
			if pages <= 0 {
				return nil, nil
			}
			result.ClientDisconnect, result.OCRPages = clientGone, pages
		}
	}
	result.Duration = time.Since(started)
	return result, nil
}

func streamDeepSeekAudio(ctx context.Context, cancel context.CancelFunc, c *gin.Context, reader io.Reader) (hasAudio, clientGone bool, err error) {
	buf := make([]byte, 32<<10)
	for {
		n, readErr := reader.Read(buf)
		if n > 0 {
			hasAudio = true
			if _, writeErr := c.Writer.Write(buf[:n]); writeErr != nil {
				cancel()
				return true, true, nil // provider started synthesis: bill the full input
			}
			c.Writer.Flush()
		}
		if readErr == io.EOF {
			return hasAudio, ctx.Err() != nil, nil
		}
		if ctx.Err() != nil {
			cancel()
			return hasAudio, true, nil
		}
		if readErr != nil {
			return false, false, readErr // upstream truncation is never billed
		}
	}
}

// streamDeepSeekOCR flushes keepalive whitespace immediately. Only an
// unclassified/error envelope waits for EOF so diagnostics can be sanitized;
// recognized result envelopes stream immediately while retaining a bounded copy.
func streamDeepSeekOCR(c *gin.Context, reader io.Reader, limit int) (pages int, clientGone bool, err error) {
	buf := make([]byte, 32<<10)
	var captured []byte
	jsonStarted, overflow, streaming := false, false, false
	write := func(data []byte) {
		if !clientGone {
			if _, writeErr := c.Writer.Write(data); writeErr != nil {
				clientGone = true
				return
			}
			c.Writer.Flush()
		}
	}
	for {
		n, readErr := reader.Read(buf)
		chunk := buf[:n]
		if !jsonStarted && n > 0 {
			i := 0
			for i < n && (chunk[i] == ' ' || chunk[i] == '\n' || chunk[i] == '\r' || chunk[i] == '\t') {
				i++
			}
			if i > 0 {
				write(chunk[:i])
			}
			chunk = chunk[i:]
			jsonStarted = len(chunk) > 0
		}
		if len(chunk) > 0 {
			remaining := limit - len(captured)
			if remaining > 0 {
				captured = append(captured, chunk[:min(remaining, len(chunk))]...)
			}
			if streaming {
				write(chunk)
			} else if prefix := captured[:min(len(captured), 64<<10)]; !gjson.GetBytes(prefix, "error").Exists() &&
				(gjson.GetBytes(prefix, "object").String() == "ocr.result" ||
					gjson.GetBytes(prefix, "pages").IsArray() ||
					strings.HasPrefix(gjson.GetBytes(prefix, "id").String(), "ocr-") ||
					gjson.GetBytes(prefix, "page_count").Int() > 0) {
				streaming = true
				write(captured)
				if len(chunk) > remaining {
					write(chunk[max(0, remaining):])
				}
			}
			if len(chunk) > remaining {
				overflow = true
			}
		}
		if readErr == io.EOF {
			break
		}
		if readErr != nil {
			return 0, clientGone, readErr
		}
	}
	data := captured
	if !overflow {
		if !streaming {
			if clientPrivacyErrorValue(gjson.GetBytes(data, "error")) {
				setOpsUpstreamError(c, http.StatusOK, sanitizeUpstreamErrorMessage(extractUpstreamErrorMessage(data)), truncateString(string(data), 2048))
			}
			write(sanitizeClientUpstreamErrorBody(http.StatusOK, data))
		}
		var payload struct {
			Pages int             `json:"page_count"`
			Error json.RawMessage `json:"error"`
		}
		if json.Unmarshal(data, &payload) != nil || len(payload.Error) != 0 {
			return 0, clientGone, nil
		}
		return payload.Pages, clientGone, nil
	}
	if !streaming {
		write([]byte(`{"error":{"type":"upstream_error","message":"OCR response exceeds size limit"}}`))
	}
	if !gjson.GetBytes(data, "error").Exists() {
		// gjson accepts an incomplete document; require a complete positive
		// top-level number in the retained prefix, not a truncated digit string.
		value := gjson.GetBytes(data, "page_count")
		end := value.Index + len(value.Raw)
		if value.Type == gjson.Number && value.Int() > 0 && value.Float() == float64(value.Int()) && end < len(data) && strings.ContainsRune(",} \t\r\n", rune(data[end])) {
			return int(value.Int()), clientGone, nil
		}
	}
	slog.Error("deepseek_ocr.billing_page_count_missing", "capture_limit", limit)
	return 0, clientGone, nil
}
