package service

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestParseOpenAISSEDataFramePreservesEffectiveTypeSemantics(t *testing.T) {
	tests := []struct {
		name         string
		data         string
		fallbackType string
		wantType     string
		wantValid    bool
		wantDone     bool
	}{
		{name: "payload wins", data: `{"type":"response.completed"}`, fallbackType: "error", wantType: "response.completed", wantValid: true},
		{name: "event field fallback", data: `{"response":{"id":"resp_1"}}`, fallbackType: " response.done ", wantType: "response.done", wantValid: true},
		{name: "done sentinel", data: " [DONE] ", fallbackType: "response.completed", wantType: "response.completed", wantDone: true},
		{name: "malformed keeps best effort type", data: `{"type":"response.completed"} trailing`, fallbackType: "error", wantType: "response.completed"},
		{name: "empty", data: " \t ", fallbackType: " response.in_progress ", wantType: "response.in_progress"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			frame := parseOpenAISSEDataFrame([]byte(tt.data), tt.fallbackType)
			require.Equal(t, tt.wantType, frame.eventType)
			require.Equal(t, tt.wantValid, frame.validJSON)
			require.Equal(t, tt.wantDone, frame.isDone())
			require.Equal(t, effectiveOpenAISSEEventType([]byte(tt.data), tt.fallbackType), frame.eventType)
		})
	}
}

func TestOpenAISSEFrameHotPathClassifiers(t *testing.T) {
	delta := parseOpenAISSEDataFrame([]byte(`{"type":"response.output_text.delta","delta":"hello"}`), "")
	require.True(t, openAIStreamFrameStartsVisibleOutput(delta))
	require.False(t, openAISSEFrameMayContainImageOutput(delta))
	require.False(t, openAISSEFrameMayContainGrokSearch(delta))
	require.False(t, openAISSEEventMayNormalizeImageStatus(delta.eventType))

	done := parseOpenAISSEDataFrame([]byte(`{"type":"response.output_item.done","item":{"type":"web_search_call","id":"ws_1"}}`), "")
	require.True(t, openAISSEFrameMayContainImageOutput(done))
	require.True(t, openAISSEFrameMayContainGrokSearch(done))
	require.True(t, openAISSEEventMayNormalizeImageStatus(done.eventType))

	vendorImages := parseOpenAISSEDataFrame([]byte(`{"type":"vendor.images.done","data":[{"b64_json":"abc"}]}`), "")
	require.True(t, openAISSEFrameMayContainImageOutput(vendorImages))

	invalid := parseOpenAISSEDataFrame([]byte(`{"type":"response.output_item.done"`), "")
	require.False(t, openAISSEFrameMayContainImageOutput(invalid))
	require.False(t, openAISSEFrameMayContainGrokSearch(invalid))
}

func TestSplitOpenAIConcatenatedJSONDocumentsPrefilterPreservesRepairSemantics(t *testing.T) {
	require.False(t, mayContainOpenAIConcatenatedJSONDocuments([]byte(`{"type":"response.output_text.delta","delta":"hello"}`)))
	require.True(t, mayContainOpenAIConcatenatedJSONDocuments([]byte("{\"type\":\"response.created\"} \r\n\t {\"type\":\"response.completed\"}")))

	documents, repaired := splitOpenAIConcatenatedJSONDocuments([]byte("{\"type\":\"response.created\"} \r\n\t {\"type\":\"response.completed\"}"))
	require.True(t, repaired)
	require.Len(t, documents, 2)

	// The prefilter may conservatively match a boundary-looking string. The
	// decoder must still reject it as a single valid document, unchanged.
	documents, repaired = splitOpenAIConcatenatedJSONDocuments([]byte(`{"type":"response.output_text.delta","delta":"} {"}`))
	require.False(t, repaired)
	require.Nil(t, documents)
}

