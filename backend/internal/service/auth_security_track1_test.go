package service

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"strings"
	"testing"
	"time"

	"entgo.io/ent/dialect"
	entsql "entgo.io/ent/dialect/sql"
	dbent "github.com/Wei-Shaw/sub2api/ent"
	"github.com/Wei-Shaw/sub2api/ent/authidentity"
	"github.com/Wei-Shaw/sub2api/ent/enttest"
	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/util/urlvalidator"
	"github.com/pquerna/otp/totp"
	"github.com/stretchr/testify/require"
	_ "modernc.org/sqlite"
)

type track1UserRepoStub struct {
	UserRepository
	users        map[int64]*User
	byEmail      map[string]*User
	updateCalled int
}

func newTrack1UserRepoStub(users ...*User) *track1UserRepoStub {
	r := &track1UserRepoStub{
		users:   make(map[int64]*User, len(users)),
		byEmail: make(map[string]*User, len(users)),
	}
	for _, u := range users {
		r.users[u.ID] = u
		r.byEmail[strings.ToLower(strings.TrimSpace(u.Email))] = u
	}
	return r
}

func (r *track1UserRepoStub) GetByID(_ context.Context, id int64) (*User, error) {
	u, ok := r.users[id]
	if !ok {
		return nil, ErrUserNotFound
	}
	return u, nil
}

func (r *track1UserRepoStub) GetByEmail(_ context.Context, email string) (*User, error) {
	u, ok := r.byEmail[strings.ToLower(strings.TrimSpace(email))]
	if !ok {
		return nil, ErrUserNotFound
	}
	return u, nil
}

func (r *track1UserRepoStub) Update(_ context.Context, user *User, _ UserUpdateFields) error {
	r.updateCalled++
	r.users[user.ID] = user
	r.byEmail[strings.ToLower(strings.TrimSpace(user.Email))] = user
	return nil
}

func (r *track1UserRepoStub) UpdateTotpSecret(_ context.Context, userID int64, encryptedSecret *string) error {
	if u, ok := r.users[userID]; ok {
		u.TotpSecretEncrypted = encryptedSecret
	}
	return nil
}

func (r *track1UserRepoStub) DisableTotp(_ context.Context, userID int64) error {
	if u, ok := r.users[userID]; ok {
		u.TotpEnabled = false
		u.TotpSecretEncrypted = nil
	}
	return nil
}

func (r *track1UserRepoStub) DisableTotpWithExpectedRole(_ context.Context, userID int64, _ string, _ *string) error {
	if u, ok := r.users[userID]; ok {
		u.TotpEnabled = false
		u.TotpSecretEncrypted = nil
	}
	return nil
}

type track1PasskeyRepoStub struct {
	PasskeyRepository
	records map[int64][]PasskeyCredentialRecord
	err     error
}

func (r *track1PasskeyRepoStub) ListByUserID(_ context.Context, userID int64) ([]PasskeyCredentialRecord, error) {
	if r.err != nil {
		return nil, r.err
	}
	return r.records[userID], nil
}

type track1SettingRepoStub struct {
	SettingRepository
	values map[string]string
	err    error
}

func (r *track1SettingRepoStub) GetValue(_ context.Context, key string) (string, error) {
	if r.err != nil {
		return "", r.err
	}
	if v, ok := r.values[key]; ok {
		return v, nil
	}
	return "", ErrSettingNotFound
}

func (r *track1SettingRepoStub) GetMultiple(_ context.Context, keys []string) (map[string]string, error) {
	if r.err != nil {
		return nil, r.err
	}
	out := make(map[string]string, len(keys))
	for _, k := range keys {
		if v, ok := r.values[k]; ok {
			out[k] = v
		}
	}
	return out, nil
}

func (r *track1SettingRepoStub) GetAll(_ context.Context) (map[string]string, error) {
	if r.err != nil {
		return nil, r.err
	}
	out := make(map[string]string, len(r.values))
	for k, v := range r.values {
		out[k] = v
	}
	return out, nil
}

type track1TotpCacheStub struct {
	TotpCache
	sessions       map[string]*TotpLoginSession
	revokedUserIDs []int64
	approvals      map[int64]string
}

func newTrack1TotpCacheStub() *track1TotpCacheStub {
	return &track1TotpCacheStub{
		sessions:  make(map[string]*TotpLoginSession),
		approvals: make(map[int64]string),
	}
}

