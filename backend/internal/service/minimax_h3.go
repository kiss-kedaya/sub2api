// Adapted from ranxi2001/sub2api @ 9620d315, LGPL-3.0-or-later.
package service

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"math"
	"mime"
	"mime/multipart"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/gin-gonic/gin"
	"github.com/tidwall/gjson"
	"github.com/tidwall/sjson"
)

const (
	MiniMaxH3EndpointCreate           GrokMediaEndpoint        = "minimax_h3_create"
	MiniMaxH3EndpointStatus           GrokMediaEndpoint        = "minimax_h3_status"
	MiniMaxH3EndpointContent          GrokMediaEndpoint        = "minimax_h3_content"
	OpenAIEndpointCapabilityMiniMaxH3 OpenAIEndpointCapability = "minimax_h3"
	MiniMaxH3PriceFamily                                       = "minimax-h3"
	MiniMaxH3PerSecond                                         = "per_second"
	MiniMaxH3PerRequest                                        = "per_request"
	miniMaxH3MaxMultipartFieldBytes                            = 64 << 10
)

func (e GrokMediaEndpoint) IsMiniMaxH3() bool {
	return e == MiniMaxH3EndpointCreate || e == MiniMaxH3EndpointStatus || e == MiniMaxH3EndpointContent
}

func MiniMaxH3TaskKey(id string) string { return "minimax-h3:" + strings.TrimSpace(id) }

func parseMiniMaxH3SecondsString(raw string) (int, error) {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return 0, nil
	}
	duration, err := strconv.Atoi(trimmed)
	if err != nil || duration < 1 || duration > MiniMaxH3VideoBillingMaxDurationSeconds || trimmed != strconv.Itoa(duration) {
		return 0, fmt.Errorf("seconds must be an integer from 1 to %d", MiniMaxH3VideoBillingMaxDurationSeconds)
	}
	return duration, nil
}

// H3 uses the OpenAI Videos protocol (JSON or multipart/form-data), not MiniMax's
// native video API. Keep the original payload so frame/reference media and future
// fields survive untouched.
func ParseMiniMaxH3Request(body []byte) (GrokMediaRequestInfo, error) {
	return ParseMiniMaxH3RequestWithContentType("application/json", body)
}

func ParseMiniMaxH3RequestWithContentType(contentType string, body []byte) (GrokMediaRequestInfo, error) {
	if mediaType, params, err := mime.ParseMediaType(contentType); err == nil && mediaType == "multipart/form-data" {
		return parseMiniMaxH3MultipartRequest(params["boundary"], body)
	}
	info := GrokMediaRequestInfo{N: 1, Resolution: "768p"}
	if !gjson.ValidBytes(body) || !gjson.ParseBytes(body).IsObject() {
		return info, fmt.Errorf("request body must be a JSON object")
	}
	model, prompt, seconds := gjson.GetBytes(body, "model"), gjson.GetBytes(body, "prompt"), gjson.GetBytes(body, "seconds")
	if model.Type != gjson.String || strings.TrimSpace(model.String()) == "" {
		return info, fmt.Errorf("model is required")
	}
	if prompt.Type != gjson.String || strings.TrimSpace(prompt.String()) == "" || utf8.RuneCountInString(prompt.String()) > 7000 {
		return info, fmt.Errorf("prompt must contain 1 to 7000 characters")
	}
	duration := 0
	if seconds.Exists() && seconds.Type != gjson.Null {
		switch seconds.Type {
		case gjson.String:
			if strings.TrimSpace(seconds.String()) == "" {
				return info, fmt.Errorf("seconds must be an integer from 1 to %d", MiniMaxH3VideoBillingMaxDurationSeconds)
			}
			parsed, err := parseMiniMaxH3SecondsString(seconds.String())
			if err != nil {
				return info, err
			}
			duration = parsed
		case gjson.Number:
			f := seconds.Float()
			if math.IsNaN(f) || math.IsInf(f, 0) || f != math.Trunc(f) || f < 1 || f > float64(MiniMaxH3VideoBillingMaxDurationSeconds) {
				return info, fmt.Errorf("seconds must be an integer from 1 to %d", MiniMaxH3VideoBillingMaxDurationSeconds)
			}
			duration = int(f)
		default:
			return info, fmt.Errorf("seconds must be an integer from 1 to %d", MiniMaxH3VideoBillingMaxDurationSeconds)
		}
	}
	// H3 creates one task/video. Reject batching fields rather than undercharge.
	if n := gjson.GetBytes(body, "n"); n.Exists() && (n.Type != gjson.Number || n.Float() != 1) {
		return info, fmt.Errorf("MiniMax-H3 supports one video per request")
	}
	info.Model, info.Prompt, info.DurationSeconds = strings.TrimSpace(model.String()), prompt.String(), duration
	info.Size = gjson.GetBytes(body, "size").String()
	for _, item := range gjson.GetBytes(body, "metadata.content").Array() {
		if item.Get("type").String() == "image_url" {
			info.InputImageURLs = append(info.InputImageURLs, item.Get("image_url.url").String())
		}
	}
	return info, nil
}

