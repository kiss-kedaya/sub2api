package repository

import (
	"log"
	"time"

	"gorm.io/gorm"
)

// MaxExpiresAt is the maximum allowed expiration date for subscriptions (year 2099)
// This prevents time.Time JSON serialization errors (RFC 3339 requires year <= 9999)
var maxExpiresAt = time.Date(2099, 12, 31, 23, 59, 59, 0, time.UTC)

// AutoMigrate runs schema migrations for all repository persistence models.
// Persistence models are defined within individual `*_repo.go` files.
func AutoMigrate(db *gorm.DB) error {
	err := db.AutoMigrate(
		&userModel{},
		&apiKeyModel{},
		&groupModel{},
		&accountModel{},
		&accountGroupModel{},
		&proxyModel{},
		&redeemCodeModel{},
		&usageLogModel{},
		&settingModel{},
		&userSubscriptionModel{},
	)
	if err != nil {
		return err
	}

	// 修复无效的过期时间（年份超过 2099 会导致 JSON 序列化失败）
	return fixInvalidExpiresAt(db)
}

// fixInvalidExpiresAt 修复 user_subscriptions 表中无效的过期时间
func fixInvalidExpiresAt(db *gorm.DB) error {
	result := db.Model(&userSubscriptionModel{}).
		Where("expires_at > ?", maxExpiresAt).
		Update("expires_at", maxExpiresAt)
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected > 0 {
		log.Printf("[AutoMigrate] Fixed %d subscriptions with invalid expires_at (year > 2099)", result.RowsAffected)
	}
	return nil
}
