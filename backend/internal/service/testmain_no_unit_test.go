//go:build !unit

package service

import (
	"os"
	"testing"
)

// 非 unit 构建（go test ./... / -tags=integration）下没有 unit 版 TestMain，
// 但 content moderation 等未打标签的用例会用 httptest 本地服务验证业务语义。
// 这里放行管理面出站校验的本机回环目标；出站安全策略本身由
// urlvalidator 与 admin_ssrf_entry_test（unit 构建，显式关闭本开关）覆盖。
func TestMain(m *testing.M) {
	restore := SetAdminOutboundAllowPrivateForTest(true)
	// 账号测试/模型同步的私网校验同理：解析失败按拒绝处理后，用不可解析假域名的用例
	// 需要默认放行；策略本身由显式关闭本开关的用例覆盖。
	SetAccountTestAllowPrivateTargetsForTest(true)
	code := m.Run()
	SetAccountTestAllowPrivateTargetsForTest(false)
	restore()
	os.Exit(code)
}
