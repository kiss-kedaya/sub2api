package service

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
)

// 密钥轮换门禁的「风险确认跳过」记录与校验。
//
// 用户在充值 / 兑换拦截页点「我已知晓风险，继续充值」并二次确认后，写入一条
// 带有效期的 confirm（key_rotation_risk_acks）。门禁评估时，只要这条确认
// 未过期、且 cutoff_snapshot 等于当前 gate cutoff，就把用户视为合规放行
// 充值 / 兑换（模型调用本来就不受门禁影响）。
//
// 设计约束：
//   - 显式：必须 acknowledge_risk=true，不接受默认值；
//   - 有期限：expires_at = now + security.key_rotation_risk_ack_hours（默认 24h）；
//   - 可审计：写入记录 + 审计日志 KEY_ROTATION_RISK_ACK（含 expires_at/cutoff/IP/UA）；
//   - 会失效：过期、cutoff 被调新（新一轮事件）都要求重新确认。
const (
	// KeyRotationRiskAckDisabledCode 是功能关闭时返回的 403 错误码。
	KeyRotationRiskAckDisabledCode = "KEY_ROTATION_RISK_ACK_DISABLED"
	// KeyRotationRiskAckDisabledMessage 是功能关闭时的用户提示。
	KeyRotationRiskAckDisabledMessage = "风险确认跳过当前不可用，请更换 API Key 或设置 IP 白名单后再充值/兑换"
	// KeyRotationReasonRiskAcknowledged 是存在有效风险确认时门禁的 reason。
	KeyRotationReasonRiskAcknowledged = "risk_acknowledged"
)

// ErrKeyRotationRiskAckDisabled 是功能关闭时返回的 403。
var ErrKeyRotationRiskAckDisabled = infraerrors.Forbidden(
	KeyRotationRiskAckDisabledCode,
	KeyRotationRiskAckDisabledMessage,
)

// KeyRotationRiskAck 一条风险确认记录。append-only，不修改历史行。
type KeyRotationRiskAck struct {
	ID             int64     `json:"id"`
	UserID         int64     `json:"user_id"`
	AcknowledgedAt time.Time `json:"acknowledged_at"`
	ExpiresAt      time.Time `json:"expires_at"`
	ClientIP       string    `json:"client_ip"`
	UserAgent      string    `json:"user_agent"`
	// CutoffSnapshot 是确认当时的 gate cutoff；cutoff 变化后该确认自动失效。
	CutoffSnapshot time.Time `json:"cutoff_snapshot"`
	CreatedAt      time.Time `json:"created_at"`
}

// KeyRotationRiskAckRepository 风险确认仓储（raw SQL）。
type KeyRotationRiskAckRepository interface {
	// Create 追加一条确认并回填数据库默认值（id/acknowledged_at/created_at）。
	Create(ctx context.Context, ack *KeyRotationRiskAck) (*KeyRotationRiskAck, error)
	// FindLatestValid 返回 user_id 下「未过期 且 cutoff_snapshot 等于 cutoff」的最新一条
	// 确认；不存在时返回 (nil, nil)。查询走 (user_id, expires_at DESC) 索引。
	FindLatestValid(ctx context.Context, userID int64, cutoff time.Time, now time.Time) (*KeyRotationRiskAck, error)
}

// KeyRotationRiskAckService 负责写入与校验风险确认。
type KeyRotationRiskAckService struct {
	repo     KeyRotationRiskAckRepository
	settings KeyRotationRiskAckSettingsProvider
	now      func() time.Time
}

// NewKeyRotationRiskAckService 构造风险确认服务。*SettingService 绑定到
// KeyRotationRiskAckSettingsProvider；测试可注入固定设置的 stub。
func NewKeyRotationRiskAckService(
	repo KeyRotationRiskAckRepository,
	settings KeyRotationRiskAckSettingsProvider,
) *KeyRotationRiskAckService {
	return &KeyRotationRiskAckService{repo: repo, settings: settings, now: time.Now}
}

// Settings 读取当前风险确认设置；读取失败时回退默认值（开启/24h），
// 让门禁行为保持确定，同时记录排障日志。
func (s *KeyRotationRiskAckService) Settings(ctx context.Context) KeyRotationRiskAckSettings {
	if s == nil || s.settings == nil {
		return DefaultKeyRotationRiskAckSettings()
	}
	settings, err := s.settings.GetKeyRotationRiskAckSettings(ctx)
	if err != nil {
		slog.Warn("read key rotation risk ack settings failed; falling back to defaults", "error", err)
		return DefaultKeyRotationRiskAckSettings()
	}
	return settings
}

// HasValidRiskAck 返回用户当前有效的风险确认（未过期 + cutoff 匹配）。
// 没有时返回 (nil, nil)。这是门禁评估与状态接口共用的唯一校验入口，避免重复查询。
func (s *KeyRotationRiskAckService) HasValidRiskAck(ctx context.Context, userID int64, cutoff time.Time) (*KeyRotationRiskAck, error) {
	if s == nil || s.repo == nil || userID <= 0 {
		return nil, nil
	}
	now := s.now().UTC()
	ack, err := s.repo.FindLatestValid(ctx, userID, cutoff.UTC(), now)
	if err != nil {
		return nil, fmt.Errorf("key rotation risk ack: find valid ack for user %d: %w", userID, err)
	}
	if ack == nil {
		return nil, nil
	}
	if !ack.ExpiresAt.After(now) {
		return nil, nil
	}
	if !ack.CutoffSnapshot.Equal(cutoff.UTC()) {
		return nil, nil
	}
	return ack, nil
}

// Acknowledge 写入一条风险确认。功能关闭时返回 ErrKeyRotationRiskAckDisabled（403）。
func (s *KeyRotationRiskAckService) Acknowledge(
	ctx context.Context,
	userID int64,
	cutoff time.Time,
	clientIP string,
	userAgent string,
) (*KeyRotationRiskAck, error) {
	if s == nil || s.repo == nil {
		return nil, fmt.Errorf("key rotation risk ack service unavailable")
	}
	if userID <= 0 {
		return nil, infraerrors.Unauthorized("UNAUTHORIZED", "User not authenticated")
	}
	settings := s.Settings(ctx)
	if !settings.Enabled {
		return nil, ErrKeyRotationRiskAckDisabled
	}
	now := s.now().UTC()
	ack := &KeyRotationRiskAck{
		UserID:         userID,
		AcknowledgedAt: now,
		ExpiresAt:      now.Add(time.Duration(settings.Hours) * time.Hour),
		ClientIP:       truncateRiskAckField(clientIP, 64),
		UserAgent:      truncateRiskAckField(userAgent, 512),
		CutoffSnapshot: cutoff.UTC(),
		CreatedAt:      now,
	}
	created, err := s.repo.Create(ctx, ack)
	if err != nil {
		return nil, fmt.Errorf("key rotation risk ack: create for user %d: %w", userID, err)
	}
	return created, nil
}

// truncateRiskAckField 限制 IP / UA 的入库长度，避免恶意超长头撑爆行宽。
func truncateRiskAckField(value string, limit int) string {
	if len(value) <= limit {
		return value
	}
	return value[:limit]
}
