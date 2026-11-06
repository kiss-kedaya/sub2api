package service

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

const gifSheetUpstreamTextError = `No supported sheet fits 16 GIF frames at size '1024x1024'.
Invalid size '4096x4096'.
The longest edge must be less than or equal to 3840.`

func gifSheetTestAccount(baseURL string) *Account {
	return &Account{
		ID:       77,
		Name:     "gif-sheet-relay",
		Platform: PlatformOpenAI,
		Type:     AccountTypeAPIKey,
		Credentials: map[string]any{
			"api_key":  "sk-test",
			"base_url": baseURL,
		},
	}
}

func gifSheetTestParsed(frameCount int, size, outputFormat string) *OpenAIImagesRequest {
	count := frameCount
	return &OpenAIImagesRequest{
		Endpoint:     openAIImagesGenerationsEndpoint,
		Model:        "gpt-image-2.6-sunburst",
		Size:         size,
		SizeTier:     normalizeOpenAIImageSizeTier(size),
		OutputFormat: outputFormat,
		N:            1,
		FrameCount:   &count,
	}
}

func TestOpenAIImagesHTTP200Error(t *testing.T) {
	requestHeader := http.Header{"X-Request-Id": []string{"ERR-711DE84247"}}
	plainHeader := http.Header{"X-Request-Id": []string{"req_text_error"}}

	t.Run("plain text sheet error is recognized", func(t *testing.T) {
		upErr := openAIImagesHTTP200Error([]byte(gifSheetUpstreamTextError), requestHeader)
		require.NotNil(t, upErr)
		require.Equal(t, http.StatusBadRequest, upErr.clientStatusCode())
		require.Equal(t, "invalid_request_error", upErr.clientErrorType())
		require.Equal(t, "ERR-711DE84247", upErr.Code)
		require.Equal(t, "ERR-711DE84247", upErr.UpstreamRequestID)
		require.Contains(t, upErr.Message, "No supported sheet fits 16 GIF frames")
		require.Contains(t, upErr.Message, "longest edge must be less than or equal to 3840")
	})

	t.Run("plain text error code without ERR header", func(t *testing.T) {
		upErr := openAIImagesHTTP200Error([]byte("image generation failed: backend unavailable"), plainHeader)
		require.NotNil(t, upErr)
		require.Equal(t, "upstream_error_response", upErr.Code)
		require.Equal(t, "req_text_error", upErr.UpstreamRequestID)
		require.Contains(t, upErr.Message, "backend unavailable")
	})

	t.Run("normal b64_json payload is not an error", func(t *testing.T) {
		require.Nil(t, openAIImagesHTTP200Error(
			[]byte(`{"created":1,"data":[{"b64_json":"aGVsbG8="}]}`), requestHeader))
	})

	t.Run("normal url payload is not an error", func(t *testing.T) {
		require.Nil(t, openAIImagesHTTP200Error(
			[]byte(`{"created":1,"data":[{"url":"https://example.test/a.png"}]}`), requestHeader))
	})

	t.Run("json error envelope keeps upstream status", func(t *testing.T) {
		upErr := openAIImagesHTTP200Error(
			[]byte(`{"error":{"type":"invalid_request_error","code":"invalid_value","message":"Invalid size '4096x4096'."}}`),
			plainHeader,
		)
		require.NotNil(t, upErr)
		require.Equal(t, http.StatusBadRequest, upErr.clientStatusCode())
		require.Equal(t, "invalid_value", upErr.Code)
		require.Contains(t, upErr.Message, "Invalid size '4096x4096'")
	})

	t.Run("json error message without type keeps request-shape status", func(t *testing.T) {
		upErr := openAIImagesHTTP200Error(
			[]byte(`{"error":{"message":"No supported sheet fits 16 GIF frames at size '1024x1024'."}}`),
			plainHeader,
		)
		require.NotNil(t, upErr)
		require.Equal(t, http.StatusBadRequest, upErr.clientStatusCode())
		require.Contains(t, upErr.Message, "No supported sheet fits")
	})

	t.Run("json detail with recognized text is an error", func(t *testing.T) {
		upErr := openAIImagesHTTP200Error(
			[]byte(`{"detail":"No supported sheet fits 16 GIF frames at size '1024x1024'."}`),
			plainHeader,
		)
		require.NotNil(t, upErr)
		require.Equal(t, http.StatusBadRequest, upErr.clientStatusCode())
		require.Contains(t, upErr.Message, "No supported sheet fits")
	})

	t.Run("json type error is an error", func(t *testing.T) {
		upErr := openAIImagesHTTP200Error(
			[]byte(`{"type":"error","error":{"type":"server_error","code":"server_error","message":"image backend unavailable"}}`),
			plainHeader,
		)
		require.NotNil(t, upErr)
		require.Equal(t, http.StatusInternalServerError, upErr.clientStatusCode())
		require.Equal(t, "req_text_error", upErr.UpstreamRequestID)
		require.Contains(t, upErr.Message, "image backend unavailable")
	})

	t.Run("empty data array keeps existing passthrough behavior", func(t *testing.T) {
		require.Nil(t, openAIImagesHTTP200Error([]byte(`{"created":1,"data":[]}`), plainHeader))
	})

	t.Run("unrelated json message is not an error", func(t *testing.T) {
		require.Nil(t, openAIImagesHTTP200Error([]byte(`{"message":"all good"}`), plainHeader))
	})

	t.Run("unrelated plain text is not an error", func(t *testing.T) {
		require.Nil(t, openAIImagesHTTP200Error([]byte("service warming up"), plainHeader))
	})
}

