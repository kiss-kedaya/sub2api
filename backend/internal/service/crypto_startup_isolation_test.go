package service

import (
	"testing"
	"time"
)

func TestCryptoReviewDoesNotInitializeGatewayCaches(t *testing.T) {
	t.Setenv("CRYPTO_ADMIN_TEST_MODE", "true")
	// A read-only payment process must not query gateway-only repositories or
	// subscribe to their Redis channels during construction.
	if NewTLSFingerprintProfileService(nil, nil) == nil || NewErrorPassthroughService(nil, nil) == nil {
		t.Fatal("gateway services must remain wired without starting their caches")
	}
}

func TestDisabledTicketOutboxDoesNotTouchRepository(t *testing.T) {
	for _, mode := range []string{"review", "background-disabled"} {
		t.Run(mode, func(t *testing.T) {
			t.Setenv("CRYPTO_ADMIN_TEST_MODE", "false")
			t.Setenv(disableBackgroundJobsEnv, "false")
			if mode == "review" {
				t.Setenv("CRYPTO_ADMIN_TEST_MODE", "true")
			} else {
				t.Setenv(disableBackgroundJobsEnv, "true")
			}
			queue := &EmailQueueService{stopChan: make(chan struct{})}
			defer close(queue.stopChan)
			svc := ProvideTicketService(nil, nil, queue, &NotificationEmailService{})
			done := make(chan struct{})
			go func() { queue.wg.Wait(); close(done) }()
			select {
			case <-done:
			case <-time.After(time.Second):
				t.Fatal("ticket provider started an outbox worker")
			}
			// The direct worker entry must also return before acquiring DB work.
			svc.runTicketEmailWorker(make(chan struct{}))
		})
	}
}
