package service

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/url"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/openai"
	"github.com/stretchr/testify/require"
)

type excelGrantFixtureRepository struct {
	record       *ExcelGrantRecord
	session      *ExcelGrantSession
	stores       int
	consumeCalls int
}

func (r *excelGrantFixtureRepository) List(context.Context) ([]ExcelGrantRecord, error) {
	if r.record == nil {
		return []ExcelGrantRecord{}, nil
	}
	return []ExcelGrantRecord{*r.record}, nil
}
func (r *excelGrantFixtureRepository) Get(context.Context, int64) (*ExcelGrantRecord, error) {
	return r.record, nil
}
func (r *excelGrantFixtureRepository) SaveConfig(_ context.Context, row *ExcelGrantRecord, expected string) error {
	if r.record != nil && r.record.Revision != expected {
		return errors.New("changed")
	}
	r.record = row
	return nil
}
func (r *excelGrantFixtureRepository) CreateSession(_ context.Context, s ExcelGrantSession) error {
	r.session = &s
	return nil
}
func (r *excelGrantFixtureRepository) ConsumeSession(_ context.Context, id int64, session, hash string) (*ExcelGrantSession, error) {
	r.consumeCalls++
	if r.session == nil || r.session.ID != session || r.session.AccountID != id || r.session.StateHash != hash {
		return nil, nil
	}
	s := r.session
	r.session = nil
	return s, nil
}
func (r *excelGrantFixtureRepository) StoreGrant(_ context.Context, _ int64, revision, expected, primary, cipher string, expires time.Time) (bool, error) {
	if r.record.Revision != revision || r.record.Cipher != expected {
		return false, nil
	}
	r.stores++
	r.record.Cipher = cipher
	r.record.Configured = true
	r.record.ExpiresAt = &expires
	return true, nil
}
func (r *excelGrantFixtureRepository) WithAccountLock(_ context.Context, _ int64, run func() error) error {
	return run()
}

type excelGrantFixtureOAuth struct {
	token                *openai.TokenResponse
	exchanges, refreshes int
}

