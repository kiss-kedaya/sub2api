package service

import (
	"net/http"
	"strings"
)

// gatewayBrowserUserAgent is the outbound identity used when the client sent a
// script/bot User-Agent. Cloudflare 1010 and similar browser-integrity checks
// reject python-urllib / Go-http-client while accepting a normal browser UA.
const gatewayBrowserUserAgent = "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/131.0.0.0 Safari/537.36"

var scriptUserAgentMarkers = []string{
	"python-urllib",
	"python-requests",
	"python-httpx",
	"aiohttp",
	"httpx/",
	"go-http-client",
	"curl/",
	"wget/",
	"httpie",
	"libwww-perl",
	"java/",
	"okhttp",
	"apache-httpclient",
	"node-fetch",
	"undici",
	"axios/",
	"postmanruntime",
	"insomnia/",
	"faraday",
	"rest-client",
	"powershell",
	"scrapy",
}

// IsScriptUserAgent reports client identities that are not browsers or official
// CLI clients (Codex / Claude Code / Gemini CLI).
func IsScriptUserAgent(ua string) bool {
	lower := strings.ToLower(strings.TrimSpace(ua))
	if lower == "" {
		return false
	}
	if strings.Contains(lower, "mozilla/") {
		return false
	}
	if strings.Contains(lower, "codex") ||
		strings.Contains(lower, "claude-cli") ||
		strings.Contains(lower, "claude-code") ||
		strings.Contains(lower, "gemini-cli") ||
		strings.Contains(lower, "google-cloud-sdk") {
		return false
	}
	for _, marker := range scriptUserAgentMarkers {
		if strings.Contains(lower, marker) {
			return true
		}
	}
	return false
}

// SanitizeForwardedUserAgent drops script/bot User-Agents so later account or
// platform identity can replace them. Official CLI and browser UAs pass through.
func SanitizeForwardedUserAgent(ua string) string {
	if IsScriptUserAgent(ua) {
		return ""
	}
	return strings.TrimSpace(ua)
}

// EnsureNonScriptUserAgent replaces missing or script User-Agents with a
// browser identity. Codex/Claude identity enforcement should run after this
// and may overwrite the value.
func EnsureNonScriptUserAgent(header http.Header) {
	if header == nil {
		return
	}
	current := strings.TrimSpace(header.Get("User-Agent"))
	if current == "" || IsScriptUserAgent(current) {
		header.Set("User-Agent", gatewayBrowserUserAgent)
	}
}
