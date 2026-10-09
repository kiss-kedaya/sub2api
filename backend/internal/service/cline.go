package service

import (
	"net/url"
	"strings"
)

// Cline 只提供 Chat Completions。Responses / Messages 入站在网关里转成 Chat。
// 同一个 Key 有积分和 ClinePass 两个钱包，超限只冷却对应钱包，不停整号。

const (
	DefaultClineTestModel     = "deepseek/deepseek-v4-flash"
	DefaultClinePassTestModel = "cline-pass/glm-5.3-flash"
	clineAPIHost              = "api.cline.bot"
	clinePassModelPrefix      = "cline-pass/"
	clineFreeModelPrefix      = "cline-free/"
	clinePassRateLimitKey     = "cline:pass"
	clineCreditsRateLimitKey  = "cline:credits"
)

func (a *Account) IsCline() bool {
	return a != nil && a.Platform == PlatformCline
}

func clineWalletRateLimitKey(model string) string {
	model = strings.ToLower(strings.TrimSpace(model))
	switch {
	case model == "":
		return ""
	case strings.HasPrefix(model, clinePassModelPrefix):
		return clinePassRateLimitKey
	case strings.HasPrefix(model, clineFreeModelPrefix):
		return ""
	default:
		return clineCreditsRateLimitKey
	}
}

func isOfficialClineHost(target string) bool {
	parsed, err := url.Parse(strings.TrimSpace(target))
	if err != nil {
		return false
	}
	return strings.EqualFold(parsed.Scheme, "https") && strings.EqualFold(parsed.Hostname(), clineAPIHost)
}

func (a *Account) clineAccountAPISupported() bool {
	return a.IsCline() && a.Type == AccountTypeAPIKey && isOfficialClineHost(a.GetOpenAIBaseURL())
}
