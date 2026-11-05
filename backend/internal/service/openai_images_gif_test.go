package service

import (
	"bytes"
	"context"
	"encoding/base64"
	"image"
	"image/color"
	"image/gif"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
	"github.com/tidwall/sjson"
)

var openAIGIFTestPalette = color.Palette{
	color.RGBA{A: 0xff},
	color.RGBA{R: 0xff, A: 0xff},
	color.RGBA{G: 0xff, A: 0xff},
	color.RGBA{B: 0xff, A: 0xff},
}

// encodeOpenAIGIFTestAnimation builds a full-canvas test animation where every
// frame is filled with one palette color, so a test can tell which source frame
// survived a frame-count rewrite.
func encodeOpenAIGIFTestAnimation(t *testing.T, frames, delayCS, width, height int) []byte {
	t.Helper()
	images := make([]*image.Paletted, frames)
	delays := make([]int, frames)
	for index := range images {
		frame := image.NewPaletted(image.Rect(0, 0, width, height), openAIGIFTestPalette)
		for pixel := range frame.Pix {
			frame.Pix[pixel] = uint8(index % len(openAIGIFTestPalette))
		}
		images[index] = frame
		delays[index] = delayCS
	}
	var buffer bytes.Buffer
	require.NoError(t, gif.EncodeAll(&buffer, &gif.GIF{
		Image:     images,
		Delay:     delays,
		LoopCount: 0,
		Config:    image.Config{ColorModel: openAIGIFTestPalette, Width: width, Height: height},
	}))
	return buffer.Bytes()
}

func decodeOpenAIGIFTestAnimation(t *testing.T, data []byte) *gif.GIF {
	t.Helper()
	animation, err := gif.DecodeAll(bytes.NewReader(data))
	require.NoError(t, err)
	return animation
}

func openAIGIFTestDecodeBase64(t *testing.T, encoded string) []byte {
	t.Helper()
	decoded, err := base64.StdEncoding.DecodeString(encoded)
	require.NoError(t, err)
	return decoded
}

func TestNormalizeOpenAIImagesGIFAnimation_HonorsExplicitControls(t *testing.T) {
	source := encodeOpenAIGIFTestAnimation(t, 9, 20, 64, 64)
	frameCount := 4
	frameDurationMS := 100

	normalized := normalizeOpenAIImagesGIFAnimation(source, openAIGIFAnimationControls{
		frameCount:      &frameCount,
		frameDurationMS: &frameDurationMS,
		size:            "32x32",
	})
	require.False(t, bytes.Equal(source, normalized), "mismatched animation must be rewritten")

	animation := decodeOpenAIGIFTestAnimation(t, normalized)
	require.Len(t, animation.Image, 4)
	require.Equal(t, 32, animation.Config.Width)
	require.Equal(t, 32, animation.Config.Height)
	require.Len(t, animation.Delay, 4)
	for index, delay := range animation.Delay {
		require.Equal(t, 10, delay, "frame %d delay", index)
	}
	for index, frame := range animation.Image {
		require.Equal(t, image.Rect(0, 0, 32, 32), frame.Bounds(), "frame %d bounds", index)
	}
	// Four evenly-spaced frames of nine: 0, 2, 4, 6.
	sourceAnimation := decodeOpenAIGIFTestAnimation(t, source)
	for index, wantSource := range []int{0, 2, 4, 6} {
		require.Equal(
			t,
			color.RGBAModel.Convert(sourceAnimation.Image[wantSource].At(0, 0)),
			color.RGBAModel.Convert(animation.Image[index].At(0, 0)),
			"frame %d samples source frame %d", index, wantSource,
		)
	}
	require.Equal(t, 0, animation.LoopCount)
}

func TestNormalizeOpenAIImagesGIFAnimation_KeepsMatchingAnimation(t *testing.T) {
	source := encodeOpenAIGIFTestAnimation(t, 2, 10, 16, 16)
	frameCount := 2
	frameDurationMS := 100

	normalized := normalizeOpenAIImagesGIFAnimation(source, openAIGIFAnimationControls{
		frameCount:      &frameCount,
		frameDurationMS: &frameDurationMS,
		size:            "16x16",
	})
	require.True(t, bytes.Equal(source, normalized), "an animation that already matches stays byte-identical")
}

