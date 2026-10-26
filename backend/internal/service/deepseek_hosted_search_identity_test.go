//go:build unit

package service

import (
	"context"
	"io"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
	"github.com/tidwall/sjson"
)

// The real native provider keeps call_id and output_index stable, but assigns
// different item ids to the final response snapshot, including reasoning items.
func deepSeekHostedIdentityLiveIDs(response string) string {
	lines := strings.Split(response, "\n")
	for i, line := range lines {
		if !strings.HasPrefix(line, "data: ") {
			continue
		}
		raw := []byte(strings.TrimPrefix(line, "data: "))
		if gjson.GetBytes(raw, "type").String() == "response.completed" {
			continue
		}
		for _, path := range []string{"item.id", "item_id"} {
			if id := gjson.GetBytes(raw, path).String(); id != "" && !strings.HasPrefix(id, "msg_") {
				raw, _ = sjson.SetBytes(raw, path, "live_"+id)
			}
		}
		lines[i] = "data: " + string(raw)
	}
	return strings.Join(lines, "\n")
}

func TestDeepSeekHostedSearchNativeTerminalIdentityAliases(t *testing.T) {
	first := hostedSearchCall()
	first["id"], first["call_id"] = "fc_first_snapshot", "call_first"
	second := hostedSearchCall()
	second["id"], second["call_id"] = "fc_second_snapshot", "call_second"
	second["arguments"] = `{"query":"DeepSeek tools search examples","count":1}`
	firstMessage := hostedSearchAnswer()
	firstMessage["id"] = "msg_first"
	upstream := &hostedSearchUpstream{modelResponses: [][]byte{
		hostedSearchResponse("resp_search", 10, 2,
			map[string]any{"type": "reasoning", "id": "rs_snapshot", "status": "completed", "summary": []any{}, "encrypted_content": "opaque-native-reasoning"},
			firstMessage, first, second),
		hostedSearchResponse("resp_summary", 20, 3, hostedSearchAnswer()),
	}, searchResponse: `{"results":[{"title":"DeepSeek docs","url":"https://example.com/search","summary":"Structured web results"}]}`,
		modelBody: func(_ int, response string) io.ReadCloser {
			return io.NopCloser(strings.NewReader(deepSeekHostedIdentityLiveIDs(response)))
		},
	}
	svc, c, recorder, account, body := hostedSearchFixture(t, upstream, true, "auto", false)
	result, err := svc.Forward(context.Background(), c, account, body)
	require.NoError(t, err, recorder.Body.String())
	require.NotNil(t, result)
	require.Len(t, upstream.modelRequests, 2)
	require.Len(t, upstream.searchRequests, 2, "snapshot item-id aliases must not execute a search twice")
	require.Equal(t, 2, result.SearchCount)
	require.Zero(t, result.WebSearchCalls)
	final := hostedSearchFinal(t, recorder, true)
	require.Len(t, final.Get("output").Array(), 5)
	require.Equal(t, "live_rs_snapshot", final.Get("output.0.id").String(), "the downstream final snapshot must retain its live reasoning id")
	require.Equal(t, "opaque-native-reasoning", final.Get("output.0.encrypted_content").String())
	require.Equal(t, "web_search_call", final.Get("output.2.type").String())
	require.Equal(t, "web_search_call", final.Get("output.3.type").String())
	require.NotEqual(t, final.Get("output.2.id").String(), final.Get("output.3.id").String())
	require.Contains(t, string(upstream.modelRequests[1]), `"id":"rs_snapshot"`, "the upstream continuation must keep the provider's raw terminal identity")
	require.NotContains(t, string(upstream.modelRequests[1]), "live_rs_snapshot")
	require.NotContains(t, recorder.Body.String(), deepSeekHostedSearchToolName)
}

func TestDeepSeekHostedSearchRejectsRepeatedCallIDInDifferentSlots(t *testing.T) {
	first, second := hostedSearchCall(), hostedSearchCall()
	first["id"], second["id"] = "fc_first_snapshot", "fc_second_snapshot"
	upstream := &hostedSearchUpstream{modelResponses: [][]byte{hostedSearchResponse("resp_duplicate", 10, 2, first, second)},
		searchResponse: `{"results":[{"title":"must not be called","url":"https://example.com/"}]}`,
		modelBody: func(_ int, response string) io.ReadCloser {
			return io.NopCloser(strings.NewReader(deepSeekHostedIdentityLiveIDs(response)))
		},
	}
	svc, c, recorder, account, body := hostedSearchFixture(t, upstream, true, "auto", false)
	_, err := svc.Forward(context.Background(), c, account, body)
	require.Error(t, err)
	require.Len(t, upstream.modelRequests, 1)
	require.Empty(t, upstream.searchRequests, "a genuinely repeated call_id must be rejected before any paid search")
	require.NotContains(t, recorder.Body.String(), "response.completed")
}
