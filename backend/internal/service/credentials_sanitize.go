package service

import "strings"

// SanitizeStoredCredentials strips secrets that must never be persisted on the
// account credentials map after conversion to OAuth tokens (Grok Web SSO / password).
// Call from admin create/update/import/apply-oauth paths.
func SanitizeStoredCredentials(platform string, creds map[string]any) map[string]any {
	if creds == nil {
		return nil
	}
	// Always strip Grok ephemeral secrets regardless of platform label (import
	// may set platform later; better never store them).
	for _, key := range []string{
		"password", "sso_token", "sso", "sso-rw", "clearTextPassword",
	} {
		delete(creds, key)
	}
	// Platform-specific: also strip cookie jar residue on Grok OAuth stores.
	if strings.EqualFold(strings.TrimSpace(platform), PlatformGrok) {
		delete(creds, "cookie")
	}
	return creds
}
