package service

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestGroupQualityEventJSONOmitsAccountIDAndSanitizesPelicanErrors(t *testing.T) {
	t.Run("account_id is never marshaled to JSON", func(t *testing.T) {
		e := &GroupQualityEvent{
			ID:           11,
			GroupID:      43,
			AccountID:    9988,
			ModelID:      "claude-sonnet-5",
			Status:       "degraded",
			ErrorMessage: "feet do not plausibly contact crank pedals",
		}
		PopulateGroupQualityAudit(e, "")
		raw, err := json.Marshal(e)
		require.NoError(t, err)
		require.NotContains(t, string(raw), "account_id")
		require.NotContains(t, string(raw), "9988")
		require.Contains(t, string(raw), "feet do not plausibly contact crank pedals")
	})

	t.Run("success events always clear error_message", func(t *testing.T) {
		e := &GroupQualityEvent{
			Status:       "success",
			ErrorMessage: "internal debug note",
		}
		PopulateGroupQualityAudit(e, "")
		require.Empty(t, e.ErrorMessage)
	})

	t.Run("sensitive or raw upstream degraded errors are cleared", func(t *testing.T) {
		sensitive := []string{
			"upstream https://relay.internal:8443/v1/messages returned 502",
			"Post \"https://api.example.com/v1\": dial tcp 10.0.0.5:443: i/o timeout",
			"account_id=42 failed with HTTP 401 unauthorized",
			"Authorization: Bearer test-tok-123",
			`{"error":{"type":"authentication_error","message":"invalid key"}}`,
			"unexpected EOF reading upstream body",
			"context deadline exceeded",
			"/etc/sub2api/config.yaml permission denied",
		}
		for _, msg := range sensitive {
			e := &GroupQualityEvent{Status: "degraded", ErrorMessage: msg}
			PopulateGroupQualityAudit(e, "")
			require.Empty(t, e.ErrorMessage, "expected sensitive error to be cleared: %s", msg)
		}
	})
}
