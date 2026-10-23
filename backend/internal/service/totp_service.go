package service

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"fmt"
	"time"

	"github.com/pquerna/otp"
	"github.com/pquerna/otp/totp"

	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
)

var (
	ErrTotpNotEnabled      = infraerrors.BadRequest("TOTP_NOT_ENABLED", "totp feature is not enabled")
	ErrTotpAlreadyEnabled  = infraerrors.BadRequest("TOTP_ALREADY_ENABLED", "totp is already enabled for this account")
	ErrTotpNotSetup        = infraerrors.BadRequest("TOTP_NOT_SETUP", "totp is not set up for this account")
	ErrTotpInvalidCode     = infraerrors.BadRequest("TOTP_INVALID_CODE", "invalid totp code")
	ErrTotpSetupExpired    = infraerrors.BadRequest("TOTP_SETUP_EXPIRED", "totp setup session expired")
	ErrTotpTooManyAttempts = infraerrors.TooManyRequests("TOTP_TOO_MANY_ATTEMPTS", "too many verification attempts, please try again later")
	ErrVerifyCodeRequired  = infraerrors.BadRequest("VERIFY_CODE_REQUIRED", "email verification code is required")
	ErrPasswordRequired    = infraerrors.BadRequest("PASSWORD_REQUIRED", "password is required")
	ErrTotpUnavailable     = infraerrors.ServiceUnavailable("TOTP_UNAVAILABLE", "totp verification unavailable")
)

// TotpCache defines cache operations for TOTP service
type TotpCache interface {
	// Setup session methods
	GetSetupSession(ctx context.Context, userID int64) (*TotpSetupSession, error)
	SetSetupSession(ctx context.Context, userID int64, session *TotpSetupSession, ttl time.Duration) error
	DeleteSetupSession(ctx context.Context, userID int64) error

	// Login session methods (for 2FA login flow)
	GetLoginSession(ctx context.Context, tempToken string) (*TotpLoginSession, error)
	SetLoginSession(ctx context.Context, tempToken string, session *TotpLoginSession, ttl time.Duration) error
	DeleteLoginSession(ctx context.Context, tempToken string) error

	// Rate limiting
	IncrementVerifyAttempts(ctx context.Context, userID int64) (int, error)
	GetVerifyAttempts(ctx context.Context, userID int64) (int, error)
	ClearVerifyAttempts(ctx context.Context, userID int64) error

	// Step-up grant methods (敏感操作 sudo 窗口)
	SetStepUpGrant(ctx context.Context, userID int64, sessionKey string, ttl time.Duration) error
	HasStepUpGrant(ctx context.Context, userID int64, sessionKey string) (bool, error)
}

// SecretEncryptor defines encryption operations for TOTP secrets
type SecretEncryptor interface {
	Encrypt(plaintext string) (string, error)
	Decrypt(ciphertext string) (string, error)
}

// TotpSetupSession represents a TOTP setup session
type TotpSetupSession struct {
	Secret     string // Plain text TOTP secret (not encrypted yet)
	SetupToken string // Random token to verify setup request
	CreatedAt  time.Time
	// CryptoSession binds payment enrollment to the authenticated JWT session.
	CryptoSession   string
	SecretEncrypted bool
}

// TotpLoginSession represents a pending 2FA login session
type TotpLoginSession struct {
	UserID           int64
	Email            string
	TokenExpiry      time.Time
	PendingOAuthBind *PendingOAuthBindLoginSession `json:"pending_oauth_bind,omitempty"`
}

type PendingOAuthBindLoginSession struct {
	PendingSessionToken string `json:"pending_session_token,omitempty"`
	BrowserSessionKey   string `json:"browser_session_key,omitempty"`
}

// TotpStatus represents the TOTP status for a user
type TotpStatus struct {
	Enabled        bool       `json:"enabled"`
	EnabledAt      *time.Time `json:"enabled_at,omitempty"`
	FeatureEnabled bool       `json:"feature_enabled"`
}

// TotpSetupResponse represents the response for initiating TOTP setup
type TotpSetupResponse struct {
	Secret     string `json:"secret"`
	QRCodeURL  string `json:"qr_code_url"`
	SetupToken string `json:"setup_token"`
	Countdown  int    `json:"countdown"` // seconds until setup expires
}

