package openai

import "net/url"

// Public registration used by the official Excel add-in. No site credentials
// or private re-login service is bundled with this integration.
const (
	ExcelClientID     = "app_fnr0pYvVwwFDocDumLG3H2Bp"
	ExcelAuthorizeURL = "https://auth.openai.com/api/accounts/authorize"
	ExcelRedirectURI  = "https://bps.openai.com/basispoints/extension/360590d7-f8f9-4d88-bf75-0edfe0a4b9f3/auth/callback"
)

func BuildExcelAuthorizationURL(state, challenge string) string {
	q := url.Values{"client_id": {ExcelClientID}, "response_type": {"code"}, "redirect_uri": {ExcelRedirectURI}, "scope": {"openid offline_access email profile organization.read"}, "state": {state}, "code_challenge_method": {"S256"}, "code_challenge": {challenge}, "audience": {"https://api.openai.com/v1"}, "platform": {"PC"}}
	return ExcelAuthorizeURL + "?" + q.Encode()
}
