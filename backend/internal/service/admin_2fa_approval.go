package service

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strings"
	"time"

	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
)

const (
	Admin2FAApprovalTTL         = 10 * time.Minute
	admin2FAApprovalCodeBytes   = 32
	admin2FAApprovalMaxAttempts = 5
)

var (
	ErrAdmin2FAApprovalInvalid     = infraerrors.Forbidden("ADMIN_2FA_APPROVAL_INVALID", "invalid or expired server authorization code")
	ErrAdmin2FAApprovalUnavailable = infraerrors.ServiceUnavailable("ADMIN_2FA_APPROVAL_UNAVAILABLE", "server authorization is unavailable")
	ErrAdmin2FAApprovalRateLimited = infraerrors.TooManyRequests("ADMIN_2FA_APPROVAL_RATE_LIMITED", "too many server authorization attempts")
	ErrAdmin2FAApprovalReadOnly    = infraerrors.Forbidden("ADMIN_2FA_APPROVAL_READ_ONLY", "read-only crypto test mode forbids server authorization")
)

// Admin2FAApprovalUserReader always reads the current account security state.
type Admin2FAApprovalUserReader interface {
	GetByID(context.Context, int64) (*User, error)
}

// Admin2FAApprovalStore stores only a digest. A matching consume must atomically
// delete the digest, so parallel requests can never share one authorization.
type Admin2FAApprovalStore interface {
	SetAdmin2FAApproval(context.Context, int64, string, time.Duration) error
	ConsumeAdmin2FAApproval(context.Context, int64, string) (bool, error)
	IncrementCryptoAttempt(context.Context, string, time.Duration) (int, error)
}

type Admin2FAApprovalService struct {
	users Admin2FAApprovalUserReader
	store Admin2FAApprovalStore
}

func NewAdmin2FAApprovalService(users Admin2FAApprovalUserReader, store Admin2FAApprovalStore) *Admin2FAApprovalService {
	return &Admin2FAApprovalService{users: users, store: store}
}

// Issue is invoked only by the server CLI. There is intentionally no HTTP issue
// endpoint. Issuing another code replaces any earlier code for this account.
func (s *Admin2FAApprovalService) Issue(ctx context.Context, userID int64) (string, time.Time, error) {
	if CryptoAdminTestMode() {
		return "", time.Time{}, ErrAdmin2FAApprovalReadOnly
	}
	user, err := s.currentAdmin(ctx, userID)
	if err != nil {
		return "", time.Time{}, err
	}
	bytes := make([]byte, admin2FAApprovalCodeBytes)
	if _, err := rand.Read(bytes); err != nil {
		return "", time.Time{}, ErrAdmin2FAApprovalUnavailable
	}
	code := hex.EncodeToString(bytes)
	expiresAt := time.Now().Add(Admin2FAApprovalTTL)
	if err := s.store.SetAdmin2FAApproval(ctx, userID, admin2FAApprovalDigest(user, code), Admin2FAApprovalTTL); err != nil {
		return "", time.Time{}, ErrAdmin2FAApprovalUnavailable
	}
	return code, expiresAt, nil
}

// Consume verifies the current account and burns the code before returning its
// exact security snapshot. The caller must bind its database write to the
// returned role and encrypted TOTP secret. If that write fails, issue a new code.
func (s *Admin2FAApprovalService) Consume(ctx context.Context, userID int64, code string) (*User, error) {
	if CryptoAdminTestReadOnly() {
		return nil, ErrAdmin2FAApprovalReadOnly
	}
	if s == nil || s.users == nil || s.store == nil || userID <= 0 {
		return nil, ErrAdmin2FAApprovalUnavailable
	}
	key := fmt.Sprintf("totp:admin-approval:{%d}:attempts", userID)
	attempts, err := s.store.IncrementCryptoAttempt(ctx, key, Admin2FAApprovalTTL)
	if err != nil || attempts <= 0 {
		return nil, ErrAdmin2FAApprovalUnavailable
	}
	if attempts > admin2FAApprovalMaxAttempts {
		return nil, ErrAdmin2FAApprovalRateLimited
	}
	code = strings.TrimSpace(code)
	if len(code) != admin2FAApprovalCodeBytes*2 {
		return nil, ErrAdmin2FAApprovalInvalid
	}
	decoded, err := hex.DecodeString(code)
	if err != nil || len(decoded) != admin2FAApprovalCodeBytes {
		return nil, ErrAdmin2FAApprovalInvalid
	}
	// Normalize hex casing without widening the accepted code alphabet.
	code = hex.EncodeToString(decoded)
	user, err := s.currentAdmin(ctx, userID)
	if err != nil {
		return nil, err
	}
	consumed, err := s.store.ConsumeAdmin2FAApproval(ctx, userID, admin2FAApprovalDigest(user, code))
	if err != nil {
		return nil, ErrAdmin2FAApprovalUnavailable
	}
	if !consumed {
		return nil, ErrAdmin2FAApprovalInvalid
	}
	return user, nil
}

func (s *Admin2FAApprovalService) currentAdmin(ctx context.Context, userID int64) (*User, error) {
	if s == nil || s.users == nil || s.store == nil || userID <= 0 {
		return nil, ErrAdmin2FAApprovalUnavailable
	}
	user, err := s.users.GetByID(ctx, userID)
	if err != nil {
		return nil, ErrAdmin2FAApprovalUnavailable
	}
	if user == nil || user.ID != userID || user.DeletedAt != nil || !user.IsAdmin() || !user.IsActive() ||
		!user.TotpEnabled || user.TotpSecretEncrypted == nil || *user.TotpSecretEncrypted == "" {
		return nil, ErrAdmin2FAApprovalInvalid
	}
	return user, nil
}

func admin2FAApprovalDigest(user *User, code string) string {
	digest := sha256.New()
	// Bind both identity and the authenticator generation. A replacement TOTP
	// secret invalidates codes issued before re-enrollment without any key scan.
	fmt.Fprintf(digest, "admin-2fa-disable\x00%d\x00%s\x00%s", user.ID, *user.TotpSecretEncrypted, code)
	return hex.EncodeToString(digest.Sum(nil))
}