const (
	totpSetupTTL    = 5 * time.Minute
	totpLoginTTL    = 5 * time.Minute
	totpAttemptsTTL = 15 * time.Minute
	maxTotpAttempts = 5
	totpIssuer      = "Sub2API"
)

// TotpService handles TOTP operations
type TotpService struct {
	userRepo          UserRepository
	encryptor         SecretEncryptor
	cache             TotpCache
	settingService    *SettingService
	emailService      *EmailService
	emailQueueService *EmailQueueService
	cryptoSecurity    *CryptoSecurityService
}

// NewTotpService creates a new TOTP service
func NewTotpService(
	userRepo UserRepository,
	encryptor SecretEncryptor,
	cache TotpCache,
	settingService *SettingService,
	emailService *EmailService,
	emailQueueService *EmailQueueService,
) *TotpService {
	return &TotpService{
		userRepo:          userRepo,
		encryptor:         encryptor,
		cache:             cache,
		settingService:    settingService,
		emailService:      emailService,
		emailQueueService: emailQueueService,
	}
}

// GetStatus returns the TOTP status for a user
func (s *TotpService) GetStatus(ctx context.Context, userID int64) (*TotpStatus, error) {
	featureEnabled := s.settingService.IsTotpEnabled(ctx)

	user, err := s.userRepo.GetByID(ctx, userID)
	if err != nil {
		return nil, fmt.Errorf("get user: %w", err)
	}

	return &TotpStatus{
		Enabled:        user.TotpEnabled,
		EnabledAt:      user.TotpEnabledAt,
		FeatureEnabled: featureEnabled,
	}, nil
}

// usesEmailVerification 判断 TOTP 启用/停用时的身份校验方式。
// 管理员一律使用密码校验：管理员账号的邮箱常为占位地址收不到验证码，
// 且管理员凭证失守时攻击者往往同时控制通知邮箱，邮箱验证码不构成有效防线。
// 普通用户维持原有行为：开启邮箱验证时用邮箱验证码，否则用密码。
func (s *TotpService) usesEmailVerification(ctx context.Context, user *User) bool {
	return user.Role != RoleAdmin && s.settingService.IsEmailVerifyEnabled(ctx)
}

// verifyIdentity 按 usesEmailVerification 的结果校验邮箱验证码或密码。
func (s *TotpService) verifyIdentity(ctx context.Context, user *User, emailCode, password string) error {
	if s.usesEmailVerification(ctx, user) {
		if emailCode == "" {
			return ErrVerifyCodeRequired
		}
		return s.emailService.VerifyCode(ctx, user.Email, emailCode)
	}
	if password == "" {
		return ErrPasswordRequired
	}
	if !user.CheckPassword(password) {
		return ErrPasswordIncorrect
	}
	return nil
}

// InitiateSetup starts the TOTP setup process
// If email verification is enabled, emailCode is required; otherwise password is required
func (s *TotpService) InitiateSetup(ctx context.Context, userID int64, emailCode, password string) (*TotpSetupResponse, error) {
	// Check if TOTP feature is enabled globally
	if !s.settingService.IsTotpEnabled(ctx) {
		return nil, ErrTotpNotEnabled
	}
	unlock, err := s.lockEnrollment(ctx, userID, "setup", 5)
	if err != nil {
		return nil, err
	}
	defer unlock()
	return s.initiateSetup(ctx, userID, emailCode, password, "", false)
}

// InitiateSetupForSession binds administrator enrollment to the validated JWT
// session when payment verification is configured, while retaining the normal
// email/password identity check for the general TOTP flow.
func (s *TotpService) InitiateSetupForSession(ctx context.Context, userID int64, emailCode, password, sessionID string) (*TotpSetupResponse, error) {
	if !s.settingService.IsTotpEnabled(ctx) {
		return nil, ErrTotpNotEnabled
	}
	user, err := s.userRepo.GetByID(ctx, userID)
	if err != nil || user == nil {
		return nil, ErrTotpUnavailable
	}
	binding := ""
	if user.IsAdmin() && s.cryptoSecurity != nil && s.cryptoSecurity.ready() {
		if sessionID == "" {
			return nil, ErrCryptoAdminRequired
		}
		binding = s.cryptoSecurity.digest("session", userID, sessionID)
	}
	unlock, err := s.lockEnrollment(ctx, userID, "setup", 5)
	if err != nil {
		return nil, err
	}
	defer unlock()
	return s.initiateSetup(ctx, userID, emailCode, password, binding, false)
}

