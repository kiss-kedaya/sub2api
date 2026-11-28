package handler

import (
	"bytes"
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/ent/authidentity"
	"github.com/Wei-Shaw/sub2api/internal/config"
	servermiddleware "github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/go-webauthn/webauthn/protocol/webauthncbor"
	"github.com/go-webauthn/webauthn/protocol/webauthncose"
	"github.com/go-webauthn/webauthn/webauthn"
	"github.com/pquerna/otp/totp"
	"github.com/stretchr/testify/require"
)

type track1PasskeyFixture struct {
	privKey    *ecdsa.PrivateKey
	credID     []byte
	userHandle []byte
	rpID       string
	origin     string
	repo       *track1RealPasskeyRepo
	svc        *service.PasskeyService
}

type track1RealPasskeyRepo struct {
	service.PasskeyRepository
	records []service.PasskeyCredentialRecord
	handle  []byte
}

func (r *track1RealPasskeyRepo) ListByUserID(context.Context, int64) ([]service.PasskeyCredentialRecord, error) {
	return r.records, nil
}

func (r *track1RealPasskeyRepo) GetUserHandle(context.Context, int64) ([]byte, error) {
	return r.handle, nil
}

func (r *track1RealPasskeyRepo) UpdateUsage(context.Context, int64, uint32, bool, bool, time.Time) error {
	return nil
}

func (r *track1RealPasskeyRepo) UpdateCredential(context.Context, int64, *webauthn.Credential, time.Time) error {
	return nil
}

type track1UserRepoAdapter struct {
	service.UserRepository
	userSvc *service.UserService
}

func (a *track1UserRepoAdapter) GetByID(ctx context.Context, id int64) (*service.User, error) {
	return a.userSvc.GetByID(ctx, id)
}

func newTrack1RealPasskeyFixture(t *testing.T, userSvc *service.UserService, userID int64) *track1PasskeyFixture {
	t.Helper()
	priv, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	require.NoError(t, err)

	xBytes := make([]byte, 32)
	yBytes := make([]byte, 32)
	priv.X.FillBytes(xBytes)
	priv.Y.FillBytes(yBytes)

	coseKey := webauthncose.EC2PublicKeyData{
		PublicKeyData: webauthncose.PublicKeyData{
			KeyType:   int64(webauthncose.EllipticKey),
			Algorithm: int64(webauthncose.AlgES256),
		},
		Curve:  int64(webauthncose.P256),
		XCoord: xBytes,
		YCoord: yBytes,
	}
	pubBytes, err := webauthncbor.Marshal(coseKey)
	require.NoError(t, err)

	credID := []byte("track1-passkey-cred-id-0123456789")
	userHandle := []byte("track1-user-handle-0123456789abc")
	rpID := "example.test"
	origin := "https://example.test"

	repo := &track1RealPasskeyRepo{
		handle: userHandle,
		records: []service.PasskeyCredentialRecord{{
			ID:         1,
			UserID:     userID,
			UserHandle: userHandle,
			Credential: webauthn.Credential{
				ID:        credID,
				PublicKey: pubBytes,
			},
		}},
	}
	cfg := &config.Config{WebAuthn: config.WebAuthnConfig{
		Enabled:       true,
		RPDisplayName: "Sub2API",
		RPID:          rpID,
		RPOrigins:     []string{origin},
	}}
	svc, err := service.NewPasskeyService(cfg, repo, newFactorLoginSessionStore(), &track1UserRepoAdapter{userSvc: userSvc})
	require.NoError(t, err)
	return &track1PasskeyFixture{
		privKey:    priv,
		credID:     credID,
		userHandle: userHandle,
		rpID:       rpID,
		origin:     origin,
		repo:       repo,
		svc:        svc,
	}
}

func (f *track1PasskeyFixture) signAssertionJSON(t *testing.T, rawOptions any) json.RawMessage {
	t.Helper()
	optBytes, err := json.Marshal(rawOptions)
	require.NoError(t, err)
	var envelope struct {
		PublicKey struct {
			Challenge string `json:"challenge"`
		} `json:"publicKey"`
	}
	require.NoError(t, json.Unmarshal(optBytes, &envelope))
	require.NotEmpty(t, envelope.PublicKey.Challenge)

	clientDataJSON, err := json.Marshal(map[string]any{
		"type":      "webauthn.get",
		"challenge": envelope.PublicKey.Challenge,
		"origin":    f.origin,
	})
	require.NoError(t, err)

	rpHash := sha256.Sum256([]byte(f.rpID))
	authData := make([]byte, 37)
	copy(authData[:32], rpHash[:])
	authData[32] = 0x05 // UP (0x01) | UV (0x04)
	binary.BigEndian.PutUint32(authData[33:37], 1)

	clientHash := sha256.Sum256(clientDataJSON)
	signedPayload := append(append([]byte{}, authData...), clientHash[:]...)
	digest := sha256.Sum256(signedPayload)
	sig, err := ecdsa.SignASN1(rand.Reader, f.privKey, digest[:])
	require.NoError(t, err)

	credIDB64 := base64.RawURLEncoding.EncodeToString(f.credID)
	assertion := map[string]any{
		"id":    credIDB64,
		"rawId": credIDB64,
		"type":  "public-key",
		"response": map[string]any{
			"authenticatorData": base64.RawURLEncoding.EncodeToString(authData),
			"clientDataJSON":    base64.RawURLEncoding.EncodeToString(clientDataJSON),
			"signature":         base64.RawURLEncoding.EncodeToString(sig),
			"userHandle":        base64.RawURLEncoding.EncodeToString(f.userHandle),
		},
	}
	raw, err := json.Marshal(assertion)
	require.NoError(t, err)
	return raw
}

type track1StatefulRefreshCache struct {
	oauthPendingFlowRefreshTokenCacheStub
	revokedBefore map[int64]time.Time
	oauthCodes    map[string]*service.OAuthLoginCodeData
}

func (c *track1StatefulRefreshCache) RevokeAccessTokensBefore(_ context.Context, userID int64, before time.Time, _ time.Duration) error {
	if c.revokedBefore == nil {
		c.revokedBefore = make(map[int64]time.Time)
	}
	c.revokedBefore[userID] = before
	return nil
}

func (c *track1StatefulRefreshCache) AccessTokensRevokedBefore(_ context.Context, userID int64) (time.Time, error) {
	return c.revokedBefore[userID], nil
}

