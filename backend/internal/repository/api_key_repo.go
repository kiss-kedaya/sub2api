package repository

import (
	"context"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"

	"github.com/Wei-Shaw/sub2api/internal/pkg/pagination"

	"gorm.io/gorm"
)

type apiKeyRepository struct {
	db *gorm.DB
}

func NewApiKeyRepository(db *gorm.DB) service.ApiKeyRepository {
	return &apiKeyRepository{db: db}
}

func (r *apiKeyRepository) Create(ctx context.Context, key *service.ApiKey) error {
	m := apiKeyModelFromService(key)
	err := r.db.WithContext(ctx).Create(m).Error
	if err == nil {
		applyApiKeyModelToService(key, m)
	}
	return translatePersistenceError(err, nil, service.ErrApiKeyExists)
}

func (r *apiKeyRepository) GetByID(ctx context.Context, id int64) (*service.ApiKey, error) {
	var m apiKeyModel
	err := r.db.WithContext(ctx).Preload("User").Preload("Group").First(&m, id).Error
	if err != nil {
		return nil, translatePersistenceError(err, service.ErrApiKeyNotFound, nil)
	}
	return apiKeyModelToService(&m), nil
}

func (r *apiKeyRepository) GetByKey(ctx context.Context, key string) (*service.ApiKey, error) {
	var m apiKeyModel
	err := r.db.WithContext(ctx).Preload("User").Preload("Group").Where("key = ?", key).First(&m).Error
	if err != nil {
		return nil, translatePersistenceError(err, service.ErrApiKeyNotFound, nil)
	}
	return apiKeyModelToService(&m), nil
}

func (r *apiKeyRepository) Update(ctx context.Context, key *service.ApiKey) error {
	m := apiKeyModelFromService(key)
	err := r.db.WithContext(ctx).Model(m).Select("name", "group_id", "status", "updated_at").Updates(m).Error
	if err == nil {
		applyApiKeyModelToService(key, m)
	}
	return err
}

func (r *apiKeyRepository) Delete(ctx context.Context, id int64) error {
	return r.db.WithContext(ctx).Delete(&apiKeyModel{}, id).Error
}

func (r *apiKeyRepository) ListByUserID(ctx context.Context, userID int64, params pagination.PaginationParams) ([]service.ApiKey, *pagination.PaginationResult, error) {
	var keys []apiKeyModel
	var total int64

	db := r.db.WithContext(ctx).Model(&apiKeyModel{}).Where("user_id = ?", userID)

	if err := db.Count(&total).Error; err != nil {
		return nil, nil, err
	}

	if err := db.Preload("Group").Offset(params.Offset()).Limit(params.Limit()).Order("id DESC").Find(&keys).Error; err != nil {
		return nil, nil, err
	}

	outKeys := make([]service.ApiKey, 0, len(keys))
	for i := range keys {
		outKeys = append(outKeys, *apiKeyModelToService(&keys[i]))
	}

	return outKeys, paginationResultFromTotal(total, params), nil
}

func (r *apiKeyRepository) CountByUserID(ctx context.Context, userID int64) (int64, error) {
	var count int64
	err := r.db.WithContext(ctx).Model(&apiKeyModel{}).Where("user_id = ?", userID).Count(&count).Error
	return count, err
}

func (r *apiKeyRepository) ExistsByKey(ctx context.Context, key string) (bool, error) {
	var count int64
	err := r.db.WithContext(ctx).Model(&apiKeyModel{}).Where("key = ?", key).Count(&count).Error
	return count > 0, err
}

func (r *apiKeyRepository) ListByGroupID(ctx context.Context, groupID int64, params pagination.PaginationParams) ([]service.ApiKey, *pagination.PaginationResult, error) {
	var keys []apiKeyModel
	var total int64

	db := r.db.WithContext(ctx).Model(&apiKeyModel{}).Where("group_id = ?", groupID)

	if err := db.Count(&total).Error; err != nil {
		return nil, nil, err
	}

	if err := db.Preload("User").Offset(params.Offset()).Limit(params.Limit()).Order("id DESC").Find(&keys).Error; err != nil {
		return nil, nil, err
	}

	outKeys := make([]service.ApiKey, 0, len(keys))
	for i := range keys {
		outKeys = append(outKeys, *apiKeyModelToService(&keys[i]))
	}

	return outKeys, paginationResultFromTotal(total, params), nil
}

