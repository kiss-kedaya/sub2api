package dto

import (
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

func TestAPIKeyDTOAutoBlockDatacenterIPsRoundTrip(t *testing.T) {
	require.False(t, APIKeyFromService(&service.APIKey{}).AutoBlockDatacenterIPs)
	require.True(t, APIKeyFromService(&service.APIKey{
		AutoBlockDatacenterIPs: true,
	}).AutoBlockDatacenterIPs)
}
