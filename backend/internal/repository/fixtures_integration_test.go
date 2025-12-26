//go:build integration

package repository

import (
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
	"gorm.io/datatypes"
	"gorm.io/gorm"
)

func mustCreateUser(t *testing.T, db *gorm.DB, u *userModel) *userModel {
	t.Helper()
	if u.PasswordHash == "" {
		u.PasswordHash = "test-password-hash"
	}
	if u.Role == "" {
		u.Role = service.RoleUser
	}
	if u.Status == "" {
		u.Status = service.StatusActive
	}
	if u.Concurrency == 0 {
		u.Concurrency = 5
	}
	if u.CreatedAt.IsZero() {
		u.CreatedAt = time.Now()
	}
	if u.UpdatedAt.IsZero() {
		u.UpdatedAt = u.CreatedAt
	}
	require.NoError(t, db.Create(u).Error, "create user")
	return u
}

func mustCreateGroup(t *testing.T, db *gorm.DB, g *groupModel) *groupModel {
	t.Helper()
	if g.Platform == "" {
		g.Platform = service.PlatformAnthropic
	}
	if g.Status == "" {
		g.Status = service.StatusActive
	}
	if g.SubscriptionType == "" {
		g.SubscriptionType = service.SubscriptionTypeStandard
	}
	if g.CreatedAt.IsZero() {
		g.CreatedAt = time.Now()
	}
	if g.UpdatedAt.IsZero() {
		g.UpdatedAt = g.CreatedAt
	}
	require.NoError(t, db.Create(g).Error, "create group")
	return g
}

func mustCreateProxy(t *testing.T, db *gorm.DB, p *proxyModel) *proxyModel {
	t.Helper()
	if p.Protocol == "" {
		p.Protocol = "http"
	}
	if p.Host == "" {
		p.Host = "127.0.0.1"
	}
	if p.Port == 0 {
		p.Port = 8080
	}
	if p.Status == "" {
		p.Status = service.StatusActive
	}
	if p.CreatedAt.IsZero() {
		p.CreatedAt = time.Now()
	}
	if p.UpdatedAt.IsZero() {
		p.UpdatedAt = p.CreatedAt
	}
	require.NoError(t, db.Create(p).Error, "create proxy")
	return p
}

func mustCreateAccount(t *testing.T, db *gorm.DB, a *accountModel) *accountModel {
	t.Helper()
	if a.Platform == "" {
		a.Platform = service.PlatformAnthropic
	}
	if a.Type == "" {
		a.Type = service.AccountTypeOAuth
	}
	if a.Status == "" {
		a.Status = service.StatusActive
	}
	if !a.Schedulable {
		a.Schedulable = true
	}
	if a.Credentials == nil {
		a.Credentials = datatypes.JSONMap{}
	}
	if a.Extra == nil {
		a.Extra = datatypes.JSONMap{}
	}
	if a.CreatedAt.IsZero() {
		a.CreatedAt = time.Now()
	}
	if a.UpdatedAt.IsZero() {
		a.UpdatedAt = a.CreatedAt
	}
	require.NoError(t, db.Create(a).Error, "create account")
	return a
}

func mustCreateApiKey(t *testing.T, db *gorm.DB, k *apiKeyModel) *apiKeyModel {
	t.Helper()
	if k.Status == "" {
		k.Status = service.StatusActive
	}
	if k.CreatedAt.IsZero() {
		k.CreatedAt = time.Now()
	}
	if k.UpdatedAt.IsZero() {
		k.UpdatedAt = k.CreatedAt
	}
	require.NoError(t, db.Create(k).Error, "create api key")
	return k
}

func mustCreateRedeemCode(t *testing.T, db *gorm.DB, c *redeemCodeModel) *redeemCodeModel {
	t.Helper()
	if c.Status == "" {
		c.Status = service.StatusUnused
	}
	if c.Type == "" {
		c.Type = service.RedeemTypeBalance
	}
	if c.CreatedAt.IsZero() {
		c.CreatedAt = time.Now()
	}
	require.NoError(t, db.Create(c).Error, "create redeem code")
	return c
}

func mustCreateSubscription(t *testing.T, db *gorm.DB, s *userSubscriptionModel) *userSubscriptionModel {
	t.Helper()
	if s.Status == "" {
		s.Status = service.SubscriptionStatusActive
	}
	now := time.Now()
	if s.StartsAt.IsZero() {
		s.StartsAt = now.Add(-1 * time.Hour)
	}
	if s.ExpiresAt.IsZero() {
		s.ExpiresAt = now.Add(24 * time.Hour)
	}
	if s.AssignedAt.IsZero() {
		s.AssignedAt = now
	}
	if s.CreatedAt.IsZero() {
		s.CreatedAt = now
	}
	if s.UpdatedAt.IsZero() {
		s.UpdatedAt = now
	}
	require.NoError(t, db.Create(s).Error, "create user subscription")
	return s
}

func mustBindAccountToGroup(t *testing.T, db *gorm.DB, accountID, groupID int64, priority int) {
	t.Helper()
	require.NoError(t, db.Create(&accountGroupModel{
		AccountID: accountID,
		GroupID:   groupID,
		Priority:  priority,
		CreatedAt: time.Now(),
	}).Error, "create account_group")
}
