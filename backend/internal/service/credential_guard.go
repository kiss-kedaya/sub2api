package service

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/mail"
	"net/url"
	"sync"
	"time"

	"github.com/google/uuid"
)

type CredentialGuardConfig struct {
	Enabled         bool   `json:"enabled"`
	AutoRelogin     bool   `json:"auto_relogin"`
	IntervalSeconds int    `json:"interval_seconds"`
	CooldownSeconds int    `json:"cooldown_seconds"`
	MaxFailures     int    `json:"max_failures"`
	ReloginEndpoint string `json:"relogin_endpoint"`
	AllowedOrigin   string `json:"allowed_origin"`
	LoginEmail      string `json:"login_email"`
}
type CredentialGuardInput struct {
	CredentialGuardConfig
	Revision      string `json:"revision"`
	Password      string `json:"password,omitempty"`
	TOTPSecret    string `json:"totp_secret,omitempty"`
	APIToken      string `json:"api_token,omitempty"`
	ClearSecrets  bool   `json:"clear_secrets,omitempty"`
	ResetFailures bool   `json:"reset_failures,omitempty"`
}
type CredentialGuardRecord struct {
	AccountID         int64                 `json:"account_id"`
	AccountName       string                `json:"account_name"`
	Config            CredentialGuardConfig `json:"config"`
	Revision          string                `json:"revision"`
	SecretsConfigured bool                  `json:"secrets_configured"`
	State             string                `json:"state"`
	Code              string                `json:"code"`
	HTTPStatus        int                   `json:"http_status"`
	Failures          int                   `json:"failures"`
	Blocked           bool                  `json:"blocked"`
	LastCheckedAt     *time.Time            `json:"last_checked_at"`
	NextCheckAt       time.Time             `json:"next_check_at"`
	SecretCipher      string                `json:"-"`
	Lease             string                `json:"-"`
}
type CredentialGuardCompletion struct {
	State, Code string
	HTTPStatus  int
	Blocked     bool
	Next        time.Time
}
type CredentialGuardRepository interface {
	List(context.Context) ([]CredentialGuardRecord, error)
	Get(context.Context, int64) (*CredentialGuardRecord, error)
	Save(context.Context, *CredentialGuardRecord, string, bool) error
	Claim(context.Context, int64) (*CredentialGuardRecord, error)
	ReserveRelogin(context.Context, *CredentialGuardRecord) (bool, error)
	Complete(context.Context, *CredentialGuardRecord, CredentialGuardCompletion) error
	PersistCredentials(context.Context, *CredentialGuardRecord, *Account, map[string]any) (bool, error)
}
type CredentialGuardProber interface {
	ProbeCredentialGuard(context.Context, *Account) (string, int)
}
type credentialGuardSecrets struct {
	Password   string `json:"password"`
	TOTPSecret string `json:"totp_secret"`
	APIToken   string `json:"api_token"`
}
type CredentialGuardService struct {
	repo        CredentialGuardRepository
	accounts    AccountRepository
	prober      CredentialGuardProber
	encryptor   SecretEncryptor
	invalidator TokenCacheInvalidator
	client      *http.Client
	lifecycle   sync.Mutex
	cancel      context.CancelFunc
	wg          sync.WaitGroup
}

