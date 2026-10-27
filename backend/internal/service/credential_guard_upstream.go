package service

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"

	"github.com/Wei-Shaw/sub2api/internal/pkg/openai"
)

// ProbeCredentialGuard reuses the ordinary OAuth model request and selected
// proxy, while bypassing cache and status mutation. A cached model list cannot
// demonstrate that the current access token is still accepted upstream.
func (s *OpenAIGatewayService) ProbeCredentialGuard(ctx context.Context, a *Account) (string, int) {
	if !credentialGuardEligible(a) {
		return "account_unsupported", 0
	}
	headers, err := s.buildOpenAIAuthenticationHeaders(ctx, a, a.GetOpenAIAccessToken())
	if err != nil {
		return "credential_invalid", 401
	}
	setOpenAIChatGPTAccountHeaders(headers, a)
	headers.Set("Accept", "application/json")
	identity := resolveCodexOutboundIdentity(a.GetOpenAIUserAgent())
	headers.Set("Originator", identity.originator)
	headers.Set("User-Agent", identity.userAgent)
	headers.Set("Version", identity.version)
	u, err := buildCodexModelsManifestURL(chatgptCodexModelsURL, false, CodexCanonicalClientVersion())
	if err != nil {
		return "inspection_unavailable", 0
	}
	request := openAIModelsRequest{url: u.String(), headers: headers, proxyURL: upstreamModelsProxyURL(a), accountID: a.ID, credentialAccountID: a.ID, credentialAccount: a, accountConcurrency: a.Concurrency}
	_, err = s.fetchOpenAIModelsUpstream(ctx, request, "")
	if err == nil {
		return "healthy", 200
	}
	var upstream *codexModelsManifestUpstreamError
	if errors.As(err, &upstream) {
		if credentialGuardSecurityCode(guardProviderCode(upstream.body)) {
			return "security_verification_required", upstream.statusCode
		}
		switch upstream.statusCode {
		case 401:
			return "credential_invalid", 401
		case 403:
			return "policy_blocked", 403
		case 429:
			return "rate_limited", 429
		}
		return "upstream_unavailable", upstream.statusCode
	}
	return "transport_error", 0
}

func guardProviderCode(body []byte) string {
	var doc struct {
		Code  string `json:"code"`
		Error struct {
			Code string `json:"code"`
			Type string `json:"type"`
		} `json:"error"`
	}
	if json.Unmarshal(body, &doc) != nil {
		return ""
	}
	if doc.Code != "" {
		return doc.Code
	}
	if doc.Error.Code != "" {
		return doc.Error.Code
	}
	return doc.Error.Type
}
func credentialGuardSecurityCode(code string) bool {
	switch strings.ToLower(strings.TrimSpace(code)) {
	case "mfa_required", "captcha_required", "security_check", "security_verification_required", "phone_verification_required", "email_verification_required", "authorization_denied", "account_deactivated", "account_deleted":
		return true
	}
	return false
}