func (c *track1StatefulRefreshCache) StoreOAuthLoginCode(_ context.Context, code string, data *service.OAuthLoginCodeData, _ time.Duration) error {
	if c.oauthCodes == nil {
		c.oauthCodes = make(map[string]*service.OAuthLoginCodeData)
	}
	cp := *data
	c.oauthCodes[code] = &cp
	return nil
}

func (c *track1StatefulRefreshCache) ConsumeOAuthLoginCode(_ context.Context, code string) (*service.OAuthLoginCodeData, error) {
	if c.oauthCodes == nil {
		return nil, service.ErrInvalidToken
	}
	d, ok := c.oauthCodes[code]
	if !ok {
		return nil, service.ErrInvalidToken
	}
	delete(c.oauthCodes, code)
	return d, nil
}

type track1StatefulTotpCache struct {
	oauthPendingFlowTotpCacheStub
	stepUpGrants      map[string]bool
	revokedGrantUsers []int64
}

func (s *track1StatefulTotpCache) SaveTwoFactorGrant(context.Context, string, *service.TwoFactorGrantRecord, time.Duration) error {
	return nil
}

func (s *track1StatefulTotpCache) LoadTwoFactorGrant(context.Context, string) (*service.TwoFactorGrantRecord, error) {
	return nil, nil
}

func (s *track1StatefulTotpCache) DeleteTwoFactorGrant(context.Context, string) error { return nil }

func (s *track1StatefulTotpCache) RevokeTwoFactorGrantsForUser(_ context.Context, userID int64) (int64, error) {
	s.revokedGrantUsers = append(s.revokedGrantUsers, userID)
	return 1, nil
}

func (s *track1StatefulTotpCache) IncrementTwoFactorGrantVerifyAttempt(context.Context, int64, string, time.Duration) (int64, error) {
	return 1, nil
}

func (s *track1StatefulTotpCache) grantKey(userID int64, sessionKey string, factor service.Factor) string {
	return sessionKey + ":" + factor.String() + ":" + string(rune(userID))
}

func (s *track1StatefulTotpCache) SetStepUpGrant(_ context.Context, userID int64, sessionKey string, factor service.Factor, _ time.Duration) error {
	if s.stepUpGrants == nil {
		s.stepUpGrants = make(map[string]bool)
	}
	s.stepUpGrants[s.grantKey(userID, sessionKey, factor)] = true
	return nil
}

func (s *track1StatefulTotpCache) HasStepUpGrant(_ context.Context, userID int64, sessionKey string, factor service.Factor) (bool, error) {
	return s.stepUpGrants[s.grantKey(userID, sessionKey, factor)], nil
}

type track1PersonalKeyRevokerSpy struct {
	revokedUserIDs []int64
}

func (s *track1PersonalKeyRevokerSpy) Delete(_ context.Context, userID int64) (bool, error) {
	s.revokedUserIDs = append(s.revokedUserIDs, userID)
	return true, nil
}

// Area 1: Existing Passkey-only user logging in via GitHub verified-email OAuth must:
// 1) write ZERO auth_identities and ZERO username/default mutations before 2FA,
// 2) route through pending session -> /pending/exchange returning next_factor="passkey" + passkey_options,
// 3) bind auth_identities + consume pending session + issue JWT ONLY after real Passkey assertion succeeds.
func TestEmailOAuthPasskeyOnlyZeroPre2FAWritesAndCompletesViaPasskeyFinish(t *testing.T) {
	gin.SetMode(gin.TestMode)
	ctx := context.Background()

	totpCache := &track1StatefulTotpCache{}
	handler, client := newOAuthPendingFlowTestHandlerWithDependencies(t, oauthPendingFlowTestHandlerOptions{
		settingValues: map[string]string{
			service.SettingKeyTotpEnabled:                             "true",
			service.SettingKeyAuthSourceDefaultGitHubBalance:          "25",
			service.SettingKeyAuthSourceDefaultGitHubGrantOnFirstBind: "true",
		},
		totpCache:     totpCache,
		totpEncryptor: oauthPendingFlowTotpEncryptorStub{},
	})
	t.Cleanup(func() { _ = client.Close() })

	userEntity, err := client.User.Create().
		SetEmail("pk-oauth@example.com").
		SetUsername("").
		SetPasswordHash("hash").
		SetRole(service.RoleUser).
		SetStatus(service.StatusActive).
		SetBalance(1.0).
		SetTotpEnabled(false).
		Save(ctx)
	require.NoError(t, err)

	pk := newTrack1RealPasskeyFixture(t, handler.userService, userEntity.ID)
	handler.totpService.SetPasskeyRepository(pk.repo)
	handler.SetPasskeyService(pk.svc)

	// Step 1: OAuth callback with verified email matching existing passkey-only user.
	cbRec := httptest.NewRecorder()
	cbCtx, _ := gin.CreateTestContext(cbRec)
	cbCtx.Request = httptest.NewRequest(http.MethodGet, "/api/v1/auth/oauth/github/callback", nil)

	handler.emailOAuthCallbackWithProfile(cbCtx, "github", config.EmailOAuthProviderConfig{
		Enabled:             true,
		FrontendRedirectURL: "/auth/oauth/callback",
	}, "/auth/oauth/callback", "/dashboard", &emailOAuthProfile{
		Subject:       "gh-pk-only-sub",
		Email:         "pk-oauth@example.com",
		EmailVerified: true,
		Username:      "attacker-gh-username",
	})

	require.Equal(t, http.StatusFound, cbRec.Code)
	require.Equal(t, "/auth/oauth/callback", cbRec.Header().Get("Location"))
	require.NotContains(t, cbRec.Header().Get("Location"), "access_token")

	// Assert ZERO pre-2FA DB writes!
	identCount, err := client.AuthIdentity.Query().Where(authidentity.UserIDEQ(userEntity.ID)).Count(ctx)
	require.NoError(t, err)
	require.Zero(t, identCount, "auth_identities must NOT be written before Passkey 2FA")
	reloadedBefore, err := client.User.Get(ctx, userEntity.ID)
	require.NoError(t, err)
	require.Empty(t, reloadedBefore.Username, "username must NOT be mutated before Passkey 2FA")
	require.InDelta(t, 1.0, reloadedBefore.Balance, 0.0001, "first-bind balance must NOT be granted before Passkey 2FA")

	// Step 2: Frontend exchanges pending session -> gets Passkey challenge (no tokens yet!).
	sessCookie := findCookie(cbRec.Result().Cookies(), oauthPendingSessionCookieName)
	browserCookie := findCookie(cbRec.Result().Cookies(), oauthPendingBrowserCookieName)
	require.NotNil(t, sessCookie)
	require.NotNil(t, browserCookie)

	exRec := httptest.NewRecorder()
	exCtx, _ := gin.CreateTestContext(exRec)
	exReq := httptest.NewRequest(http.MethodPost, "/api/v1/auth/oauth/pending/exchange", nil)
	exReq.AddCookie(sessCookie)
	exReq.AddCookie(browserCookie)
	exCtx.Request = exReq

	handler.ExchangePendingOAuthCompletion(exCtx)

	require.Equal(t, http.StatusOK, exRec.Code, exRec.Body.String())
	exData := decodeJSONResponseData(t, exRec)
	require.Equal(t, true, exData["requires_2fa"])
	require.Equal(t, "passkey", exData["next_factor"])
	require.NotContains(t, exData, "access_token")
	tempToken, _ := exData["temp_token"].(string)
	pkSessionToken, _ := exData["passkey_session_token"].(string)
	require.NotEmpty(t, tempToken)
	require.NotEmpty(t, pkSessionToken)
	require.NotNil(t, exData["passkey_options"])

	// PendingAuthSession must still be unconsumed before Passkey assertion completes.
	pendingRow, err := client.PendingAuthSession.Query().Only(ctx)
	require.NoError(t, err)
	require.Nil(t, pendingRow.ConsumedAt)

	// Step 3: Complete Passkey assertion -> binds identity, consumes pending session, issues JWT!
	assertionJSON := pk.signAssertionJSON(t, exData["passkey_options"])
	finishBody, err := json.Marshal(map[string]any{
		"temp_token":    tempToken,
		"session_token": pkSessionToken,
		"credential":    assertionJSON,
	})
	require.NoError(t, err)

	finRec := httptest.NewRecorder()
	finCtx, _ := gin.CreateTestContext(finRec)
	finReq := httptest.NewRequest(http.MethodPost, "/api/v1/auth/login/2fa/passkey/finish", bytes.NewReader(finishBody))
	finReq.Header.Set("Content-Type", "application/json")
	finCtx.Request = finReq

	handler.Login2FAPasskeyFinish(finCtx)

	require.Equal(t, http.StatusOK, finRec.Code, finRec.Body.String())
	finData := decodeJSONResponseData(t, finRec)
	require.NotEmpty(t, finData["access_token"])

	// Now GitHub identity MUST be bound and pending session MUST be consumed!
	identCountAfter, err := client.AuthIdentity.Query().Where(
		authidentity.UserIDEQ(userEntity.ID),
		authidentity.ProviderTypeEQ("github"),
	).Count(ctx)
	require.NoError(t, err)
	require.Equal(t, 1, identCountAfter)
	pendingAfter, err := client.PendingAuthSession.Get(ctx, pendingRow.ID)
	require.NoError(t, err)
	require.NotNil(t, pendingAfter.ConsumedAt)
}

