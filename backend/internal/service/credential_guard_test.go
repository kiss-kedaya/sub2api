package service

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

type credentialGuardFixtureAccounts struct {
	AccountRepository
	account Account
}

func (r *credentialGuardFixtureAccounts) GetByID(context.Context, int64) (*Account, error) {
	copy := r.account
	return &copy, nil
}

type credentialGuardFixtureRepository struct {
	record              *CredentialGuardRecord
	completion          CredentialGuardCompletion
	reserved, persisted int
}

func (r *credentialGuardFixtureRepository) List(context.Context) ([]CredentialGuardRecord, error) {
	if r.record == nil {
		return []CredentialGuardRecord{}, nil
	}
	return []CredentialGuardRecord{*r.record}, nil
}
func (r *credentialGuardFixtureRepository) Get(context.Context, int64) (*CredentialGuardRecord, error) {
	return r.record, nil
}
func (r *credentialGuardFixtureRepository) Save(_ context.Context, row *CredentialGuardRecord, _ string, _ bool) error {
	r.record = row
	return nil
}
func (r *credentialGuardFixtureRepository) Claim(context.Context, int64) (*CredentialGuardRecord, error) {
	return r.record, nil
}
func (r *credentialGuardFixtureRepository) ReserveRelogin(context.Context, *CredentialGuardRecord) (bool, error) {
	r.reserved++
	return true, nil
}
func (r *credentialGuardFixtureRepository) Complete(_ context.Context, _ *CredentialGuardRecord, c CredentialGuardCompletion) error {
	r.completion = c
	return nil
}
func (r *credentialGuardFixtureRepository) PersistCredentials(context.Context, *CredentialGuardRecord, *Account, map[string]any) (bool, error) {
	r.persisted++
	return true, nil
}

type credentialGuardFixtureProber struct {
	code   string
	status int
	calls  int
}

func (p *credentialGuardFixtureProber) ProbeCredentialGuard(context.Context, *Account) (string, int) {
	p.calls++
	return p.code, p.status
}

type credentialGuardFixtureTransport func(*http.Request) (*http.Response, error)

func (f credentialGuardFixtureTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	return f(req)
}