func (c *track1TotpCacheStub) SetAdmin2FAApproval(_ context.Context, userID int64, digest string, _ time.Duration) error {
	c.approvals[userID] = digest
	return nil
}

func (c *track1TotpCacheStub) ConsumeAdmin2FAApproval(_ context.Context, userID int64, digest string) (bool, error) {
	if c.approvals[userID] == digest && digest != "" {
		delete(c.approvals, userID)
		return true, nil
	}
	return false, nil
}

func (c *track1TotpCacheStub) IncrementCryptoAttempt(context.Context, string, time.Duration) (int, error) {
	return 1, nil
}

func (c *track1TotpCacheStub) SaveTwoFactorGrant(context.Context, string, *TwoFactorGrantRecord, time.Duration) error {
	return nil
}

func (c *track1TotpCacheStub) LoadTwoFactorGrant(context.Context, string) (*TwoFactorGrantRecord, error) {
	return nil, nil
}

func (c *track1TotpCacheStub) DeleteTwoFactorGrant(context.Context, string) error { return nil }

func (c *track1TotpCacheStub) RevokeTwoFactorGrantsForUser(_ context.Context, userID int64) (int64, error) {
	c.revokedUserIDs = append(c.revokedUserIDs, userID)
	return 1, nil
}

func (c *track1TotpCacheStub) IncrementTwoFactorGrantVerifyAttempt(context.Context, int64, string, time.Duration) (int64, error) {
	return 1, nil
}

func (c *track1TotpCacheStub) GetLoginSession(_ context.Context, tempToken string) (*TotpLoginSession, error) {
	s, ok := c.sessions[tempToken]
	if !ok {
		return nil, nil
	}
	cp := *s
	return &cp, nil
}

func (c *track1TotpCacheStub) SetLoginSession(_ context.Context, tempToken string, session *TotpLoginSession, _ time.Duration) error {
	cp := *session
	c.sessions[tempToken] = &cp
	return nil
}

func (c *track1TotpCacheStub) DeleteLoginSession(_ context.Context, tempToken string) error {
	delete(c.sessions, tempToken)
	return nil
}

func (c *track1TotpCacheStub) ClearVerifyAttempts(context.Context, int64) error { return nil }

func (c *track1TotpCacheStub) GetVerifyAttempts(context.Context, int64) (int, error) { return 0, nil }

func (c *track1TotpCacheStub) IncrementVerifyAttempts(context.Context, int64) (int, error) {
	return 1, nil
}

func (c *track1TotpCacheStub) MarkLoginCodeUsed(context.Context, int64, string, time.Duration) (bool, error) {
	return true, nil
}

type track1EncryptorStub struct{}

func (track1EncryptorStub) Encrypt(plaintext string) (string, error)  { return plaintext, nil }
func (track1EncryptorStub) Decrypt(ciphertext string) (string, error) { return ciphertext, nil }

type track1PersonalKeyRevokerSpy struct {
	revokedUserIDs []int64
}

func (s *track1PersonalKeyRevokerSpy) Delete(_ context.Context, userID int64) (bool, error) {
	s.revokedUserIDs = append(s.revokedUserIDs, userID)
	return true, nil
}

type track1RefreshCacheSpy struct {
	RefreshTokenCache
	revokedAllUsers []int64
	revokedBefore   map[int64]time.Time
}

func (s *track1RefreshCacheSpy) DeleteUserRefreshTokens(_ context.Context, userID int64) error {
	s.revokedAllUsers = append(s.revokedAllUsers, userID)
	return nil
}

func (s *track1RefreshCacheSpy) RevokeAccessTokensBefore(_ context.Context, userID int64, before time.Time, _ time.Duration) error {
	if s.revokedBefore == nil {
		s.revokedBefore = make(map[int64]time.Time)
	}
	s.revokedBefore[userID] = before
	return nil
}

func (s *track1RefreshCacheSpy) AccessTokensRevokedBefore(_ context.Context, userID int64) (time.Time, error) {
	return s.revokedBefore[userID], nil
}

func (s *track1RefreshCacheSpy) StoreRefreshToken(context.Context, string, *RefreshTokenData, time.Duration) error {
	return nil
}

func (s *track1RefreshCacheSpy) AddToUserTokenSet(context.Context, int64, string, time.Duration) error {
	return nil
}

func (s *track1RefreshCacheSpy) AddToFamilyTokenSet(context.Context, string, string, time.Duration) error {
	return nil
}

