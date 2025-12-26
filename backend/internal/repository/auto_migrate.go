package repository

import "gorm.io/gorm"

// AutoMigrate runs schema migrations for all repository persistence models.
// Persistence models are defined within individual `*_repo.go` files.
func AutoMigrate(db *gorm.DB) error {
	return db.AutoMigrate(
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
}