// 线上复现：中转站用 HTTP 200 + 纯文本错误返回失败，旧逻辑会 c.Data(200) 并把
// ImageCount 记成 1。修复后必须返回错误、不产出 ForwardResult（handler 因此不会
// 进入 RecordUsage），客户端拿到 502 + 上游原始 message。
func TestHandleOpenAIImagesNonStreamingResponse_TextErrorIsNotSuccess(t *testing.T) {
	gin.SetMode(gin.TestMode)
	c, rec := newOpenAIImagesTestContext(t, nil)
	parsed := gifSheetTestParsed(16, "864x864", "gif")
	resp := &http.Response{
		StatusCode: http.StatusOK,
		Header: http.Header{
			"Content-Type": []string{"text/plain"},
			"X-Request-Id": []string{"ERR-87ADE609BA"},
		},
		Body: io.NopCloser(strings.NewReader(gifSheetUpstreamTextError)),
	}

	svc := &OpenAIGatewayService{}
	usage, imageCount, sizes, err := svc.handleOpenAIImagesNonStreamingResponse(
		context.Background(), resp, c, gifSheetTestAccount("https://api.sourcesdata.com/v1"), parsed,
	)

	require.Error(t, err)
	var upErr *OpenAIImagesUpstreamError
	require.ErrorAs(t, err, &upErr)
	require.Equal(t, http.StatusBadRequest, upErr.StatusCode)
	require.Equal(t, "invalid_request_error", upErr.ErrorType)
	require.Equal(t, "ERR-87ADE609BA", upErr.Code)
	require.Equal(t, "ERR-87ADE609BA", upErr.UpstreamRequestID)
	require.Contains(t, upErr.Message, "No supported sheet fits")
	require.Contains(t, upErr.Message, "longest edge must be less than or equal to 3840")
	require.Zero(t, imageCount, "失败响应不得按 1 张图计费")
	require.Empty(t, sizes)
	require.Zero(t, usage.InputTokens+usage.OutputTokens)
	require.Equal(t, http.StatusBadRequest, rec.Code)
	require.Contains(t, rec.Body.String(), "No supported sheet fits")
}

