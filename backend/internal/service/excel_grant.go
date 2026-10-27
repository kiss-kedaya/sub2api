package service

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/openai"
	"github.com/google/uuid"
)

type ExcelGrantConfig struct {
	Enabled bool     `json:"enabled"`
	Models  []string `json:"models"`
}
type ExcelGrantRecord struct {
	AccountID   int64            `json:"account_id"`
	AccountName string           `json:"account_name"`
	Config      ExcelGrantConfig `json:"config"`
	Revision    string           `json:"revision"`
	Configured  bool             `json:"configured"`
	ExpiresAt   *time.Time       `json:"expires_at"`
	Cipher      string           `json:"-"`
}
type ExcelGrantSession struct {
	ID, StateHash, VerifierCipher, Revision, PrimaryHash string
	AccountID                                            int64
	ExpiresAt                                            time.Time
}
type ExcelGrantRepository interface {
	List(context.Context) ([]ExcelGrantRecord, error)
	Get(context.Context, int64) (*ExcelGrantRecord, error)
	SaveConfig(context.Context, *ExcelGrantRecord, string) error
	CreateSession(context.Context, ExcelGrantSession) error
	ConsumeSession(context.Context, int64, string, string) (*ExcelGrantSession, error)
	StoreGrant(context.Context, int64, string, string, string, string, time.Time) (bool, error)
	WithAccountLock(context.Context, int64, func() error) error
}
type ExcelGrantService struct {
	repo      ExcelGrantRepository
	accounts  AccountRepository
	oauth     OpenAIOAuthClient
	encryptor SecretEncryptor
	tokenMu   sync.Mutex
	tokens    map[int64]excelGrantCacheEntry
	lifecycle sync.Mutex
	cancel    context.CancelFunc
	wg        sync.WaitGroup
}
type excelGrantCacheEntry struct {
	grant excelGrantTokens
	until time.Time
}

func NewExcelGrantService(repo ExcelGrantRepository, accounts AccountRepository, oauth OpenAIOAuthClient, encryptor SecretEncryptor) *ExcelGrantService {
	return &ExcelGrantService{repo: repo, accounts: accounts, oauth: oauth, encryptor: encryptor, tokens: make(map[int64]excelGrantCacheEntry)}
}
func (s *ExcelGrantService) List(ctx context.Context) ([]ExcelGrantRecord, error) {
	return s.repo.List(ctx)
}
func (s *ExcelGrantService) Get(ctx context.Context, id int64) (*ExcelGrantRecord, error) {
	return s.repo.Get(ctx, id)
}
func (s *ExcelGrantService) SaveConfig(ctx context.Context, id int64, config ExcelGrantConfig, expected string) (*ExcelGrantRecord, error) {
	a, err := s.accounts.GetByID(ctx, id)
	if err != nil {
		return nil, err
	}
	if !credentialGuardEligible(a) {
		return nil, errors.New("excel grants require an ordinary OpenAI OAuth account")
	}
	if len(config.Models) > 100 {
		return nil, errors.New("select at most 100 BPS models")
	}
	seen := map[string]bool{}
	models := []string{}
	for _, model := range config.Models {
		model = strings.TrimSpace(model)
		if model == "" || len(model) > 200 || strings.ContainsAny(model, "\r\n\x00*") {
			return nil, errors.New("BPS routing requires exact model names")
		}
		if !seen[model] {
			seen[model] = true
			models = append(models, model)
		}
	}
	config.Models = models
	if config.Enabled && len(models) == 0 {
		return nil, errors.New("select at least one exact model before enabling BPS")
	}
	row := &ExcelGrantRecord{AccountID: id, Config: config, Revision: uuid.NewString()}
	if err = s.repo.SaveConfig(ctx, row, expected); err != nil {
		return nil, err
	}
	s.Invalidate(id)
	return s.repo.Get(ctx, id)
}
func (s *ExcelGrantService) BeginAuthorization(ctx context.Context, id int64) (map[string]string, error) {
	if s.encryptor == nil {
		return nil, errors.New("grant encryption unavailable")
	}
	a, err := s.accounts.GetByID(ctx, id)
	if err != nil {
		return nil, err
	}
	if !credentialGuardEligible(a) {
		return nil, errors.New("account cannot authorize an independent Excel grant")
	}
	row, err := s.repo.Get(ctx, id)
	if err != nil {
		return nil, err
	}
	if row == nil {
		return nil, errors.New("save BPS model settings before authorizing")
	}
	state, err := openai.GenerateState()
	if err != nil {
		return nil, errors.New("cannot generate OAuth state")
	}
	verifier, err := openai.GenerateCodeVerifier()
	if err != nil {
		return nil, errors.New("cannot generate PKCE verifier")
	}
	cipher, err := s.encryptor.Encrypt(verifier)
	if err != nil {
		return nil, errors.New("cannot encrypt OAuth session")
	}
	session := ExcelGrantSession{ID: uuid.NewString(), AccountID: id, StateHash: excelGrantHash(state), VerifierCipher: cipher, Revision: row.Revision, PrimaryHash: excelGrantPrimaryHash(a), ExpiresAt: time.Now().Add(10 * time.Minute)}
	if err = s.repo.CreateSession(ctx, session); err != nil {
		return nil, err
	}
	return map[string]string{"session_id": session.ID, "auth_url": openai.BuildExcelAuthorizationURL(state, openai.GenerateCodeChallenge(verifier))}, nil
}
func (s *ExcelGrantService) CompleteAuthorization(ctx context.Context, id int64, sessionID, callback string) (*ExcelGrantRecord, error) {
	u, err := url.Parse(callback)
	registered, _ := url.Parse(openai.ExcelRedirectURI)
	if err != nil || u.Scheme != registered.Scheme || u.Host != registered.Host || u.Path != registered.Path || u.User != nil || u.Fragment != "" || u.Query().Get("code") == "" || u.Query().Get("state") == "" {
		return nil, errors.New("paste the complete official Excel callback URL")
	}
	session, err := s.repo.ConsumeSession(ctx, id, sessionID, excelGrantHash(u.Query().Get("state")))
	if err != nil || session == nil {
		return nil, errors.New("OAuth session expired, used or state mismatch")
	}
	verifier, err := s.encryptor.Decrypt(session.VerifierCipher)
	if err != nil {
		return nil, errors.New("OAuth session cannot be decrypted")
	}
	a, err := s.accounts.GetByID(ctx, id)
	if err != nil || excelGrantPrimaryHash(a) != session.PrimaryHash {
		return nil, errors.New("account changed during authorization")
	}
	var saved *ExcelGrantRecord
	err = s.repo.WithAccountLock(ctx, id, func() error {
		row, e := s.repo.Get(ctx, id)
		if e != nil || row == nil || row.Revision != session.Revision {
			return errors.New("BPS settings changed during authorization")
		}
		proxy := ""
		if a.Proxy != nil {
			proxy = a.Proxy.URL()
		}
		token, e := s.oauth.ExchangeCode(ctx, u.Query().Get("code"), verifier, openai.ExcelRedirectURI, proxy, openai.ExcelClientID)
		if e != nil {
			return errors.New("official Excel token exchange failed")
		}
		grant, e := validateExcelGrantTokens(a, token)
		if e != nil {
			return e
		}
		cipher, e := s.encryptGrant(grant)
		if e != nil {
			return e
		}
		changed, e := s.repo.StoreGrant(ctx, id, row.Revision, row.Cipher, session.PrimaryHash, cipher, time.Unix(grant.ExpiresAt, 0))
		if e != nil {
			return e
		}
		if !changed {
			return errors.New("account or grant changed during authorization")
		}
		saved, e = s.repo.Get(ctx, id)
		return e
	})
	if err == nil {
		s.Invalidate(id)
	}
	return saved, err
}

