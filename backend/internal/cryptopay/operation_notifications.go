package cryptopay

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/cryptopay/mode"
)

// NotifyWalletOperation reports only a fixed operation/phase. No seed, code,
// private key, destination, device key or request body is sent to Bark.
func (s *Service) NotifyWalletOperation(ctx context.Context, action, phase string) error {
	if err := mode.CheckWritable(); err != nil {
		return err
	}
	labels := map[string]string{"sweep": "执行钱包归集", "destination_change": "修改归集地址", "export_mnemonics": "导出钱包助记词"}
	label, ok := labels[action]
	if !ok {
		return errors.New("invalid wallet notification action")
	}
	stage := ""
	switch phase {
	case "requested":
		stage = "已点击操作按钮，尚未完成 2FA 验证；此通知不代表操作已执行"
	case "verified":
		stage = "新鲜 2FA 验证已通过，即将执行操作；此通知不代表操作已完成"
	default:
		return errors.New("invalid wallet notification phase")
	}
	if s == nil {
		return errors.New("wallet security notifications are unavailable")
	}
	push := s.getPushNotifier()
	if push == nil {
		return errors.New("wallet security notifications are unavailable")
	}
	notifyCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	return push.PushToAdmins(notifyCtx, "钱包敏感操作："+label,
		fmt.Sprintf("操作：%s\n状态：%s\n时间：%s", label, stage, time.Now().UTC().Format(time.RFC3339)), "crypto-security")
}
