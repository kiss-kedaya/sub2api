package service

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"sync"
	"sync/atomic"
	"time"

	"github.com/redis/go-redis/v9"
)

// KeyRiskReasonAutoIPSharing is the machine-readable source recorded for every
// key disabled by the gateway IP-sharing risk control. It is written to the
// structured log line and to the in-process marker surfaced by the key list.
const KeyRiskReasonAutoIPSharing = "auto_ip_sharing"

// apiKeyRiskMarkerTTL bounds how long the risk marker is attached to a key.
// The key itself stays inactive until the user rotates or re-enables it; the
// marker only drives the user-facing "this key may be stolen" hint.
const apiKeyRiskMarkerTTL = 30 * 24 * time.Hour

// apiKeyRiskRedisPrefix stores one JSON marker per auto-disabled key. It is a
// Redis key, not a database column: the marker is derived state with a TTL, so
// adding a migration for it would be more than this feature needs.
const apiKeyRiskRedisPrefix = "gateway_key_risk:"

// apiKeyRiskReadTimeout bounds the Redis read done when the in-process cache
// misses (another replica disabled the key).
const apiKeyRiskReadTimeout = 200 * time.Millisecond

// APIKeyRiskInfo is the evidence bundle attached to a key that the gateway
// disabled on its own. It answers "who did it, when, from which IP and how many
// distinct accounts were involved", which is what the operator needs when a
// shared egress was disabled by mistake.
type APIKeyRiskInfo struct {
	Reason        string
	IP            string
	DistinctUsers int
	WindowMinutes int
	HitAt         time.Time
}

type apiKeyRiskMarker struct {
	info      APIKeyRiskInfo
	expiresAt time.Time
}

// apiKeyRiskMarkers is the process-local cache. Redis (when configured) is the
// durable, cross-replica copy; the cache keeps the key list from paying a round
// trip on every row.
var apiKeyRiskMarkers = struct {
	mu    sync.RWMutex
	byKey map[int64]apiKeyRiskMarker
}{byKey: map[int64]apiKeyRiskMarker{}}

// apiKeyRiskRedis is configured once at router construction when Redis exists.
var apiKeyRiskRedis atomic.Pointer[redis.Client]

// ConfigureAPIKeyRiskStore lets the gateway wire the marker store to the shared
// Redis client. A nil client keeps the markers process-local.
func ConfigureAPIKeyRiskStore(client *redis.Client) {
	apiKeyRiskRedis.Store(client)
}

func apiKeyRiskRedisKey(keyID int64) string {
	return apiKeyRiskRedisPrefix + fmt.Sprintf("%d", keyID)
}

func markAPIKeyRiskDisabled(keyID int64, info APIKeyRiskInfo) {
	if keyID <= 0 {
		return
	}
	if info.HitAt.IsZero() {
		info.HitAt = time.Now()
	}
	if info.Reason == "" {
		info.Reason = KeyRiskReasonAutoIPSharing
	}
	storeAPIKeyRiskMarker(keyID, info)
	if client := apiKeyRiskRedis.Load(); client != nil {
		if payload, err := json.Marshal(info); err == nil {
			ctx, cancel := context.WithTimeout(context.Background(), apiKeyRiskReadTimeout)
			defer cancel()
			if err := client.Set(ctx, apiKeyRiskRedisKey(keyID), payload, apiKeyRiskMarkerTTL).Err(); err != nil {
				slog.Warn("gateway key risk marker could not be persisted", "key_id", keyID, "error", err)
			}
		}
	}
}

func storeAPIKeyRiskMarker(keyID int64, info APIKeyRiskInfo) {
	apiKeyRiskMarkers.mu.Lock()
	defer apiKeyRiskMarkers.mu.Unlock()
	if apiKeyRiskMarkers.byKey == nil {
		apiKeyRiskMarkers.byKey = map[int64]apiKeyRiskMarker{}
	}
	apiKeyRiskMarkers.byKey[keyID] = apiKeyRiskMarker{info: info, expiresAt: time.Now().Add(apiKeyRiskMarkerTTL)}
}

