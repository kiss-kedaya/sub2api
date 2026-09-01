package repository

import (
	"context"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/Wei-Shaw/sub2api/ent"
	"github.com/Wei-Shaw/sub2api/ent/setting"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"golang.org/x/sync/singleflight"
)

// settingCacheTTL controls the process-local settings cache. Settings change
// infrequently (usually from the admin UI); successful writes invalidate the
// affected entries immediately. The TTL is a safety net for writes performed
// by another process/instance, so cross-instance changes become visible within
// a bounded interval.
const settingCacheTTL = 30 * time.Second
const settingQueryTimeout = 30 * time.Second

type settingCacheEntry struct {
	setting   *service.Setting // nil when found is false (negative cache)
	found     bool
	expiresAt int64 // unix nanoseconds
}

type settingAllCacheEntry struct {
	values    map[string]string
	expiresAt int64 // unix nanoseconds
}

type settingRepository struct {
	client *ent.Client

	mu         sync.Mutex
	keyCache   map[string]settingCacheEntry
	allCache   *settingAllCacheEntry
	version    uint64
	loadSingle singleflight.Group
}

func NewSettingRepository(client *ent.Client) service.SettingRepository {
	return &settingRepository{
		client:   client,
		keyCache: make(map[string]settingCacheEntry),
	}
}

func (r *settingRepository) peekKeyCache(key string) (settingCacheEntry, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	entry, ok := r.keyCache[key]
	if !ok || time.Now().UnixNano() >= entry.expiresAt {
		return settingCacheEntry{}, false
	}
	return entry, true
}

// storeKeyCacheIfVersion avoids a stale in-flight read repopulating an entry
// after a successful write invalidated it.
func (r *settingRepository) storeKeyCacheIfVersion(key string, entry settingCacheEntry, version uint64) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.version != version {
		return
	}
	if r.keyCache == nil {
		r.keyCache = make(map[string]settingCacheEntry)
	}
	r.keyCache[key] = entry
}

func (r *settingRepository) storeAllCacheIfVersion(entry *settingAllCacheEntry, version uint64) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.version != version {
		return
	}
	r.allCache = entry
}

func (r *settingRepository) cacheVersion() uint64 {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.version
}

func (r *settingRepository) invalidateKeyCache(key string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.version++
	delete(r.keyCache, key)
}

func (r *settingRepository) invalidateAllCache() {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.version++
	r.allCache = nil
}

func (r *settingRepository) loadSetting(ctx context.Context, key string) (*service.Setting, bool, error) {
	m, err := r.client.Setting.Query().Where(setting.KeyEQ(key)).Only(ctx)
	if err != nil {
		if ent.IsNotFound(err) {
			return nil, false, nil
		}
		return nil, false, err
	}
	return &service.Setting{
		ID:        m.ID,
		Key:       m.Key,
		Value:     m.Value,
		UpdatedAt: m.UpdatedAt,
	}, true, nil
}

func settingLoadContext(ctx context.Context) (context.Context, context.CancelFunc) {
	base := context.WithoutCancel(ctx)
	return context.WithTimeout(base, settingQueryTimeout)
}

