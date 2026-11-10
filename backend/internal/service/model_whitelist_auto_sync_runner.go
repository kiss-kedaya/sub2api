package service

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"sort"
	"strings"
	"sync"
	"time"

	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
	"github.com/Wei-Shaw/sub2api/internal/pkg/logger"
	"github.com/google/uuid"
)

// ModelWhitelistAutoSyncStateExtraKey stores the runner's own runtime state,
// separate from the administrator-owned configuration key. Keeping them apart
// means a full account edit that rewrites `model_whitelist_auto_sync` cannot
// erase the last run/next run/error bookkeeping, and the runner never has to
// merge its writes into a map the UI also edits.
const ModelWhitelistAutoSyncStateExtraKey = "model_whitelist_auto_sync_state"

const (
	modelWhitelistAutoSyncCycleInterval  = time.Minute
	modelWhitelistAutoSyncRequestTimeout = 60 * time.Second
	modelWhitelistAutoSyncMaxPerCycle    = 20
	modelWhitelistAutoSyncDueScanLimit   = 100
	modelWhitelistAutoSyncCycleTimeout   = 21 * time.Minute
	modelWhitelistAutoSyncLeaderLockKey  = "model:whitelist:auto-sync:leader"
	// The lease covers the bounded cycle and is always released after completion.
	// TTL is only crash recovery if a process is terminated before defer runs.
	modelWhitelistAutoSyncLeaderLockTTL = 23 * time.Minute
	modelWhitelistAutoSyncMaxManualIDs  = 20
	modelWhitelistAutoSyncBackoffBase   = 5 * time.Minute
	modelWhitelistAutoSyncBackoffMax    = 6 * time.Hour
)

// ErrModelWhitelistAutoSyncUnavailable is returned when the runner has no
// account repository or upstream fetch service wired.
var ErrModelWhitelistAutoSyncUnavailable = infraerrors.ServiceUnavailable(
	"MODEL_WHITELIST_AUTO_SYNC_UNAVAILABLE", "model whitelist auto sync is unavailable",
)

// Reasons a run can finish without writing the account.
const (
	ModelWhitelistAutoSyncSkipDisabled     = "disabled"
	ModelWhitelistAutoSyncSkipCustomMap    = "custom_mapping"
	ModelWhitelistAutoSyncSkipEmptyCatalog = "empty_catalog"
	ModelWhitelistAutoSyncSkipConcurrent   = "concurrent_edit"
	ModelWhitelistAutoSyncSkipNotDue       = "not_due"
	ModelWhitelistAutoSyncSkipNotAPIKey    = "not_apikey_account"
	ModelWhitelistAutoSyncSkipLocked       = "locked"
)

// ModelWhitelistAutoSyncState is persisted in accounts.extra. Timestamps are
// stored as RFC3339 strings so the value survives a JSON round-trip without a
// schema migration.
type ModelWhitelistAutoSyncState struct {
	LastRunAt           *time.Time `json:"last_run_at,omitempty"`
	LastSuccessAt       *time.Time `json:"last_success_at,omitempty"`
	NextRunAt           *time.Time `json:"next_run_at,omitempty"`
	LastError           string     `json:"last_error,omitempty"`
	LastErrorAt         *time.Time `json:"last_error_at,omitempty"`
	LastSkippedReason   string     `json:"last_skipped_reason,omitempty"`
	ConsecutiveFailures int        `json:"consecutive_failures,omitempty"`
	LastAppliedModels   int        `json:"last_applied_models,omitempty"`
}

// ModelWhitelistAutoSyncResult is the manual-run response and the per-account
// unit the runner processes.
type ModelWhitelistAutoSyncResult struct {
	AccountID     int64                        `json:"account_id"`
	Applied       bool                         `json:"applied"`
	SkippedReason string                       `json:"skipped_reason,omitempty"`
	Models        []string                     `json:"models,omitempty"`
	ModelCount    int                          `json:"model_count"`
	Error         string                       `json:"error,omitempty"`
	State         *ModelWhitelistAutoSyncState `json:"state,omitempty"`
}