// Area 1: Passkey-only and dual-factor existing users MUST be blocked on ALL OAuth login paths
// with ZERO pre-2FA DB writes (no username update, no first-bind defaults).
func TestOAuthLoginBlocksPasskeyOnlyAndDualFactorWithZeroPre2FAWrites(t *testing.T) {
	ctx := context.Background()

	passkeyOnlyUser := &User{
		ID:          101,
		Email:       "passkey-only@example.com",
		Username:    "", // intentionally empty so a buggy pre-2FA username update would be caught
		Role:        RoleUser,
		Status:      StatusActive,
		TotpEnabled: false,
	}
	dualUser := &User{
		ID:          102,
		Email:       "dual-factor@example.com",
		Username:    "",
		Role:        RoleUser,
		Status:      StatusActive,
		TotpEnabled: true,
	}

	userRepo := newTrack1UserRepoStub(passkeyOnlyUser, dualUser)
	passkeyRepo := &track1PasskeyRepoStub{
		records: map[int64][]PasskeyCredentialRecord{
			101: {{ID: 1, UserID: 101}},
			102: {{ID: 2, UserID: 102}},
		},
	}
	cfg := &config.Config{
		JWT: config.JWTConfig{
			Secret:                   "test-secret",
			ExpireHour:               1,
			AccessTokenExpireMinutes: 60,
			RefreshTokenExpireDays:   7,
		},
	}
	settingSvc := NewSettingService(&track1SettingRepoStub{
		values: map[string]string{
			SettingKeyTotpEnabled:         "true",
			SettingKeyRegistrationEnabled: "true",
		},
	}, cfg)
	totpSvc := NewTotpService(userRepo, track1EncryptorStub{}, newTrack1TotpCacheStub(), settingSvc, nil, nil)
	totpSvc.SetPasskeyRepository(passkeyRepo)

	db, err := sql.Open("sqlite", "file:auth_security_track1?mode=memory&cache=shared")
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	_, err = db.Exec("PRAGMA foreign_keys = ON")
	require.NoError(t, err)
	entClient := enttest.NewClient(t, enttest.WithOptions(dbent.Driver(entsql.OpenDB(dialect.SQLite, db))))
	t.Cleanup(func() { _ = entClient.Close() })

	authSvc := NewAuthService(entClient, userRepo, nil, &track1RefreshCacheSpy{}, cfg, settingSvc, nil, nil, nil, nil, nil, nil, nil)
	authSvc.SetLoginTwoFactorPolicy(totpSvc)

	// 1. LoginOrRegisterVerifiedEmailOAuth must reject passkey-only user BEFORE updating username or writing auth_identities.
	pair, user, err := authSvc.LoginOrRegisterVerifiedEmailOAuth(ctx, EmailOAuthIdentityInput{
		ProviderType:    "github",
		ProviderKey:     "github",
		ProviderSubject: "gh-101",
		Email:           passkeyOnlyUser.Email,
		EmailVerified:   true,
		Username:        "attacker-gh-name",
	})
	require.ErrorIs(t, err, ErrLoginTOTPRequired)
	require.Nil(t, pair)
	require.NotNil(t, user)
	require.Equal(t, int64(101), user.ID)
	require.Equal(t, 0, userRepo.updateCalled, "must not mutate user before 2FA")
	require.Empty(t, passkeyOnlyUser.Username)
	identCount, countErr := entClient.AuthIdentity.Query().Where(authidentity.ProviderTypeEQ("github")).Count(ctx)
	require.NoError(t, countErr)
	require.Zero(t, identCount, "must NOT write auth_identities before 2FA")

	// 2. LoginOrRegisterOAuthWithTokenPairAndPromoCode must reject existing passkey-only & dual users.
	pair, user, err = authSvc.LoginOrRegisterOAuthWithTokenPairAndPromoCode(ctx, passkeyOnlyUser.Email, "new-name", "", "", "", "linuxdo")
	require.ErrorIs(t, err, ErrLoginTOTPRequired)
	require.Nil(t, pair)
	require.NotNil(t, user)
	require.Equal(t, 0, userRepo.updateCalled)

	pair, user, err = authSvc.LoginOrRegisterOAuthWithTokenPairAndPromoCode(ctx, dualUser.Email, "new-name", "", "", "", "oidc")
	require.ErrorIs(t, err, ErrLoginTOTPRequired)
	require.Nil(t, pair)
	require.NotNil(t, user)
	require.Equal(t, 0, userRepo.updateCalled)

	// 3. Legacy LoginOrRegisterOAuth must also reject existing passkey-only user.
	token, user, err := authSvc.LoginOrRegisterOAuth(ctx, passkeyOnlyUser.Email, "new-name")
	require.ErrorIs(t, err, ErrLoginTOTPRequired)
	require.Empty(t, token)
	require.NotNil(t, user)
	require.Equal(t, 0, userRepo.updateCalled)

	// 4. Fail-closed when passkey repository errors.
	passkeyRepo.err = errors.New("passkey db down")
	pair, user, err = authSvc.LoginOrRegisterVerifiedEmailOAuth(ctx, EmailOAuthIdentityInput{
		ProviderType:    "google",
		ProviderKey:     "google",
		ProviderSubject: "gg-101",
		Email:           passkeyOnlyUser.Email,
		EmailVerified:   true,
		Username:        "attacker-gg-name",
	})
	require.ErrorIs(t, err, ErrTwoFactorPolicyUnavailable)
	require.Nil(t, pair)
	require.Nil(t, user)
	require.Equal(t, 0, userRepo.updateCalled)
}

