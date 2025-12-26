package repository

import (
	"context"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/pagination"
	"github.com/Wei-Shaw/sub2api/internal/service"

	"gorm.io/gorm"
)

type userSubscriptionRepository struct {
	db *gorm.DB
}

func NewUserSubscriptionRepository(db *gorm.DB) service.UserSubscriptionRepository {
	return &userSubscriptionRepository{db: db}
}

func (r *userSubscriptionRepository) Create(ctx context.Context, sub *service.UserSubscription) error {
	m := userSubscriptionModelFromService(sub)
	err := r.db.WithContext(ctx).Create(m).Error
	if err == nil {
		applyUserSubscriptionModelToService(sub, m)
	}
	return translatePersistenceError(err, nil, service.ErrSubscriptionAlreadyExists)
}

func (r *userSubscriptionRepository) GetByID(ctx context.Context, id int64) (*service.UserSubscription, error) {
	var m userSubscriptionModel
	err := r.db.WithContext(ctx).
		Preload("User").
		Preload("Group").
		Preload("AssignedByUser").
		First(&m, id).Error
	if err != nil {
		return nil, translatePersistenceError(err, service.ErrSubscriptionNotFound, nil)
	}
	return userSubscriptionModelToService(&m), nil
}

func (r *userSubscriptionRepository) GetByUserIDAndGroupID(ctx context.Context, userID, groupID int64) (*service.UserSubscription, error) {
	var m userSubscriptionModel
	err := r.db.WithContext(ctx).
		Preload("Group").
		Where("user_id = ? AND group_id = ?", userID, groupID).
		First(&m).Error
	if err != nil {
		return nil, translatePersistenceError(err, service.ErrSubscriptionNotFound, nil)
	}
	return userSubscriptionModelToService(&m), nil
}

func (r *userSubscriptionRepository) GetActiveByUserIDAndGroupID(ctx context.Context, userID, groupID int64) (*service.UserSubscription, error) {
	var m userSubscriptionModel
	err := r.db.WithContext(ctx).
		Preload("Group").
		Where("user_id = ? AND group_id = ? AND status = ? AND expires_at > ?",
			userID, groupID, service.SubscriptionStatusActive, time.Now()).
		First(&m).Error
	if err != nil {
		return nil, translatePersistenceError(err, service.ErrSubscriptionNotFound, nil)
	}
	return userSubscriptionModelToService(&m), nil
}

func (r *userSubscriptionRepository) Update(ctx context.Context, sub *service.UserSubscription) error {
	sub.UpdatedAt = time.Now()
	m := userSubscriptionModelFromService(sub)
	err := r.db.WithContext(ctx).Save(m).Error
	if err == nil {
		applyUserSubscriptionModelToService(sub, m)
	}
	return err
}

func (r *userSubscriptionRepository) Delete(ctx context.Context, id int64) error {
	return r.db.WithContext(ctx).Delete(&userSubscriptionModel{}, id).Error
}

func (r *userSubscriptionRepository) ListByUserID(ctx context.Context, userID int64) ([]service.UserSubscription, error) {
	var subs []userSubscriptionModel
	err := r.db.WithContext(ctx).
		Preload("Group").
		Where("user_id = ?", userID).
		Order("created_at DESC").
		Find(&subs).Error
	if err != nil {
		return nil, err
	}
	return userSubscriptionModelsToService(subs), nil
}

func (r *userSubscriptionRepository) ListActiveByUserID(ctx context.Context, userID int64) ([]service.UserSubscription, error) {
	var subs []userSubscriptionModel
	err := r.db.WithContext(ctx).
		Preload("Group").
		Where("user_id = ? AND status = ? AND expires_at > ?",
			userID, service.SubscriptionStatusActive, time.Now()).
		Order("created_at DESC").
		Find(&subs).Error
	if err != nil {
		return nil, err
	}
	return userSubscriptionModelsToService(subs), nil
}