func (r *settingRepository) Get(ctx context.Context, key string) (*service.Setting, error) {
	if entry, ok := r.peekKeyCache(key); ok {
		if !entry.found {
			return nil, service.ErrSettingNotFound
		}
		value := *entry.setting
		return &value, nil
	}

	flightVersion := r.cacheVersion()
	flightKey := "setting:" + key + ":" + strconv.FormatUint(flightVersion, 10)
	resultCh := r.loadSingle.DoChan(flightKey, func() (any, error) {
		if entry, ok := r.peekKeyCache(key); ok {
			return entry, nil
		}
		queryCtx, cancel := settingLoadContext(ctx)
		defer cancel()
		value, found, err := r.loadSetting(queryCtx, key)
		if err != nil {
			return nil, err
		}
		entry := settingCacheEntry{
			setting:   value,
			found:     found,
			expiresAt: time.Now().Add(settingCacheTTL).UnixNano(),
		}
		r.storeKeyCacheIfVersion(key, entry, flightVersion)
		return entry, nil
	})
	var value any
	select {
	case result := <-resultCh:
		if result.Err != nil {
			return nil, result.Err
		}
		value = result.Val
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	entry, ok := value.(settingCacheEntry)
	if !ok || !entry.found {
		return nil, service.ErrSettingNotFound
	}
	result := *entry.setting
	return &result, nil
}

func (r *settingRepository) GetValue(ctx context.Context, key string) (string, error) {
	value, err := r.Get(ctx, key)
	if err != nil {
		return "", err
	}
	return value.Value, nil
}

func (r *settingRepository) Set(ctx context.Context, key, value string) error {
	now := time.Now()
	err := r.client.Setting.
		Create().
		SetKey(key).
		SetValue(value).
		SetUpdatedAt(now).
		OnConflictColumns(setting.FieldKey).
		UpdateNewValues().
		Exec(ctx)
	if err == nil {
		r.invalidateKeyCache(key)
		r.invalidateAllCache()
	}
	return err
}

func (r *settingRepository) GetMultiple(ctx context.Context, keys []string) (map[string]string, error) {
	if len(keys) == 0 {
		return map[string]string{}, nil
	}

	result := make(map[string]string, len(keys))
	missing := make([]string, 0, len(keys))
	seenMissing := make(map[string]struct{}, len(keys))
	for _, key := range keys {
		if entry, ok := r.peekKeyCache(key); ok {
			if entry.found {
				result[key] = entry.setting.Value
			}
			continue
		}
		if _, seen := seenMissing[key]; !seen {
			seenMissing[key] = struct{}{}
			missing = append(missing, key)
		}
	}
	if len(missing) == 0 {
		return result, nil
	}

	// Canonicalize the key set so callers that request the same settings in a
	// different order still share one in-flight query.
	flightKeys := append([]string(nil), missing...)
	sort.Strings(flightKeys)
	flightVersion := r.cacheVersion()
	flightKey := "setting-multi:" + strconv.FormatUint(flightVersion, 10) + ":" + strings.Join(flightKeys, "\x00")
	resultCh := r.loadSingle.DoChan(flightKey, func() (any, error) {
		remaining := make([]string, 0, len(missing))
		for _, key := range missing {
			if _, ok := r.peekKeyCache(key); !ok {
				remaining = append(remaining, key)
			}
		}
		if len(remaining) == 0 {
			return map[string]string{}, nil
		}
		queryCtx, cancel := settingLoadContext(ctx)
		defer cancel()
		settings, err := r.client.Setting.Query().Where(setting.KeyIn(remaining...)).All(queryCtx)
		if err != nil {
			return nil, err
		}
		values := make(map[string]string, len(settings))
		now := time.Now().Add(settingCacheTTL).UnixNano()
		found := make(map[string]struct{}, len(settings))
		for _, m := range settings {
			values[m.Key] = m.Value
			found[m.Key] = struct{}{}
			r.storeKeyCacheIfVersion(m.Key, settingCacheEntry{
				setting:   &service.Setting{ID: m.ID, Key: m.Key, Value: m.Value, UpdatedAt: m.UpdatedAt},
				found:     true,
				expiresAt: now,
			}, flightVersion)
		}
		for _, key := range remaining {
			if _, ok := found[key]; !ok {
				r.storeKeyCacheIfVersion(key, settingCacheEntry{found: false, expiresAt: now}, flightVersion)
			}
		}
		return values, nil
	})
	var loaded any
	select {
	case result := <-resultCh:
		if result.Err != nil {
			return nil, result.Err
		}
		loaded = result.Val
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	if values, ok := loaded.(map[string]string); ok {
		for key, value := range values {
			result[key] = value
		}
	}
	// A caller queued behind another batch may have received an empty result
	// because that batch populated the cache while it waited. Re-read all
	// originally missing keys so the method still returns a complete map.
	for _, key := range missing {
		if _, exists := result[key]; exists {
			continue
		}
		if entry, ok := r.peekKeyCache(key); ok && entry.found {
			result[key] = entry.setting.Value
		}
	}
	return result, nil
}

func (r *settingRepository) SetMultiple(ctx context.Context, settings map[string]string) error {
	if len(settings) == 0 {
		return nil
	}

	now := time.Now()
	builders := make([]*ent.SettingCreate, 0, len(settings))
	for key, value := range settings {
		builders = append(builders, r.client.Setting.Create().SetKey(key).SetValue(value).SetUpdatedAt(now))
	}
	err := r.client.Setting.
		CreateBulk(builders...).
		OnConflictColumns(setting.FieldKey).
		UpdateNewValues().
		Exec(ctx)
	if err == nil {
		for key := range settings {
			r.invalidateKeyCache(key)
		}
		r.invalidateAllCache()
	}
	return err
}

func (r *settingRepository) GetAll(ctx context.Context) (map[string]string, error) {
	r.mu.Lock()
	if r.allCache != nil && time.Now().UnixNano() < r.allCache.expiresAt {
		values := cloneSettingValues(r.allCache.values)
		r.mu.Unlock()
		return values, nil
	}
	r.mu.Unlock()

	flightVersion := r.cacheVersion()
	flightKey := "setting-all:" + strconv.FormatUint(flightVersion, 10)
	resultCh := r.loadSingle.DoChan(flightKey, func() (any, error) {
		r.mu.Lock()
		if r.allCache != nil && time.Now().UnixNano() < r.allCache.expiresAt {
			values := cloneSettingValues(r.allCache.values)
			r.mu.Unlock()
			return values, nil
		}
		r.mu.Unlock()

		queryCtx, cancel := settingLoadContext(ctx)
		defer cancel()
		settings, err := r.client.Setting.Query().All(queryCtx)
		if err != nil {
			return nil, err
		}
		values := make(map[string]string, len(settings))
		for _, s := range settings {
			values[s.Key] = s.Value
		}
		r.storeAllCacheIfVersion(&settingAllCacheEntry{
			values:    values,
			expiresAt: time.Now().Add(settingCacheTTL).UnixNano(),
		}, flightVersion)
		return values, nil
	})
	var value any
	select {
	case result := <-resultCh:
		if result.Err != nil {
			return nil, result.Err
		}
		value = result.Val
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	values, ok := value.(map[string]string)
	if !ok {
		return map[string]string{}, nil
	}
	return cloneSettingValues(values), nil
}

func cloneSettingValues(values map[string]string) map[string]string {
	result := make(map[string]string, len(values))
	for key, value := range values {
		result[key] = value
	}
	return result
}

func (r *settingRepository) Delete(ctx context.Context, key string) error {
	_, err := r.client.Setting.Delete().Where(setting.KeyEQ(key)).Exec(ctx)
	if err == nil {
		r.invalidateKeyCache(key)
		r.invalidateAllCache()
	}
	return err
}
