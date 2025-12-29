package repository

import (
	"context"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"

	"github.com/Wei-Shaw/sub2api/internal/pkg/pagination"

	"gorm.io/gorm"
)

type proxyRepository struct {
	db *gorm.DB
}

func NewProxyRepository(db *gorm.DB) service.ProxyRepository {
	return &proxyRepository{db: db}
}

func (r *proxyRepository) Create(ctx context.Context, proxy *service.Proxy) error {
	m := proxyModelFromService(proxy)
	err := r.db.WithContext(ctx).Create(m).Error
	if err == nil {
		applyProxyModelToService(proxy, m)
	}
	return err
}

func (r *proxyRepository) GetByID(ctx context.Context, id int64) (*service.Proxy, error) {
	var m proxyModel
	err := r.db.WithContext(ctx).First(&m, id).Error
	if err != nil {
		return nil, translatePersistenceError(err, service.ErrProxyNotFound, nil)
	}
	return proxyModelToService(&m), nil
}

func (r *proxyRepository) Update(ctx context.Context, proxy *service.Proxy) error {
	m := proxyModelFromService(proxy)
	err := r.db.WithContext(ctx).Save(m).Error
	if err == nil {
		applyProxyModelToService(proxy, m)
	}
	return err
}

func (r *proxyRepository) Delete(ctx context.Context, id int64) error {
	return r.db.WithContext(ctx).Delete(&proxyModel{}, id).Error
}

func (r *proxyRepository) List(ctx context.Context, params pagination.PaginationParams) ([]service.Proxy, *pagination.PaginationResult, error) {
	return r.ListWithFilters(ctx, params, "", "", "")
}

// ListWithFilters lists proxies with optional filtering by protocol, status, and search query
func (r *proxyRepository) ListWithFilters(ctx context.Context, params pagination.PaginationParams, protocol, status, search string) ([]service.Proxy, *pagination.PaginationResult, error) {
	var proxies []proxyModel
	var total int64

	db := r.db.WithContext(ctx).Model(&proxyModel{})

	// Apply filters
	if protocol != "" {
		db = db.Where("protocol = ?", protocol)
	}
	if status != "" {
		db = db.Where("status = ?", status)
	}
	if search != "" {
		searchPattern := "%" + search + "%"
		db = db.Where("name ILIKE ?", searchPattern)
	}

	if err := db.Count(&total).Error; err != nil {
		return nil, nil, err
	}

	if err := db.Offset(params.Offset()).Limit(params.Limit()).Order("id DESC").Find(&proxies).Error; err != nil {
		return nil, nil, err
	}

	outProxies := make([]service.Proxy, 0, len(proxies))
	for i := range proxies {
		outProxies = append(outProxies, *proxyModelToService(&proxies[i]))
	}

	return outProxies, paginationResultFromTotal(total, params), nil
}

func (r *proxyRepository) ListActive(ctx context.Context) ([]service.Proxy, error) {
	var proxies []proxyModel
	err := r.db.WithContext(ctx).Where("status = ?", service.StatusActive).Find(&proxies).Error
	if err != nil {
		return nil, err
	}
	outProxies := make([]service.Proxy, 0, len(proxies))
	for i := range proxies {
		outProxies = append(outProxies, *proxyModelToService(&proxies[i]))
	}
	return outProxies, nil
}

// ExistsByHostPortAuth checks if a proxy with the same host, port, username, and password exists
func (r *proxyRepository) ExistsByHostPortAuth(ctx context.Context, host string, port int, username, password string) (bool, error) {
	var count int64
	err := r.db.WithContext(ctx).Model(&proxyModel{}).
		Where("host = ? AND port = ? AND username = ? AND password = ?", host, port, username, password).
		Count(&count).Error
	if err != nil {
		return false, err
	}
	return count > 0, nil
}

