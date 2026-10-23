package service

import (
	"context"
	"errors"

	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
)

var ErrTotpCodeReplayed = infraerrors.Conflict("TOTP_CODE_REPLAYED", "This verification code has already been used; enter a new code")

// VerifyFreshCode reuses the existing authenticator, attempt budget and shared
// one-use counter. It issues no session grant and ignores global UI switches.
func (s *TotpService) VerifyFreshCode(ctx context.Context, userID int64, code string) error {
	if s == nil || s.cryptoSecurity == nil || !s.cryptoSecurity.ready() {
		return ErrTotpUnavailable
	}
	u, err := s.verifyCode(ctx, userID, code, func(counter int64) error {
		return s.cryptoSecurity.consume(ctx, userID, counter, code)
	})
	if errors.Is(err, ErrCryptoTotpReplayed) {
		return ErrTotpCodeReplayed
	}
	if errors.Is(err, ErrCryptoTotpUnavailable) {
		return ErrTotpUnavailable
	}
	if err != nil {
		return err
	}
	if u.ID != userID || !u.IsActive() || u.DeletedAt != nil {
		return ErrTotpUnavailable
	}
	return nil
}