func TestNormalizeOpenAIImagesGIFAnimation_WithoutControlsIsNoop(t *testing.T) {
	source := encodeOpenAIGIFTestAnimation(t, 9, 20, 32, 32)
	require.True(t, bytes.Equal(source, normalizeOpenAIImagesGIFAnimation(source, openAIGIFAnimationControls{})))
}

func TestNormalizeOpenAIImagesGIFAnimation_ExpandsFrameCount(t *testing.T) {
	source := encodeOpenAIGIFTestAnimation(t, 3, 20, 16, 16)
	frameCount := 5

	normalized := normalizeOpenAIImagesGIFAnimation(source, openAIGIFAnimationControls{frameCount: &frameCount})
	animation := decodeOpenAIGIFTestAnimation(t, normalized)
	require.Len(t, animation.Image, 5)
	sourceAnimation := decodeOpenAIGIFTestAnimation(t, source)
	for index, wantSource := range []int{0, 0, 1, 1, 2} {
		require.Equal(
			t,
			color.RGBAModel.Convert(sourceAnimation.Image[wantSource].At(0, 0)),
			color.RGBAModel.Convert(animation.Image[index].At(0, 0)),
			"frame %d duplicates source frame %d", index, wantSource,
		)
		for _, delay := range animation.Delay {
			require.Equal(t, 20, delay, "unspecified duration keeps the upstream delay")
		}
	}
}

func TestNormalizeOpenAIImagesGIFAnimation_RoundsFrameDuration(t *testing.T) {
	source := encodeOpenAIGIFTestAnimation(t, 2, 20, 16, 16)
	frameDurationMS := 125

	normalized := normalizeOpenAIImagesGIFAnimation(source, openAIGIFAnimationControls{frameDurationMS: &frameDurationMS})
	animation := decodeOpenAIGIFTestAnimation(t, normalized)
	require.Len(t, animation.Image, 2)
	require.Equal(t, []int{13, 13}, animation.Delay, "125 ms rounds to the 10 ms GIF delay unit")
}

func TestNormalizeOpenAIImagesGIFAnimation_IgnoresOutOfRangeControls(t *testing.T) {
	source := encodeOpenAIGIFTestAnimation(t, 9, 20, 16, 16)
	for _, frameCount := range []int{0, 1, 17, 64} {
		require.True(
			t,
			bytes.Equal(source, normalizeOpenAIImagesGIFAnimation(source, openAIGIFAnimationControls{frameCount: &frameCount})),
			"frame_count=%d stays upstream-owned", frameCount,
		)
	}
	for _, duration := range []int{0, 5, 19, 2001} {
		require.True(
			t,
			bytes.Equal(source, normalizeOpenAIImagesGIFAnimation(source, openAIGIFAnimationControls{frameDurationMS: &duration})),
			"frame_duration_ms=%d stays upstream-owned", duration,
		)
	}
	require.True(t, bytes.Equal(source, normalizeOpenAIImagesGIFAnimation(source, openAIGIFAnimationControls{size: "auto"})))
}

func TestNormalizeOpenAIImagesGIFAnimation_LeavesNonGIFBytesUntouched(t *testing.T) {
	png := []byte("\x89PNG\r\n\x1a\n not really a png")
	frameCount := 4
	require.True(t, bytes.Equal(png, normalizeOpenAIImagesGIFAnimation(png, openAIGIFAnimationControls{frameCount: &frameCount})))
}

func TestNormalizeOpenAIImagesGIFAnimationResponseBody_RewritesGIFItemsOnly(t *testing.T) {
	gifSource := encodeOpenAIGIFTestAnimation(t, 9, 20, 32, 32)
	pngPayload := []byte("not a gif")
	body := []byte(`{"created":1,"data":[{"b64_json":""},{"b64_json":""},{"b64_json":""}]}`)
	var err error
	body, err = sjson.SetBytes(body, "data.0.b64_json", base64.StdEncoding.EncodeToString(gifSource))
	require.NoError(t, err)
	body, err = sjson.SetBytes(body, "data.0.output_format", "gif")
	require.NoError(t, err)
	body, err = sjson.SetBytes(body, "data.1.b64_json", base64.StdEncoding.EncodeToString(pngPayload))
	require.NoError(t, err)
	body, err = sjson.SetBytes(body, "data.1.output_format", "png")
	require.NoError(t, err)

	frameCount := 2
	parsed := &OpenAIImagesRequest{OutputFormat: "gif", FrameCount: &frameCount}
	normalizedBody := normalizeOpenAIImagesGIFAnimationResponseBody(body, parsed)

	rewritten := decodeOpenAIGIFTestAnimation(t, openAIGIFTestDecodeBase64(t, gjson.GetBytes(normalizedBody, "data.0.b64_json").String()))
	require.Len(t, rewritten.Image, 2)
	require.Equal(t, base64.StdEncoding.EncodeToString(pngPayload), gjson.GetBytes(normalizedBody, "data.1.b64_json").String(), "non-GIF results are untouched")
}

