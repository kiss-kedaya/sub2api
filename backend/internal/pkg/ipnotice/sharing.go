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

// gatewayBusinessRootPaths are the same gateway endpoints re-registered at the
// server root without the /v1 prefix (routes.RegisterGatewayRoutes rootRoute).
// They authenticate with the same API key and hit the same handlers, so a
// detector that only counted /v1/ gave a shared key an easy escape: call
// /responses (or /chat/completions, /models, ...) exclusively and the
// distinct-user counter never moves, so the IP is never listed and the shared
// keys are never disabled. Every entry is matched on a path-segment boundary,
// never as a loose substring.
var gatewayBusinessRootPaths = []string{
	"/responses",    // OpenAI Responses root alias (also /responses/*subpath)
	"/alpha/search", // OpenAI Alpha Search root alias
	"/models",       // model discovery root alias
	"/messages/count_tokens",
	"/chat/completions",
	"/embeddings",
	"/images/generations",
	"/images/edits",
	"/images/tasks",
	"/videos",
	"/tts",
	"/stt",
	"/custom-voices",
	"/realtime",
	"/web_search",
	"/x_search",
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
	for _, root := range gatewayBusinessRootPaths {
		if path == root || strings.HasPrefix(path, root+"/") {
			return true
		}
	}
	return false
}
