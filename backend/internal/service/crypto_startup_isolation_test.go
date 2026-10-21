package service

import (
	"context"
	"testing"
	"time"
)

type cryptoReviewSettingsObserver struct {
	SettingRepository
	read chan struct{}
}

func (s *cryptoReviewSettingsObserver) GetValue(context.Context, string) (string, error) {
	select {
	case s.read <- struct{}{}:
	default:
	}
	return "", ErrSettingNotFound
}

type cryptoReviewModerationRepository struct {
	ContentModerationRepository
}

func TestCryptoReviewDoesNotStartModerationPolling(t *testing.T) {
	t.Setenv("CRYPTO_ADMIN_TEST_MODE", "true")
	observer := &cryptoReviewSettingsObserver{read: make(chan struct{}, 1)}
	NewContentModerationService(observer, &cryptoReviewModerationRepository{}, nil, nil, nil, nil, nil, nil)
	select {
	case <-observer.read:
		t.Fatal("moderation constructor started polling production settings")
	case <-time.After(100 * time.Millisecond):
	}
}

func TestCryptoReviewMissingRuntimeLogConfigDoesNotPersistDefaults(t *testing.T) {
	t.Setenv("CRYPTO_ADMIN_TEST_MODE", "true")
	repo := newRuntimeSettingRepoStub()
	svc := &OpsService{settingRepo: repo}
	cfg, err := svc.GetRuntimeLogConfig(context.Background())
	if err != nil || cfg == nil {
		t.Fatalf("read-only startup must retain usable log defaults: %v", err)
	}
	if repo.setCalls != 0 {
		t.Fatal("read-only startup tried to persist missing log settings")
	}
}

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
