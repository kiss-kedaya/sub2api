package repository

import (
	"context"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"

	"github.com/Wei-Shaw/sub2api/internal/pkg/pagination"

	"github.com/lib/pq"
	"gorm.io/gorm"
)

type userRepository struct {
	db *gorm.DB
}

func NewUserRepository(db *gorm.DB) service.UserRepository {
	return &userRepository{db: db}
}

func (r *userRepository) Create(ctx context.Context, user *service.User) error {
	m := userModelFromService(user)
	err := r.db.WithContext(ctx).Create(m).Error
	if err == nil {
		applyUserModelToService(user, m)
	}
	return translatePersistenceError(err, nil, service.ErrEmailExists)
}

func (r *userRepository) GetByID(ctx context.Context, id int64) (*service.User, error) {
	var m userModel
	err := r.db.WithContext(ctx).First(&m, id).Error
	if err != nil {
		return nil, translatePersistenceError(err, service.ErrUserNotFound, nil)
	}
	return userModelToService(&m), nil
}

func (r *userRepository) GetByEmail(ctx context.Context, email string) (*service.User, error) {
	var m userModel
	err := r.db.WithContext(ctx).Where("email = ?", email).First(&m).Error
	if err != nil {
		return nil, translatePersistenceError(err, service.ErrUserNotFound, nil)
	}
	return userModelToService(&m), nil
}

func (r *userRepository) Update(ctx context.Context, user *service.User) error {
	m := userModelFromService(user)
	err := r.db.WithContext(ctx).Save(m).Error
	if err == nil {
		applyUserModelToService(user, m)
	}
	return translatePersistenceError(err, nil, service.ErrEmailExists)
}

func (r *userRepository) Delete(ctx context.Context, id int64) error {
	return r.db.WithContext(ctx).Delete(&userModel{}, id).Error
}

func (r *userRepository) List(ctx context.Context, params pagination.PaginationParams) ([]service.User, *pagination.PaginationResult, error) {
	return r.ListWithFilters(ctx, params, "", "", "")
}

// ListWithFilters lists users with optional filtering by status, role, and search query
func (r *userRepository) ListWithFilters(ctx context.Context, params pagination.PaginationParams, status, role, search string) ([]service.User, *pagination.PaginationResult, error) {
	var users []userModel
	var total int64

	db := r.db.WithContext(ctx).Model(&userModel{})

	// Apply filters
	if status != "" {
		db = db.Where("status = ?", status)
	}
	if role != "" {
		db = db.Where("role = ?", role)
	}
	if search != "" {
		searchPattern := "%" + search + "%"
		db = db.Where(
			"email ILIKE ? OR username ILIKE ? OR wechat ILIKE ?",
			searchPattern, searchPattern, searchPattern,
		)
	}

	if err := db.Count(&total).Error; err != nil {
		return nil, nil, err
	}

	// Query users with pagination (reuse the same db with filters applied)
	if err := db.Offset(params.Offset()).Limit(params.Limit()).Order("id DESC").Find(&users).Error; err != nil {
		return nil, nil, err
	}

	// Batch load subscriptions for all users (avoid N+1)
	if len(users) > 0 {
		userIDs := make([]int64, len(users))
		userMap := make(map[int64]*service.User, len(users))
		outUsers := make([]service.User, 0, len(users))
		for i := range users {
			userIDs[i] = users[i].ID
			u := userModelToService(&users[i])
			outUsers = append(outUsers, *u)
			userMap[u.ID] = &outUsers[len(outUsers)-1]
		}

		// Query active subscriptions with groups in one query
		var subscriptions []userSubscriptionModel
		if err := r.db.WithContext(ctx).
			Preload("Group").
			Where("user_id IN ? AND status = ?", userIDs, service.SubscriptionStatusActive).
			Find(&subscriptions).Error; err != nil {
			return nil, nil, err
		}

		// Associate subscriptions with users
		for i := range subscriptions {
			if user, ok := userMap[subscriptions[i].UserID]; ok {
				user.Subscriptions = append(user.Subscriptions, *userSubscriptionModelToService(&subscriptions[i]))
			}
		}

		return outUsers, paginationResultFromTotal(total, params), nil
	}

	outUsers := make([]service.User, 0, len(users))
	for i := range users {
		outUsers = append(outUsers, *userModelToService(&users[i]))
	}

	return outUsers, paginationResultFromTotal(total, params), nil
}

func (r *userRepository) UpdateBalance(ctx context.Context, id int64, amount float64) error {
	return r.db.WithContext(ctx).Model(&userModel{}).Where("id = ?", id).
		Update("balance", gorm.Expr("balance + ?", amount)).Error
}

