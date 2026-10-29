//go:build unit

package handler

import (
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/handler/dto"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

func TestParseCustomEndpointsPreservesMinRecharge(t *testing.T) {
	items := dto.ParseCustomEndpoints(`[
		{"name":"public","endpoint":"https://public.example.com","description":"","min_recharge":0},
		{"name":"legacy","endpoint":"https://legacy.example.com","description":""}
	]`)

	require.Len(t, items, 2)
	require.NotNil(t, items[0].MinRecharge)
	require.Equal(t, 0.0, *items[0].MinRecharge)
	require.Nil(t, items[1].MinRecharge)
}

func TestFilterCustomEndpointsByRecharge(t *testing.T) {
	zero := 0.0
	fiveHundred := 500.0
	twoThousand := 2000.0

	endpoints := []dto.CustomEndpoint{
		{Name: "public", Endpoint: "https://public.example.com", MinRecharge: &zero},
		{Name: "legacy", Endpoint: "https://legacy.example.com"},
		{Name: "mid", Endpoint: "https://mid.example.com", MinRecharge: &fiveHundred},
		{Name: "vip", Endpoint: "https://vip.example.com", MinRecharge: &twoThousand},
	}

	t.Run("zero recharge only sees endpoints open to everyone", func(t *testing.T) {
		got := filterCustomEndpointsByRecharge(endpoints, 0)
		require.Len(t, got, 1)
		require.Equal(t, "public", got[0].Name)
	})

	t.Run("partial recharge unlocks the matching tier", func(t *testing.T) {
		got := filterCustomEndpointsByRecharge(endpoints, 600)
		require.Len(t, got, 2)
		require.Equal(t, []string{"public", "mid"}, []string{got[0].Name, got[1].Name})
	})

	t.Run("legacy endpoint without threshold keeps the historical default", func(t *testing.T) {
		got := filterCustomEndpointsByRecharge(endpoints, service.CustomEndpointRechargeThreshold)
		require.Len(t, got, 3)
		require.Equal(t, []string{"public", "legacy", "mid"}, []string{got[0].Name, got[1].Name, got[2].Name})
	})

	t.Run("high recharge sees every endpoint in configured order", func(t *testing.T) {
		got := filterCustomEndpointsByRecharge(endpoints, 100000)
		require.Len(t, got, 4)
		require.Equal(t, []string{"public", "legacy", "mid", "vip"}, []string{got[0].Name, got[1].Name, got[2].Name, got[3].Name})
	})

	t.Run("empty input returns a non-nil empty slice", func(t *testing.T) {
		got := filterCustomEndpointsByRecharge(nil, 0)
		require.NotNil(t, got)
		require.Empty(t, got)
	})
}