// Area 1: TotpService.CreateLoginSessionForFactors stores PendingOAuthBind and MarkLoginFactorPassed preserves it.
func TestTotpCreateLoginSessionForFactorsPreservesPendingOAuthBind(t *testing.T) {
	ctx := context.Background()
	cache := newTrack1TotpCacheStub()
	totpSvc := NewTotpService(nil, nil, cache, nil, nil, nil)

	bind := &PendingOAuthBindLoginSession{
		PendingSessionToken: "pending-sess-1",
		BrowserSessionKey:   "browser-key-1",
	}
	tempToken, err := totpSvc.CreateLoginSessionForFactors(
		ctx,
		55,
		"dual@example.com",
		[]Factor{FactorTOTP, FactorPasskey},
		nil,
		bind,
	)
	require.NoError(t, err)
	require.NotEmpty(t, tempToken)

	sess, err := totpSvc.GetLoginSession(ctx, tempToken)
	require.NoError(t, err)
	require.NotNil(t, sess)
	require.Equal(t, []Factor{FactorTOTP, FactorPasskey}, sess.RequiredFactors)
	require.NotNil(t, sess.PendingOAuthBind)
	require.Equal(t, "pending-sess-1", sess.PendingOAuthBind.PendingSessionToken)
	require.Equal(t, "browser-key-1", sess.PendingOAuthBind.BrowserSessionKey)

	updated, err := totpSvc.MarkLoginFactorPassed(ctx, tempToken, FactorTOTP)
	require.NoError(t, err)
	require.Equal(t, []Factor{FactorTOTP}, updated.PassedFactors)
	require.Equal(t, []Factor{FactorPasskey}, LoginSessionMissingFactors(updated))
	require.NotNil(t, updated.PendingOAuthBind)
	require.Equal(t, "pending-sess-1", updated.PendingOAuthBind.PendingSessionToken)
}

