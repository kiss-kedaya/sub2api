package service

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/tidwall/gjson"
)

type track1CorpusSample struct {
	name            string
	responsesNoop   []byte
	responsesHit    []byte
	responsesEsc    []byte
	chatCanonical   []byte
	chatReordered   []byte
	compatSeedSmall json.RawMessage
	compatSeedItem  json.RawMessage
}

func buildTrack1Corpus() []track1CorpusSample {
	specs := []struct {
		name      string
		turns     int
		padBytes  int
		toolCalls int
	}{
		{name: "Small_1.5KB", turns: 3, padBytes: 180, toolCalls: 1},
		{name: "Medium_35KB", turns: 22, padBytes: 1100, toolCalls: 2},
		{name: "Large_280KB", turns: 80, padBytes: 2800, toolCalls: 3},
	}

	out := make([]track1CorpusSample, 0, len(specs))
	for _, sp := range specs {
		pad := strings.Repeat("a", sp.padBytes)
		padEsc := strings.Repeat("log line\\u001b[0m ", max(1, sp.padBytes/18))

		// Build Responses API payload (input array + reasoning + function_call_output + instructions).
		var respNoop strings.Builder
		_, _ = respNoop.WriteString(`{"model":"gpt-5.4","store":false,"instructions":"You are a careful coding assistant.","reasoning":{"effort":"high"},"input":[`)
		for i := 0; i < sp.turns; i++ {
			if i > 0 {
				_ = respNoop.WriteByte(',')
			}
			fmt.Fprintf(&respNoop,
				`{"type":"message","role":"user","content":[{"type":"input_text","text":"step %d inspect %s"}]},`+
					`{"type":"reasoning","id":"enc_%d","encrypted_content":"opaque_blob_%d_%s","summary":[{"type":"summary_text","text":"thinking %d"}]},`+
					`{"type":"function_call","id":"fc_%d","call_id":"call_%d","name":"read_file","arguments":"{\"path\":\"main.go\"}"},`+
					`{"type":"function_call_output","call_id":"call_%d","output":"content_%d_%s"}`,
				i, pad[:min(64, len(pad))], i, i, pad[:min(48, len(pad))], i, i, i, i, i, pad)
		}
		_, _ = respNoop.WriteString(`],"tools":[{"type":"function","name":"read_file","parameters":{"type":"object","properties":{"path":{"type":"string"}},"required":["path"]}}]}`)

		respHit := strings.Replace(respNoop.String(),
			`"reasoning":{"effort":"high"}`,
			`"reasoning":{"summary":"auto","effort":"high"}`, 1)
		respHit = strings.Replace(respHit,
			`"type":"reasoning","id":"enc_0"`,
			`"type":"reasoning","id":"rs_server_0"`, 1)

		respEsc := strings.Replace(respNoop.String(),
			`"output":"content_0_`,
			`"output":"`+padEsc+`_`, 1)

		// Build Chat Completions canonical + non-canonical/reordered DeepSeek payload.
		var chatCanon strings.Builder
		_, _ = chatCanon.WriteString(`{"model":"deepseek-chat","messages":[`)
		fmt.Fprintf(&chatCanon, `{"role":"system","content":"system prompt %s"}`, pad[:min(64, len(pad))])
		for i := 0; i < sp.turns; i++ {
			fmt.Fprintf(&chatCanon, `,{"role":"user","content":"question %d %s"}`, i, pad[:min(96, len(pad))])
			fmt.Fprintf(&chatCanon, `,{"role":"assistant","reasoning_content":"thought %d","tool_calls":[`, i)
			for tc := 0; tc < sp.toolCalls; tc++ {
				if tc > 0 {
					_ = chatCanon.WriteByte(',')
				}
				fmt.Fprintf(&chatCanon, `{"id":"call_%d_%d","type":"function","function":{"name":"lookup","arguments":"{\"q\":%d}"}}`, i, tc, tc)
			}
			_, _ = chatCanon.WriteString(`]}`)
			for tc := 0; tc < sp.toolCalls; tc++ {
				fmt.Fprintf(&chatCanon, `,{"role":"tool","content":"result %d %d %s","tool_call_id":"call_%d_%d"}`, i, tc, pad, i, tc)
			}
		}
		_, _ = chatCanon.WriteString(`]}`)

		// Reordered + orphan tool + extra whitespace variant for rewrite path.
		var chatReord strings.Builder
		_, _ = chatReord.WriteString(`{"model":"deepseek-chat","messages":[`)
		fmt.Fprintf(&chatReord, `{"role":"system","content":"system prompt %s"},`, pad[:min(64, len(pad))])
		fmt.Fprintf(&chatReord, `{"role":"tool","tool_call_id":"orphan_0","content":"stale"}`)
		for i := 0; i < sp.turns; i++ {
			fmt.Fprintf(&chatReord, `,{"role":"user","content":"question %d %s"}`, i, pad[:min(96, len(pad))])
			fmt.Fprintf(&chatReord, `,{"role":"assistant","reasoning_content":"thought %d","tool_calls":[{"id":"call_%d_0","type":"function","function":{"name":"lookup","arguments":"{\"q\":1}"}}]}`, i, i)
			fmt.Fprintf(&chatReord, `,{"role":"user","content":"intervening"}`)
			fmt.Fprintf(&chatReord, `,{"role":"tool","tool_call_id":"call_%d_0","content":"result %d %s"}`, i, i, pad)
		}
		_, _ = chatReord.WriteString(`]}`)

		// Unsorted compat seed item matching sample scale (~350B small item + full tools/message item).
		smallItem := json.RawMessage(fmt.Sprintf(
			`{"role":"user","content":[{"text":"hello %d","type":"input_text"}],"metadata":{"z":1,"a":"<tag>&value"}}`,
			sp.turns,
		))
		var itemObj strings.Builder
		_, _ = itemObj.WriteString(`{"role":"assistant","content":"`)
		_, _ = itemObj.WriteString(pad)
		_, _ = itemObj.WriteString(`","tool_calls":[{"type":"function","id":"call_1","function":{"arguments":"{\"b\":2,\"a\":1}","name":"fn"}}],"z_extra":true}`)

		out = append(out, track1CorpusSample{
			name:            sp.name,
			responsesNoop:   []byte(respNoop.String()),
			responsesHit:    []byte(respHit),
			responsesEsc:    []byte(respEsc),
			chatCanonical:   []byte(chatCanon.String()),
			chatReordered:   []byte(chatReord.String()),
			compatSeedSmall: smallItem,
			compatSeedItem:  json.RawMessage(itemObj.String()),
		})
	}
	return out
}

