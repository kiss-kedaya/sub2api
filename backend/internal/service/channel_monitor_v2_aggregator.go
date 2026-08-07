package service

import (
	"context"
	"database/sql"
	"sync"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/logger"
	"github.com/google/uuid"
)

const (
	channelMonitorV2AggregatorLockKey = "channel-monitor-v2-aggregator"
	// Backfill walks back to the longest stored tier (1d rollup = 90d). Per-tier
	// prune in the repository drops short-lived 1m/user/hist facts earlier.
	channelMonitorV2RetentionMax  = 90 * 24 * time.Hour
	channelMonitorV2RecentOverlap = 10 * time.Minute
	channelMonitorV2InitialWindow = 2 * time.Hour
	channelMonitorV2BackfillChunk = 24 * time.Hour
)

// channelMonitorRuntimeSubscriber is the optional settings hook that lets the
// aggregator wake immediately when channel_monitor_enabled / mode flips.
type channelMonitorRuntimeSubscriber interface {
	SubscribeChannelMonitorRuntime(listener func()) (unsubscribe func())
}

type ChannelMonitorV2Aggregator struct {
	repo       ChannelMonitorV2Repository
	db         *sql.DB
	settings   channelMonitorRuntimeReader
	instanceID string
	stopCh     chan struct{}
	// kickCh wakes the loop early after a settings change (buffered 1).
	kickCh     chan struct{}
	startOnce  sync.Once
	stopOnce   sync.Once
	mu         sync.Mutex
	backfillAt time.Time
	unsub      func()
	ctx        context.Context
	cancel     context.CancelFunc
}

func NewChannelMonitorV2Aggregator(repo ChannelMonitorV2Repository, db *sql.DB, settings channelMonitorRuntimeReader) *ChannelMonitorV2Aggregator {
	return &ChannelMonitorV2Aggregator{
		repo:       repo,
		db:         db,
		settings:   settings,
		instanceID: uuid.NewString(),
		stopCh:     make(chan struct{}),
		kickCh:     make(chan struct{}, 1),
	}
}

func (s *ChannelMonitorV2Aggregator) Start() {
	if s == nil || s.repo == nil {
		return
	}
	s.startOnce.Do(func() {
		s.mu.Lock()
		s.ctx, s.cancel = context.WithCancel(context.Background())
		s.mu.Unlock()
		if sub, ok := s.settings.(channelMonitorRuntimeSubscriber); ok && sub != nil {
			unsub := sub.SubscribeChannelMonitorRuntime(func() {
				s.kick()
			})
			s.mu.Lock()
			stopped := s.ctx == nil
			if !stopped {
				select {
				case <-s.ctx.Done():
					stopped = true
				default:
				}
			}
			if !stopped {
				s.unsub = unsub
			}
			s.mu.Unlock()
			if stopped && unsub != nil {
				unsub()
			}
		}
		go s.loop()
	})
}

func (s *ChannelMonitorV2Aggregator) Stop() {
	if s == nil {
		return
	}
	s.stopOnce.Do(func() {
		s.mu.Lock()
		cancel := s.cancel
		unsub := s.unsub
		s.cancel = nil
		s.unsub = nil
		s.mu.Unlock()
		if cancel != nil {
			cancel()
		}
		if unsub != nil {
			unsub()
		}
		close(s.stopCh)
	})
}

// kick wakes the aggregation loop so mode flips take effect without waiting
// for the next refresh interval.
func (s *ChannelMonitorV2Aggregator) kick() {
	if s == nil {
		return
	}
	select {
	case s.kickCh <- struct{}{}:
	default:
	}
}

func (s *ChannelMonitorV2Aggregator) loop() {
	for {
		interval := time.Minute
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		if !s.passiveAggregationAllowed(ctx) {
			cancel()
			if !s.wait(interval) {
				return
			}
			continue
		}
		if cfg, err := s.repo.GetConfig(ctx); err == nil {
			if !cfg.Enabled {
				cancel()
				if !s.wait(interval) {
					return
				}
				continue
			}
			if cfg.RefreshIntervalSeconds > 0 {
				interval = time.Duration(cfg.RefreshIntervalSeconds) * time.Second
			}
		}
		cancel()
		s.runOnce()
		if !s.wait(interval) {
			return
		}
	}
}

func (s *ChannelMonitorV2Aggregator) passiveAggregationAllowed(ctx context.Context) bool {
	if s == nil || s.settings == nil {
		// Fail closed without settings: do not aggregate under ambiguous mode.
		return false
	}
	return s.settings.GetChannelMonitorRuntime(ctx).PassiveAggregationAllowed()
}

func (s *ChannelMonitorV2Aggregator) wait(interval time.Duration) bool {
	timer := time.NewTimer(interval)
	defer timer.Stop()
	select {
	case <-timer.C:
		return true
	case <-s.kickCh:
		// Drain any coalesced kicks so a burst of settings writes only wakes once.
		for {
			select {
			case <-s.kickCh:
			default:
				return true
			}
		}
	case <-s.stopCh:
		return false
	}
}

func (s *ChannelMonitorV2Aggregator) runOnce() {
	s.mu.Lock()
	parent := s.ctx
	s.mu.Unlock()
	if parent == nil {
		parent = context.Background()
	}
	ctx, cancel := context.WithTimeout(parent, 55*time.Second)
	defer cancel()
	release, acquired := tryAcquireSingletonLeaderLock(ctx, nil, s.db, channelMonitorV2AggregatorLockKey, s.instanceID, 2*time.Minute)
	if !acquired {
		return
	}
	if release != nil {
		defer release()
	}

	now := time.Now().UTC().Truncate(time.Minute)
	if s.backfillAt.IsZero() {
		start := now.Add(-channelMonitorV2InitialWindow)
		if err := s.repo.RecomputeRange(ctx, start, now); err != nil {
			logger.LegacyPrintf("service.channel_monitor_v2", "[ChannelMonitorV2] recent aggregation failed: %v", err)
			return
		}
		s.backfillAt = start
		return
	}

	if err := s.repo.RecomputeRange(ctx, now.Add(-channelMonitorV2RecentOverlap), now); err != nil {
		logger.LegacyPrintf("service.channel_monitor_v2", "[ChannelMonitorV2] overlap aggregation failed: %v", err)
		return
	}
	cutoff := now.Add(-channelMonitorV2RetentionMax)
	if s.backfillAt.After(cutoff) {
		end := s.backfillAt
		start := end.Add(-channelMonitorV2BackfillChunk)
		if start.Before(cutoff) {
			start = cutoff
		}
		if err := s.repo.RecomputeRange(ctx, start, end); err != nil {
			logger.LegacyPrintf("service.channel_monitor_v2", "[ChannelMonitorV2] backfill failed %s..%s: %v", start, end, err)
			return
		}
		s.backfillAt = start
	}
}
