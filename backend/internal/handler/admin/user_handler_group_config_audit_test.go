package admin

import (
	"encoding/json"
	"net/http/httptest"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func floatPtr(v float64) *float64    { return &v }
func int64SetPtr(v []int64) *[]int64 { return &v }

// 生效倍率必须复刻 SyncUserGroupRates 的语义：
// nil=不修改，空 map=清空全部，非空 map=逐分组写入/删除。
func TestEffectiveUserGroupRates(t *testing.T) {
	current := map[int64]float64{2: 1.5, 3: 1.2}

	require.Equal(t, current, effectiveUserGroupRates(current, nil))
	require.Empty(t, effectiveUserGroupRates(current, map[int64]*float64{}))

	next := effectiveUserGroupRates(current, map[int64]*float64{
		2: floatPtr(2.5),
		3: nil,
		4: floatPtr(0.9),
	})
	require.Equal(t, map[int64]float64{2: 2.5, 4: 0.9}, next)
}

// 审计只记录真实变化的倍率：
// 新增分组只出现在新值里，删除分组新值为 null，未变分组不产生噪声。
func TestChangedUserGroupRates(t *testing.T) {
	current := map[int64]float64{2: 1.5, 3: 1.2, 4: 0.8}
	next := map[int64]float64{2: 2.5, 3: 1.2, 5: 1.1}

	oldChanged, newChanged := changedUserGroupRates(current, next)
	require.Equal(t, map[int64]float64{2: 1.5, 4: 0.8}, oldChanged)

	expected := map[int64]*float64{2: floatPtr(2.5), 4: nil, 5: floatPtr(1.1)}
	require.Len(t, newChanged, len(expected))
	for groupID, want := range expected {
		got, ok := newChanged[groupID]
		require.Truef(t, ok, "missing group %d", groupID)
		if want == nil {
			require.Nil(t, got)
			continue
		}
		require.NotNil(t, got)
		require.Equal(t, *want, *got)
	}

	// 没有变化时不产生任何条目。
	oldNoop, newNoop := changedUserGroupRates(current, current)
	require.Empty(t, oldNoop)
	require.Empty(t, newNoop)
}

func TestCompactAuditGroupIDs(t *testing.T) {
	require.Equal(t, "", compactAuditGroupIDs(nil))
	require.Equal(t, "2,3,7,10", compactAuditGroupIDs([]int64{10, 2, 3, 2, 7}))
	// 非法 ID 被过滤，重复项只出现一次。
	require.Equal(t, "2,7", compactAuditGroupIDs([]int64{0, 7, -3, 2, 7}))
}

// 分组配置变更必须落进审计 extra（含旧/新倍率与分组变更），
// 且 extra 走键白名单：请求体里的密码等字段不会从这里漏出去。
func TestSetUserGroupConfigAuditExtra(t *testing.T) {
	gin.SetMode(gin.TestMode)
	target := &service.User{
		ID:                   42,
		AllowedGroups:        []int64{2, 3},
		RestrictPublicGroups: true,
		GroupRates:           map[int64]float64{2: 1.5, 5: 0.9},
	}
	req := UpdateUserRequest{
		Password:             "super-secret-password",
		AllowedGroups:        int64SetPtr([]int64{3, 7}),
		RestrictPublicGroups: boolPtr(false),
		GroupRates: map[int64]*float64{
			2: floatPtr(2.5),
			5: nil,
		},
	}

	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	setUserGroupConfigAuditExtra(c, target, req)

	// 键名与 middleware.auditCtxKeyExtra 一致（该常量不导出，测试用字面量镜像）。
	raw, ok := c.Get("audit_extra")
	require.True(t, ok)
	extra, ok := raw.(map[string]any)
	require.True(t, ok)

	require.EqualValues(t, 42, extra["target_user_id"])
	require.Equal(t, "2,3", extra["user_allowed_groups_old"])
	require.Equal(t, "3,7", extra["user_allowed_groups_new"])
	require.Equal(t, true, extra["user_allowed_groups_changed"])

	require.Equal(t, false, extra["user_restrict_public_groups_new"])
	require.Equal(t, true, extra["user_restrict_public_groups_old"])

	ratesNew, ok := extra["user_group_rates_new"].(string)
	require.True(t, ok)
	var decodedNew map[string]*float64
	require.NoError(t, json.Unmarshal([]byte(ratesNew), &decodedNew))
	require.NotNil(t, decodedNew["2"])
	require.Equal(t, 2.5, *decodedNew["2"])
	require.Contains(t, decodedNew, "5")
	require.Nil(t, decodedNew["5"])

	ratesOld, ok := extra["user_group_rates_old"].(string)
	require.True(t, ok)
	var decodedOld map[string]float64
	require.NoError(t, json.Unmarshal([]byte(ratesOld), &decodedOld))
	require.Equal(t, 1.5, decodedOld["2"])

	// 密码绝不能通过这条通道进入审计。
	for _, value := range extra {
		if text, isString := value.(string); isString {
			require.NotContains(t, text, "super-secret-password")
		}
	}
}

// 没有敏感字段变化时不写审计噪声（只有 target_user_id 的 map 不会落）
func TestSetUserGroupConfigAuditExtraNoChange(t *testing.T) {
	gin.SetMode(gin.TestMode)
	target := &service.User{
		ID:                   42,
		AllowedGroups:        []int64{2, 3},
		RestrictPublicGroups: true,
		GroupRates:           map[int64]float64{2: 1.5},
	}
	// 前端编辑表单会回传未修改的值：必须按「值是否变化」判断。
	req := UpdateUserRequest{
		AllowedGroups:        int64SetPtr([]int64{3, 2}),
		RestrictPublicGroups: boolPtr(true),
		GroupRates: map[int64]*float64{
			2: floatPtr(1.5),
		},
	}

	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	setUserGroupConfigAuditExtra(c, target, req)

	_, ok := c.Get("audit_extra")
	require.False(t, ok)
}
