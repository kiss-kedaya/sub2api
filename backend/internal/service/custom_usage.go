package service

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"net/http"
	"sync"
	"time"

	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
)

const customUsageCooldown = 15 * time.Second
const customUsageCacheLifetime = 24 * time.Hour
const customUsageCacheLimit = 512

func customUsageInterval(minutes int) time.Duration {
	if uint64(minutes) > uint64((time.Duration(1<<63-1))/time.Minute) {
		return time.Duration(1<<63 - 1)
	}
	return time.Duration(minutes) * time.Minute
}

// CustomUsageResult is the only query payload. Missing values stay absent on failure.
type CustomUsageResult struct {
	Enabled         bool       `json:"enabled"`
	Configured      bool       `json:"configured"`
	IntervalMinutes int        `json:"interval_minutes"`
	Remaining       *float64   `json:"remaining,omitempty"`
	Used            *float64   `json:"used,omitempty"`
	Total           *float64   `json:"total,omitempty"`
	Unit            string     `json:"unit"`
	PlanName        string     `json:"plan_name,omitempty"`
	UpdatedAt       *time.Time `json:"updated_at,omitempty"`
	Error           string     `json:"error,omitempty"`
	Stale           bool       `json:"stale,omitempty"`
}

// CustomUsageRepository uses the existing atomic JSONB merge, never a whole-row update.
type CustomUsageRepository interface {
	GetByID(context.Context, int64) (*Account, error)
	GetByIDs(context.Context, []int64) ([]*Account, error)
	BulkUpdate(context.Context, []int64, AccountBulkUpdate) (int64, error)
}
type customUsageCached struct {
	fingerprint [32]byte
	result      CustomUsageResult
	at          time.Time
}
type customUsageFlight struct {
	preview     bool
	fingerprint [32]byte
	done        chan struct{}
	result      CustomUsageResult
}
type customUsageEntry struct {
	saved, preview       customUsageCached
	lastAttempt, touched time.Time
	flight               *customUsageFlight
}

// CustomUsageService is admin-only and demand-driven: no background scans or gateway hooks.
type CustomUsageService struct {
	repo   CustomUsageRepository
	client *http.Client
	now    func() time.Time
	mu     sync.Mutex
	cache  map[int64]*customUsageEntry
	slots  chan struct{}
	writes [64]sync.Mutex
}

// NewCustomUsageService creates a bounded direct-public-only query service.
func NewCustomUsageService(repo CustomUsageRepository) *CustomUsageService {
	return &CustomUsageService{repo: repo, client: newCustomUsageClient(), now: time.Now, cache: map[int64]*customUsageEntry{}, slots: make(chan struct{}, upstreamBillingProbeConcurrency)}
}

// NewCustomUsageService shares the billing probe's repository and sizing conventions.
// Its hardened dial adapter is separate because the gateway client permits proxies.
func (s *UpstreamBillingProbeService) NewCustomUsageService() *CustomUsageService {
	if s == nil {
		return NewCustomUsageService(nil)
	}
	return NewCustomUsageService(s.accountRepo)
}
func (s *CustomUsageService) account(ctx context.Context, id int64) (*Account, error) {
	if s == nil || s.repo == nil {
		return nil, ErrUpstreamBillingProbeUnavailable
	}
	if id <= 0 {
		return nil, ErrCustomUsageConfig
	}
	a, err := s.repo.GetByID(ctx, id)
	if err != nil {
		if errors.Is(err, ErrAccountNotFound) {
			return nil, ErrAccountNotFound
		}
		return nil, infraerrors.ServiceUnavailable("CUSTOM_USAGE_READ_FAILED", "custom usage read failed")
	}
	if a == nil {
		return nil, ErrAccountNotFound
	}
	if a.Type != AccountTypeAPIKey || a.IsCredentialShadow() {
		return nil, ErrUpstreamBillingProbeAccountInvalid
	}
	return a, nil
}

// GetConfig returns only editable public fields and credential-presence booleans.
func (s *CustomUsageService) GetConfig(ctx context.Context, id int64) (*CustomUsageConfigView, error) {
	a, err := s.account(ctx, id)
	if err != nil {
		return nil, err
	}
	c, secrets, configured := readCustomUsage(a)
	return &CustomUsageConfigView{CustomUsageConfig: publicCustomUsage(c), Configured: configured, HasAPIKey: secrets.APIKey != "", HasAccessToken: secrets.AccessToken != ""}, nil
}