func TestNormalizeOpenAIImagesGIFAnimationResponseBody_WithoutOutputFormatUsesItemFormat(t *testing.T) {
	gifSource := encodeOpenAIGIFTestAnimation(t, 9, 20, 16, 16)
	body := []byte(`{"data":[{"b64_json":"","output_format":"gif"}]}`)
	body, err := sjson.SetBytes(body, "data.0.b64_json", base64.StdEncoding.EncodeToString(gifSource))
	require.NoError(t, err)

	frameDurationMS := 600
	parsed := &OpenAIImagesRequest{FrameDurationMS: &frameDurationMS}
	normalizedBody := normalizeOpenAIImagesGIFAnimationResponseBody(body, parsed)
	animation := decodeOpenAIGIFTestAnimation(t, openAIGIFTestDecodeBase64(t, gjson.GetBytes(normalizedBody, "data.0.b64_json").String()))
	require.Len(t, animation.Image, 9, "the request did not change the frame count")
	for _, delay := range animation.Delay {
		require.Equal(t, 60, delay)
	}
}

func TestNormalizeOpenAIImagesGIFAnimationEvent(t *testing.T) {
	gifSource := encodeOpenAIGIFTestAnimation(t, 9, 20, 16, 16)
	event := []byte(`{"type":"image_generation.completed","b64_json":"","output_format":"gif"}`)
	event, err := sjson.SetBytes(event, "b64_json", base64.StdEncoding.EncodeToString(gifSource))
	require.NoError(t, err)

	frameCount := 4
	parsed := &OpenAIImagesRequest{OutputFormat: "gif", FrameCount: &frameCount}
	normalizedEvent := normalizeOpenAIImagesGIFAnimationEvent(event, parsed)
	require.Equal(t, "image_generation.completed", gjson.GetBytes(normalizedEvent, "type").String())
	animation := decodeOpenAIGIFTestAnimation(t, openAIGIFTestDecodeBase64(t, gjson.GetBytes(normalizedEvent, "b64_json").String()))
	require.Len(t, animation.Image, 4)
}

func TestResizeOpenAIImagesGIFFrame_PreservesSubRectOffsets(t *testing.T) {
	source := image.NewPaletted(image.Rect(8, 8, 24, 24), openAIGIFTestPalette)
	resized := resizeOpenAIImagesGIFFrame(source, 64, 64, 32, 32)
	require.Equal(t, image.Rect(4, 4, 12, 12), resized.Bounds())
}

func TestOpenAIGatewayServiceForwardImages_APIKeyHonorsGIFAnimation(t *testing.T) {
	gin.SetMode(gin.TestMode)
	body := []byte(`{"model":"gpt-image-2.6-flare","prompt":"a looping lantern","size":"32x32","output_format":"gif","frame_count":2,"frame_duration_ms":100}`)
	c, rec := newOpenAIImagesTestContext(t, body)

	upstreamGIF := encodeOpenAIGIFTestAnimation(t, 9, 20, 64, 64)
	upstream := &httpUpstreamRecorder{resp: &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": []string{"application/json"}},
		Body: io.NopCloser(strings.NewReader(
			`{"created":1710000000,"data":[{"b64_json":"` + base64.StdEncoding.EncodeToString(upstreamGIF) + `","output_format":"gif","size":"64x64"}]}`,
		)),
	}}
	svc := newOpenAIImagesTestService(upstream)
	parsed, err := svc.ParseOpenAIImagesRequest(c, body)
	require.NoError(t, err)

	_, err = svc.ForwardImages(context.Background(), c, newOpenAIImagesAPIKeyAccount(), body, parsed, "")
	require.NoError(t, err)

	animation := decodeOpenAIGIFTestAnimation(t, openAIGIFTestDecodeBase64(t, gjson.Get(rec.Body.String(), "data.0.b64_json").String()))
	require.Len(t, animation.Image, 2)
	require.Equal(t, 32, animation.Config.Width)
	require.Equal(t, 32, animation.Config.Height)
	require.Equal(t, []int{10, 10}, animation.Delay)
}