// Area 1: Dual-factor (TOTP + Passkey) PendingOAuthBind must require BOTH factors in sequence;
// passing TOTP alone MUST NOT bind auth_identities or consume PendingAuthSession,
// and jumping straight to Passkey finish without TOTP MUST fail closed (403).
func TestOAuthBindLoginDualFactorRequiresBothTOTPAndPasskeyBeforeBinding(t *testing.T) {
	gin.SetMode(gin.TestMode)
	ctx := context.Background()

	totpCache := &track1StatefulTotpCache{}
	handler, client := newOAuthPendingFlowTestHandlerWithDependencies(t, oauthPendingFlowTestHandlerOptions{
		settingValues: map[string]string{
			service.SettingKeyTotpEnabled: "true",
		},
		totpCache:     totpCache,
		totpEncryptor: oauthPendingFlowTotpEncryptorStub{},
	})
	t.Cleanup(func() { _ = client.Close() })

	pwHash, err := handler.authService.HashPassword("secret-123")
	require.NoError(t, err)
	totpSecret := "JBSWY3DPEHPK3PXP"
	userEntity, err := client.User.Create().
		SetEmail("dual-bind@example.com").
		SetUsername("dual-owner").
		SetPasswordHash(pwHash).
		SetRole(service.RoleUser).
		SetStatus(service.StatusActive).
		SetTotpEnabled(true).
		SetTotpSecretEncrypted(totpSecret).
		SetTotpEnabledAt(time.Now().UTC().Add(-time.Hour)).
		Save(ctx)
	require.NoError(t, err)

	pk := newTrack1RealPasskeyFixture(t, handler.userService, userEntity.ID)
	handler.totpService.SetPasskeyRepository(pk.repo)
	handler.SetPasskeyService(pk.svc)

	pendingSess, err := client.PendingAuthSession.Create().
		SetSessionToken("dual-bind-pending-token").
		SetIntent("adopt_existing_user_by_email").
		SetProviderType("oidc").
		SetProviderKey("https://issuer.example").
		SetProviderSubject("oidc-dual-sub").
		SetTargetUserID(userEntity.ID).
		SetResolvedEmail(userEntity.Email).
		SetBrowserSessionKey("dual-bind-browser-key").
		SetRedirectTo("/dashboard").
		SetExpiresAt(time.Now().UTC().Add(10 * time.Minute)).
		Save(ctx)
	require.NoError(t, err)

	// Step 1: bind-login returns TOTP challenge first with required_factors=["totp","passkey"].
	bindRec := httptest.NewRecorder()
	bindCtx, _ := gin.CreateTestContext(bindRec)
	bindReq := httptest.NewRequest(http.MethodPost, "/api/v1/auth/oauth/oidc/bind-login",
		strings.NewReader(`{"email":"dual-bind@example.com","password":"secret-123"}`))
	bindReq.Header.Set("Content-Type", "application/json")
	bindReq.AddCookie(&http.Cookie{Name: oauthPendingSessionCookieName, Value: encodeCookieValue(pendingSess.SessionToken)})
	bindReq.AddCookie(&http.Cookie{Name: oauthPendingBrowserCookieName, Value: encodeCookieValue(pendingSess.BrowserSessionKey)})
	bindCtx.Request = bindReq

	handler.BindOIDCOAuthLogin(bindCtx)

	require.Equal(t, http.StatusOK, bindRec.Code, bindRec.Body.String())
	bindData := decodeJSONResponseData(t, bindRec)
	require.Equal(t, true, bindData["requires_2fa"])
	require.Equal(t, "totp", bindData["next_factor"])
	require.Equal(t, []any{"totp", "passkey"}, bindData["required_factors"])
	tempToken, _ := bindData["temp_token"].(string)
	require.NotEmpty(t, tempToken)

	// Step 2: Submit TOTP code -> must progress to next_factor="passkey", STILL NO token, NO bind, NO consume!
	code, err := totp.GenerateCode(totpSecret, time.Now())
	require.NoError(t, err)
	totpBody, _ := json.Marshal(map[string]string{
		"temp_token": tempToken,
		"totp_code":  code,
	})
	totpRec := httptest.NewRecorder()
	totpCtx, _ := gin.CreateTestContext(totpRec)
	totpReq := httptest.NewRequest(http.MethodPost, "/api/v1/auth/login/2fa", bytes.NewReader(totpBody))
	totpReq.Header.Set("Content-Type", "application/json")
	totpCtx.Request = totpReq

	handler.Login2FA(totpCtx)

	require.Equal(t, http.StatusOK, totpRec.Code, totpRec.Body.String())
	totpData := decodeJSONResponseData(t, totpRec)
	require.Equal(t, true, totpData["requires_2fa"])
	require.Equal(t, "passkey", totpData["next_factor"])
	require.Equal(t, []any{"totp"}, totpData["passed_factors"])
	require.NotContains(t, totpData, "access_token")
	pkSessToken, _ := totpData["passkey_session_token"].(string)
	require.NotEmpty(t, pkSessToken)

	// Verify ZERO binding and pending session still unconsumed after TOTP alone!
	identCountMid, err := client.AuthIdentity.Query().Where(authidentity.UserIDEQ(userEntity.ID)).Count(ctx)
	require.NoError(t, err)
	require.Zero(t, identCountMid, "must NOT bind after TOTP alone when Passkey is still pending")
	pendingMid, err := client.PendingAuthSession.Get(ctx, pendingSess.ID)
	require.NoError(t, err)
	require.Nil(t, pendingMid.ConsumedAt, "pending session must remain unconsumed until Passkey completes")

	// Step 3: Complete Passkey assertion -> NOW binds, consumes session, and issues JWT!
	assertionJSON := pk.signAssertionJSON(t, totpData["passkey_options"])
	finBody, _ := json.Marshal(map[string]any{
		"temp_token":    tempToken,
		"session_token": pkSessToken,
		"credential":    assertionJSON,
	})
	finRec := httptest.NewRecorder()
	finCtx, _ := gin.CreateTestContext(finRec)
	finReq := httptest.NewRequest(http.MethodPost, "/api/v1/auth/login/2fa/passkey/finish", bytes.NewReader(finBody))
	finReq.Header.Set("Content-Type", "application/json")
	finCtx.Request = finReq

	handler.Login2FAPasskeyFinish(finCtx)

	require.Equal(t, http.StatusOK, finRec.Code, finRec.Body.String())
	finData := decodeJSONResponseData(t, finRec)
	require.NotEmpty(t, finData["access_token"])

	identCountEnd, err := client.AuthIdentity.Query().Where(
		authidentity.UserIDEQ(userEntity.ID),
		authidentity.ProviderTypeEQ("oidc"),
	).Count(ctx)
	require.NoError(t, err)
	require.Equal(t, 1, identCountEnd)
	pendingEnd, err := client.PendingAuthSession.Get(ctx, pendingSess.ID)
	require.NoError(t, err)
	require.NotNil(t, pendingEnd.ConsumedAt)
}