// PutConfig validates before atomically merging the two managed map keys.
func (s *CustomUsageService) PutConfig(ctx context.Context, id int64, c CustomUsageConfig) (*CustomUsageConfigView, error) {
	if id <= 0 {
		return nil, ErrCustomUsageConfig
	}
	lock := &s.writes[uint64(id)%uint64(len(s.writes))]
	lock.Lock()
	defer lock.Unlock()
	a, err := s.account(ctx, id)
	if err != nil {
		return nil, err
	}
	old, secrets, _ := readCustomUsage(a)
	c, secrets = mergeCustomUsage(c, old, secrets)
	prepared, err := prepareCustomUsage(a, c, secrets)
	if err != nil {
		return nil, err
	}
	c = prepared.config
	secrets.RequestURL = c.Request.URL
	secrets.Headers = c.Request.Headers
	public := publicCustomUsage(c)
	configJSON, err := json.Marshal(public)
	if err != nil {
		return nil, ErrCustomUsageConfig
	}
	secretJSON, err := json.Marshal(secrets)
	if err != nil {
		return nil, ErrCustomUsageConfig
	}
	var configMap, secretMap map[string]any
	if json.Unmarshal(configJSON, &configMap) != nil || json.Unmarshal(secretJSON, &secretMap) != nil {
		return nil, ErrCustomUsageConfig
	}
	n, err := s.repo.BulkUpdate(ctx, []int64{id}, AccountBulkUpdate{
		Credentials:         map[string]any{CustomUsageCredentialsKey: secretMap},
		Extra:               map[string]any{CustomUsageExtraKey: configMap},
		CustomUsageExpected: &AccountCustomUsageExpected{Credentials: a.Credentials, Config: a.Extra[CustomUsageExtraKey]},
	})
	if err != nil {
		return nil, infraerrors.ServiceUnavailable("CUSTOM_USAGE_SAVE_FAILED", "custom usage save failed")
	}
	if n != 1 {
		return nil, infraerrors.Conflict("CUSTOM_USAGE_CONFIG_CHANGED", "account configuration changed; reload before saving")
	}
	s.mu.Lock()
	if entry := s.cache[id]; entry != nil {
		entry.saved = customUsageCached{}
		entry.preview = customUsageCached{}
	}
	s.mu.Unlock()
	return &CustomUsageConfigView{CustomUsageConfig: public, Configured: true, HasAPIKey: secrets.APIKey != "", HasAccessToken: secrets.AccessToken != ""}, nil
}
func customUsageEmpty(c CustomUsageConfig, configured bool) CustomUsageResult {
	return CustomUsageResult{Enabled: c.Enabled, Configured: configured, IntervalMinutes: c.IntervalMinutes}
}
func customUsageFingerprint(p customUsagePrepared) [32]byte {
	payload := struct {
		Config  CustomUsageConfig
		URL     string
		Headers http.Header
	}{Config: p.config}
	if p.request != nil {
		payload.URL = p.request.URL.String()
		payload.Headers = p.request.Header
	}
	data, _ := json.Marshal(payload)
	return sha256.Sum256(data)
}
func (s *CustomUsageService) cachedLocked(e *customUsageEntry, p customUsagePrepared, fingerprint [32]byte, preview bool) (CustomUsageResult, bool) {
	r := customUsageEmpty(p.config, true)
	if e == nil {
		return r, false
	}
	value := e.saved
	if preview {
		value = e.preview
	}
	if value.fingerprint != fingerprint || s.now().Sub(value.at) > customUsageCacheLifetime {
		return r, false
	}
	r = value.result
	if r.UpdatedAt != nil && p.config.IntervalMinutes > 0 && s.now().Sub(*r.UpdatedAt) >= customUsageInterval(p.config.IntervalMinutes) {
		r.Stale = true
	}
	return r, true
}
func (s *CustomUsageService) entryLocked(id int64) *customUsageEntry {
	if e := s.cache[id]; e != nil {
		e.touched = s.now()
		return e
	}
	if len(s.cache) >= customUsageCacheLimit {
		var oldest int64
		var at time.Time
		for k, e := range s.cache {
			if e.flight != nil || s.now().Sub(e.lastAttempt) < customUsageCooldown {
				continue
			}
			if at.IsZero() || e.touched.Before(at) {
				oldest = k
				at = e.touched
			}
		}
		if at.IsZero() {
			return nil
		}
		delete(s.cache, oldest)
	}
	e := &customUsageEntry{touched: s.now()}
	s.cache[id] = e
	return e
}