func TestOpenAIGatewayServiceForwardImages_OAuthDirectStreamingHonorsGIFAnimation(t *testing.T) {
	gin.SetMode(gin.TestMode)
	body := []byte(`{"model":"gpt-image-2.6-flare","prompt":"a looping lantern","size":"16x16","output_format":"gif","frame_count":4,"frame_duration_ms":200,"stream":true}`)
	c, rec := newOpenAIImagesTestContext(t, body)

	upstreamGIF := encodeOpenAIGIFTestAnimation(t, 9, 20, 64, 64)
	streamBody := "event: image_generation.completed\n" +
		"data: {\"type\":\"image_generation.completed\",\"b64_json\":\"" + base64.StdEncoding.EncodeToString(upstreamGIF) + "\",\"output_format\":\"gif\",\"usage\":{\"input_tokens\":11,\"output_tokens\":22}}\n\n"
	upstream := &httpUpstreamRecorder{resp: &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": []string{"text/event-stream"}},
		Body:       io.NopCloser(strings.NewReader(streamBody)),
	}}
	svc := newOpenAIImagesTestService(upstream)
	parsed, err := svc.ParseOpenAIImagesRequest(c, body)
	require.NoError(t, err)

	_, err = svc.ForwardImages(context.Background(), c, newDirectImagesOAuthAccount(), body, parsed, "")
	require.NoError(t, err)

	var completedData string
	for _, line := range strings.Split(rec.Body.String(), "\n") {
		if strings.HasPrefix(line, "data: ") && strings.Contains(line, "image_generation.completed") {
			completedData = strings.TrimPrefix(line, "data: ")
		}
	}
	require.NotEmpty(t, completedData)
	animation := decodeOpenAIGIFTestAnimation(t, openAIGIFTestDecodeBase64(t, gjson.Get(completedData, "b64_json").String()))
	require.Len(t, animation.Image, 4)
	require.Equal(t, 16, animation.Config.Width)
	require.Equal(t, 16, animation.Config.Height)
	require.Equal(t, []int{20, 20, 20, 20}, animation.Delay)
}

func TestScanOpenAIGIFMetadata(t *testing.T) {
	source := encodeOpenAIGIFTestAnimation(t, 9, 20, 64, 48)
	metadata, ok := scanOpenAIGIFMetadata(source)
	require.True(t, ok)
	require.Equal(t, 64, metadata.width)
	require.Equal(t, 48, metadata.height)
	require.Equal(t, 9, metadata.frameCount)
	require.Len(t, metadata.delays, 9)
	for _, delay := range metadata.delays {
		require.Equal(t, 20, delay)
	}

	_, ok = scanOpenAIGIFMetadata([]byte("not a gif at all"))
	require.False(t, ok)
}

// encodeOpenAIGIFDeltaTestAnimation builds an animation whose frames after the
// first only encode the rectangle that changed since the previous frame, like
// Pillow's default encoder does, and returns the logical fully rendered frames
// so a test can assert what the animation should look like.
func encodeOpenAIGIFDeltaTestAnimation(t *testing.T, frames, width, height int) ([]byte, []*image.Paletted) {
	t.Helper()
	palette := color.Palette{
		color.RGBA{A: 0xff},
		color.RGBA{B: 0xff, A: 0xff},
		color.RGBA{R: 0xff, A: 0xff},
	}
	logical := make([]*image.Paletted, frames)
	for index := range logical {
		frame := image.NewPaletted(image.Rect(0, 0, width, height), palette)
		for y := 0; y < height; y++ {
			for x := 0; x < width; x++ {
				if (x/8+y/8)%2 == 0 {
					frame.SetColorIndex(x, y, 0)
				} else {
					frame.SetColorIndex(x, y, 1)
				}
			}
		}
		// A 10x10 block marching right by six pixels per frame, which is the
		// pattern that exposes delta-frame sampling bugs.
		left := 4 + index*6
		for y := 20; y < 30; y++ {
			for x := left; x < left+10; x++ {
				frame.SetColorIndex(x, y, 2)
			}
		}
		logical[index] = frame
	}
	delta := make([]*image.Paletted, frames)
	delta[0] = logical[0]
	for index := 1; index < frames; index++ {
		bounds := openAIGIFTestDifferenceBounds(logical[index-1], logical[index])
		require.False(t, bounds.Empty(), "frame %d must differ from its predecessor", index)
		frame := image.NewPaletted(bounds, palette)
		for y := bounds.Min.Y; y < bounds.Max.Y; y++ {
			for x := bounds.Min.X; x < bounds.Max.X; x++ {
				frame.SetColorIndex(x, y, logical[index].ColorIndexAt(x, y))
			}
		}
		delta[index] = frame
	}
	delays := make([]int, frames)
	for index := range delays {
		delays[index] = 20
	}
	var buffer bytes.Buffer
	require.NoError(t, gif.EncodeAll(&buffer, &gif.GIF{
		Image:     delta,
		Delay:     delays,
		LoopCount: 0,
		Config:    image.Config{ColorModel: palette, Width: width, Height: height},
	}))
	return buffer.Bytes(), logical
}