func TestOpenAIStreamingCountsGrokSearchOnlyForGrokPlatform(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, tt := range []struct {
		platform string
		want     int
	}{
		{platform: PlatformOpenAI, want: 0},
		{platform: PlatformGrok, want: 1},
	} {
		t.Run(tt.platform, func(t *testing.T) {
			body := strings.Join([]string{
				`data: {"type":"response.output_item.done","output_index":0,"item":{"type":"web_search_call","id":"ws_1","call_id":"call_1"}}`,
				"",
				`data: {"type":"response.completed","response":{"id":"resp_1","output":[{"type":"web_search_call","id":"ws_1","call_id":"call_1"}],"usage":{"input_tokens":4,"output_tokens":2}}}`,
				"",
			}, "\n")
			resp := &http.Response{
				StatusCode: http.StatusOK,
				Header:     http.Header{"Content-Type": []string{"text/event-stream"}},
				Body:       io.NopCloser(strings.NewReader(body)),
			}
			recorder := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(recorder)
			c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", nil)
			svc := &OpenAIGatewayService{
				cfg: &config.Config{Gateway: config.GatewayConfig{
					MaxLineSize: defaultMaxLineSize,
				}},
				toolCorrector: NewCodexToolCorrector(),
			}
			account := &Account{ID: 1, Name: "hot-path-test", Platform: tt.platform, Type: AccountTypeAPIKey}

			result, err := svc.handleStreamingResponse(context.Background(), resp, c, account, time.Now(), "gpt-test", "gpt-test")
			require.NoError(t, err)
			require.NotNil(t, result)
			require.Equal(t, tt.want, result.searchCount)
			require.Equal(t, 4, result.usage.InputTokens)
			require.Equal(t, 2, result.usage.OutputTokens)
		})
	}
}

var (
	benchmarkOpenAISSEHotPathBoolSink  bool
	benchmarkOpenAISSEHotPathIntSink   int
	benchmarkOpenAISSEHotPathBytesSink []byte
)

func BenchmarkOpenAIResponsesSSEHotPath(b *testing.B) {
	data := []byte(`{"type":"response.output_text.delta","sequence_number":42,"delta":"streaming response benchmark payload"}`)

	b.Run("legacy repeated frame scans", func(b *testing.B) {
		imageCounter := newOpenAIImageOutputCounter()
		doneItems := newResponsesStreamOutputItems()
		seenSearch := make(map[string]struct{})
		seenImages := make(map[string]struct{})
		b.ReportAllocs()
		b.SetBytes(int64(len(data)))
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			benchmarkOpenAISSEHotPathBytesSink, benchmarkOpenAISSEHotPathBoolSink = normalizeCompletedImageGenerationStatus(data)
			imageCounter.AddSSEData(data)
			benchmarkOpenAISSEHotPathIntSink = countGrokNativeSearchCallsInSSEDataDedup(data, seenSearch)
			_, benchmarkOpenAISSEHotPathBoolSink = extractImageGenerationOutputFromSSEData(data, seenImages)
			doneItems.Observe(data)
			benchmarkOpenAISSEHotPathBoolSink = openAIStreamDataStartsVisibleOutput(string(data), "")
		}
	})

	b.Run("single parsed frame with guards", func(b *testing.B) {
		imageCounter := newOpenAIImageOutputCounter()
		doneItems := newResponsesStreamOutputItems()
		seenImages := make(map[string]struct{})
		b.ReportAllocs()
		b.SetBytes(int64(len(data)))
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			frame := parseOpenAISSEDataFrame(data, "")
			if openAISSEEventMayNormalizeImageStatus(frame.eventType) {
				benchmarkOpenAISSEHotPathBytesSink, benchmarkOpenAISSEHotPathBoolSink = normalizeCompletedImageGenerationStatusFrame(frame)
			}
			if openAISSEFrameMayContainImageOutput(frame) {
				imageCounter.AddSSEFrame(frame)
			}
			// PlatformOpenAI bypasses Grok search accounting entirely.
			if frame.eventType == "response.output_item.done" {
				_, benchmarkOpenAISSEHotPathBoolSink = extractImageGenerationOutputFromSSEFrame(frame, seenImages)
				doneItems.ObserveFrame(frame)
			}
			benchmarkOpenAISSEHotPathBoolSink = openAIStreamFrameStartsVisibleOutput(frame)
		}
	})
}