// General and payment endpoints mutate the same user secret. Use the same
// distributed lock and administrator attempt budgets on both entry points.
func (s *TotpService) lockEnrollment(ctx context.Context, userID int64, action string, max int) (func(), error) {
	user, err := s.userRepo.GetByID(ctx, userID)
	if err != nil || user == nil {
		return nil, ErrTotpUnavailable
	}
	if s.cryptoSecurity == nil || !s.cryptoSecurity.ready() {
		// A site without payment verification must retain ordinary login TOTP.
		// Payment operations themselves still fail closed in VerifyRisk.
		return func() {}, nil
	}
	if user.IsAdmin() {
		if _, err := s.cryptoSecurity.CurrentAdmin(ctx, userID); err != nil {
			return nil, err
		}
		if err := s.cryptoSecurity.limit(ctx, userID, action, max, totpAttemptsTTL); err != nil {
			return nil, err
		}
	}
	return s.cryptoSecurity.lockSetup(ctx, userID)
}

func (s *TotpService) initiateSetup(ctx context.Context, userID int64, emailCode, password, cryptoSession string, skipIdentity bool) (*TotpSetupResponse, error) {
	// Get user and check if TOTP is already enabled
	user, err := s.userRepo.GetByID(ctx, userID)
	if err != nil {
		return nil, fmt.Errorf("get user: %w", err)
	}

	if user.TotpEnabled {
		return nil, ErrTotpAlreadyEnabled
	}
	if cryptoSession != "" && (!user.IsAdmin() || !user.IsActive() || user.DeletedAt != nil) {
		return nil, ErrCryptoAdminRequired
	}

	if !skipIdentity {
		if err := s.verifyIdentity(ctx, user, emailCode, password); err != nil {
			return nil, err
		}
	}

	// Generate a new TOTP key
	key, err := totp.Generate(totp.GenerateOpts{
		Issuer:      totpIssuer,
		AccountName: user.Email,
	})
	if err != nil {
		return nil, fmt.Errorf("generate totp key: %w", err)
	}

	// Generate a random setup token
	setupToken, err := generateRandomToken(32)
	if err != nil {
		return nil, fmt.Errorf("generate setup token: %w", err)
	}

	// Store the setup session in cache
	session := &TotpSetupSession{
		Secret:        key.Secret(),
		SetupToken:    setupToken,
		CreatedAt:     time.Now(),
		CryptoSession: cryptoSession,
	}
	if cryptoSession != "" || user.IsAdmin() {
		// Both enrollment endpoints establish the payment factor for admins.
		if s.encryptor == nil {
			return nil, ErrTotpUnavailable
		}
		session.Secret, err = s.encryptor.Encrypt(session.Secret)
		if err != nil {
			return nil, ErrTotpUnavailable
		}
		session.SecretEncrypted = true
	}

	if err := s.cache.SetSetupSession(ctx, userID, session, totpSetupTTL); err != nil {
		return nil, fmt.Errorf("store setup session: %w", err)
	}

	return &TotpSetupResponse{
		Secret:     key.Secret(),
		QRCodeURL:  key.URL(),
		SetupToken: setupToken,
		Countdown:  int(totpSetupTTL.Seconds()),
	}, nil
}

// CompleteSetup completes normal enrollment. Crypto sessions cannot be completed
// through the general endpoint, even if the global feature is later enabled.
func (s *TotpService) CompleteSetup(ctx context.Context, userID int64, totpCode, setupToken string) error {
	if !s.settingService.IsTotpEnabled(ctx) {
		return ErrTotpNotEnabled
	}
	unlock, err := s.lockEnrollment(ctx, userID, "enable", 10)
	if err != nil {
		return err
	}
	defer unlock()
	return s.completeSetup(ctx, userID, totpCode, setupToken, "", nil)
}

