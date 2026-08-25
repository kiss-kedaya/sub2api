package main

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestValidateLoopbackAddress(t *testing.T) {
	for _, address := range []string{"127.0.0.1:6060", "[::1]:6060", "localhost:6060"} {
		require.NoError(t, validateLoopbackAddress(address), address)
	}
	for _, address := range []string{"0.0.0.0:6060", ":6060", "192.168.1.2:6060", "not-an-address"} {
		require.Error(t, validateLoopbackAddress(address), address)
	}
}

func TestParsePprofInt(t *testing.T) {
	require.Equal(t, 25, parsePprofInt("25", 10))
	require.Equal(t, 0, parsePprofInt("0", 10))
	require.Equal(t, 10, parsePprofInt("-1", 10))
	require.Equal(t, 10, parsePprofInt("invalid", 10))
}