func (r *userSubscriptionRepository) ListByGroupID(ctx context.Context, groupID int64, params pagination.PaginationParams) ([]service.UserSubscription, *pagination.PaginationResult, error) {
	var subs []userSubscriptionModel
	var total int64

	query := r.db.WithContext(ctx).Model(&userSubscriptionModel{}).Where("group_id = ?", groupID)
	if err := query.Count(&total).Error; err != nil {
		return nil, nil, err
	}

	err := query.
		Preload("User").
		Preload("Group").
		Order("created_at DESC").
		Offset(params.Offset()).
		Limit(params.Limit()).
		Find(&subs).Error
	if err != nil {
		return nil, nil, err
	}

	return userSubscriptionModelsToService(subs), paginationResultFromTotal(total, params), nil
}

func (r *userSubscriptionRepository) List(ctx context.Context, params pagination.PaginationParams, userID, groupID *int64, status string) ([]service.UserSubscription, *pagination.PaginationResult, error) {
	var subs []userSubscriptionModel
	var total int64

	query := r.db.WithContext(ctx).Model(&userSubscriptionModel{})
	if userID != nil {
		query = query.Where("user_id = ?", *userID)
	}
	if groupID != nil {
		query = query.Where("group_id = ?", *groupID)
	}
	if status != "" {
		query = query.Where("status = ?", status)
	}

	if err := query.Count(&total).Error; err != nil {
		return nil, nil, err
	}

	err := query.
		Preload("User").
		Preload("Group").
		Preload("AssignedByUser").
		Order("created_at DESC").
		Offset(params.Offset()).
		Limit(params.Limit()).
		Find(&subs).Error
	if err != nil {
		return nil, nil, err
	}

	return userSubscriptionModelsToService(subs), paginationResultFromTotal(total, params), nil
}

func (r *userSubscriptionRepository) ExistsByUserIDAndGroupID(ctx context.Context, userID, groupID int64) (bool, error) {
	var count int64
	err := r.db.WithContext(ctx).Model(&userSubscriptionModel{}).
		Where("user_id = ? AND group_id = ?", userID, groupID).
		Count(&count).Error
	return count > 0, err
}

func (r *userSubscriptionRepository) ExtendExpiry(ctx context.Context, subscriptionID int64, newExpiresAt time.Time) error {
	return r.db.WithContext(ctx).Model(&userSubscriptionModel{}).
		Where("id = ?", subscriptionID).
		Updates(map[string]any{
			"expires_at": newExpiresAt,
			"updated_at": time.Now(),
		}).Error
}

func (r *userSubscriptionRepository) UpdateStatus(ctx context.Context, subscriptionID int64, status string) error {
	return r.db.WithContext(ctx).Model(&userSubscriptionModel{}).
		Where("id = ?", subscriptionID).
		Updates(map[string]any{
			"status":     status,
			"updated_at": time.Now(),
		}).Error
}

func (r *userSubscriptionRepository) UpdateNotes(ctx context.Context, subscriptionID int64, notes string) error {
	return r.db.WithContext(ctx).Model(&userSubscriptionModel{}).
		Where("id = ?", subscriptionID).
		Updates(map[string]any{
			"notes":      notes,
			"updated_at": time.Now(),
		}).Error
}

func (r *userSubscriptionRepository) ActivateWindows(ctx context.Context, id int64, start time.Time) error {
	return r.db.WithContext(ctx).Model(&userSubscriptionModel{}).
		Where("id = ?", id).
		Updates(map[string]any{
			"daily_window_start":   start,
			"weekly_window_start":  start,
			"monthly_window_start": start,
			"updated_at":           time.Now(),
		}).Error
}

func (r *userSubscriptionRepository) ResetDailyUsage(ctx context.Context, id int64, newWindowStart time.Time) error {
	return r.db.WithContext(ctx).Model(&userSubscriptionModel{}).
		Where("id = ?", id).
		Updates(map[string]any{
			"daily_usage_usd":    0,
			"daily_window_start": newWindowStart,
			"updated_at":         time.Now(),
		}).Error
}

