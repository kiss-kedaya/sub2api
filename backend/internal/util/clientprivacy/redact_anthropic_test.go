package clientprivacy

import (
	"strings"
	"testing"
)

// Anthropic 官方参数路径在错误信息里必须原样保留：客户端要按这些字段名
// 修正请求。曾因被域名规则误判，thinking.type.enabled 被打码成 [redacted]。
func TestTextKeepsAnthropicParameterPaths(t *testing.T) {
	cases := []string{
		"\"thinking.type.enabled\" is not supported for this model. Use \"thinking.type.adaptive\" and \"output_config.effort\" to control thinking behavior.",
		"`thinking.type.enabled` is not supported for this model.",
		"`thinking.budget_tokens` must be less than max_tokens.",
		"`output_config.format` must be a json_schema object.",
		"`tool_choice.type` must be one of auto, any, tool.",
		"`metadata.user_id` must be a string.",
		"`max_tokens` is required.",
		"`temperature` is deprecated for this model.",
	}
	for _, input := range cases {
		if got := Text(input); got != input {
			t.Errorf("Text() altered a documented Anthropic parameter path\n input: %s\n  got: %s", input, got)
		}
	}
}

// 真正的上游诊断（域名、内网地址、凭据）仍然必须被清除。
func TestTextStillRedactsUpstreamDiagnostics(t *testing.T) {
	cases := []struct {
		name   string
		input  string
		banned []string
	}{
		{
			name:   "internal hostname",
			input:  "dial tcp: lookup internal-gw.corp.example: no such host",
			banned: []string{"internal-gw.corp.example"},
		},
		{
			name:   "private ip",
			input:  "connection to 10.1.2.3:8080 refused",
			banned: []string{"10.1.2.3"},
		},
		{
			name:   "api key",
			input:  "invalid api key " + syntheticAPIKey,
			banned: []string{syntheticAPIKey},
		},
		{
			name:   "upstream url",
			input:  "POST https://relay.example.test/v1/messages failed",
			banned: []string{"https://relay.example.test/v1/messages"},
		},
	}
	for _, tc := range cases {
		got := Text(tc.input)
		for _, secret := range tc.banned {
			if strings.Contains(got, secret) {
				t.Errorf("%s: Text() leaked %q\n input: %s\n   got: %s", tc.name, secret, tc.input, got)
			}
		}
	}
}

// syntheticAPIKey 是本包测试用的合成占位值：不指向任何真实凭据。
const syntheticAPIKey = "sk-" + "syntheticplaceholder0000000000"
