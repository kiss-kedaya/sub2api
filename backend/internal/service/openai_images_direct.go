package service

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	"github.com/Wei-Shaw/sub2api/internal/util/responseheaders"
	"github.com/gin-gonic/gin"
	"github.com/tidwall/gjson"
	"github.com/tidwall/sjson"
)

type openAIImagesForceResponsesContextKey struct{}

// withOpenAIImagesForceResponses pins the next attempt to the Responses bridge.
// It is used when the native Codex Images endpoint is unavailable upstream
// (404/405), so an account whose plan no longer exposes the direct endpoint
// still gets its request served by the image_generation bridge.
func withOpenAIImagesForceResponses(ctx context.Context) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	return context.WithValue(ctx, openAIImagesForceResponsesContextKey{}, true)
}

func isOpenAIImagesForceResponses(ctx context.Context) bool {
	if ctx == nil {
		return false
	}
	forced, _ := ctx.Value(openAIImagesForceResponsesContextKey{}).(bool)
	return forced
}

// usesCodexDirectImages lists the models explicitly served by the native
// ChatGPT Codex Images endpoint (/backend-api/codex/images/{generations,edits})
// instead of the Responses image_generation bridge.
//
// The list stays explicit: a future or unknown snapshot must not be sent to an
// endpoint whose payload contract it may not understand. GPT Image 2.6 needs the
// native endpoint because the bridge pins its hosted image_generation tool to the
// internal gpt-image-2-codex model, which rejects the complexity control used by
// the 2.6 image models.
func usesCodexDirectImages(model string) bool {
	switch strings.ToLower(strings.TrimSpace(model)) {
	case "gpt-image-1.5", "gpt-image-2",
		"gpt-image-2.5-flare", "gpt-image-2.5-sunburst",
		"gpt-image-2.5-flare-2026-09-08", "gpt-image-2.5-sunburst-2026-09-08",
		"gpt-image-2.6-flare", "gpt-image-2.6-sunburst",
		"gpt-image-2.6-flare-2026-11-04", "gpt-image-2.6-sunburst-2026-11-04":
		return true
	default:
		return false
	}
}

// buildOpenAIImagesOAuthPayload chooses the upstream endpoint and request body
// for an OAuth image request. Forwarding and account tests share it so both use
// the same routing and validation.
func buildOpenAIImagesOAuthPayload(parsed *OpenAIImagesRequest, model string) ([]byte, string, error) {
	if parsed == nil {
		return nil, "", fmt.Errorf("parsed images request is required")
	}
	model = strings.TrimSpace(model)
	if !usesCodexDirectImages(model) {
		body, err := buildOpenAIImagesResponsesRequest(parsed, model)
		return body, chatgptCodexURL, err
	}
	if strings.TrimSpace(parsed.Prompt) == "" {
		return nil, "", fmt.Errorf("prompt is required")
	}
	// Send only parsed and validated image fields upstream; never forward the
	// raw client body, which could inject arbitrary JSON fields.
	payload := make(map[string]any, 16)
	payload["model"] = model
	prompt := parsed.Prompt
	if !parsed.Multipart && gjson.ValidBytes(parsed.Body) {
		if rawPrompt := gjson.GetBytes(parsed.Body, "prompt").String(); rawPrompt != "" {
			prompt = rawPrompt
		}
	}
	payload["prompt"] = prompt
	for _, field := range []struct {
		key   string
		value string
	}{
		{"size", parsed.Size}, {"quality", parsed.Quality}, {"complexity", parsed.Complexity},
		{"background", parsed.Background}, {"output_format", parsed.OutputFormat},
		{"moderation", parsed.Moderation}, {"input_fidelity", parsed.InputFidelity},
		{"style", parsed.Style},
	} {
		if value := strings.TrimSpace(field.value); value != "" {
			payload[field.key] = value
		}
	}
	if parsed.N > 1 {
		payload["n"] = parsed.N
	}
	if parsed.OutputCompression != nil {
		payload["output_compression"] = *parsed.OutputCompression
	}
	if parsed.PartialImages != nil {
		payload["partial_images"] = *parsed.PartialImages
	}
	// GIF animation controls are part of the native Images contract; forward
	// them verbatim and leave format/range validation to the upstream.
	if parsed.FrameCount != nil {
		payload["frame_count"] = *parsed.FrameCount
	}
	if parsed.FrameDurationMS != nil {
		payload["frame_duration_ms"] = *parsed.FrameDurationMS
	}
	if parsed.Stream {
		payload["stream"] = true
	}

	endpoint := "/images/generations"
	if parsed.IsEdits() {
		endpoint = "/images/edits"
		images := make([]map[string]string, 0, len(parsed.InputImageURLs)+len(parsed.Uploads))
		for _, imageURL := range parsed.InputImageURLs {
			if imageURL = strings.TrimSpace(imageURL); imageURL != "" {
				images = append(images, map[string]string{"image_url": imageURL})
			}
		}
		for _, upload := range parsed.Uploads {
			url, err := openAIImageUploadToDataURL(upload)
			if err != nil {
				return nil, "", err
			}
			images = append(images, map[string]string{"image_url": url})
		}
		if len(images) == 0 {
			return nil, "", fmt.Errorf("image input is required")
		}
		payload["images"] = images
		mask := strings.TrimSpace(parsed.MaskImageURL)
		if parsed.MaskUpload != nil {
			var err error
			mask, err = openAIImageUploadToDataURL(*parsed.MaskUpload)
			if err != nil {
				return nil, "", err
			}
		}
		if mask != "" {
			payload["mask"] = map[string]string{"image_url": mask}
		}
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return nil, "", fmt.Errorf("marshal Codex Images request: %w", err)
	}
	return body, strings.TrimSuffix(chatgptCodexURL, "/responses") + endpoint, nil
}

