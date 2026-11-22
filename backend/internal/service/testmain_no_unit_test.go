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
	code := m.Run()
	restore()
	os.Exit(code)
}
