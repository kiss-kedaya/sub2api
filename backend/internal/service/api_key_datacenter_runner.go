package service

import (
	"context"
	"database/sql"
	"fmt"
	"sync"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/logger"
	"github.com/google/uuid"
)

// The periodic datacenter-IP blacklist job. Every cycle it walks the keys that
// opted in (auto_block_datacenter_ips = true) and appends the hosting/proxy IPs
// they were recently used from to their ip_blacklist. The job is append-only:
// it never removes an entry, and a key whose classification fails (or returns
// nothing) is simply left as it is.
const (
	// APIKeyDatacenterAutoBlockInterval is how often the sweep runs.
	APIKeyDatacenterAutoBlockInterval = 5 * time.Minute
	// Cycle bounds keep one slow cycle from stacking on the next tick.
	apiKeyDatacenterAutoBlockCycleTimeout = 4 * time.Minute
	// Per-key bound so one slow key cannot starve the rest of the batch.
	apiKeyDatacenterAutoBlockKeyTimeout  = 60 * time.Second
	apiKeyDatacenterAutoBlockMaxPerCycle = 200
	// Rotating cursor: keys are scanned oldest-updated first and every write
	// bumps updated_at, so a full queue eventually visits every key.
	apiKeyDatacenterAutoBlockLeaderLockKey = "api_key:datacenter:auto-block:leader"
	apiKeyDatacenterAutoBlockLeaderLockTTL = apiKeyDatacenterAutoBlockCycleTimeout + time.Minute
	apiKeyDatacenterAutoBlockLogComponent  = "service.api_key_datacenter_auto_block"
)

// apiKeyAutoBlockLister is the narrow repository capability the runner needs.
// It is not part of APIKeyRepository so unrelated repositories and test stubs
// do not have to implement it.
type apiKeyAutoBlockLister interface {
	ListAutoBlockDatacenterIPsKeys(ctx context.Context, limit int) ([]APIKey, error)
}

// APIKeyDatacenterAutoBlockResult is the per-key outcome of one cycle.
type APIKeyDatacenterAutoBlockResult struct {
	KeyID   int64    `json:"key_id"`
	Checked int      `json:"checked_ip_count"`
	Added   []string `json:"added,omitempty"`
	Error   string   `json:"error,omitempty"`
}

// APIKeyDatacenterAutoBlockService runs the periodic, leader-elected datacenter
// IP blacklist sweep.
type APIKeyDatacenterAutoBlockService struct {
	apiKeyService *APIKeyService

	parentCtx    context.Context
	parentCancel context.CancelFunc
	wg           sync.WaitGroup
	mu           sync.Mutex
	started      bool
	stopped      bool
	runMu        sync.Mutex

	lockCache  LeaderLockCache
	db         *sql.DB
	instanceID string
}

// NewAPIKeyDatacenterAutoBlockService creates the runner without starting it.
func NewAPIKeyDatacenterAutoBlockService(apiKeyService *APIKeyService) *APIKeyDatacenterAutoBlockService {
	ctx, cancel := context.WithCancel(context.Background())
	return &APIKeyDatacenterAutoBlockService{
		apiKeyService: apiKeyService,
		parentCtx:     ctx,
		parentCancel:  cancel,
		instanceID:    uuid.NewString(),
	}
}

// SetLeaderLock wires cross-instance coordination.
func (s *APIKeyDatacenterAutoBlockService) SetLeaderLock(lockCache LeaderLockCache, db *sql.DB) {
	if s == nil {
		return
	}
	s.lockCache = lockCache
	s.db = db
}

// ProvideAPIKeyDatacenterAutoBlockService is the wire provider; it starts the
// process-wide runner the same way the model whitelist sync does.
func ProvideAPIKeyDatacenterAutoBlockService(
	apiKeyService *APIKeyService,
	lockCache LeaderLockCache,
	db *sql.DB,
) *APIKeyDatacenterAutoBlockService {
	svc := NewAPIKeyDatacenterAutoBlockService(apiKeyService)
	svc.SetLeaderLock(lockCache, db)
	startBackgroundJob(svc.Start)
	return svc
}

// Start begins the periodic loop (no-op when background jobs are disabled).
func (s *APIKeyDatacenterAutoBlockService) Start() {
	if BackgroundJobsDisabled() || s == nil {
		return
	}
	s.mu.Lock()
	if s.started || s.stopped {
		s.mu.Unlock()
		return
	}
	s.started = true
	s.wg.Add(1)
	s.mu.Unlock()
	go s.runLoop()
}

// Stop cancels the loop and waits for the in-flight cycle.
func (s *APIKeyDatacenterAutoBlockService) Stop() {
	if s == nil {
		return
	}
	s.mu.Lock()
	if s.stopped {
		s.mu.Unlock()
		return
	}
	s.stopped = true
	s.parentCancel()
	s.mu.Unlock()
	s.wg.Wait()
}

