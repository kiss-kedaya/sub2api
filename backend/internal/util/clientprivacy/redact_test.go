package clientprivacy

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
)

func TestTextRemovesPrivateDiagnostics(t *testing.T) {
	for _, input := range []string{
		"POST https://private-upstream.example/v1/messages?key=synthetic-secret failed",
		"dial tcp 192.0.2.41:8443: connection refused",
		"dial tcp [2001:db8::41]:8443: connection refused",
		"provider private-upstream.example:8443 failed",
		"contact synthetic-user@private-upstream.example",
		"Authorization: Bearer synthetic-credential-value",
		"proxy_url='socks5://synthetic-user:synthetic-pass@192.0.2.42:1080'",
		"api_key=synthetic-opaque-key-value",
		"account_name: synthetic upstream account",
		"sk-" + strings.Repeat("synthetic", 4),
		"-----BEGIN PRIVATE KEY-----\nsynthetic-key-material\n-----END PRIVATE KEY-----",
		"https&#58;&#47;&#47;private-upstream&#46;example failed",
		"https&amp;#58;&amp;#47;&amp;#47;private-upstream&amp;#46;example failed",
		`{"api\u005fkey":"synthetic-opaque-key-value"}`,
		"Incorrect API key provided: synthetic-opaque-value. You can find your API key in the dashboard",
		"Invalid access token supplied: at-synthetic-opaque-value",
		"Account 123 (synthetic-account-name) upstream=synthetic-provider-name",
	} {
		t.Run(input[:min(len(input), 25)], func(t *testing.T) {
			output := Text(input)
			if output == input || !strings.Contains(output, redacted) {
				t.Fatalf("private diagnostic was not redacted: %q", output)
			}
			for _, forbidden := range []string{"private-upstream", "192.0.2.", "2001:db8", "synthetic-credential", "synthetic-opaque", "synthetic-user", "synthetic-pass", "synthetic upstream account", "synthetic-key-material", "synthetic-account-name", "synthetic-provider-name"} {
				if strings.Contains(output, forbidden) {
					t.Fatalf("private diagnostic survived: %q", output)
				}
			}
		})
	}
}

func TestTextKeepsSafeProtocolSemantics(t *testing.T) {
	for _, input := range []string{
		"rate limit exceeded; retry in 1.5 seconds (429)",
		"invalid_request_error: messages.0.content must be a string",
		"response.failed: error.message describes the failure",
		"stream relay failure: scanner_error: unexpected EOF",
		"input tokens 1024 exceed maximum 512",
		"reasoning.mode is not supported for this model",
		"No healthy Grok OAuth account is currently available",
		"response.output_text.delta",
		"response.output_text.annotation.added",
		"response.output_item.added",
		"response.content_part.done",
		"response.reasoning_summary_text.delta",
		"response.function_call_arguments.done",
		"response.web_search_call.in_progress",
		"tools.0.function.parameters.properties.query.type",
		"generation_config.response_mime_type",
		"cache_control.ttl",
		"reasoning.effort",
		"io.EOF",
		"Missing required parameter: 'api_key'",
		"Unknown parameter: tools.0.function.arguments",
	} {
		if output := Text(input); output != input {
			t.Fatalf("safe protocol text changed: got %q, want %q", output, input)
		}
	}
}

func TestTextBoundsRecursiveDiagnosticEscaping(t *testing.T) {
	input := "https&#58;//private-upstream.example"
	for depth := 0; depth < maxDepth+2; depth++ {
		input = strings.ReplaceAll(input, "&", "&amp;")
	}
	if output := Text(input); output != redacted {
		t.Fatalf("over-nested encoding did not fail closed: %q", output)
	}
}

func TestJSONKeepsSafeDocumentBytes(t *testing.T) {
	input := []byte(" {\n  \"error\": {\"type\":\"invalid_request_error\", \"code\":\"context_length_exceeded\", \"param\":\"messages.0.content\", \"message\":\"too many tokens\"}, \"status_code\":403, \"usage\":{\"input_tokens\":1024,\"output_tokens\":7}\n} ")
	if output := JSON(input); !bytes.Equal(output, input) {
		t.Fatalf("safe document changed: %s", output)
	}
}

