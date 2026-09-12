package service

import (
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

func TestApplyGrokImagineImageGeometryMapsOpenAISize(t *testing.T) {
	t.Parallel()

	out, err := applyGrokImagineImageGeometry([]byte(`{"model":"grok-imagine-image-2.0","prompt":"hi","size":"1152x1536"}`))
	require.NoError(t, err)
	require.False(t, gjson.GetBytes(out, "size").Exists())
	require.Equal(t, "2k", gjson.GetBytes(out, "resolution").String())
	require.Equal(t, "3:4", gjson.GetBytes(out, "aspect_ratio").String())
}

func TestApplyGrokImagineImageGeometryKeepsClientGeometry(t *testing.T) {
	t.Parallel()

	out, err := applyGrokImagineImageGeometry([]byte(`{"size":"1024x1024","resolution":"2K","aspect_ratio":"16:9"}`))
	require.NoError(t, err)
	require.False(t, gjson.GetBytes(out, "size").Exists())
	require.Equal(t, "2k", gjson.GetBytes(out, "resolution").String())
	require.Equal(t, "16:9", gjson.GetBytes(out, "aspect_ratio").String())
}

func TestSanitizeGrokMediaForwardBodyConvertsImageSize(t *testing.T) {
	t.Parallel()

	out, contentType, err := sanitizeGrokMediaForwardBody(
		GrokMediaEndpointImagesGenerations,
		[]byte(`{"model":"grok-imagine-image","prompt":"hi","size":"1024x1024"}`),
		"application/json",
	)
	require.NoError(t, err)
	require.Equal(t, "application/json", contentType)
	require.False(t, gjson.GetBytes(out, "size").Exists())
	require.Equal(t, "1k", gjson.GetBytes(out, "resolution").String())
	require.Equal(t, "1:1", gjson.GetBytes(out, "aspect_ratio").String())
}

func TestParseGrokMediaRequestKeepsImageResolutionOutOfVideoNormalize(t *testing.T) {
	t.Parallel()

	info := ParseGrokMediaRequest("application/json", []byte(`{"model":"grok-imagine-image-2.0","resolution":"2K","aspect_ratio":"16:9"}`))
	require.Equal(t, "2k", info.ImageResolution)
	require.Equal(t, "16:9", info.AspectRatio)
	require.Equal(t, VideoBillingResolution480P, info.Resolution)
}

func TestGrokImagineAspectRatioFromSize(t *testing.T) {
	t.Parallel()
	require.Equal(t, "1:1", grokImagineAspectRatioFromSize("1024x1024"))
	require.Equal(t, "3:4", grokImagineAspectRatioFromSize("1152x1536"))
	require.Equal(t, "4:3", grokImagineAspectRatioFromSize("1536x1152"))
	require.Equal(t, "16:9", grokImagineAspectRatioFromSize("1792x1024"))
	require.Equal(t, "16:9", grokImagineAspectRatioFromSize("1280x720"))
}

func TestApplyGrokImagineVideoGeometryMapsCanvasFields(t *testing.T) {
	t.Parallel()

	out, err := applyGrokImagineVideoGeometry([]byte(`{
		"model":"grok-imagine-video-1.5",
		"prompt":"waves",
		"size":"1280x720",
		"resolution_name":"720p",
		"generate_audio":"true",
		"watermark":"false",
		"mode":"frames"
	}`))
	require.NoError(t, err)
	require.False(t, gjson.GetBytes(out, "size").Exists())
	require.False(t, gjson.GetBytes(out, "resolution_name").Exists())
	require.False(t, gjson.GetBytes(out, "generate_audio").Exists())
	require.False(t, gjson.GetBytes(out, "watermark").Exists())
	require.False(t, gjson.GetBytes(out, "mode").Exists())
	require.Equal(t, "720p", gjson.GetBytes(out, "resolution").String())
	require.Equal(t, "16:9", gjson.GetBytes(out, "aspect_ratio").String())
}

func TestAdaptGrokVideoClientResponseExposesOpenAITaskID(t *testing.T) {
	t.Parallel()

	created := adaptGrokVideoClientResponse(
		GrokMediaEndpointVideosGenerations,
		"",
		[]byte(`{"request_id":"eed70fd0-e818-9e8d-9b7f-58442b974104"}`),
	)
	require.Equal(t, "eed70fd0-e818-9e8d-9b7f-58442b974104", gjson.GetBytes(created, "id").String())
	require.Equal(t, "eed70fd0-e818-9e8d-9b7f-58442b974104", gjson.GetBytes(created, "request_id").String())

	status := adaptGrokVideoClientResponse(
		GrokMediaEndpointVideoStatus,
		"task-1",
		[]byte(`{"status":"done","video":{"url":"https://vidgen.x.ai/task-1.mp4"}}`),
	)
	require.Equal(t, "completed", gjson.GetBytes(status, "status").String())
	require.Equal(t, "task-1", gjson.GetBytes(status, "id").String())
	require.Equal(t, "task-1", gjson.GetBytes(status, "request_id").String())
	require.Equal(t, "https://vidgen.x.ai/task-1.mp4", gjson.GetBytes(status, "video.url").String())

	expired := adaptGrokVideoClientResponse(
		GrokMediaEndpointVideoStatus,
		"task-2",
		[]byte(`{"status":"expired"}`),
	)
	require.Equal(t, "failed", gjson.GetBytes(expired, "status").String())
}
