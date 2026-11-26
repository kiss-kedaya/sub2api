package service

import (
	"net/http"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// 线上真实事件的形状：Anthropic 组织被停用的中转号。reference / request id 是
// 同形状的合成值。申诉页是公开的官方页面，客户端和管理员都需要读到它。
const clientPrivacyOrgOnHoldBody = `{"error":{"details":{"appeal_url":"https://console.anthropic.com/appeal","error_code":"organization_on_hold"},"message":"This organization has been disabled. An organization admin can appeal at https://console.anthropic.com/appeal (Reference: TS-00000000-0000-4000-8000-000000000000) (request id: req_011SyntheticRequestID000)","type":"invalid_request_error"},"request_id":"req_011SyntheticRequestID000","type":"error"}`

func TestClientErrorPrivacyKeepsOfficialPublicLinks(t *testing.T) {
	const appeal = "https://console.anthropic.com/appeal"
	body := []byte(clientPrivacyOrgOnHoldBody)

	// 客户端响应（后台诊断页的“客户端响应”）：整份文档逐字不变。
	for _, status := range []int{http.StatusBadRequest, http.StatusForbidden} {
		require.Equal(t, clientPrivacyOrgOnHoldBody, string(sanitizeClientUpstreamErrorBody(status, body)))
	}

	// 入库的“上游消息”：申诉页留着，没有被打码。
	stored := sanitizeUpstreamErrorMessage(ExtractUpstreamErrorMessage(body))
	require.Contains(t, stored, "appeal at "+appeal+" (Reference: TS-")
	require.NotContains(t, stored, "[redacted]")

	// 用户侧错误日志视图沿用同一套规则。
	view := ToUserErrorRequest(&OpsErrorLog{Message: stored})
	require.Contains(t, view.Message, appeal)
	detail := ToUserErrorRequestDetail(&OpsErrorLogDetail{OpsErrorLog: OpsErrorLog{Message: stored}, ErrorBody: clientPrivacyOrgOnHoldBody})
	require.Equal(t, clientPrivacyOrgOnHoldBody, detail.ErrorBody)
}

func TestClientErrorPrivacyStillRedactsRelayAddressNextToPublicLink(t *testing.T) {
	const appeal = "https://console.anthropic.com/appeal"
	const relay = "https://relay.example.test/v1/messages"
	body := `{"error":{"message":"POST ` + relay + ` failed; appeal at ` + appeal + `","upstream_url":"` + appeal + `","type":"invalid_request_error"}}`

	cleaned := string(sanitizeClientUpstreamErrorBody(http.StatusBadRequest, []byte(body)))
	require.NotContains(t, cleaned, "relay.example.test")
	require.Contains(t, cleaned, "POST [redacted] failed; appeal at "+appeal)
	require.Equal(t, 1, strings.Count(cleaned, appeal), "the upstream_url field is private and must not keep its value")

	stored := sanitizeUpstreamErrorMessage(ExtractUpstreamErrorMessage([]byte(body)))
	require.Equal(t, "POST [redacted] failed; appeal at "+appeal, stored)
}