// ModelWhitelistAutoSyncService runs the periodic, strictly serial
// "fetch upstream models and replace model_mapping" job.
type ModelWhitelistAutoSyncService struct {
	accountRepo        AccountRepository
	accountTestService modelWhitelistFetcher

	parentCtx    context.Context
	parentCancel context.CancelFunc
	wg           sync.WaitGroup
	mu           sync.Mutex
	started      bool
	stopped      bool
	runMu        sync.Mutex

	now        func() time.Time
	lockCache  LeaderLockCache
	db         *sql.DB
	instanceID string
}

type modelWhitelistFetcher interface {
	FetchUpstreamSupportedModels(context.Context, *Account) ([]string, error)
}

type modelWhitelistAutoSyncDueLister interface {
	ListDueModelWhitelistAutoSyncAccounts(context.Context, time.Time, int) ([]Account, error)
}

type modelWhitelistAutoSyncMappingWriter interface {
	UpdateAccountModelMappingCAS(ctx context.Context, account *Account, expected, next map[string]string, expectedConfig map[string]any) (bool, error)
}

// NewModelWhitelistAutoSyncService creates the runner without starting it.
func NewModelWhitelistAutoSyncService(accountRepo AccountRepository, accountTestService modelWhitelistFetcher) *ModelWhitelistAutoSyncService {
	ctx, cancel := context.WithCancel(context.Background())
	return &ModelWhitelistAutoSyncService{
		accountRepo:        accountRepo,
		accountTestService: accountTestService,
		parentCtx:          ctx,
		parentCancel:       cancel,
		now:                time.Now,
		instanceID:         uuid.NewString(),
	}
}

// SetLeaderLock wires cross-instance coordination.
func (s *ModelWhitelistAutoSyncService) SetLeaderLock(lockCache LeaderLockCache, db *sql.DB) {
	if s == nil {
		return
	}
	s.lockCache = lockCache
	s.db = db
}

// ProvideModelWhitelistAutoSyncService is the wire provider; it starts the
// process-wide runner the same way the upstream billing probe does.
func ProvideModelWhitelistAutoSyncService(
	accountRepo AccountRepository,
	accountTestService *AccountTestService,
	lockCache LeaderLockCache,
	db *sql.DB,
) *ModelWhitelistAutoSyncService {
	svc := NewModelWhitelistAutoSyncService(accountRepo, accountTestService)
	svc.SetLeaderLock(lockCache, db)
	startBackgroundJob(svc.Start)
	return svc
}

