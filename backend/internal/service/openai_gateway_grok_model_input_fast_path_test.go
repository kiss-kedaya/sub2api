package service

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

func TestGrokSimpleMessagesMatchesReplayEncoder(t *testing.T) {
	cases := []struct {
		name, input string
		fast        bool
	}{
		{"typed", `[{"type":"message","role":"user","content":"hello"}]`, true},
		{"implicit and whitespace", `[ { "role":"system","content":"rules" }, {"role":"user","content":"你好 <>& \n"} ]`, true},
		{"normalized role", `[{"role":" USER ","content":"hello"}]`, true},
		{"assistant", `[{"role":"assistant","content":"answer"}]`, true},
		{"complete assistant", `[{"type":"message","role":"assistant","id":"m","status":"completed","content":"answer"}]`, true},
		{"escaped fields", `[{"ro\u006ce":"user","cont\u0065nt":"a\"b\\c","ty\u0070e":"message"}]`, false},
		{"surrogate", `[{"role":"user","content":"\ud800"}]`, false},
		{"empty array", `[ ]`, true},
		{"duplicate role", `[{"role":"user","role":"tool","content":"hello"}]`, false},
		{"duplicate escaped key", `[{"role":"user","ro\u006ce":"tool","content":"hello"}]`, false},
		{"unknown field", `[{"role":"user","content":"hello","extra":9007199254740993}]`, false},
		{"nested metadata", `[{"role":"user","content":"hello","id":{"n":1,"n":2}}]`, false},
		{"empty content", `[{"role":"user","content":" \t"}]`, false},
		{"missing role", `[{"type":"message","content":"hello"}]`, false},
		{"null type", `[{"type":null,"role":"user","content":"hello"}]`, false},
		{"partial assistant", `[{"role":"assistant","id":"m","content":"hello"}]`, false},
		{"nonpair call id", `[{"role":"user","content":"hello","call_id":"remove"}]`, false},
		{"structured text", `[{"role":"assistant","content":[{"type":"output_text","text":"hello"}]}]`, false},
		{"images", `[{"role":"user","content":[{"type":"input_image","image_url":"https://example.com/a.png"}]}]`, false},
		{"output before call", `[{"type":"function_call_output","output":"done"},{"type":"function_call","name":"read","arguments":{}}]`, false},
		{"chat tool", `[{"role":" TOOL ","content":"done"}]`, false},
		{"custom tool", `[{"type":"custom_tool_call","name":"apply_patch","input":"patch"}]`, false},
		{"reasoning", `[{"type":"reasoning","status":"completed","content":null}]`, false},
		{"scalar items", `[null,1,"hello"]`, false},
		{"invalid array", `[{"role":"user","content":"hello"},]`, false},
		{"invalid utf8", "[{\"role\":\"user\",\"content\":\"" + string([]byte{0xff}) + "\"}]", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			body := []byte(`{"before":9007199254740993,"input":` + tc.input + `,"after":"unchanged"}`)
			original := bytes.Clone(body)
			input := gjson.GetBytes(body, "input")
			_, fast := encodeGrokSimpleMessages(input)
			require.Equal(t, tc.fast, fast)
			expected, oldErr := encodeGrokResponsesModelInput(input.Raw)
			actual, err := sanitizeGrokResponsesModelInput(body)
			require.Equal(t, original, body, "must not mutate caller bytes")
			if oldErr != nil {
				require.EqualError(t, err, oldErr.Error())
				require.Nil(t, actual)
				return
			}
			require.NoError(t, err)
			decode := func(raw string) any {
				d := json.NewDecoder(strings.NewReader(raw))
				d.UseNumber()
				var v any
				require.NoError(t, d.Decode(&v))
				return v
			}
			require.Equal(t, decode(string(expected)), decode(gjson.GetBytes(actual, "input").Raw))
			end := input.Index + len(input.Raw)
			require.True(t, bytes.HasPrefix(actual, body[:input.Index]))
			require.True(t, bytes.HasSuffix(actual, body[end:]))
		})
	}
}

func TestGrokSimpleMessagesPatchValidationAndModel(t *testing.T) {
	for _, raw := range []string{`{"input":[{"role":"user","content":"hello"},]}`, `{"input":[{"role":"user","content":"hello"}]} trailing`} {
		out, err := patchGrokResponsesBody([]byte(raw), "grok-4.5")
		require.Error(t, err)
		require.Nil(t, out)
	}
	body := []byte(`{"model":"client","input":[{"role":"user","content":"hello"}]}`)
	original := bytes.Clone(body)
	out, err := patchGrokResponsesBody(body, "grok-4.5")
	require.NoError(t, err)
	require.Equal(t, "grok-4.5", gjson.GetBytes(out, "model").Str)
	require.Equal(t, "message", gjson.GetBytes(out, "input.0.type").Str)
	require.Equal(t, original, body)
}