func openAIGIFTestDifferenceBounds(previous, current *image.Paletted) image.Rectangle {
	bounds := previous.Bounds().Union(current.Bounds())
	result := image.Rectangle{Min: image.Pt(bounds.Max.X, bounds.Max.Y), Max: image.Pt(bounds.Min.X, bounds.Min.Y)}
	for y := bounds.Min.Y; y < bounds.Max.Y; y++ {
		for x := bounds.Min.X; x < bounds.Max.X; x++ {
			if previous.ColorIndexAt(x, y) == current.ColorIndexAt(x, y) {
				continue
			}
			result = result.Union(image.Rect(x, y, x+1, y+1))
		}
	}
	return result
}

func requireOpenAIGIFTestFrameMatches(t *testing.T, want, got *image.Paletted, frameIndex int) {
	t.Helper()
	require.Equal(t, want.Bounds(), got.Bounds(), "frame %d covers the whole canvas", frameIndex)
	for y := want.Bounds().Min.Y; y < want.Bounds().Max.Y; y++ {
		for x := want.Bounds().Min.X; x < want.Bounds().Max.X; x++ {
			require.Equal(
				t,
				color.RGBAModel.Convert(want.At(x, y)),
				color.RGBAModel.Convert(got.At(x, y)),
				"frame %d pixel (%d, %d)", frameIndex, x, y,
			)
		}
	}
}

// TestNormalizeOpenAIImagesGIFAnimation_ReducedFrameCountComposesDeltaFrames is
// the regression test for sampling delta frames directly: dropping frames left
// the dropped frames' pixels on the canvas (残影 / 花屏).
func TestNormalizeOpenAIImagesGIFAnimation_ReducedFrameCountComposesDeltaFrames(t *testing.T) {
	source, logical := encodeOpenAIGIFDeltaTestAnimation(t, 9, 64, 64)
	decodedSource := decodeOpenAIGIFTestAnimation(t, source)
	deltaFrames := 0
	for _, frame := range decodedSource.Image {
		if frame.Bounds() != decodedSource.Image[0].Bounds() {
			deltaFrames++
		}
	}
	require.Positive(t, deltaFrames, "fixture must contain sub-rectangle delta frames")

	for _, target := range []int{2, 4, 5} {
		frameCount := target
		normalized := normalizeOpenAIImagesGIFAnimation(source, openAIGIFAnimationControls{frameCount: &frameCount})
		require.False(t, bytes.Equal(source, normalized), "frame_count=%d must rewrite the animation", target)
		animation := decodeOpenAIGIFTestAnimation(t, normalized)
		require.Len(t, animation.Image, target)
		for index, frame := range animation.Image {
			want := logical[openAIGIFFrameIndex(index, len(logical), target)]
			requireOpenAIGIFTestFrameMatches(t, want, frame, index)
		}
	}
}

// reduce without other controls must not be affected; check a matching
// frame_count stays byte-identical.
func TestNormalizeOpenAIImagesGIFAnimation_DeltaFrameCountUnchangedIsNoop(t *testing.T) {
	source, _ := encodeOpenAIGIFDeltaTestAnimation(t, 9, 64, 64)
	frameCount := 9
	require.True(t, bytes.Equal(source, normalizeOpenAIImagesGIFAnimation(source, openAIGIFAnimationControls{frameCount: &frameCount})))
}
