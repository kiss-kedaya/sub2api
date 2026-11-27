//go:build unit

package service

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/pkg/ctxkey"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestUpstreamResponseModelObserver_ObserveAnthropicMessageID(t *testing.T) {
	t.Run("extracts msg id from non-streaming Messages JSON", func(t *testing.T) {
		obs := &upstreamResponseModelObserver{}
		body := []byte(`{
			"model": "claude-opus-5-5",
			"id": "msg_011ChSwsWaLgTDX3Ay9jabZw",
			"type": "message",
			"role": "assistant",
			"content": [{"type": "text", "text": "12 x 14 = 168."}]
		}`)
		obs.ObserveAnthropic(body)
		require.Equal(t, "claude-opus-5-5", obs.Model())
		require.Equal(t, "msg_011ChSwsWaLgTDX3Ay9jabZw", obs.MessageID())
	})

	t.Run("extracts msg id from streaming message_start and ignores delta frames", func(t *testing.T) {
		obs := &upstreamResponseModelObserver{}
		obs.ObserveAnthropic([]byte(`{"type":"message_start","message":{"id":"msg_01StreamStart123","type":"message","role":"assistant","model":"claude-sonnet-4-5"}}`))
		// Delta frame has no model: should be ignored without clearing message ID.
		obs.ObserveAnthropic([]byte(`{"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"hi"}}`))
		require.Equal(t, "claude-sonnet-4-5", obs.Model())
		require.Equal(t, "msg_01StreamStart123", obs.MessageID())
	})
}

func TestResolveUsageResponseIDs(t *testing.T) {
	t.Run("always saves X-Request-Id and X-Client-Request-Id regardless of group toggle", func(t *testing.T) {
		ctx := context.WithValue(context.Background(), ctxkey.ResponseRequestID, "4276cee5-69e9-497a-9318-f99c84625bac")
		ctx = context.WithValue(ctx, ctxkey.ClientRequestID, "564966d3-8995-44d3-bc78-b5730b19a922")
		apiKey := &APIKey{Group: &Group{ID: 1, Platform: PlatformAnthropic, SaveResponseMsgID: false}}

		respID := resolveUsageResponseRequestID(ctx, "fallback-req")
		clientID := resolveUsageClientRequestID(ctx)
		msgID := resolveUsageResponseMsgID(ctx, apiKey, "msg_011ChSwsWaLgTDX3Ay9jabZw")

		require.NotNil(t, respID)
		require.Equal(t, "4276cee5-69e9-497a-9318-f99c84625bac", *respID)
		require.NotNil(t, clientID)
		require.Equal(t, "564966d3-8995-44d3-bc78-b5730b19a922", *clientID)
		require.Nil(t, msgID, "msg id must not be saved when group toggle is disabled")
	})

	t.Run("saves response_msg_id when Anthropic group toggle is enabled", func(t *testing.T) {
		apiKey := &APIKey{Group: &Group{ID: 2, Platform: PlatformAnthropic, SaveResponseMsgID: true}}
		msgID := resolveUsageResponseMsgID(context.Background(), apiKey, "msg_011ChSwsWaLgTDX3Ay9jabZw")
		require.NotNil(t, msgID)
		require.Equal(t, "msg_011ChSwsWaLgTDX3Ay9jabZw", *msgID)
	})

	t.Run("never saves response_msg_id for non-Anthropic group", func(t *testing.T) {
		apiKey := &APIKey{Group: &Group{ID: 3, Platform: PlatformOpenAI, SaveResponseMsgID: true}}
		msgID := resolveUsageResponseMsgID(context.Background(), apiKey, "msg_011ChSwsWaLgTDX3Ay9jabZw")
		require.Nil(t, msgID)
	})

	t.Run("respects ctxkey.SaveResponseMsgID override for detached worker context", func(t *testing.T) {
		apiKey := &APIKey{Group: &Group{ID: 4, Platform: PlatformAnthropic, SaveResponseMsgID: false}}
		enabledCtx := context.WithValue(context.Background(), ctxkey.SaveResponseMsgID, true)
		msgID := resolveUsageResponseMsgID(enabledCtx, apiKey, "msg_smart_routed")
		require.NotNil(t, msgID)
		require.Equal(t, "msg_smart_routed", *msgID)

		disabledCtx := context.WithValue(context.Background(), ctxkey.SaveResponseMsgID, false)
		apiKeyOn := &APIKey{Group: &Group{ID: 5, Platform: PlatformAnthropic, SaveResponseMsgID: true}}
		require.Nil(t, resolveUsageResponseMsgID(disabledCtx, apiKeyOn, "msg_smart_routed"))
	})

	t.Run("falls back to ctxkey.RequestID when ResponseRequestID and result request id are absent", func(t *testing.T) {
		ctx := context.WithValue(context.Background(), ctxkey.RequestID, "local-req-uuid")
		respID := resolveUsageResponseRequestID(ctx, "web_search:forced-id")
		require.NotNil(t, respID)
		require.Equal(t, "local-req-uuid", *respID)
	})

	t.Run("truncates overly long trace ids safely to 128 bytes", func(t *testing.T) {
		longID := strings.Repeat("a", 200)
		normalized := normalizeUsageTraceID(longID)
		require.Len(t, normalized, 128)
	})

	t.Run("direct RecordUsage reads live OpsGinContext X-Request-Id and blocks raw fallback when stripped", func(t *testing.T) {
		gin.SetMode(gin.TestMode)
		rec := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(rec)
		c.Request = httptest.NewRequest(http.MethodPost, "/v1/videos", nil)
		c.Header("X-Request-Id", "live-response-header-id")
		ctx := WithOpsGinContext(context.Background(), c)

		respID := resolveUsageResponseRequestID(ctx, "grok-video:forced-task")
		require.NotNil(t, respID)
		require.Equal(t, "live-response-header-id", *respID)

		// When force_remove leaves no X-Request-Id on the response writer, do not leak resultRequestID.
		recStripped := httptest.NewRecorder()
		cStripped, _ := gin.CreateTestContext(recStripped)
		cStripped.Request = httptest.NewRequest(http.MethodPost, "/v1/images/generations", nil)
		strippedCtx := WithOpsGinContext(context.Background(), cStripped)
		require.Nil(t, resolveUsageResponseRequestID(strippedCtx, "secret-upstream-id"))

		// Explicit empty ctxkey.ResponseRequestID from detached worker also suppresses fallback.
		emptyDetached := context.WithValue(context.Background(), ctxkey.ResponseRequestID, "")
		require.Nil(t, resolveUsageResponseRequestID(emptyDetached, "secret-upstream-id"))
	})
}
