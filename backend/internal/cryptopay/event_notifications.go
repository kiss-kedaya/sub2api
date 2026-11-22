package cryptopay

import (
	"context"
	"math/big"
	"sort"
	"strings"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/cryptopay/notify"
	"github.com/Wei-Shaw/sub2api/internal/cryptopay/sweeper"
	"github.com/Wei-Shaw/sub2api/internal/cryptopay/wallet"
	"github.com/Wei-Shaw/sub2api/internal/service"
)

// Event ids published for operator notifications. They are part of the public
// contract: webhook receivers and mail filters key on these strings.
const (
	EventSignerSetup             = "signer.setup"
	EventSignerResplit           = "signer.resplit"
	EventSignerVerifyShare       = "signer.verify_share"
	EventSignerUnlock            = "signer.unlock"
	EventSignerLock              = "signer.lock"
	EventSweepDestinationChanged = "sweep.destination_changed"
	EventSweepExecuted           = "sweep.executed"
)

// NewEventNotifier builds the best-effort operator notifier: every event is
// resolved against the current crypto.config.notify and delivered to the
// configured webhook/email. A nil settings service disables notifications
// (delegated payments processes never touch the crypto wallet).
func NewEventNotifier(settings *service.SettingService, email notify.EmailSender) notify.Notifier {
	if settings == nil {
		return nil
	}
	store := NewServiceSettingStore(settings)
	provider := func(ctx context.Context) (notify.Config, error) {
		cfg, err := LoadConfig(ctx, store)
		if err != nil {
			return notify.Config{}, err
		}
		if cfg.Notify == nil || !cfg.Notify.Enabled {
			return notify.Config{}, nil
		}
		return notify.Config{
			Enabled:    true,
			WebhookURL: cfg.Notify.WebhookURL,
			Email:      cfg.Notify.Email,
		}, nil
	}
	sinks := []notify.Sink{notify.NewWebhookSink()}
	if email != nil {
		sinks = append(sinks, notify.NewEmailSink(email))
	}
	return notify.NewDispatcher(provider, notify.WithSinks(sinks...))
}

// SetEventNotifier wires the operator notification channel. Nil disables it.
func (s *Service) SetEventNotifier(notifier notify.Notifier) {
	if s == nil {
		return
	}
	s.eventsMu.Lock()
	s.events = notifier
	s.eventsMu.Unlock()
}

// NotifyEvent publishes one operator event. It never blocks, never fails and
// is a no-op when no notifier is wired. Detail must contain observable,
// non-secret fields only: no shards, mnemonics, private keys, TOTP seeds or the
// vault data key.
func (s *Service) NotifyEvent(ctx context.Context, event string, detail map[string]any) {
	if s == nil || event == "" {
		return
	}
	s.eventsMu.RLock()
	notifier := s.events
	s.eventsMu.RUnlock()
	if notifier == nil {
		return
	}
	actorID := wallet.AuditActorUserID(ctx)
	notifier.Notify(context.WithoutCancel(ctx), notify.Event{
		Event:   event,
		At:      time.Now().UTC(),
		ActorID: actorID,
		Detail:  detail,
	})
}

// reportSweepExecuted publishes sweep.executed after a real run actually moved
// funds. A run that only reconciled or planned is not reported here: the failure
// path already has its own alerts, and "nothing was swept" is not an execution.
//
// Only observable fields are published. Amounts are the raw on-chain integer
// amounts (smallest unit, grouped by chain|token): the operator sees real
// numbers instead of a made-up decimal total, and the notification carries no
// address, hash or signing material.
func (s *Service) reportSweepExecuted(ctx context.Context, summary *sweeper.Summary, runErr error) {
	if s == nil || summary == nil || summary.DryRun || runErr != nil || summary.Swept <= 0 {
		return
	}
	chains := make(map[string]struct{}, len(summary.Entries))
	amounts := make(map[string]*big.Int)
	for _, entry := range summary.Entries {
		if entry.Chain != "" {
			chains[entry.Chain] = struct{}{}
		}
		if entry.Status != "swept" || strings.TrimSpace(entry.Amount) == "" {
			continue
		}
		raw, ok := new(big.Int).SetString(strings.TrimSpace(entry.Amount), 10)
		if !ok {
			continue
		}
		key := entry.Chain + "|" + entry.Token
		if current, exists := amounts[key]; exists {
			current.Add(current, raw)
			continue
		}
		amounts[key] = raw
	}
	detail := map[string]any{
		"dry_run":     summary.DryRun,
		"planned":     summary.Planned,
		"swept":       summary.Swept,
		"failed":      summary.Failed,
		"pending":     summary.Pending,
		"gas_topups":  summary.GasTopUp,
		"addresses":   summary.Swept,
		"chains":      sortedStringSet(chains),
		"amount_unit": "raw",
	}
	if len(amounts) > 0 {
		rawAmounts := make(map[string]string, len(amounts))
		for key, amount := range amounts {
			rawAmounts[key] = amount.String()
		}
		detail["amount_raw_by_asset"] = rawAmounts
	}
	s.NotifyEvent(ctx, EventSweepExecuted, detail)
}

func sortedStringSet(set map[string]struct{}) []string {
	values := make([]string, 0, len(set))
	for value := range set {
		values = append(values, value)
	}
	sort.Strings(values)
	return values
}