// Area 1: Fail-closed 503 when totpService == nil or settingSvc == nil in pending exchange & bind-login.
func TestOAuthPendingExchangeAndBindLoginFailClosedWithoutTwoFactorDeps(t *testing.T) {
	gin.SetMode(gin.TestMode)
	ctx := context.Background()

	handler, client := newOAuthPendingFlowTestHandlerWithDependencies(t, oauthPendingFlowTestHandlerOptions{})
	t.Cleanup(func() { _ = client.Close() })

	pwHash, err := handler.authService.HashPassword("secret-123")
	require.NoError(t, err)
	userEntity, err := client.User.Create().
		SetEmail("failclosed@example.com").
		SetUsername("failclosed").
		SetPasswordHash(pwHash).
		SetRole(service.RoleUser).
		SetStatus(service.StatusActive).
		Save(ctx)
	require.NoError(t, err)

	_, err = client.PendingAuthSession.Create().
		SetSessionToken("fc-exchange-token").
		SetIntent("login").
		SetProviderType("linuxdo").
		SetProviderKey("linuxdo").
		SetProviderSubject("fc-sub").
		SetTargetUserID(userEntity.ID).
		SetResolvedEmail(userEntity.Email).
		SetBrowserSessionKey("fc-browser-key").
		SetLocalFlowState(map[string]any{
			oauthCompletionResponseKey: map[string]any{"redirect": "/dashboard"},
		}).
		SetExpiresAt(time.Now().UTC().Add(10 * time.Minute)).
		Save(ctx)
	require.NoError(t, err)

	origTotp := handler.totpService
	handler.totpService = nil

	// Exchange fails closed with 503.
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/oauth/pending/exchange", nil)
	req.AddCookie(&http.Cookie{Name: oauthPendingSessionCookieName, Value: encodeCookieValue("fc-exchange-token")})
	req.AddCookie(&http.Cookie{Name: oauthPendingBrowserCookieName, Value: encodeCookieValue("fc-browser-key")})
	c.Request = req

	handler.ExchangePendingOAuthCompletion(c)
	require.Equal(t, http.StatusServiceUnavailable, rec.Code)
	require.NotContains(t, rec.Body.String(), "access_token")

	// bind-login also fails closed with 503 when settingSvc == nil.
	handler.totpService = origTotp
	handler.settingSvc = nil
	rec2 := httptest.NewRecorder()
	c2, _ := gin.CreateTestContext(rec2)
	req2 := httptest.NewRequest(http.MethodPost, "/api/v1/auth/oauth/pending/bind-login",
		strings.NewReader(`{"email":"failclosed@example.com","password":"secret-123"}`))
	req2.Header.Set("Content-Type", "application/json")
	req2.AddCookie(&http.Cookie{Name: oauthPendingSessionCookieName, Value: encodeCookieValue("fc-exchange-token")})
	req2.AddCookie(&http.Cookie{Name: oauthPendingBrowserCookieName, Value: encodeCookieValue("fc-browser-key")})
	c2.Request = req2

	handler.BindPendingOAuthLogin(c2)
	require.Equal(t, http.StatusServiceUnavailable, rec2.Code)
	require.NotContains(t, rec2.Body.String(), "access_token")
}

