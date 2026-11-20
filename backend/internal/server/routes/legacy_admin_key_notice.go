package routes

import (
	"context"
	"log/slog"
	"strings"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
)

// WarnLegacyAdminAPIKeyConfigured 在启动时提示仍配置着旧版全权管理员 API Key。
//
// 旧版密钥只保留「读取与使用」，新建/轮换入口已停止：调用方应把自动化迁移到受限
// 管理密钥，再删除旧 key。这里只读一次设置并输出迁移提示，不输出密钥内容，
// 也不阻塞启动（由调用方放到 goroutine 中执行）。
func WarnLegacyAdminAPIKeyConfigured(settingService *service.SettingService) {
	if settingService == nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	key, err := settingService.GetAdminAPIKey(ctx)
	if err != nil {
		slog.Warn("cannot read legacy admin API key state; migration status unknown", "error", err)
		return
	}
	if strings.TrimSpace(key) == "" {
		return
	}
	slog.Warn("legacy full-power admin API key is configured: " +
		"reading and using it is retained, but new creation/rotation is disabled; " +
		"migrate automation to scoped admin API keys (settings -> security -> admin access keys) and then delete the legacy key")
}
