//go:build unit

package service

import (
	"context"
	"encoding/json"
	"io"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
	"github.com/tidwall/sjson"
)

// Native SSE may rename item.id in its final snapshot, but an existing output
// slot must never change its tool owner, item type or call_id. Mutate the wire
// after the live owned declaration so Forward, not just an identity helper,
// proves that no model-provided replacement can trigger a paid page fetch.
func TestDeepSeekHostedFetchRejectsOwnedSlotIdentityChangesBeforeExecution(t *testing.T) {
	tests := []struct {
		name        string
		phase       string
		replacement string
		initialName string
	}{
		{name: "fetch done becomes client function", phase: "done", replacement: "client", initialName: deepSeekHostedFetchToolName},
		{name: "fetch done becomes message", phase: "done", replacement: "message", initialName: deepSeekHostedFetchToolName},
		{name: "fetch done loses item", phase: "done", replacement: "missing", initialName: deepSeekHostedFetchToolName},
		{name: "fetch done changes call id", phase: "done", replacement: "call_id", initialName: deepSeekHostedFetchToolName},
		{name: "fetch terminal becomes client function", phase: "terminal", replacement: "client", initialName: deepSeekHostedFetchToolName},
		{name: "fetch terminal becomes message", phase: "terminal", replacement: "message", initialName: deepSeekHostedFetchToolName},
		{name: "fetch terminal loses owned slot", phase: "terminal", replacement: "missing", initialName: deepSeekHostedFetchToolName},
		{name: "fetch terminal changes call id", phase: "terminal", replacement: "call_id", initialName: deepSeekHostedFetchToolName},
		{name: "search done becomes fetch", phase: "done", replacement: "fetch", initialName: deepSeekHostedSearchToolName},
		{name: "search terminal becomes fetch", phase: "terminal", replacement: "fetch", initialName: deepSeekHostedSearchToolName},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			call := hostedSearchCall()
			call["name"] = tt.initialName
			if tt.initialName == deepSeekHostedFetchToolName {
				call["arguments"] = `{"url":"https://example.com/public-page"}`
			}
			mutated := false
			upstream := &hostedSearchUpstream{
				modelResponses: [][]byte{hostedSearchResponse("resp_identity_swap", 10, 2, call)},
				searchResponse: `{"results":[{"title":"must not be searched","url":"https://example.com/"}]}`,
				fetchResponse:  `{"title":"must not be fetched","content":"No paid fetch is permitted."}`,
				modelBody: func(_ int, response string) io.ReadCloser {
					lines := strings.Split(deepSeekHostedIdentityLiveIDs(response), "\n")
					for i, line := range lines {
						if !strings.HasPrefix(line, "data: ") {
							continue
						}
						raw := []byte(strings.TrimPrefix(line, "data: "))
						kind := gjson.GetBytes(raw, "type").String()
						if tt.phase == "done" && kind != "response.output_item.done" ||
							tt.phase == "terminal" && kind != "response.completed" {
							continue
						}
						path := "item"
						if tt.phase == "terminal" {
							path = "response.output.0"
						}
						var err error
						switch tt.replacement {
						case "client":
							raw, err = sjson.SetBytes(raw, path+".name", "client_owned_identity_swap")
						case "message":
							raw, err = sjson.SetBytes(raw, path, map[string]any{
								"type": "message", "id": "msg_identity_swap", "role": "assistant", "status": "completed",
								"content": []any{map[string]string{"type": "output_text", "text": "identity-swap-must-not-leak"}},
							})
						case "missing":
							if tt.phase == "terminal" {
								raw, err = sjson.SetBytes(raw, "response.output", []any{})
							} else {
								raw, err = sjson.DeleteBytes(raw, "item")
							}
						case "call_id":
							raw, err = sjson.SetBytes(raw, path+".call_id", "call_identity_swap")
						case "fetch":
							raw, err = sjson.SetBytes(raw, path+".name", deepSeekHostedFetchToolName)
						}
						require.NoError(t, err)
						mutated = true
						lines[i] = "data: " + string(raw)
					}
					return io.NopCloser(strings.NewReader(strings.Join(lines, "\n")))
				},
			}
			svc, c, recorder, account, body := hostedSearchFixture(t, upstream, true, "auto", false)
			result, err := svc.Forward(context.Background(), c, account, body)
			require.True(t, mutated, "the fixture must exercise the selected invalid native event")
			require.Error(t, err)
			require.Len(t, upstream.modelRequests, 1, "an inconsistent tool turn must not start a model continuation")
			require.Empty(t, upstream.searchRequests)
			require.Empty(t, upstream.fetchRequests, "ownership must be checked before any paid provider request")
			if result != nil {
				require.Zero(t, result.SearchCount)
				require.Zero(t, result.WebSearchCalls)
				require.False(t, result.ClientDisconnect, "a rejected provider event is not user cancellation")
			}
			wire := recorder.Body.String()
			require.True(t, IsResponseCommitted(c))
			require.Equal(t, 1, strings.Count(wire, "event: response.failed\n"))
			require.NotContains(t, wire, "event: response.completed\n")
			require.NotContains(t, wire, `"name":"`+deepSeekHostedSearchToolName+`"`)
			require.NotContains(t, wire, `"name":"`+deepSeekHostedFetchToolName+`"`)
			require.NotContains(t, wire, "client_owned_identity_swap")
			require.NotContains(t, wire, "identity-swap-must-not-leak", "invalid terminal snapshots cannot replace the already advertised owned output")
			sequence := int64(0)
			var ownedID string
			for _, line := range strings.Split(wire, "\n") {
				if !strings.HasPrefix(line, "data: ") {
					continue
				}
				raw := strings.TrimPrefix(line, "data: ")
				require.True(t, json.Valid([]byte(raw)))
				event := gjson.Parse(raw)
				require.Equal(t, sequence, event.Get("sequence_number").Int())
				sequence++
				if event.Get("type").String() == "response.output_item.added" {
					require.Empty(t, ownedID)
					ownedID = event.Get("item.id").String()
					require.Equal(t, "web_search_call", event.Get("item.type").String())
				}
			}
			require.NotEmpty(t, ownedID)
		})
	}
}
