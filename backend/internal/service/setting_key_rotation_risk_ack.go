package service

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strconv"
	"strings"
)

// 密钥轮换门禁的「风险确认跳过」（risk acknowledgement）设置。
//
// 密钥泄漏事件后，部分用户既不愿意轮换 API Key，也不愿意设置 IP 白名单。
// 管理员可以开启一个可审计的风险确认通道：用户显式确认「已知晓 key 泄漏后
// 他人可直接盗刷余额、风险自负」后，在有限时间内继续充值 / 兑换。
//
// 两项设置都放在 settings 表（走 SettingService 读写），后台可配：
//   - security.key_rotation_risk_ack_enabled —— 是否允许风险确认跳过，默认 true；
//   - security.key_rotation_risk_ack_hours   —— 一次确认的有效小时数，默认 24，范围 1..720。
//
// 关闭后：已写入的确认立即不再参与门禁评估（视为要求整改），用户也不能再写新确认。
const (
	SettingKeyKeyRotationRiskAckEnabled = "security.key_rotation_risk_ack_enabled"
	SettingKeyKeyRotationRiskAckHours   = "security.key_rotation_risk_ack_hours"
)

const (
	// DefaultKeyRotationRiskAckEnabled 默认开启：老板要求「让他们可以继续充值」。
	DefaultKeyRotationRiskAckEnabled = true
	// DefaultKeyRotationRiskAckHours 默认 24 小时有效。
	DefaultKeyRotationRiskAckHours = 24
	// MinKeyRotationRiskAckHours / MaxKeyRotationRiskAckHours 限定有效期范围。
	MinKeyRotationRiskAckHours = 1
	MaxKeyRotationRiskAckHours = 720
)

// KeyRotationRiskAckSettings 是对外（用户状态接口 / 后台设置接口）暴露的有效配置。
type KeyRotationRiskAckSettings struct {
	Enabled bool `json:"enabled"`
	Hours   int  `json:"hours"`
}

// KeyRotationRiskAckSettingsProvider 是门禁与确认服务读取设置的窄接口；
// *SettingService 实现它，测试可以注入固定值。
type KeyRotationRiskAckSettingsProvider interface {
	GetKeyRotationRiskAckSettings(ctx context.Context) (KeyRotationRiskAckSettings, error)
}

// DefaultKeyRotationRiskAckSettings 返回默认值（开启、24 小时）。
func DefaultKeyRotationRiskAckSettings() KeyRotationRiskAckSettings {
	return KeyRotationRiskAckSettings{Enabled: DefaultKeyRotationRiskAckEnabled, Hours: DefaultKeyRotationRiskAckHours}
}

// NormalizeKeyRotationRiskAckSettings 校验并规范化待写入的设置。
// enabled 缺省为 false 时仍按调用方 JSON 原样保留；hours 缺省 (0) 归一到默认值，
// 越界直接拒绝，避免管理员把有效期写成负数或几年。
func NormalizeKeyRotationRiskAckSettings(in KeyRotationRiskAckSettings) (KeyRotationRiskAckSettings, error) {
	out := in
	if out.Hours == 0 {
		out.Hours = DefaultKeyRotationRiskAckHours
	}
	if out.Hours < MinKeyRotationRiskAckHours || out.Hours > MaxKeyRotationRiskAckHours {
		return KeyRotationRiskAckSettings{}, fmt.Errorf(
			"key rotation risk ack hours must be between %d and %d",
			MinKeyRotationRiskAckHours, MaxKeyRotationRiskAckHours,
		)
	}
	return out, nil
}

// GetKeyRotationRiskAckSettings 读取风险确认设置。
// 未配置时返回默认值；库里是脏数据时记日志并回退默认值，绝不让一条坏设置
// 把门禁行为变成不可预期（尤其是把开关误判成关闭/开启）。
func (s *SettingService) GetKeyRotationRiskAckSettings(ctx context.Context) (KeyRotationRiskAckSettings, error) {
	settings := DefaultKeyRotationRiskAckSettings()
	if s == nil || s.settingRepo == nil {
		return settings, nil
	}

	rawEnabled, err := s.settingRepo.GetValue(ctx, SettingKeyKeyRotationRiskAckEnabled)
	if err != nil && !errors.Is(err, ErrSettingNotFound) {
		return settings, fmt.Errorf("read %s: %w", SettingKeyKeyRotationRiskAckEnabled, err)
	}
	if err == nil {
		value := strings.TrimSpace(rawEnabled)
		if value != "" {
			parsed, parseErr := strconv.ParseBool(value)
			if parseErr != nil {
				slog.Warn("invalid key rotation risk ack enabled setting; falling back to default",
					"key", SettingKeyKeyRotationRiskAckEnabled, "value", value)
			} else {
				settings.Enabled = parsed
			}
		}
	}

	rawHours, err := s.settingRepo.GetValue(ctx, SettingKeyKeyRotationRiskAckHours)
	if err != nil && !errors.Is(err, ErrSettingNotFound) {
		return settings, fmt.Errorf("read %s: %w", SettingKeyKeyRotationRiskAckHours, err)
	}
	if err == nil {
		value := strings.TrimSpace(rawHours)
		if value != "" {
			parsed, parseErr := strconv.Atoi(value)
			if parseErr != nil || parsed < MinKeyRotationRiskAckHours || parsed > MaxKeyRotationRiskAckHours {
				slog.Warn("invalid key rotation risk ack hours setting; falling back to default",
					"key", SettingKeyKeyRotationRiskAckHours, "value", value)
			} else {
				settings.Hours = parsed
			}
		}
	}
	return settings, nil
}

// SetKeyRotationRiskAckSettings 写入风险确认设置（后台接口调用）。
func (s *SettingService) SetKeyRotationRiskAckSettings(ctx context.Context, settings KeyRotationRiskAckSettings) (KeyRotationRiskAckSettings, error) {
	normalized, err := NormalizeKeyRotationRiskAckSettings(settings)
	if err != nil {
		return KeyRotationRiskAckSettings{}, err
	}
	if s == nil || s.settingRepo == nil {
		return KeyRotationRiskAckSettings{}, fmt.Errorf("setting service unavailable")
	}
	if err := s.SetRawSetting(ctx, SettingKeyKeyRotationRiskAckEnabled, strconv.FormatBool(normalized.Enabled)); err != nil {
		return KeyRotationRiskAckSettings{}, fmt.Errorf("write %s: %w", SettingKeyKeyRotationRiskAckEnabled, err)
	}
	if err := s.SetRawSetting(ctx, SettingKeyKeyRotationRiskAckHours, strconv.Itoa(normalized.Hours)); err != nil {
		return KeyRotationRiskAckSettings{}, fmt.Errorf("write %s: %w", SettingKeyKeyRotationRiskAckHours, err)
	}
	return normalized, nil
}