func parseMiniMaxH3MultipartRequest(boundary string, body []byte) (GrokMediaRequestInfo, error) {
	info := GrokMediaRequestInfo{N: 1, Resolution: "768p"}
	if strings.TrimSpace(boundary) == "" {
		return info, fmt.Errorf("invalid multipart/form-data boundary")
	}
	reader := multipart.NewReader(bytes.NewReader(body), boundary)
	var modelVal, promptVal, secondsVal, sizeVal, nVal string
	hasModel, hasSeconds, hasN := false, false, false
	for {
		part, err := reader.NextPart()
		if err == io.EOF {
			break
		}
		if err != nil {
			return info, fmt.Errorf("invalid multipart/form-data body")
		}
		name := part.FormName()
		if part.FileName() != "" {
			_, _ = io.Copy(io.Discard, part)
			_ = part.Close()
			continue
		}
		switch name {
		case "model", "prompt", "seconds", "size", "n":
			raw, readErr := io.ReadAll(io.LimitReader(part, miniMaxH3MaxMultipartFieldBytes+1))
			_ = part.Close()
			if readErr != nil || len(raw) > miniMaxH3MaxMultipartFieldBytes {
				return info, fmt.Errorf("multipart field %q is invalid or too large", name)
			}
			val := string(raw)
			switch name {
			case "model":
				trimmed := strings.TrimSpace(val)
				if hasModel && trimmed != modelVal {
					return info, ErrDuplicateModelField
				}
				modelVal, hasModel = trimmed, true
			case "prompt":
				promptVal = val
			case "seconds":
				secondsVal, hasSeconds = val, true
			case "size":
				sizeVal = strings.TrimSpace(val)
			case "n":
				nVal, hasN = strings.TrimSpace(val), true
			}
		default:
			_, _ = io.Copy(io.Discard, part)
			_ = part.Close()
		}
	}
	if !hasModel || modelVal == "" {
		return info, fmt.Errorf("model is required")
	}
	if strings.TrimSpace(promptVal) == "" || utf8.RuneCountInString(promptVal) > 7000 {
		return info, fmt.Errorf("prompt must contain 1 to 7000 characters")
	}
	duration := 0
	if hasSeconds {
		if strings.TrimSpace(secondsVal) == "" {
			return info, fmt.Errorf("seconds must be an integer from 1 to %d", MiniMaxH3VideoBillingMaxDurationSeconds)
		}
		parsed, err := parseMiniMaxH3SecondsString(secondsVal)
		if err != nil {
			return info, err
		}
		duration = parsed
	}
	if hasN && nVal != "1" {
		return info, fmt.Errorf("MiniMax-H3 supports one video per request")
	}
	info.Model, info.Prompt, info.DurationSeconds, info.Size = modelVal, promptVal, duration, sizeVal
	return info, nil
}