// Area 1: bind-login enforces unified IP+email rate limit across providers and enforces Turnstile captcha when enabled.
func TestBindLoginEnforcesUnifiedRateLimitAndCaptcha(t *testing.T) {
	gin.SetMode(gin.TestMode)
	ctx := context.Background()

	handler, client := newOAuthPendingFlowTestHandlerWithDependencies(t, oauthPendingFlowTestHandlerOptions{
		settingValues: map[string]string{
			service.SettingKeyTurnstileEnabled:   "true",
			service.SettingKeyTurnstileSiteKey:   "site-key",
			service.SettingKeyTurnstileSecretKey: "secret-key",
		},
	})
	t.Cleanup(func() { _ = client.Close() })

	_, err := client.PendingAuthSession.Create().
		SetSessionToken("rl-sess-token").
		SetIntent("adopt_existing_user_by_email").
		SetProviderType("oidc").
		SetProviderKey("https://issuer.example").
		SetProviderSubject("rl-sub").
		SetResolvedEmail("victim@example.com").
		SetBrowserSessionKey("rl-browser-key").
		SetExpiresAt(time.Now().UTC().Add(10 * time.Minute)).
		Save(ctx)
	require.NoError(t, err)

	var seenKeys []string
	count := 0
	handler.SetBindLoginRateLimiter(func(_ context.Context, key string, limit int, _ time.Duration) (bool, time.Duration, error) {
		seenKeys = append(seenKeys, key)
		count++
		if count > 1 {
			return false, 30 * time.Second, nil
		}
		return true, 0, nil
	})

	// First call: passes rate limit, fails closed in VerifyCaptcha before checking password!
	rec1 := httptest.NewRecorder()
	c1, _ := gin.CreateTestContext(rec1)
	req1 := httptest.NewRequest(http.MethodPost, "/api/v1/auth/oauth/oidc/bind-login",
		strings.NewReader(`{"email":"Victim@Example.com","password":"wrong"}`))
	req1.Header.Set("Content-Type", "application/json")
	req1.RemoteAddr = "198.51.100.10:1234"
	req1.AddCookie(&http.Cookie{Name: oauthPendingSessionCookieName, Value: encodeCookieValue("rl-sess-token")})
	req1.AddCookie(&http.Cookie{Name: oauthPendingBrowserCookieName, Value: encodeCookieValue("rl-browser-key")})
	c1.Request = req1

	handler.BindOIDCOAuthLogin(c1)
	require.NotEqual(t, http.StatusUnauthorized, rec1.Code, "captcha check must run before credential check")
	require.Contains(t, rec1.Body.String(), "TURNSTILE", "captcha enforcement must trigger when Turnstile is enabled")

	// Second call via pending endpoint: hits SAME normalized IP+email bucket -> 429!
	rec2 := httptest.NewRecorder()
	c2, _ := gin.CreateTestContext(rec2)
	req2 := httptest.NewRequest(http.MethodPost, "/api/v1/auth/oauth/pending/bind-login",
		strings.NewReader(`{"email":"victim@example.com","password":"wrong"}`))
	req2.Header.Set("Content-Type", "application/json")
	req2.RemoteAddr = "198.51.100.10:9999"
	req2.AddCookie(&http.Cookie{Name: oauthPendingSessionCookieName, Value: encodeCookieValue("rl-sess-token")})
	req2.AddCookie(&http.Cookie{Name: oauthPendingBrowserCookieName, Value: encodeCookieValue("rl-browser-key")})
	c2.Request = req2

	handler.BindPendingOAuthLogin(c2)
	require.Equal(t, http.StatusTooManyRequests, rec2.Code)
	require.Len(t, seenKeys, 2)
	require.Equal(t, seenKeys[0], seenKeys[1], "rate-limit key must be identical across providers and email case")
}

// Area 1: tryOIDCVerifiedEmailFastPath emits one-time #code=... (no raw tokens) and blocks non-admin in backend mode.
func TestOIDCVerifiedEmailFastPathUsesOneTimeCodeAndChecksBackendMode(t *testing.T) {
	gin.SetMode(gin.TestMode)
	ctx := context.Background()

	refreshCache := &track1StatefulRefreshCache{}
	handler, client := newOAuthPendingFlowTestHandlerWithDependencies(t, oauthPendingFlowTestHandlerOptions{
		refreshTokenCache: refreshCache,
		settingValues: map[string]string{
			service.SettingKeyRegistrationEnabled: "true",
		},
	})
	t.Cleanup(func() { _ = client.Close() })
	handler.cfg = &config.Config{
		Security: config.SecurityConfig{
			OAuthCodeExchangeEnabled: true,
		},
	}

	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodGet, "/api/v1/auth/oauth/oidc/callback", nil)

	handled := handler.tryOIDCVerifiedEmailFastPath(
		c,
		"/auth/oidc/callback",
		"/dashboard",
		service.PendingAuthIdentityKey{ProviderType: "oidc", ProviderKey: "https://issuer.example", ProviderSubject: "sub-code-1"},
		"fastpath@example.com",
		"fastuser",
		map[string]any{"email": "fastpath@example.com"},
	)
	require.True(t, handled)
	require.Equal(t, http.StatusFound, rec.Code)

	loc := rec.Header().Get("Location")
	parsed, err := url.Parse(loc)
	require.NoError(t, err)
	frag, err := url.ParseQuery(parsed.Fragment)
	require.NoError(t, err)
	require.NotEmpty(t, frag.Get("code"), "must emit one-time code when code exchange is enabled")
	require.Empty(t, frag.Get("access_token"), "must NOT leak access_token in URL fragment")
	require.Empty(t, frag.Get("refresh_token"), "must NOT leak refresh_token in URL fragment")

	// Backend mode blocks non-admin user.
	handler2, client2 := newOAuthPendingFlowTestHandlerWithDependencies(t, oauthPendingFlowTestHandlerOptions{
		refreshTokenCache: &track1StatefulRefreshCache{},
		settingValues: map[string]string{
			service.SettingKeyRegistrationEnabled: "true",
			service.SettingKeyBackendModeEnabled:  "true",
		},
	})
	t.Cleanup(func() { _ = client2.Close() })
	_ = ctx

	rec2 := httptest.NewRecorder()
	c2, _ := gin.CreateTestContext(rec2)
	c2.Request = httptest.NewRequest(http.MethodGet, "/api/v1/auth/oauth/oidc/callback", nil)

	handled2 := handler2.tryOIDCVerifiedEmailFastPath(
		c2,
		"/auth/oidc/callback",
		"/dashboard",
		service.PendingAuthIdentityKey{ProviderType: "oidc", ProviderKey: "https://issuer.example", ProviderSubject: "sub-code-2"},
		"blocked@example.com",
		"blockeduser",
		map[string]any{"email": "blocked@example.com"},
	)
	require.True(t, handled2)
	require.Equal(t, http.StatusFound, rec2.Code)
	require.Contains(t, rec2.Header().Get("Location"), "error=login_blocked")
}

