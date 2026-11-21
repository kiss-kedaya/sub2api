package routes

import (
	"context"
	"log/slog"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
)

// MigrateAndWarnLegacyAdminAPIKey 在启动时处理仍配置着旧版全权管理员 API Key 的部署。
//
// 两件事：
//  1. 把历史明文行迁移为哈希存储并删除明文（只存哈希）；
//  2. 输出迁移提示：自动化应改用受限管理密钥，再删除旧 key。
//
// 旧版密钥只保留「读取与使用」，新建/轮换入口已停止。这里只读一次设置并
// 输出提示，不输出密钥内容，也不阻塞启动（由调用方放到 goroutine 中执行）。
func MigrateAndWarnLegacyAdminAPIKey(settingService *service.SettingService) {
	if settingService == nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	migrated, err := settingService.MigrateAdminAPIKeyStorage(ctx)
	if err != nil {
		slog.Warn("cannot migrate legacy admin api key storage; migration status unknown", "error", err)
		return
	}
	configured, err := settingService.AdminAPIKeyConfigured(ctx)
	if err != nil {
		slog.Warn("cannot read legacy admin api key state; migration status unknown", "error", err)
		return
	}
	if !configured {
		return
	}
	if migrated {
		slog.Warn("legacy admin api key storage upgraded to hashed-at-rest form")
	}
	slog.Warn("legacy full-power admin API key is configured: " +
		"reading and using it is retained, but new creation/rotation is disabled and it can only call the machine allowlist; " +
		"migrate automation to scoped admin API keys (settings -> security -> admin access keys) and then delete the legacy key")
}