// CompleteSetupForSession rejects attempts to finish an administrator's setup
// from a different JWT session than the one that generated its QR code.
func (s *TotpService) CompleteSetupForSession(ctx context.Context, userID int64, totpCode, setupToken, sessionID string) error {
	if !s.settingService.IsTotpEnabled(ctx) {
		return ErrTotpNotEnabled
	}
	user, err := s.userRepo.GetByID(ctx, userID)
	if err != nil || user == nil {
		return ErrTotpUnavailable
	}
	binding := ""
	var consume func(int64) error
	if user.IsAdmin() && s.cryptoSecurity != nil && s.cryptoSecurity.ready() {
		if sessionID == "" {
			return ErrCryptoAdminRequired
		}
		binding = s.cryptoSecurity.digest("session", userID, sessionID)
		consume = func(counter int64) error { return s.cryptoSecurity.consume(ctx, userID, counter, totpCode) }
	}
	unlock, err := s.lockEnrollment(ctx, userID, "enable", 10)
	if err != nil {
		return err
	}
	defer unlock()
	return s.completeSetup(ctx, userID, totpCode, setupToken, binding, consume)
}

func (s *TotpService) completeSetup(ctx context.Context, userID int64, code, token, binding string, consume func(int64) error) error {
	if err := s.reserveVerifyAttempt(ctx, userID); err != nil {
		return err
	}
	session, err := s.cache.GetSetupSession(ctx, userID)
	if err != nil {
		return ErrTotpUnavailable
	}
	if session == nil || time.Since(session.CreatedAt) >= totpSetupTTL || session.CreatedAt.After(time.Now().Add(time.Minute)) ||
		session.CryptoSession != binding || subtle.ConstantTimeCompare([]byte(session.SetupToken), []byte(token)) != 1 {
		return ErrTotpSetupExpired
	}
	user, err := s.userRepo.GetByID(ctx, userID)
	if err != nil || user == nil {
		return ErrTotpUnavailable
	}
	if user.TotpEnabled {
		return ErrTotpAlreadyEnabled
	}
	if (binding != "" || user.IsAdmin()) && (!user.IsAdmin() || !user.IsActive() || user.DeletedAt != nil) {
		return ErrCryptoAdminRequired
	}
	if user.IsAdmin() && consume == nil && s.cryptoSecurity != nil && s.cryptoSecurity.ready() {
		consume = func(counter int64) error { return s.cryptoSecurity.consume(ctx, userID, counter, code) }
	}
	secret := session.Secret
	if session.SecretEncrypted {
		secret, err = s.encryptor.Decrypt(secret)
		if err != nil {
			return ErrTotpUnavailable
		}
	}
	counter, valid := validateTotpCode(code, secret, time.Now())
	if !valid {
		return ErrTotpInvalidCode
	}
	if consume != nil {
		if err := consume(counter); err != nil {
			return err
		}
	}
	encrypted, err := s.encryptor.Encrypt(secret)
	if err != nil {
		return ErrTotpUnavailable
	}
	// Commit the encrypted secret and enabled state together, conditional on
	// there still being no authenticator. The distributed lock alone cannot
	// protect against a delayed request after its lease expires.
	binder, ok := s.userRepo.(TotpAtomicBinder)
	if !ok {
		return ErrUserSecurityStateChanged
	}
	// Delete before writing so a cache failure cannot enable enrollment.
	if err := s.cache.DeleteSetupSession(ctx, userID); err != nil {
		return ErrTotpUnavailable
	}
	if err := s.cache.ClearVerifyAttempts(ctx, userID); err != nil {
		return ErrTotpUnavailable
	}
	if err := binder.BindTotpIfDisabled(ctx, userID, encrypted); err != nil {
		return fmt.Errorf("enable totp: %w", err)
	}
	return nil
}

