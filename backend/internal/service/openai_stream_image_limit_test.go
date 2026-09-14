package service

import (
	"bufio"
	"bytes"
	"io"
	"strings"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/pkg/apicompat"
	"github.com/stretchr/testify/require"
)

func largeImageRegressionSSE() string {
	return "data: {\"type\":\"response.image_generation_call.partial_image\",\"partial_image_b64\":\"" +
		strings.Repeat("A", 2*1024*1024) + "\"}\n\n" +
		"data: {\"type\":\"response.completed\",\"response\":{\"id\":\"resp_image\",\"status\":\"completed\",\"output\":[],\"usage\":{\"input_tokens\":3,\"output_tokens\":2}}}\n\n"
}

func TestOpenAIStreamingPassthroughPreservesLargeImage(t *testing.T) {
	body := largeImageRegressionSSE()
	result, recorder, _, err := runPassthroughFlushTest(t, io.NopCloser(strings.NewReader(body)), -1)
	require.NoError(t, err)
	require.NotNil(t, result)
	require.Equal(t, len(body), recorder.Body.Len())
	require.True(t, body == recorder.Body.String())
	require.Equal(t, 3, result.usage.InputTokens)
}

func TestResponsesClientToolStreamPreservesLargeImage(t *testing.T) {
	body := largeImageRegressionSSE()
	reader, writer := io.Pipe()
	go transformResponsesClientToolStream(io.NopCloser(strings.NewReader(body)), writer, apicompat.ResponsesClientToolMapping{}, defaultMaxLineSize)
	got, err := io.ReadAll(reader)
	require.NoError(t, reader.Close())
	require.NoError(t, err)
	require.True(t, bytes.Contains(got, []byte(strings.Repeat("A", 2*1024*1024))))
	require.Contains(t, string(got[len(got)-200:]), "response.completed")
}

func TestOpenAICompatScannerPreservesLargeImageAndConfiguredLimit(t *testing.T) {
	t.Run("image event", func(t *testing.T) {
		svc := &OpenAIGatewayService{}
		scanner := svc.newUpstreamSSEScanner(strings.NewReader(largeImageRegressionSSE()))
		require.True(t, scanner.Scan())
		require.Greater(t, len(scanner.Bytes()), 1024*1024)
		require.NoError(t, scanner.Err())
	})
	t.Run("configured smaller limit", func(t *testing.T) {
		svc := &OpenAIGatewayService{cfg: &config.Config{Gateway: config.GatewayConfig{MaxLineSize: 1024}}}
		scanner := svc.newUpstreamSSEScanner(strings.NewReader(strings.Repeat("x", 2048)))
		require.False(t, scanner.Scan())
		require.ErrorIs(t, scanner.Err(), bufio.ErrTooLong)
	})
	t.Run("oversized event remains bounded", func(t *testing.T) {
		svc := &OpenAIGatewayService{cfg: &config.Config{Gateway: config.GatewayConfig{MaxLineSize: defaultMaxLineSize}}}
		scanner := svc.newUpstreamSSEScanner(strings.NewReader(strings.Repeat("x", sseScannerTokenMax+1)))
		require.False(t, scanner.Scan())
		require.ErrorIs(t, scanner.Err(), bufio.ErrTooLong)
	})
}
