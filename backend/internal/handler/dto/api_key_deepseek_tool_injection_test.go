package dto

import (
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

func TestAPIKeyDTODeepSeekToolInjectionDefaultsAndDisabled(t *testing.T) {
	disabled := false
	require.True(t, APIKeyFromService(&service.APIKey{}).DeepSeekToolInjectionEnabled)
	require.False(t, APIKeyFromService(&service.APIKey{
		DeepSeekToolInjectionEnabled: &disabled,
	}).DeepSeekToolInjectionEnabled)
}
