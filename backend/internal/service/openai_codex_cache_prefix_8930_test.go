//go:build unit

package service

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

func TestCodexGPT62CachePrefixPreservesClientGuidance(t *testing.T) {
	for _, model := range []string{"gpt-6.2-sol", "gpt-6.2-astra"} {
		t.Run(model, func(t *testing.T) {
			input := []any{
				map[string]any{"type": "message", "role": "system", "content": "stable first"},
				map[string]any{"type": "message", "role": "user", "content": "turn one"},
				map[string]any{"type": "message", "role": "developer", "content": "later guidance"},
			}
			body := map[string]any{"model": model, "input": input, "instructions": "  just this turn  "}
			require.NoError(t, applyCodexOAuthTransform(body, true, false).Error)
			require.Equal(t, "  just this turn  ", body["instructions"])
			items := body["input"].([]any)
			require.Len(t, items, 3)
			require.Equal(t, "developer", items[0].(map[string]any)["role"])
			require.Equal(t, "stable first", extractTextFromContent(items[0].(map[string]any)["content"]))
			require.Equal(t, "turn one", extractTextFromContent(items[1].(map[string]any)["content"]))
			require.Equal(t, "later guidance", extractTextFromContent(items[2].(map[string]any)["content"]))
		})
	}
}

func TestCodexGPT62CachePrefixDefaultAndContinuation(t *testing.T) {
	const model = "gpt-6.2-sol"
	body := map[string]any{"model": model, "input": "hello"}
	require.NoError(t, applyCodexOAuthTransform(body, true, false).Error)
	require.Empty(t, body["instructions"])
	items := body["input"].([]any)
	require.Len(t, items, 2)
	require.Equal(t, "developer", items[0].(map[string]any)["role"])
	require.Equal(t, defaultCodexSynthInstructions(model), extractTextFromContent(items[0].(map[string]any)["content"]))
	require.NoError(t, applyCodexOAuthTransform(body, true, false).Error)
	require.Len(t, body["input"], 2)

	caller := map[string]any{"model": model, "input": []any{map[string]any{"role": "developer", "content": "caller prefix"}}}
	require.NoError(t, applyCodexOAuthTransform(caller, false, false).Error)
	callerItems := caller["input"].([]any)
	require.Len(t, callerItems, 2)
	require.Equal(t, defaultCodexSynthInstructions(model), extractTextFromContent(callerItems[0].(map[string]any)["content"]))
	require.Equal(t, "caller prefix", extractTextFromContent(callerItems[1].(map[string]any)["content"]))
	require.Empty(t, caller["instructions"])

	continuation := map[string]any{"model": model, "input": "follow up", "previous_response_id": "resp_fixture"}
	require.NoError(t, applyCodexOAuthTransform(continuation, false, false).Error)
	require.Len(t, continuation["input"], 1)
	require.Equal(t, "resp_fixture", continuation["previous_response_id"])
}

func TestCodexGPT62CachePrefixKeepsLegacyAndTemplateTurnSemantics(t *testing.T) {
	legacy := map[string]any{"model": "gpt-6.1-sol", "input": "hello"}
	require.NoError(t, applyCodexOAuthTransform(legacy, true, false).Error)
	require.Equal(t, defaultCodexSynthInstructions("gpt-6.1-sol"), legacy["instructions"])

	stable := map[string]any{"model": "gpt-6.2-astra", "input": []any{map[string]any{"role": "user", "content": "hello"}}}
	changed, err := applyForcedCodexInstructionsTemplate(stable, "stable server guidance", forcedCodexInstructionsTemplateData{})
	require.NoError(t, err)
	require.True(t, changed)
	require.Empty(t, stable["instructions"])
	require.Equal(t, "stable server guidance", extractTextFromContent(stable["input"].([]any)[0].(map[string]any)["content"]))
	turn := map[string]any{"model": "gpt-6.2-astra", "instructions": "only this turn", "input": []any{}}
	_, err = applyForcedCodexInstructionsTemplate(turn, "template: {{ .ExistingInstructions }}", forcedCodexInstructionsTemplateData{ExistingInstructions: "only this turn"})
	require.NoError(t, err)
	require.True(t, strings.HasSuffix(turn["instructions"].(string), "only this turn"))
	require.Empty(t, turn["input"])
}

func TestForwardCodexGPT62CachePrefixOnWire(t *testing.T) {
	cases := []struct {
		name, body, instructions, firstDeveloper string
		passthrough, fieldExists                 bool
	}{
		{"provided prefix", "{\"model\":\"gpt-6.2-astra\",\"input\":[{\"type\":\"message\",\"role\":\"developer\",\"content\":[{\"type\":\"input_text\",\"text\":\"stable prefix\"}]},{\"type\":\"message\",\"role\":\"user\",\"content\":[{\"type\":\"input_text\",\"text\":\"hi\"}]}],\"stream\":true}", "", defaultCodexSynthInstructions("gpt-6.2-astra"), false, true},
		{"default prefix", "{\"model\":\"gpt-6.2-astra\",\"input\":\"hi\",\"stream\":true}", "", defaultCodexSynthInstructions("gpt-6.2-astra"), false, true},
		{"turn-scoped instructions", "{\"model\":\"gpt-6.2-astra\",\"input\":[{\"type\":\"message\",\"role\":\"system\",\"content\":[{\"type\":\"input_text\",\"text\":\"client system\"}]},{\"type\":\"message\",\"role\":\"user\",\"content\":[{\"type\":\"input_text\",\"text\":\"hi\"}]}],\"instructions\":\"this turn only\",\"stream\":true}", "this turn only", "client system", false, true},
		{"passthrough keeps instructions absent", "{\"model\":\"gpt-6.2-astra\",\"input\":[{\"type\":\"message\",\"role\":\"developer\",\"content\":[{\"type\":\"input_text\",\"text\":\"stable prefix\"}]},{\"type\":\"message\",\"role\":\"user\",\"content\":[{\"type\":\"input_text\",\"text\":\"hi\"}]}],\"stream\":true}", "", "stable prefix", true, false},
	}
	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			s := newAstraOAuthSetup(t, tt.passthrough)
			s.upstream.resp = &http.Response{
				StatusCode: http.StatusOK,
				Header:     http.Header{"Content-Type": []string{"text/event-stream"}},
				Body:       io.NopCloser(strings.NewReader(codexCompletedSSE("{\"id\":\"resp_8930\",\"model\":\"gpt-6.2-astra\",\"output\":[],\"usage\":{\"input_tokens\":1,\"output_tokens\":1}}"))),
			}
			_, err := s.svc.Forward(context.Background(), s.c, s.account, []byte(tt.body))
			require.NoError(t, err)
			sent := gjson.ParseBytes(s.upstream.lastBody)
			require.Equal(t, "gpt-6.2-astra", sent.Get("model").String())
			require.Equal(t, tt.fieldExists, sent.Get("instructions").Exists())
			require.Equal(t, tt.instructions, sent.Get("instructions").String())
			items := sent.Get("input").Array()
			if tt.name == "provided prefix" {
				require.Len(t, items, 3)
				require.Equal(t, "stable prefix", extractTextFromContent(items[1].Get("content").Value()))
			} else {
				require.Len(t, items, 2)
			}
			require.Equal(t, "developer", items[0].Get("role").String())
			require.Equal(t, tt.firstDeveloper, extractTextFromContent(items[0].Get("content").Value()))
			require.Equal(t, "user", items[len(items)-1].Get("role").String())
		})
	}
}