// Start begins the periodic loop (no-op when background jobs are disabled).
func (s *ModelWhitelistAutoSyncService) Start() {
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
func (s *ModelWhitelistAutoSyncService) Stop() {
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

func (s *ModelWhitelistAutoSyncService) runLoop() {
	defer s.wg.Done()
	_ = s.RunDue(s.parentCtx)
	ticker := time.NewTicker(modelWhitelistAutoSyncCycleInterval)
	defer ticker.Stop()
	for {
		select {
		case <-s.parentCtx.Done():
			return
		case <-ticker.C:
			if err := s.RunDue(s.parentCtx); err != nil {
				logger.LegacyPrintf("service.model_whitelist_auto_sync", "run_due_failed: err=%v", err)
			}
		}
	}
}

// RunDue processes at most one bounded, strictly serial batch of due accounts.
func (s *ModelWhitelistAutoSyncService) RunDue(ctx context.Context) error {
	if s == nil || s.accountRepo == nil {
		return nil
	}
	s.runMu.Lock()
	defer s.runMu.Unlock()

	now := s.currentTime()
	cycleCtx, cycleCancel := context.WithTimeout(ctx, modelWhitelistAutoSyncCycleTimeout)
	defer cycleCancel()
	release, acquired, err := s.tryAcquireLeaderLock(cycleCtx, modelWhitelistAutoSyncLeaderLockKey)
	if err != nil {
		return fmt.Errorf("acquire model whitelist auto sync cycle lock: %w", err)
	}
	if !acquired {
		return nil
	}
	release = releaseOnce(release)
	defer release()

	lister, ok := s.accountRepo.(modelWhitelistAutoSyncDueLister)
	if !ok {
		return fmt.Errorf("account repository does not support model whitelist auto sync listing")
	}
	accounts, err := lister.ListDueModelWhitelistAutoSyncAccounts(cycleCtx, now, modelWhitelistAutoSyncDueScanLimit)
	if err != nil {
		return fmt.Errorf("list due model whitelist auto sync accounts: %w", err)
	}

	processed := 0
	for i := range accounts {
		if processed >= modelWhitelistAutoSyncMaxPerCycle {
			break
		}
		if err := cycleCtx.Err(); err != nil {
			return err
		}
		account := &accounts[i]
		freshAccount, readErr := s.accountRepo.GetByID(cycleCtx, account.ID)
		if readErr != nil {
			continue
		}
		account = freshAccount
		enabled, interval, ok := modelWhitelistAutoSyncConfig(account.Extra)
		if !ok || !enabled || !account.IsActive() {
			continue
		}
		state := decodeModelWhitelistAutoSyncState(account.Extra)
		if state != nil && state.NextRunAt != nil && now.Before(*state.NextRunAt) {
			continue
		}
		processed++
		// Serial by construction: one account at a time, each with its own
		// deadline, so a slow upstream cannot turn into a stampede.
		func() {
			accountCtx, cancel := context.WithTimeout(cycleCtx, modelWhitelistAutoSyncRequestTimeout)
			defer cancel()
			_, runErr := s.syncAccount(accountCtx, account, interval)
			if runErr != nil {
				logger.LegacyPrintf("service.model_whitelist_auto_sync",
					"account_id=%d sync_failed: %s", account.ID, safeModelWhitelistError(runErr))
			}
		}()
	}
	return nil
}

// RunNow performs the atomic "fetch upstream models and replace model_mapping"
// action for one account, ignoring the schedule but keeping every guard.
func (s *ModelWhitelistAutoSyncService) RunNow(ctx context.Context, accountID int64) (ModelWhitelistAutoSyncResult, error) {
	if s == nil || s.accountRepo == nil {
		return ModelWhitelistAutoSyncResult{AccountID: accountID}, ErrModelWhitelistAutoSyncUnavailable
	}
	s.runMu.Lock()
	defer s.runMu.Unlock()
	lockCtx, lockCancel := context.WithTimeout(ctx, 2*time.Second)
	release, acquired, err := s.tryAcquireLeaderLock(lockCtx, modelWhitelistAutoSyncLeaderLockKey)
	lockCancel()
	if err != nil {
		return ModelWhitelistAutoSyncResult{AccountID: accountID}, err
	}
	if !acquired {
		return ModelWhitelistAutoSyncResult{AccountID: accountID, SkippedReason: ModelWhitelistAutoSyncSkipLocked}, nil
	}
	release = releaseOnce(release)
	defer release()
	return s.runNowUnderGlobalLease(ctx, accountID)
}

// runNowUnderGlobalLease reloads every account field after the global lease is
// held. It is also used inside a batch that owns one lease for its full run.
func (s *ModelWhitelistAutoSyncService) runNowUnderGlobalLease(ctx context.Context, accountID int64) (ModelWhitelistAutoSyncResult, error) {
	account, err := s.accountRepo.GetByID(ctx, accountID)
	if err != nil {
		return ModelWhitelistAutoSyncResult{AccountID: accountID}, err
	}
	if account.Type != AccountTypeAPIKey {
		return ModelWhitelistAutoSyncResult{AccountID: accountID, SkippedReason: ModelWhitelistAutoSyncSkipNotAPIKey, State: decodeModelWhitelistAutoSyncState(account.Extra)}, nil
	}
	if !account.IsActive() {
		return ModelWhitelistAutoSyncResult{AccountID: accountID, SkippedReason: ModelWhitelistAutoSyncSkipDisabled, State: decodeModelWhitelistAutoSyncState(account.Extra)}, nil
	}
	enabled, interval, ok := modelWhitelistAutoSyncConfig(account.Extra)
	if !ok || !enabled {
		return ModelWhitelistAutoSyncResult{AccountID: accountID, SkippedReason: ModelWhitelistAutoSyncSkipDisabled, State: decodeModelWhitelistAutoSyncState(account.Extra)}, nil
	}
	runCtx, runCancel := context.WithTimeout(ctx, modelWhitelistAutoSyncRequestTimeout)
	defer runCancel()
	return s.syncAccount(runCtx, account, interval)
}

// RunManyNow processes an explicitly selected account set serially. It is used
// by the bulk-edit dialog so the browser sends one request and upstream calls
// never fan out concurrently. IDs are deduplicated and capped to bound request
// duration and upstream load; individual failures are returned per account.
func (s *ModelWhitelistAutoSyncService) RunManyNow(ctx context.Context, accountIDs []int64) []ModelWhitelistAutoSyncResult {
	if s == nil || s.accountRepo == nil {
		results := make([]ModelWhitelistAutoSyncResult, 0, len(accountIDs))
		for _, id := range accountIDs {
			results = append(results, ModelWhitelistAutoSyncResult{AccountID: id, Error: ErrModelWhitelistAutoSyncUnavailable.Error()})
		}
		return results
	}
	s.runMu.Lock()
	defer s.runMu.Unlock()
	batchCtx, batchCancel := context.WithTimeout(ctx, modelWhitelistAutoSyncCycleTimeout)
	defer batchCancel()
	lockCtx, lockCancel := context.WithTimeout(batchCtx, 2*time.Second)
	release, acquired, lockErr := s.tryAcquireLeaderLock(lockCtx, modelWhitelistAutoSyncLeaderLockKey)
	lockCancel()
	if lockErr != nil {
		return []ModelWhitelistAutoSyncResult{{Error: safeModelWhitelistError(lockErr)}}
	}
	if !acquired {
		return []ModelWhitelistAutoSyncResult{{SkippedReason: ModelWhitelistAutoSyncSkipLocked}}
	}
	release = releaseOnce(release)
	defer release()
	seen := make(map[int64]struct{}, len(accountIDs))
	results := make([]ModelWhitelistAutoSyncResult, 0, min(len(accountIDs), modelWhitelistAutoSyncMaxManualIDs))
	for _, id := range accountIDs {
		if err := batchCtx.Err(); err != nil {
			break
		}
		if id <= 0 {
			continue
		}
		if _, exists := seen[id]; exists {
			continue
		}
		seen[id] = struct{}{}
		if len(results) >= modelWhitelistAutoSyncMaxManualIDs {
			break
		}
		result, err := s.runNowUnderGlobalLease(batchCtx, id)
		if err != nil && result.Error == "" {
			result.Error = safeModelWhitelistError(err)
		}
		results = append(results, result)
	}
	return results
}

func releaseOnce(release func()) func() {
	var once sync.Once
	return func() { once.Do(release) }
}

// syncAccount is the single-account unit used by both the runner and RunNow.
// Order matters: guards run before the upstream call, and the write is a
// compare-and-set on credentials.model_mapping so a concurrent credential edit
// is never overwritten.
func (s *ModelWhitelistAutoSyncService) syncAccount(ctx context.Context, account *Account, interval time.Duration) (ModelWhitelistAutoSyncResult, error) {
	result := ModelWhitelistAutoSyncResult{AccountID: account.ID}
	now := s.currentTime()

	_, custom, err := modelWhitelistBaselineMapping(account)
	if err != nil {
		return s.finishWithError(ctx, account, interval, now, result, fmt.Errorf("read model_mapping: %w", err))
	}
	if custom {
		result.SkippedReason = ModelWhitelistAutoSyncSkipCustomMap
		return s.finishSkipped(ctx, account, interval, now, result)
	}
	baselineCredentials := cloneMap(account.Credentials)
	baselineConfig := autoSyncConfigSnapshot(account.Extra)

	if s.accountTestService == nil {
		return s.finishWithError(ctx, account, interval, now, result, ErrModelWhitelistAutoSyncUnavailable)
	}
	// The auto-sync policy must only overwrite from a live upstream response.
	// SyncUpstreamModelCatalog intentionally falls back to configured models for
	// unsupported /models endpoints, which is useful for capability hydration but
	// would make an auto-sync silently re-apply a stale whitelist.
	var models []string
	var fetchErr error
	for attempt := 0; attempt < 3; attempt++ {
		if attempt > 0 {
			backoff := time.Duration(attempt) * 3 * time.Second
			select {
			case <-time.After(backoff):
			case <-ctx.Done():
				fetchErr = ctx.Err()
			}
			if fetchErr != nil {
				break
			}
		}
		models, fetchErr = s.accountTestService.FetchUpstreamSupportedModels(ctx, account)
		if fetchErr == nil {
			break
		}
	}
	if fetchErr != nil {
		// Persistent upstream failure: an unreachable model list must never keep
		// a stale/closed whitelist. Clear the mapping so the account passes any
		// model through, then record the error for the operator.
		if writer, ok := s.accountRepo.(modelWhitelistAutoSyncMappingWriter); ok {
			if baseline, _, baseErr := modelWhitelistBaselineMapping(account); baseErr == nil {
				_, _ = writer.UpdateAccountModelMappingCAS(ctx, account, baseline, map[string]string{}, autoSyncConfigSnapshot(account.Extra))
			}
		}
		return s.finishWithError(ctx, account, interval, now, result, fmt.Errorf("upstream model sync failed after 3 attempts: %w", fetchErr))
	}
	models = normalizeModelIDs(models)
	if len(models) == 0 {
		result.SkippedReason = ModelWhitelistAutoSyncSkipEmptyCatalog
		return s.finishSkipped(ctx, account, interval, now, result)
	}
	current, err := s.accountRepo.GetByID(ctx, account.ID)
	if err != nil {
		return s.finishWithError(ctx, account, interval, now, result, err)
	}
	currentEnabled, currentInterval, configOK := modelWhitelistAutoSyncConfig(current.Extra)
	if current.Type != AccountTypeAPIKey || !current.IsActive() || !configOK || !currentEnabled || currentInterval != interval ||
		!reflect.DeepEqual(baselineConfig, autoSyncConfigSnapshot(current.Extra)) ||
		!reflect.DeepEqual(baselineCredentials, cloneMap(current.Credentials)) {
		result.SkippedReason = ModelWhitelistAutoSyncSkipConcurrent
		return s.finishSkipped(ctx, account, interval, now, result)
	}
	baseline, custom, err := modelWhitelistBaselineMapping(current)
	if err != nil {
		return s.finishWithError(ctx, current, interval, now, result, err)
	}
	if custom {
		result.SkippedReason = ModelWhitelistAutoSyncSkipCustomMap
		return s.finishSkipped(ctx, current, interval, now, result)
	}
	result.Models = models
	result.ModelCount = len(models)

	next := make(map[string]string, len(models))
	for _, model := range models {
		next[model] = model
	}

	writer, ok := s.accountRepo.(modelWhitelistAutoSyncMappingWriter)
	if !ok {
		return s.finishWithError(ctx, account, interval, now, result, fmt.Errorf("account repository does not support model mapping writes"))
	}
	applied, writeErr := writer.UpdateAccountModelMappingCAS(ctx, current, baseline, next, autoSyncConfigSnapshot(current.Extra))
	if writeErr != nil {
		return s.finishWithError(ctx, account, interval, now, result, writeErr)
	}
	if !applied {
		// Somebody edited the account (mapping or credentials) while we fetched.
		// Nothing was written; retry on the normal cadence.
		result.SkippedReason = ModelWhitelistAutoSyncSkipConcurrent
		return s.finishSkipped(ctx, account, interval, now, result)
	}

	result.Applied = true
	state := ModelWhitelistAutoSyncState{
		LastRunAt:         modelWhitelistTimePtr(now),
		LastSuccessAt:     modelWhitelistTimePtr(now),
		NextRunAt:         modelWhitelistTimePtr(now.Add(effectiveModelWhitelistInterval(interval, 0))),
		LastAppliedModels: len(models),
	}
	result.State = &state
	if err := s.writeState(ctx, account.ID, &state); err != nil {
		logger.LegacyPrintf("service.model_whitelist_auto_sync", "account_id=%d state_write_failed: %v", account.ID, err)
	}
	return result, nil
}

func cloneMap(input map[string]any) map[string]any {
	if input == nil {
		return map[string]any{}
	}
	output := make(map[string]any, len(input))
	for key, value := range input {
		output[key] = value
	}
	return output
}

func autoSyncConfigSnapshot(extra map[string]any) map[string]any {
	if extra == nil {
		return map[string]any{}
	}
	raw, ok := extra[ModelWhitelistAutoSyncExtraKey]
	if !ok {
		return map[string]any{}
	}
	config, ok := raw.(map[string]any)
	if !ok {
		return map[string]any{"invalid": true}
	}
	return cloneMap(config)
}

func (s *ModelWhitelistAutoSyncService) finishSkipped(ctx context.Context, account *Account, interval time.Duration, now time.Time, result ModelWhitelistAutoSyncResult) (ModelWhitelistAutoSyncResult, error) {
	state := ModelWhitelistAutoSyncState{
		LastRunAt:         modelWhitelistTimePtr(now),
		NextRunAt:         modelWhitelistTimePtr(now.Add(effectiveModelWhitelistInterval(interval, 0))),
		LastSkippedReason: result.SkippedReason,
	}
	if previous := decodeModelWhitelistAutoSyncState(account.Extra); previous != nil {
		state.LastSuccessAt = previous.LastSuccessAt
		state.ConsecutiveFailures = previous.ConsecutiveFailures
		state.LastError = previous.LastError
		state.LastErrorAt = previous.LastErrorAt
		state.LastAppliedModels = previous.LastAppliedModels
	}
	result.State = &state
	if err := s.writeState(ctx, account.ID, &state); err != nil {
		logger.LegacyPrintf("service.model_whitelist_auto_sync", "account_id=%d state_write_failed: %v", account.ID, err)
	}
	return result, nil
}

func (s *ModelWhitelistAutoSyncService) finishWithError(ctx context.Context, account *Account, interval time.Duration, now time.Time, result ModelWhitelistAutoSyncResult, runErr error) (ModelWhitelistAutoSyncResult, error) {
	failures := 1
	state := ModelWhitelistAutoSyncState{LastRunAt: modelWhitelistTimePtr(now)}
	if previous := decodeModelWhitelistAutoSyncState(account.Extra); previous != nil {
		failures = previous.ConsecutiveFailures + 1
		state.LastSuccessAt = previous.LastSuccessAt
		state.LastAppliedModels = previous.LastAppliedModels
	}
	state.ConsecutiveFailures = failures
	state.LastError = safeModelWhitelistError(runErr)
	state.LastErrorAt = modelWhitelistTimePtr(now)
	state.NextRunAt = modelWhitelistTimePtr(now.Add(modelWhitelistBackoff(interval, failures)))
	result.Error = state.LastError
	result.State = &state
	if err := s.writeState(ctx, account.ID, &state); err != nil {
		logger.LegacyPrintf("service.model_whitelist_auto_sync", "account_id=%d state_write_failed: %v", account.ID, err)
	}
	return result, runErr
}

func (s *ModelWhitelistAutoSyncService) writeState(ctx context.Context, accountID int64, state *ModelWhitelistAutoSyncState) error {
	if state == nil {
		return nil
	}
	// The upstream request context may already be deadline-exceeded when a
	// timeout is recorded. State is durable bookkeeping and must still be
	// persisted with a short independent timeout.
	stateCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	return s.accountRepo.UpdateExtra(stateCtx, accountID, map[string]any{
		ModelWhitelistAutoSyncStateExtraKey: state.toMap(),
	})
}

func (s *ModelWhitelistAutoSyncService) currentTime() time.Time {
	if s != nil && s.now != nil {
		return s.now()
	}
	return time.Now()
}

func (s *ModelWhitelistAutoSyncService) tryAcquireLeaderLock(ctx context.Context, key string) (func(), bool, error) {
	lockTTL := modelWhitelistAutoSyncLeaderLockTTL
	lockCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	if s.lockCache != nil {
		acquired, err := s.lockCache.TryAcquireLeaderLock(lockCtx, key, s.instanceID, lockTTL)
		if err != nil {
			return nil, false, err
		}
		if !acquired {
			return nil, false, nil
		}
		return func() {
			releaseCtx, releaseCancel := context.WithTimeout(context.Background(), 2*time.Second)
			defer releaseCancel()
			_ = s.lockCache.ReleaseLeaderLock(releaseCtx, key, s.instanceID)
		}, true, nil
	}
	if s.db != nil {
		return tryAcquireDBAdvisoryLockWithError(lockCtx, s.db, hashAdvisoryLockID(key))
	}
	return func() {}, true, nil
}

// modelWhitelistAutoSyncConfig reads the administrator-owned config object.
// ok=false means "no usable config" (absent or malformed), which the runner
// treats as disabled.
func modelWhitelistAutoSyncConfig(extra map[string]any) (enabled bool, interval time.Duration, ok bool) {
	// Default-on: an account without an explicit config participates in the
	// automatic upstream model sync. Only {"enabled": false} opts out.
	if extra == nil {
		return true, time.Duration(modelWhitelistAutoSyncDefaultIntervalHours) * time.Hour, true
	}
	raw, exists := extra[ModelWhitelistAutoSyncExtraKey]
	if !exists || raw == nil {
		return true, time.Duration(modelWhitelistAutoSyncDefaultIntervalHours) * time.Hour, true
	}
	config, isMap := raw.(map[string]any)
	if !isMap {
		return false, 0, false
	}
	enabled, _ = config["enabled"].(bool)
	hours := modelWhitelistAutoSyncDefaultIntervalHours
	if value, present := config["interval_hours"]; present && value != nil {
		if parsed, valid := modelWhitelistAutoSyncHours(value); valid && parsed > 0 {
			hours = parsed
		}
	}
	if hours < modelWhitelistAutoSyncMinIntervalHours {
		hours = modelWhitelistAutoSyncMinIntervalHours
	}
	if hours > modelWhitelistAutoSyncMaxIntervalHours {
		hours = modelWhitelistAutoSyncMaxIntervalHours
	}
	return enabled, time.Duration(hours) * time.Hour, true
}

// modelWhitelistBaselineMapping returns the mapping the runner may overwrite.
// A non-object value or any non-identity entry is treated as an administrator's
// custom mapping and blocks the write.
func modelWhitelistBaselineMapping(account *Account) (map[string]string, bool, error) {
	baseline := map[string]string{}
	if account == nil || account.Credentials == nil {
		return baseline, false, nil
	}
	raw, exists := account.Credentials["model_mapping"]
	if !exists || raw == nil {
		return baseline, false, nil
	}
	payload, err := json.Marshal(raw)
	if err != nil {
		return nil, false, err
	}
	var decoded map[string]any
	if err := json.Unmarshal(payload, &decoded); err != nil {
		return nil, true, nil
	}
	for key, value := range decoded {
		text, isString := value.(string)
		if !isString {
			return nil, true, nil
		}
		key = strings.TrimSpace(key)
		text = strings.TrimSpace(text)
		if key == "" {
			continue
		}
		if text != key {
			return nil, true, nil
		}
		baseline[key] = text
	}
	return baseline, false, nil
}

func normalizeModelIDs(input []string) []string {
	seen := make(map[string]struct{}, len(input))
	models := make([]string, 0, len(input))
	for _, model := range input {
		model = strings.TrimSpace(model)
		if model == "" {
			continue
		}
		if _, exists := seen[model]; exists {
			continue
		}
		seen[model] = struct{}{}
		models = append(models, model)
	}
	sort.Strings(models)
	return models
}

func effectiveModelWhitelistInterval(interval time.Duration, failures int) time.Duration {
	if interval <= 0 {
		interval = time.Duration(modelWhitelistAutoSyncDefaultIntervalHours) * time.Hour
	}
	if failures <= 0 {
		return interval
	}
	delay := modelWhitelistAutoSyncBackoffBase
	for i := 1; i < failures; i++ {
		if delay >= modelWhitelistAutoSyncBackoffMax {
			break
		}
		delay *= 2
	}
	if delay > modelWhitelistAutoSyncBackoffMax {
		delay = modelWhitelistAutoSyncBackoffMax
	}
	if delay < interval {
		delay = interval
	}
	return delay
}

func modelWhitelistBackoff(interval time.Duration, failures int) time.Duration {
	return effectiveModelWhitelistInterval(interval, failures)
}

func safeModelWhitelistError(err error) string {
	if err == nil {
		return ""
	}
	var syncErr *UpstreamModelSyncError
	if errors.As(err, &syncErr) {
		return syncErr.SafeMessage()
	}
	return "model_sync_failed"
}

func modelWhitelistTimePtr(value time.Time) *time.Time {
	utc := value.UTC()
	return &utc
}

// toMap renders the state for accounts.extra (RFC3339 strings survive JSON).
func (s *ModelWhitelistAutoSyncState) toMap() map[string]any {
	if s == nil {
		return nil
	}
	out := map[string]any{}
	if s.LastRunAt != nil {
		out["last_run_at"] = s.LastRunAt.UTC().Format(time.RFC3339)
	}
	if s.LastSuccessAt != nil {
		out["last_success_at"] = s.LastSuccessAt.UTC().Format(time.RFC3339)
	}
	if s.NextRunAt != nil {
		out["next_run_at"] = s.NextRunAt.UTC().Format(time.RFC3339)
	}
	if s.LastErrorAt != nil {
		out["last_error_at"] = s.LastErrorAt.UTC().Format(time.RFC3339)
	}
	if s.LastError != "" {
		out["last_error"] = s.LastError
	}
	if s.LastSkippedReason != "" {
		out["last_skipped_reason"] = s.LastSkippedReason
	}
	if s.ConsecutiveFailures > 0 {
		out["consecutive_failures"] = s.ConsecutiveFailures
	}
	if s.LastAppliedModels > 0 {
		out["last_applied_models"] = s.LastAppliedModels
	}
	return out
}

func decodeModelWhitelistAutoSyncState(extra map[string]any) *ModelWhitelistAutoSyncState {
	if extra == nil {
		return nil
	}
	raw, exists := extra[ModelWhitelistAutoSyncStateExtraKey]
	if !exists || raw == nil {
		return nil
	}
	decoded, ok := raw.(map[string]any)
	if !ok {
		payload, err := json.Marshal(raw)
		if err != nil {
			return nil
		}
		if err := json.Unmarshal(payload, &decoded); err != nil {
			return nil
		}
	}
	state := &ModelWhitelistAutoSyncState{}
	state.LastRunAt = parseModelWhitelistTime(decoded["last_run_at"])
	state.LastSuccessAt = parseModelWhitelistTime(decoded["last_success_at"])
	state.NextRunAt = parseModelWhitelistTime(decoded["next_run_at"])
	state.LastErrorAt = parseModelWhitelistTime(decoded["last_error_at"])
	if text, ok := decoded["last_error"].(string); ok {
		state.LastError = text
	}
	if text, ok := decoded["last_skipped_reason"].(string); ok {
		state.LastSkippedReason = text
	}
	state.ConsecutiveFailures = modelWhitelistIntValue(decoded["consecutive_failures"])
	state.LastAppliedModels = modelWhitelistIntValue(decoded["last_applied_models"])
	return state
}

func modelWhitelistIntValue(raw any) int {
	switch typed := raw.(type) {
	case int:
		return typed
	case int32:
		return int(typed)
	case int64:
		return int(typed)
	case float64:
		return int(typed)
	default:
		return 0
	}
}

func parseModelWhitelistTime(raw any) *time.Time {
	text, ok := raw.(string)
	if !ok || strings.TrimSpace(text) == "" {
		return nil
	}
	parsed, err := time.Parse(time.RFC3339, text)
	if err != nil {
		return nil
	}
	utc := parsed.UTC()
	return &utc
}