// Area 2: OAuth bind/start & bind-token & pending/exchange (bind_current_user) enforce
// token revocation, session binding, 2FA step-up, and in-flight revocation checks.
func TestOAuthBindEnforcesRevocationSessionBindingStepUpAndInFlightInvalidation(t *testing.T) {
	gin.SetMode(gin.TestMode)
	ctx := context.Background()

	refreshCache := &track1StatefulRefreshCache{}
	totpCache := &track1StatefulTotpCache{}
	handler, client := newOAuthPendingFlowTestHandlerWithDependencies(t, oauthPendingFlowTestHandlerOptions{
		refreshTokenCache: refreshCache,
		totpCache:         totpCache,
		totpEncryptor:     oauthPendingFlowTotpEncryptorStub{},
		settingValues: map[string]string{
			service.SettingKeyTotpEnabled:             "true",
			service.SettingKeySessionBindingEnabled:   "true",
			service.SettingKeyGitHubOAuthEnabled:      "true",
			service.SettingKeyGitHubOAuthClientID:     "gh-client",
			service.SettingKeyGitHubOAuthClientSecret: "gh-secret",
			service.SettingKeyGitHubOAuthRedirectURL:  "https://api.example.com/api/v1/auth/oauth/github/callback",
		},
	})
	t.Cleanup(func() { _ = client.Close() })
	handler.cfg = &config.Config{
		JWT: config.JWTConfig{
			Secret:                   "test-secret",
			ExpireHour:               1,
			AccessTokenExpireMinutes: 60,
			RefreshTokenExpireDays:   7,
		},
		Security: config.SecurityConfig{
			SessionBindingMode: "strict",
		},
	}
	handler.settingSvc = service.NewSettingService(&oauthPendingFlowSettingRepoStub{
		values: map[string]string{
			service.SettingKeyTotpEnabled:             "true",
			service.SettingKeySessionBindingEnabled:   "true",
			service.SettingKeyGitHubOAuthEnabled:      "true",
			service.SettingKeyGitHubOAuthClientID:     "gh-client",
			service.SettingKeyGitHubOAuthClientSecret: "gh-secret",
			service.SettingKeyGitHubOAuthRedirectURL:  "https://api.example.com/api/v1/auth/oauth/github/callback",
		},
	}, handler.cfg)

	userEntity, err := client.User.Create().
		SetEmail("bind-sec@example.com").
		SetUsername("bind-sec").
		SetPasswordHash("hash").
		SetRole(service.RoleUser).
		SetStatus(service.StatusActive).
		SetTotpEnabled(true).
		SetTotpSecretEncrypted("JBSWY3DPEHPK3PXP").
		Save(ctx)
	require.NoError(t, err)

	svcUser, err := handler.userService.GetByID(ctx, userEntity.ID)
	require.NoError(t, err)

	origBinding := &service.SessionBinding{IP: "203.0.113.10", UserAgent: "Mozilla/5.0 Chrome/120"}
	pair, err := handler.authService.GenerateTokenPair(service.WithSessionBinding(ctx, origBinding), svcUser, "sess-family-1")
	require.NoError(t, err)

	// 1. PrepareOAuthBindAccessTokenCookie rejects 2FA user without step-up grant (403 STEP_UP_REQUIRED).
	prepRec := httptest.NewRecorder()
	prepCtx, _ := gin.CreateTestContext(prepRec)
	prepReq := httptest.NewRequest(http.MethodPost, "/api/v1/auth/oauth/bind-token", nil)
	prepReq.Header.Set("Authorization", "Bearer "+pair.AccessToken)
	prepCtx.Request = prepReq
	prepCtx.Set(string(servermiddleware.ContextKeyUser), servermiddleware.AuthSubject{UserID: userEntity.ID})
	prepCtx.Set(servermiddleware.ContextKeySessionID, "sess-family-1")

	handler.PrepareOAuthBindAccessTokenCookie(prepCtx)
	require.Equal(t, http.StatusForbidden, prepRec.Code, prepRec.Body.String())
	require.Contains(t, prepRec.Body.String(), "STEP_UP_REQUIRED")
	require.Nil(t, findCookie(prepRec.Result().Cookies(), oauthBindAccessTokenCookieName))

	// 2. Direct /bind/start with cookie WITHOUT step-up grant -> 403 STEP_UP_REQUIRED.
	startNoStepUpRec := httptest.NewRecorder()
	startNoStepUpCtx, _ := gin.CreateTestContext(startNoStepUpRec)
	startNoStepUpReq := httptest.NewRequest(http.MethodGet, "/api/v1/auth/oauth/github/start?intent=bind_current_user", nil).
		WithContext(service.WithSessionBinding(ctx, origBinding))
	startNoStepUpReq.AddCookie(&http.Cookie{Name: oauthBindAccessTokenCookieName, Value: pair.AccessToken})
	startNoStepUpCtx.Request = startNoStepUpReq

	handler.GitHubOAuthStart(startNoStepUpCtx)
	require.Equal(t, http.StatusForbidden, startNoStepUpRec.Code)
	require.Contains(t, startNoStepUpRec.Body.String(), "STEP_UP_REQUIRED")

	// Grant step-up for this session.
	require.NoError(t, totpCache.SetStepUpGrant(ctx, userEntity.ID, "sess-family-1", service.FactorTOTP, 5*time.Minute))

	// 3. Direct /bind/start from DIFFERENT IP/UA (session binding mismatch) -> 401 SESSION_BINDING_MISMATCH.
	attackerBinding := &service.SessionBinding{IP: "198.51.100.99", UserAgent: "curl/8.0"}
	mismatchRec := httptest.NewRecorder()
	mismatchCtx, _ := gin.CreateTestContext(mismatchRec)
	mismatchReq := httptest.NewRequest(http.MethodGet, "/api/v1/auth/oauth/github/start?intent=bind_current_user", nil).
		WithContext(service.WithSessionBinding(ctx, attackerBinding))
	mismatchReq.AddCookie(&http.Cookie{Name: oauthBindAccessTokenCookieName, Value: pair.AccessToken})
	mismatchCtx.Request = mismatchReq

	handler.GitHubOAuthStart(mismatchCtx)
	require.Equal(t, http.StatusUnauthorized, mismatchRec.Code)
	require.Contains(t, mismatchRec.Body.String(), "SESSION_BINDING_MISMATCH")

	// 4. Legitimate /bind/start WITH matching binding AND step-up grant -> 302 with meta cookie!
	okRec := httptest.NewRecorder()
	okCtx, _ := gin.CreateTestContext(okRec)
	okReq := httptest.NewRequest(http.MethodGet, "/api/v1/auth/oauth/github/start?intent=bind_current_user", nil).
		WithContext(service.WithSessionBinding(ctx, origBinding))
	okReq.AddCookie(&http.Cookie{Name: oauthBindAccessTokenCookieName, Value: pair.AccessToken})
	okCtx.Request = okReq

	handler.GitHubOAuthStart(okCtx)
	require.Equal(t, http.StatusFound, okRec.Code, okRec.Body.String())
	bindCookie := findCookie(okRec.Result().Cookies(), emailOAuthBindUserCookie)
	require.NotNil(t, bindCookie)
	meta, err := parseOAuthBindUserCookieMeta(decodeCookieValueForTest(t, bindCookie.Value), "test-secret")
	require.NoError(t, err)
	require.Equal(t, userEntity.ID, meta.UserID)
	require.True(t, meta.HasTokenVersion)
	require.Greater(t, meta.TokenIssuedAt, int64(0))

	// 5. Revoke all sessions -> /bind/start with old token -> 401 TOKEN_REVOKED!
	require.NoError(t, handler.authService.RevokeAllUserTokens(ctx, userEntity.ID))
	revokedRec := httptest.NewRecorder()
	revokedCtx, _ := gin.CreateTestContext(revokedRec)
	revokedReq := httptest.NewRequest(http.MethodGet, "/api/v1/auth/oauth/github/start?intent=bind_current_user", nil).
		WithContext(service.WithSessionBinding(ctx, origBinding))
	revokedReq.AddCookie(&http.Cookie{Name: oauthBindAccessTokenCookieName, Value: pair.AccessToken})
	revokedCtx.Request = revokedReq

	handler.GitHubOAuthStart(revokedCtx)
	require.Equal(t, http.StatusUnauthorized, revokedRec.Code)
	require.Contains(t, revokedRec.Body.String(), "TOKEN_REVOKED")

	// 6. In-flight bind_current_user pending session created BEFORE revoke-all-sessions MUST be rejected on /pending/exchange!
	inFlightSess, err := client.PendingAuthSession.Create().
		SetSessionToken("inflight-bind-token").
		SetIntent(oauthIntentBindCurrentUser).
		SetProviderType("linuxdo").
		SetProviderKey("linuxdo").
		SetProviderSubject("ld-inflight-sub").
		SetTargetUserID(userEntity.ID).
		SetResolvedEmail("ld@example.com").
		SetBrowserSessionKey("inflight-browser-key").
		SetLocalFlowState(map[string]any{
			oauthCompletionResponseKey:     map[string]any{"redirect": "/profile"},
			oauthBindTokenIssuedAtStateKey: meta.TokenIssuedAt,
			oauthBindTokenVersionStateKey:  meta.TokenVersion,
		}).
		SetExpiresAt(time.Now().UTC().Add(10 * time.Minute)).
		Save(ctx)
	require.NoError(t, err)

	exRec := httptest.NewRecorder()
	exCtx, _ := gin.CreateTestContext(exRec)
	exReq := httptest.NewRequest(http.MethodPost, "/api/v1/auth/oauth/pending/exchange", nil)
	exReq.AddCookie(&http.Cookie{Name: oauthPendingSessionCookieName, Value: encodeCookieValue(inFlightSess.SessionToken)})
	exReq.AddCookie(&http.Cookie{Name: oauthPendingBrowserCookieName, Value: encodeCookieValue(inFlightSess.BrowserSessionKey)})
	exCtx.Request = exReq

	handler.ExchangePendingOAuthCompletion(exCtx)
	require.Equal(t, http.StatusUnauthorized, exRec.Code, exRec.Body.String())
	require.Contains(t, exRec.Body.String(), "TOKEN_REVOKED")

	// Verify ZERO auth_identities were bound!
	boundCount, err := client.AuthIdentity.Query().Where(authidentity.UserIDEQ(userEntity.ID)).Count(ctx)
	require.NoError(t, err)
	require.Zero(t, boundCount)
}

