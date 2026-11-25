package admin

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestGroupRequestsDecodeOpenAITierMultipliers(t *testing.T) {
	var createReq CreateGroupRequest
	require.NoError(t, json.Unmarshal([]byte(`{"name":"tier","openai_fast_multiplier":1,"openai_ultrafast_multiplier":2.5}`), &createReq))
	require.NotNil(t, createReq.OpenAIFastMultiplier)
	require.Equal(t, 1.0, *createReq.OpenAIFastMultiplier)
	require.NotNil(t, createReq.OpenAIUltrafastMultiplier)
	require.Equal(t, 2.5, *createReq.OpenAIUltrafastMultiplier)

	var updateReq UpdateGroupRequest
	require.NoError(t, json.Unmarshal([]byte(`{"openai_fast_multiplier":0}`), &updateReq))
	require.NotNil(t, updateReq.OpenAIFastMultiplier, "0 reaches the service, where it clears the override")
	require.Equal(t, 0.0, *updateReq.OpenAIFastMultiplier)
	require.Nil(t, updateReq.OpenAIUltrafastMultiplier)

	var omitted UpdateGroupRequest
	require.NoError(t, json.Unmarshal([]byte(`{}`), &omitted))
	require.Nil(t, omitted.OpenAIFastMultiplier)
	require.Nil(t, omitted.OpenAIUltrafastMultiplier)
}
