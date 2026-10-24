package service

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"time"

	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
)

// ErrCryptoGrantRequired is returned before a payment operation executes. The
// client may discard its grant and request a new authenticator verification.
var ErrCryptoGrantRequired = infraerrors.Forbidden("CRYPTO_GRANT_REQUIRED", "payment verification expired or is invalid; verify two-factor authentication again")

const cryptoGrantTTL = 5 * time.Minute

// CryptoVerificationGrant is a fixed-lifetime payment capability. It is not a
// login token and cannot authorize account administration outside crypto routes.
type CryptoVerificationGrant struct {
	Credential string    `json:"credential"`
	ExpiresAt  time.Time `json:"expires_at"`
	ExpiresIn  int64     `json:"expires_in"`
}

type cryptoGrantClaims struct {
	Version    int    `json:"v"`
	UserID     int64  `json:"u"`
	Session    string `json:"s"`
	State      string `json:"a"`
	Nonce      string `json:"n"`
	IssuedAt   int64  `json:"iat"`
	IssuedAtMS int64  `json:"iat_ms"`
	ExpiresAt  int64  `json:"exp"`
}

// CryptoGrantRevocationReader reads short-lived logout tombstones shared by
// the authenticator store and refresh-token revocation paths.
type CryptoGrantRevocationReader interface {
	CryptoGrantRevocationTimes(context.Context, int64, string) (int64, int64, error)
}

// CryptoGrantUserRevocationKey identifies only grant revocations, not JWTs.
func CryptoGrantUserRevocationKey(userID int64) string {
	return fmt.Sprintf("crypto:grant:revoked:user:%d", userID)
}

// CryptoGrantSessionRevocationKey avoids persisting plaintext session IDs.
func CryptoGrantSessionRevocationKey(sessionID string) string {
	digest := sha256.Sum256([]byte(sessionID))
	return fmt.Sprintf("crypto:grant:revoked:session:%x", digest)
}

// IssueRiskGrant consumes one fresh, replay-protected code and issues a payment
// grant bound to this administrator and the authenticated JWT session. Redis
// registration is mandatory, so signed tokens alone cannot bypass revocation.
func (s *CryptoSecurityService) IssueRiskGrant(ctx context.Context, userID int64, sessionID, code string) (*CryptoVerificationGrant, error) {
	if _, err := s.CurrentOperator(ctx, userID); err != nil {
		return nil, err
	}
	if !validCryptoGrantSession(sessionID) {
		return nil, ErrCryptoAdminRequired
	}
	user, err := s.verifyAdminRiskUser(ctx, userID, code)
	if err != nil {
		return nil, err
	}
	if _, ok := s.store.(CryptoGrantRevocationReader); !ok {
		return nil, ErrCryptoTotpUnavailable
	}
	nonce := make([]byte, 32)
	if _, err := rand.Read(nonce); err != nil {
		return nil, ErrCryptoTotpUnavailable
	}
	issuedAt := time.Now().UTC()
	now := issuedAt.Truncate(time.Second)
	claims := cryptoGrantClaims{
		Version: 1, UserID: userID,
		Session:  s.digest("grant-session", userID, sessionID),
		State:    s.grantState(user),
		Nonce:    base64.RawURLEncoding.EncodeToString(nonce),
		IssuedAt: now.Unix(), IssuedAtMS: issuedAt.UnixMilli(), ExpiresAt: now.Add(cryptoGrantTTL).Unix(),
	}
	credential, err := s.signRiskGrant(claims)
	if err != nil {
		return nil, ErrCryptoTotpUnavailable
	}
	// No plaintext authenticator, JWT, or grant is stored in Redis. This record
	// has an absolute expiry and is never refreshed by grant use.
	ttl := time.Until(time.Unix(claims.ExpiresAt, 0))
	if ttl <= 0 || ttl > cryptoGrantTTL {
		return nil, ErrCryptoTotpUnavailable
	}
	ok, err := s.store.SetCryptoValueIfAbsent(ctx, s.grantStoreKey(userID, credential), "1", ttl)
	if err != nil || !ok {
		return nil, ErrCryptoTotpUnavailable
	}
	return &CryptoVerificationGrant{
		Credential: credential, ExpiresAt: time.Unix(claims.ExpiresAt, 0).UTC(),
		ExpiresIn: int64(cryptoGrantTTL / time.Second),
	}, nil
}

