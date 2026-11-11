package service

import (
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

func mshHeaders(fiveMinute, oneHour string) http.Header {
	h := http.Header{}
	if fiveMinute != "" {
		h.Set(mshCacheWrite5mHeader, fiveMinute)
	}
	if oneHour != "" {
		h.Set(mshCacheWrite1hHeader, oneHour)
	}
	return h
}

func TestParseMSHCacheWriteBuckets(t *testing.T) {
	for name, tc := range map[string]struct {
		header http.Header
		five   int
		one    int
		ok     bool
	}{
		"both":         {mshHeaders("9984", "0"), 9984, 0, true},
		"only 5m":      {mshHeaders("1200", ""), 1200, 0, true},
		"only 1h":      {mshHeaders("", "800"), 0, 800, true},
		"neither":      {http.Header{}, 0, 0, false},
		"nil":          {nil, 0, 0, false},
		"not a number": {mshHeaders("many", "0"), 0, 0, false},
		"negative":     {mshHeaders("-1", "0"), 0, 0, false},
		"spaces":       {mshHeaders(" 42 ", " 0 "), 42, 0, true},
	} {
		t.Run(name, func(t *testing.T) {
			five, one, ok := ParseMSHCacheWriteBuckets(tc.header)
			require.Equal(t, tc.ok, ok)
			if tc.ok {
				require.Equal(t, tc.five, five)
				require.Equal(t, tc.one, one)
			}
		})
	}
}

func TestInjectChatCompletionCacheCreationAddsBothBuckets(t *testing.T) {
	body := []byte(`{"id":"chatcmpl-1","usage":{"prompt_tokens":10194,"completion_tokens":16,"total_tokens":10210,"prompt_tokens_details":{"cache_write_tokens":9984}}}`)

	got := InjectChatCompletionCacheCreation(body, mshHeaders("9984", "0"))

	require.Equal(t, float64(9984), gjson.GetBytes(got, "usage.cache_creation.ephemeral_5m_input_tokens").Float())
	require.Equal(t, float64(0), gjson.GetBytes(got, "usage.cache_creation.ephemeral_1h_input_tokens").Float())
	// Both keys are always present, so a reader never treats a missing key as zero.
	require.True(t, gjson.GetBytes(got, "usage.cache_creation.ephemeral_1h_input_tokens").Exists())
	// Nothing else in the body changes.
	require.Equal(t, float64(9984), gjson.GetBytes(got, "usage.prompt_tokens_details.cache_write_tokens").Float())
	require.Equal(t, "chatcmpl-1", gjson.GetBytes(got, "id").String())
}

func TestInjectChatCompletionCacheCreationLeavesBodiesItShouldNotTouch(t *testing.T) {
	withBuckets := []byte(`{"usage":{"prompt_tokens":10,"cache_creation":{"ephemeral_5m_input_tokens":5,"ephemeral_1h_input_tokens":5}}}`)
	noUsage := []byte(`{"id":"chatcmpl-1","choices":[]}`)
	notJSON := []byte(`<html>upstream error page</html>`)
	usageNotObject := []byte(`{"usage":null}`)

	for name, tc := range map[string]struct {
		body   []byte
		header http.Header
	}{
		"upstream already wrote the split": {withBuckets, mshHeaders("9984", "0")},
		"no buckets in the headers":        {[]byte(`{"usage":{"prompt_tokens":10}}`), http.Header{}},
		"no usage object":                  {noUsage, mshHeaders("1", "2")},
		"not json":                         {notJSON, mshHeaders("1", "2")},
		"usage is null":                    {usageNotObject, mshHeaders("1", "2")},
		"empty body":                       {nil, mshHeaders("1", "2")},
	} {
		t.Run(name, func(t *testing.T) {
			require.Equal(t, tc.body, InjectChatCompletionCacheCreation(tc.body, tc.header))
		})
	}
}

func TestInjectChatCompletionCacheCreationIntoSSELine(t *testing.T) {
	t.Run("usage chunk gets the split and the prefix is preserved", func(t *testing.T) {
		line := `data: {"id":"chatcmpl-1","choices":[],"usage":{"prompt_tokens":10194,"prompt_tokens_details":{"cache_write_tokens":9984}}}`

		got := InjectChatCompletionCacheCreationIntoSSELine(line, 9984, 0)

		require.Contains(t, got, "data: {")
		jsonPayload := got[len("data: "):]
		require.True(t, gjson.Valid(jsonPayload))
		require.Equal(t, float64(9984), gjson.Get(jsonPayload, "usage.cache_creation.ephemeral_5m_input_tokens").Float())
		require.True(t, gjson.Get(jsonPayload, "usage.cache_creation.ephemeral_1h_input_tokens").Exists())
	})

	t.Run("lines it must not touch", func(t *testing.T) {
		for _, line := range []string{
			``,
			`event: message`,
			`data: [DONE]`,
			`data: {"choices":[{"delta":{"content":"hi"}}]}`,
			`data: {"usage":{"cache_creation":{"ephemeral_5m_input_tokens":1}}}`,
			`data: not-json`,
		} {
			require.Equal(t, line, InjectChatCompletionCacheCreationIntoSSELine(line, 9, 9))
		}
	})
}