func (r *userSubscriptionRepository) ResetWeeklyUsage(ctx context.Context, id int64, newWindowStart time.Time) error {
	return r.db.WithContext(ctx).Model(&userSubscriptionModel{}).
		Where("id = ?", id).
		Updates(map[string]any{
			"weekly_usage_usd":    0,
			"weekly_window_start": newWindowStart,
			"updated_at":          time.Now(),
		}).Error
}

func (r *userSubscriptionRepository) ResetMonthlyUsage(ctx context.Context, id int64, newWindowStart time.Time) error {
	return r.db.WithContext(ctx).Model(&userSubscriptionModel{}).
		Where("id = ?", id).
		Updates(map[string]any{
			"monthly_usage_usd":    0,
			"monthly_window_start": newWindowStart,
			"updated_at":           time.Now(),
		}).Error
}

func (r *userSubscriptionRepository) IncrementUsage(ctx context.Context, id int64, costUSD float64) error {
	return r.db.WithContext(ctx).Model(&userSubscriptionModel{}).
		Where("id = ?", id).
		Updates(map[string]any{
			"daily_usage_usd":   gorm.Expr("daily_usage_usd + ?", costUSD),
			"weekly_usage_usd":  gorm.Expr("weekly_usage_usd + ?", costUSD),
			"monthly_usage_usd": gorm.Expr("monthly_usage_usd + ?", costUSD),
			"updated_at":        time.Now(),
		}).Error
}

func (r *userSubscriptionRepository) BatchUpdateExpiredStatus(ctx context.Context) (int64, error) {
	result := r.db.WithContext(ctx).Model(&userSubscriptionModel{}).
		Where("status = ? AND expires_at <= ?", service.SubscriptionStatusActive, time.Now()).
		Updates(map[string]any{
			"status":     service.SubscriptionStatusExpired,
			"updated_at": time.Now(),
		})
	return result.RowsAffected, result.Error
}

// Extra repository helpers (currently used only by integration tests).

func (r *userSubscriptionRepository) ListExpired(ctx context.Context) ([]service.UserSubscription, error) {
	var subs []userSubscriptionModel
	err := r.db.WithContext(ctx).
		Where("status = ? AND expires_at <= ?", service.SubscriptionStatusActive, time.Now()).
		Find(&subs).Error
	if err != nil {
		return nil, err
	}
	return userSubscriptionModelsToService(subs), nil
}

func (r *userSubscriptionRepository) CountByGroupID(ctx context.Context, groupID int64) (int64, error) {
	var count int64
	err := r.db.WithContext(ctx).Model(&userSubscriptionModel{}).
		Where("group_id = ?", groupID).
		Count(&count).Error
	return count, err
}

func (r *userSubscriptionRepository) CountActiveByGroupID(ctx context.Context, groupID int64) (int64, error) {
	var count int64
	err := r.db.WithContext(ctx).Model(&userSubscriptionModel{}).
		Where("group_id = ? AND status = ? AND expires_at > ?",
			groupID, service.SubscriptionStatusActive, time.Now()).
		Count(&count).Error
	return count, err
}

func (r *userSubscriptionRepository) DeleteByGroupID(ctx context.Context, groupID int64) (int64, error) {
	result := r.db.WithContext(ctx).Where("group_id = ?", groupID).Delete(&userSubscriptionModel{})
	return result.RowsAffected, result.Error
}

