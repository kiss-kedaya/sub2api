package cryptopay

import (
	"context"
	"sync"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/cryptopay/notify"
	"github.com/Wei-Shaw/sub2api/internal/cryptopay/sweeper"
	"github.com/Wei-Shaw/sub2api/internal/cryptopay/wallet"
	"github.com/stretchr/testify/require"
)

// capturingNotifier records events synchronously so tests can assert on what a
// phone or mailbox would have received.
type capturingNotifier struct {
	mu     sync.Mutex
	events []notify.Event
}

func (c *capturingNotifier) Notify(_ context.Context, ev notify.Event) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.events = append(c.events, ev)
}

func (c *capturingNotifier) sent() []notify.Event {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]notify.Event(nil), c.events...)
}

func TestNotifyEventCarriesActorAndIsOptional(t *testing.T) {
	t.Run("no notifier means no panic", func(t *testing.T) {
		service := &Service{}
		service.NotifyEvent(context.Background(), EventSignerSetup, nil)
	})

	t.Run("actor is attributed from the wallet audit context", func(t *testing.T) {
		service := &Service{}
		capture := &capturingNotifier{}
		service.SetEventNotifier(capture)
		actorID := int64(41)
		ctx := wallet.WithAuditActor(context.Background(), wallet.AuditActor{UserID: &actorID})
		service.NotifyEvent(ctx, EventSignerSetup, map[string]any{"vault_version": 3})

		events := capture.sent()
		require.Len(t, events, 1)
		require.Equal(t, EventSignerSetup, events[0].Event)
		require.Equal(t, int64(41), events[0].ActorID)
		require.Equal(t, 3, events[0].Detail["vault_version"])
		require.False(t, events[0].At.IsZero())
	})

	t.Run("a canceled request context still publishes", func(t *testing.T) {
		service := &Service{}
		capture := &capturingNotifier{}
		service.SetEventNotifier(capture)
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		service.NotifyEvent(ctx, EventSignerResplit, nil)
		require.Len(t, capture.sent(), 1, "a canceled HTTP context must not swallow the event")
	})
}

func TestNotifySignerEventPublishesOnlyPublicMetadata(t *testing.T) {
	service := &Service{}
	capture := &capturingNotifier{}
	service.SetEventNotifier(capture)
	status := &wallet.ShardStatus{
		Configured: true, Scheme: "shamir-gf256", Threshold: 2, Total: 3,
		VaultVersion: 3, Locked: true,
		// The commitment is public, but it must not be copied into a
		// notification either: the event is about the action, not the wallet.
		Commitment: "salted-digest-that-must-not-leak",
	}
	service.notifySignerEvent(context.Background(), EventSignerSetup, status, true, true)

	events := capture.sent()
	require.Len(t, events, 1)
	detail := events[0].Detail
	require.Equal(t, 3, detail["vault_version"])
	require.Equal(t, "shamir-gf256", detail["scheme"])
	require.Equal(t, 2, detail["threshold"])
	require.Equal(t, 3, detail["total"])
	locked, ok := detail["sweep_destination_locked"].(bool)
	require.True(t, ok)
	require.True(t, locked)
	lockCreated, ok := detail["sweep_destination_lock_created"].(bool)
	require.True(t, ok)
	require.True(t, lockCreated)
	require.NotContains(t, detail, "dk_commitment")
	for _, value := range detail {
		require.NotEqual(t, "salted-digest-that-must-not-leak", value)
	}
}

func TestReportSweepExecutedPublishesOnlyRealSweeps(t *testing.T) {
	summary := &sweeper.Summary{
		Planned: 3, Swept: 2, Failed: 1, GasTopUp: 1,
		Entries: []sweeper.SweepEntry{
			{Chain: "bsc", Token: "usdt", Address: "0xdeposit-one", Amount: "100", Status: "swept"},
			{Chain: "bsc", Token: "usdt", Address: "0xdeposit-two", Amount: "250", Status: "swept"},
			{Chain: "ethereum", Token: "native", Address: "0xdeposit-three", Amount: "5", Status: "failed"},
		},
	}

	t.Run("successful run reports counts, chains and raw amounts", func(t *testing.T) {
		service := &Service{}
		capture := &capturingNotifier{}
		service.SetEventNotifier(capture)
		service.reportSweepExecuted(context.Background(), summary, nil)

		events := capture.sent()
		require.Len(t, events, 1)
		require.Equal(t, EventSweepExecuted, events[0].Event)
		detail := events[0].Detail
		require.Equal(t, 2, detail["addresses"])
		require.Equal(t, 3, detail["planned"])
		require.Equal(t, 2, detail["swept"])
		require.Equal(t, 1, detail["failed"])
		require.Equal(t, []string{"bsc", "ethereum"}, detail["chains"])
		rawAmounts, ok := detail["amount_raw_by_asset"].(map[string]string)
		require.True(t, ok)
		require.Equal(t, "350", rawAmounts["bsc|usdt"], "swept amounts are summed per chain|token")
		require.NotContains(t, rawAmounts, "ethereum|native", "failed transfers moved no funds")
		// Addresses and hashes are deliberately absent from the payload.
		for _, value := range detail {
			require.NotEqual(t, "0xdeposit-one", value)
		}
	})

	t.Run("dry run, failure and empty sweeps stay silent", func(t *testing.T) {
		for name, tc := range map[string]struct {
			summary *sweeper.Summary
			runErr  error
		}{
			"dry run":       {summary: &sweeper.Summary{DryRun: true, Swept: 1}},
			"run error":     {summary: summary, runErr: context.DeadlineExceeded},
			"nothing swept": {summary: &sweeper.Summary{Planned: 2}},
			"nil summary":   {summary: nil},
		} {
			t.Run(name, func(t *testing.T) {
				service := &Service{}
				capture := &capturingNotifier{}
				service.SetEventNotifier(capture)
				service.reportSweepExecuted(context.Background(), tc.summary, tc.runErr)
				require.Empty(t, capture.sent())
			})
		}
	})
}

func TestNotifySignerLockEventIsDefinedForFutureManualLock(t *testing.T) {
	// There is no manual "lock the signer" admin action today (the vault locks
	// itself after every operation), so the event id stays reserved for the day
	// such a route exists.
	require.Equal(t, "signer.lock", EventSignerLock)
}
