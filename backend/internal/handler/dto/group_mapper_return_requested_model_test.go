package dto

import (
	"encoding/json"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

func TestAdminGroupModelNamePolicyIsExplicitAndNotExposedToUsers(t *testing.T) {
	for _, enabled := range []bool{false, true} {
		group := &service.Group{ID: 17, Name: "model-policy", ReturnRequestedModel: enabled}
		payload, err := json.Marshal(GroupFromServiceAdmin(group))
		require.NoError(t, err)
		var admin map[string]any
		require.NoError(t, json.Unmarshal(payload, &admin))
		require.Contains(t, admin, "return_requested_model")
		require.Equal(t, enabled, admin["return_requested_model"])

		userPayload, err := json.Marshal(GroupFromService(group))
		require.NoError(t, err)
		require.NotContains(t, string(userPayload), "return_requested_model")
	}
}
