package service

import (
	"bytes"
	"context"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

func newDirectImagesOAuthAccount() *Account {
	return &Account{
		ID:       11,
		Name:     "openai-oauth-images",
		Platform: PlatformOpenAI,
		Type:     AccountTypeOAuth,
		Credentials: map[string]any{
			"access_token":       "token-123",
			"chatgpt_account_id": "acct-123",
		},
	}
}

func TestUsesCodexDirectImages(t *testing.T) {
	for _, model := range []string{
		"gpt-image-1.5",
		"gpt-image-2",
		"gpt-image-2.5-flare",
		"gpt-image-2.5-sunburst",
		"gpt-image-2.5-flare-2026-09-08",
		"gpt-image-2.6-flare",
		"gpt-image-2.6-sunburst",
		"GPT-Image-2.6-Flare",
		"gpt-image-2.6-sunburst-2026-11-04",
	} {
		require.True(t, usesCodexDirectImages(model), model)
	}
	for _, model := range []string{"", "gpt-image-1", "gpt-image-2-codex", "gpt-image-2.6", "gpt-5.6-luna"} {
		require.False(t, usesCodexDirectImages(model), model)
	}
}

func TestBuildOpenAIImagesOAuthPayload_DirectGenerations(t *testing.T) {
	compression := 80
	frames := 12
	frameDuration := 150
	parsed := &OpenAIImagesRequest{
		Endpoint:          openAIImagesGenerationsEndpoint,
		Model:             "gpt-image-2.6-flare",
		Prompt:            "draw a fox",
		Size:              "1024x1024",
		Quality:           "xhigh",
		Complexity:        "high",
		Background:        "transparent",
		OutputFormat:      "svg",
		N:                 2,
		Stream:            true,
		OutputCompression: &compression,
		FrameCount:        &frames,
		FrameDurationMS:   &frameDuration,
	}

	body, targetURL, err := buildOpenAIImagesOAuthPayload(parsed, "gpt-image-2.6-flare")
	require.NoError(t, err)
	require.Equal(t, "https://chatgpt.com/backend-api/codex/images/generations", targetURL)
	require.Equal(t, "gpt-image-2.6-flare", gjson.GetBytes(body, "model").String())
	require.Equal(t, "draw a fox", gjson.GetBytes(body, "prompt").String())
	require.Equal(t, "1024x1024", gjson.GetBytes(body, "size").String())
	require.Equal(t, "xhigh", gjson.GetBytes(body, "quality").String())
	require.Equal(t, "high", gjson.GetBytes(body, "complexity").String())
	require.Equal(t, "transparent", gjson.GetBytes(body, "background").String())
	require.Equal(t, "svg", gjson.GetBytes(body, "output_format").String())
	require.Equal(t, int64(2), gjson.GetBytes(body, "n").Int())
	require.True(t, gjson.GetBytes(body, "stream").Bool())
	require.Equal(t, int64(80), gjson.GetBytes(body, "output_compression").Int())
	require.Equal(t, int64(12), gjson.GetBytes(body, "frame_count").Int())
	require.Equal(t, int64(150), gjson.GetBytes(body, "frame_duration_ms").Int())
	require.False(t, gjson.GetBytes(body, "response_format").Exists())
	require.False(t, gjson.GetBytes(body, "tools").Exists())
	require.False(t, gjson.GetBytes(body, "input").Exists())
}

func TestBuildOpenAIImagesOAuthPayload_DirectEditsCarriesImagesAndMask(t *testing.T) {
	mask := &OpenAIImagesUpload{FileName: "mask.png", ContentType: "image/png", Data: []byte("mask")}
	parsed := &OpenAIImagesRequest{
		Endpoint:      openAIImagesEditsEndpoint,
		Model:         "gpt-image-2.6-sunburst",
		Prompt:        "add a hat",
		Uploads:       []OpenAIImagesUpload{{FileName: "cat.png", ContentType: "image/png", Data: []byte("cat")}},
		MaskUpload:    mask,
		InputFidelity: "high",
	}

	body, targetURL, err := buildOpenAIImagesOAuthPayload(parsed, "gpt-image-2.6-sunburst")
	require.NoError(t, err)
	require.Equal(t, "https://chatgpt.com/backend-api/codex/images/edits", targetURL)
	require.Equal(t, "data:image/png;base64,Y2F0", gjson.GetBytes(body, "images.0.image_url").String())
	require.Equal(t, "data:image/png;base64,bWFzaw==", gjson.GetBytes(body, "mask.image_url").String())
	require.Equal(t, "high", gjson.GetBytes(body, "input_fidelity").String())
}

func TestBuildOpenAIImagesOAuthPayload_KeepsBridgeForLegacyModels(t *testing.T) {
	parsed := &OpenAIImagesRequest{
		Endpoint:   openAIImagesGenerationsEndpoint,
		Model:      "gpt-image-1",
		Prompt:     "draw a cat",
		Complexity: "max",
	}

	body, targetURL, err := buildOpenAIImagesOAuthPayload(parsed, "gpt-image-1")
	require.NoError(t, err)
	require.Equal(t, chatgptCodexURL, targetURL)
	require.Equal(t, openAIImagesResponsesMainModel, gjson.GetBytes(body, "model").String())
	require.Equal(t, "gpt-image-1", gjson.GetBytes(body, "tools.0.model").String())
	require.Equal(t, "max", gjson.GetBytes(body, "tools.0.complexity").String())
}

func TestParseOpenAIImagesRequest_Complexity(t *testing.T) {
	svc := &OpenAIGatewayService{}
	body := []byte(`{"model":"gpt-image-2.6-flare","prompt":"draw","complexity":"high"}`)
	c, _ := newOpenAIImagesTestContext(t, body)
	parsed, err := svc.ParseOpenAIImagesRequest(c, body)
	require.NoError(t, err)
	require.Equal(t, "high", parsed.Complexity)
	require.True(t, parsed.HasNativeOptions)
}

func TestParseOpenAIImagesRequest_MultipartComplexity(t *testing.T) {
	svc := &OpenAIGatewayService{}
	var payload bytes.Buffer
	writer := multipart.NewWriter(&payload)
	require.NoError(t, writer.WriteField("model", "gpt-image-2.6-sunburst"))
	require.NoError(t, writer.WriteField("prompt", "draw"))
	require.NoError(t, writer.WriteField("complexity", "max"))
	require.NoError(t, writer.Close())

	req := httptest.NewRequest(http.MethodPost, "/v1/images/generations", bytes.NewReader(payload.Bytes()))
	req.Header.Set("Content-Type", writer.FormDataContentType())
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = req

	parsed, err := svc.ParseOpenAIImagesRequest(c, payload.Bytes())
	require.NoError(t, err)
	require.True(t, parsed.Multipart)
	require.Equal(t, "max", parsed.Complexity)
	require.True(t, parsed.HasNativeOptions)
}

func TestOpenAIImageOutputMIMEType_GIFAndSVG(t *testing.T) {
	require.Equal(t, "image/gif", openAIImageOutputMIMEType("gif"))
	require.Equal(t, "image/svg+xml", openAIImageOutputMIMEType("SVG"))
	require.Equal(t, "image/png", openAIImageOutputMIMEType(""))
}

func TestParseCodexDirectImagesResponse(t *testing.T) {
	results, err := parseCodexDirectImagesResponse([]byte(
		`{"created":1710000000,"model":"gpt-image-2.6-flare","data":[{"b64_json":"aW1hZ2U=","revised_prompt":"draw a fox","output_format":"gif","size":"1024x1024","quality":"xhigh"}]}`,
	))
	require.NoError(t, err)
	require.Len(t, results, 1)
	require.Equal(t, "aW1hZ2U=", results[0].Result)
	require.Equal(t, "draw a fox", results[0].RevisedPrompt)
	require.Equal(t, "gif", results[0].OutputFormat)
	require.Equal(t, "gpt-image-2.6-flare", results[0].Model)

	_, err = parseCodexDirectImagesResponse([]byte(`{"created":1,"data":[]}`))
	require.Error(t, err)
	_, err = parseCodexDirectImagesResponse([]byte(`not-json`))
	require.Error(t, err)
	_, err = parseCodexDirectImagesResponse([]byte(`{"error":{"type":"invalid_request_error","message":"The selected image model is unavailable"}}`))
	require.ErrorContains(t, err, "The selected image model is unavailable")
}

func TestCodexDirectImagesUsage(t *testing.T) {
	usage, ok := codexDirectImagesUsage([]byte(
		`{"usage":{"input_tokens":11,"output_tokens":22,"input_tokens_details":{"cached_tokens_details":{"image_tokens":3,"text_tokens":1}}}}`,
	))
	require.True(t, ok)
	require.Equal(t, 11, usage.InputTokens)
	require.Equal(t, 22, usage.OutputTokens)
	require.Equal(t, 22, usage.ImageOutputTokens, "the image-only endpoint reports image output tokens without a breakdown")
	require.Equal(t, 4, usage.CacheReadInputTokens)

	usage, ok = codexDirectImagesUsage([]byte(`{"usage":{"input_tokens":5,"output_tokens":7,"output_tokens_details":{"image_tokens":6}}}`))
	require.True(t, ok)
	require.Equal(t, 6, usage.ImageOutputTokens)

	_, ok = codexDirectImagesUsage([]byte(`{"created":1}`))
	require.False(t, ok)
}

func TestNormalizeOpenAIResponsesImageOnlyModelMovesComplexity(t *testing.T) {
	req := map[string]any{"model": "gpt-image-2.6-flare", "complexity": "high", "input": "draw a fox"}
	require.True(t, normalizeOpenAIResponsesImageOnlyModel(req))
	require.Equal(t, openAIImagesResponsesMainModelValue(), req["model"])
	tools, ok := req["tools"].([]any)
	require.True(t, ok)
	require.Len(t, tools, 1)
	tool, ok := tools[0].(map[string]any)
	require.True(t, ok)
	require.Equal(t, "high", tool["complexity"])
	require.NotContains(t, req, "complexity")
}

func TestGPTImage26PricingFallbackUsesImageCacheRate(t *testing.T) {
	svc := &PricingService{pricingData: map[string]*LiteLLMModelPricing{}}
	for _, model := range []string{
		"gpt-image-2.6-flare", "gpt-image-2.6-sunburst",
		"gpt-image-2.6-flare-2026-11-04", "gpt-image-2.6-sunburst-2026-11-04",
	} {
		pricing := svc.GetModelPricing(model)
		require.NotNil(t, pricing, model)
		require.Equal(t, 5e-6, pricing.InputCostPerToken, model)
		require.Equal(t, 1.25e-6, pricing.CacheReadInputTokenCost, model)
		require.Equal(t, 8e-6, pricing.InputCostPerImageToken, model)
		require.Equal(t, 2e-6, pricing.CacheReadInputImageTokenCost, model)
		require.Equal(t, 30e-6, pricing.OutputCostPerImageToken, model)
		require.Zero(t, pricing.OutputCostPerToken, model)
	}
}

func TestComputeTokenBreakdown_ImageCacheReadUsesImageCacheRate(t *testing.T) {
	svc := NewBillingService(&config.Config{}, nil)
	pricing := &ModelPricing{
		InputPricePerToken:          5e-6,
		ImageInputPricePerToken:     8e-6,
		ImageCacheReadPricePerToken: 2e-6,
		OutputPricePerToken:         10e-6,
		ImageOutputPricePerToken:    30e-6,
		ImageOutputPriceExplicit:    true,
		CacheReadPricePerToken:      1.25e-6,
	}
	tokens := UsageTokens{
		InputTokens:          100,
		ImageInputTokens:     60,
		ImageCacheReadTokens: 40,
		OutputTokens:         10,
		ImageOutputTokens:    10,
		CacheReadTokens:      40,
	}

	cost := svc.computeTokenBreakdown(pricing, tokens, 1.0, "", false)

	require.InDelta(t, float64(40)*2e-6, cost.CacheReadCost, 1e-15, "cached image tokens use the image cache rate")
	require.InDelta(t, float64(60)*8e-6, cost.ImageInputCost, 1e-15)
	require.InDelta(t, float64(40)*5e-6, cost.InputCost, 1e-15)
	require.InDelta(t, float64(10)*30e-6, cost.ImageOutputCost, 1e-15)
}

func TestOpenAIGatewayServiceForwardImages_OAuthGPTImage26UsesDirectEndpoint(t *testing.T) {
	gin.SetMode(gin.TestMode)
	body := []byte(`{"model":"gpt-image-2.6-flare","prompt":"draw a fox","size":"1024x1024","quality":"xhigh","complexity":"high","response_format":"b64_json"}`)
	c, rec := newOpenAIImagesTestContext(t, body)

	upstream := &httpUpstreamRecorder{resp: &http.Response{
		StatusCode: http.StatusOK,
		Header: http.Header{
			"Content-Type": []string{"application/json"},
			"X-Request-Id": []string{"req_direct_123"},
		},
		Body: io.NopCloser(strings.NewReader(
			`{"created":1710000000,"model":"gpt-image-2.6-flare-2026-11-04","data":[{"b64_json":"aW1hZ2U=","revised_prompt":"draw a fox","output_format":"png","size":"1024x1024","quality":"xhigh"}],"usage":{"input_tokens":11,"output_tokens":22,"output_tokens_details":{"image_tokens":22},"input_tokens_details":{"cached_tokens_details":{"image_tokens":3,"text_tokens":1}}}}`,
		)),
	}}
	svc := newOpenAIImagesTestService(upstream)
	account := newDirectImagesOAuthAccount()
	account.Credentials["model_mapping"] = map[string]any{"gpt-image-2.6-flare": "gpt-image-2.6-flare-2026-11-04"}

	parsed, err := svc.ParseOpenAIImagesRequest(c, body)
	require.NoError(t, err)
	result, err := svc.ForwardImages(context.Background(), c, account, body, parsed, "")
	require.NoError(t, err)
	require.NotNil(t, result)
	require.Equal(t, "gpt-image-2.6-flare", result.Model)
	require.Equal(t, "gpt-image-2.6-flare-2026-11-04", result.UpstreamModel)
	require.Equal(t, "/backend-api/codex/images/generations", result.UpstreamEndpoint)
	require.Equal(t, 1, result.ImageCount)
	require.Equal(t, 11, result.Usage.InputTokens)
	require.Equal(t, 22, result.Usage.OutputTokens)
	require.Equal(t, 22, result.Usage.ImageOutputTokens)
	require.Equal(t, 4, result.Usage.CacheReadInputTokens)

	require.NotNil(t, upstream.lastReq)
	require.Equal(t, "https://chatgpt.com/backend-api/codex/images/generations", upstream.lastReq.URL.String())
	require.Equal(t, "acct-123", upstream.lastReq.Header.Get("chatgpt-account-id"))
	require.Equal(t, "application/json", upstream.lastReq.Header.Get("Content-Type"))
	require.Equal(t, "application/json", upstream.lastReq.Header.Get("Accept"))
	require.Empty(t, upstream.lastReq.Header.Get("OpenAI-Beta"))
	require.Equal(t, "gpt-image-2.6-flare-2026-11-04", gjson.GetBytes(upstream.lastBody, "model").String())
	require.Equal(t, "high", gjson.GetBytes(upstream.lastBody, "complexity").String())
	require.Equal(t, "xhigh", gjson.GetBytes(upstream.lastBody, "quality").String())
	require.Equal(t, "1024x1024", gjson.GetBytes(upstream.lastBody, "size").String())
	require.False(t, gjson.GetBytes(upstream.lastBody, "tools").Exists())

	require.Equal(t, http.StatusOK, rec.Code)
	require.Equal(t, "gpt-image-2.6-flare-2026-11-04", gjson.Get(rec.Body.String(), "model").String())
	require.Equal(t, "gpt-image-2.6-flare", gjson.Get(rec.Body.String(), "data.0.model").String())
	require.Equal(t, "aW1hZ2U=", gjson.Get(rec.Body.String(), "data.0.b64_json").String())
	require.Equal(t, "draw a fox", gjson.Get(rec.Body.String(), "data.0.revised_prompt").String())
}

func TestOpenAIGatewayServiceForwardImages_OAuthDirectURLResponseFormat(t *testing.T) {
	gin.SetMode(gin.TestMode)
	body := []byte(`{"model":"gpt-image-2.6-sunburst","prompt":"draw a cup","response_format":"url"}`)
	c, rec := newOpenAIImagesTestContext(t, body)

	upstream := &httpUpstreamRecorder{resp: &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": []string{"application/json"}},
		Body: io.NopCloser(strings.NewReader(
			`{"created":1710000000,"data":[{"b64_json":"aW1hZ2U=","output_format":"webp","size":"1024x1024"}]}`,
		)),
	}}
	svc := newOpenAIImagesTestService(upstream)
	parsed, err := svc.ParseOpenAIImagesRequest(c, body)
	require.NoError(t, err)
	_, err = svc.ForwardImages(context.Background(), c, newDirectImagesOAuthAccount(), body, parsed, "")
	require.NoError(t, err)

	require.Equal(t, "data:image/webp;base64,aW1hZ2U=", gjson.Get(rec.Body.String(), "data.0.url").String())
	require.False(t, gjson.Get(rec.Body.String(), "data.0.b64_json").Exists())
}