// Disable verifies the current authenticator. Administrators may instead use
// DisableWithServerAuthorization for a one-time, server-issued recovery code.
// Passwords, email codes and earlier step-up grants cannot replace current TOTP
// for any account. Legacy identity arguments remain accepted but are unused.
func (s *TotpService) Disable(ctx context.Context, userID int64, _, _ string, totpCodes ...string) error {
	// Get user
	user, err := s.userRepo.GetByID(ctx, userID)
	if err != nil {
		return fmt.Errorf("get user: %w", err)
	}

	if !user.TotpEnabled {
		if user.IsAdmin() {
			return ErrCryptoTotpNotSetup
		}
		return ErrTotpNotSetup
	}
	if len(totpCodes) != 1 || !isSixDigitTotp(totpCodes[0]) {
		if user.IsAdmin() {
			return ErrCryptoTotpRequired
		}
		return ErrTotpInvalidCode
	}
	var verifiedUser *User
	if user.IsAdmin() {
		if s.cryptoSecurity != nil && s.cryptoSecurity.ready() {
			unlock, err := s.cryptoSecurity.lockSetup(ctx, userID)
			if err != nil {
				return err
			}
			defer unlock()
			var verifyErr error
			verifiedUser, verifyErr = s.cryptoSecurity.verifyAdminRiskUser(ctx, userID, totpCodes[0])
			if verifyErr != nil {
				return verifyErr
			}
		} else {
			// Sites without payment verification still support ordinary TOTP.
			var verifyErr error
			verifiedUser, verifyErr = s.verifyCode(ctx, userID, totpCodes[0], nil)
			if verifyErr != nil {
				return cryptoTotpError(verifyErr)
			}
		}
	} else {
		var verifyErr error
		verifiedUser, verifyErr = s.verifyCode(ctx, userID, totpCodes[0], nil)
		if verifyErr != nil {
			return verifyErr
		}
	}
	if verifiedUser.Role != user.Role {
		return ErrUserSecurityStateChanged
	}
	user = verifiedUser

	// Bind the write to the exact role and secret that were verified. Concurrent
	// promotion or re-enrollment must not turn a stale code into authorization.
	disabler, ok := s.userRepo.(TotpRoleConditionalDisabler)
	if !ok {
		return ErrUserSecurityStateChanged
	}
	if err := disabler.DisableTotpWithExpectedRole(ctx, userID, user.Role, user.TotpSecretEncrypted); err != nil {
		return fmt.Errorf("disable totp: %w", err)
	}

	return nil
}

// DisableWithServerAuthorization recovers an administrator's authenticator
// without knowing its current code. Only the server CLI can issue authorization;
// the cached digest is consumed once and is bound to the exact current secret.
func (s *TotpService) DisableWithServerAuthorization(ctx context.Context, userID int64, code string) error {
	if s == nil || s.userRepo == nil {
		return ErrAdmin2FAApprovalUnavailable
	}
	store, ok := s.cache.(Admin2FAApprovalStore)
	if !ok {
		return ErrAdmin2FAApprovalUnavailable
	}
	verified, err := NewAdmin2FAApprovalService(s.userRepo, store).Consume(ctx, userID, code)
	if err != nil {
		return err
	}
	disabler, ok := s.userRepo.(TotpRoleConditionalDisabler)
	if !ok {
		return ErrUserSecurityStateChanged
	}
	if err := disabler.DisableTotpWithExpectedRole(ctx, userID, verified.Role, verified.TotpSecretEncrypted); err != nil {
		return fmt.Errorf("disable totp: %w", err)
	}
	return nil
}

// VerifyCode verifies a code using the existing encrypted user secret.
func (s *TotpService) VerifyCode(ctx context.Context, userID int64, code string) error {
	_, err := s.verifyCode(ctx, userID, code, nil)
	return err
}

// VerifyCodeOnce adds an atomic, shared consumption step to ordinary verification.
// The consumer receives the matched TOTP counter, rather than the request clock
// bucket, so crossing a 30-second boundary cannot allow the same code twice.
func (s *TotpService) VerifyCodeOnce(ctx context.Context, userID int64, code string, consume func(int64) error) error {
	if consume == nil {
		return ErrTotpUnavailable
	}
	_, err := s.verifyCode(ctx, userID, code, consume)
	return err
}

func (s *TotpService) reserveVerifyAttempt(ctx context.Context, userID int64) error {
	if s.cache == nil {
		return ErrTotpUnavailable
	}
	attempts, err := s.cache.GetVerifyAttempts(ctx, userID)
	if err != nil {
		return ErrTotpUnavailable
	}
	if attempts >= maxTotpAttempts {
		return ErrTotpTooManyAttempts
	}
	// Reserve before validating; concurrent failures cannot all pass a read check.
	attempts, err = s.cache.IncrementVerifyAttempts(ctx, userID)
	if err != nil {
		return ErrTotpUnavailable
	}
	if attempts > maxTotpAttempts {
		return ErrTotpTooManyAttempts
	}
	return nil
}

