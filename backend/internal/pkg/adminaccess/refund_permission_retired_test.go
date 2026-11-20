//go:build unit

package adminaccess

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// 退款/退款确认已强制 step-up（机器凭据一律 403），因此不再作为受限密钥可授予权限：
// 目录里不能继续展示一个授权后必然被拒的权限。
func TestPaymentRefundPermissionRetired(t *testing.T) {
	for _, permission := range PermissionCatalog() {
		require.NotEqual(t, "payment.refund", permission.ID, "退款权限必须从受限密钥目录移除")
	}
	require.Error(t, ValidatePolicy(Policy{GlobalPermissions: []string{"payment.refund"}}))
	require.False(t, RouteAllowed(
		Policy{GlobalPermissions: []string{"payment.refund"}},
		"POST", "/api/v1/admin/payment/orders/1/refund",
	))
}