// CountAccountsByProxyID returns the number of accounts using a specific proxy
func (r *proxyRepository) CountAccountsByProxyID(ctx context.Context, proxyID int64) (int64, error) {
	var count int64
	err := r.db.WithContext(ctx).Table("accounts").
		Where("proxy_id = ?", proxyID).
		Where("deleted_at IS NULL").
		Count(&count).Error
	return count, err
}

// GetAccountCountsForProxies returns a map of proxy ID to account count for all proxies
func (r *proxyRepository) GetAccountCountsForProxies(ctx context.Context) (map[int64]int64, error) {
	type result struct {
		ProxyID int64 `gorm:"column:proxy_id"`
		Count   int64 `gorm:"column:count"`
	}
	var results []result
	err := r.db.WithContext(ctx).
		Table("accounts").
		Select("proxy_id, COUNT(*) as count").
		Where("proxy_id IS NOT NULL").
		Where("deleted_at IS NULL").
		Group("proxy_id").
		Scan(&results).Error
	if err != nil {
		return nil, err
	}

	counts := make(map[int64]int64)
	for _, r := range results {
		counts[r.ProxyID] = r.Count
	}
	return counts, nil
}

// ListActiveWithAccountCount returns all active proxies with account count, sorted by creation time descending
func (r *proxyRepository) ListActiveWithAccountCount(ctx context.Context) ([]service.ProxyWithAccountCount, error) {
	var proxies []proxyModel
	err := r.db.WithContext(ctx).
		Where("status = ?", service.StatusActive).
		Order("created_at DESC").
		Find(&proxies).Error
	if err != nil {
		return nil, err
	}

	// Get account counts
	counts, err := r.GetAccountCountsForProxies(ctx)
	if err != nil {
		return nil, err
	}

	// Build result with account counts
	result := make([]service.ProxyWithAccountCount, 0, len(proxies))
	for i := range proxies {
		proxy := proxyModelToService(&proxies[i])
		if proxy == nil {
			continue
		}
		result = append(result, service.ProxyWithAccountCount{
			Proxy:        *proxy,
			AccountCount: counts[proxy.ID],
		})
	}

	return result, nil
}

type proxyModel struct {
	ID        int64          `gorm:"primaryKey"`
	Name      string         `gorm:"size:100;not null"`
	Protocol  string         `gorm:"size:20;not null"`
	Host      string         `gorm:"size:255;not null"`
	Port      int            `gorm:"not null"`
	Username  string         `gorm:"size:100"`
	Password  string         `gorm:"size:100"`
	Status    string         `gorm:"size:20;default:active;not null"`
	CreatedAt time.Time      `gorm:"not null"`
	UpdatedAt time.Time      `gorm:"not null"`
	DeletedAt gorm.DeletedAt `gorm:"index"`
}

func (proxyModel) TableName() string { return "proxies" }

func proxyModelToService(m *proxyModel) *service.Proxy {
	if m == nil {
		return nil
	}
	return &service.Proxy{
		ID:        m.ID,
		Name:      m.Name,
		Protocol:  m.Protocol,
		Host:      m.Host,
		Port:      m.Port,
		Username:  m.Username,
		Password:  m.Password,
		Status:    m.Status,
		CreatedAt: m.CreatedAt,
		UpdatedAt: m.UpdatedAt,
	}
}

func proxyModelFromService(p *service.Proxy) *proxyModel {
	if p == nil {
		return nil
	}
	return &proxyModel{
		ID:        p.ID,
		Name:      p.Name,
		Protocol:  p.Protocol,
		Host:      p.Host,
		Port:      p.Port,
		Username:  p.Username,
		Password:  p.Password,
		Status:    p.Status,
		CreatedAt: p.CreatedAt,
		UpdatedAt: p.UpdatedAt,
	}
}

func applyProxyModelToService(proxy *service.Proxy, m *proxyModel) {
	if proxy == nil || m == nil {
		return
	}
	proxy.ID = m.ID
	proxy.CreatedAt = m.CreatedAt
	proxy.UpdatedAt = m.UpdatedAt
}
