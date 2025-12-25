package repository

import (
	"context"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"

	"github.com/Wei-Shaw/sub2api/internal/model"
	"github.com/Wei-Shaw/sub2api/internal/pkg/pagination"

	"gorm.io/gorm"
)

type redeemCodeRepository struct {
	db *gorm.DB
}

func NewRedeemCodeRepository(db *gorm.DB) service.RedeemCodeRepository {
	return &redeemCodeRepository{db: db}
}

func (r *redeemCodeRepository) Create(ctx context.Context, code *model.RedeemCode) error {
	return r.db.WithContext(ctx).Create(code).Error
}

func (r *redeemCodeRepository) CreateBatch(ctx context.Context, codes []model.RedeemCode) error {
	return r.db.WithContext(ctx).Create(&codes).Error
}

func (r *redeemCodeRepository) GetByID(ctx context.Context, id int64) (*model.RedeemCode, error) {
	var code model.RedeemCode
	err := r.db.WithContext(ctx).First(&code, id).Error
	if err != nil {
		return nil, translatePersistenceError(err, service.ErrRedeemCodeNotFound, nil)
	}
	return &code, nil
}

func (r *redeemCodeRepository) GetByCode(ctx context.Context, code string) (*model.RedeemCode, error) {
	var redeemCode model.RedeemCode
	err := r.db.WithContext(ctx).Where("code = ?", code).First(&redeemCode).Error
	if err != nil {
		return nil, translatePersistenceError(err, service.ErrRedeemCodeNotFound, nil)
	}
	return &redeemCode, nil
}

func (r *redeemCodeRepository) Delete(ctx context.Context, id int64) error {
	return r.db.WithContext(ctx).Delete(&model.RedeemCode{}, id).Error
}

func (r *redeemCodeRepository) List(ctx context.Context, params pagination.PaginationParams) ([]model.RedeemCode, *pagination.PaginationResult, error) {
	return r.ListWithFilters(ctx, params, "", "", "")
}

// ListWithFilters lists redeem codes with optional filtering by type, status, and search query
func (r *redeemCodeRepository) ListWithFilters(ctx context.Context, params pagination.PaginationParams, codeType, status, search string) ([]model.RedeemCode, *pagination.PaginationResult, error) {
	var codes []model.RedeemCode
	var total int64

	db := r.db.WithContext(ctx).Model(&model.RedeemCode{})

	// Apply filters
	if codeType != "" {
		db = db.Where("type = ?", codeType)
	}
	if status != "" {
		db = db.Where("status = ?", status)
	}
	if search != "" {
		searchPattern := "%" + search + "%"
		db = db.Where("code ILIKE ?", searchPattern)
	}

	if err := db.Count(&total).Error; err != nil {
		return nil, nil, err
	}

	if err := db.Preload("User").Preload("Group").Offset(params.Offset()).Limit(params.Limit()).Order("id DESC").Find(&codes).Error; err != nil {
		return nil, nil, err
	}

	pages := int(total) / params.Limit()
	if int(total)%params.Limit() > 0 {
		pages++
	}

	return codes, &pagination.PaginationResult{
		Total:    total,
		Page:     params.Page,
		PageSize: params.Limit(),
		Pages:    pages,
	}, nil
}

func (r *redeemCodeRepository) Update(ctx context.Context, code *model.RedeemCode) error {
	return r.db.WithContext(ctx).Save(code).Error
}

func (r *redeemCodeRepository) Use(ctx context.Context, id, userID int64) error {
	now := time.Now()
	result := r.db.WithContext(ctx).Model(&model.RedeemCode{}).
		Where("id = ? AND status = ?", id, model.StatusUnused).
		Updates(map[string]any{
			"status":  model.StatusUsed,
			"used_by": userID,
			"used_at": now,
		})
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected == 0 {
		return service.ErrRedeemCodeUsed.WithCause(gorm.ErrRecordNotFound)
	}
	return nil
}

// ListByUser returns all redeem codes used by a specific user
func (r *redeemCodeRepository) ListByUser(ctx context.Context, userID int64, limit int) ([]model.RedeemCode, error) {
	var codes []model.RedeemCode
	if limit <= 0 {
		limit = 10
	}

	err := r.db.WithContext(ctx).
		Preload("Group").
		Where("used_by = ?", userID).
		Order("used_at DESC").
		Limit(limit).
		Find(&codes).Error

	if err != nil {
		return nil, err
	}
	return codes, nil
}