func fixtureCredentialGuard(t *testing.T) (*CredentialGuardService, *credentialGuardFixtureRepository, *credentialGuardFixtureProber, *credentialGuardFixtureAccounts) {
	t.Helper()
	accounts := &credentialGuardFixtureAccounts{account: Account{ID: 3, Name: "fixture", Platform: PlatformOpenAI, Type: AccountTypeOAuth, Status: StatusActive, Schedulable: true, Credentials: map[string]any{"email": "operator@example.com"}}}
	repo := &credentialGuardFixtureRepository{}
	probe := &credentialGuardFixtureProber{code: "healthy", status: 200}
	s := NewCredentialGuardService(repo, accounts, probe, managedProxyFixtureEncryptor{}, nil)
	return s, repo, probe, accounts
}
func TestCredentialGuardDisabledByDefaultAndNoDefaultLoginProvider(t *testing.T) {
	c := DefaultCredentialGuardConfig()
	require.False(t, c.Enabled)
	require.False(t, c.AutoRelogin)
	require.Empty(t, c.ReloginEndpoint)
	require.NoError(t, validateCredentialGuardConfig(c))
}
func TestCredentialGuardRequiresExactPublicHTTPSOrigin(t *testing.T) {
	c := DefaultCredentialGuardConfig()
	c.AutoRelogin = true
	c.LoginEmail = "operator@example.com"
	for _, endpoint := range []string{"http://example.com/login", "https://127.0.0.1/login", "https://169.254.169.254/login", "https://example.com/login?secret=value", "https://example.com/login"} {
		c.ReloginEndpoint = endpoint
		c.AllowedOrigin = "https://different.example"
		require.Error(t, validateCredentialGuardConfig(c))
	}
	c.ReloginEndpoint = "https://login.example.com/relogin"
	c.AllowedOrigin = "https://login.example.com"
	require.NoError(t, validateCredentialGuardConfig(c))
}
func TestCredentialGuardSecretsEncryptedAndNeverSerialized(t *testing.T) {
	s, repo, _, _ := fixtureCredentialGuard(t)
	input := CredentialGuardInput{CredentialGuardConfig: DefaultCredentialGuardConfig(), Password: "private-password", TOTPSecret: "private-totp", APIToken: "private-api"}
	row, err := s.Save(context.Background(), 3, input)
	require.NoError(t, err)
	require.Contains(t, repo.record.SecretCipher, "sealed:")
	raw, err := json.Marshal(row)
	require.NoError(t, err)
	require.NotContains(t, string(raw), "private-password")
	require.NotContains(t, string(raw), "private-totp")
	require.NotContains(t, string(raw), "private-api")
	require.NotContains(t, string(raw), "secret_cipher")
	input.Revision = row.Revision
	input.Password = ""
	input.TOTPSecret = ""
	input.APIToken = ""
	_, err = s.Save(context.Background(), 3, input)
	require.NoError(t, err)
	plain, err := s.encryptor.Decrypt(repo.record.SecretCipher)
	require.NoError(t, err)
	require.Contains(t, plain, "private-password")
}
func TestCredentialGuardKeepsManualDisabledAndTransientStates(t *testing.T) {
	for _, code := range []string{"healthy", "rate_limited", "transport_error", "policy_blocked", "security_verification_required"} {
		t.Run(code, func(t *testing.T) {
			s, repo, p, a := fixtureCredentialGuard(t)
			p.code = code
			row := &CredentialGuardRecord{AccountID: 3, Config: DefaultCredentialGuardConfig()}
			row.Config.AutoRelogin = true
			s.inspect(context.Background(), row, false)
			require.Zero(t, repo.reserved)
			require.Zero(t, repo.persisted)
			if code == "policy_blocked" || code == "security_verification_required" {
				require.True(t, repo.completion.Blocked)
			}
			a.account.Schedulable = false
			p.calls = 0
			s.inspect(context.Background(), row, true)
			require.Zero(t, p.calls)
			require.Equal(t, "account_disabled_by_operator", repo.completion.Code)
		})
	}
}
func TestCredentialGuardExistingManualBlockCannotBeClearedByProbe(t *testing.T) {
	s, repo, _, _ := fixtureCredentialGuard(t)
	s.inspect(context.Background(), &CredentialGuardRecord{AccountID: 3, Blocked: true, Config: DefaultCredentialGuardConfig()}, false)
	require.True(t, repo.completion.Blocked)
}
func TestCredentialGuardLoginDoesNotFollowRedirectOrLeakSecrets(t *testing.T) {
	s, _, _, a := fixtureCredentialGuard(t)
	cfg := DefaultCredentialGuardConfig()
	cfg.ReloginEndpoint = "https://login.example.com/relogin"
	cfg.AllowedOrigin = "https://login.example.com"
	cfg.LoginEmail = "operator@example.com"
	raw, _ := json.Marshal(credentialGuardSecrets{Password: "private-password"})
	cipher, _ := s.encryptor.Encrypt(string(raw))
	row := &CredentialGuardRecord{Config: cfg, SecretCipher: cipher}
	calls := 0
	s.client = &http.Client{Transport: credentialGuardFixtureTransport(func(req *http.Request) (*http.Response, error) {
		calls++
		require.Equal(t, "login.example.com", req.URL.Host)
		return &http.Response{StatusCode: 302, Header: http.Header{"Location": []string{"https://redirect.example"}}, Body: io.NopCloser(strings.NewReader(`{"code":"redirect"}`)), Request: req}, nil
	})}
	_, code, _ := s.relogin(context.Background(), row, &a.account)
	require.Equal(t, "relogin_upstream_error", code)
	require.Equal(t, 1, calls)
	s.client = &http.Client{Transport: credentialGuardFixtureTransport(func(*http.Request) (*http.Response, error) {
		return nil, errors.New("token=private-api password=private-password")
	})}
	_, code, _ = s.relogin(context.Background(), row, &a.account)
	require.Equal(t, "relogin_transport_error", code)
	require.NotContains(t, code, "private")
}
func TestCredentialGuardReadsNDJSONTerminalResultWithoutWaitingForEOF(t *testing.T) {
	raw, err := readCredentialGuardProviderResult(strings.NewReader("{\"type\":\"progress\",\"message\":\"private-token\"}\n{\"type\":\"result\",\"payload\":{\"code\":\"mfa_required\"}}\ninvalid-trailer"))
	require.NoError(t, err)
	require.Equal(t, "mfa_required", guardProviderCode(raw))
	require.NotContains(t, string(raw), "private-token")
	_, err = readCredentialGuardProviderResult(strings.NewReader(`{"type":"progress"}`))
	require.Error(t, err)
	_, err = readCredentialGuardProviderResult(strings.NewReader(`{"credential":{"access_token":"` + strings.Repeat("x", 1<<20) + `"}}`))
	require.Error(t, err)
}
func TestCredentialGuardFailureBudgetStopsLastFailedAttempt(t *testing.T) {
	s, repo, p, a := fixtureCredentialGuard(t)
	p.code = "credential_invalid"
	p.status = 401
	cfg := DefaultCredentialGuardConfig()
	cfg.AutoRelogin = true
	cfg.ReloginEndpoint = "https://login.example.com/relogin"
	cfg.AllowedOrigin = "https://login.example.com"
	cfg.LoginEmail = "operator@example.com"
	raw, _ := json.Marshal(credentialGuardSecrets{Password: "private-password"})
	cipher, _ := s.encryptor.Encrypt(string(raw))
	s.client = &http.Client{Timeout: time.Second, Transport: credentialGuardFixtureTransport(func(*http.Request) (*http.Response, error) { return nil, errors.New("timeout") })}
	s.inspect(context.Background(), &CredentialGuardRecord{AccountID: a.account.ID, Config: cfg, SecretCipher: cipher, Failures: 2}, false)
	require.Equal(t, 1, repo.reserved)
	require.True(t, repo.completion.Blocked)
	require.Equal(t, "manual_required", repo.completion.State)
}