// parseCodexDirectImagesResponse validates a native Images API response and
// returns its image results. The background account test previews images through
// the same parser, so empty payloads never count as success.
func parseCodexDirectImagesResponse(body []byte) ([]openAIResponsesImageResult, error) {
	if !gjson.ValidBytes(body) {
		return nil, fmt.Errorf("invalid Images API JSON response")
	}
	if upstreamErr := openAIImagesUpstreamErrorFromSSEPayload(body); upstreamErr != nil {
		return nil, upstreamErr
	}
	root := gjson.ParseBytes(body)
	if upstreamErr := openAIImagesUpstreamErrorFromGJSON(root.Get("error"), ""); upstreamErr != nil {
		return nil, upstreamErr
	}
	var results []openAIResponsesImageResult
	for _, item := range root.Get("data").Array() {
		result := strings.TrimSpace(item.Get("b64_json").String())
		if result == "" {
			continue
		}
		meta := func(key string) string {
			if value := item.Get(key).String(); value != "" {
				return value
			}
			return root.Get(key).String()
		}
		results = append(results, openAIResponsesImageResult{
			Result:        result,
			RevisedPrompt: item.Get("revised_prompt").String(),
			OutputFormat:  meta("output_format"),
			Size:          meta("size"),
			Quality:       meta("quality"),
			Background:    meta("background"),
			Model:         meta("model"),
		})
	}
	if len(results) == 0 {
		return nil, &OpenAIImagesUpstreamError{StatusCode: http.StatusBadGateway, ErrorType: "upstream_error", Message: "Images API returned no image output"}
	}
	reconcileOpenAIResponsesImageResultSizes(results, nil)
	return results, nil
}

// codexDirectImageURL converts a native b64_json image into the data URL the
// images endpoint exposes for response_format=url.
func codexDirectImageURL(body []byte, path, outputFormat string) []byte {
	if result := gjson.GetBytes(body, path+"b64_json").String(); result != "" {
		body, _ = sjson.SetBytes(body, path+"url", "data:"+openAIImageOutputMIMEType(outputFormat)+";base64,"+result)
		body, _ = sjson.DeleteBytes(body, path+"b64_json")
	}
	return body
}

// isOpenAIImagesMainModelError reports whether the upstream rejected the
// configured Responses driver model rather than the requested image model. A
// retired driver is not an image-model quota failure and must not cool down the
// whole image account pool.
func isOpenAIImagesMainModelError(status int, body []byte) bool {
	if !isOpenAICodexPlanGatedModelError(status, body) {
		return false
	}
	message := extractUpstreamErrorMessage(body)
	model := openAIImagesResponsesMainModelValue()
	return strings.Contains(message, "'"+model+"'") || strings.Contains(message, `"`+model+`"`)
}

