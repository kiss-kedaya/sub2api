package service

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// 2026-11-24 事故回归锁：
// content_moderation_config 里 enabled=false（主开关关）、auto_ban_enabled=true、
// ban_threshold=10、violation_window_hours=720、cyber_policy_exclude_from_ban_count=false，
// 结果 13:24-14:00 半小时内自动封禁 22 个付费用户，命中分类全是 cyber_policy。
//
// 本文件锁定三件事：
//  1. 主开关 enabled=false 时自动封禁强制失效（P0）；
//  2. enabled=true 且 count>=threshold 时仍然会封（行为不能被修丢）；
//  3. 自动封禁发生时必须通知管理员。

// autobanTestUserRepo 是可在测试里控制 GetFirstAdmin 的最小 UserRepository。
type autobanTestUserRepo struct {
	contentModerationTestUserRepo
	firstAdmin *User
}

func (r *autobanTestUserRepo) GetFirstAdmin(ctx context.Context) (*User, error) {
	if r.firstAdmin == nil {
		return nil, nil
	}
	clone := *r.firstAdmin
	return &clone, nil
}

// autobanNotifyRecorder 记录管理员通知调用（按需成为 mock）。
type autobanNotifyRecorder struct {
	mu            sync.Mutex
	calls         []adminBanNotification
	returnedCalls chan adminBanNotification
	err           error
	// configured 表示测试显式设置了 notifier；用于断言「默认路径也被走到」。
	used bool
}

func newAutobanNotifyRecorder() *autobanNotifyRecorder {
	// 缓冲足够大，避免异步通知 goroutine 阻塞。
	return &autobanNotifyRecorder{returnedCalls: make(chan adminBanNotification, 8)}
}

func (r *autobanNotifyRecorder) notify(ctx context.Context, cfg *ContentModerationConfig, n adminBanNotification) error {
	r.mu.Lock()
	r.calls = append(r.calls, n)
	r.used = true
	r.mu.Unlock()
	select {
	case r.returnedCalls <- n:
	default:
	}
	return r.err
}

func (r *autobanNotifyRecorder) snapshot() []adminBanNotification {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]adminBanNotification, len(r.calls))
	copy(out, r.calls)
	return out
}

// waitForCall 等异步管理员通知落地（通知走独立 goroutine，不能同步断言）。
func (r *autobanNotifyRecorder) waitForCall(t *testing.T) adminBanNotification {
	t.Helper()
	select {
	case n := <-r.returnedCalls:
		return n
	case <-time.After(3 * time.Second):
		require.FailNow(t, "自动封禁后必须通知管理员，但 3 秒内没有收到通知")
		return adminBanNotification{}
	}
}

// seedFlaggedLogs 在测试 repo 里塞 count 条窗口内的已标记日志（排除 hash_block）。
func seedFlaggedLogs(repo *contentModerationTestRepo, userID int64, count int) {
	for i := 0; i < count; i++ {
		id := userID
		repo.logs = append(repo.logs, ContentModerationLog{
			UserID:    &id,
			Flagged:   true,
			Action:    "block",
			CreatedAt: time.Now().Add(-time.Hour),
		})
	}
}

// incidentConfig 复刻事故当时的线上配置。
func incidentConfig() *ContentModerationConfig {
	cfg := defaultContentModerationConfig()
	cfg.Enabled = false
	cfg.AutoBanEnabled = true
	cfg.BanThreshold = 10
	cfg.ViolationWindowHours = 720
	cfg.CyberPolicyExcludeFromBanCount = false
	return cfg
}

// cyberIncidentLog 复刻事故里的命中形态：cyber_policy、score=1.0、无关键词。
func cyberIncidentLog(userID int64) *ContentModerationLog {
	id := userID
	return &ContentModerationLog{
		Flagged:         true,
		UserID:          &id,
		UserEmail:       "security-researcher@example.com",
		Action:          ContentModerationActionCyberPolicy,
		HighestCategory: "cyber_policy",
		HighestScore:    1.0,
		Model:           "gpt-5",
		Endpoint:        "/v1/responses",
		GroupName:       "vip",
		CreatedAt:       time.Now(),
	}
}

// ---------- 1. 主开关联动 ----------

