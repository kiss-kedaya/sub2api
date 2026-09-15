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

func TestOpenAIFrameTTFTClassificationMatchesDataEntryPoint(t *testing.T) {
	tests := []struct {
		name         string
		data         string
		fallbackType string
		wantClient   bool
		wantVisible  bool
		wantSemantic bool
	}{
		{name: "empty"},
		{name: "whitespace", data: " \r\n\t "},
		{name: "empty with type", fallbackType: "response.output_text.delta"},
		{name: "done marker", data: " [DONE] ", fallbackType: "response.completed", wantClient: true},
		{name: "preamble payload", data: `{"type":"response.created"}`},
		{name: "preamble fallback", data: `{}`, fallbackType: " response.in_progress "},
		{name: "payload wins", data: `{"type":"response.created","delta":"hello"}`, fallbackType: "response.output_text.delta"},
		{name: "empty type uses fallback", data: `{"type":" ","delta":"hello"}`, fallbackType: " response.output_text.delta ", wantClient: true, wantVisible: true, wantSemantic: true},
		{name: "trimmed payload type", data: `{"type":" response.output_text.delta ","delta":"hello"}`, wantClient: true, wantVisible: true, wantSemantic: true},
		{name: "delta without payload type", data: `{"delta":"hello"}`, fallbackType: "response.output_text.delta", wantClient: true, wantVisible: true, wantSemantic: true},
		{name: "semantic delta", data: `{"type":"response.output_text.delta","delta":"hello"}`, wantClient: true, wantVisible: true, wantSemantic: true},
		{name: "empty delta", data: `{"type":"response.output_text.delta","delta":""}`, wantClient: true, wantSemantic: true},
		{name: "null delta", data: `{"type":"response.output_text.delta","delta":null}`, wantClient: true, wantSemantic: true},
		{name: "whitespace delta", data: `{"type":"response.output_text.delta","delta":" "}`, wantClient: true, wantVisible: true, wantSemantic: true},
		{name: "numeric delta compatibility", data: `{"type":"response.output_text.delta","delta":0}`, wantClient: true, wantVisible: true, wantSemantic: true},
		{name: "empty added reasoning", data: `{"type":"response.output_item.added","item":{"type":"reasoning","summary":[]}}`, wantSemantic: true},
		{name: "empty added text", data: `{"type":"response.content_part.added","part":{"type":"output_text","text":""}}`, wantSemantic: true},
		{name: "tool arguments", data: `{"type":"response.function_call_arguments.done","arguments":"{}"}`, wantClient: true, wantVisible: true, wantSemantic: true},
		{name: "partial image", data: `{"type":"response.image_generation_call.partial_image","partial_image_b64":"YWJj"}`, wantClient: true, wantVisible: true, wantSemantic: true},
		{name: "empty completed", data: `{"type":"response.completed","response":{"output":[]}}`, wantClient: true, wantSemantic: true},
		{name: "completed text", data: `{"type":"response.completed","response":{"output":[{"type":"message","content":[{"type":"output_text","text":"hello"}]}]}}`, wantClient: true, wantVisible: true, wantSemantic: true},
		{name: "failed", data: `{"type":"response.failed"}`},
		{name: "retryable error", data: `{"type":"error","error":{"code":"server_is_overloaded","message":"server is overloaded"}}`},
		{name: "nonretryable error", data: `{"type":"error","error":{"code":"invalid_request_error","message":"invalid request"}}`, wantClient: true, wantSemantic: true},
		{name: "unknown event", data: `{"type":"vendor.progress"}`, wantClient: true, wantSemantic: true},
		{name: "no type", data: `{}`, wantClient: true, wantSemantic: true},
		{name: "scalar", data: `null`, wantClient: true, wantSemantic: true},
		{name: "malformed untyped", data: `{`, wantClient: true, wantSemantic: true},
		{name: "malformed preamble", data: `{"type":"response.created"} trailing`},
		{name: "malformed delta", data: `{"type":"response.output_text.delta","delta":"hello"} trailing`, wantClient: true, wantSemantic: true},
		{name: "malformed with fallback", data: `{`, fallbackType: "response.in_progress"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			frame := parseOpenAISSEDataFrame([]byte(tt.data), tt.fallbackType)
			// Both streaming loops pass the effective type to the legacy entry points.
			require.Equal(t, tt.wantClient, openAIStreamDataStartsClientOutput(tt.data, frame.eventType))
			require.Equal(t, tt.wantClient, openAIStreamFrameStartsClientOutput(frame))
			require.Equal(t, tt.wantVisible, openAIStreamDataStartsVisibleOutput(tt.data, frame.eventType))
			require.Equal(t, tt.wantVisible, openAIStreamFrameStartsVisibleOutput(frame))
			require.Equal(t, tt.wantSemantic, openAIStreamDataStartsSemanticTTFT(tt.data, frame.eventType))
			require.Equal(t, tt.wantSemantic, openAIStreamFrameStartsSemanticTTFT(frame))
			for _, mode := range []string{OpenAITTFTModeSemantic, OpenAITTFTModeVisible, "", "unknown"} {
				for _, forceOutput := range []bool{false, true} {
					want := forceOutput || tt.wantSemantic
					if mode == OpenAITTFTModeVisible {
						want = tt.wantVisible
					}
					require.Equal(t, want, openAIStreamDataStartsTTFT(tt.data, frame.eventType, forceOutput, mode), "data mode=%q force=%t", mode, forceOutput)
					require.Equal(t, want, openAIStreamFrameStartsTTFT(frame, forceOutput, mode), "frame mode=%q force=%t", mode, forceOutput)
				}
			}
		})
	}
}