// Return the exact account snapshot whose secret was verified so security
// mutations can use it as an atomic write precondition.
func (s *TotpService) verifyCode(ctx context.Context, userID int64, code string, consume func(int64) error) (*User, error) {
	if err := s.reserveVerifyAttempt(ctx, userID); err != nil {
		return nil, err
	}
	user, err := s.userRepo.GetByID(ctx, userID)
	if err != nil || user == nil {
		return nil, ErrTotpUnavailable
	}
	if !user.TotpEnabled || user.TotpSecretEncrypted == nil {
		return nil, ErrTotpNotSetup
	}
	if s.encryptor == nil {
		return nil, ErrTotpUnavailable
	}
	secret, err := s.encryptor.Decrypt(*user.TotpSecretEncrypted)
	if err != nil {
		return nil, ErrTotpUnavailable
	}
	counter, valid := validateTotpCode(code, secret, time.Now())
	if !valid {
		return nil, ErrTotpInvalidCode
	}
	// Login and step-up must not provide reusable codes for payment operations,
	// or reset the failure budget by replaying an already-consumed code.
	if consume == nil && user.IsAdmin() && s.cryptoSecurity != nil && s.cryptoSecurity.ready() {
		consume = func(counter int64) error { return s.cryptoSecurity.consume(ctx, userID, counter, code) }
	}
	if consume != nil {
		if err := consume(counter); err != nil {
			return nil, err
		}
	}
	if err := s.cache.ClearVerifyAttempts(ctx, userID); err != nil {
		return nil, ErrTotpUnavailable
	}
	return user, nil
}

func isSixDigitTotp(code string) bool {
	if len(code) != 6 {
		return false
	}
	for i := range code {
		if code[i] < '0' || code[i] > '9' {
			return false
		}
	}
	return true
}

func validateTotpCode(code, secret string, now time.Time) (int64, bool) {
	if !isSixDigitTotp(code) {
		return 0, false
	}
	counter := now.Unix() / 30
	for _, offset := range []int64{0, -1, 1} {
		candidate := counter + offset
		valid, err := totp.ValidateCustom(code, secret, time.Unix(candidate*30, 0), totp.ValidateOpts{
			Period: 30, Skew: 0, Digits: otp.DigitsSix, Algorithm: otp.AlgorithmSHA1,
		})
		if err == nil && valid {
			return candidate, true
		}
	}
	return 0, false
}

// StepUpGrantTTL 敏感操作 step-up 验证的有效窗口（sudo 模式）。
const StepUpGrantTTL = 15 * time.Minute

// VerifyStepUp 校验 TOTP 码并授予当前会话一段时间的 step-up 权限。
// 返回授权有效期，供前端展示/设置提醒。
func (s *TotpService) VerifyStepUp(ctx context.Context, userID int64, sessionKey, code string) (time.Duration, error) {
	if err := s.VerifyCode(ctx, userID, code); err != nil {
		return 0, err
	}
	if err := s.cache.SetStepUpGrant(ctx, userID, sessionKey, StepUpGrantTTL); err != nil {
		return 0, fmt.Errorf("store step-up grant: %w", err)
	}
	return StepUpGrantTTL, nil
}

// HasStepUpGrant 检查当前会话是否在 step-up 有效期内。
func (s *TotpService) HasStepUpGrant(ctx context.Context, userID int64, sessionKey string) (bool, error) {
	return s.cache.HasStepUpGrant(ctx, userID, sessionKey)
}

// CreateLoginSession creates a temporary login session for 2FA
func (s *TotpService) CreateLoginSession(ctx context.Context, userID int64, email string) (string, error) {
	return s.createLoginSession(ctx, userID, email, nil)
}

