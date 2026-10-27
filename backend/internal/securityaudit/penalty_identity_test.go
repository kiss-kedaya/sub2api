package securityaudit

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestPenaltyConversationIdentityUsesOnlyExplicitSessionFields(t *testing.T) {
	for _, tc := range []struct{ body, want string }{
		{`{"metadata":{"user_id":"user_device_account_abc_session_12345678-abcd"}}`, "12345678-abcd"},
		{`{"metadata":{"user_id":"{\"device_id\":\"same-device\",\"session_id\":\"session-one\"}"}}`, "session-one"},
		{`{"client_metadata":{"session_id":"session-two"}}`, "session-two"},
		{`{"metadata":{"user_id":"same-user"},"input":"same prompt"}`, ""},
		{`{"previous_response_id":"resp_previous","input":"hi"}`, ""},
	} {
		require.Equal(t, tc.want, penaltyRequestSessionIdentity(Request{Body: []byte(tc.body)}))
	}
}
