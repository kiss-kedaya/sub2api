package service

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
)

var (
	ErrCryptoTotpRequired     = infraerrors.Forbidden("CRYPTO_TOTP_REQUIRED", "a fresh six-digit payment TOTP code is required")
	ErrCryptoTotpNotSetup     = infraerrors.Forbidden("CRYPTO_TOTP_NOT_SETUP", "bind TOTP before performing payment operations")
	ErrCryptoTotpReplayed     = infraerrors.Conflict("CRYPTO_TOTP_REPLAYED", "this verification code or setup token has already been used")
	ErrCryptoTotpRateLimited  = infraerrors.TooManyRequests("CRYPTO_TOTP_RATE_LIMITED", "too many payment verification attempts")
	ErrCryptoTotpUnavailable  = infraerrors.ServiceUnavailable("CRYPTO_TOTP_REQUIRED", "payment verification unavailable")
	ErrCryptoAdminRequired    = infraerrors.Forbidden("CRYPTO_ADMIN_REQUIRED", "an active administrator JWT session is required")
	ErrCryptoOperatorRequired = infraerrors.Forbidden("CRYPTO_OPERATOR_REQUIRED", "this account is not a configured payment administrator")
)

const cryptoReplayTTL = 90 * time.Second

// CryptoTotpStore provides atomic, shared replay and enrollment controls.
// Redis implementation belongs to repository, not this service package.
type CryptoTotpStore interface {
	IncrementCryptoAttempt(ctx context.Context, key string, ttl time.Duration) (int, error)
	SetCryptoValueIfAbsent(ctx context.Context, key, value string, ttl time.Duration) (bool, error)
	CryptoValueExists(ctx context.Context, key string) (bool, error)
	ReleaseCryptoValue(ctx context.Context, key, value string) error
}

// CryptoSecurityStatus describes payment enrollment readiness, independently of
// the global login-TOTP and step-up switches.
type CryptoSecurityStatus struct {
	Enabled        bool `json:"enabled"`
	FeatureEnabled bool `json:"feature_enabled"`
	CanSetup       bool `json:"can_setup"`
	ReadOnly       bool `json:"read_only"`
}

// CryptoSecurityService reuses TotpService's user secret, encryption, enrollment
// and verification. Redis is mandatory; there is no local fallback or sudo grant.
type CryptoSecurityService struct {
	totp      *TotpService
	store     CryptoTotpStore
	key       []byte
	operators map[int64]struct{}
}

// NewCryptoSecurityService never generates or changes an encryption key. Only an
// explicitly configured, persistent TOTP key may enable payment verification.
func NewCryptoSecurityService(totp *TotpService, cfg *config.Config) *CryptoSecurityService {
	s := &CryptoSecurityService{totp: totp, operators: cryptoOperatorIDs(os.Getenv("CRYPTO_ADMIN_USER_IDS"))}
	if totp != nil {
		totp.cryptoSecurity = s
		s.store, _ = totp.cache.(CryptoTotpStore)
	}
	if cfg != nil && cfg.Totp.EncryptionKeyConfigured {
		key, err := hex.DecodeString(cfg.Totp.EncryptionKey)
		if err == nil && len(key) == 32 {
			s.key = key
		}
	}
	return s
}

// Payment authority is pinned by the operator at process startup, independently
// of mutable admin roles. Otherwise a stolen admin JWT or machine key could
// create another administrator, enroll a fresh authenticator and bypass 2FA.
// Missing or malformed deployment configuration authorizes nobody.
func cryptoOperatorIDs(raw string) map[int64]struct{} {
	entries := strings.Split(raw, ",")
	if len(entries) > 64 {
		return nil
	}
	ids := make(map[int64]struct{}, len(entries))
	for _, entry := range entries {
		value := strings.TrimSpace(entry)
		id, err := strconv.ParseInt(value, 10, 64)
		if err != nil || id <= 0 || strconv.FormatInt(id, 10) != value {
			return nil
		}
		ids[id] = struct{}{}
	}
	return ids
}

func (s *CryptoSecurityService) ready() bool {
	return s != nil && s.totp != nil && s.totp.encryptor != nil && s.totp.cache != nil && s.store != nil && len(s.key) == 32
}