func NewCredentialGuardService(repo CredentialGuardRepository, accounts AccountRepository, prober CredentialGuardProber, encryptor SecretEncryptor, invalidator TokenCacheInvalidator) *CredentialGuardService {
	return &CredentialGuardService{repo: repo, accounts: accounts, prober: prober, encryptor: encryptor, invalidator: invalidator, client: newSSRFSafeHTTPClient(90 * time.Second)}
}
func DefaultCredentialGuardConfig() CredentialGuardConfig {
	return CredentialGuardConfig{IntervalSeconds: 1800, CooldownSeconds: 1800, MaxFailures: 3}
}
func validateCredentialGuardConfig(c CredentialGuardConfig) error {
	if c.IntervalSeconds < 60 || c.IntervalSeconds > 86400 || c.CooldownSeconds < 300 || c.CooldownSeconds > 604800 || c.MaxFailures < 1 || c.MaxFailures > 5 {
		return errors.New("invalid inspection interval, cooldown or failure budget")
	}
	if c.ReloginEndpoint != "" {
		if validateAccountOpsCustomURL(c.ReloginEndpoint) != nil {
			return errors.New("re-login requires a public HTTPS service")
		}
		u, _ := url.Parse(c.ReloginEndpoint)
		if u.RawQuery != "" || len(c.ReloginEndpoint) > 2048 {
			return errors.New("re-login endpoint must not contain query credentials")
		}
		if c.AllowedOrigin != u.Scheme+"://"+u.Host {
			return errors.New("explicitly allow the exact HTTPS origin of the re-login service")
		}
	}
	if c.LoginEmail != "" {
		m, err := mail.ParseAddress(c.LoginEmail)
		if err != nil || m.Address != c.LoginEmail || len(c.LoginEmail) > 254 {
			return errors.New("invalid login email")
		}
	}
	if c.AutoRelogin && (c.ReloginEndpoint == "" || c.LoginEmail == "") {
		return errors.New("configure a re-login service and login email before enabling automatic recovery")
	}
	return nil
}
func (s *CredentialGuardService) List(ctx context.Context) ([]CredentialGuardRecord, error) {
	return s.repo.List(ctx)
}
func (s *CredentialGuardService) Get(ctx context.Context, id int64) (*CredentialGuardRecord, error) {
	return s.repo.Get(ctx, id)
}
func (s *CredentialGuardService) Save(ctx context.Context, id int64, input CredentialGuardInput) (*CredentialGuardRecord, error) {
	a, err := s.accounts.GetByID(ctx, id)
	if err != nil {
		return nil, err
	}
	if !credentialGuardEligible(a) {
		return nil, errors.New("only ordinary OpenAI OAuth accounts can use credential inspection")
	}
	if err = validateCredentialGuardConfig(input.CredentialGuardConfig); err != nil {
		return nil, err
	}
	previous, err := s.repo.Get(ctx, id)
	if err != nil {
		return nil, err
	}
	if previous == nil {
		previous = &CredentialGuardRecord{AccountID: id, AccountName: a.Name, Config: DefaultCredentialGuardConfig()}
	}
	if input.Revision != previous.Revision {
		return nil, errors.New("configuration changed; refresh before saving")
	}
	secret := credentialGuardSecrets{}
	if previous.SecretCipher != "" && !input.ClearSecrets {
		if s.encryptor == nil {
			return nil, errors.New("credential encryption unavailable")
		}
		raw, e := s.encryptor.Decrypt(previous.SecretCipher)
		if e != nil || json.Unmarshal([]byte(raw), &secret) != nil {
			return nil, errors.New("saved login credentials cannot be decrypted")
		}
	}
	if len(input.Password) > 4096 || len(input.TOTPSecret) > 512 || len(input.APIToken) > 8192 {
		return nil, errors.New("login credential exceeds the size limit")
	}
	if input.Password != "" {
		secret.Password = input.Password
	}
	if input.TOTPSecret != "" {
		secret.TOTPSecret = input.TOTPSecret
	}
	if input.APIToken != "" {
		secret.APIToken = input.APIToken
	}
	if input.AutoRelogin && secret.Password == "" {
		return nil, errors.New("automatic re-login requires an encrypted password")
	}
	if (previous.Config.ReloginEndpoint != input.ReloginEndpoint || previous.Config.LoginEmail != input.LoginEmail) && previous.SecretCipher != "" && !input.ClearSecrets {
		return nil, errors.New("clear saved credentials and provide new ones when changing the login identity or service")
	}
	cipher := ""
	if secret.Password != "" || secret.TOTPSecret != "" || secret.APIToken != "" {
		if s.encryptor == nil {
			return nil, errors.New("credential encryption unavailable")
		}
		raw, _ := json.Marshal(secret)
		cipher, err = s.encryptor.Encrypt(string(raw))
		if err != nil || cipher == "" {
			return nil, errors.New("login credentials cannot be encrypted")
		}
	}
	record := &CredentialGuardRecord{AccountID: id, AccountName: a.Name, Config: input.CredentialGuardConfig, Revision: uuid.NewString(), SecretCipher: cipher, SecretsConfigured: cipher != ""}
	if err = s.repo.Save(ctx, record, input.Revision, input.ResetFailures); err != nil {
		return nil, err
	}
	return s.repo.Get(ctx, id)
}
func credentialGuardEligible(a *Account) bool {
	return a != nil && a.IsOpenAIOAuth() && !a.IsCredentialShadow() && !a.IsOpenAIAgentIdentity() && !a.IsOpenAIPersonalAccessToken()
}
func (s *CredentialGuardService) Start() {
	s.lifecycle.Lock()
	defer s.lifecycle.Unlock()
	if s.cancel != nil {
		return
	}
	ctx, cancel := context.WithCancel(context.Background())
	s.cancel = cancel
	s.wg.Add(1)
	go func() {
		defer s.wg.Done()
		ticker := time.NewTicker(time.Minute)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				for n := 0; n < 10; n++ {
					if ctx.Err() != nil {
						return
					}
					row, err := s.repo.Claim(ctx, 0)
					if err != nil || row == nil {
						break
					}
					s.inspect(ctx, row, false)
				}
			}
		}
	}()
}
func (s *CredentialGuardService) Stop() {
	s.lifecycle.Lock()
	if s.cancel != nil {
		s.cancel()
	}
	s.lifecycle.Unlock()
	s.wg.Wait()
}
func (s *CredentialGuardService) Run(ctx context.Context, id int64, relogin bool) (*CredentialGuardRecord, error) {
	row, err := s.repo.Claim(ctx, id)
	if err != nil {
		return nil, err
	}
	if row == nil {
		return nil, errors.New("inspection is already running or account is not configured")
	}
	s.inspect(ctx, row, relogin)
	saved, err := s.repo.Get(ctx, id)
	if err != nil {
		return nil, err
	}
	if saved != nil && saved.Lease == row.Lease {
		return nil, errors.New("inspection result could not be persisted")
	}
	return saved, nil
}
func (s *CredentialGuardService) inspect(parent context.Context, row *CredentialGuardRecord, manualRelogin bool) {
	ctx, cancel := context.WithTimeout(parent, 2*time.Minute)
	defer cancel()
	result := CredentialGuardCompletion{State: "transient", Code: "inspection_unavailable", Blocked: row.Blocked, Next: time.Now().Add(time.Duration(row.Config.IntervalSeconds) * time.Second)}
	defer func() {
		cleanup, c := context.WithTimeout(context.WithoutCancel(parent), 5*time.Second)
		defer c()
		_ = s.repo.Complete(cleanup, row, result)
	}()
	a, err := s.accounts.GetByID(ctx, row.AccountID)
	if err != nil || !credentialGuardEligible(a) {
		result.Code = "account_unavailable"
		return
	}
	if !a.Schedulable || a.Status != StatusActive {
		result.State = "skipped"
		result.Code = "account_disabled_by_operator"
		return
	}
	code, status := s.prober.ProbeCredentialGuard(ctx, a)
	result.Code = code
	result.HTTPStatus = status
	if code == "healthy" {
		result.State = "healthy"
		return
	}
	if code == "policy_blocked" || code == "security_verification_required" {
		result.State = "manual_required"
		result.Blocked = true
		return
	}
	if code != "credential_invalid" {
		return
	}
	result.State = "invalid"
	if (!row.Config.AutoRelogin && !manualRelogin) || row.Blocked {
		return
	}
	reserved, err := s.repo.ReserveRelogin(ctx, row)
	if err != nil || !reserved {
		result.Code = "relogin_budget_or_cooldown"
		result.Blocked = row.Failures >= row.Config.MaxFailures
		return
	}
	credentials, code, status := s.relogin(ctx, row, a)
	result.Code = code
	result.HTTPStatus = status
	if code != "relogin_succeeded" {
		result.Blocked = code == "security_verification_required" || code == "identity_mismatch" || code == "password_rejected" || code == "policy_blocked" || row.Failures+1 >= row.Config.MaxFailures
		if result.Blocked {
			result.State = "manual_required"
		}
		result.Next = time.Now().Add(time.Duration(row.Config.CooldownSeconds) * time.Second)
		return
	}
	probeAccount := *a
	probeAccount.Credentials = credentials
	if validCode, validStatus := s.prober.ProbeCredentialGuard(ctx, &probeAccount); validCode != "healthy" {
		result.Code = "replacement_credential_not_verified"
		result.HTTPStatus = validStatus
		result.State = "manual_required"
		result.Blocked = true
		return
	}
	applied, err := s.repo.PersistCredentials(ctx, row, a, credentials)
	if err != nil || !applied {
		result.State = "skipped"
		result.Code = "account_or_config_changed"
		return
	}
	if s.invalidator != nil {
		_ = s.invalidator.InvalidateToken(ctx, &probeAccount)
	}
	result.State = "healthy"
	result.Code = "relogin_succeeded"
}