func TestOpenAIGatewayServiceForwardImages_OAuthDirectFallsBackToBridgeOnNotFound(t *testing.T) {
	gin.SetMode(gin.TestMode)
	body := []byte(`{"model":"gpt-image-2.6-flare","prompt":"draw a cat","response_format":"b64_json"}`)
	c, rec := newOpenAIImagesTestContext(t, body)

	bridgeSSE := "data: {\"type\":\"response.output_item.done\",\"item\":{\"id\":\"ig_1\",\"type\":\"image_generation_call\",\"result\":\"aW1hZ2U=\",\"output_format\":\"png\"}}\n\n" +
		"data: {\"type\":\"response.completed\",\"response\":{\"created_at\":1710000006,\"tool_usage\":{\"image_gen\":{\"images\":1}},\"output\":[]}}\n\n" +
		"data: [DONE]\n\n"
	upstream := &httpUpstreamRecorder{responses: []*http.Response{
		{
			StatusCode: http.StatusNotFound,
			Header:     http.Header{"Content-Type": []string{"application/json"}},
			Body:       io.NopCloser(strings.NewReader(`{"error":{"message":"not found"}}`)),
		},
		{
			StatusCode: http.StatusOK,
			Header:     http.Header{"Content-Type": []string{"text/event-stream"}, "X-Request-Id": []string{"req_bridge"}},
			Body:       io.NopCloser(strings.NewReader(bridgeSSE)),
		},
	}}
	svc := newOpenAIImagesTestService(upstream)
	parsed, err := svc.ParseOpenAIImagesRequest(c, body)
	require.NoError(t, err)
	result, err := svc.ForwardImages(context.Background(), c, newDirectImagesOAuthAccount(), body, parsed, "")
	require.NoError(t, err)
	require.NotNil(t, result)
	require.Len(t, upstream.requests, 2)
	require.Equal(t, "/backend-api/codex/images/generations", upstream.requests[0].URL.Path)
	require.Equal(t, chatgptCodexURL, upstream.requests[1].URL.String())
	require.Equal(t, "responses=experimental", upstream.requests[1].Header.Get("OpenAI-Beta"))
	require.Equal(t, openAIImagesResponsesMainModel, gjson.GetBytes(upstream.bodies[1], "model").String())
	require.Equal(t, "gpt-image-2.6-flare", gjson.GetBytes(upstream.bodies[1], "tools.0.model").String())
	require.Equal(t, 1, result.ImageCount)
	require.Equal(t, http.StatusOK, rec.Code)
	require.Equal(t, "aW1hZ2U=", gjson.Get(rec.Body.String(), "data.0.b64_json").String())
}