func (o *excelGrantFixtureOAuth) ExchangeCode(_ context.Context, code, verifier, redirect, proxy, client string) (*openai.TokenResponse, error) {
	o.exchanges++
	if client != openai.ExcelClientID || redirect != openai.ExcelRedirectURI || code == "" || verifier == "" {
		return nil, errors.New("invalid OAuth parameters")
	}
	return o.token, nil
}
func (o *excelGrantFixtureOAuth) RefreshToken(ctx context.Context, refresh, proxy string) (*openai.TokenResponse, error) {
	return o.RefreshTokenWithClientID(ctx, refresh, proxy, openai.ExcelClientID)
}
func (o *excelGrantFixtureOAuth) RefreshTokenWithClientID(_ context.Context, refresh, proxy, client string) (*openai.TokenResponse, error) {
	o.refreshes++
	if client != openai.ExcelClientID {
		return nil, errors.New("invalid client")
	}
	return o.token, nil
}
func excelGrantFixtureJWT(claims map[string]any) string {
	raw, _ := json.Marshal(claims)
	return "e30." + base64.RawURLEncoding.EncodeToString(raw) + ".fixture"
}
func fixtureExcelGrant(t *testing.T) (*ExcelGrantService, *excelGrantFixtureRepository, *excelGrantFixtureOAuth, *credentialGuardFixtureAccounts) {
	t.Helper()
	a := &credentialGuardFixtureAccounts{account: Account{ID: 4, Platform: PlatformOpenAI, Type: AccountTypeOAuth, Status: StatusActive, Schedulable: true, Credentials: map[string]any{"email": "operator@example.com", "chatgpt_account_id": "workspace-fixture", "access_token": "primary-token"}}}
	exp := time.Now().Add(time.Hour).Unix()
	token := &openai.TokenResponse{AccessToken: excelGrantFixtureJWT(map[string]any{"exp": exp, "client_id": openai.ExcelClientID, "https://api.openai.com/auth": map[string]any{"chatgpt_account_id": "workspace-fixture"}}), RefreshToken: "refresh-fixture", IDToken: excelGrantFixtureJWT(map[string]any{"email": "operator@example.com", "aud": []string{openai.ExcelClientID}, "exp": exp}), ExpiresIn: 3600}
	r := &excelGrantFixtureRepository{record: &ExcelGrantRecord{AccountID: 4, Revision: "initial", Config: ExcelGrantConfig{Enabled: true, Models: []string{"gpt-fixture"}}}}
	o := &excelGrantFixtureOAuth{token: token}
	s := NewExcelGrantService(r, a, o, managedProxyFixtureEncryptor{})
	return s, r, o, a
}
func TestExcelGrantAuthorizationIsIndependentEncryptedAndSingleUse(t *testing.T) {
	s, r, o, a := fixtureExcelGrant(t)
	original, _ := json.Marshal(a.account.Credentials)
	begin, err := s.BeginAuthorization(context.Background(), 4)
	require.NoError(t, err)
	u, err := url.Parse(begin["auth_url"])
	require.NoError(t, err)
	require.Equal(t, openai.ExcelClientID, u.Query().Get("client_id"))
	require.NotEmpty(t, u.Query().Get("code_challenge"))
	require.NotContains(t, begin["auth_url"], "primary-token")
	require.NotContains(t, r.session.VerifierCipher, "code_verifier")
	callback := openai.ExcelRedirectURI + "?" + url.Values{"code": {"fixture-code"}, "state": {u.Query().Get("state")}}.Encode()
	row, err := s.CompleteAuthorization(context.Background(), 4, begin["session_id"], callback)
	require.NoError(t, err)
	require.True(t, row.Configured)
	require.Equal(t, 1, o.exchanges)
	require.Equal(t, 1, r.stores)
	current, _ := json.Marshal(a.account.Credentials)
	require.JSONEq(t, string(original), string(current), "Codex grant was overwritten")
	raw, _ := json.Marshal(row)
	require.NotContains(t, string(raw), "refresh-fixture")
	require.NotContains(t, string(raw), "sealed:")
	_, err = s.CompleteAuthorization(context.Background(), 4, begin["session_id"], callback)
	require.Error(t, err)
	require.Equal(t, 1, o.exchanges)
}
func TestExcelGrantOAuthStateAndCallbackOriginValidatedBeforeExchange(t *testing.T) {
	s, _, o, _ := fixtureExcelGrant(t)
	begin, err := s.BeginAuthorization(context.Background(), 4)
	require.NoError(t, err)
	for _, callback := range []string{"https://evil.example/callback?code=x&state=y", openai.ExcelRedirectURI + "?code=x&state=wrong"} {
		_, err = s.CompleteAuthorization(context.Background(), 4, begin["session_id"], callback)
		require.Error(t, err)
	}
	require.Zero(t, o.exchanges)
}
func TestExcelGrantRejectsDifferentWorkspaceOrClient(t *testing.T) {
	_, _, o, a := fixtureExcelGrant(t)
	good, err := validateExcelGrantTokens(&a.account, o.token)
	require.NoError(t, err)
	require.Equal(t, "workspace-fixture", good.AccountID)
	copy := *o.token
	copy.IDToken = excelGrantFixtureJWT(map[string]any{"email": "different@example.com", "aud": []string{openai.ExcelClientID}, "exp": time.Now().Add(time.Hour).Unix()})
	_, err = validateExcelGrantTokens(&a.account, &copy)
	require.Error(t, err)
	copy = *o.token
	copy.AccessToken = excelGrantFixtureJWT(map[string]any{"client_id": openai.ClientID, "exp": time.Now().Add(time.Hour).Unix(), "https://api.openai.com/auth": map[string]any{"chatgpt_account_id": "workspace-fixture"}})
	_, err = validateExcelGrantTokens(&a.account, &copy)
	require.Error(t, err)
}
func TestExcelBPSRoutingRequiresExactOptedInModel(t *testing.T) {
	_, _, _, a := fixtureExcelGrant(t)
	require.False(t, ExcelBPSRouteSelected(&a.account, "gpt-fixture"))
	a.account.Extra = map[string]any{"excel_bps_enabled": true, "excel_bps_models": []any{"gpt-fixture"}}
	require.True(t, ExcelBPSRouteSelected(&a.account, "gpt-fixture"))
	require.False(t, ExcelBPSRouteSelected(&a.account, "gpt-fixture-other"))
	a.account.Extra["excel_bps_enabled"] = false
	require.False(t, ExcelBPSRouteSelected(&a.account, "gpt-fixture"))
}
