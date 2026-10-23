package notify

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"

	"github.com/Wei-Shaw/sub2api/internal/notify/bark"
)

// DeviceKeyEncryptor encrypts/decrypts user-supplied Bark keys at rest.
// Implemented by the repository-layer AES encryptor.
type DeviceKeyEncryptor interface {
	EncryptString(plaintext string) (string, error)
	DecryptString(ciphertext string) (string, error)
}

// encryptorAdapter adapts the repository AESEncryptor (Encrypt/Decrypt) to
// the interface above without leaking that package into callers.
type encryptorAdapter struct {
	inner interface {
		Encrypt(plaintext string) (string, error)
		Decrypt(ciphertext string) (string, error)
	}
}

// AdaptEncryptor wraps any Encrypt/Decrypt pair for the Bark service.
func AdaptEncryptor(inner interface {
	Encrypt(plaintext string) (string, error)
	Decrypt(ciphertext string) (string, error)
}) DeviceKeyEncryptor {
	return &encryptorAdapter{inner: inner}
}

func (a *encryptorAdapter) EncryptString(plaintext string) (string, error) {
	return a.inner.Encrypt(plaintext)
}

func (a *encryptorAdapter) DecryptString(ciphertext string) (string, error) {
	return a.inner.Decrypt(ciphertext)
}

// UserBarkService reads per-user Bark settings and sends pushes.
// The encrypted device key never leaves this layer in plaintext form.
type UserBarkService struct {
	db        *sql.DB
	encryptor DeviceKeyEncryptor
	newSender func(string) bark.Sender
}

func NewUserBarkService(db *sql.DB, encryptor DeviceKeyEncryptor) *UserBarkService {
	return &UserBarkService{db: db, encryptor: encryptor, newSender: func(key string) bark.Sender { return bark.New(key) }}
}

var (
	ErrBarkKeyRequired   = errors.New("configure a Bark device key before enabling notifications")
	ErrBarkInvalidKey    = errors.New("invalid Bark device key")
	ErrBarkNotConfigured = errors.New("wallet owner Bark notifications are not configured or enabled")
)

// SendToUserRequired is for security alerts. A missing recipient is an error,
// so callers keep the durable alert pending instead of silently acknowledging it.
func (s *UserBarkService) SendToUserRequired(ctx context.Context, userID int64, n bark.Notification) error {
	if s == nil || s.db == nil || s.encryptor == nil || s.newSender == nil {
		return ErrBarkNotConfigured
	}
	var encrypted string
	var enabled bool
	err := s.db.QueryRowContext(ctx, `SELECT bark_device_key_encrypted, bark_notify_enabled FROM users
		WHERE id = $1 AND role = 'admin' AND status = 'active' AND deleted_at IS NULL`, userID).Scan(&encrypted, &enabled)
	if err != nil {
		return ErrBarkNotConfigured
	}
	if !enabled || strings.TrimSpace(encrypted) == "" {
		return ErrBarkNotConfigured
	}
	key, err := s.encryptor.DecryptString(encrypted)
	if err != nil || strings.TrimSpace(key) == "" {
		return errors.New("wallet owner Bark device key unavailable")
	}
	sender := s.newSender(key)
	if sender == nil {
		return ErrBarkNotConfigured
	}
	return sender.Send(ctx, n)
}

// Status reports whether the user has a key configured (never the key).
func (s *UserBarkService) Status(ctx context.Context, userID int64) (configured, enabled bool, err error) {
	var encrypted string
	err = s.db.QueryRowContext(ctx,
		`SELECT bark_device_key_encrypted, bark_notify_enabled FROM users WHERE id = $1`,
		userID).Scan(&encrypted, &enabled)
	if err != nil {
		return false, false, fmt.Errorf("read bark settings: %w", err)
	}
	return strings.TrimSpace(encrypted) != "", enabled, nil
}

// SendToUser delivers a push if the user has Bark enabled.
func (s *UserBarkService) SendToUser(ctx context.Context, userID int64, n bark.Notification) error {
	var encrypted string
	var enabled bool
	err := s.db.QueryRowContext(ctx,
		`SELECT bark_device_key_encrypted, bark_notify_enabled FROM users WHERE id = $1`,
		userID).Scan(&encrypted, &enabled)
	if err != nil {
		return fmt.Errorf("read bark settings: %w", err)
	}
	if !enabled || strings.TrimSpace(encrypted) == "" {
		return nil
	}
	key, err := s.encryptor.DecryptString(encrypted)
	if err != nil {
		return fmt.Errorf("decrypt bark key: %w", err)
	}
	return bark.New(key).Send(ctx, n)
}

// SendToAdmins delivers a push to every admin who configured Bark on their
// own profile. Operational alerts (upstream balance, watchdog) reuse the
// same per-user keys the profile card writes, so there is no second place
// to configure notifications.
func (s *UserBarkService) SendToAdmins(ctx context.Context, n bark.Notification) error {
	rows, err := s.db.QueryContext(ctx,
		`SELECT bark_device_key_encrypted FROM users
		  WHERE role = $1 AND bark_notify_enabled = TRUE
		    AND status = 'active' AND deleted_at IS NULL
		    AND bark_device_key_encrypted <> ''`, "admin")
	if err != nil {
		return fmt.Errorf("list admin bark keys: %w", err)
	}
	defer func() { _ = rows.Close() }()
	var encryptedKeys []string
	for rows.Next() {
		var encrypted string
		if err := rows.Scan(&encrypted); err != nil {
			return err
		}
		encryptedKeys = append(encryptedKeys, encrypted)
	}
	if err := rows.Err(); err != nil {
		return err
	}
	var firstErr error
	for _, encrypted := range encryptedKeys {
		key, err := s.encryptor.DecryptString(encrypted)
		if err != nil {
			if firstErr == nil {
				firstErr = fmt.Errorf("decrypt admin bark key: %w", err)
			}
			continue
		}
		if err := bark.New(key).Send(ctx, n); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	return firstErr
}