// CurrentAdmin reads the repository on every request. Cached roles and JWT roles
// cannot retain access after deactivation, soft deletion or demotion.
func (s *CryptoSecurityService) CurrentAdmin(ctx context.Context, userID int64) (*User, error) {
	if s == nil || s.totp == nil || s.totp.userRepo == nil || userID <= 0 {
		return nil, ErrCryptoAdminRequired
	}
	user, err := s.totp.userRepo.GetByID(ctx, userID)
	if err != nil {
		return nil, ErrCryptoTotpUnavailable
	}
	if user == nil || user.ID != userID || user.DeletedAt != nil || !user.IsAdmin() || !user.IsActive() {
		return nil, ErrCryptoAdminRequired
	}
	return user, nil
}

func (s *CryptoSecurityService) CurrentOperator(ctx context.Context, userID int64) (*User, error) {
	if s == nil {
		return nil, ErrCryptoAdminRequired
	}
	if _, allowed := s.operators[userID]; !allowed {
		return nil, ErrCryptoOperatorRequired
	}
	return s.CurrentAdmin(ctx, userID)
}

// Status does not consult the global TOTP feature switch.
func (s *CryptoSecurityService) Status(ctx context.Context, userID int64) (*CryptoSecurityStatus, error) {
	u, err := s.CurrentOperator(ctx, userID)
	if err != nil {
		return nil, err
	}
	enabled := u.TotpEnabled && u.TotpSecretEncrypted != nil && *u.TotpSecretEncrypted != ""
	return &CryptoSecurityStatus{
		Enabled: enabled, FeatureEnabled: s.ready(),
		CanSetup: !u.TotpEnabled && s.ready() && !CryptoAdminTestReadOnly(),
		ReadOnly: CryptoAdminTestReadOnly(),
	}, nil
}

func (s *CryptoSecurityService) limit(ctx context.Context, userID int64, action string, max int, ttl time.Duration) error {
	if !s.ready() {
		return ErrCryptoTotpUnavailable
	}
	key := fmt.Sprintf("crypto:totp:{%d}:attempts:%s", userID, action)
	n, err := s.store.IncrementCryptoAttempt(ctx, key, ttl)
	if err != nil || n < 0 {
		return ErrCryptoTotpUnavailable
	}
	if n > max {
		return ErrCryptoTotpRateLimited
	}
	return nil
}

func (s *CryptoSecurityService) digest(purpose string, userID int64, value string) string {
	mac := hmac.New(sha256.New, s.key)
	fmt.Fprintf(mac, "%s\x00%d\x00%s", purpose, userID, value)
	return hex.EncodeToString(mac.Sum(nil))
}

func (s *CryptoSecurityService) consume(ctx context.Context, userID int64, counter int64, code string) error {
	// Counter is the matched TOTP time step, not the request arrival bucket.
	digest := s.digest("code", userID, fmt.Sprintf("%d:%s", counter, code))
	key := fmt.Sprintf("crypto:totp:{%d}:used:%s", userID, digest)
	ok, err := s.store.SetCryptoValueIfAbsent(ctx, key, "1", cryptoReplayTTL)
	if err != nil {
		return ErrCryptoTotpUnavailable
	}
	if !ok {
		return ErrCryptoTotpReplayed
	}
	return nil
}

func cryptoTotpError(err error) error {
	switch {
	case err == nil:
		return nil
	case errors.Is(err, ErrTotpNotSetup):
		return ErrCryptoTotpNotSetup
	case errors.Is(err, ErrTotpTooManyAttempts):
		return ErrCryptoTotpRateLimited
	case errors.Is(err, ErrTotpInvalidCode), errors.Is(err, ErrTotpSetupExpired):
		return ErrCryptoTotpRequired
	case errors.Is(err, ErrTotpAlreadyEnabled):
		return ErrCryptoTotpReplayed
	case errors.Is(err, ErrTotpUnavailable):
		return ErrCryptoTotpUnavailable
	case errors.Is(err, ErrCryptoTotpRequired), errors.Is(err, ErrCryptoTotpReplayed), errors.Is(err, ErrCryptoTotpUnavailable):
		return err
	default:
		return ErrCryptoTotpUnavailable
	}
}

// VerifyRisk requires a new code for each operation, even when login TOTP or
// step-up is disabled or a valid step-up grant already exists.
func (s *CryptoSecurityService) VerifyRisk(ctx context.Context, userID int64, code string) error {
	if _, err := s.CurrentOperator(ctx, userID); err != nil {
		return err
	}
	return s.verifyAdminRisk(ctx, userID, code)
}

// Ordinary admins still manage their own authenticator without acquiring
// wallet authority. They use the same atomic code-consumption controls.
func (s *CryptoSecurityService) verifyAdminRisk(ctx context.Context, userID int64, code string) error {
	_, err := s.verifyAdminRiskUser(ctx, userID, code)
	return err
}