// SearchApiKeys searches API keys by user ID and/or keyword (name)
func (r *apiKeyRepository) SearchApiKeys(ctx context.Context, userID int64, keyword string, limit int) ([]service.ApiKey, error) {
	var keys []apiKeyModel

	db := r.db.WithContext(ctx).Model(&apiKeyModel{})

	if userID > 0 {
		db = db.Where("user_id = ?", userID)
	}

	if keyword != "" {
		searchPattern := "%" + keyword + "%"
		db = db.Where("name ILIKE ?", searchPattern)
	}

	if err := db.Limit(limit).Order("id DESC").Find(&keys).Error; err != nil {
		return nil, err
	}

	outKeys := make([]service.ApiKey, 0, len(keys))
	for i := range keys {
		outKeys = append(outKeys, *apiKeyModelToService(&keys[i]))
	}
	return outKeys, nil
}

// ClearGroupIDByGroupID 将指定分组的所有 API Key 的 group_id 设为 nil
func (r *apiKeyRepository) ClearGroupIDByGroupID(ctx context.Context, groupID int64) (int64, error) {
	result := r.db.WithContext(ctx).Model(&apiKeyModel{}).
		Where("group_id = ?", groupID).
		Update("group_id", nil)
	return result.RowsAffected, result.Error
}

// CountByGroupID 获取分组的 API Key 数量
func (r *apiKeyRepository) CountByGroupID(ctx context.Context, groupID int64) (int64, error) {
	var count int64
	err := r.db.WithContext(ctx).Model(&apiKeyModel{}).Where("group_id = ?", groupID).Count(&count).Error
	return count, err
}

type apiKeyModel struct {
	ID        int64          `gorm:"primaryKey"`
	UserID    int64          `gorm:"index;not null"`
	Key       string         `gorm:"uniqueIndex;size:128;not null"`
	Name      string         `gorm:"size:100;not null"`
	GroupID   *int64         `gorm:"index"`
	Status    string         `gorm:"size:20;default:active;not null"`
	CreatedAt time.Time      `gorm:"not null"`
	UpdatedAt time.Time      `gorm:"not null"`
	DeletedAt gorm.DeletedAt `gorm:"index"`

	User  *userModel  `gorm:"foreignKey:UserID"`
	Group *groupModel `gorm:"foreignKey:GroupID"`
}

func (apiKeyModel) TableName() string { return "api_keys" }

func apiKeyModelToService(m *apiKeyModel) *service.ApiKey {
	if m == nil {
		return nil
	}
	return &service.ApiKey{
		ID:        m.ID,
		UserID:    m.UserID,
		Key:       m.Key,
		Name:      m.Name,
		GroupID:   m.GroupID,
		Status:    m.Status,
		CreatedAt: m.CreatedAt,
		UpdatedAt: m.UpdatedAt,
		User:      userModelToService(m.User),
		Group:     groupModelToService(m.Group),
	}
}

func apiKeyModelFromService(k *service.ApiKey) *apiKeyModel {
	if k == nil {
		return nil
	}
	return &apiKeyModel{
		ID:        k.ID,
		UserID:    k.UserID,
		Key:       k.Key,
		Name:      k.Name,
		GroupID:   k.GroupID,
		Status:    k.Status,
		CreatedAt: k.CreatedAt,
		UpdatedAt: k.UpdatedAt,
	}
}

func applyApiKeyModelToService(key *service.ApiKey, m *apiKeyModel) {
	if key == nil || m == nil {
		return
	}
	key.ID = m.ID
	key.CreatedAt = m.CreatedAt
	key.UpdatedAt = m.UpdatedAt
}