// Area 3: OIDC outbound URL validation, discovery metadata SSRF blocking, and body size cap.
func TestOIDCOutboundSSRFValidationAndBodyCap(t *testing.T) {
	ctx := context.Background()

	// Ensure private-target override is OFF (production behavior).
	t.Cleanup(SetAdminOutboundAllowPrivateForTest(false))

	for _, blocked := range []string{
		"http://127.0.0.1:8080/token",
		"http://169.254.169.254/latest/meta-data/",
		"http://[::1]:8080/jwks",
		"http://10.0.0.1/userinfo",
		"file:///etc/passwd",
		"gopher://127.0.0.1:6379/_INFO",
	} {
		_, err := ValidateOIDCOutboundURL(ctx, blocked)
		require.Error(t, err, "expected SSRF target %s to be blocked", blocked)
	}

	// Valid public HTTPS URL succeeds when DNS resolves to a public IP.
	publicCtx := urlvalidator.WithResolver(ctx, func(_ context.Context, _ string) ([]netip.Addr, error) {
		return []netip.Addr{netip.MustParseAddr("93.184.216.34")}, nil
	})
	validated, err := ValidateOIDCOutboundURL(publicCtx, "https://issuer.example.com/.well-known/openid-configuration")
	require.NoError(t, err)
	require.Equal(t, "https://issuer.example.com/.well-known/openid-configuration", validated)

	// Malicious discovery JSON pointing token_endpoint to loopback must be rejected.
	t.Run("discovery metadata pointing to private IP is rejected", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{
				"issuer": "https://issuer.example.com",
				"authorization_endpoint": "https://issuer.example.com/authorize",
				"token_endpoint": "http://127.0.0.1:6379/token",
				"userinfo_endpoint": "https://issuer.example.com/userinfo",
				"jwks_uri": "http://169.254.169.254/latest/meta-data/"
			}`))
		}))
		defer srv.Close()

		// Without allow-private, even hitting the loopback discovery server is blocked!
		_, err := oidcResolveProviderMetadata(ctx, srv.URL+"/.well-known/openid-configuration")
		require.Error(t, err)
	})

	// Oversized OIDC response body (> 1 MiB) is rejected.
	maxLen := int(OIDCMaxResponseBodyBytes)
	oversized := bytes.Repeat([]byte("a"), maxLen+1)
	_, err = ReadOIDCResponseBody(bytes.NewReader(oversized))
	require.Error(t, err)

	exact := bytes.Repeat([]byte("b"), maxLen)
	got, err := ReadOIDCResponseBody(bytes.NewReader(exact))
	require.NoError(t, err)
	require.Len(t, got, maxLen)
}

// Area 4: uak_ personal API key must be revoked on RevokeAllUserTokens, TotpService.Disable,
// TotpService.DisableWithServerAuthorization, and RevokeTwoFactorGrantsForUser (admin 2FA reset).
func TestPersonalAPIKeyRevokedOnAllSecurityEvents(t *testing.T) {
	ctx := context.Background()

	secret := "JBSWY3DPEHPK3PXP"
	encSecret := secret
	user := &User{
		ID:                  42,
		Email:               "uak-owner@example.com",
		Role:                RoleUser,
		Status:              StatusActive,
		TotpEnabled:         true,
		TotpSecretEncrypted: &encSecret,
	}
	userRepo := newTrack1UserRepoStub(user)
	revoker := &track1PersonalKeyRevokerSpy{}
	refreshSpy := &track1RefreshCacheSpy{}
	totpCache := newTrack1TotpCacheStub()

	cfg := &config.Config{
		JWT: config.JWTConfig{
			Secret:                   "test-secret",
			ExpireHour:               1,
			AccessTokenExpireMinutes: 60,
			RefreshTokenExpireDays:   7,
		},
	}
	authSvc := NewAuthService(nil, userRepo, nil, refreshSpy, cfg, nil, nil, nil, nil, nil, nil, nil, nil)
	authSvc.SetPersonalAPIKeyRevoker(revoker)

	// 1. RevokeAllUserTokens revokes uak_.
	require.NoError(t, authSvc.RevokeAllUserTokens(ctx, user.ID))
	require.Equal(t, []int64{42}, revoker.revokedUserIDs)

	// 2. TotpService.Disable revokes uak_.
	totpSvc := NewTotpService(userRepo, track1EncryptorStub{}, totpCache, nil, nil, nil)
	totpSvc.SetPersonalAPIKeyRevoker(revoker)

	code, err := totp.GenerateCode(secret, time.Now())
	require.NoError(t, err)
	require.NoError(t, totpSvc.Disable(ctx, user.ID, "", "", code))
	require.Len(t, revoker.revokedUserIDs, 2)
	require.Equal(t, int64(42), revoker.revokedUserIDs[1])

	// 3. TotpService.DisableWithServerAuthorization revokes uak_.
	user.Role = RoleAdmin
	user.TotpEnabled = true
	user.TotpSecretEncrypted = &encSecret
	approvalSvc := NewAdmin2FAApprovalService(userRepo, totpCache)
	approvalCode, _, err := approvalSvc.Issue(ctx, user.ID)
	require.NoError(t, err)
	require.NoError(t, totpSvc.DisableWithServerAuthorization(ctx, user.ID, approvalCode))
	require.Len(t, revoker.revokedUserIDs, 3)

	// 4. RevokeTwoFactorGrantsForUser (used by Admin2FAResetService.AdminResetUserTOTP and ResetPassword) revokes uak_.
	require.NoError(t, totpSvc.RevokeTwoFactorGrantsForUser(ctx, user.ID))
	require.Len(t, revoker.revokedUserIDs, 4)
	require.Contains(t, totpCache.revokedUserIDs, user.ID)
}
