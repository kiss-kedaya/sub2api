package service

import (
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

func TestRewriteOpenAIResponsesInputTargetsTopLevelDuplicateArray(t *testing.T) {
	// The nested and top-level arrays are byte-identical. The rewrite must use
	// gjson's source offset instead of replacing the first matching byte run.
	body := []byte(`{"shadow":{"input":[{"type":"message","id":"invalid"}]},"input":[{"type":"message","id":"invalid"}],"sequence":9007199254740993}`)

	rewritten, changed, err := sanitizeOpenAIResponsesInputItemIDs(body)

	require.NoError(t, err)
	require.True(t, changed)
	require.Equal(t, "invalid", gjson.GetBytes(rewritten, "shadow.input.0.id").String())
	require.False(t, gjson.GetBytes(rewritten, "input.0.id").Exists())
	require.Equal(t, "9007199254740993", gjson.GetBytes(rewritten, "sequence").Raw)
}

func TestRewriteOpenAIResponsesInputNoopReturnsOriginalSlice(t *testing.T) {
	body := []byte(`{"input":[{"type":"message","id":"msg_valid","content":"unchanged"}]}`)

	rewritten, changed, err := sanitizeOpenAIResponsesInputItemIDs(body)

	require.NoError(t, err)
	require.False(t, changed)
	require.Equal(t, body, rewritten)
	require.Same(t, &body[0], &rewritten[0], "no-op rewrites must not allocate or copy the request body")
}

func TestRewriteOpenAIResponsesInputPreservesSurroundingBytes(t *testing.T) {
	body := []byte(" \n{\n  \"prefix\": {\"opaque\":900719925474099312345},\n  \"input\" : [ {\"type\":\"message\",\"id\":\"invalid\",\"content\":\"\\u4f60\\u597d\"} ],\n  \"suffix\": [true, null, {\"nested\":\"value\"}]\n}\t")
	input := parseRawJSONView(body).Get("input")
	require.True(t, input.IsArray())
	prefix := append([]byte(nil), body[:input.Index]...)
	suffix := append([]byte(nil), body[input.Index+len(input.Raw):]...)

	rewritten, changed, err := sanitizeOpenAIResponsesInputItemIDs(body)

	require.NoError(t, err)
	require.True(t, changed)
	require.Equal(t, prefix, rewritten[:len(prefix)])
	require.Equal(t, suffix, rewritten[len(rewritten)-len(suffix):])
	require.Equal(t, "900719925474099312345", gjson.GetBytes(rewritten, "prefix.opaque").Raw)
	require.Equal(t, "你好", gjson.GetBytes(rewritten, "input.0.content").String())
}

func TestRewriteOpenAIResponsesInputKeepsNestedFieldsInsideItems(t *testing.T) {
	body := []byte(`{"input":[{"type":"message","id":"invalid","namespace":"drop","content":[{"id":"nested","namespace":"keep"}]}]}`)

	withoutID, changed, err := sanitizeOpenAIResponsesInputItemIDs(body)
	require.NoError(t, err)
	require.True(t, changed)
	require.False(t, gjson.GetBytes(withoutID, "input.0.id").Exists())
	require.Equal(t, "nested", gjson.GetBytes(withoutID, "input.0.content.0.id").String())

	withoutNamespace, err := stripOpenAIResponsesInputNamespaces(withoutID, false)
	require.NoError(t, err)
	require.False(t, gjson.GetBytes(withoutNamespace, "input.0.namespace").Exists())
	require.Equal(t, "keep", gjson.GetBytes(withoutNamespace, "input.0.content.0.namespace").String())
}

func TestRewriteOpenAIResponsesInputAppendPreservesRawItems(t *testing.T) {
	body := []byte(`{"input":[{"type":"compaction_trigger","opaque":1},{"type":"message","content":"tail","sequence":9007199254740993},{"type":"compaction_trigger","opaque":2}],"metadata":{"input":[1,2,3]}}`)

	rewritten, changed, err := NormalizeCompactionTriggerInputOrder(body)

	require.NoError(t, err)
	require.True(t, changed)
	require.Equal(t, int64(2), gjson.GetBytes(rewritten, "input.#").Int())
	require.Equal(t, "9007199254740993", gjson.GetBytes(rewritten, "input.0.sequence").Raw)
	require.Equal(t, "compaction_trigger", gjson.GetBytes(rewritten, "input.1.type").String())
	require.Equal(t, int64(3), gjson.GetBytes(rewritten, "metadata.input.#").Int())
}

func TestOpenAIRequestBodyHasToolsPreservesSingleObjectInputCompatibility(t *testing.T) {
	body := []byte(`{"input":{"type":"additional_tools","tools":[{"type":"function","name":"spawn_agent"}]},"parallel_tool_calls":false}`)

	require.True(t, openAIRequestBodyHasTools(body))
	normalized, changed, err := normalizeOpenAIParallelToolCallsWithoutTools(body)
	require.NoError(t, err)
	require.False(t, changed)
	require.Equal(t, body, normalized)
}
