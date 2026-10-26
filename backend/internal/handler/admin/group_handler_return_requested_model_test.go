package admin

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestGroupRequestsDecodeReturnRequestedModel(t *testing.T) {
	var create CreateGroupRequest
	require.NoError(t, json.Unmarshal([]byte(`{"name":"model-policy","return_requested_model":true}`), &create))
	require.True(t, create.ReturnRequestedModel)
	var createDefault CreateGroupRequest
	require.NoError(t, json.Unmarshal([]byte(`{"name":"model-policy"}`), &createDefault))
	require.False(t, createDefault.ReturnRequestedModel)

	var update UpdateGroupRequest
	require.NoError(t, json.Unmarshal([]byte(`{"return_requested_model":false}`), &update))
	require.NotNil(t, update.ReturnRequestedModel)
	require.False(t, *update.ReturnRequestedModel)
	var omitted UpdateGroupRequest
	require.NoError(t, json.Unmarshal([]byte(`{}`), &omitted))
	require.Nil(t, omitted.ReturnRequestedModel)
}
