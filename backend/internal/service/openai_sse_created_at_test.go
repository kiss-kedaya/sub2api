package service

import (
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

// 下游若是官方版，created_at 是裸 int64。上游把 1790892388.0 原样透传过去，
// 下游整帧反序列化失败、用量记成 0。透传必须在写出前收成整数。
func TestNormalizeFractionalCreatedAtForClient(t *testing.T) {
	t.Run("integer stays untouched", func(t *testing.T) {
		in := []byte(`{"type":"response.completed","response":{"created_at":1700000123,"usage":{"input_tokens":5}}}`)
		out, changed := normalizeFractionalCreatedAtForClient(in)
		require.False(t, changed)
		require.Equal(t, in, out)
	})

	t.Run("fractional response.created_at becomes integer", func(t *testing.T) {
		in := []byte(`{"type":"response.completed","response":{"id":"resp_9","created_at":1790892388.0,"usage":{"input_tokens":5,"output_tokens":10}}}`)
		out, changed := normalizeFractionalCreatedAtForClient(in)
		require.True(t, changed)
		require.Equal(t, "1790892388", gjson.GetBytes(out, "response.created_at").Raw)
		require.Equal(t, int64(5), gjson.GetBytes(out, "response.usage.input_tokens").Int(), "归一不得改动 usage")
	})

	t.Run("top level fractional created_at", func(t *testing.T) {
		in := []byte(`{"id":"resp_9","created_at":1790892388.5,"usage":{"input_tokens":3}}`)
		out, changed := normalizeFractionalCreatedAtForClient(in)
		require.True(t, changed)
		require.Equal(t, "1790892388", gjson.GetBytes(out, "created_at").Raw)
	})

	t.Run("frame without created_at is untouched", func(t *testing.T) {
		in := []byte(`{"type":"response.output_text.delta","delta":"hi"}`)
		out, changed := normalizeFractionalCreatedAtForClient(in)
		require.False(t, changed)
		require.Equal(t, in, out)
	})
}