type excelGrantTokens struct {
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token"`
	IDToken      string `json:"id_token"`
	ExpiresAt    int64  `json:"expires_at"`
	AccountID    string `json:"account_id"`
	Email        string `json:"email"`
}

func validateExcelGrantTokens(a *Account, token *openai.TokenResponse) (excelGrantTokens, error) {
	grant := excelGrantTokens{}
	if token == nil || token.AccessToken == "" || token.RefreshToken == "" || token.IDToken == "" || token.ExpiresIn <= 0 {
		return grant, errors.New("excel token exchange returned incomplete credentials")
	}
	claims, err := openai.ParseIDToken(token.IDToken)
	if err != nil {
		return grant, errors.New("excel identity token is invalid")
	}
	clientOK := false
	for _, aud := range claims.Aud {
		clientOK = clientOK || aud == openai.ExcelClientID
	}
	if !clientOK {
		return grant, errors.New("Excel identity token client mismatch")
	}
	access, err := openai.DecodeIDToken(token.AccessToken)
	if err != nil || access.Exp <= time.Now().Unix() || access.OpenAIAuth == nil || access.OpenAIAuth.ChatGPTAccountID == "" {
		return grant, errors.New("Excel access token identity missing")
	}
	parts := strings.Split(token.AccessToken, ".")
	payload, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return grant, errors.New("Excel access token is invalid")
	}
	var client struct {
		ClientID string `json:"client_id"`
		AZP      string `json:"azp"`
	}
	if json.Unmarshal(payload, &client) != nil || (client.ClientID != openai.ExcelClientID && client.AZP != openai.ExcelClientID) {
		return grant, errors.New("Excel access token client mismatch")
	}
	info := claims.GetUserInfo()
	expectedEmail := strings.TrimSpace(a.GetCredential("email"))
	expectedID := a.GetChatGPTAccountID()
	if expectedEmail == "" || !strings.EqualFold(info.Email, expectedEmail) || (expectedID != "" && access.OpenAIAuth.ChatGPTAccountID != expectedID) {
		return grant, errors.New("Excel authorization does not match this account")
	}
	expires := time.Now().Unix() + token.ExpiresIn
	if access.Exp < expires {
		expires = access.Exp
	}
	return excelGrantTokens{AccessToken: token.AccessToken, RefreshToken: token.RefreshToken, IDToken: token.IDToken, ExpiresAt: expires, AccountID: access.OpenAIAuth.ChatGPTAccountID, Email: info.Email}, nil
}
func (s *ExcelGrantService) encryptGrant(grant excelGrantTokens) (string, error) {
	raw, _ := json.Marshal(grant)
	cipher, err := s.encryptor.Encrypt(string(raw))
	if err != nil || cipher == "" {
		return "", errors.New("Excel grant encryption failed")
	}
	return cipher, nil
}
func excelGrantHash(value string) string {
	sum := sha256.Sum256([]byte(value))
	return hex.EncodeToString(sum[:])
}
func excelGrantPrimaryHash(a *Account) string { return ExcelGrantPrimaryHash(a) }

