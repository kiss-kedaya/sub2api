package middleware

import (
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

func TestAPIKeyQueueIdentityReentersGroupClientGateAfterPolicyChange(t *testing.T) {
	for _, initiallyRestricted := range []bool{false, true} {
		groupID := int64(11)
		key := &service.APIKey{ID: 8, UserID: 12, GroupID: &groupID, Group: &service.Group{
			ID: groupID, Platform: service.PlatformAnthropic, ClaudeCodeOnly: initiallyRestricted,
		}}
		initial := captureAPIKeyQueueIdentity(key)
		require.False(t, apiKeyQueueCoreIdentityChanged(initial, key))
		key.Group.ClaudeCodeOnly = !initiallyRestricted
		require.True(t, apiKeyQueueCoreIdentityChanged(initial, key))
	}
}
