package notify

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/notify/bark"
)

var ErrBarkOriginalNotificationFailed = errors.New("original Bark device could not be notified; settings were not changed")

// UpdateSettings runs only after fresh account 2FA. Keep the original row
// locked through its notification, so concurrent changes cannot redirect it.
func (s *UserBarkService) UpdateSettings(ctx context.Context, userID int64, deviceKey *string, enabled bool) error {
	if s == nil || s.db == nil || s.encryptor == nil {
		return ErrBarkNotConfigured
	}
	var nextKey string
	if deviceKey != nil {
		nextKey = strings.TrimSpace(*deviceKey)
		if len(nextKey) > 256 || strings.ContainsAny(nextKey, "\r\n\t /?#") {
			return ErrBarkInvalidKey
		}
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	var encrypted string
	var wasEnabled bool
	err = tx.QueryRowContext(ctx, `SELECT bark_device_key_encrypted,bark_notify_enabled FROM users WHERE id=$1 AND status='active' AND deleted_at IS NULL FOR UPDATE`, userID).Scan(&encrypted, &wasEnabled)
	if err != nil {
		return err
	}
	nextEncrypted := encrypted
	if deviceKey != nil {
		if nextKey == "" {
			nextEncrypted, enabled = "", false
		} else {
			nextEncrypted, err = s.encryptor.EncryptString(nextKey)
			if err != nil {
				return errors.New("bark key encryption unavailable")
			}
		}
	}
	if enabled && nextEncrypted == "" {
		return ErrBarkKeyRequired
	}
	if deviceKey == nil && enabled == wasEnabled {
		return nil
	}
	if encrypted != "" {
		oldKey, err := s.encryptor.DecryptString(encrypted)
		if err != nil || oldKey == "" || s.newSender == nil {
			return ErrBarkOriginalNotificationFailed
		}
		if deviceKey != nil && nextKey == oldKey && enabled == wasEnabled {
			return nil
		}
		sender := s.newSender(oldKey)
		if sender == nil {
			return ErrBarkOriginalNotificationFailed
		}
		alertCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
		err = sender.Send(alertCtx, bark.Notification{Title: "Bark 通知配置即将修改", Body: "当前账号已通过新鲜 2FA 验证，正在修改或清除 Bark 设备 key、通知开关。此消息发送至修改前的设备；若非本人操作，请立即核查账号。", Group: "account-security", Level: "timeSensitive"})
		cancel()
		if err != nil {
			return ErrBarkOriginalNotificationFailed
		}
	}
	_, err = tx.ExecContext(ctx, `UPDATE users SET bark_device_key_encrypted=$1,bark_notify_enabled=$2 WHERE id=$3`, nextEncrypted, enabled, userID)
	if err != nil {
		return err
	}
	return tx.Commit()
}

// WalletOperationNotificationsEnabled permits portable deployments without
// Bark. Once a device is configured and enabled, operation delivery is required.
func (s *UserBarkService) WalletOperationNotificationsEnabled(ctx context.Context, userID int64) (bool, error) {
	configured, enabled, err := s.Status(ctx, userID)
	return configured && enabled, err
}