func TestJSONRemovesSecretsWithoutChangingStatusOrUsage(t *testing.T) {
	input := []byte(`{"type":"error","error":{"type":"invalid_request_error","code":"invalid_parameter","param":"messages.0.content","message":"POST https://private-upstream.example failed","account_name":"synthetic-account","credentials":{"api_key":"synthetic-key"},"headers":{"X-Private":"synthetic-header"},"stack":"synthetic-trace"},"status_code":403,"usage":{"input_tokens":1024,"output_tokens":7}}`)
	output := JSON(input)
	var decoded map[string]any
	if err := json.Unmarshal(output, &decoded); err != nil {
		t.Fatal(err)
	}
	if decoded["status_code"] != float64(403) || decoded["type"] != "error" {
		t.Fatalf("status or event type changed: %s", output)
	}
	errorBody, ok := decoded["error"].(map[string]any)
	if !ok {
		t.Fatalf("error object missing: %s", output)
	}
	if errorBody["type"] != "invalid_request_error" || errorBody["code"] != "invalid_parameter" || errorBody["param"] != "messages.0.content" {
		t.Fatalf("error protocol fields changed: %s", output)
	}
	usage, ok := decoded["usage"].(map[string]any)
	if !ok {
		t.Fatalf("usage object missing: %s", output)
	}
	if usage["input_tokens"] != float64(1024) || usage["output_tokens"] != float64(7) {
		t.Fatalf("usage changed: %s", output)
	}
	for _, value := range []string{"private-upstream", "synthetic-account", "synthetic-key", "synthetic-header", "synthetic-trace"} {
		if bytes.Contains(output, []byte(value)) {
			t.Fatalf("private field survived: %s", output)
		}
	}
}

func TestJSONVisitsDuplicateAndEscapedKeys(t *testing.T) {
	input := []byte(`{"error":{"message":"https:\/\/private-upstream.example","message":"safe","api\u005fkey":"synthetic-credential","api_key":null}}`)
	output := JSON(input)
	if bytes.Equal(output, input) || bytes.Contains(output, []byte("synthetic-credential")) || bytes.Contains(output, []byte("private-upstream")) {
		t.Fatalf("duplicate/escaped private field survived: %s", output)
	}
}

func TestJSONMalformedDiagnosticsFailClosed(t *testing.T) {
	for _, input := range []string{
		`{"error":{"api\u005fkey":"synthetic-opaque-value",`,
		`[{"message":"https:\/\/private-upstream.example"}`,
		`{"error":"safe"} trailing private-upstream.example`,
	} {
		output := JSON([]byte(input))
		if !json.Valid(output) || bytes.Contains(output, []byte("synthetic-opaque")) || bytes.Contains(output, []byte("private-upstream")) {
			t.Fatalf("malformed diagnostic survived: %s", output)
		}
	}
}

func TestJSONRedactsSecretsInFieldNamesAndNestedAliases(t *testing.T) {
	key := "sk-" + strings.Repeat("synthetic", 4)
	input := []byte(`{"error":{"` + key + `":"detail","upstream_base_url":"synthetic-private-address","proxy_credentials":"synthetic-opaque-value","upstream_status_code":403,"upstream_request_id":"request-safe","stack_trace":"synthetic-trace"}}`)
	output := JSON(input)
	for _, forbidden := range []string{key, "synthetic-private-address", "synthetic-opaque-value", "synthetic-trace"} {
		if bytes.Contains(output, []byte(forbidden)) {
			t.Fatalf("private alias survived: %s", output)
		}
	}
	if !bytes.Contains(output, []byte(`"upstream_status_code":403`)) || !bytes.Contains(output, []byte(`"upstream_request_id":"request-safe"`)) {
		t.Fatalf("status/correlation changed: %s", output)
	}
}