func rewriteMiniMaxH3MultipartModel(contentType string, body []byte, newModel string) ([]byte, string, error) {
	mediaType, params, err := mime.ParseMediaType(contentType)
	if err != nil || mediaType != "multipart/form-data" || strings.TrimSpace(params["boundary"]) == "" {
		return body, contentType, nil
	}
	reader := multipart.NewReader(bytes.NewReader(body), params["boundary"])
	var out bytes.Buffer
	writer := multipart.NewWriter(&out)
	replaced := false
	for {
		part, err := reader.NextPart()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, "", fmt.Errorf("invalid multipart/form-data body")
		}
		if part.FileName() == "" && part.FormName() == "model" {
			_, _ = io.Copy(io.Discard, part)
			_ = part.Close()
			if !replaced {
				if writeErr := writer.WriteField("model", newModel); writeErr != nil {
					return nil, "", writeErr
				}
				replaced = true
			}
			continue
		}
		dst, createErr := writer.CreatePart(part.Header)
		if createErr != nil {
			_ = part.Close()
			return nil, "", createErr
		}
		_, copyErr := io.Copy(dst, part)
		_ = part.Close()
		if copyErr != nil {
			return nil, "", copyErr
		}
	}
	if !replaced {
		if err := writer.WriteField("model", newModel); err != nil {
			return nil, "", err
		}
	}
	if err := writer.Close(); err != nil {
		return nil, "", err
	}
	return out.Bytes(), writer.FormDataContentType(), nil
}

// Reuse the group's versioned media JSONB: one per_second tariff, a flat
// per_request tariff (any duration up to 30s), or a fixed request_<seconds>s
// tariff. No currency conversion or resolution fallback.
func miniMaxH3RequestSeconds(key string) (int, bool) {
	if !strings.HasPrefix(key, "request_") || !strings.HasSuffix(key, "s") {
		return 0, false
	}
	n, err := strconv.Atoi(strings.TrimSuffix(strings.TrimPrefix(key, "request_"), "s"))
	return n, err == nil && n >= 1 && n <= MiniMaxH3VideoBillingMaxDurationSeconds && key == fmt.Sprintf("request_%ds", n)
}

func QuoteMiniMaxH3Video(prices map[string]map[string]float64, seconds int) (float64, error) {
	if seconds < 0 || seconds > MiniMaxH3VideoBillingMaxDurationSeconds {
		return 0, fmt.Errorf("seconds must be between 1 and %d", MiniMaxH3VideoBillingMaxDurationSeconds)
	}
	tariff := prices[MiniMaxH3PriceFamily]
	if len(tariff) != 1 {
		return 0, fmt.Errorf("MiniMax-H3 video is disabled or its group price is not configured")
	}
	for key, price := range tariff {
		if price < 0 || math.IsNaN(price) || math.IsInf(price, 0) {
			break
		}
		if key == MiniMaxH3PerRequest {
			return price, nil
		}
		if key == MiniMaxH3PerSecond {
			if seconds < 1 {
				return 0, fmt.Errorf("seconds must be between 1 and %d for per-second video pricing", MiniMaxH3VideoBillingMaxDurationSeconds)
			}
			total := price * float64(seconds)
			if !math.IsInf(total, 0) {
				return total, nil
			}
		}
		if duration, ok := miniMaxH3RequestSeconds(key); ok {
			if seconds != duration {
				return 0, fmt.Errorf("this group's per-request video price requires seconds=\"%d\"", duration)
			}
			return price, nil
		}
	}
	return 0, fmt.Errorf("invalid MiniMax-H3 group video price")
}

func buildMiniMaxH3URL(base string, endpoint GrokMediaEndpoint, taskID string) (string, error) {
	base = strings.TrimRight(base, "/")
	if !strings.HasSuffix(base, "/v1") {
		base += "/v1"
	}
	base += "/videos"
	if endpoint != MiniMaxH3EndpointCreate {
		if err := validateUpstreamPathSegment("MiniMax-H3 task ID", taskID); err != nil || strings.TrimSpace(taskID) == "" {
			return "", fmt.Errorf("invalid MiniMax-H3 task ID")
		}
		base += "/" + taskID
		if endpoint == MiniMaxH3EndpointContent {
			base += "/content"
		}
	}
	return base, nil
}

func validMiniMaxH3OutputURL(raw string) bool {
	u, err := url.Parse(raw)
	return err == nil && u.Scheme == "https" && u.Hostname() != "" && u.User == nil
}