// Area 3: OIDC handler outbound endpoints block private IP, redirect-to-metadata SSRF, and oversized JWKS.
func TestOIDCHandlerBlocksSSRFRedirectAndOversizedBody(t *testing.T) {
	ctx := context.Background()

	// 1. Default production setting (allowPrivateForTest=false) blocks loopback token/userinfo/jwks URLs.
	t.Cleanup(service.SetAdminOutboundAllowPrivateForTest(false))
	_, err := oidcExchangeCode(ctx, config.OIDCConnectConfig{TokenURL: "http://127.0.0.1:8080/token"}, "code", "https://app.example/cb", "")
	require.Error(t, err)
	_, err = oidcFetchUserInfo(ctx, config.OIDCConnectConfig{UserInfoURL: "http://169.254.169.254/latest/meta-data/"}, &oidcTokenResponse{AccessToken: "tok"})
	require.Error(t, err)
	_, err = oidcFetchJWKSet(ctx, "http://[::1]:8080/jwks")
	require.Error(t, err)

	// 2. Oversized JWKS payload (> 1 MiB) is rejected even on an allowed test server.
	t.Run("oversized JWKS body rejected", func(t *testing.T) {
		t.Cleanup(service.SetAdminOutboundAllowPrivateForTest(true))
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write(bytes.Repeat([]byte(" "), int(service.OIDCMaxResponseBodyBytes)+10))
		}))
		defer srv.Close()

		_, fetchErr := oidcFetchJWKSet(ctx, srv.URL)
		require.Error(t, fetchErr)
		require.Contains(t, fetchErr.Error(), "exceeds limit")
	})

	// 3. Redirect to non-HTTP scheme (e.g. file://) is blocked by CheckRedirect even in test mode.
	t.Run("redirect to disallowed scheme blocked", func(t *testing.T) {
		t.Cleanup(service.SetAdminOutboundAllowPrivateForTest(true))
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			http.Redirect(w, r, "file:///etc/passwd", http.StatusFound)
		}))
		defer srv.Close()

		_, fetchErr := oidcFetchJWKSet(ctx, srv.URL)
		require.Error(t, fetchErr)
	})
}