func TestApplyFlaggedAccountSideEffects_MasterSwitchOffNeverBans(t *testing.T) {
	userID := int64(7001)
	repo := &contentModerationTestRepo{}
	// 已有 20 条违规，远超阈值 10。
	seedFlaggedLogs(repo, userID, 20)
	userRepo := &autobanTestUserRepo{contentModerationTestUserRepo: contentModerationTestUserRepo{
		user: &User{ID: userID, Email: "security-researcher@example.com", Status: StatusActive},
	}}
	invalidator := &contentModerationTestAuthCacheInvalidator{}
	notifier := newAutobanNotifyRecorder()
	svc := NewContentModerationService(nil, repo, nil, nil, userRepo, nil, invalidator, nil)
	svc.SetAdminBanNotifier(notifier.notify)

	cfg := incidentConfig() // enabled=false + auto_ban_enabled=true
	require.False(t, cfg.Enabled)

	banned := svc.applyFlaggedAccountSideEffects(context.Background(), cfg, cyberIncidentLog(userID))

	require.False(t, banned, "内容审核主开关关闭时不得自动封禁")
	require.Empty(t, userRepo.updated, "enabled=false 时禁止写用户状态")
	require.Empty(t, invalidator.userIDs, "enabled=false 时不应失效鉴权缓存")
	require.Empty(t, notifier.snapshot(), "没有封禁就不该发管理员封禁告警")
}

func TestApplyFlaggedAccountSideEffects_MasterSwitchOffSkipsViolationCounting(t *testing.T) {
	userID := int64(7002)
	repo := &banCountArgsTestRepo{}
	svc := NewContentModerationService(nil, repo, nil, nil, nil, nil, nil, nil)

	cfg := incidentConfig()
	log := cyberIncidentLog(userID)
	svc.applyFlaggedAccountSideEffects(context.Background(), cfg, log)

	require.Empty(t, repo.snapshotCountCalls(), "主开关关闭时不应查询违规计数")
	require.Zero(t, log.ViolationCount)
}

func TestParseContentModerationConfig_DisabledForcesAutoBanOff(t *testing.T) {
	// 老配置：完整 JSON 里 enabled=false 但 auto_ban_enabled=true。
	raw := `{"enabled":false,"auto_ban_enabled":true,"ban_threshold":10,"violation_window_hours":720}`

	cfg, err := parseContentModerationConfig(raw)
	require.NoError(t, err)
	require.False(t, cfg.Enabled)
	require.False(t, cfg.AutoBanEnabled, "enabled=false 必须在配置解析阶段把 auto_ban_enabled 归一化为 false")
}

func TestParseContentModerationConfig_EnabledKeepsAutoBanOn(t *testing.T) {
	raw := `{"enabled":true,"auto_ban_enabled":true,"ban_threshold":10}`

	cfg, err := parseContentModerationConfig(raw)
	require.NoError(t, err)
	require.True(t, cfg.Enabled)
	require.True(t, cfg.AutoBanEnabled, "主开关打开时 auto_ban_enabled 必须保持原值")
}

func TestUpdateConfig_DisabledForcesAutoBanOffOnSave(t *testing.T) {
	settingRepo := &contentModerationTestSettingRepo{values: map[string]string{}}
	svc := NewContentModerationService(settingRepo, nil, nil, nil, nil, nil, nil, nil)

	enabled, disabled := true, false
	_, err := svc.UpdateConfig(context.Background(), UpdateContentModerationConfigInput{Enabled: &enabled, AutoBanEnabled: &enabled})
	require.NoError(t, err)

	// 关掉主开关，但管理端这次请求仍然提交 auto_ban_enabled=true。
	view, err := svc.UpdateConfig(context.Background(), UpdateContentModerationConfigInput{Enabled: &disabled})
	require.NoError(t, err)
	require.False(t, view.Enabled)
	require.False(t, view.AutoBanEnabled, "保存配置时 enabled=false 必须把 auto_ban_enabled 归一化为 false")

	var saved ContentModerationConfig
	require.NoError(t, json.Unmarshal([]byte(settingRepo.values[SettingKeyContentModerationConfig]), &saved))
	require.False(t, saved.AutoBanEnabled, "落盘配置里也不能留下 enabled=false + auto_ban_enabled=true 的组合")
}