func isMiniMaxH3ContentRedirect(statusCode int) bool {
	return statusCode == http.StatusFound || statusCode == http.StatusSeeOther || statusCode == http.StatusTemporaryRedirect
}

func (s *OpenAIGatewayService) ForwardMiniMaxH3(ctx context.Context, c *gin.Context, account *Account, endpoint GrokMediaEndpoint, taskID string, body []byte) (*OpenAIForwardResult, error) {
	return s.ForwardMiniMaxH3WithContentType(ctx, c, account, endpoint, taskID, body, "")
}

func (s *OpenAIGatewayService) ForwardMiniMaxH3WithContentType(ctx context.Context, c *gin.Context, account *Account, endpoint GrokMediaEndpoint, taskID string, body []byte, contentType string) (*OpenAIForwardResult, error) {
	if account == nil || !endpoint.IsMiniMaxH3() || !account.SupportsOpenAIEndpointCapability(OpenAIEndpointCapabilityMiniMaxH3) {
		return nil, fmt.Errorf("MiniMax-H3 requires a MiniMax pay-as-you-go API key account with a custom base URL")
	}
	base, err := s.validateUpstreamBaseURL(account.GetCredential("base_url"))
	if err != nil {
		return nil, err
	}
	target, err := buildMiniMaxH3URL(base, endpoint, strings.TrimPrefix(taskID, "minimax-h3:"))
	if err != nil {
		return nil, err
	}
	method := http.MethodGet
	upstreamContentType := "application/json"
	result := &OpenAIForwardResult{ResponseID: taskID}
	if endpoint == MiniMaxH3EndpointCreate {
		info, err := ParseMiniMaxH3RequestWithContentType(contentType, body)
		if err != nil {
			return nil, err
		}
		mediaType, _, _ := mime.ParseMediaType(contentType)
		isMultipart := mediaType == "multipart/form-data"
		effectiveModel := info.Model
		// compositeTargetPlatformMiddleware rewrites JSON bodies only; apply its
		// upstream_model alias here for multipart/form-data requests too (#720).
		if isMultipart {
			if resolved, ok := ResolvedUpstreamModelFromContext(ctx); ok && resolved != "" {
				effectiveModel = resolved
			}
		}
		upstreamModel := strings.TrimSpace(account.GetMappedModel(effectiveModel))
		if upstreamModel == "" {
			upstreamModel = effectiveModel
		}
		result.Model, result.BillingModel, result.UpstreamModel = info.Model, info.Model, upstreamModel
		result.VideoResolution, result.VideoDurationSeconds = info.Resolution, info.DurationSeconds
		if isMultipart {
			if upstreamModel != info.Model {
				body, upstreamContentType, err = rewriteMiniMaxH3MultipartModel(contentType, body, upstreamModel)
				if err != nil {
					return nil, err
				}
			} else {
				upstreamContentType = contentType
			}
		} else {
			body, err = sjson.SetBytes(body, "model", result.UpstreamModel)
			if err != nil {
				return nil, err
			}
		}
		method = http.MethodPost
	}
	token := strings.TrimSpace(account.GetCredential("api_key"))
	if token == "" {
		return nil, fmt.Errorf("MiniMax-H3 account missing api_key")
	}
	started := time.Now()
	// Never follow credential-bearing redirects, including same-host redirects.
	upstreamCtx, releaseCtx := detachUpstreamContext(ctx)
	defer releaseCtx()
	req, err := http.NewRequestWithContext(WithHTTPUpstreamRedirectsDisabled(upstreamCtx), method, target, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	account.ApplyHeaderOverrides(req.Header)
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", upstreamContentType)
	req.Header.Set("Accept", "application/json")
	proxy := ""
	if account.ProxyID != nil && account.Proxy != nil {
		proxy = account.Proxy.URL()
	}
	if endpoint == MiniMaxH3EndpointCreate {
		if owner, ok := grokVideoBindOwnerFrom(ctx); ok && owner.OnRequestStarted != nil {
			owner.OnRequestStarted()
		}
	}
	SetActualOpenAIUpstreamEndpoint(c, "/v1/videos")
	resp, err := s.httpUpstream.Do(req, proxy, account.ID, account.Concurrency)
	SetOpsLatencyMs(c, OpsUpstreamLatencyMsKey, time.Since(started).Milliseconds())
	if err != nil {
		return nil, fmt.Errorf("MiniMax-H3 upstream transport failed")
	}
	defer func() { _ = resp.Body.Close() }()
	result.Duration, result.ResponseHeaders = time.Since(started), resp.Header.Clone()
	if endpoint == MiniMaxH3EndpointContent && isMiniMaxH3ContentRedirect(resp.StatusCode) {
		location := resp.Header.Get("Location")
		if !validMiniMaxH3OutputURL(location) {
			return nil, fmt.Errorf("MiniMax-H3 content response has an invalid output URL")
		}
		result.VideoCount = 1
		return s.forwardVideoProviderContent(upstreamCtx, c, account, location, result)
	}
	if endpoint == MiniMaxH3EndpointContent && (resp.StatusCode == http.StatusOK || resp.StatusCode == http.StatusPartialContent || resp.StatusCode == http.StatusRequestedRangeNotSatisfiable) {
		mediaType := strings.ToLower(strings.TrimSpace(strings.Split(resp.Header.Get("Content-Type"), ";")[0]))
		if resp.StatusCode != http.StatusRequestedRangeNotSatisfiable {
			if !strings.HasPrefix(mediaType, "video/") && mediaType != "application/octet-stream" {
				return nil, fmt.Errorf("video content response is not video data")
			}
			result.VideoCount = 1
		}
		if err := runGrokMediaBeforePublish(ctx, result); err != nil {
			return nil, err
		}
		c.Header("Cache-Control", "private, no-store")
		if err := writeGrokMediaContentResponse(c, resp); err != nil {
			return nil, err
		}
		return result, nil
	}
	responseBody, err := ReadUpstreamResponseBody(resp.Body, s.cfg, c, openAITooLargeError)
	if err != nil {
		return nil, err
	}
	// An ambiguous POST must never become an automatic account-switch retry.
	if resp.StatusCode >= 300 {
		if endpoint == MiniMaxH3EndpointCreate && resp.StatusCode >= 400 {
			if err := rejectVideoProviderCreate(ctx); err != nil {
				return nil, err
			}
		}
		return nil, writeVideoProviderError(c, resp)
	}
	if !gjson.ValidBytes(responseBody) || !gjson.ParseBytes(responseBody).IsObject() {
		return nil, fmt.Errorf("invalid MiniMax-H3 response")
	}
	switch endpoint {
	case MiniMaxH3EndpointCreate:
		id := gjson.GetBytes(responseBody, "id").String()
		if id == "" {
			id = gjson.GetBytes(responseBody, "task_id").String()
		}
		if _, err := buildMiniMaxH3URL(base, MiniMaxH3EndpointStatus, id); err != nil {
			return nil, err
		}
		result.ResponseID = MiniMaxH3TaskKey(id)
		if err := s.acceptVideoProviderTask(ctx, account, result); err != nil {
			return nil, err
		}
	case MiniMaxH3EndpointStatus:
		result.VideoStatus = strings.ToLower(strings.TrimSpace(gjson.GetBytes(responseBody, "status").String()))
		rawURL := gjson.GetBytes(responseBody, "metadata.url").String()
		if result.VideoStatus == "completed" {
			// Accept both relays returning metadata.url and standard OpenAI Videos
			// responses (object="video") whose media is retrieved via /content (#720).
			if validMiniMaxH3OutputURL(rawURL) || (rawURL == "" && gjson.GetBytes(responseBody, "object").String() == "video") {
				result.VideoCount = 1
			}
		}
		if rawURL != "" {
			responseBody, err = sjson.SetBytes(responseBody, "metadata.url", videoProviderContentProxyURL(VideoProviderMiniMaxH3, taskID))
			if err != nil {
				return nil, err
			}
		}
	}
	if err := runGrokMediaBeforePublish(ctx, result); err != nil {
		return nil, err
	}
	c.Header("Cache-Control", "private, no-store")
	writeGrokMediaResponse(c, resp, responseBody, s.responseHeaderFilter)
	return result, nil
}
