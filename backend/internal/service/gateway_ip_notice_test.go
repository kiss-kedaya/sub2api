package service

import (
	"context"
	"fmt"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	coremiddleware "github.com/Wei-Shaw/sub2api/internal/middleware"
	"github.com/Wei-Shaw/sub2api/internal/pkg/ipnotice"
	"github.com/alicebob/miniredis/v2"
	coderws "github.com/coder/websocket"
	"github.com/gin-gonic/gin"
	"github.com/redis/go-redis/v9" //nolint:depguard // Exercise the shared Redis IP counter across actual WebSocket turns.
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

func TestGatewayIPNoticeWebSocketEveryTurnAndRateLimit(t *testing.T) {
	redisServer := miniredis.RunT(t)
	redisClient := redis.NewClient(&redis.Options{Addr: redisServer.Addr()})
	t.Cleanup(func() { require.NoError(t, redisClient.Close()) })
	session := &GatewayIPNoticeSession{IP: "203.0.113.1", Message: "operator notice", RPM: 100, Limiter: coremiddleware.NewRateLimiter(redisClient)}
	var upstreamTurns atomic.Int32
	done := make(chan error, 1)
	router := gin.New()
	router.GET("/responses", func(c *gin.Context) {
		result, err := session.Allow(c.Request.Context())
		if err != nil || !result.Allowed {
			c.Status(400)
			return
		}
		SetGatewayIPNoticeSession(c, session)
		raw, err := coderws.Accept(c.Writer, c.Request, nil)
		if err != nil {
			done <- err
			return
		}
		conn := WrapGatewayIPNoticeWebSocket(c, raw)
		defer func() { _ = conn.CloseNow() }()
		for {
			kind, body, err := conn.Read(c.Request.Context())
			if err != nil {
				done <- err
				return
			}
			if !strings.Contains(gjson.GetBytes(body, "input").String(), session.Message) {
				done <- fmt.Errorf("missing input notice")
				return
			}
			turn := upstreamTurns.Add(1)
			for _, frame := range []string{
				fmt.Sprintf(`{"type":"response.created","sequence_number":0,"response":{"id":"resp%d"}}`, turn),
				`{"type":"response.completed","sequence_number":1,"response":{"output":[],"usage":{"output_tokens":1}}}`,
			} {
				if err := conn.Write(c.Request.Context(), kind, []byte(frame)); err != nil {
					done <- err
					return
				}
			}
		}
	})
	server := httptest.NewServer(router)
	defer server.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	client, _, err := coderws.Dial(ctx, "ws"+strings.TrimPrefix(server.URL, "http")+"/responses", nil)
	require.NoError(t, err)
	defer func() { _ = client.CloseNow() }()
	for turn := range 101 {
		payload := []byte(`{"type":"response.create","model":"fixture","input":"user input"}`)
		if turn%2 == 1 {
			payload = []byte(`{"model":"fixture","input":"user input"}`)
		}
		require.NoError(t, client.Write(ctx, coderws.MessageText, payload))
		visible := 0
		for {
			_, frame, err := client.Read(ctx)
			require.NoError(t, err, "turn %d", turn)
			kind := gjson.GetBytes(frame, "type").String()
			if turn == 100 {
				require.Equal(t, "error", kind)
				require.Equal(t, int64(400), gjson.GetBytes(frame, "error.status_code").Int())
				require.Equal(t, session.Message, gjson.GetBytes(frame, "error.message").String())
				break
			}
			if kind == "response.output_text.delta" {
				visible++
				require.Equal(t, ipnotice.RenderNotice(session.Message), gjson.GetBytes(frame, "delta").String())
			}
			if kind == "response.completed" {
				require.Equal(t, 1, visible)
				require.Equal(t, ipnotice.RenderNotice(session.Message), gjson.GetBytes(frame, "response.output.0.content.0.text").String())
				break
			}
		}
	}
	require.Equal(t, int32(100), upstreamTurns.Load())
	select {
	case <-done:
	case <-ctx.Done():
		t.Fatal("server reader did not exit")
	}
}

func TestGatewayIPNoticeOrdinaryWebSocketIsNotDecorated(t *testing.T) {
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	var original *coderws.Conn
	require.Equal(t, GatewayClientWebSocket(original), WrapGatewayIPNoticeWebSocket(c, original))
}