func TestJSONDeepAndOversizedDiagnosticsFailClosed(t *testing.T) {
	for _, input := range [][]byte{
		[]byte(strings.Repeat(`{"nested":`, maxDepth+2) + `"synthetic-hidden-value"` + strings.Repeat("}", maxDepth+2)),
		[]byte(strings.Repeat("x", maxInputBytes+1)),
	} {
		output := JSON(input)
		if !json.Valid(output) || bytes.Contains(output, []byte("synthetic-hidden-value")) {
			t.Fatalf("unsafe depth/size fallback: %s", output)
		}
	}
}

func TestSSEJoinsErrorDataBeforeSanitizing(t *testing.T) {
	input := "event: error\r\nid: event-safe\r\nretry: 1000\r\ndata: {\"type\":\"error\",\r\ndata: \"error\":{\"type\":\"api_error\",\"message\":\"https://private-upstream.example failed\",\"status_code\":403}}\r\n\r\n"
	output := SSE(input)
	if !strings.Contains(output, "event: error\r\nid: event-safe\r\nretry: 1000\r\n") || !strings.HasSuffix(output, "\r\n\r\n") {
		t.Fatalf("SSE framing changed: %q", output)
	}
	if strings.Contains(output, "private-upstream") || !strings.Contains(output, `"status_code":403`) || !strings.Contains(output, `"type":"api_error"`) {
		t.Fatalf("SSE privacy/status failure: %q", output)
	}
}

func TestSSEKeepsSafeFrameBytes(t *testing.T) {
	input := "event: error\nid: evt_123\nretry: 1000\ndata:{\"type\":\"error\",\n" +
		"data: \"error\":{\"type\":\"api_error\",\"message\":\"unexpected EOF\"}}\n\n"
	if output := SSE(input); output != input {
		t.Fatalf("safe frame changed: %q", output)
	}
}

func TestSSECoversBareDataOnlyAndMalformedErrors(t *testing.T) {
	for _, input := range []string{
		`{"error":{"message":"https://private-upstream.example failed"}}` + "\n\n",
		`data: {"error":{"message":"https://private-upstream.example failed"}}` + "\n\n",
		"event: error\ndata: api_key=synthetic-opaque-value\n\n",
	} {
		output := SSE(input)
		if output == input || strings.Contains(output, "private-upstream") || strings.Contains(output, "synthetic-opaque") {
			t.Fatalf("private error survived: %q", output)
		}
	}
}

func TestSSEPreservesSuccessfulContentInMixedStream(t *testing.T) {
	success := "event: response.output_text.delta\ndata: {\"type\":\"response.output_text.delta\",\"delta\":\"Source https://public-article.example\"}\n\n"
	failure := "event: response.failed\ndata: {\"type\":\"response.failed\",\"response\":{\"status\":\"failed\",\"error\":{\"message\":\"https://private-upstream.example failed\"}}}\n\n"
	output := SSE(success + failure)
	if !strings.HasPrefix(output, success) || strings.Contains(output, "private-upstream") {
		t.Fatalf("mixed stream changed successful content or leaked error: %q", output)
	}
	largeSuccess := "event: response.output_text.delta\ndata: " + strings.Repeat("x", maxInputBytes+1) + "\n\n"
	if output := SSE(largeSuccess); output != largeSuccess {
		t.Fatal("large successful frame changed")
	}
}

func TestErrorSSEHandlesUnnamedHTTPErrorFrames(t *testing.T) {
	input := "data: {\"message\":\"https://private-upstream.example failed\"}\n\n"
	if output := ErrorSSE(input); strings.Contains(output, "private-upstream") || output == input {
		t.Fatalf("known HTTP error leaked: %q", output)
	}
	truncated := `{"error":{"api\u005fkey":"synthetic-opaque-value",`
	if output := ErrorSSE(truncated); !json.Valid([]byte(output)) || strings.Contains(output, "synthetic-opaque") {
		t.Fatalf("known bare truncated JSON error leaked: %q", output)
	}
}
