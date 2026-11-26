//go:build unit

package service

import (
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

// scanGatewayRequestTopLevel is a structural scan: it finds where each top-level member starts and ends. It is
// not a validity gate, and PERF-18 deliberately keeps gjson.ValidBytes as the gate (first parse and every
// refresh of a changed body). This table pins where the two disagree, so nobody swaps one for the other by
// accident: "scanOK" is what the scan accepts, "valid" what gjson.ValidBytes accepts.
func TestScanTopLevelIsNotAValidityGate(t *testing.T) {
	cases := []struct {
		name   string
		body   string
		valid  bool
		scanOK bool
	}{
		{"well-formed", `{"model":"m","messages":[{"role":"user","content":"hi"}]}`, true, true},
		{"empty", ``, false, false},
		{"whitespace only", " \n\t", false, false},
		{"not an object (scan finds nothing, no error)", `[1,2,3]`, true, true},
		{"scalar top level", `"text"`, true, true},
		{"invalid escape in a value", `{"model":"a\qb"}`, false, true},
		{"truncated unicode escape", `{"model":"a\u12"}`, false, true},
		{"raw newline inside a string", "{\"model\":\"a\nb\"}", false, true},
		{"raw tab inside a string", "{\"model\":\"a\tb\"}", false, true},
		{"invalid UTF-8 inside a string (accepted by both)", "{\"model\":\"a\xffb\"}", true, true},
		{"lone surrogate escape (accepted by both)", `{"model":"\ud83d"}`, true, true},
		{"duplicate keys (accepted by both; first wins, model duplicates are rejected later)", `{"model":"a","model":"b"}`, true, true},
		{"BOM before the object", "\xef\xbb\xbf" + `{"model":"a"}`, false, true},
		{"trailing garbage after the object", `{"model":"a"} x`, false, true},
		{"second document after the object", `{"model":"a"}{"model":"b"}`, false, true},
		{"trailing comma in the object", `{"model":"a",}`, false, true},
		{"trailing comma in an array value", `{"messages":[1,2,]}`, false, true},
		{"missing comma between members", `{"model":"a" "stream":true}`, false, false},
		{"mismatched brackets inside a value", `{"messages":[1,2}}`, false, true},
		{"bad literal", `{"stream":tru}`, false, true},
		{"leading zero number", `{"max_tokens":012}`, false, true},
		{"number with trailing dot", `{"max_tokens":1.}`, false, true},
		{"single quoted string", `{'model':'a'}`, false, false},
		{"unquoted key", `{model:"a"}`, false, false},
		{"unterminated string", `{"model":"abc`, false, false},
		{"unterminated object", `{"model":"a"`, false, false},
		{"comment", `{"model":"a"/*x*/}`, false, false},
		{"NaN literal", `{"max_tokens":NaN}`, false, true},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			require.Equal(t, tc.valid, gjson.Valid(tc.body), "gjson.Valid")
			require.Equal(t, tc.valid, gjson.ValidBytes([]byte(tc.body)), "gjson.ValidBytes")
			_, err := scanGatewayRequestTopLevel([]byte(tc.body))
			require.Equal(t, tc.scanOK, err == nil, "scan accepted: %v", err)
			// whatever the scan thinks, the gate decides: ParseGatewayRequest rejects every invalid body
			_, parseErr := ParseGatewayRequest(NewRequestBodyRef([]byte(tc.body)), PlatformAnthropic)
			if !tc.valid {
				require.Error(t, parseErr)
			}
		})
	}
	// the cases the scan accepts although the document is invalid are exactly why it cannot stand in for the gate
	accepted := 0
	for _, tc := range cases {
		if tc.scanOK && !tc.valid {
			accepted++
		}
	}
	require.GreaterOrEqual(t, accepted, 10)
}