// ExcelGrantPrimaryHash binds a secondary grant to the exact primary credential
// and selected proxy without storing either in authorization session metadata.
func ExcelGrantPrimaryHash(a *Account) string {
	if a == nil {
		return ""
	}
	raw, _ := json.Marshal(a.Credentials)
	proxy := ""
	if a.ProxyID != nil {
		proxy = strconv.FormatInt(*a.ProxyID, 10)
	}
	return excelGrantHash(string(raw) + "|" + proxy)
}
func (s *ExcelGrantService) RouteEnabled(ctx context.Context, id int64, model string) (bool, error) {
	row, err := s.repo.Get(ctx, id)
	if err != nil || row == nil || !row.Config.Enabled {
		return false, err
	}
	for _, allowed := range row.Config.Models {
		if allowed == model {
			return true, nil
		}
	}
	return false, nil
}
func (s *ExcelGrantService) AccessToken(ctx context.Context, id int64) (string, string, error) {
	if s.encryptor == nil {
		return "", "", errors.New("grant encryption unavailable")
	}
	s.tokenMu.Lock()
	cached, ok := s.tokens[id]
	s.tokenMu.Unlock()
	if ok && time.Now().Before(cached.until) && cached.grant.ExpiresAt > time.Now().Add(2*time.Minute).Unix() {
		return cached.grant.AccessToken, cached.grant.AccountID, nil
	}
	var grant excelGrantTokens
	err := s.repo.WithAccountLock(ctx, id, func() error {
		row, e := s.repo.Get(ctx, id)
		if e != nil {
			return e
		}
		if row == nil || !row.Config.Enabled || row.Cipher == "" {
			return errors.New("Excel grant is not authorized or enabled")
		}
		raw, e := s.encryptor.Decrypt(row.Cipher)
		if e != nil || json.Unmarshal([]byte(raw), &grant) != nil || grant.AccessToken == "" {
			return errors.New("Excel grant cannot be decrypted")
		}
		if grant.ExpiresAt > time.Now().Add(2*time.Minute).Unix() {
			return nil
		}
		a, e := s.accounts.GetByID(ctx, id)
		if e != nil || !credentialGuardEligible(a) {
			return errors.New("Excel account unavailable")
		}
		proxy := ""
		if a.Proxy != nil {
			proxy = a.Proxy.URL()
		}
		token, e := s.oauth.RefreshTokenWithClientID(ctx, grant.RefreshToken, proxy, openai.ExcelClientID)
		if e != nil || token == nil {
			return errors.New("Excel grant refresh failed; authorize again if required")
		}
		if token.RefreshToken == "" {
			token.RefreshToken = grant.RefreshToken
		}
		if token.IDToken == "" {
			token.IDToken = grant.IDToken
		}
		refreshed, e := validateExcelGrantTokens(a, token)
		if e != nil {
			return e
		}
		cipher, e := s.encryptGrant(refreshed)
		if e != nil {
			return e
		}
		changed, e := s.repo.StoreGrant(ctx, id, row.Revision, row.Cipher, excelGrantPrimaryHash(a), cipher, time.Unix(refreshed.ExpiresAt, 0))
		if e != nil {
			return e
		}
		if !changed {
			return errors.New("Excel grant changed during refresh")
		}
		grant = refreshed
		return nil
	})
	if err != nil {
		return "", "", err
	}
	s.tokenMu.Lock()
	if len(s.tokens) > 4096 {
		s.tokens = make(map[int64]excelGrantCacheEntry)
	}
	s.tokens[id] = excelGrantCacheEntry{grant: grant, until: time.Now().Add(30 * time.Second)}
	s.tokenMu.Unlock()
	return grant.AccessToken, grant.AccountID, nil
}
func (s *ExcelGrantService) Invalidate(id int64) {
	s.tokenMu.Lock()
	delete(s.tokens, id)
	s.tokenMu.Unlock()
}
