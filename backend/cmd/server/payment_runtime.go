package main

import (
	"database/sql"

	"github.com/Wei-Shaw/sub2api/ent"
	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/cryptopay"
	"github.com/Wei-Shaw/sub2api/internal/handler"
	"github.com/Wei-Shaw/sub2api/internal/notify"
	"github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/redis/go-redis/v9"
)

// provideNotificationRuntime preserves the gateway notifications in both payment roles.
func provideNotificationRuntime(db *sql.DB, encryptor service.SecretEncryptor, balance *service.BalanceNotifyService, cnBalance *service.CNProviderBalanceCheckService, groupWatch *service.GroupWatchService) *notify.UserBarkService {
	bark := notify.NewUserBarkService(db, notify.AdaptEncryptor(encryptor))
	push := notify.NewPushAdapter(bark)
	balance.SetPushNotifier(push)
	cnBalance.SetPushNotifier(push)
	groupWatch.SetPushNotifier(push)
	middleware.SetBalanceExhaustedNotifier(balance.NotifyInsufficientBalance)
	return bark
}

type localCryptoRuntime struct {
	CryptoService        *cryptopay.Service
	CryptoHandler        *handler.CryptoHandler
	CryptoDepositHandler *handler.CryptoDepositHandler
}

func provideLocalCryptoRuntime(cfg *config.Config, db *sql.DB, client *ent.Client, settings *service.SettingService, payments *service.PaymentService, bark *notify.UserBarkService, totp *service.TotpService, email *service.EmailService, rdb *redis.Client) localCryptoRuntime {
	// The normal constructor checks shared wallet state synchronously, even
	// when this process delegates chain scanning. Skip construction entirely.
	if cfg.Server.DelegatePayments {
		return localCryptoRuntime{}
	}
	addresses := cryptopay.NewAddressService(db, settings)
	creditor := cryptopay.NewDepositCreditor(client, payments, db)
	creditor.SetBarkNotifier(bark)
	runtime := cryptopay.NewService(db, settings, creditor, addresses, cfg, rdb)
	runtime.SetPushNotifier(notify.NewOwnerPushAdapter(bark, 1))
	// Operator notifications (webhook/email) are best effort and wired from the
	// same crypto.config row the sweep destination lives in.
	runtime.SetEventNotifier(cryptopay.NewEventNotifier(settings, email))
	payments.SetDepositAddressResolver(runtime)
	return localCryptoRuntime{CryptoService: runtime, CryptoHandler: handler.NewCryptoHandler(addresses, settings, runtime, totp, cfg), CryptoDepositHandler: handler.NewCryptoDepositHandler(runtime)}
}

func provideLocalPaymentOrderExpiry(cfg *config.Config, payments *service.PaymentService, lockCache service.LeaderLockCache, db *sql.DB) *service.PaymentOrderExpiryService {
	if cfg.Server.DelegatePayments {
		return nil
	}
	return service.ProvidePaymentOrderExpiryService(payments, lockCache, db)
}