func TestOpenAIGatewayServiceForwardImages_OAuthDirectStreamingRewritesURLs(t *testing.T) {
	gin.SetMode(gin.TestMode)
	body := []byte(`{"model":"gpt-image-2.6-flare","prompt":"draw a cat","stream":true,"response_format":"url"}`)
	c, rec := newOpenAIImagesTestContext(t, body)

	streamBody := "event: image_generation.partial_image\n" +
		"data: {\"type\":\"image_generation.partial_image\",\"partial_image_index\":0,\"b64_json\":\"cGFydGlhbA==\",\"output_format\":\"png\"}\n\n" +
		"event: image_generation.completed\n" +
		"data: {\"type\":\"image_generation.completed\",\"b64_json\":\"aW1hZ2U=\",\"output_format\":\"png\",\"usage\":{\"input_tokens\":11,\"output_tokens\":22,\"output_tokens_details\":{\"image_tokens\":22}}}\n\n"
	upstream := &httpUpstreamRecorder{resp: &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": []string{"text/event-stream"}},
		Body:       io.NopCloser(strings.NewReader(streamBody)),
	}}
	svc := newOpenAIImagesTestService(upstream)
	parsed, err := svc.ParseOpenAIImagesRequest(c, body)
	require.NoError(t, err)
	result, err := svc.ForwardImages(context.Background(), c, newDirectImagesOAuthAccount(), body, parsed, "")
	require.NoError(t, err)
	require.NotNil(t, result)
	require.Equal(t, 1, result.ImageCount)
	require.Equal(t, 22, result.Usage.ImageOutputTokens)
	require.Equal(t, "text/event-stream", upstream.lastReq.Header.Get("Accept"))
	require.Empty(t, upstream.lastReq.Header.Get("OpenAI-Beta"))

	var completedData string
	for _, line := range strings.Split(rec.Body.String(), "\n") {
		if strings.HasPrefix(line, "data: ") && strings.Contains(line, "image_generation.completed") {
			completedData = strings.TrimPrefix(line, "data: ")
		}
	}
	require.NotEmpty(t, completedData)
	require.Equal(t, "data:image/png;base64,aW1hZ2U=", gjson.Get(completedData, "url").String())
	require.Equal(t, "aW1hZ2U=", gjson.Get(completedData, "b64_json").String(), "the streaming contract keeps b64_json next to url")
}