// 200 + 正常 data[].b64_json 不能被错误识别误伤。
func TestHandleOpenAIImagesNonStreamingResponse_NormalImageStillSucceeds(t *testing.T) {
	gin.SetMode(gin.TestMode)
	c, rec := newOpenAIImagesTestContext(t, nil)
	parsed := gifSheetTestParsed(8, "864x864", "gif")
	resp := &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": []string{"application/json"}},
		Body: io.NopCloser(strings.NewReader(
			`{"created":1710000000,"data":[{"b64_json":"aGVsbG8=","output_format":"gif","size":"864x864"}]}`,
		)),
	}

	svc := &OpenAIGatewayService{}
	_, imageCount, _, err := svc.handleOpenAIImagesNonStreamingResponse(
		context.Background(), resp, c, gifSheetTestAccount("https://api.sourcesdata.com/v1"), parsed,
	)

	require.NoError(t, err)
	require.Equal(t, 1, imageCount)
	require.Equal(t, http.StatusOK, rec.Code)
}

// 走完整的 API Key 转发路径：上游 200 + 文本错误时 ForwardImages 返回 nil,error，
// handler 的 record usage 分支不会执行。
func TestForwardOpenAIImagesAPIKey_TextErrorReturnsNilResult(t *testing.T) {
	body := []byte(`{"model":"gpt-image-2.6-sunburst","prompt":"draw a lantern","size":"1024x1024","output_format":"png"}`)
	c, rec := newOpenAIImagesTestContext(t, body)
	recorder := &httpUpstreamRecorder{resp: &http.Response{
		StatusCode: http.StatusOK,
		Header: http.Header{
			"Content-Type": []string{"text/plain"},
			"X-Request-Id": []string{"ERR-711DE84247"},
		},
		Body: io.NopCloser(strings.NewReader("image generation failed: backend unavailable")),
	}}
	svc := newOpenAIImagesTestService(recorder)

	parsed, err := svc.ParseOpenAIImagesRequest(c, body)
	require.NoError(t, err)
	result, err := svc.ForwardImages(
		context.Background(), c, gifSheetTestAccount("https://api.sourcesdata.com/v1"), body, parsed, "",
	)

	require.Nil(t, result, "失败响应不得返回 ForwardResult，否则 handler 会按成功计费")
	require.Error(t, err)
	var upErr *OpenAIImagesUpstreamError
	require.ErrorAs(t, err, &upErr)
	require.Equal(t, http.StatusBadGateway, upErr.StatusCode)
	require.Equal(t, "ERR-711DE84247", upErr.Code)
	require.Len(t, recorder.requests, 1, "上游请求本身已经发出")
	require.Equal(t, http.StatusBadGateway, rec.Code)
}

// 16 帧 + 1024x1024 在发往中转上游前直接 400，给出 960x960 / 9 帧的可用组合。
func TestForwardOpenAIImagesAPIKey_GIFSheetLimitRejectedBeforeUpstream(t *testing.T) {
	body := []byte(`{"model":"gpt-image-2.6-sunburst","prompt":"draw a lantern","size":"1024x1024","output_format":"gif","frame_count":16,"frame_duration_ms":200}`)
	c, rec := newOpenAIImagesTestContext(t, body)
	recorder := &httpUpstreamRecorder{resp: openAIImagesJSONResponse()}
	svc := newOpenAIImagesTestService(recorder)

	parsed, err := svc.ParseOpenAIImagesRequest(c, body)
	require.NoError(t, err)
	result, err := svc.ForwardImages(
		context.Background(), c, gifSheetTestAccount("https://api.sourcesdata.com/v1"), body, parsed, "",
	)

	require.Nil(t, result)
	var upErr *OpenAIImagesUpstreamError
	require.ErrorAs(t, err, &upErr)
	require.Equal(t, http.StatusBadRequest, upErr.StatusCode)
	require.Equal(t, "image_gif_sheet_size_unsupported", upErr.Code)
	require.Contains(t, upErr.Message, "4x4")
	require.Contains(t, upErr.Message, "960x960")
	require.Contains(t, upErr.Message, "9 frames")
	require.Nil(t, recorder.lastReq, "超限请求不得发往上游")
	require.Equal(t, http.StatusBadRequest, rec.Code)
}