func newTrack1BenchGinContext() *gin.Context {
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", nil)
	return c
}

var (
	track1BenchBytesSink  []byte
	track1BenchStringSink string
	track1BenchKeysSink   []string
	track1BenchBoolSink   bool
)

func BenchmarkTrack1GrokModerationTranscriptKeys(b *testing.B) {
	corpus := buildTrack1Corpus()
	apiKey := &APIKey{ID: 101, UserID: 42}
	for _, sample := range corpus {
		for _, write := range []bool{false, true} {
			mode := "read"
			if write {
				mode = "write"
			}
			b.Run(sample.name+"/"+mode, func(b *testing.B) {
				b.SetBytes(int64(len(sample.responsesNoop)))
				b.ReportAllocs()
				b.ResetTimer()
				for i := 0; i < b.N; i++ {
					track1BenchKeysSink = grokModerationTranscriptKeys(apiKey, sample.responsesNoop, write)
				}
			})
		}
	}
}

func BenchmarkTrack1DeriveOpenAICyberTranscriptBlockKeys(b *testing.B) {
	corpus := buildTrack1Corpus()
	for _, sample := range corpus {
		b.Run(sample.name, func(b *testing.B) {
			b.SetBytes(int64(len(sample.chatCanonical)))
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				res := deriveOpenAICyberTranscriptBlockKeys(77, sample.chatCanonical)
				track1BenchKeysSink = res.lookupKeys
			}
		})
	}
}

func BenchmarkTrack1NormalizeCompatSeedJSON(b *testing.B) {
	corpus := buildTrack1Corpus()
	for _, sample := range corpus {
		b.Run(sample.name+"/uncached_item", func(b *testing.B) {
			b.SetBytes(int64(len(sample.compatSeedItem)))
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				track1BenchStringSink = normalizeCompatSeedJSONUncached(sample.compatSeedItem)
			}
		})
		b.Run(sample.name+"/small_sub512_uncached", func(b *testing.B) {
			b.SetBytes(int64(len(sample.compatSeedSmall)))
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				track1BenchStringSink = normalizeCompatSeedJSONUncached(sample.compatSeedSmall)
			}
		})
		b.Run(sample.name+"/memo_repeat", func(b *testing.B) {
			b.SetBytes(int64(len(sample.compatSeedItem)))
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				track1BenchStringSink = normalizeCompatSeedJSON(sample.compatSeedItem)
			}
		})
	}
}

