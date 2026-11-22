package admin

import (
	"fmt"
	"sort"
	"strings"
)

// 账号 base_url / api_base_urls 是服务端主动出站的目标地址：管理员把它改到
// 内网，就等于把账号变成一个内网请求/探测代理（生产 URL allowlist 默认关闭）。
// 因此变更这两个字段要求 step-up，并把旧/新地址写进审计。

const (
	credentialKeyBaseURL     = "base_url"
	credentialKeyAPIBaseURLs = "api_base_urls"
)

// accountOutboundCredentialChange 描述一次出站地址变更（只含地址，不含密钥）。
type accountOutboundCredentialChange struct {
	BaseURLChanged     bool
	BaseURLOld         string
	BaseURLNew         string
	APIBaseURLsChanged bool
	APIBaseURLsOld     string
	APIBaseURLsNew     string
}

func (c *accountOutboundCredentialChange) auditFields() map[string]any {
	if c == nil {
		return nil
	}
	fields := map[string]any{"changed": true}
	if c.BaseURLChanged {
		fields["account_base_url_old"] = c.BaseURLOld
		fields["account_base_url_new"] = c.BaseURLNew
	}
	if c.APIBaseURLsChanged {
		fields["account_api_base_urls_changed"] = true
		fields["account_api_base_urls_old"] = c.APIBaseURLsOld
		fields["account_api_base_urls_new"] = c.APIBaseURLsNew
	}
	return fields
}

// credentialsTouchOutbound 判断请求体里的 credentials 是否显式携带了
// base_url / api_base_urls（前端编辑表单会带上未改动的字段）。
func credentialsTouchOutbound(credentials map[string]any) bool {
	if len(credentials) == 0 {
		return false
	}
	if _, ok := credentials[credentialKeyBaseURL]; ok {
		return true
	}
	_, ok := credentials[credentialKeyAPIBaseURLs]
	return ok
}

// detectAccountOutboundChange 比较旧 credentials 与本次请求，找出真实变更。
// 没有任何真实变更时返回 nil（不触发 step-up，避免日常编辑被打断）。
func detectAccountOutboundChange(oldCredentials, requestCredentials map[string]any) *accountOutboundCredentialChange {
	if len(requestCredentials) == 0 {
		return nil
	}
	change := &accountOutboundCredentialChange{}

	if raw, ok := requestCredentials[credentialKeyBaseURL]; ok {
		oldValue := normalizeOutboundAddress(oldCredentials[credentialKeyBaseURL])
		newValue := normalizeOutboundAddress(raw)
		if oldValue != newValue {
			change.BaseURLChanged = true
			change.BaseURLOld = truncateAddress(oldValue)
			change.BaseURLNew = truncateAddress(newValue)
		}
	}

	if raw, ok := requestCredentials[credentialKeyAPIBaseURLs]; ok {
		oldValue := summarizeAPIBaseURLs(oldCredentials[credentialKeyAPIBaseURLs])
		newValue := summarizeAPIBaseURLs(raw)
		if oldValue != newValue {
			change.APIBaseURLsChanged = true
			change.APIBaseURLsOld = oldValue
			change.APIBaseURLsNew = newValue
		}
	}

	if !change.BaseURLChanged && !change.APIBaseURLsChanged {
		return nil
	}
	return change
}

func normalizeOutboundAddress(value any) string {
	text, ok := value.(string)
	if !ok {
		if value == nil {
			return ""
		}
		text = fmt.Sprint(value)
	}
	return strings.TrimSpace(text)
}

// summarizeAPIBaseURLs 把 api_base_urls 压成稳定字符串用于比较与审计，
// 例如 "anthropic=https://a|openai=https://b"。
func summarizeAPIBaseURLs(value any) string {
	mapping := map[string]string{}
	switch typed := value.(type) {
	case map[string]any:
		for protocol, address := range typed {
			normalized := normalizeOutboundAddress(address)
			if normalized == "" {
				continue
			}
			mapping[strings.TrimSpace(protocol)] = normalized
		}
	case map[string]string:
		for protocol, address := range typed {
			normalized := normalizeOutboundAddress(address)
			if normalized == "" {
				continue
			}
			mapping[strings.TrimSpace(protocol)] = normalized
		}
	case nil:
		return ""
	default:
		return truncateAddress(fmt.Sprint(typed))
	}
	if len(mapping) == 0 {
		return ""
	}
	keys := make([]string, 0, len(mapping))
	for key := range mapping {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	parts := make([]string, 0, len(keys))
	for _, key := range keys {
		parts = append(parts, key+"="+mapping[key])
	}
	return truncateAddress(strings.Join(parts, "|"))
}

// truncateAddress 限制审计字段长度（审计 extra 单值上限 128）。
func truncateAddress(value string) string {
	const maxLen = 120
	if len(value) <= maxLen {
		return value
	}
	return value[:maxLen] + "..."
}