// DeductBalance 扣减用户余额，仅当余额充足时执行
func (r *userRepository) DeductBalance(ctx context.Context, id int64, amount float64) error {
	result := r.db.WithContext(ctx).Model(&userModel{}).
		Where("id = ? AND balance >= ?", id, amount).
		Update("balance", gorm.Expr("balance - ?", amount))
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected == 0 {
		return service.ErrInsufficientBalance
	}
	return nil
}

func (r *userRepository) UpdateConcurrency(ctx context.Context, id int64, amount int) error {
	return r.db.WithContext(ctx).Model(&userModel{}).Where("id = ?", id).
		Update("concurrency", gorm.Expr("concurrency + ?", amount)).Error
}

func (r *userRepository) ExistsByEmail(ctx context.Context, email string) (bool, error) {
	var count int64
	err := r.db.WithContext(ctx).Model(&userModel{}).Where("email = ?", email).Count(&count).Error
	return count > 0, err
}

// RemoveGroupFromAllowedGroups 从所有用户的 allowed_groups 数组中移除指定的分组ID
// 使用 PostgreSQL 的 array_remove 函数
func (r *userRepository) RemoveGroupFromAllowedGroups(ctx context.Context, groupID int64) (int64, error) {
	result := r.db.WithContext(ctx).Model(&userModel{}).
		Where("? = ANY(allowed_groups)", groupID).
		Update("allowed_groups", gorm.Expr("array_remove(allowed_groups, ?)", groupID))
	return result.RowsAffected, result.Error
}

// GetFirstAdmin 获取第一个管理员用户（用于 Admin API Key 认证）
func (r *userRepository) GetFirstAdmin(ctx context.Context) (*service.User, error) {
	var m userModel
	err := r.db.WithContext(ctx).
		Where("role = ? AND status = ?", service.RoleAdmin, service.StatusActive).
		Order("id ASC").
		First(&m).Error
	if err != nil {
		return nil, translatePersistenceError(err, service.ErrUserNotFound, nil)
	}
	return userModelToService(&m), nil
}

type userModel struct {
	ID            int64          `gorm:"primaryKey"`
	Email         string         `gorm:"uniqueIndex;size:255;not null"`
	Username      string         `gorm:"size:100;default:''"`
	Wechat        string         `gorm:"size:100;default:''"`
	Notes         string         `gorm:"type:text;default:''"`
	PasswordHash  string         `gorm:"size:255;not null"`
	Role          string         `gorm:"size:20;default:user;not null"`
	Balance       float64        `gorm:"type:decimal(20,8);default:0;not null"`
	Concurrency   int            `gorm:"default:5;not null"`
	Status        string         `gorm:"size:20;default:active;not null"`
	AllowedGroups pq.Int64Array  `gorm:"type:bigint[]"`
	CreatedAt     time.Time      `gorm:"not null"`
	UpdatedAt     time.Time      `gorm:"not null"`
	DeletedAt     gorm.DeletedAt `gorm:"index"`
}

func (userModel) TableName() string { return "users" }

func userModelToService(m *userModel) *service.User {
	if m == nil {
		return nil
	}
	return &service.User{
		ID:            m.ID,
		Email:         m.Email,
		Username:      m.Username,
		Wechat:        m.Wechat,
		Notes:         m.Notes,
		PasswordHash:  m.PasswordHash,
		Role:          m.Role,
		Balance:       m.Balance,
		Concurrency:   m.Concurrency,
		Status:        m.Status,
		AllowedGroups: []int64(m.AllowedGroups),
		CreatedAt:     m.CreatedAt,
		UpdatedAt:     m.UpdatedAt,
	}
}

func userModelFromService(u *service.User) *userModel {
	if u == nil {
		return nil
	}
	return &userModel{
		ID:            u.ID,
		Email:         u.Email,
		Username:      u.Username,
		Wechat:        u.Wechat,
		Notes:         u.Notes,
		PasswordHash:  u.PasswordHash,
		Role:          u.Role,
		Balance:       u.Balance,
		Concurrency:   u.Concurrency,
		Status:        u.Status,
		AllowedGroups: pq.Int64Array(u.AllowedGroups),
		CreatedAt:     u.CreatedAt,
		UpdatedAt:     u.UpdatedAt,
	}
}

func applyUserModelToService(dst *service.User, src *userModel) {
	if dst == nil || src == nil {
		return
	}
	dst.ID = src.ID
	dst.CreatedAt = src.CreatedAt
	dst.UpdatedAt = src.UpdatedAt
}