// ---------- 2. 回归锁：该封的还得封 ----------

func TestApplyFlaggedAccountSideEffects_EnabledAndOverThresholdBans(t *testing.T) {
	userID := int64(7003)
	repo := &contentModerationTestRepo{}
	// 已有 10 条违规 + 本次 1 条 = 11 >= 阈值 10。
	seedFlaggedLogs(repo, userID, 10)
	userRepo := &autobanTestUserRepo{contentModerationTestUserRepo: contentModerationTestUserRepo{
		user: &User{ID: userID, Email: "abuser@example.com", Status: StatusActive, Role: RoleUser},
	}}
	invalidator := &contentModerationTestAuthCacheInvalidator{}
	notifier := newAutobanNotifyRecorder()
	svc := NewContentModerationService(nil, repo, nil, nil, userRepo, nil, invalidator, nil)
	svc.SetAdminBanNotifier(notifier.notify)

	cfg := defaultContentModerationConfig()
	cfg.Enabled = true
	cfg.AutoBanEnabled = true
	cfg.BanThreshold = 10
	cfg.ViolationWindowHours = 720

	log := &ContentModerationLog{
		Flagged:         true,
		UserID:          &userID,
		UserEmail:       "abuser@example.com",
		Action:          "block",
		HighestCategory: "sexual",
		HighestScore:    0.95,
		Model:           "gpt-5",
		Endpoint:        "/v1/chat/completions",
	}

	banned := svc.applyFlaggedAccountSideEffects(context.Background(), cfg, log)

	require.True(t, banned, "enabled=true + auto_ban_enabled=true + count>=threshold 必须封禁（回归锁）")
	require.True(t, log.AutoBanned)
	require.Equal(t, 11, log.ViolationCount)
	require.Len(t, userRepo.updated, 1)
	require.Equal(t, StatusDisabled, userRepo.updated[0].Status)
	require.Equal(t, []int64{userID}, invalidator.userIDs)
}

func TestApplyFlaggedAccountSideEffects_BelowThresholdDoesNotBan(t *testing.T) {
	userID := int64(7004)
	repo := &contentModerationTestRepo{}
	seedFlaggedLogs(repo, userID, 3)
	userRepo := &autobanTestUserRepo{contentModerationTestUserRepo: contentModerationTestUserRepo{
		user: &User{ID: userID, Email: "u@example.com", Status: StatusActive, Role: RoleUser},
	}}
	notifier := newAutobanNotifyRecorder()
	svc := NewContentModerationService(nil, repo, nil, nil, userRepo, nil, nil, nil)
	svc.SetAdminBanNotifier(notifier.notify)

	cfg := defaultContentModerationConfig()
	cfg.Enabled = true
	cfg.AutoBanEnabled = true
	cfg.BanThreshold = 10

	banned := svc.applyFlaggedAccountSideEffects(context.Background(), cfg, &ContentModerationLog{
		Flagged: true, UserID: &userID, Action: "block", HighestCategory: "sexual",
	})

	require.False(t, banned)
	require.Empty(t, userRepo.updated)
	require.Empty(t, notifier.snapshot(), "没封人就不该发管理员封禁告警")
}

// ---------- 3. cyber_policy 默认不计入封禁 ----------

func TestParseContentModerationConfig_CyberPolicyExcludeDefaultsTrue(t *testing.T) {
	// 旧配置 JSON 完全没有这个字段：必须落到 true（新缺省）。
	raw := `{"enabled":true,"auto_ban_enabled":true,"ban_threshold":10}`
	cfg, err := parseContentModerationConfig(raw)
	require.NoError(t, err)
	require.True(t, cfg.CyberPolicyExcludeFromBanCount,
		"cyber_policy_exclude_from_ban_count 缺省必须是 true（安全研究流量被分类器系统性误判）")

	// 空配置也必须是 true。
	cfg, err = parseContentModerationConfig("")
	require.NoError(t, err)
	require.True(t, cfg.CyberPolicyExcludeFromBanCount)
}