// 官方 api.openai.com 的 API Key 账号不套用中转站的拼图上限，保持原行为。
func TestForwardOpenAIImagesAPIKey_OfficialBaseURLSkipsGIFSheetLimit(t *testing.T) {
	body := []byte(`{"model":"gpt-image-2.6-sunburst","prompt":"draw a lantern","size":"1024x1024","output_format":"gif","frame_count":16,"frame_duration_ms":200}`)
	c, _ := newOpenAIImagesTestContext(t, body)
	recorder := &httpUpstreamRecorder{resp: openAIImagesJSONResponse()}
	svc := newOpenAIImagesTestService(recorder)
	account := newOpenAIImagesAPIKeyAccount()

	parsed, err := svc.ParseOpenAIImagesRequest(c, body)
	require.NoError(t, err)
	result, err := svc.ForwardImages(context.Background(), c, account, body, parsed, "")

	require.NoError(t, err)
	require.NotNil(t, result)
	require.Equal(t, 1, result.ImageCount)
	require.NotNil(t, recorder.lastReq)
}

func TestValidateOpenAIImagesGIFSheetLimit(t *testing.T) {
	custom := gifSheetTestAccount("https://api.sourcesdata.com/v1")
	official := gifSheetTestAccount("https://api.openai.com/v1")

	tests := []struct {
		name       string
		frameCount int
		size       string
		format     string
		account    *Account
		wantErr    bool
		contains   []string
	}{
		{name: "16 frames at 960 fits exactly", frameCount: 16, size: "960x960", format: "gif", account: custom},
		{name: "16 frames at 864 fits", frameCount: 16, size: "864x864", format: "gif", account: custom},
		{name: "16 frames at 1024 rejected", frameCount: 16, size: "1024x1024", format: "gif", account: custom, wantErr: true, contains: []string{"4x4", "960x960", "9 frames"}},
		{name: "8 frames at 1280 fits exactly", frameCount: 8, size: "1280x1280", format: "gif", account: custom},
		{name: "8 frames at 1281 rejected", frameCount: 8, size: "1281x1281", format: "gif", account: custom, wantErr: true, contains: []string{"3x3", "1280x1280"}},
		{name: "9 frames at 1024 fits", frameCount: 9, size: "1024x1024", format: "gif", account: custom},
		{name: "4 frames at 1920 fits exactly", frameCount: 4, size: "1920x1920", format: "gif", account: custom},
		{name: "4 frames at 1921 rejected", frameCount: 4, size: "1921x1921", format: "gif", account: custom, wantErr: true},
		{name: "non-square 1024x1536 rejected", frameCount: 16, size: "1024x1536", format: "gif", account: custom, wantErr: true, contains: []string{"640x960", "4 frames"}},
		{name: "size edge above 3840 rejected", frameCount: 16, size: "4096x4096", format: "gif", account: custom, wantErr: true, contains: []string{"exceeds"}},
		{name: "png output is not gated", frameCount: 16, size: "1024x1024", format: "png", account: custom},
		{name: "official base url is not gated", frameCount: 16, size: "1024x1024", format: "gif", account: official},
		{name: "auto size is not gated", frameCount: 16, size: "auto", format: "gif", account: custom},
		{name: "nil frame count is not gated", frameCount: 0, size: "1024x1024", format: "gif", account: custom},
		{name: "out of range frame count stays upstream-owned", frameCount: 17, size: "1024x1024", format: "gif", account: custom},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			parsed := gifSheetTestParsed(tt.frameCount, tt.size, tt.format)
			if tt.frameCount == 0 {
				parsed.FrameCount = nil
			}
			err := validateOpenAIImagesGIFSheetLimit(parsed, tt.account)
			if !tt.wantErr {
				require.NoError(t, err)
				return
			}
			require.Error(t, err)
			for _, want := range tt.contains {
				require.Contains(t, err.Error(), want)
			}
		})
	}
}
