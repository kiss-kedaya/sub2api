package apicompat

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestLiftResponsesToolOutputMedia(t *testing.T) {
	var input any
	require.NoError(t, json.Unmarshal([]byte(`[
		{"type":"function_call","call_id":"call_image","name":"view_image","arguments":"{}"},
		{"type":"function_call_output","call_id":"call_image","output":[{"type":"input_image","image_url":"data:image/png;base64,AQID"}]}
	]`), &input))

	lifted, changed := LiftResponsesToolOutputMedia(input)
	require.True(t, changed)

	items, ok := lifted.([]any)
	require.True(t, ok)
	require.Len(t, items, 3)

	outputItem, ok := items[1].(map[string]any)
	require.True(t, ok)
	outputText, ok := outputItem["output"].(string)
	require.True(t, ok)
	require.Contains(t, outputText, toolOutputMediaMarker)
	require.NotContains(t, outputText, "data:image/png")

	mediaMessage, ok := items[2].(map[string]any)
	require.True(t, ok)
	require.Equal(t, "message", mediaMessage["type"])
	require.Equal(t, "user", mediaMessage["role"])

	parts, ok := mediaMessage["content"].([]map[string]any)
	require.True(t, ok)
	require.Len(t, parts, 2)
	require.Equal(t, "input_text", parts[0]["type"])
	require.Equal(t, "[Tool output media for call call_image]", parts[0]["text"])
	require.Equal(t, "input_image", parts[1]["type"])
	require.Equal(t, "data:image/png;base64,AQID", parts[1]["image_url"])
}

func TestLiftResponsesToolOutputMediaLeavesPlainOutputUntouched(t *testing.T) {
	input := []any{
		map[string]any{
			"type":    "function_call_output",
			"call_id": "call_text",
			"output":  "plain output",
		},
	}

	lifted, changed := LiftResponsesToolOutputMedia(input)
	require.False(t, changed)
	require.Equal(t, input, lifted)
}