// CreatePendingOAuthBindLoginSession creates a temporary 2FA session that will
// finalize a pending OAuth bind after the TOTP code is verified.
func (s *TotpService) CreatePendingOAuthBindLoginSession(
	ctx context.Context,
	userID int64,
	email string,
	pendingSessionToken string,
	browserSessionKey string,
) (string, error) {
	return s.createLoginSession(ctx, userID, email, &PendingOAuthBindLoginSession{
		PendingSessionToken: pendingSessionToken,
		BrowserSessionKey:   browserSessionKey,
	})
}

func (s *TotpService) createLoginSession(
	ctx context.Context,
	userID int64,
	email string,
	pendingOAuthBind *PendingOAuthBindLoginSession,
) (string, error) {
	// Generate a random temp token
	tempToken, err := generateRandomToken(32)
	if err != nil {
		return "", fmt.Errorf("generate temp token: %w", err)
	}

	session := &TotpLoginSession{
		UserID:           userID,
		Email:            email,
		TokenExpiry:      time.Now().Add(totpLoginTTL),
		PendingOAuthBind: pendingOAuthBind,
	}

	if err := s.cache.SetLoginSession(ctx, tempToken, session, totpLoginTTL); err != nil {
		return "", fmt.Errorf("store login session: %w", err)
	}

	return tempToken, nil
}

// GetLoginSession retrieves a login session
func (s *TotpService) GetLoginSession(ctx context.Context, tempToken string) (*TotpLoginSession, error) {
	return s.cache.GetLoginSession(ctx, tempToken)
}

// DeleteLoginSession deletes a login session
func (s *TotpService) DeleteLoginSession(ctx context.Context, tempToken string) error {
	return s.cache.DeleteLoginSession(ctx, tempToken)
}

// IsTotpEnabledForUser checks if TOTP is enabled for a specific user
func (s *TotpService) IsTotpEnabledForUser(ctx context.Context, userID int64) (bool, error) {
	user, err := s.userRepo.GetByID(ctx, userID)
	if err != nil {
		return false, fmt.Errorf("get user: %w", err)
	}
	return user.TotpEnabled, nil
}

// MaskEmail masks an email address for display
func MaskEmail(email string) string {
	if len(email) < 3 {
		return "***"
	}

	atIdx := -1
	for i, c := range email {
		if c == '@' {
			atIdx = i
			break
		}
	}

	if atIdx == -1 || atIdx < 1 {
		return email[:1] + "***"
	}

	localPart := email[:atIdx]
	domain := email[atIdx:]

	if len(localPart) <= 2 {
		return localPart[:1] + "***" + domain
	}

	return localPart[:1] + "***" + localPart[len(localPart)-1:] + domain
}

// generateRandomToken generates a random hex-encoded token
func generateRandomToken(byteLength int) (string, error) {
	b := make([]byte, byteLength)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}

// VerificationMethod represents the method required for TOTP operations
type VerificationMethod struct {
	Method string `json:"method"` // "email" or "password"
}

// GetVerificationMethod returns the verification method for TOTP operations.
// Disable always requires the current authenticator; enable retains the
// existing email/password identity verification contract.
func (s *TotpService) GetVerificationMethod(ctx context.Context, userID int64, operation ...string) (*VerificationMethod, error) {
	user, err := s.userRepo.GetByID(ctx, userID)
	if err != nil {
		return nil, fmt.Errorf("get user: %w", err)
	}
	if len(operation) > 0 && operation[0] == "disable" {
		return &VerificationMethod{Method: "totp"}, nil
	}
	if s.usesEmailVerification(ctx, user) {
		return &VerificationMethod{Method: "email"}, nil
	}
	return &VerificationMethod{Method: "password"}, nil
}

// SendVerifyCode sends an email verification code for TOTP operations
func (s *TotpService) SendVerifyCode(ctx context.Context, userID int64, locale ...string) error {
	// Check if email verification is enabled
	if !s.settingService.IsEmailVerifyEnabled(ctx) {
		return infraerrors.BadRequest("EMAIL_VERIFY_NOT_ENABLED", "email verification is not enabled")
	}

	// Get user email
	user, err := s.userRepo.GetByID(ctx, userID)
	if err != nil {
		return fmt.Errorf("get user: %w", err)
	}

	// Get site name for email
	siteName := s.settingService.GetSiteName(ctx)

	// Send verification code via queue
	return s.emailQueueService.EnqueueVerifyCode(user.Email, siteName, firstEmailLocale(locale))
}