// Query supports a non-persisting draft. force bypasses freshness, never cooldown.
// interval=0 and force=false is strictly cache-only; drafts are explicit manual previews.
func (s *CustomUsageService) Query(ctx context.Context, id int64, force bool, draft *CustomUsageConfig) (CustomUsageResult, error) {
	a, err := s.account(ctx, id)
	if err != nil {
		return CustomUsageResult{}, err
	}
	c, secrets, configured := readCustomUsage(a)
	if draft != nil {
		c, secrets = mergeCustomUsage(*draft, c, secrets)
		configured = true
	}
	empty := customUsageEmpty(c, configured)
	if !configured || !c.Enabled {
		return empty, nil
	}
	p, err := prepareCustomUsage(a, c, secrets)
	if err != nil {
		empty.Error = "invalid_config"
		return empty, nil
	}
	fingerprint := customUsageFingerprint(p)
	if ctx.Err() != nil {
		empty.Error = "request_cancelled"
		return empty, nil
	}
	s.mu.Lock()
	e := s.entryLocked(id)
	cached, ok := s.cachedLocked(e, p, fingerprint, draft != nil)
	if e == nil {
		s.mu.Unlock()
		empty.Error = "busy"
		return empty, nil
	}
	if !force && draft == nil && (p.config.IntervalMinutes == 0 || (ok && s.now().Sub(e.saved.at) < customUsageInterval(p.config.IntervalMinutes))) {
		s.mu.Unlock()
		if !ok {
			cached.Stale = true
		}
		return cached, nil
	}
	if e.flight != nil {
		f := e.flight
		s.mu.Unlock()
		if f.fingerprint != fingerprint || f.preview != (draft != nil) {
			empty.Error = "busy"
			return empty, nil
		}
		select {
		case <-ctx.Done():
			empty.Error = "request_cancelled"
			return empty, nil
		case <-f.done:
			return f.result, nil
		}
	}
	if !e.lastAttempt.IsZero() && s.now().Sub(e.lastAttempt) < customUsageCooldown {
		s.mu.Unlock()
		if ok {
			return cached, nil
		}
		empty.Error = "cooldown"
		return empty, nil
	}
	select {
	case s.slots <- struct{}{}:
	default:
		s.mu.Unlock()
		empty.Error = "busy"
		return empty, nil
	}
	f := &customUsageFlight{preview: draft != nil, fingerprint: fingerprint, done: make(chan struct{})}
	e.flight = f
	e.lastAttempt = s.now()
	s.mu.Unlock()
	result := s.fetch(ctx, p)
	lock := &s.writes[uint64(id)%uint64(len(s.writes))]
	lock.Lock()
	defer lock.Unlock()
	fresh, err := s.account(ctx, id)
	if err != nil {
		result = empty
		result.Error = "account_changed"
	} else {
		latest, creds, exists := readCustomUsage(fresh)
		if draft != nil {
			latest, creds = mergeCustomUsage(*draft, latest, creds)
			exists = true
		}
		prepared, err := prepareCustomUsage(fresh, latest, creds)
		if !exists || err != nil || customUsageFingerprint(prepared) != fingerprint {
			result = empty
			result.Error = "config_changed"
		}
	}
	s.mu.Lock()
	if result.Error != "" && ok && cached.UpdatedAt != nil && result.Error != "account_changed" && result.Error != "config_changed" {
		priorError := result.Error
		result = cached
		result.Error = priorError
		result.Stale = true
	}
	value := customUsageCached{fingerprint: fingerprint, result: result, at: s.now()}
	if draft == nil {
		e.saved = value
	} else {
		e.preview = value
	}
	f.result = result
	e.flight = nil
	close(f.done)
	s.mu.Unlock()
	<-s.slots
	return result, nil
}

// Batch performs one account read, returns only cache, and never issues upstream HTTP.
func (s *CustomUsageService) Batch(ctx context.Context, ids []int64) (map[int64]CustomUsageResult, error) {
	if s == nil || s.repo == nil {
		return nil, ErrUpstreamBillingProbeUnavailable
	}
	if len(ids) == 0 || len(ids) > CustomUsageMaxBatchSize {
		return nil, ErrCustomUsageConfig
	}
	unique := make([]int64, 0, len(ids))
	items := map[int64]CustomUsageResult{}
	for _, id := range ids {
		if id <= 0 {
			return nil, ErrCustomUsageConfig
		}
		if _, ok := items[id]; ok {
			continue
		}
		items[id] = CustomUsageResult{Error: "account_not_found"}
		unique = append(unique, id)
	}
	accounts, err := s.repo.GetByIDs(ctx, unique)
	if err != nil {
		return nil, infraerrors.ServiceUnavailable("CUSTOM_USAGE_READ_FAILED", "custom usage read failed")
	}
	for _, a := range accounts {
		if a == nil {
			continue
		}
		if _, requested := items[a.ID]; !requested {
			continue
		}
		c, secrets, configured := readCustomUsage(a)
		r := customUsageEmpty(c, configured)
		if a.Type != AccountTypeAPIKey || a.IsCredentialShadow() {
			r.Error = "unsupported_account"
			items[a.ID] = r
			continue
		}
		if !configured || !c.Enabled {
			items[a.ID] = r
			continue
		}
		p, err := prepareCustomUsage(a, c, secrets)
		if err != nil {
			r.Error = "invalid_config"
			items[a.ID] = r
			continue
		}
		fingerprint := customUsageFingerprint(p)
		s.mu.Lock()
		value, ok := s.cachedLocked(s.cache[a.ID], p, fingerprint, false)
		s.mu.Unlock()
		if ok {
			r = value
		} else {
			r.Stale = true
		}
		items[a.ID] = r
	}
	return items, nil
}

// StripCustomUsageManaged rejects write-through from generic create/update/import paths.
func StripCustomUsageManaged(values map[string]any, key string) map[string]any {
	if values == nil {
		return nil
	}
	out := shallowCopyMap(values)
	delete(out, key)
	return out
}
