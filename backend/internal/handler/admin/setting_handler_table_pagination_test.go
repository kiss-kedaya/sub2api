//go:build unit

package admin

import (
	"net/http"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

func TestUpdateSettingsRejectsInvalidTablePreferences(t *testing.T) {
	for _, payload := range []map[string]any{
		{"table_default_page_size": 0},
		{"table_default_page_size": 4},
		{"table_default_page_size": -1},
		{"table_default_page_size": 1001},
		{"table_default_page_size": 20.5},
		{"table_default_page_size": nil},
		{"table_page_size_options": []int{}},
		{"table_page_size_options": nil},
		{"table_page_size_options": []int{10, 1001}},
		{"table_page_size_options": []int{4, 10}},
		{"table_page_size_options": []float64{10, 20.5}},
	} {
		h, repo := newStepUpSwitchTestHandler(t, map[string]string{
			service.SettingKeyTableDefaultPageSize: "20",
			service.SettingKeyTablePageSizeOptions: "[10,20,50]",
		})
		rec := doUpdateSettings(t, h, payload, nil)
		require.Equal(t, http.StatusBadRequest, rec.Code, "payload: %v", payload)
		require.Equal(t, "20", repo.values[service.SettingKeyTableDefaultPageSize])
		require.Equal(t, "[10,20,50]", repo.values[service.SettingKeyTablePageSizeOptions])
	}
}

func TestUpdateSettingsNormalizesAndPreservesTablePreferences(t *testing.T) {
	h, repo := newStepUpSwitchTestHandler(t, map[string]string{
		service.SettingKeyTableDefaultPageSize: "20",
		service.SettingKeyTablePageSizeOptions: "[10,20,50]",
	})
	rec := doUpdateSettings(t, h, map[string]any{"table_page_size_options": []int{50, 10, 20, 10}}, nil)
	require.Equal(t, http.StatusOK, rec.Code)
	require.Equal(t, "20", repo.values[service.SettingKeyTableDefaultPageSize])
	require.Equal(t, "[10,20,50]", repo.values[service.SettingKeyTablePageSizeOptions])
	rec = doUpdateSettings(t, h, map[string]any{"site_name": "Example"}, nil)
	require.Equal(t, http.StatusOK, rec.Code)
	require.Equal(t, "20", repo.values[service.SettingKeyTableDefaultPageSize])
	require.Equal(t, "[10,20,50]", repo.values[service.SettingKeyTablePageSizeOptions])
	def, max := h.settingService.GetTablePaginationLimits(t.Context())
	require.Equal(t, 20, def)
	require.Equal(t, 50, max)
}
