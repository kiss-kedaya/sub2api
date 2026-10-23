package handler

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/ipnotice"
	"github.com/Wei-Shaw/sub2api/internal/service"
	coderws "github.com/coder/websocket"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

type noticeE2ESettings struct {
	service.SettingRepository
	raw string
}

func (r *noticeE2ESettings) GetValue(_ context.Context, key string) (string, error) {
	if key == service.SettingKeyGatewayIPNotice {
		return r.raw, nil
	}
	return "", service.ErrSettingNotFound
}

// This goes through the real handler, passthrough relay, local HTTP/WS sockets,
// protocol parsing and billing. The fixture upstream retains conversation IDs.
func TestGatewayIPNoticeRealHandlerThreeTurnConversation(t *testing.T) {
	const notice = "Test operator notice: verify this credential."
	var turns atomic.Int32
	upstreamErrors := make(chan error, 1)
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := coderws.Accept(w, r, nil)
		if err != nil {
			upstreamErrors <- err
			return
		}
		defer func() { _ = conn.CloseNow() }()
		for turn := 1; turn <= 3; turn++ {
			_, body, err := conn.Read(r.Context())
			if err != nil {
				upstreamErrors <- err
				return
			}
			if got := gjson.GetBytes(body, "input").String(); got != notice+"\n\n"+fmt.Sprintf("question %d", turn) {
				upstreamErrors <- fmt.Errorf("wrong upstream input: %q", got)
				return
			}
			if turn > 1 && gjson.GetBytes(body, "previous_response_id").String() != fmt.Sprintf("resp_%d", turn-1) {
				upstreamErrors <- fmt.Errorf("conversation link was lost")
				return
			}
			turns.Add(1)
			id := fmt.Sprintf("resp_%d", turn)
			item := map[string]any{"id": fmt.Sprintf("msg_%d", turn), "type": "message", "role": "assistant", "status": "completed", "content": []any{map[string]any{"type": "output_text", "text": fmt.Sprintf("answer %d", turn), "annotations": []any{}}}}
			frames := []map[string]any{
				{"type": "response.created", "response": map[string]any{"id": id, "model": "gpt-5.1", "status": "in_progress", "output": []any{}}},
				{"type": "response.output_item.added", "output_index": 0, "item": map[string]any{"id": fmt.Sprintf("msg_%d", turn), "type": "message", "role": "assistant", "status": "in_progress", "content": []any{}}},
				{"type": "response.content_part.added", "output_index": 0, "content_index": 0, "item_id": fmt.Sprintf("msg_%d", turn), "part": map[string]any{"type": "output_text", "text": "", "annotations": []any{}}},
				{"type": "response.output_text.delta", "output_index": 0, "content_index": 0, "item_id": fmt.Sprintf("msg_%d", turn), "delta": fmt.Sprintf("answer %d", turn)},
				{"type": "response.output_item.done", "output_index": 0, "item": item},
				{"type": "response.completed", "response": map[string]any{"id": id, "model": "gpt-5.1", "status": "completed", "output": []any{item}, "usage": map[string]any{"input_tokens": 10, "output_tokens": 2}}},
			}
			for seq, frame := range frames {
				frame["sequence_number"] = seq
				encoded, _ := json.Marshal(frame)
				if err = conn.Write(r.Context(), coderws.MessageText, encoded); err != nil {
					upstreamErrors <- err
					return
				}
			}
		}
		upstreamErrors <- nil
		_, _, _ = conn.Read(r.Context())
	}))
	defer upstream.Close()
	raw, err := json.Marshal(ipnotice.Settings{Enabled: true, IPs: []string{"203.0.113.1"}, Message: notice, RPM: 100})
	require.NoError(t, err)
	settings := service.NewSettingService(&noticeE2ESettings{raw: string(raw)}, nil)
	h := newOpenAIWSFirstRefusalHarness(t, service.PlatformOpenAI, upstream.URL, 0, settings)
	ctx, cancel := context.WithTimeout(context.Background(), 12*time.Second)
	defer cancel()
	client, _, err := coderws.Dial(ctx, h.url, &coderws.DialOptions{HTTPHeader: h.headers})
	require.NoError(t, err)
	defer func() { _ = client.CloseNow() }()
	for turn := 1; turn <= 3; turn++ {
		input := map[string]any{"type": "response.create", "model": "gpt-5.1", "input": fmt.Sprintf("question %d", turn)}
		if turn > 1 {
			input["previous_response_id"] = fmt.Sprintf("resp_%d", turn-1)
		}
		payload, err := json.Marshal(input)
		require.NoError(t, err)
		require.NoError(t, client.Write(ctx, coderws.MessageText, payload))
		var text string
		for {
			_, body, err := client.Read(ctx)
			require.NoError(t, err, "turn %d", turn)
			if gjson.GetBytes(body, "type").String() == "response.output_text.delta" {
				text += gjson.GetBytes(body, "delta").String()
			}
			if gjson.GetBytes(body, "type").String() == "response.completed" {
				require.Equal(t, ipnotice.RenderNotice(notice)+fmt.Sprintf("answer %d", turn), text)
				require.Equal(t, ipnotice.RenderNotice(notice), gjson.GetBytes(body, "response.output.0.content.0.text").String())
				require.Equal(t, fmt.Sprintf("answer %d", turn), gjson.GetBytes(body, "response.output.1.content.0.text").String())
				break
			}
		}
	}
	require.Equal(t, int32(3), turns.Load())
	require.NoError(t, <-upstreamErrors)
}