// Area 4: ResetPassword revokes BOTH 2FA grants AND uak_ personal API key;
// UserHandler.ChangePassword revokes refresh sessions AND uak_ personal API key.
func TestResetPasswordAndChangePasswordRevokePersonalAPIKeyAnd2FAGrants(t *testing.T) {
	gin.SetMode(gin.TestMode)
	ctx := context.Background()

	emailCache := &track1PasswordResetEmailCacheStub{
		tokens: map[string]string{"reset-token-123": "reset-owner@example.com"},
	}
	refreshCache := &track1StatefulRefreshCache{}
	totpCache := &track1StatefulTotpCache{}
	handler, client := newOAuthPendingFlowTestHandlerWithDependencies(t, oauthPendingFlowTestHandlerOptions{
		refreshTokenCache: refreshCache,
		totpCache:         totpCache,
		emailCache:        emailCache,
		settingValues: map[string]string{
			service.SettingKeyEmailVerifyEnabled:   "true",
			service.SettingKeyPasswordResetEnabled: "true",
		},
	})
	t.Cleanup(func() { _ = client.Close() })

	pwHash, err := handler.authService.HashPassword("old-password-123")
	require.NoError(t, err)
	userEntity, err := client.User.Create().
		SetEmail("reset-owner@example.com").
		SetUsername("reset-owner").
		SetPasswordHash(pwHash).
		SetRole(service.RoleUser).
		SetStatus(service.StatusActive).
		Save(ctx)
	require.NoError(t, err)

	revoker := &track1PersonalKeyRevokerSpy{}
	handler.authService.SetPersonalAPIKeyRevoker(revoker)
	handler.totpService.SetPersonalAPIKeyRevoker(revoker)

	// 1. ResetPassword revokes 2FA grants AND uak_ personal API key.
	resetRec := httptest.NewRecorder()
	resetCtx, _ := gin.CreateTestContext(resetRec)
	resetReq := httptest.NewRequest(http.MethodPost, "/api/v1/auth/reset-password",
		strings.NewReader(`{"email":"reset-owner@example.com","token":"reset-token-123","new_password":"new-password-456"}`))
	resetReq.Header.Set("Content-Type", "application/json")
	resetCtx.Request = resetReq

	handler.ResetPassword(resetCtx)
	require.Equal(t, http.StatusOK, resetRec.Code, resetRec.Body.String())
	require.Contains(t, totpCache.revokedGrantUsers, userEntity.ID, "ResetPassword must revoke 2FA grants")
	require.Contains(t, revoker.revokedUserIDs, userEntity.ID, "ResetPassword must revoke uak_ personal API key")

	// 2. UserHandler.ChangePassword revokes refresh sessions AND uak_ personal API key.
	revoker.revokedUserIDs = nil
	totpCache.revokedGrantUsers = nil
	userHandler := NewUserHandler(handler.userService, handler.authService, nil, nil, nil, nil)
	userHandler.SetTotpService(handler.totpService)

	chRec := httptest.NewRecorder()
	chCtx, _ := gin.CreateTestContext(chRec)
	chReq := httptest.NewRequest(http.MethodPut, "/api/v1/user/password",
		strings.NewReader(`{"old_password":"new-password-456","new_password":"newer-password-789"}`))
	chReq.Header.Set("Content-Type", "application/json")
	chCtx.Request = chReq
	chCtx.Set(string(servermiddleware.ContextKeyUser), servermiddleware.AuthSubject{UserID: userEntity.ID})

	userHandler.ChangePassword(chCtx)
	require.Equal(t, http.StatusOK, chRec.Code, chRec.Body.String())
	require.Contains(t, revoker.revokedUserIDs, userEntity.ID, "ChangePassword must revoke uak_ personal API key")
	require.Contains(t, totpCache.revokedGrantUsers, userEntity.ID, "ChangePassword must revoke 2FA grants")
	require.False(t, refreshCache.revokedBefore[userEntity.ID].IsZero(), "ChangePassword must revoke access tokens before now")
}

type track1PasswordResetEmailCacheStub struct {
	service.EmailCache
	tokens map[string]string
}

func (c *track1PasswordResetEmailCacheStub) GetPasswordResetToken(_ context.Context, email string) (*service.PasswordResetTokenData, error) {
	for tok, em := range c.tokens {
		if strings.EqualFold(em, email) {
			return &service.PasswordResetTokenData{
				Token:     tok,
				CreatedAt: time.Now(),
			}, nil
		}
	}
	return nil, nil
}

func (c *track1PasswordResetEmailCacheStub) DeletePasswordResetToken(_ context.Context, email string) error {
	for tok, em := range c.tokens {
		if strings.EqualFold(em, email) {
			delete(c.tokens, tok)
		}
	}
	return nil
}

func (c *track1PasswordResetEmailCacheStub) DeletePasswordResetCooldown(context.Context, string) error {
	return nil
}