// APIKeyRiskInfoFor returns the risk marker of a key disabled by the gateway.
// The second result is false for keys disabled by the user or by quota/expiry.
func APIKeyRiskInfoFor(keyID int64) (APIKeyRiskInfo, bool) {
	if keyID <= 0 {
		return APIKeyRiskInfo{}, false
	}
	apiKeyRiskMarkers.mu.RLock()
	marker, ok := apiKeyRiskMarkers.byKey[keyID]
	apiKeyRiskMarkers.mu.RUnlock()
	if ok && time.Now().Before(marker.expiresAt) {
		return marker.info, true
	}
	if ok {
		clearAPIKeyRiskDisabled(keyID)
	}
	// Another replica may have disabled this key: fall back to the shared copy.
	client := apiKeyRiskRedis.Load()
	if client == nil {
		return APIKeyRiskInfo{}, false
	}
	ctx, cancel := context.WithTimeout(context.Background(), apiKeyRiskReadTimeout)
	defer cancel()
	payload, err := client.Get(ctx, apiKeyRiskRedisKey(keyID)).Bytes()
	if err != nil {
		return APIKeyRiskInfo{}, false
	}
	var info APIKeyRiskInfo
	if err := json.Unmarshal(payload, &info); err != nil {
		return APIKeyRiskInfo{}, false
	}
	storeAPIKeyRiskMarker(keyID, info)
	return info, true
}

func clearAPIKeyRiskDisabled(keyID int64) {
	if keyID <= 0 {
		return
	}
	apiKeyRiskMarkers.mu.Lock()
	delete(apiKeyRiskMarkers.byKey, keyID)
	apiKeyRiskMarkers.mu.Unlock()
	if client := apiKeyRiskRedis.Load(); client != nil {
		ctx, cancel := context.WithTimeout(context.Background(), apiKeyRiskReadTimeout)
		defer cancel()
		_ = client.Del(ctx, apiKeyRiskRedisKey(keyID)).Err()
	}
}

// resetAPIKeyRiskMarkers is used by tests to isolate cases.
func resetAPIKeyRiskMarkers() {
	apiKeyRiskMarkers.mu.Lock()
	defer apiKeyRiskMarkers.mu.Unlock()
	apiKeyRiskMarkers.byKey = map[int64]apiKeyRiskMarker{}
}

// DisableKeysForRisk disables the API keys that were used from a risky shared
// IP. It only flips status to inactive: it never deletes a key and never
// touches upstream accounts or groups.
//
// Idempotent by design: a key that is not active anymore is skipped, so a
// repeated detection (or two replicas racing) cannot write the same status
// twice. Returns the IDs it actually disabled plus the first error seen; one
// bad key never aborts the rest.
func (s *APIKeyService) DisableKeysForRisk(ctx context.Context, keyIDs []int64, info APIKeyRiskInfo) ([]int64, error) {
	if s == nil || s.apiKeyRepo == nil || len(keyIDs) == 0 {
		return nil, nil
	}
	if info.Reason == "" {
		info.Reason = KeyRiskReasonAutoIPSharing
	}
	if info.HitAt.IsZero() {
		info.HitAt = time.Now()
	}
	seen := make(map[int64]struct{}, len(keyIDs))
	disabled := make([]int64, 0, len(keyIDs))
	var firstErr error
	for _, id := range keyIDs {
		if id <= 0 {
			continue
		}
		if _, dup := seen[id]; dup {
			continue
		}
		seen[id] = struct{}{}

		key, err := s.apiKeyRepo.GetByID(ctx, id)
		if err != nil {
			if firstErr == nil {
				firstErr = fmt.Errorf("load api key %d: %w", id, err)
			}
			slog.Error("gateway key risk control could not load key", "key_id", id, "error", err)
			continue
		}
		if key == nil || key.Status != StatusActive {
			// Already inactive (including our own previous pass) or blocked by
			// quota/expiry: nothing to write.
			continue
		}
		key.Status = StatusAPIKeyInactive
		if err := s.apiKeyRepo.Update(ctx, key, APIKeyUpdateFields{Status: true}); err != nil {
			if firstErr == nil {
				firstErr = fmt.Errorf("disable api key %d: %w", id, err)
			}
			slog.Error("gateway key risk control could not disable key", "key_id", id, "error", err)
			continue
		}
		s.InvalidateAuthCacheByKey(ctx, key.Key)
		markAPIKeyRiskDisabled(id, info)
		disabled = append(disabled, id)
		// Structured audit trail: this is the durable record of the action
		// (source, hit time, IP and distinct users), independent of process
		// lifetime.
		slog.Warn("api key auto disabled by gateway IP risk control",
			"source", info.Reason,
			"key_id", id,
			"user_id", key.UserID,
			"ip", info.IP,
			"distinct_users", info.DistinctUsers,
			"window_minutes", info.WindowMinutes,
			"hit_at", info.HitAt,
		)
	}
	return disabled, firstErr
}