func TestOpenAIFrameTTFTReusesParsedPayload(t *testing.T) {
	data := []byte(`{"type":"response.image_generation_call.partial_image","partial_image_b64":"` + strings.Repeat("A", 16*1024) + `"}`)
	frame := parseOpenAISSEDataFrame(data, "")
	allocs := testing.AllocsPerRun(100, func() {
		benchmarkOpenAISSEHotPathBoolSink = openAIStreamFrameStartsTTFT(frame, false, OpenAITTFTModeVisible)
	})
	require.True(t, benchmarkOpenAISSEHotPathBoolSink)
	require.Zero(t, allocs, "classifying an already parsed plain image payload must not allocate another frame")
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

func BenchmarkOpenAIPassthroughFrameTTFT(b *testing.B) {
	for _, payload := range []struct {
		name string
		data []byte
	}{
		{"text", []byte(`{"type":"response.output_text.delta","sequence_number":42,"delta":"streaming response benchmark payload"}`)},
		{"image16K", []byte(`{"type":"response.image_generation_call.partial_image","partial_image_b64":"` + strings.Repeat("A", 16*1024) + `"}`)},
	} {
		for _, mode := range []string{OpenAITTFTModeSemantic, OpenAITTFTModeVisible} {
			b.Run(payload.name+"/"+mode+"/data", func(b *testing.B) {
				data := string(payload.data)
				b.ReportAllocs()
				b.ResetTimer()
				for i := 0; i < b.N; i++ {
					frame := parseOpenAISSEDataFrame(payload.data, "")
					benchmarkOpenAISSEHotPathBoolSink = openAIStreamDataStartsTTFT(data, frame.eventType, false, mode)
				}
			})
			b.Run(payload.name+"/"+mode+"/frame", func(b *testing.B) {
				b.ReportAllocs()
				b.ResetTimer()
				for i := 0; i < b.N; i++ {
					frame := parseOpenAISSEDataFrame(payload.data, "")
					benchmarkOpenAISSEHotPathBoolSink = openAIStreamFrameStartsTTFT(frame, false, mode)
				}
			})
		}
	}
}

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

	payload := string(data)
	eventType := "response.output_text.delta"
	b.Run("three classifiers parse data", func(b *testing.B) {
		b.ReportAllocs()
		b.SetBytes(int64(len(data)))
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			benchmarkOpenAISSEHotPathIntSink = 0
			if openAIStreamDataStartsClientOutput(payload, eventType) {
				benchmarkOpenAISSEHotPathIntSink++
			}
			if openAIStreamDataStartsVisibleOutput(payload, eventType) {
				benchmarkOpenAISSEHotPathIntSink++
			}
			if openAIStreamDataStartsTTFT(payload, eventType, false, OpenAITTFTModeVisible) {
				benchmarkOpenAISSEHotPathIntSink++
			}
		}
	})

	b.Run("three classifiers reuse frame", func(b *testing.B) {
		b.ReportAllocs()
		b.SetBytes(int64(len(data)))
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			frame := parseOpenAISSEDataFrame(data, eventType)
			benchmarkOpenAISSEHotPathIntSink = 0
			if openAIStreamFrameStartsClientOutput(frame) {
				benchmarkOpenAISSEHotPathIntSink++
			}
			if openAIStreamFrameStartsVisibleOutput(frame) {
				benchmarkOpenAISSEHotPathIntSink++
			}
			if openAIStreamFrameStartsTTFT(frame, false, OpenAITTFTModeVisible) {
				benchmarkOpenAISSEHotPathIntSink++
			}
		}
	})
}
