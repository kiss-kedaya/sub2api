package ipnotice

import "strings"

// gatewayBusinessPathPrefixes are the authenticated gateway API roots. Every
// one of them requires an API key, so a match means "a real billable gateway
// call" rather than infrastructure traffic.
//
// Health checks, readiness/liveness probes, metrics, static assets and admin
// endpoints intentionally never match: counting them would let a single
// operator or orchestrator inflate the distinct-user tally for their own egress
// IP and get it auto-listed.
var gatewayBusinessPathPrefixes = []string{
	"/v1/",                // OpenAI/Anthropic-compatible gateway, usage and billing reads
	"/v1beta/",            // Gemini-compatible gateway
	"/api/v3/",            // Ark-compatible gateway
	"/antigravity/",       // Antigravity gateway
	"/backend-api/codex/", // Codex direct gateway
}

// IsGatewayBusinessPath reports whether path belongs to an API-key
// authenticated gateway endpoint whose traffic may count toward IP sharing.
func IsGatewayBusinessPath(path string) bool {
	if path == "" {
		return false
	}
	for _, prefix := range gatewayBusinessPathPrefixes {
		if strings.HasPrefix(path, prefix) {
			return true
		}
	}
	// Exact root hits ("/v1") are not routed today, but treat them as business
	// paths so a future alias cannot silently escape detection.
	trimmed := strings.TrimSuffix(path, "/")
	for _, prefix := range gatewayBusinessPathPrefixes {
		if trimmed == strings.TrimSuffix(prefix, "/") {
			return true
		}
	}
	return false
}