type userSubscriptionModel struct {
	ID      int64 `gorm:"primaryKey"`
	UserID  int64 `gorm:"index;not null"`
	GroupID int64 `gorm:"index;not null"`

	StartsAt  time.Time `gorm:"not null"`
	ExpiresAt time.Time `gorm:"not null"`
	Status    string    `gorm:"size:20;default:active;not null"`

	DailyWindowStart   *time.Time
	WeeklyWindowStart  *time.Time
	MonthlyWindowStart *time.Time

	DailyUsageUSD   float64 `gorm:"type:decimal(20,10);default:0;not null"`
	WeeklyUsageUSD  float64 `gorm:"type:decimal(20,10);default:0;not null"`
	MonthlyUsageUSD float64 `gorm:"type:decimal(20,10);default:0;not null"`

	AssignedBy *int64    `gorm:"index"`
	AssignedAt time.Time `gorm:"not null"`
	Notes      string    `gorm:"type:text"`

	CreatedAt time.Time `gorm:"not null"`
	UpdatedAt time.Time `gorm:"not null"`

	User           *userModel  `gorm:"foreignKey:UserID"`
	Group          *groupModel `gorm:"foreignKey:GroupID"`
	AssignedByUser *userModel  `gorm:"foreignKey:AssignedBy"`
}

func (userSubscriptionModel) TableName() string { return "user_subscriptions" }

func userSubscriptionModelToService(m *userSubscriptionModel) *service.UserSubscription {
	if m == nil {
		return nil
	}
	return &service.UserSubscription{
		ID:                 m.ID,
		UserID:             m.UserID,
		GroupID:            m.GroupID,
		StartsAt:           m.StartsAt,
		ExpiresAt:          m.ExpiresAt,
		Status:             m.Status,
		DailyWindowStart:   m.DailyWindowStart,
		WeeklyWindowStart:  m.WeeklyWindowStart,
		MonthlyWindowStart: m.MonthlyWindowStart,
		DailyUsageUSD:      m.DailyUsageUSD,
		WeeklyUsageUSD:     m.WeeklyUsageUSD,
		MonthlyUsageUSD:    m.MonthlyUsageUSD,
		AssignedBy:         m.AssignedBy,
		AssignedAt:         m.AssignedAt,
		Notes:              m.Notes,
		CreatedAt:          m.CreatedAt,
		UpdatedAt:          m.UpdatedAt,
		User:               userModelToService(m.User),
		Group:              groupModelToService(m.Group),
		AssignedByUser:     userModelToService(m.AssignedByUser),
	}
}

func userSubscriptionModelsToService(models []userSubscriptionModel) []service.UserSubscription {
	out := make([]service.UserSubscription, 0, len(models))
	for i := range models {
		if s := userSubscriptionModelToService(&models[i]); s != nil {
			out = append(out, *s)
		}
	}
	return out
}

func userSubscriptionModelFromService(s *service.UserSubscription) *userSubscriptionModel {
	if s == nil {
		return nil
	}
	return &userSubscriptionModel{
		ID:                 s.ID,
		UserID:             s.UserID,
		GroupID:            s.GroupID,
		StartsAt:           s.StartsAt,
		ExpiresAt:          s.ExpiresAt,
		Status:             s.Status,
		DailyWindowStart:   s.DailyWindowStart,
		WeeklyWindowStart:  s.WeeklyWindowStart,
		MonthlyWindowStart: s.MonthlyWindowStart,
		DailyUsageUSD:      s.DailyUsageUSD,
		WeeklyUsageUSD:     s.WeeklyUsageUSD,
		MonthlyUsageUSD:    s.MonthlyUsageUSD,
		AssignedBy:         s.AssignedBy,
		AssignedAt:         s.AssignedAt,
		Notes:              s.Notes,
		CreatedAt:          s.CreatedAt,
		UpdatedAt:          s.UpdatedAt,
	}
}

func applyUserSubscriptionModelToService(sub *service.UserSubscription, m *userSubscriptionModel) {
	if sub == nil || m == nil {
		return
	}
	sub.ID = m.ID
	sub.CreatedAt = m.CreatedAt
	sub.UpdatedAt = m.UpdatedAt
}
