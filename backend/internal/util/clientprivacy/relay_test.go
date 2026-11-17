package clientprivacy

import (
	"encoding/json"
	"strconv"
	"strings"
	"testing"
)

func TestRelayErrorTypeUsesExistingStatusMapping(t *testing.T) {
	for _, tc := range []struct {
		status int
		want   string
	}{
		{0, "upstream_error"}, {200, "upstream_error"}, {400, "invalid_request_error"},
		{401, "authentication_error"}, {403, "permission_error"}, {404, "not_found_error"},
		{422, "upstream_error"}, {429, "rate_limit_error"}, {500, "api_error"}, {503, "api_error"},
	} {
		t.Run(strconv.Itoa(tc.status), func(t *testing.T) {
			for _, relayType := range []string{"new_api_error", "one_api_error"} {
				input := []byte(`{"error":{"type":"` + relayType + `","message":"bad input (request id: Synthetic123)","code":"invalid_request"}}`)
				output := JSONWithStatus(input, tc.status)
				var decoded struct {
					Error struct{ Type, Message, Code string }
				}
				if err := json.Unmarshal(output, &decoded); err != nil {
					t.Fatal(err)
				}
				if decoded.Error.Type != tc.want || decoded.Error.Message != "bad input" || decoded.Error.Code != "invalid_request" {
					t.Fatalf("unexpected error: %s", output)
				}
			}
		})
	}
}

func TestRelayErrorMessageOnlyRemovesExactSuffix(t *testing.T) {
	for _, tc := range []struct{ input, want string }{
		{"bad input (request id: Synthetic123)", "bad input"},
		{"bad input\t (request id: Synthetic123)", "bad input"},
		{"bad input (request_id: 00000000-0000-4000-8000-000000000001)", "bad input (request_id: 00000000-0000-4000-8000-000000000001)"},
		{"bad input (request id: Synthetic123) try again", "bad input (request id: Synthetic123) try again"},
	} {
		if got := ErrorMessage(tc.input); got != tc.want {
			t.Fatalf("got %q, want %q", got, tc.want)
		}
		if got := Text(tc.input); got != tc.input {
			t.Fatalf("internal diagnostic was changed: %q", got)
		}
	}
	for _, kind := range []string{"api_error", "invalid_request_error", "rate_limit_error", "vendor_api_error"} {
		if got := ErrorType(400, kind); got != kind {
			t.Fatalf("non-relay type changed: %q", got)
		}
	}
}

func TestRelayErrorSSEUsesHTTPStatus(t *testing.T) {
	input := "event: error\ndata: {\"error\":{\"type\":\"new_api_error\",\"message\":\"bad input (request id: Synthetic123)\"}}\n\n"
	output := ErrorSSEWithStatus(input, 400)
	if strings.Contains(output, "new_api_error") || strings.Contains(output, "request id:") || !strings.Contains(output, `"type":"invalid_request_error"`) {
		t.Fatalf("unexpected error stream: %q", output)
	}
}