func (s *CredentialGuardService) relogin(ctx context.Context, row *CredentialGuardRecord, a *Account) (map[string]any, string, int) {
	if err := validateCredentialGuardConfig(row.Config); err != nil || row.Config.ReloginEndpoint == "" || s.encryptor == nil {
		return nil, "relogin_not_configured", 0
	}
	raw, err := s.encryptor.Decrypt(row.SecretCipher)
	secret := credentialGuardSecrets{}
	if err != nil || json.Unmarshal([]byte(raw), &secret) != nil || secret.Password == "" {
		return nil, "login_secret_unavailable", 0
	}
	body, _ := json.Marshal(map[string]string{"action": "start", "email": row.Config.LoginEmail, "auth_mode": "password_2fa", "password": secret.Password, "mfa_secret": secret.TOTPSecret})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, row.Config.ReloginEndpoint, bytes.NewReader(body))
	if err != nil {
		return nil, "relogin_not_configured", 0
	}
	req.Header.Set("Content-Type", "application/json")
	if secret.APIToken != "" {
		req.Header.Set("Authorization", "Bearer "+secret.APIToken)
	}
	guarded := *s.client
	guarded.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	resp, err := guarded.Do(req)
	if err != nil {
		return nil, "relogin_transport_error", 0
	}
	defer func() { _ = resp.Body.Close() }()
	data, err := readCredentialGuardProviderResult(resp.Body)
	if err != nil {
		return nil, "relogin_response_invalid", resp.StatusCode
	}
	code := guardProviderCode(data)
	if credentialGuardSecurityCode(code) {
		return nil, "security_verification_required", resp.StatusCode
	}
	if code == "password_rejected" || code == "invalid_password" {
		return nil, "password_rejected", resp.StatusCode
	}
	if resp.StatusCode == 401 {
		return nil, "password_rejected", 401
	}
	if resp.StatusCode == 403 {
		return nil, "policy_blocked", 403
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, "relogin_upstream_error", resp.StatusCode
	}
	var result struct {
		Credential map[string]any `json:"credential"`
		Code       string         `json:"code"`
	}
	if json.Unmarshal(data, &result) != nil || result.Credential == nil {
		return nil, "relogin_response_invalid", resp.StatusCode
	}
	text := func(key string) string { value, _ := result.Credential[key].(string); return strings.TrimSpace(value) }
	if text("access_token") == "" || text("refresh_token") == "" || text("id_token") == "" {
		return nil, "replacement_credential_incomplete", resp.StatusCode
	}
	// This is only an identity consistency check. The replacement access token
	// must also succeed against the real provider before any CAS write occurs.
	claims, err := openai.ParseIDToken(text("id_token"))
	if err != nil {
		return nil, "identity_mismatch", resp.StatusCode
	}
	info := claims.GetUserInfo()
	expectedEmail := strings.TrimSpace(a.GetCredential("email"))
	if !strings.EqualFold(info.Email, row.Config.LoginEmail) || (expectedEmail != "" && !strings.EqualFold(info.Email, expectedEmail)) {
		return nil, "identity_mismatch", resp.StatusCode
	}
	expectedID := a.GetCredential("chatgpt_account_id")
	if expectedID == "" {
		expectedID = a.GetCredential("account_id")
	}
	if expectedID != "" && info.ChatGPTAccountID != expectedID {
		return nil, "identity_mismatch", resp.StatusCode
	}
	allowed := map[string]any{"access_token": text("access_token"), "refresh_token": text("refresh_token"), "id_token": text("id_token"), "email": info.Email}
	if info.ChatGPTAccountID != "" {
		allowed["chatgpt_account_id"] = info.ChatGPTAccountID
		allowed["account_id"] = info.ChatGPTAccountID
	}
	if expiry := text("expires_at"); expiry != "" {
		allowed["expires_at"] = expiry
	}
	return MergeCredentials(a.Credentials, allowed), "relogin_succeeded", resp.StatusCode
}

// Session providers may send progress objects as NDJSON before their terminal
// result. Read only bounded JSON and return immediately at that result, without
// waiting for EOF or logging progress text supplied by the provider.
func readCredentialGuardProviderResult(body io.Reader) ([]byte, error) {
	limited := &io.LimitedReader{R: body, N: (1 << 20) + 1}
	decoder := json.NewDecoder(limited)
	for n := 0; n < 4096; n++ {
		var event map[string]json.RawMessage
		if err := decoder.Decode(&event); err != nil {
			return nil, errors.New("login response has no valid result")
		}
		if limited.N <= 0 {
			return nil, errors.New("login response exceeds the size limit")
		}
		var kind string
		_ = json.Unmarshal(event["type"], &kind)
		if kind == "result" {
			if len(event["payload"]) == 0 {
				return nil, errors.New("login result is empty")
			}
			return event["payload"], nil
		}
		if len(event["credential"]) > 0 || len(event["error"]) > 0 || len(event["code"]) > 0 {
			raw, err := json.Marshal(event)
			return raw, err
		}
		var status string
		_ = json.Unmarshal(event["status"], &status)
		switch status {
		case "failed", "error", "auth_failed":
			raw, err := json.Marshal(event)
			return raw, err
		}
	}
	return nil, errors.New("login response exceeds the event limit")
}
