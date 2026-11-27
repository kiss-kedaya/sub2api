//go:build unit

// Adapted from ranxi2001/sub2api @ 9620d315, LGPL-3.0-or-later.
package service

import (
	"bytes"
	"context"
	"io"
	"math"
	"mime"
	"mime/multipart"
	"net/http"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

func miniMaxH3TestAccount() *Account {
	return &Account{ID: 9, Platform: PlatformMiniMax, Type: AccountTypeAPIKey, Credentials: map[string]any{
		"api_key": "test-upstream-key", "base_url": "https://relay.example/v1", "account_mode": AccountModePayG,
		"model_mapping": map[string]any{"video-alias": "MiniMax-H3", "h3-30s": "dmad_30"},
	}}
}

func TestMiniMaxH3ForwardPreservesProtocol(t *testing.T) {
	body := []byte(`{"model":"video-alias","prompt":"waves","seconds":"1","size":"1344x768","mode":"frames","metadata":{"ratio":"16:9","content":[{"type":"image_url","image_url":{"url":"data:image/png;base64,aGVsbG8="},"role":"first_frame"},{"type":"video_url","video_url":{"url":"https://example.com/reference.mp4"},"role":"reference_video"}]},"future":{"keep":true}}`)
	upstream := &grokMediaContentUpstreamStub{response: grokMediaContentStatusResponse(`{"id":"task-1","status":"queued"}`)}
	svc := &OpenAIGatewayService{httpUpstream: upstream}
	c, w := grokMediaContentTestContext(http.MethodPost, "/v1/videos", nil)
	result, err := svc.ForwardMiniMaxH3(context.Background(), c, miniMaxH3TestAccount(), MiniMaxH3EndpointCreate, "", body)
	require.NoError(t, err)
	require.Equal(t, "https://relay.example/v1/videos", upstream.request.URL.String())
	require.Equal(t, "Bearer test-upstream-key", upstream.request.Header.Get("Authorization"))
	require.True(t, HTTPUpstreamRedirectsDisabled(upstream.request.Context()))
	forwarded, err := io.ReadAll(upstream.request.Body)
	require.NoError(t, err)
	require.Equal(t, "MiniMax-H3", gjson.GetBytes(forwarded, "model").String())
	for _, field := range []string{"prompt", "seconds", "size", "mode", "metadata", "future"} {
		require.Equal(t, gjson.GetBytes(body, field).Raw, gjson.GetBytes(forwarded, field).Raw)
	}
	require.Equal(t, "minimax-h3:task-1", result.ResponseID)
	require.Equal(t, "video-alias", result.BillingModel)
	require.Equal(t, "768p", result.VideoResolution)
	require.Equal(t, 1, result.VideoDurationSeconds)
	require.Zero(t, result.VideoCount, "async acceptance must not charge")
	require.JSONEq(t, `{"id":"task-1","status":"queued"}`, w.Body.String())
}

func TestMiniMaxH3ForwardSupportsCustomModelMappingAnd30s(t *testing.T) {
	body := []byte(`{"model":"h3-30s","prompt":"two stitched scenes","seconds":30,"size":"1184x672"}`)
	upstream := &grokMediaContentUpstreamStub{response: grokMediaContentStatusResponse(`{"id":"video_42","object":"video","status":"queued","model":"dmad_30","seconds":"30"}`)}
	svc := &OpenAIGatewayService{httpUpstream: upstream}
	c, w := grokMediaContentTestContext(http.MethodPost, "/v1/videos", nil)
	result, err := svc.ForwardMiniMaxH3(context.Background(), c, miniMaxH3TestAccount(), MiniMaxH3EndpointCreate, "", body)
	require.NoError(t, err)
	forwarded, err := io.ReadAll(upstream.request.Body)
	require.NoError(t, err)
	require.Equal(t, "dmad_30", gjson.GetBytes(forwarded, "model").String())
	require.Equal(t, "minimax-h3:video_42", result.ResponseID)
	require.Equal(t, "dmad_30", result.UpstreamModel)
	require.Equal(t, 30, result.VideoDurationSeconds)
	require.Equal(t, http.StatusOK, w.Code)
}

func TestMiniMaxH3ForwardMultipartWithModelRewrite(t *testing.T) {
	var raw bytes.Buffer
	mw := multipart.NewWriter(&raw)
	require.NoError(t, mw.WriteField("model", "h3-30s"))
	require.NoError(t, mw.WriteField("prompt", "city skyline at dusk"))
	require.NoError(t, mw.WriteField("seconds", "30"))
	part, err := mw.CreateFormFile("input_reference", "ref.jpg")
	require.NoError(t, err)
	_, err = part.Write([]byte("fake-jpeg-bytes"))
	require.NoError(t, err)
	require.NoError(t, mw.Close())

	upstream := &grokMediaContentUpstreamStub{response: grokMediaContentStatusResponse(`{"id":"video_99","object":"video","status":"queued"}`)}
	svc := &OpenAIGatewayService{httpUpstream: upstream}
	c, _ := grokMediaContentTestContext(http.MethodPost, "/v1/videos", nil)
	result, err := svc.ForwardMiniMaxH3WithContentType(context.Background(), c, miniMaxH3TestAccount(), MiniMaxH3EndpointCreate, "", raw.Bytes(), mw.FormDataContentType())
	require.NoError(t, err)
	require.Equal(t, "minimax-h3:video_99", result.ResponseID)
	require.Equal(t, 30, result.VideoDurationSeconds)
	require.Equal(t, "dmad_30", result.UpstreamModel)

	ct := upstream.request.Header.Get("Content-Type")
	mediaType, params, err := mime.ParseMediaType(ct)
	require.NoError(t, err)
	require.Equal(t, "multipart/form-data", mediaType)
	forwarded, err := io.ReadAll(upstream.request.Body)
	require.NoError(t, err)
	reader := multipart.NewReader(bytes.NewReader(forwarded), params["boundary"])
	fields := map[string]string{}
	files := map[string]string{}
	for {
		p, nextErr := reader.NextPart()
		if nextErr == io.EOF {
			break
		}
		require.NoError(t, nextErr)
		data, readErr := io.ReadAll(p)
		require.NoError(t, readErr)
		if p.FileName() != "" {
			files[p.FormName()+":"+p.FileName()] = string(data)
		} else {
			fields[p.FormName()] = string(data)
		}
	}
	require.Equal(t, "dmad_30", fields["model"])
	require.Equal(t, "city skyline at dusk", fields["prompt"])
	require.Equal(t, "30", fields["seconds"])
	require.Equal(t, "fake-jpeg-bytes", files["input_reference:ref.jpg"])
}

func TestMiniMaxH3ValidationAndURLs(t *testing.T) {
	for _, body := range []string{
		`{`,
		`[]`,
		`{}`,
		`{"model":"MiniMax-H3","prompt":"x","seconds":"0"}`,
		`{"model":"MiniMax-H3","prompt":"x","seconds":0}`,
		`{"model":"MiniMax-H3","prompt":"x","seconds":"31"}`,
		`{"model":"MiniMax-H3","prompt":"x","seconds":31}`,
		`{"model":"MiniMax-H3","prompt":"x","seconds":1.5}`,
		`{"model":"MiniMax-H3","prompt":"x","seconds":"01"}`,
		`{"model":"MiniMax-H3","prompt":"x","seconds":""}`,
		`{"model":"MiniMax-H3","prompt":" ","seconds":"1"}`,
		`{"model":"MiniMax-H3","prompt":"x","seconds":"1","n":2}`,
	} {
		_, err := ParseMiniMaxH3Request([]byte(body))
		require.Error(t, err, body)
	}
	for _, tc := range []struct {
		body        string
		wantSeconds int
	}{
		{`{"model":"MiniMax-H3","prompt":"x","seconds":"1"}`, 1},
		{`{"model":"MiniMax-H3","prompt":"x","seconds":15}`, 15},
		{`{"model":"MiniMax-H3","prompt":"x","seconds":"30"}`, 30},
		{`{"model":"MiniMax-H3","prompt":"x","seconds":30}`, 30},
		{`{"model":"dmad_30","prompt":"x"}`, 0},
	} {
		info, err := ParseMiniMaxH3Request([]byte(tc.body))
		require.NoError(t, err, tc.body)
		require.Equal(t, tc.wantSeconds, info.DurationSeconds, tc.body)
	}
	for _, base := range []string{"https://relay.example", "https://relay.example/v1/"} {
		target, err := buildMiniMaxH3URL(base, MiniMaxH3EndpointCreate, "")
		require.NoError(t, err)
		require.Equal(t, "https://relay.example/v1/videos", target)
	}
	for _, id := range []string{"", "..", "a/b", "a?b", "a#b", "%2e%2e"} {
		_, err := buildMiniMaxH3URL("https://relay.example", MiniMaxH3EndpointStatus, id)
		require.Error(t, err, id)
	}
	a := miniMaxH3TestAccount()
	require.True(t, a.SupportsOpenAIEndpointCapability(OpenAIEndpointCapabilityMiniMaxH3))
	a.Credentials["account_mode"] = AccountModeCoding
	require.False(t, a.SupportsOpenAIEndpointCapability(OpenAIEndpointCapabilityMiniMaxH3))
	a.Credentials["account_mode"] = AccountModePayG
	a.Platform = PlatformGrok
	require.False(t, a.SupportsOpenAIEndpointCapability(OpenAIEndpointCapabilityMiniMaxH3))
}

func TestMiniMaxH3StatusStates(t *testing.T) {
	for _, status := range []string{"queued", "in_progress", "unknown", "failed", "completed"} {
		t.Run(status, func(t *testing.T) {
			body := `{"id":"task-1","status":"` + status + `","metadata":{"url":"https://cdn.example/output.mp4"},"seconds":"15"}`
			upstream := &grokMediaContentUpstreamStub{response: grokMediaContentStatusResponse(body)}
			svc := &OpenAIGatewayService{httpUpstream: upstream}
			c, w := grokMediaContentTestContext(http.MethodGet, "/v1/videos/task-1", nil)
			result, err := svc.ForwardMiniMaxH3(context.Background(), c, miniMaxH3TestAccount(), MiniMaxH3EndpointStatus, MiniMaxH3TaskKey("task-1"), nil)
			require.NoError(t, err)
			require.NotContains(t, w.Body.String(), "cdn.example")
			require.Equal(t, "/v1/videos/task-1/content", gjson.Get(w.Body.String(), "metadata.url").String())
			require.Equal(t, "/v1/videos/task-1", upstream.request.URL.Path)
			want := 0
			if status == "completed" {
				want = 1
			}
			require.Equal(t, want, result.VideoCount)
			require.Zero(t, result.VideoDurationSeconds, "status must not override requested seconds")
		})
	}
}

func TestMiniMaxH3StatusStandardOpenAIVideosCompleted(t *testing.T) {
	t.Run("completed standard video without metadata.url is billable", func(t *testing.T) {
		body := `{"id":"video_42","object":"video","status":"completed","progress":100,"model":"dmad_30","seconds":"30","size":"1184x672","error":null}`
		upstream := &grokMediaContentUpstreamStub{response: grokMediaContentStatusResponse(body)}
		svc := &OpenAIGatewayService{httpUpstream: upstream}
		c, w := grokMediaContentTestContext(http.MethodGet, "/v1/videos/video_42", nil)
		result, err := svc.ForwardMiniMaxH3(context.Background(), c, miniMaxH3TestAccount(), MiniMaxH3EndpointStatus, MiniMaxH3TaskKey("video_42"), nil)
		require.NoError(t, err)
		require.Equal(t, 1, result.VideoCount)
		require.Equal(t, "completed", result.VideoStatus)
		require.Equal(t, http.StatusOK, w.Code)
	})
	t.Run("completed with invalid metadata.url is still rejected from billing", func(t *testing.T) {
		body := `{"id":"video_42","object":"video","status":"completed","metadata":{"url":"http://127.0.0.1/internal.mp4"}}`
		upstream := &grokMediaContentUpstreamStub{response: grokMediaContentStatusResponse(body)}
		svc := &OpenAIGatewayService{httpUpstream: upstream}
		c, _ := grokMediaContentTestContext(http.MethodGet, "/v1/videos/video_42", nil)
		result, err := svc.ForwardMiniMaxH3(context.Background(), c, miniMaxH3TestAccount(), MiniMaxH3EndpointStatus, MiniMaxH3TaskKey("video_42"), nil)
		require.NoError(t, err)
		require.Equal(t, 0, result.VideoCount)
	})
}

func TestMiniMaxH3ContentProxiesPublicCDNWithoutCredentials(t *testing.T) {
	redirect := grokMediaContentStatusResponse("")
	redirect.StatusCode = http.StatusFound
	redirect.Header.Set("Location", "https://cdn.example/output.mp4?signature=fake")
	upstream := &grokMediaContentUpstreamStub{responses: []*http.Response{redirect, {
		StatusCode: http.StatusOK, Header: http.Header{"Content-Type": {"video/mp4"}}, Body: io.NopCloser(strings.NewReader("video-payload")),
	}}}
	svc := &OpenAIGatewayService{httpUpstream: upstream}
	c, w := grokMediaContentTestContext(http.MethodGet, "/v1/videos/task-1/content", nil)
	result, err := svc.ForwardMiniMaxH3(context.Background(), c, miniMaxH3TestAccount(), MiniMaxH3EndpointContent, MiniMaxH3TaskKey("task-1"), nil)
	require.NoError(t, err)
	require.Equal(t, http.StatusOK, w.Code)
	require.Equal(t, "video-payload", w.Body.String())
	require.Empty(t, w.Header().Get("Location"))
	require.Len(t, upstream.requests, 2)
	require.Equal(t, "Bearer test-upstream-key", upstream.requests[0].Header.Get("Authorization"))
	require.True(t, HTTPUpstreamRedirectsDisabled(upstream.requests[0].Context()))
	require.Empty(t, upstream.requests[1].Header.Get("Authorization"))
	require.True(t, HTTPUpstreamPublicHostsOnly(upstream.requests[1].Context()))
	require.Equal(t, 1, result.VideoCount)
}

func TestMiniMaxH3UpstreamErrorIsNotRetried(t *testing.T) {
	for _, status := range []int{400, 429, 503} {
		upstream := &grokMediaContentUpstreamStub{response: grokMediaContentStatusResponse(`{"error":{"message":"upstream failure"}}`)}
		upstream.response.StatusCode = status
		svc := &OpenAIGatewayService{httpUpstream: upstream}
		c, w := grokMediaContentTestContext(http.MethodPost, "/v1/videos", nil)
		_, err := svc.ForwardMiniMaxH3(context.Background(), c, miniMaxH3TestAccount(), MiniMaxH3EndpointCreate, "", []byte(`{"model":"MiniMax-H3","prompt":"waves","seconds":"1"}`))
		require.Error(t, err)
		require.Equal(t, status, w.Code)
		require.Len(t, upstream.requests, 1)
	}
}

func TestMiniMaxH3TariffsAndBillingSnapshot(t *testing.T) {
	perSecond := map[string]map[string]float64{MiniMaxH3PriceFamily: {MiniMaxH3PerSecond: 0.02}}
	perRequest := map[string]map[string]float64{MiniMaxH3PriceFamily: {"request_5s": 0.08}}
	perRequest30 := map[string]map[string]float64{MiniMaxH3PriceFamily: {"request_30s": 0.5}}
	flatPerRequest := map[string]map[string]float64{MiniMaxH3PriceFamily: {MiniMaxH3PerRequest: 0.5}}

	base, err := QuoteMiniMaxH3Video(perSecond, 5)
	require.NoError(t, err)
	require.InDelta(t, 0.1, base, 1e-9)

	base30, err := QuoteMiniMaxH3Video(perSecond, 30)
	require.NoError(t, err)
	require.InDelta(t, 0.6, base30, 1e-9)

	_, err = QuoteMiniMaxH3Video(perSecond, 0)
	require.Error(t, err, "per_second requires explicit seconds")

	base, err = QuoteMiniMaxH3Video(perRequest, 5)
	require.NoError(t, err)
	require.InDelta(t, 0.08, base, 1e-9)
	_, err = QuoteMiniMaxH3Video(perRequest, 15)
	require.ErrorContains(t, err, `seconds="5"`)

	base, err = QuoteMiniMaxH3Video(perRequest30, 30)
	require.NoError(t, err)
	require.InDelta(t, 0.5, base, 1e-9)

	for _, sec := range []int{0, 4, 8, 15, 20, 30} {
		flat, flatErr := QuoteMiniMaxH3Video(flatPerRequest, sec)
		require.NoError(t, flatErr)
		require.InDelta(t, 0.5, flat, 1e-9)
	}
	_, err = QuoteMiniMaxH3Video(flatPerRequest, 31)
	require.Error(t, err)

	for _, tariff := range []map[string]map[string]float64{
		nil,
		{MiniMaxH3PriceFamily: {"480p": 0.1}},
		{MiniMaxH3PriceFamily: {MiniMaxH3PerSecond: -1}},
		{MiniMaxH3PriceFamily: {MiniMaxH3PerSecond: math.Inf(1)}},
		{MiniMaxH3PriceFamily: {MiniMaxH3PerSecond: 0.1, "request_5s": 0.3}},
		{MiniMaxH3PriceFamily: {MiniMaxH3PerRequest: 0.5, "request_30s": 0.5}},
	} {
		_, err := QuoteMiniMaxH3Video(tariff, 5)
		require.Error(t, err)
	}
	require.Equal(t, perRequest, NormalizeVideoModelPrices(perRequest))
	require.Equal(t, perRequest30, NormalizeVideoModelPrices(perRequest30))
	require.Equal(t, flatPerRequest, NormalizeVideoModelPrices(flatPerRequest))

	svc := &OpenAIGatewayService{}
	base = 0.08
	result := &OpenAIForwardResult{VideoCount: 1, MiniMaxH3BaseCost: &base, VideoDurationSeconds: 5}
	cost := svc.calculateOpenAIVideoCost(context.Background(), "mapped-name", &APIKey{}, result, 0.1)
	require.InDelta(t, 0.008, cost.ActualCost, 1e-9)
	require.Equal(t, 30, NormalizeVideoBillingDurationSecondsForProvider(VideoProviderMiniMaxH3, 30))
	require.Equal(t, 30, NormalizeVideoBillingDurationSecondsForProvider(VideoProviderMiniMaxH3, 45))
}