func (s *APIKeyDatacenterAutoBlockService) runLoop() {
	defer s.wg.Done()
	if _, err := s.RunDue(s.parentCtx); err != nil {
		logger.LegacyPrintf(apiKeyDatacenterAutoBlockLogComponent, "run_due_failed: err=%v", err)
	}
	ticker := time.NewTicker(APIKeyDatacenterAutoBlockInterval)
	defer ticker.Stop()
	for {
		select {
		case <-s.parentCtx.Done():
			return
		case <-ticker.C:
			if _, err := s.RunDue(s.parentCtx); err != nil {
				logger.LegacyPrintf(apiKeyDatacenterAutoBlockLogComponent, "run_due_failed: err=%v", err)
			}
		}
	}
}

// RunDue processes one bounded batch. A failure on one key is recorded in that
// key's result and never aborts the batch; only a leader-lock or listing error
// is returned, because those mean no key was inspected at all.
func (s *APIKeyDatacenterAutoBlockService) RunDue(ctx context.Context) ([]APIKeyDatacenterAutoBlockResult, error) {
	if s == nil || s.apiKeyService == nil || s.apiKeyService.apiKeyRepo == nil {
		return nil, nil
	}
	s.runMu.Lock()
	defer s.runMu.Unlock()

	lister, ok := s.apiKeyService.apiKeyRepo.(apiKeyAutoBlockLister)
	if !ok {
		return nil, fmt.Errorf("api key repository does not support auto block datacenter IP listing")
	}

	cycleCtx, cycleCancel := context.WithTimeout(ctx, apiKeyDatacenterAutoBlockCycleTimeout)
	defer cycleCancel()
	release, acquired, err := s.tryAcquireLeaderLock(cycleCtx, apiKeyDatacenterAutoBlockLeaderLockKey)
	if err != nil {
		return nil, fmt.Errorf("acquire auto block datacenter IP cycle lock: %w", err)
	}
	if !acquired {
		return nil, nil
	}
	defer release()

	keys, err := lister.ListAutoBlockDatacenterIPsKeys(cycleCtx, apiKeyDatacenterAutoBlockMaxPerCycle)
	if err != nil {
		return nil, fmt.Errorf("list auto block datacenter IP keys: %w", err)
	}

	results := make([]APIKeyDatacenterAutoBlockResult, 0, len(keys))
	for i := range keys {
		if cycleCtx.Err() != nil {
			break
		}
		key := keys[i]
		// Defensive: the lister is expected to filter, but the runner must never
		// touch a key whose switch is off.
		if !key.AutoBlockDatacenterIPs || key.ID <= 0 {
			continue
		}
		results = append(results, s.runOne(cycleCtx, key.ID))
	}
	return results, nil
}

// runOne re-reads the key (so a toggle turned off after listing is honored),
// then appends. Each key gets its own deadline so a slow lookup does not eat
// the whole cycle.
func (s *APIKeyDatacenterAutoBlockService) runOne(cycleCtx context.Context, keyID int64) APIKeyDatacenterAutoBlockResult {
	result := APIKeyDatacenterAutoBlockResult{KeyID: keyID}
	keyCtx, cancel := context.WithTimeout(cycleCtx, apiKeyDatacenterAutoBlockKeyTimeout)
	defer cancel()

	fresh, err := s.apiKeyService.apiKeyRepo.GetByID(keyCtx, keyID)
	if err != nil {
		result.Error = "load_failed"
		logger.LegacyPrintf(apiKeyDatacenterAutoBlockLogComponent,
			"key_id=%d load_failed: %v", keyID, err)
		return result
	}
	if fresh == nil || !fresh.AutoBlockDatacenterIPs {
		return result
	}
	added, checked, err := s.apiKeyService.AppendDatacenterIPsToBlacklist(keyCtx, fresh)
	result.Checked = checked
	result.Added = added
	if err != nil {
		// Fail open: the key's blacklist is unchanged and the next cycle retries.
		result.Error = "append_failed"
		logger.LegacyPrintf(apiKeyDatacenterAutoBlockLogComponent,
			"key_id=%d append_failed: %v", keyID, err)
		return result
	}
	if len(added) > 0 {
		logger.LegacyPrintf(apiKeyDatacenterAutoBlockLogComponent,
			"key_id=%d checked=%d added=%d", keyID, checked, len(added))
	}
	return result
}

func (s *APIKeyDatacenterAutoBlockService) tryAcquireLeaderLock(ctx context.Context, key string) (func(), bool, error) {
	lockCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	if s.lockCache != nil {
		acquired, err := s.lockCache.TryAcquireLeaderLock(lockCtx, key, s.instanceID, apiKeyDatacenterAutoBlockLeaderLockTTL)
		if err != nil {
			return nil, false, err
		}
		if !acquired {
			return nil, false, nil
		}
		return releaseOnce(func() {
			releaseCtx, releaseCancel := context.WithTimeout(context.Background(), 2*time.Second)
			defer releaseCancel()
			_ = s.lockCache.ReleaseLeaderLock(releaseCtx, key, s.instanceID)
		}), true, nil
	}
	if s.db != nil {
		release, acquired, err := tryAcquireDBAdvisoryLockWithError(lockCtx, s.db, hashAdvisoryLockID(key))
		if err != nil {
			return nil, false, err
		}
		return release, acquired, nil
	}
	return func() {}, true, nil
}
