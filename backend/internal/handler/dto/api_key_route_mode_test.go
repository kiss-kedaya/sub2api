package dto

import (
	"encoding/json"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

// 管理端用户 API 密钥弹窗要直接显示「智能路由 +N / 单分组」：DTO 必须把
// 路由模式、候选分组数量与布尔标记一起算好，判定复用 service 的同一口径。
func TestAPIKeyFromService_MapsRouteMode(t *testing.T) {
	groupID := int64(7)
	tests := []struct {
		name       string
		key        *service.APIKey
		wantMode   string
		wantCount  int
		wantSmart  bool
		wantGroups []int64
	}{
		{
			name:     "no group",
			key:      &service.APIKey{ID: 1, UserID: 2, Status: service.StatusActive},
			wantMode: service.APIKeyRouteModeNone,
		},
		{
			name:      "single group falls back to group_id",
			key:       &service.APIKey{ID: 1, UserID: 2, Status: service.StatusActive, GroupID: &groupID},
			wantMode:  service.APIKeyRouteModeSingle,
			wantCount: 1,
		},
		{
			name:      "smart routing with ordered candidates",
			key:       &service.APIKey{ID: 1, UserID: 2, Status: service.StatusActive, GroupID: &groupID, RouteGroupIDs: []int64{7, 8, 9}},
			wantMode:  service.APIKeyRouteModeSmart,
			wantCount: 3,
			wantSmart: true,
			wantGroups: []int64{
				7, 8, 9,
			},
		},
		{
			name:      "duplicate candidates do not count as smart",
			key:       &service.APIKey{ID: 1, UserID: 2, Status: service.StatusActive, RouteGroupIDs: []int64{7, 7}},
			wantMode:  service.APIKeyRouteModeSingle,
			wantCount: 1,
			wantGroups: []int64{
				7, 7,
			},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			out := APIKeyFromService(tc.key)
			require.NotNil(t, out)
			require.Equal(t, tc.wantMode, out.RouteMode)
			require.Equal(t, tc.wantCount, out.RouteGroupCount)
			require.Equal(t, tc.wantSmart, out.IsSmartRouting)
			if tc.wantGroups == nil {
				require.Empty(t, out.GroupIDs)
			} else {
				require.Equal(t, tc.wantGroups, out.GroupIDs)
			}
		})
	}
}

// 字段必须出现在 JSON 里（即便 false/0），前端徽章才能稳定读取，
// 不依赖 omitempty 的缺省语义。
func TestAPIKeyRouteModeJSONFieldsAreStable(t *testing.T) {
	groupID := int64(7)
	out := APIKeyFromService(&service.APIKey{ID: 1, UserID: 2, Status: service.StatusActive, GroupID: &groupID})
	raw, err := json.Marshal(out)
	require.NoError(t, err)

	var decoded map[string]any
	require.NoError(t, json.Unmarshal(raw, &decoded))
	require.Equal(t, service.APIKeyRouteModeSingle, decoded["route_mode"])
	require.EqualValues(t, 1, decoded["route_group_count"])
	require.Equal(t, false, decoded["is_smart_routing"])
}