func BenchmarkTrack1StripDeepSeekReasoningSummarySwitches(b *testing.B) {
	corpus := buildTrack1Corpus()
	oauthAcc := &Account{Platform: PlatformOpenAI, Type: AccountTypeOAuth}
	for _, sample := range corpus {
		b.Run(sample.name+"/noop", func(b *testing.B) {
			b.SetBytes(int64(len(sample.responsesNoop)))
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				track1BenchBytesSink = stripDeepSeekReasoningSummarySwitches(sample.responsesNoop)
			}
		})
		b.Run(sample.name+"/rewrite", func(b *testing.B) {
			b.SetBytes(int64(len(sample.responsesHit)))
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				track1BenchBytesSink = stripDeepSeekReasoningSummarySwitches(sample.responsesHit)
			}
		})
		b.Run(sample.name+"/dup_call_id_check", func(b *testing.B) {
			b.SetBytes(int64(len(sample.responsesNoop)))
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				track1BenchBoolSink = deepSeekDuplicateCallIDRewrite(oauthAcc, sample.responsesNoop)
			}
		})
	}
}

func BenchmarkTrack1NormalizeOpenAIAPIKeyStoreFalseReasoningReplay(b *testing.B) {
	corpus := buildTrack1Corpus()
	for _, sample := range corpus {
		b.Run(sample.name+"/noop_nil_ctx", func(b *testing.B) {
			b.SetBytes(int64(len(sample.responsesNoop)))
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				track1BenchBytesSink, track1BenchBoolSink, _ = normalizeOpenAIAPIKeyStoreFalseReasoningReplay(nil, sample.responsesNoop, false)
			}
		})
		b.Run(sample.name+"/noop_with_ctx", func(b *testing.B) {
			c := newTrack1BenchGinContext()
			b.SetBytes(int64(len(sample.responsesNoop)))
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				c.Keys = nil
				track1BenchBytesSink, track1BenchBoolSink, _ = normalizeOpenAIAPIKeyStoreFalseReasoningReplay(c, sample.responsesNoop, false)
			}
		})
		b.Run(sample.name+"/rewrite", func(b *testing.B) {
			b.SetBytes(int64(len(sample.responsesHit)))
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				track1BenchBytesSink, track1BenchBoolSink, _ = normalizeOpenAIAPIKeyStoreFalseReasoningReplay(nil, sample.responsesHit, false)
			}
		})
	}
}

func BenchmarkTrack1OpenAIEmptyBase64InputImage(b *testing.B) {
	corpus := buildTrack1Corpus()
	for _, sample := range corpus {
		b.Run(sample.name+"/escape_noop_nil_ctx", func(b *testing.B) {
			b.SetBytes(int64(len(sample.responsesEsc)))
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				track1BenchBoolSink = openAIRequestBodyMayContainEmptyBase64InputImage(nil, sample.responsesEsc)
			}
		})
		b.Run(sample.name+"/escape_noop_with_ctx", func(b *testing.B) {
			c := newTrack1BenchGinContext()
			b.SetBytes(int64(len(sample.responsesEsc)))
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				c.Keys = nil
				track1BenchBoolSink = openAIRequestBodyMayContainEmptyBase64InputImage(c, sample.responsesEsc)
			}
		})
		b.Run(sample.name+"/gjson_val_escape_noop", func(b *testing.B) {
			input := gjson.GetBytes(sample.responsesEsc, "input")
			b.SetBytes(int64(len(sample.responsesEsc)))
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				track1BenchBoolSink = openAIJSONValueMayContainEmptyBase64InputImage(input)
			}
		})
	}
}

func BenchmarkTrack1NormalizeDeepSeekChatMessages(b *testing.B) {
	corpus := buildTrack1Corpus()
	for _, sample := range corpus {
		b.Run(sample.name+"/canonical_noop", func(b *testing.B) {
			b.SetBytes(int64(len(sample.chatCanonical)))
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				track1BenchBytesSink = normalizeDeepSeekChatMessages(sample.chatCanonical)
			}
		})
		b.Run(sample.name+"/reordered_rewrite", func(b *testing.B) {
			b.SetBytes(int64(len(sample.chatReordered)))
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				track1BenchBytesSink = normalizeDeepSeekChatMessages(sample.chatReordered)
			}
		})
		b.Run(sample.name+"/cc_pipeline_canonical", func(b *testing.B) {
			b.SetBytes(int64(len(sample.chatCanonical)))
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				track1BenchBytesSink = normalizeAndDropOrphanDeepSeekChatMessages(sample.chatCanonical)
			}
		})
		b.Run(sample.name+"/cc_pipeline_reordered", func(b *testing.B) {
			b.SetBytes(int64(len(sample.chatReordered)))
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				track1BenchBytesSink = normalizeAndDropOrphanDeepSeekChatMessages(sample.chatReordered)
			}
		})
	}
}