// VerifyRiskGrant checks both the server signature and its shared registration.
// A fresh repository read invalidates grants after password/2FA changes,
// deactivation, demotion, deletion, or loss of payment-operator authority.
func (s *CryptoSecurityService) VerifyRiskGrant(ctx context.Context, userID int64, sessionID, credential string) error {
	user, err := s.CurrentOperator(ctx, userID)
	if err != nil {
		return err
	}
	if !s.ready() {
		return ErrCryptoTotpUnavailable
	}
	if !user.TotpEnabled || user.TotpSecretEncrypted == nil || *user.TotpSecretEncrypted == "" {
		return ErrCryptoTotpNotSetup
	}
	if !validCryptoGrantSession(sessionID) || len(credential) > 2048 {
		return ErrCryptoGrantRequired
	}
	parts := strings.Split(credential, ".")
	if len(parts) != 3 || parts[0] != "cpg1" {
		return ErrCryptoGrantRequired
	}
	provided, err := base64.RawURLEncoding.Strict().DecodeString(parts[2])
	if err != nil || len(provided) != sha256.Size || !hmac.Equal(provided, s.grantMAC(parts[1])) {
		return ErrCryptoGrantRequired
	}
	payload, err := base64.RawURLEncoding.Strict().DecodeString(parts[1])
	if err != nil {
		return ErrCryptoGrantRequired
	}
	var claims cryptoGrantClaims
	decoder := json.NewDecoder(bytes.NewReader(payload))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&claims) != nil || decoder.Decode(new(any)) != io.EOF {
		return ErrCryptoGrantRequired
	}
	canonical, err := json.Marshal(claims)
	if err != nil || !bytes.Equal(payload, canonical) {
		return ErrCryptoGrantRequired
	}
	nonce, err := base64.RawURLEncoding.Strict().DecodeString(claims.Nonce)
	now := time.Now().Unix()
	if err != nil || len(nonce) != 32 || claims.Version != 1 || claims.UserID != userID ||
		claims.IssuedAt <= 0 || claims.IssuedAt > now || claims.ExpiresAt <= now || claims.ExpiresAt-claims.IssuedAt != int64(cryptoGrantTTL/time.Second) ||
		claims.IssuedAtMS/1000 != claims.IssuedAt || claims.IssuedAtMS > time.Now().UnixMilli() ||
		!hmac.Equal([]byte(claims.Session), []byte(s.digest("grant-session", userID, sessionID))) ||
		!hmac.Equal([]byte(claims.State), []byte(s.grantState(user))) {
		return ErrCryptoGrantRequired
	}
	revocations, ok := s.store.(CryptoGrantRevocationReader)
	if !ok {
		return ErrCryptoTotpUnavailable
	}
	userRevokedAt, sessionRevokedAt, err := revocations.CryptoGrantRevocationTimes(ctx, userID, sessionID)
	if err != nil || userRevokedAt < 0 || sessionRevokedAt < 0 {
		return ErrCryptoTotpUnavailable
	}
	// Equality is deliberately revoked: a capability and logout in the same
	// millisecond cannot be ordered safely, so verification fails closed.
	if claims.IssuedAtMS <= userRevokedAt || claims.IssuedAtMS <= sessionRevokedAt {
		return ErrCryptoGrantRequired
	}
	exists, err := s.store.CryptoValueExists(ctx, s.grantStoreKey(userID, credential))
	if err != nil {
		return ErrCryptoTotpUnavailable
	}
	if !exists {
		return ErrCryptoGrantRequired
	}
	return nil
}

func (s *CryptoSecurityService) grantState(user *User) string {
	// Password and encrypted-secret fingerprints never leave the HMAC boundary.
	// Hash each independently framed field to avoid both delimiter ambiguities
	// and fallible timestamp serialization in this authorization boundary.
	enabledAt := ""
	if user.TotpEnabledAt != nil {
		enabledAt = user.TotpEnabledAt.UTC().Format(time.RFC3339Nano)
	}
	return s.digest("grant-state", user.ID,
		s.digest("grant-email", user.ID, strings.ToLower(strings.TrimSpace(user.Email)))+
			s.digest("grant-password", user.ID, user.PasswordHash)+
			s.digest("grant-version", user.ID, fmt.Sprintf("%d", resolvedTokenVersion(user)))+
			s.digest("grant-authenticator", user.ID, *user.TotpSecretEncrypted)+
			s.digest("grant-enabled-at", user.ID, enabledAt))
}

func validCryptoGrantSession(sessionID string) bool {
	if sessionID == "" || len(sessionID) > 512 {
		return false
	}
	for _, character := range []byte(sessionID) {
		if character <= 32 || character >= 127 {
			return false
		}
	}
	return true
}

func (s *CryptoSecurityService) grantMAC(payload string) []byte {
	mac := hmac.New(sha256.New, s.key)
	_, _ = mac.Write([]byte("crypto-payment-grant-v1\x00" + payload))
	return mac.Sum(nil)
}

func (s *CryptoSecurityService) signRiskGrant(claims cryptoGrantClaims) (string, error) {
	payload, err := json.Marshal(claims)
	if err != nil {
		return "", err
	}
	encoded := base64.RawURLEncoding.EncodeToString(payload)
	return "cpg1." + encoded + "." + base64.RawURLEncoding.EncodeToString(s.grantMAC(encoded)), nil
}

func (s *CryptoSecurityService) grantStoreKey(userID int64, credential string) string {
	return fmt.Sprintf("crypto:totp:{%d}:grant:%s", userID, s.digest("grant-token", userID, credential))
}
