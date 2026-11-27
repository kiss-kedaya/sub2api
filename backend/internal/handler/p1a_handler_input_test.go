package handler

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
	"go.uber.org/zap"
)

func legacyCodexBootstrapInputLooksRelevant(body []byte) bool {
	input := gjson.GetBytes(body, "input")
	if !input.IsArray() {
		return false
	}
	found := false
	input.ForEach(func(_, item gjson.Result) bool {
		if item.Get("type").String() != "function_call_output" {
			return true
		}
		namespace := item.Get("namespace").String()
		name := item.Get("name").String()
		if isCodexDelegationTool(namespace, name) ||
			(namespace == "codex_app" && name == "automation_update") ||
			isCodexUntrustedContextCandidate(namespace, name) {
			found = true
			return false
		}
		return true
	})
	return found
}

func TestCodexBootstrapInputLooksRelevantDifferential(t *testing.T) {
	cases := []string{
		``,
		`{}`,
		`{"input":[]}`,
		`{"input":"function_call_output codex_app"}`,
		`{"input":[{"type":"function_call_output","namespace":"other","name":"create_thread"}]}`,
		`{"input":[{"type":"function_call_output","namespace":"codex_app","name":"create_thread"}]}`,
		`{"input":[{"type":"function_call_output","namespace":"codex_tui","name":"send_message_to_thread"}]}`,
		`{"input":[{"type":"function_call_output","namespace":"codex_app","name":"automation_update"}]}`,
		`{"input":[{"type":"function_call_output","namespace":"agent","name":"proposed_plan"}]}`,
		`{"input":[{"type":"function_call_output","namespace":"agent","name":"codex_apps_selected_page_part_1_of_2"}]}`,
		`{"input":[{"type":"function_\u0063all_output","namespace":"codex_\u0061pp","name":"create_thread"}]}`,
		`{"input":[{"type":"message","content":"function_call_output codex_app create_thread"}]}`,
	}
	for _, raw := range cases {
		body := []byte(raw)
		want := legacyCodexBootstrapInputLooksRelevant(body)
		require.Equalf(t, want, codexBootstrapInputLooksRelevant(nil, body), "nil ctx mismatch on %q", raw)
		c := newTopHandlerTestContext()
		require.Equalf(t, want, codexBootstrapInputLooksRelevant(c, body), "ctx mismatch on %q", raw)
	}
}

func TestValidateFunctionCallOutputRequestHandlerPaths(t *testing.T) {
	gin.SetMode(gin.TestMode)
	h := &OpenAIGatewayHandler{}
	log := zap.NewNop()

	cases := []struct {
		name     string
		body     string
		wantPass bool
	}{
		{
			name:     "no_function_call_output",
			body:     `{"input":[{"type":"message","content":"hi"}]}`,
			wantPass: true,
		},
		{
			name:     "non_function_call_output_tool_search_ignored_by_gate",
			body:     `{"input":[{"type":"tool_search_output"}]}`,
			wantPass: true,
		},
		{
			name:     "missing_call_id_rejected",
			body:     `{"input":[{"type":"function_call_output","output":"ok"}]}`,
			wantPass: false,
		},
		{
			name:     "missing_item_reference_rejected",
			body:     `{"input":[{"type":"function_call_output","call_id":"c1","output":"ok"}]}`,
			wantPass: false,
		},
		{
			name:     "previous_response_id_accepted",
			body:     `{"previous_response_id":"resp_1","input":[{"type":"function_call_output","call_id":"c1","output":"ok"}]}`,
			wantPass: true,
		},
		{
			name:     "escaped_function_call_output_validated",
			body:     `{"input":[{"type":"function_\u0063all_output","call_id":"c1","output":"ok"}]}`,
			wantPass: false,
		},
		{
			name:     "matching_item_reference_accepted",
			body:     `{"input":[{"type":"function_call_output","call_id":"c1","output":"ok"},{"type":"item_reference","id":"c1"}]}`,
			wantPass: true,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rec := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(rec)
			c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", nil)
			got := h.validateFunctionCallOutputRequest(c, []byte(tc.body), log)
			require.Equal(t, tc.wantPass, got)
			if !tc.wantPass {
				require.Equal(t, http.StatusBadRequest, rec.Code)
			}
		})
	}
}