// Keep the verified snapshot for conditional authenticator mutations.
func (s *CryptoSecurityService) verifyAdminRiskUser(ctx context.Context, userID int64, code string) (*User, error) {
	u, err := s.CurrentAdmin(ctx, userID)
	if err != nil {
		return nil, err
	}
	if !u.TotpEnabled || u.TotpSecretEncrypted == nil || *u.TotpSecretEncrypted == "" {
		return nil, ErrCryptoTotpNotSetup
	}
	if err := s.limit(ctx, userID, "verify", 30, time.Minute); err != nil {
		return nil, err
	}
	verified, err := s.totp.verifyCode(ctx, userID, code, func(counter int64) error {
		return s.consume(ctx, userID, counter, code)
	})
	if err != nil {
		return nil, cryptoTotpError(err)
	}
	if !verified.IsAdmin() || !verified.IsActive() || verified.DeletedAt != nil {
		return nil, ErrCryptoAdminRequired
	}
	return verified, nil
}

// Serialize enrollment across processes so two setup/enable requests cannot
// replace one another's seed or enable two different seeds concurrently.
func (s *CryptoSecurityService) lockSetup(ctx context.Context, userID int64) (func(), error) {
	if !s.ready() {
		return nil, ErrCryptoTotpUnavailable
	}
	token, err := generateRandomToken(32)
	if err != nil {
		return nil, ErrCryptoTotpUnavailable
	}
	key := fmt.Sprintf("crypto:totp:{%d}:setup-lock", userID)
	ok, err := s.store.SetCryptoValueIfAbsent(ctx, key, token, 30*time.Second)
	if err != nil {
		return nil, ErrCryptoTotpUnavailable
	}
	if !ok {
		return nil, ErrCryptoTotpReplayed
	}
	return func() {
		// Expiry is the safe fallback if cleanup fails or the caller disconnects.
		cleanup, cancel := context.WithTimeout(context.WithoutCancel(ctx), time.Second)
		defer cancel()
		_ = s.store.ReleaseCryptoValue(cleanup, key, token)
	}, nil
}

// InitiateSetup binds payment TOTP enrollment to the authenticated JWT session.
// The setup token and first TOTP code prove possession of the new authenticator.
func (s *CryptoSecurityService) InitiateSetup(ctx context.Context, userID int64, sessionID string) (*TotpSetupResponse, error) {
	if _, err := s.CurrentOperator(ctx, userID); err != nil {
		return nil, err
	}
	if err := s.limit(ctx, userID, "setup", 5, totpAttemptsTTL); err != nil {
		return nil, err
	}
	if sessionID == "" {
		return nil, ErrCryptoAdminRequired
	}
	unlock, err := s.lockSetup(ctx, userID)
	if err != nil {
		return nil, err
	}
	defer unlock()
	return s.totp.initiateSetup(ctx, userID, "", "", s.digest("session", userID, sessionID), true)
}

// Enable completes the shared enrollment with a six-digit code, never a
// client-supplied enabled flag, and consumes both the code and setup capability.
func (s *CryptoSecurityService) Enable(ctx context.Context, userID int64, sessionID, code, setupToken string) error {
	if _, err := s.CurrentOperator(ctx, userID); err != nil {
		return err
	}
	if err := s.limit(ctx, userID, "enable", 10, totpAttemptsTTL); err != nil {
		return err
	}
	if sessionID == "" {
		return ErrCryptoAdminRequired
	}
	unlock, err := s.lockSetup(ctx, userID)
	if err != nil {
		return err
	}
	defer unlock()
	usedToken := fmt.Sprintf("crypto:totp:{%d}:setup-used:%s", userID, s.digest("setup-token", userID, sessionID+":"+setupToken))
	used, err := s.store.CryptoValueExists(ctx, usedToken)
	if err != nil {
		return ErrCryptoTotpUnavailable
	}
	if used {
		return ErrCryptoTotpReplayed
	}
	return cryptoTotpError(s.totp.completeSetup(ctx, userID, code, setupToken, s.digest("session", userID, sessionID), func(counter int64) error {
		if err := s.consume(ctx, userID, counter, code); err != nil {
			return err
		}
		ok, err := s.store.SetCryptoValueIfAbsent(ctx, usedToken, "1", totpSetupTTL)
		if err != nil {
			return ErrCryptoTotpUnavailable
		}
		if !ok {
			return ErrCryptoTotpReplayed
		}
		return nil
	}))
}
