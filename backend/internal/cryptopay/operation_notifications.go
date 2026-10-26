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
	labels := map[string]string{"sweep": "执行钱包归集", "destination_change": "修改归集地址", "export_mnemonics": "导出钱包助记词", "export_private_key": "导出单地址私钥"}
	label, ok := labels[action]
	if !ok {
		return errors.New("invalid wallet notification action")
	}
	stage := ""
	switch phase {
	case "requested":
		stage = "已点击操作按钮，等待服务器检查支付授权；此通知不代表操作已执行"
	case "verified":
		stage = "服务器已验证当前支付授权，即将执行操作；此通知不代表操作已完成"
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
	if optional, ok := push.(interface {
		WalletOperationNotificationsEnabled(context.Context) (bool, error)
	}); ok {
		enabled, err := optional.WalletOperationNotificationsEnabled(ctx)
		if err != nil {
			return err
		}
		if !enabled {
			return nil
		}
	}
	notifyCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	return push.PushToAdmins(notifyCtx, "钱包敏感操作："+label,
		fmt.Sprintf("操作：%s\n状态：%s\n时间：%s", label, stage, time.Now().UTC().Format(time.RFC3339)), "crypto-security")
}