func TestParseContentModerationConfig_CyberPolicyExcludeExplicitFalseHonoured(t *testing.T) {
	raw := `{"enabled":true,"auto_ban_enabled":true,"cyber_policy_exclude_from_ban_count":false}`
	cfg, err := parseContentModerationConfig(raw)
	require.NoError(t, err)
	require.False(t, cfg.CyberPolicyExcludeFromBanCount,
		"配置里显式写 false 时必须尊重运营方的显式选择")
}

func TestApplyFlaggedAccountSideEffects_CyberPolicyExcludedFromBanCount(t *testing.T) {
	userID := int64(7005)
	repo := &contentModerationTestRepo{}
	// 已有 20 条 cyber_policy 命中，说明用户被分类器反复误判。
	for i := 0; i < 20; i++ {
		id := userID
		repo.logs = append(repo.logs, ContentModerationLog{
			UserID:    &id,
			Flagged:   true,
			Action:    ContentModerationActionCyberPolicy,
			CreatedAt: time.Now().Add(-time.Hour),
		})
	}
	userRepo := &autobanTestUserRepo{contentModerationTestUserRepo: contentModerationTestUserRepo{
		user: &User{ID: userID, Email: "security-researcher@example.com", Status: StatusActive, Role: RoleUser},
	}}
	notifier := newAutobanNotifyRecorder()
	svc := NewContentModerationService(nil, repo, nil, nil, userRepo, nil, nil, nil)
	svc.SetAdminBanNotifier(notifier.notify)

	cfg := defaultContentModerationConfig()
	cfg.Enabled = true
	cfg.AutoBanEnabled = true
	cfg.BanThreshold = 10
	cfg.ViolationWindowHours = 720
	cfg.CyberPolicyExcludeFromBanCount = true

	log := cyberIncidentLog(userID)
	banned := svc.applyFlaggedAccountSideEffects(context.Background(), cfg, log)

	require.False(t, banned, "cyber_policy_exclude_from_ban_count=true 时 cyber_policy 命中不得触发封禁")
	require.Empty(t, userRepo.updated)
	require.Equal(t, 1, log.ViolationCount, "历史 cyber_policy 行不得计入违规次数")
	require.Empty(t, notifier.snapshot())
}

func TestApplyFlaggedAccountSideEffects_CyberPolicyCountsWhenExcludeDisabled(t *testing.T) {
	userID := int64(7006)
	repo := &contentModerationTestRepo{}
	for i := 0; i < 10; i++ {
		id := userID
		repo.logs = append(repo.logs, ContentModerationLog{
			UserID:    &id,
			Flagged:   true,
			Action:    ContentModerationActionCyberPolicy,
			CreatedAt: time.Now().Add(-time.Hour),
		})
	}
	userRepo := &autobanTestUserRepo{contentModerationTestUserRepo: contentModerationTestUserRepo{
		user: &User{ID: userID, Email: "u@example.com", Status: StatusActive, Role: RoleUser},
	}}
	notifier := newAutobanNotifyRecorder()
	svc := NewContentModerationService(nil, repo, nil, nil, userRepo, nil, nil, nil)
	svc.SetAdminBanNotifier(notifier.notify)

	cfg := defaultContentModerationConfig()
	cfg.Enabled = true
	cfg.AutoBanEnabled = true
	cfg.BanThreshold = 10
	cfg.CyberPolicyExcludeFromBanCount = false // 显式关闭排除

	log := cyberIncidentLog(userID)
	banned := svc.applyFlaggedAccountSideEffects(context.Background(), cfg, log)

	require.True(t, banned, "显式关闭排除后 cyber_policy 行必须照常计入封禁计数")
	require.Equal(t, 11, log.ViolationCount)
	require.Len(t, userRepo.updated, 1)
}

// ---------- 4. 自动封禁必须通知管理员 ----------

