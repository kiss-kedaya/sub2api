package service

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/ctxkey"
	coderws "github.com/coder/websocket"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

type requestedModelGrokUpstream struct {
	events   chan []byte
	received chan []byte
}

func (u *requestedModelGrokUpstream) ReadMessage(ctx context.Context) ([]byte, error) {
	select {
	case event := <-u.events:
		return event, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

func (u *requestedModelGrokUpstream) WriteJSON(ctx context.Context, value any) error {
	body, err := json.Marshal(value)
	if err != nil {
		return err
	}
	select {
	case u.received <- body:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (u *requestedModelGrokUpstream) Ping(context.Context) error { return nil }
func (u *requestedModelGrokUpstream) Close() error               { return nil }

func TestGrokRealtimeRequestedModelRelayPreservesAudioAndInput(t *testing.T) {
	for _, enabled := range []bool{false, true} {
		t.Run(map[bool]string{false: "disabled", true: "enabled"}[enabled], func(t *testing.T) {
			upstream := &requestedModelGrokUpstream{events: make(chan []byte, 1), received: make(chan []byte, 1)}
			observed := make(chan bool, 1)
			router := gin.New()
			router.GET("/realtime", func(c *gin.Context) {
				c.Request = c.Request.WithContext(context.WithValue(c.Request.Context(), ctxkey.Group, &Group{ReturnRequestedModel: enabled}))
				client, err := coderws.Accept(c.Writer, c.Request, nil)
				if err != nil {
					return
				}
				defer func() { _ = client.CloseNow() }()
				service := &OpenAIGatewayService{}
				audio, _ := service.ProxyGrokRealtimeConn(c.Request.Context(), c, client, &GrokRealtimeUpstream{conn: upstream}, "public-voice")
				observed <- audio
			})
			server := httptest.NewServer(router)
			defer server.Close()
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			client, _, err := coderws.Dial(ctx, "ws"+strings.TrimPrefix(server.URL, "http")+"/realtime", nil)
			require.NoError(t, err)
			defer func() { _ = client.CloseNow() }()
			input := []byte(`{"type":"session.update","session":{"instructions":"keep model text"}}`)
			require.NoError(t, client.Write(ctx, coderws.MessageText, input))
			select {
			case actual := <-upstream.received:
				require.JSONEq(t, string(input), string(actual), "the input direction must remain unchanged")
			case <-ctx.Done():
				t.Fatal(ctx.Err())
			}
			frames := []struct {
				body string
				path string
			}{
				{`{"type":"session.created","session":{"model":"upstream-voice","instructions":"model: upstream-voice"}}`, "session.model"},
				{`{"type":"session.updated","session":{"model":"upstream-voice","instructions":"keep model text"}}`, "session.model"},
				{`{"type":"response.created","response":{"model":"upstream-voice","output":[],"usage":{"input_tokens":2}}}`, "response.model"},
				{`{"type":"response.audio.delta","delta":"YXVkaW8="}`, ""},
				{`{"type":"error","error":{"message":"upstream-voice"},"model":"upstream-voice"}`, ""},
			}
			for _, frame := range frames {
				upstream.events <- []byte(frame.body)
				_, actual, readErr := client.Read(ctx)
				require.NoError(t, readErr)
				if enabled && frame.path != "" {
					require.Equal(t, "public-voice", gjson.GetBytes(actual, frame.path).String())
					original := gjson.Parse(frame.body).Map()
					if session := original["session"]; session.Exists() {
						require.Equal(t, session.Get("instructions").String(), gjson.GetBytes(actual, "session.instructions").String())
					}
					if usage := original["response"].Get("usage"); usage.Exists() {
						require.Equal(t, usage.Raw, gjson.GetBytes(actual, "response.usage").Raw)
					}
				} else {
					require.Equal(t, frame.body, string(actual))
				}
			}
			// Session updates after a completed response must not restore the old
			// turn's requested name over the newly requested session model.
			update := []byte(`{"type":"session.update","session":{"model":"second-voice"}}`)
			require.NoError(t, client.Write(ctx, coderws.MessageText, update))
			select {
			case actual := <-upstream.received:
				require.JSONEq(t, string(update), string(actual))
			case <-ctx.Done():
				t.Fatal(ctx.Err())
			}
			upstream.events <- []byte(`{"type":"session.updated","session":{"model":"provider-second"}}`)
			_, actual, readErr := client.Read(ctx)
			require.NoError(t, readErr)
			wantModel := "provider-second"
			if enabled {
				wantModel = "second-voice"
			}
			require.Equal(t, wantModel, gjson.GetBytes(actual, "session.model").String())
			// Voice activity detection can create the next response automatically,
			// without a client response.create after the session's model changed.
			upstream.events <- []byte(`{"type":"response.created","response":{"id":"auto_second","model":"provider-second"}}`)
			_, actual, readErr = client.Read(ctx)
			require.NoError(t, readErr)
			require.Equal(t, wantModel, gjson.GetBytes(actual, "response.model").String())
			_ = client.CloseNow()
			select {
			case audio := <-observed:
				require.True(t, audio, "metadata rewriting must preserve audio billing observation")
			case <-ctx.Done():
				t.Fatal(ctx.Err())
			}
		})
	}
}
