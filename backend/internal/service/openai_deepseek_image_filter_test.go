package service

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestStripDeepSeekUnsupportedImageInputsChatPreservesTextAndUnknownFields(t *testing.T) {
	body := []byte(`{"model":"deepseek-chat","messages":[{"role":"user","content":[{"type":"text","text":"keep"},{"type":"image_url","image_url":{"url":"data:image/png;base64,hidden"}}],"metadata":{"image":"business-value"}}],"seed":9007199254740993}`)

	filtered, removed, changed, err := StripDeepSeekUnsupportedImageInputs(body, "chat_completions")
	require.NoError(t, err)
	require.True(t, changed)
	require.Equal(t, 1, removed)
	require.NotContains(t, string(filtered), "data:image/png")
	require.Contains(t, string(filtered), "business-value")
	require.Contains(t, string(filtered), "9007199254740993")

	var decoded struct {
		Messages []struct {
			Content []struct {
				Text string `json:"text"`
			} `json:"content"`
		} `json:"messages"`
	}
	require.NoError(t, json.Unmarshal(filtered, &decoded))
	require.Len(t, decoded.Messages, 1)
	content := decoded.Messages[0].Content
	require.Len(t, content, 1)
	require.Equal(t, "keep", content[0].Text)
}

func TestStripDeepSeekUnsupportedImageInputsResponsesRemovesTopLevelAndContentImages(t *testing.T) {
	body := []byte(`{"model":"deepseek-v4","input":[{"type":"input_text","text":"keep"},{"type":"input_image","image_url":"https://example.invalid/a.png"},{"type":"message","role":"user","content":[{"type":"input_text","text":"also keep"},{"type":"input_image","image_url":"https://example.invalid/b.png"}]},{"type":"function_call_output","output":[{"type":"input_text","text":"tool text"},{"type":"input_image","image_url":"https://example.invalid/c.png"}]}]}`)

	filtered, removed, changed, err := StripDeepSeekUnsupportedImageInputs(body, "responses")
	require.NoError(t, err)
	require.True(t, changed)
	require.Equal(t, 3, removed)
	require.NotContains(t, string(filtered), "example.invalid")
	require.Contains(t, string(filtered), "also keep")

	var decoded struct {
		Input []struct {
			Content []struct {
				Text string `json:"text"`
			} `json:"content"`
			Output []struct {
				Text string `json:"text"`
			} `json:"output"`
		} `json:"input"`
	}
	require.NoError(t, json.Unmarshal(filtered, &decoded))
	input := decoded.Input
	require.Len(t, input, 3)
	content := input[1].Content
	require.Len(t, content, 1)
	require.Equal(t, "also keep", content[0].Text)
	output := input[2].Output
	require.Len(t, output, 1)
	require.Equal(t, "tool text", output[0].Text)
}

func TestStripDeepSeekUnsupportedImageInputsDoesNotTouchToolArgumentsOrOtherEndpoints(t *testing.T) {
	body := []byte(`{"model":"deepseek-chat","messages":[{"role":"tool","content":"{\"image\":\"keep\"}","tool_calls":[{"function":{"arguments":"{\"image\":\"keep\"}"}}]}]}`)

	filtered, removed, changed, err := StripDeepSeekUnsupportedImageInputs(body, "chat_completions")
	require.NoError(t, err)
	require.False(t, changed)
	require.Zero(t, removed)
	require.Equal(t, string(body), string(filtered))

	filtered, removed, changed, err = StripDeepSeekUnsupportedImageInputs([]byte(`{"input":"plain text"}`), "responses")
	require.NoError(t, err)
	require.False(t, changed)
	require.Zero(t, removed)
	require.JSONEq(t, `{"input":"plain text"}`, string(filtered))
}

func TestStripDeepSeekUnsupportedImageInputsPreservesUnknownResponsesItems(t *testing.T) {
	body := []byte(`{"model":"deepseek-v4","input":[{"type":"custom_item","content":[{"type":"input_image","image_url":"keep-content-image"}],"output":[{"type":"input_image","image_url":"keep-output-image"}],"extra":{"keep":true}}]}`)

	filtered, removed, changed, err := StripDeepSeekUnsupportedImageInputs(body, "responses")
	require.NoError(t, err)
	require.False(t, changed)
	require.Zero(t, removed)
	require.Equal(t, string(body), string(filtered))
}