func TestApplyFlaggedAccountSideEffects_NotifiesAdminOnBan(t *testing.T) {
	userID := int64(7007)
	repo := &contentModerationTestRepo{}
	seedFlaggedLogs(repo, userID, 10)
	userRepo := &autobanTestUserRepo{contentModerationTestUserRepo: contentModerationTestUserRepo{
		user: &User{ID: userID, Email: "abuser@example.com", Username: "abuser", Status: StatusActive, Role: RoleUser},
	}}
	notifier := newAutobanNotifyRecorder()
	svc := NewContentModerationService(nil, repo, nil, nil, userRepo, nil, nil, nil)
	svc.SetAdminBanNotifier(notifier.notify)

	cfg := defaultContentModerationConfig()
	cfg.Enabled = true
	cfg.AutoBanEnabled = true
	cfg.BanThreshold = 10
	cfg.ViolationWindowHours = 720

	log := &ContentModerationLog{
		Flagged:         true,
		UserID:          &userID,
		UserEmail:       "abuser@example.com",
		Action:          ContentModerationActionCyberPolicy,
		HighestCategory: "cyber_policy",
		HighestScore:    1.0,
		Model:           "gpt-5-mini",
		Endpoint:        "/v1/responses",
		GroupName:       "vip",
	}

	banned := svc.applyFlaggedAccountSideEffects(context.Background(), cfg, log)
	require.True(t, banned)

	got := notifier.waitForCall(t)
	require.Equal(t, userID, got.UserID)
	require.Equal(t, "abuser@example.com", got.UserEmail)
	require.Equal(t, "abuser", got.Username)
	require.Equal(t, "cyber_policy", got.Category)
	require.InDelta(t, 1.0, got.Score, 0.0001)
	require.Equal(t, 11, got.ViolationCount)
	require.Equal(t, 10, got.BanThreshold)
	require.Equal(t, 720, got.WindowHours)
	require.Equal(t, "gpt-5-mini", got.Model)
	require.Equal(t, "/v1/responses", got.Endpoint)
	require.Equal(t, "vip", got.GroupName)
	require.False(t, got.BannedAt.IsZero())
}

func TestNotifyAdminsAutoBan_DefaultPathUsesOpsRecipients(t *testing.T) {
	// 默认通知路径：ops 告警收件人优先。这里用 GetFirstAdmin 路径验证
	// 在没有 ops / 工单邮箱时仍能找到管理员。SMTP 没配置时 SendEmail 会返回
	// ErrEmailNotConfigured，通知链路本身不 panic 即算通过。
	userID := int64(7008)
	repo := &contentModerationTestRepo{}
	seedFlaggedLogs(repo, userID, 10)
	userRepo := &autobanTestUserRepo{
		contentModerationTestUserRepo: contentModerationTestUserRepo{
			user: &User{ID: userID, Email: "u@example.com", Status: StatusActive, Role: RoleUser},
		},
		firstAdmin: &User{ID: 1, Email: "admin@example.com", Role: RoleAdmin, Status: StatusActive},
	}
	settingRepo := &contentModerationTestSettingRepo{values: map[string]string{}}
	emailService := NewEmailService(settingRepo, nil)
	svc := NewContentModerationService(settingRepo, repo, nil, nil, userRepo, nil, nil, emailService)

	cfg := defaultContentModerationConfig()
	cfg.Enabled = true
	cfg.AutoBanEnabled = true
	cfg.BanThreshold = 10

	require.True(t, svc.applyFlaggedAccountSideEffects(context.Background(), cfg, &ContentModerationLog{
		Flagged: true, UserID: &userID, UserEmail: "u@example.com", Action: "block", HighestCategory: "sexual",
	}))

	recipients, err := svc.adminBanRecipients(context.Background())
	require.NoError(t, err)
	require.Equal(t, []string{"admin@example.com"}, recipients)

	body := buildAutoBanAdminAlertEmailBody("Sub2API", cfg, adminBanNotification{
		UserID: userID, UserEmail: "u@example.com", Category: "cyber_policy", Score: 1,
		ViolationCount: 11, BanThreshold: 10, WindowHours: 720, Model: "gpt-5",
		BannedAt: time.Now(),
	})
	require.Contains(t, body, fmt.Sprintf("用户 ID：%d", userID))
	require.Contains(t, body, "触发分类：cyber_policy")
	require.Contains(t, body, "违规次数：11")
	require.Contains(t, body, "触发阈值：10")
	require.Contains(t, body, "最近命中模型：gpt-5")
	require.Contains(t, body, "***", "管理员邮件里的用户邮箱必须打码")
}