// codexDirectImagesUsage maps the native Images API usage shape onto OpenAIUsage.
// The endpoint only returns images, so output tokens without an explicit image
// breakdown are image tokens. Cached image tokens are only taken from explicit
// details instead of guessing an image/text split from the total cache count.
func codexDirectImagesUsage(body []byte) (OpenAIUsage, bool) {
	value := gjson.GetBytes(body, "usage")
	usage, ok := openAIUsageFromGJSON(value)
	if !ok {
		return usage, false
	}
	if !value.Get("output_tokens_details.image_tokens").Exists() {
		usage.ImageOutputTokens = usage.OutputTokens
	}
	cached := value.Get("input_tokens_details.cached_tokens_details")
	if !value.Get("input_tokens_details.cached_tokens").Exists() && cached.IsObject() {
		imageTokens, _ := boundedJSONNonNegativeInt(cached.Get("image_tokens"))
		textTokens, _ := boundedJSONNonNegativeInt(cached.Get("text_tokens"))
		usage.CacheReadInputTokens = min(imageTokens, max(usage.InputTokens, 0))
		usage.CacheReadInputTokens += min(textTokens, max(usage.InputTokens-usage.CacheReadInputTokens, 0))
	}
	imageCached, _ := boundedJSONNonNegativeInt(cached.Get("image_tokens"))
	usage.ImageCacheReadTokens = min(imageCached, max(usage.ImageInputTokens, 0), max(usage.CacheReadInputTokens, 0))
	return usage, true
}

func (s *OpenAIGatewayService) handleCodexDirectImagesNonStreamingResponse(
	resp *http.Response,
	c *gin.Context,
	parsed *OpenAIImagesRequest,
) (OpenAIUsage, int, []string, error) {
	body, err := ReadUpstreamResponseBody(resp.Body, s.cfg, c, openAITooLargeError)
	if err != nil {
		if shouldClassifyOpenAIUpstreamStreamReadError(err) {
			err = newOpenAIUpstreamStreamReadError(err)
		}
		return OpenAIUsage{}, 0, nil, err
	}
	// The native Codex Images endpoint may normalize GIF animation controls to
	// its own defaults; repair the returned animation so the request's
	// frame_count/frame_duration_ms/size are honored.
	body = normalizeOpenAIImagesGIFAnimationResponseBody(body, parsed)
	results, err := parseCodexDirectImagesResponse(body)
	if err != nil {
		return OpenAIUsage{}, 0, nil, err
	}
	usage, _ := codexDirectImagesUsage(body)
	if observer := upstreamResponseModelObserverFromContext(c); observer != nil {
		observer.Observe(gjson.GetBytes(body, "model").String(), true)
		for _, result := range results {
			observer.Observe(result.Model, true)
		}
	}
	if clientModel := strings.TrimSpace(parsed.Model); clientModel != "" {
		for i := range gjson.GetBytes(body, "data").Array() {
			body, _ = sjson.SetBytes(body, fmt.Sprintf("data.%d.model", i), clientModel)
		}
	}
	for i, item := range gjson.GetBytes(body, "data").Array() {
		if actualSize := detectOpenAIImageResultSize(item.Get("b64_json").String()); actualSize != "" {
			body, _ = sjson.SetBytes(body, fmt.Sprintf("data.%d.size", i), actualSize)
			if i == 0 {
				body, _ = sjson.SetBytes(body, "size", actualSize)
			}
		}
	}
	if parsed.ResponseFormat == "url" {
		for i, item := range gjson.GetBytes(body, "data").Array() {
			format := item.Get("output_format").String()
			if format == "" {
				format = gjson.GetBytes(body, "output_format").String()
			}
			if format == "" {
				format = parsed.OutputFormat
			}
			body = codexDirectImageURL(body, fmt.Sprintf("data.%d.", i), format)
		}
	}
	responseheaders.WriteFilteredHeaders(c.Writer.Header(), resp.Header, s.responseHeaderFilter)
	contentType := "application/json"
	if s.cfg != nil && !s.cfg.Security.ResponseHeaders.Enabled {
		if upstreamType := strings.TrimSpace(resp.Header.Get("Content-Type")); upstreamType != "" {
			contentType = upstreamType
		}
	}
	c.Data(resp.StatusCode, contentType, body)
	return usage, len(results), openAIResponsesImageResultSizes(results), nil
}
