package cryptopay

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

type concurrentPushNotifier struct{ calls atomic.Int64 }

func (p *concurrentPushNotifier) PushToAdmins(context.Context, string, string, string) error {
	p.calls.Add(1)
	return nil
}

func TestPushNotifierDoesNotWaitForConfigurationReload(t *testing.T) {
	svc := &Service{}
	push := &concurrentPushNotifier{}
	// Reload holds the config lock while waiting for its scanner to stop.
	// The scanner must be able to finish any in-flight alert during that wait.
	svc.mu.Lock()
	done := make(chan struct{})
	go func() {
		defer close(done)
		svc.SetPushNotifier(push)
		if notifier := svc.getPushNotifier(); notifier != nil {
			_ = notifier.PushToAdmins(context.Background(), "title", "body", "group")
		}
	}()
	select {
	case <-done:
		svc.mu.Unlock()
	case <-time.After(2 * time.Second):
		svc.mu.Unlock()
		<-done
		t.Fatal("scanner alert waited for the configuration lock; reload would deadlock")
	}
	if push.calls.Load() != 1 {
		t.Fatal("scanner alert was not delivered")
	}
}

func TestPushNotifierPublicationIsRaceSafe(t *testing.T) {
	svc := &Service{}
	push := &concurrentPushNotifier{}
	var wg sync.WaitGroup
	start := make(chan struct{})
	wg.Add(2)
	go func() {
		defer wg.Done()
		<-start
		for range 1000 {
			svc.SetPushNotifier(push)
			svc.SetPushNotifier(nil)
		}
	}()
	go func() {
		defer wg.Done()
		<-start
		for range 1000 {
			if notifier := svc.getPushNotifier(); notifier != nil {
				_ = notifier.PushToAdmins(context.Background(), "title", "body", "group")
			}
		}
	}()
	close(start)
	wg.Wait()
	svc.SetPushNotifier(push)
	if svc.getPushNotifier() != push {
		t.Fatal("published notifier was not visible")
	}
}