func TestNotifyAdminsAutoBan_RecipientsDedupeAndOrder(t *testing.T) {
	settingRepo := &contentModerationTestSettingRepo{values: map[string]string{
		SettingKeyOpsEmailNotificationConfig: `{"alert":{"enabled":true,"recipients":["ops@example.com","ops@example.com"]}}`,
		SettingKeyTicketAdminEmail:           "support@example.com",
	}}
	svc := NewContentModerationService(settingRepo, nil, nil, nil, nil, nil, nil, nil)
	svc.SetOpsService(NewOpsService(nil, settingRepo, nil, nil, nil, nil, nil, nil, nil, nil, nil))

	recipients, err := svc.adminBanRecipients(context.Background())
	require.NoError(t, err)
	require.Equal(t, []string{"ops@example.com", "support@example.com"}, recipients,
		"ops 告警收件人优先、同地址去重、再补工单管理员邮箱")
}

func TestNotifyAdminsAutoBan_NotifierErrorDoesNotBreakBan(t *testing.T) {
	userID := int64(7009)
	repo := &contentModerationTestRepo{}
	seedFlaggedLogs(repo, userID, 10)
	userRepo := &autobanTestUserRepo{contentModerationTestUserRepo: contentModerationTestUserRepo{
		user: &User{ID: userID, Email: "u@example.com", Status: StatusActive, Role: RoleUser},
	}}
	notifier := newAutobanNotifyRecorder()
	notifier.err = fmt.Errorf("smtp down")
	svc := NewContentModerationService(nil, repo, nil, nil, userRepo, nil, nil, nil)
	svc.SetAdminBanNotifier(notifier.notify)

	cfg := defaultContentModerationConfig()
	cfg.Enabled = true
	cfg.AutoBanEnabled = true
	cfg.BanThreshold = 10

	require.True(t, svc.applyFlaggedAccountSideEffects(context.Background(), cfg, &ContentModerationLog{
		Flagged: true, UserID: &userID, Action: "block", HighestCategory: "sexual",
	}), "管理员通知失败不能回滚已生效的封禁")
	require.Len(t, userRepo.updated, 1)

	// 通知确实被调用过（异步），失败只记日志。
	notifier.waitForCall(t)
}

func TestNotifyAdminsAutoBan_RateLimited(t *testing.T) {
	svc := NewContentModerationService(nil, nil, nil, nil, nil, nil, nil, nil)
	notifier := newAutobanNotifyRecorder()
	svc.SetAdminBanNotifier(notifier.notify)
	userID := int64(7010)
	user := &User{ID: userID, Email: "u@example.com"}
	cfg := defaultContentModerationConfig()

	for i := 0; i < contentModerationAdminBanNotifyLimitPerHour+5; i++ {
		svc.notifyAdminsAutoBan(context.Background(), cfg, &ContentModerationLog{
			Flagged: true, UserID: &userID, UserEmail: "u@example.com", HighestCategory: "cyber_policy",
		}, user)
	}
	// 等待可能已经启动的 goroutine 落地，再断言上限。
	time.Sleep(200 * time.Millisecond)

	require.LessOrEqual(t, len(notifier.snapshot()), contentModerationAdminBanNotifyLimitPerHour,
		"批量误封时管理员通知必须限流，不能打爆邮箱")
	require.NotZero(t, len(notifier.snapshot()))
}

func TestBuildAutoBanAdminAlertEmailBody_ContainsAllRequiredFields(t *testing.T) {
	cfg := defaultContentModerationConfig()
	body := buildAutoBanAdminAlertEmailBody("Sub2API", cfg, adminBanNotification{
		UserID: 42, UserEmail: "researcher@example.com", Username: "researcher",
		Category: "cyber_policy", Score: 1.0, ViolationCount: 10, BanThreshold: 10,
		WindowHours: 720, Model: "claude-opus-5-5", Endpoint: "/v1/messages",
		GroupName: "pro", APIKeyName: "ctf-key", BannedAt: time.Date(2026, 11, 24, 6, 0, 0, 0, time.UTC),
		RemediationHint: "check",
	})
	for _, want := range []string{"用户 ID：42", "researcher", "触发分类：cyber_policy", "违规次数：10",
		"触发阈值：10", "最近命中模型：claude-opus-5-5", "统计窗口：720 小时", "2026-11-24T06:00:00Z"} {
		require.True(t, strings.Contains(body, want), "管理员告警正文缺少 %q:\n%s", want, body)
	}
}
