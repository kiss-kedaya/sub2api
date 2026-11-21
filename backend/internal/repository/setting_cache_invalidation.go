package repository

import (
	"context"
	"fmt"
	"log"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/redis/go-redis/v9"
)

// settingsChangedChannel is the Redis pub/sub channel every replica subscribes
// to. A settings write publishes once; each replica then drops its rendered
// index.html cache and re-reads settings from the database.
const settingsChangedChannel = "settings:changed"

type settingCacheInvalidationBus struct {
	rdb *redis.Client
}

// NewSettingCacheInvalidationBus builds the Redis-backed cross-instance settings
// invalidation transport. Redis is required for any multi-replica deployment;
// when it is absent the subscriber is simply not started and the rendered HTML
// cache TTL remains the only convergence mechanism.
func NewSettingCacheInvalidationBus(rdb *redis.Client) service.SettingCacheInvalidationBus {
	if rdb == nil {
		return nil
	}
	return &settingCacheInvalidationBus{rdb: rdb}
}

func (b *settingCacheInvalidationBus) PublishSettingsChanged(ctx context.Context) error {
	// The payload is intentionally tiny; receivers only need the fact that
	// settings changed, not the values (they re-read the authoritative row).
	return b.rdb.Publish(ctx, settingsChangedChannel, "settings-changed").Err()
}

func (b *settingCacheInvalidationBus) SubscribeSettingsChanged(ctx context.Context, handler func()) error {
	if handler == nil {
		return nil
	}
	pubsub := b.rdb.Subscribe(ctx, settingsChangedChannel)
	// Receive confirms the subscription is registered before we start consuming,
	// so a settings write in between is not silently missed.
	if _, err := pubsub.Receive(ctx); err != nil {
		_ = pubsub.Close()
		return fmt.Errorf("subscribe to settings changed: %w", err)
	}
	defer func() {
		if err := pubsub.Close(); err != nil {
			log.Printf("Warning: failed to close settings cache invalidation pubsub: %v", err)
		}
	}()

	ch := pubsub.Channel()
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case _, ok := <-ch:
			if !ok {
				return nil
			}
			handler()
		}
	}
}