func TestOpenAIGatewayServiceForwardImages_OAuthBridgePassesComplexity(t *testing.T) {
	gin.SetMode(gin.TestMode)
	body := []byte(`{"model":"gpt-image-1","prompt":"draw a cat","complexity":"max","response_format":"b64_json"}`)
	c, _ := newOpenAIImagesTestContext(t, body)

	bridgeSSE := "data: {\"type\":\"response.output_item.done\",\"item\":{\"id\":\"ig_1\",\"type\":\"image_generation_call\",\"result\":\"aW1hZ2U=\",\"output_format\":\"png\"}}\n\n" +
		"data: {\"type\":\"response.completed\",\"response\":{\"created_at\":1710000006,\"tool_usage\":{\"image_gen\":{\"images\":1}},\"output\":[]}}\n\n" +
		"data: [DONE]\n\n"
	upstream := &httpUpstreamRecorder{resp: &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": []string{"text/event-stream"}},
		Body:       io.NopCloser(strings.NewReader(bridgeSSE)),
	}}
	svc := newOpenAIImagesTestService(upstream)
	parsed, err := svc.ParseOpenAIImagesRequest(c, body)
	require.NoError(t, err)
	_, err = svc.ForwardImages(context.Background(), c, newDirectImagesOAuthAccount(), body, parsed, "")
	require.NoError(t, err)

	require.Equal(t, chatgptCodexURL, upstream.lastReq.URL.String())
	require.Equal(t, "responses=experimental", upstream.lastReq.Header.Get("OpenAI-Beta"))
	require.Equal(t, "max", gjson.GetBytes(upstream.lastBody, "tools.0.complexity").String())
}
