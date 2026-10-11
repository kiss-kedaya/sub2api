package notify

import (
	"context"
	"database/sql"
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
}

func NewUserBarkService(db *sql.DB, encryptor DeviceKeyEncryptor) *UserBarkService {
	return &UserBarkService{db: db, encryptor: encryptor}
}

// SetDeviceKey stores (or clears) the user's Bark device key and toggle.
// An empty key disables the channel.
func (s *UserBarkService) SetDeviceKey(ctx context.Context, userID int64, deviceKey string, enabled bool) error {
	deviceKey = strings.TrimSpace(deviceKey)
	encrypted := ""
	if deviceKey != "" {
		var err error
		encrypted, err = s.encryptor.EncryptString(deviceKey)
		if err != nil {
			return fmt.Errorf("encrypt bark key: %w", err)
		}
	} else {
		enabled = false
	}
	_, err := s.db.ExecContext(ctx,
		`UPDATE users SET bark_device_key_encrypted = $1, bark_notify_enabled = $2 WHERE id = $3`,
		encrypted, enabled, userID)
	if err != nil {
		return fmt.Errorf("save bark settings: %w", err)
	}
	return nil
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
