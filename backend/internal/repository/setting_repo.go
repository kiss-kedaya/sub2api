package repository

import (
	"context"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

type settingRepository struct {
	db *gorm.DB
}

func NewSettingRepository(db *gorm.DB) service.SettingRepository {
	return &settingRepository{db: db}
}

func (r *settingRepository) Get(ctx context.Context, key string) (*service.Setting, error) {
	var m settingModel
	err := r.db.WithContext(ctx).Where("key = ?", key).First(&m).Error
	if err != nil {
		return nil, translatePersistenceError(err, service.ErrSettingNotFound, nil)
	}
	return settingModelToService(&m), nil
}

func (r *settingRepository) GetValue(ctx context.Context, key string) (string, error) {
	setting, err := r.Get(ctx, key)
	if err != nil {
		return "", err
	}
	return setting.Value, nil
}

func (r *settingRepository) Set(ctx context.Context, key, value string) error {
	m := &settingModel{
		Key:       key,
		Value:     value,
		UpdatedAt: time.Now(),
	}

	return r.db.WithContext(ctx).Clauses(clause.OnConflict{
		Columns:   []clause.Column{{Name: "key"}},
		DoUpdates: clause.AssignmentColumns([]string{"value", "updated_at"}),
	}).Create(m).Error
}

func (r *settingRepository) GetMultiple(ctx context.Context, keys []string) (map[string]string, error) {
	var settings []settingModel
	err := r.db.WithContext(ctx).Where("key IN ?", keys).Find(&settings).Error
	if err != nil {
		return nil, err
	}

	result := make(map[string]string)
	for _, s := range settings {
		result[s.Key] = s.Value
	}
	return result, nil
}

func (r *settingRepository) SetMultiple(ctx context.Context, settings map[string]string) error {
	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		for key, value := range settings {
			m := &settingModel{
				Key:       key,
				Value:     value,
				UpdatedAt: time.Now(),
			}
			if err := tx.Clauses(clause.OnConflict{
				Columns:   []clause.Column{{Name: "key"}},
				DoUpdates: clause.AssignmentColumns([]string{"value", "updated_at"}),
			}).Create(m).Error; err != nil {
				return err
			}
		}
		return nil
	})
}

func (r *settingRepository) GetAll(ctx context.Context) (map[string]string, error) {
	var settings []settingModel
	err := r.db.WithContext(ctx).Find(&settings).Error
	if err != nil {
		return nil, err
	}

	result := make(map[string]string)
	for _, s := range settings {
		result[s.Key] = s.Value
	}
	return result, nil
}

func (r *settingRepository) Delete(ctx context.Context, key string) error {
	return r.db.WithContext(ctx).Where("key = ?", key).Delete(&settingModel{}).Error
}

type settingModel struct {
	ID        int64     `gorm:"primaryKey"`
	Key       string    `gorm:"uniqueIndex;size:100;not null"`
	Value     string    `gorm:"type:text;not null"`
	UpdatedAt time.Time `gorm:"not null"`
}

func (settingModel) TableName() string { return "settings" }

func settingModelToService(m *settingModel) *service.Setting {
	if m == nil {
		return nil
	}
	return &service.Setting{
		ID:        m.ID,
		Key:       m.Key,
		Value:     m.Value,
		UpdatedAt: m.UpdatedAt,
	}
}
