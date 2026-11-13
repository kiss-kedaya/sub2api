package service

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"math"
	"math/rand/v2"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
	"github.com/Wei-Shaw/sub2api/internal/pkg/logger"
	"github.com/Wei-Shaw/sub2api/internal/pkg/tlsfingerprint"
	"github.com/google/uuid"
	"golang.org/x/sync/errgroup"
	"golang.org/x/sync/singleflight"
)

const (
	// These values live in accounts.extra so PR2 does not require a schema migration.
	UpstreamBillingProbeExtraKey           = "upstream_billing_probe"
	UpstreamBillingProbeEnabledExtraKey    = "upstream_billing_probe_enabled"
	UpstreamBillingRateSyncEnabledExtraKey = "upstream_billing_rate_sync_enabled"

	upstreamBillingProbeDefaultIntervalMinutes = 30
	upstreamBillingProbeMinIntervalMinutes     = 5
	upstreamBillingProbeMaxIntervalMinutes     = 24 * 60
	upstreamBillingProbeCycleInterval          = time.Minute
	upstreamBillingProbeRequestTimeout         = 10 * time.Second
	upstreamBillingProbeMaxBodyBytes           = 64 * 1024
	upstreamBillingProbeMaxPerCycle            = 20
	upstreamBillingProbeConcurrency            = 4
	upstreamBillingProbeMaxDelay               = 24 * time.Hour
	// unsupported 账号与「识别到了但读不到余额」的账号统一按 10 分钟重探：
	// 老板的要求是「识别到了就自动填、10 分钟刷一次」。原先按 interval×8 拉长
	// 到数小时，页面上就看到「旧数据 · 2 小时前」。
	upstreamBillingProbeUnsupportedRetryInterval = 10 * time.Minute
	upstreamBillingProbeAccountRateScale         = 10000.0
	upstreamBillingProbeLeaderLockKey            = "upstream:billing:probe:leader"
	upstreamBillingProbeLeaderLockTTL            = 2 * time.Minute
	// upstreamProbeMaxRedirects bounds the manual hop-following done by
	// fetchUpstreamProbeBodyFollowingRedirects. new-api answers a trailing-slash
	// mismatch with one redirect; anything longer is not a usable balance path.
	upstreamProbeMaxRedirects = 3
)

// UpstreamBillingProbeMaxBatchSize limits one manual batch and one runner cycle.
const UpstreamBillingProbeMaxBatchSize = upstreamBillingProbeMaxPerCycle

// upstreamBillingRateSyncMaxMultiplier bounds the value the automatic
// write-back may push into accounts.rate_multiplier.
//
// No other code path bounds that column from above — admins may type any
// non-negative number and the only ceiling is the DECIMAL(10,4) column itself
// (999999.9999). That ceiling is meaningless as a guard: rate_multiplier
// scales the per-request account cost that feeds quota_used, so a single
// declared 999999 would exhaust any account quota on the first request and
// poison cost reporting. 100 is picked as a deliberately generous bound: it is
// two orders of magnitude above the 1.0 default and far above any plausible
// upstream resale markup, so no legitimate declaration is rejected while an
// absurd or hostile one cannot reach the quota control plane unattended.
// It only constrains the automatic path; manual edits keep their old range.
const upstreamBillingRateSyncMaxMultiplier = 100.0

var (
	ErrUpstreamBillingProbeUnavailable = infraerrors.ServiceUnavailable(
		"UPSTREAM_BILLING_PROBE_UNAVAILABLE", "upstream billing probe is unavailable",
	)
	ErrUpstreamBillingProbeAccountInvalid = infraerrors.BadRequest(
		"UPSTREAM_BILLING_PROBE_ACCOUNT_INVALID", "account is not an API key account",
	)
	ErrUpstreamBillingProbeIdentityChanged = infraerrors.Conflict(
		"UPSTREAM_BILLING_PROBE_IDENTITY_CHANGED", "account identity changed during upstream billing probe; retry the probe",
	)
	ErrUpstreamBillingRateSyncBulkConflict = infraerrors.Conflict(
		"UPSTREAM_BILLING_RATE_SYNC_BULK_CONFLICT",
		"account rate multiplier cannot be changed in bulk while upstream billing rate sync is enabled",
	)
	ErrUpstreamBillingRateSyncConflict = infraerrors.Conflict(
		"UPSTREAM_BILLING_RATE_SYNC_CONFLICT",
		"account rate multiplier cannot be changed while upstream billing rate sync is enabled",
	)
)

const (
	UpstreamBillingProbeStatusOK          = "ok"
	UpstreamBillingProbeStatusUnsupported = "unsupported"
	UpstreamBillingProbeStatusFailed      = "failed"
	// UpstreamBillingProbeStatusPending marks a balance read that is still being
	// retried for an upstream already recognised as Sub2API / New API. It is
	// deliberately not a failure: the account list keeps the last successful
	// value (see upstream_balance_retry.go).
	UpstreamBillingProbeStatusPending = "pending"
)

// UpstreamBillingProbeSettings controls the periodic probe runner.
type UpstreamBillingProbeSettings struct {
	Enabled         bool `json:"enabled"`
	IntervalMinutes int  `json:"interval_minutes"`
}

// UpstreamBillingProbeSnapshot is persisted in accounts.extra. Data is kept as
// a sanitized map so future response fields do not require a database change.
type UpstreamBillingProbeSnapshot struct {
	Status        string         `json:"status"`
	Data          map[string]any `json:"data,omitempty"`
	ReceivedAt    *time.Time     `json:"received_at,omitempty"`
	FreshUntil    *time.Time     `json:"fresh_until,omitempty"`
	LastAttemptAt time.Time      `json:"last_attempt_at"`
	NextProbeAt   time.Time      `json:"next_probe_at"`
	FailureCount  int            `json:"failure_count,omitempty"`
	HTTPStatus    int            `json:"http_status,omitempty"`
	LastError     string         `json:"last_error,omitempty"`
	// SyncedRateMultiplier records the value this probe wrote into
	// accounts.rate_multiplier. It is only set when the account opted into rate
	// sync and the declared value passed the write-back range check, so the
	// stored snapshot always answers "did this probe move the account rate, and
	// to what" without a separate history table.
	SyncedRateMultiplier *float64                 `json:"synced_rate_multiplier,omitempty"`
	Balance              *UpstreamBalanceSnapshot `json:"balance,omitempty"`
	// UpstreamKind is what the probe recognised the upstream as (see the
	// UpstreamKind* constants). It is kept across failed probes, because a
	// timeout says nothing about which software the upstream runs.
	UpstreamKind string `json:"upstream_kind,omitempty"`
}

// UpstreamBillingProbeResult is returned by manual probe endpoints.
type UpstreamBillingProbeResult struct {
	AccountID int64                         `json:"account_id"`
	Snapshot  *UpstreamBillingProbeSnapshot `json:"snapshot,omitempty"`
	Error     string                        `json:"error,omitempty"`
}

// UpstreamBillingRateSnapshotItem is the compact representation used by the
// account table's background refresh. It intentionally excludes credentials,
// runtime counters, and usage data from the response.
type UpstreamBillingRateSnapshotItem struct {
	AccountID int64                         `json:"account_id"`
	Snapshot  *UpstreamBillingProbeSnapshot `json:"snapshot"`
}

// BuildUpstreamBillingRateSnapshotItems projects account rows into the
// read-only payload used by the rate refresh endpoint. Decode snapshots here
// so malformed or legacy extra data is handled consistently with probe logic.
func BuildUpstreamBillingRateSnapshotItems(accounts []Account) []UpstreamBillingRateSnapshotItem {
	items := make([]UpstreamBillingRateSnapshotItem, 0, len(accounts))
	for _, account := range accounts {
		var snapshot *UpstreamBillingProbeSnapshot
		// The billing endpoint is supported by every API-key platform; limiting
		// this projection to OpenAI would make the background refresh erase the
		// other platforms' persisted snapshots from the table.
		if account.Type == AccountTypeAPIKey {
			snapshot = decodeUpstreamBillingProbeSnapshot(account.Extra)
		}
		items = append(items, UpstreamBillingRateSnapshotItem{
			AccountID: account.ID,
			Snapshot:  snapshot,
		})
	}
	return items
}

type upstreamBillingProbeResponse struct {
	Object                  string   `json:"object"`
	SchemaVersion           int      `json:"schema_version"`
	BillingScope            string   `json:"billing_scope"`
	GroupRateMultiplier     *float64 `json:"group_rate_multiplier"`
	UserRateMultiplier      *float64 `json:"user_rate_multiplier"`
	ResolvedRateMultiplier  *float64 `json:"resolved_rate_multiplier"`
	PeakRateEnabled         *bool    `json:"peak_rate_enabled"`
	PeakStart               *string  `json:"peak_start"`
	PeakEnd                 *string  `json:"peak_end"`
	PeakRateMultiplier      *float64 `json:"peak_rate_multiplier"`
	AppliedPeakMultiplier   *float64 `json:"applied_peak_multiplier"`
	EffectiveRateMultiplier *float64 `json:"effective_rate_multiplier"`
	Timezone                *string  `json:"timezone"`
	ObservedAt              string   `json:"observed_at"`
}

// GetUpstreamBillingProbeSettings returns defaults when the setting is absent.
func (s *SettingService) GetUpstreamBillingProbeSettings(ctx context.Context) (*UpstreamBillingProbeSettings, error) {
	defaults := defaultUpstreamBillingProbeSettings()
	if s == nil || s.settingRepo == nil {
		return defaults, nil
	}
	value, err := s.settingRepo.GetValue(ctx, SettingKeyUpstreamBillingProbeSettings)
	if err != nil {
		if errors.Is(err, ErrSettingNotFound) {
			return defaults, nil
		}
		return nil, fmt.Errorf("get upstream billing probe settings: %w", err)
	}
	if strings.TrimSpace(value) == "" {
		return defaults, nil
	}
	settings := *defaults
	if err := json.Unmarshal([]byte(value), &settings); err != nil {
		return nil, fmt.Errorf("parse upstream billing probe settings: %w", err)
	}
	if settings.IntervalMinutes == 0 {
		settings.IntervalMinutes = defaults.IntervalMinutes
	}
	normalizeUpstreamBillingProbeSettings(&settings)
	return &settings, nil
}

// SetUpstreamBillingProbeSettings validates and persists the runner settings.
func (s *SettingService) SetUpstreamBillingProbeSettings(ctx context.Context, settings *UpstreamBillingProbeSettings) error {
	if s == nil || s.settingRepo == nil {
		return fmt.Errorf("setting repository is unavailable")
	}
	if settings == nil {
		return infraerrors.BadRequest("INVALID_UPSTREAM_BILLING_PROBE_SETTINGS", "settings cannot be nil")
	}
	if settings.IntervalMinutes < upstreamBillingProbeMinIntervalMinutes || settings.IntervalMinutes > upstreamBillingProbeMaxIntervalMinutes {
		return infraerrors.BadRequest(
			"INVALID_UPSTREAM_BILLING_PROBE_INTERVAL",
			fmt.Sprintf("interval_minutes must be between %d and %d", upstreamBillingProbeMinIntervalMinutes, upstreamBillingProbeMaxIntervalMinutes),
		)
	}
	normalizeUpstreamBillingProbeSettings(settings)
	data, err := json.Marshal(settings)
	if err != nil {
		return fmt.Errorf("marshal upstream billing probe settings: %w", err)
	}
	return s.settingRepo.Set(ctx, SettingKeyUpstreamBillingProbeSettings, string(data))
}

func defaultUpstreamBillingProbeSettings() *UpstreamBillingProbeSettings {
	return &UpstreamBillingProbeSettings{Enabled: true, IntervalMinutes: upstreamBillingProbeDefaultIntervalMinutes}
}

func normalizeUpstreamBillingProbeSettings(settings *UpstreamBillingProbeSettings) {
	if settings.IntervalMinutes < upstreamBillingProbeMinIntervalMinutes {
		settings.IntervalMinutes = upstreamBillingProbeMinIntervalMinutes
	}
	if settings.IntervalMinutes > upstreamBillingProbeMaxIntervalMinutes {
		settings.IntervalMinutes = upstreamBillingProbeMaxIntervalMinutes
	}
}

// UpstreamBillingProbeService discovers a remote Sub2API billing snapshot.
type UpstreamBillingProbeService struct {
	accountOps         *AccountOpsService
	accountRepo        AccountRepository
	accountTestService *AccountTestService
	settingService     *SettingService

	parentCtx    context.Context
	parentCancel context.CancelFunc
	wg           sync.WaitGroup
	mu           sync.Mutex
	started      bool
	stopped      bool
	cycleMu      sync.Mutex
	probeGroup   singleflight.Group
	probeSlots   chan struct{}
	now          func() time.Time
	lockCache    LeaderLockCache
	db           *sql.DB
	instanceID   string
}

type upstreamBillingProbeSnapshotWriter interface {
	UpdateUpstreamBillingProbeSnapshot(context.Context, *Account, *UpstreamBillingProbeSnapshot, *float64) error
}

type upstreamBillingProbeDueAccountLister interface {
	ListDueUpstreamBillingProbeAccounts(context.Context, time.Time, int) ([]Account, error)
}

func NewUpstreamBillingProbeService(
	accountRepo AccountRepository,
	accountTestService *AccountTestService,
	settingService *SettingService,
) *UpstreamBillingProbeService {
	ctx, cancel := context.WithCancel(context.Background())
	return &UpstreamBillingProbeService{
		accountRepo:        accountRepo,
		accountTestService: accountTestService,
		settingService:     settingService,
		parentCtx:          ctx,
		parentCancel:       cancel,
		probeSlots:         make(chan struct{}, upstreamBillingProbeConcurrency),
		now:                time.Now,
		instanceID:         uuid.NewString(),
	}
}

func (s *UpstreamBillingProbeService) SetLeaderLock(lockCache LeaderLockCache, db *sql.DB) {
	if s == nil {
		return
	}
	s.lockCache = lockCache
	s.db = db
}

// ProvideUpstreamBillingProbeService starts the process-wide periodic runner.
func ProvideUpstreamBillingProbeService(
	accountRepo AccountRepository,
	accountTestService *AccountTestService,
	settingService *SettingService,
	lockCache LeaderLockCache,
	db *sql.DB,
	accountOps *AccountOpsService,
) *UpstreamBillingProbeService {
	svc := NewUpstreamBillingProbeService(accountRepo, accountTestService, settingService)
	svc.SetLeaderLock(lockCache, db)
	svc.SetAccountOpsService(accountOps)
	startBackgroundJob(svc.Start)
	return svc
}

func (s *UpstreamBillingProbeService) Start() {
	if BackgroundJobsDisabled() {
		return
	}
	if s == nil {
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

func (s *UpstreamBillingProbeService) Stop() {
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

func (s *UpstreamBillingProbeService) runLoop() {
	defer s.wg.Done()
	_ = s.RunDue(s.parentCtx)
	ticker := time.NewTicker(upstreamBillingProbeCycleInterval)
	defer ticker.Stop()
	for {
		select {
		case <-s.parentCtx.Done():
			return
		case <-ticker.C:
			if err := s.RunDue(s.parentCtx); err != nil {
				logger.LegacyPrintf("service.upstream_billing_probe", "run_due_failed: err=%v", err)
			}
		}
	}
}

// RunDue executes at most one bounded batch of due accounts.
func (s *UpstreamBillingProbeService) RunDue(ctx context.Context) error {
	if s == nil || s.accountRepo == nil {
		return nil
	}
	s.cycleMu.Lock()
	defer s.cycleMu.Unlock()

	settings, err := s.getSettings(ctx)
	if err != nil {
		return err
	}
	if !settings.Enabled {
		return nil
	}
	runRelease, acquired, lockErr := s.tryAcquireLeaderLock(ctx, upstreamBillingProbeLeaderLockKey)
	if lockErr != nil {
		return fmt.Errorf("acquire upstream billing probe leader lock: %w", lockErr)
	}
	if !acquired {
		return nil
	}
	defer runRelease()

	lockNow := time.Now()
	cadenceRelease, acquired, lockErr := s.tryAcquireLeaderLock(ctx, upstreamBillingProbeLeaderLockKeyAt(lockNow))
	if lockErr != nil {
		return fmt.Errorf("acquire upstream billing probe cadence lock: %w", lockErr)
	}
	if !acquired {
		return nil
	}
	defer releaseUpstreamBillingProbeLeaderLock(cadenceRelease, lockNow.Truncate(upstreamBillingProbeCycleInterval).Add(upstreamBillingProbeCycleInterval))

	now := s.currentTime()
	accounts, err := s.listDueAccounts(ctx, now)
	if err != nil {
		return fmt.Errorf("list enabled upstream billing probes: %w", err)
	}
	due := make([]Account, 0, len(accounts))
	for i := range accounts {
		account := accounts[i]
		if !isUpstreamBillingProbeAccount(&account) || !account.IsActive() || !upstreamBillingProbeScheduled(&account) {
			continue
		}
		snapshot := decodeUpstreamBillingProbeSnapshot(account.Extra)
		if snapshot != nil && !snapshot.NextProbeAt.IsZero() && now.Before(snapshot.NextProbeAt) {
			continue
		}
		due = append(due, account)
	}
	sort.SliceStable(due, func(i, j int) bool {
		left := decodeUpstreamBillingProbeSnapshot(due[i].Extra)
		right := decodeUpstreamBillingProbeSnapshot(due[j].Extra)
		leftUnset := left == nil || left.NextProbeAt.IsZero()
		rightUnset := right == nil || right.NextProbeAt.IsZero()
		if leftUnset && rightUnset {
			return due[i].ID < due[j].ID
		}
		if leftUnset {
			return true
		}
		if rightUnset {
			return false
		}
		return left.NextProbeAt.Before(right.NextProbeAt)
	})
	if len(due) > upstreamBillingProbeMaxPerCycle {
		due = due[:upstreamBillingProbeMaxPerCycle]
	}

	var group errgroup.Group
	for i := range due {
		accountID := due[i].ID
		group.Go(func() error {
			if _, probeErr := s.probeScheduledAccount(ctx, accountID, settings.IntervalMinutes); probeErr != nil {
				logger.LegacyPrintf("service.upstream_billing_probe", "probe_due_failed: account_id=%d err=%v", accountID, probeErr)
			}
			return nil
		})
	}
	return group.Wait()
}

func (s *UpstreamBillingProbeService) listDueAccounts(ctx context.Context, now time.Time) ([]Account, error) {
	if lister, ok := s.accountRepo.(upstreamBillingProbeDueAccountLister); ok {
		return lister.ListDueUpstreamBillingProbeAccounts(ctx, now, upstreamBillingProbeMaxPerCycle)
	}
	// Non-production repositories and older adapters keep the generic path. The
	// runner still truncates before issuing network requests.
	return s.accountRepo.FindByExtraField(ctx, UpstreamBillingProbeEnabledExtraKey, true)
}

func (s *UpstreamBillingProbeService) getSettings(ctx context.Context) (*UpstreamBillingProbeSettings, error) {
	if s.settingService == nil {
		return defaultUpstreamBillingProbeSettings(), nil
	}
	return s.settingService.GetUpstreamBillingProbeSettings(ctx)
}

func (s *UpstreamBillingProbeService) GetSettings(ctx context.Context) (*UpstreamBillingProbeSettings, error) {
	return s.getSettings(ctx)
}

func (s *UpstreamBillingProbeService) UpdateSettings(ctx context.Context, settings *UpstreamBillingProbeSettings) error {
	if s == nil || s.settingService == nil {
		return ErrUpstreamBillingProbeUnavailable
	}
	return s.settingService.SetUpstreamBillingProbeSettings(ctx, settings)
}

// ProbeAccount performs one manual or scheduled probe. Manual calls ignore both switches.
func (s *UpstreamBillingProbeService) ProbeAccount(ctx context.Context, accountID int64) (*UpstreamBillingProbeSnapshot, error) {
	if s == nil || s.accountRepo == nil {
		return nil, ErrUpstreamBillingProbeUnavailable
	}
	settings, err := s.getSettings(ctx)
	if err != nil {
		return nil, err
	}
	return s.probeAccount(ctx, accountID, settings.IntervalMinutes)
}

func (s *UpstreamBillingProbeService) probeAccount(ctx context.Context, accountID int64, intervalMinutes int) (*UpstreamBillingProbeSnapshot, error) {
	return s.probeAccountWithMode(ctx, accountID, intervalMinutes, false)
}

func (s *UpstreamBillingProbeService) probeScheduledAccount(ctx context.Context, accountID int64, intervalMinutes int) (*UpstreamBillingProbeSnapshot, error) {
	// Scheduled rounds are allowed to sleep between balance read attempts.
	// Manual probes (ProbeAccount) run on the admin request path and are not
	// marked, so they never block on a retry.
	return s.probeAccountWithMode(withUpstreamBalanceRetry(ctx), accountID, intervalMinutes, true)
}

func (s *UpstreamBillingProbeService) probeAccountWithMode(ctx context.Context, accountID int64, intervalMinutes int, requireEnabled bool) (*UpstreamBillingProbeSnapshot, error) {
	key := strconv.FormatInt(accountID, 10)
	value, err, _ := s.probeGroup.Do(key, func() (any, error) {
		select {
		case s.probeSlots <- struct{}{}:
			defer func() { <-s.probeSlots }()
		case <-ctx.Done():
			return nil, ctx.Err()
		}
		account, loadErr := s.accountRepo.GetByID(ctx, accountID)
		if loadErr != nil {
			return nil, loadErr
		}
		if !isUpstreamBillingProbeAccount(account) {
			return nil, ErrUpstreamBillingProbeAccountInvalid
		}
		if requireEnabled {
			if !account.IsActive() || !upstreamBillingProbeScheduled(account) {
				return nil, nil
			}
			if snapshot := decodeUpstreamBillingProbeSnapshot(account.Extra); snapshot != nil &&
				!snapshot.NextProbeAt.IsZero() && s.currentTime().Before(snapshot.NextProbeAt) {
				return nil, nil
			}
		}
		return s.probeLoadedAccount(ctx, account, intervalMinutes)
	})
	if err != nil {
		return nil, err
	}
	if value == nil {
		return nil, nil
	}
	snapshot, ok := value.(*UpstreamBillingProbeSnapshot)
	if !ok {
		return nil, fmt.Errorf("invalid upstream billing probe result")
	}
	return snapshot, nil
}

// ProbeAccounts performs a bounded manual batch with the same concurrency limit as the runner.
func (s *UpstreamBillingProbeService) ProbeAccounts(ctx context.Context, accountIDs []int64) []UpstreamBillingProbeResult {
	if len(accountIDs) > upstreamBillingProbeMaxPerCycle {
		accountIDs = accountIDs[:upstreamBillingProbeMaxPerCycle]
	}
	results := make([]UpstreamBillingProbeResult, len(accountIDs))
	if s == nil || s.accountRepo == nil {
		for i, accountID := range accountIDs {
			results[i] = UpstreamBillingProbeResult{AccountID: accountID, Error: ErrUpstreamBillingProbeUnavailable.Error()}
		}
		return results
	}
	settings, settingsErr := s.getSettings(ctx)
	if settingsErr != nil {
		for i, accountID := range accountIDs {
			results[i] = UpstreamBillingProbeResult{AccountID: accountID, Error: safeProbeError(settingsErr)}
		}
		return results
	}
	var group errgroup.Group
	for i, accountID := range accountIDs {
		i, accountID := i, accountID
		results[i].AccountID = accountID
		group.Go(func() error {
			snapshot, err := s.probeAccount(ctx, accountID, settings.IntervalMinutes)
			if err != nil {
				results[i].Error = safeProbeError(err)
				return nil
			}
			results[i].Snapshot = snapshot
			return nil
		})
	}
	_ = group.Wait()
	return results
}

func upstreamBillingProbeLeaderLockKeyAt(now time.Time) string {
	return fmt.Sprintf("%s:%d", upstreamBillingProbeLeaderLockKey, now.Unix()/int64(upstreamBillingProbeCycleInterval/time.Second))
}

func (s *UpstreamBillingProbeService) tryAcquireLeaderLock(ctx context.Context, key string) (func(), bool, error) {
	lockCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	if s.lockCache != nil {
		acquired, err := s.lockCache.TryAcquireLeaderLock(lockCtx, key, s.instanceID, upstreamBillingProbeLeaderLockTTL)
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

func releaseUpstreamBillingProbeLeaderLock(release func(), releaseAt time.Time) {
	delay := time.Until(releaseAt)
	if delay <= 0 {
		release()
		return
	}
	time.AfterFunc(delay, release)
}

func (s *UpstreamBillingProbeService) SetAccountEnabled(ctx context.Context, accountID int64, enabled bool) error {
	if s == nil || s.accountRepo == nil {
		return ErrUpstreamBillingProbeUnavailable
	}
	account, err := s.accountRepo.GetByID(ctx, accountID)
	if err != nil {
		return err
	}
	if !isUpstreamBillingProbeAccount(account) {
		return ErrUpstreamBillingProbeAccountInvalid
	}
	updates := map[string]any{UpstreamBillingProbeEnabledExtraKey: enabled}
	if !enabled {
		updates[UpstreamBillingRateSyncEnabledExtraKey] = false
	}
	return s.accountRepo.UpdateExtra(ctx, accountID, updates)
}

func (s *UpstreamBillingProbeService) probeLoadedAccount(ctx context.Context, account *Account, intervalMinutes int) (*UpstreamBillingProbeSnapshot, error) {
	now := s.currentTime().UTC()
	if s.accountTestService == nil || s.accountTestService.httpUpstream == nil {
		return s.persistProbeFailure(ctx, account, intervalMinutes, now, 0, "transport_unavailable", 0)
	}
	// 平台放宽后取数直读 credentials：所有 API-key 平台的密钥与自定义上游
	// 统一存放在 credentials.api_key / credentials.base_url。
	apiKey := account.GetCredential("api_key")
	if apiKey == "" {
		return s.persistProbeFailure(ctx, account, intervalMinutes, now, 0, "missing_api_key", 0)
	}
	baseURL := account.GetCredential("base_url")
	if account.IsCNProvider() && account.IsAdaptiveAPIProtocol() {
		baseURL = account.GetCNProtocolBaseURL(APIProtocolChatCompletions)
	}
	if account.Platform == PlatformOpenAI {
		if baseURL == "" {
			// 保持官方语义：OpenAI 账号无自定义 base 时探官方域（404 → unsupported）。
			baseURL = "https://api.openai.com"
		}
	} else if upstreamBillingProbeTargetIsOfficialAPI(baseURL) {
		// 其他平台 base_url 为空或指向官方 API 根域（前端创建时会把空值
		// 填成官方默认域，且提供 us-east-1.api.x.ai 等官方区域预设）⇒
		// 必无 /v1/sub2api/billing；不发请求，直接记 unsupported，避免
		// 拿账号 Key 周期性请求官方域的不存在路径。
		snapshot := upstreamBillingProbeFailureSnapshot(account, intervalMinutes, now, 0, "unsupported", 0)
		snapshot.UpstreamKind = UpstreamKindOfficial
		if err := s.updateSnapshot(ctx, account, snapshot, nil); err != nil {
			return nil, err
		}
		return snapshot, nil
	}
	normalizedBaseURL, err := s.accountTestService.validateUpstreamBaseURL(baseURL)
	if err != nil {
		return s.persistProbeFailure(ctx, account, intervalMinutes, now, 0, "invalid_base_url", 0)
	}
	proxyURL := ""
	if account.ProxyID != nil {
		if account.Proxy == nil {
			return s.persistProbeFailure(ctx, account, intervalMinutes, now, 0, "proxy_unavailable", 0)
		}
		if account.Proxy.ID != *account.ProxyID {
			return nil, ErrUpstreamBillingProbeIdentityChanged
		}
		proxyURL = account.Proxy.URL()
	}
	target := upstreamBillingProbeTarget{apiKey: apiKey, proxyURL: proxyURL}
	rateResult := s.fetchUpstreamProbeBody(ctx, account, target,
		buildOpenAIEndpointURL(normalizedBaseURL, "/v1/sub2api/billing"), upstreamBillingProbeMaxBodyBytes)
	previousRate := account.BillingRateMultiplier()
	snapshot, syncRate := upstreamBillingRateSnapshotFromResult(account, intervalMinutes, now, rateResult, previousRate)
	previous := decodeUpstreamBillingProbeSnapshot(account.Extra)
	if previous != nil {
		snapshot.UpstreamKind = normalizeUpstreamKind(previous.UpstreamKind)
	}
	if snapshot.Status == UpstreamBillingProbeStatusOK {
		snapshot.UpstreamKind = UpstreamKindSub2API
	}
	if upstreamBalanceProbeAllowed(normalizedBaseURL, rateResult) {
		s.identifyUpstreamAndReadBalance(ctx, account, target, normalizedBaseURL, intervalMinutes, now, rateResult, snapshot)
	}
	if err := s.updateSnapshot(ctx, account, snapshot, syncRate); err != nil {
		return nil, err
	}
	if syncRate != nil && previousRate != *syncRate {
		// 写回是后台任务的裸 SQL，不经过管理端路由，因此不会产生 audit_logs 行。
		// old_rate_multiplier 是本次探测开始时读到的值（写回的 CAS 不比对该列）。
		slog.Info("upstream_billing_rate_sync_applied",
			"source", "upstream_billing_probe",
			"account_id", account.ID,
			"old_rate_multiplier", previousRate,
			"new_rate_multiplier", *syncRate,
		)
	}
	return snapshot, nil
}

// identifyUpstreamAndReadBalance works out which software the upstream runs
// and reads its balance, updating snapshot.UpstreamKind and snapshot.Balance.
//
// Endpoints are tried in a fixed order and the first whose own schema parses
// wins; a 200 alone proves nothing, because relays commonly answer unknown
// paths with an HTML page or a generic JSON envelope.
//
//  1. /v1/usage                           -> sub2api
//  2. /api/usage/token/                   -> new-api
//  3. new-api fingerprints                -> new-api
//  4. /v1/dashboard/billing/subscription  -> OpenAI-compatible
//
// Step 3 exists because new-api answers the legacy OpenAI billing endpoints on
// purpose, for clients that only know them. "Billing answered" therefore
// cannot mean "OpenAI-billing relay" while a new-api fingerprint is present,
// and the fingerprints outrank the payload: x-new-api-version and
// x-oneapi-request-id response headers, the {"type":"new_api_error"} error
// envelope, and the /api/status document new-api and one-api share. Only when
// none of them holds does step 4 decide.
//
// A stored UpstreamKindOpenAICompatible verdict is not a cache: it is
// re-identified from the top on every probe, so an account classified before
// the fingerprints existed is corrected on its next probe instead of staying
// wrong forever.
//
// Once another kind is known, later probes go straight to that kind's endpoint,
// so a recognised upstream costs one balance request per cycle and only an
// unrecognised one is asked about every path. A recognised upstream whose
// endpoint stops parsing is re-identified from the top.
//
// The sequence stops as soon as the upstream rejects the key or asks for
// back-off (401/403/429): repeating a rejected key against more paths is how a
// relay ends up rate-limiting or banning the caller's IP.
func (s *UpstreamBillingProbeService) identifyUpstreamAndReadBalance(
	ctx context.Context,
	account *Account,
	target upstreamBillingProbeTarget,
	baseURL string,
	intervalMinutes int,
	now time.Time,
	rateResult upstreamBillingProbeHTTPResult,
	snapshot *UpstreamBillingProbeSnapshot,
) {
	previousBalance := snapshot.Balance
	// Status of the first balance endpoint asked, kept for the "custom" verdict.
	lastStatusCode := 0
	fresh := func(data map[string]any, source string, statusCode int) *UpstreamBalanceSnapshot {
		return &UpstreamBalanceSnapshot{
			Status:        UpstreamBillingProbeStatusOK,
			Data:          data,
			ReceivedAt:    probeTimePtr(now),
			FreshUntil:    probeTimePtr(now.Add(2 * time.Duration(intervalMinutes) * time.Minute)),
			LastAttemptAt: now,
			HTTPStatus:    statusCode,
			Source:        source,
		}
	}
	fetch := func(path string, maxBytes int) upstreamBillingProbeHTTPResult {
		return s.fetchUpstreamProbeBody(ctx, account, target, buildOpenAIEndpointURL(baseURL, path), maxBytes)
	}
	// readable keeps a recognised upstream on the normal interval. The stretched
	// "unsupported" delay is for upstreams that return nothing useful; applying
	// it to one with a readable balance would leave that balance stale.
	readable := func() {
		// Reading a balance is proof the upstream answers balance queries, so the
		// snapshot must not stay "unsupported". Status comes from
		// /v1/sub2api/billing, which every new-api site answers with 404: without
		// this the account page showed "unsupported" and hid a balance that had
		// just been read successfully from /api/usage/token/.
		if snapshot.Status != UpstreamBillingProbeStatusOK {
			snapshot.Status = UpstreamBillingProbeStatusOK
			snapshot.LastError = ""
			snapshot.FailureCount = 0
		}
		snapshot.NextProbeAt = now.Add(nextProbeDelay(intervalMinutes, retryAfter(rateResult.header, now)))
	}

	// newAPISignal is the first new-api fingerprint this probe saw. It outranks
	// a billing answer: new-api serves the legacy OpenAI billing endpoints for
	// compatibility, so "billing answered" cannot mean "OpenAI-billing relay"
	// once the upstream proved it is new-api.
	newAPISignal := ""
	noteResponse := func(result upstreamBillingProbeHTTPResult) {
		if newAPISignal != "" {
			return
		}
		if signal, ok := upstreamNewAPIHeaderEvidence(result.header); ok {
			newAPISignal = signal
			return
		}
		if signal, ok := upstreamNewAPIErrorEvidence(result.body); ok {
			newAPISignal = signal
		}
	}
	// The rate probe is evidence too: a new-api site answers /v1/sub2api/billing
	// with the same version header as every other path, so the fingerprint is
	// usually already known before the balance paths are asked.
	noteResponse(rateResult)

	// newAPIUsageResult caches /api/usage/token/ so fingerprinting and the
	// balance read share one request: an evidenced upstream that still serves
	// the usage payload shows a real balance in the same probe.
	var newAPIUsageResult upstreamBillingProbeHTTPResult
	newAPIUsageFetched := false
	fetchNewAPIUsage := func() upstreamBillingProbeHTTPResult {
		if newAPIUsageFetched {
			return newAPIUsageResult
		}
		newAPIUsageFetched = true
		// new-api answers the trailing-slash form with a redirect on some
		// deployments (the production account #34760 answered 302). Follows
		// same-origin hops only; a login page stays "unavailable".
		newAPIUsageResult = s.fetchUpstreamProbeBodyFollowingRedirects(ctx, account, target,
			upstreamSiteRootURL(baseURL, upstreamNewAPIUsagePath), upstreamKindProbeMaxBodyBytes)
		noteResponse(newAPIUsageResult)
		return newAPIUsageResult
	}

	// adoptNewAPIFromEvidence records new-api from a fingerprint rather than
	// from the usage payload. The balance still has to come from
	// /api/usage/token/: borrowing the billing numbers is what used to render a
	// new-api site as "unlimited" with a five-figure spend.
	//
	// When that endpoint cannot answer, the operator-configured extractor is
	// consulted next (the same source the account page's custom usage column
	// uses). Only when both are unavailable does the balance become "not
	// provided": the legacy billing endpoint's spend is real and is kept as
	// "used", but its 1e8 limit is a marker and is never shown as a limit.
	adoptNewAPIFromEvidence := func(legacyUsageBody []byte) {
		usage := fetchNewAPIUsage()
		snapshot.UpstreamKind = UpstreamKindNewAPI
		if upstreamProbeAnswered(usage) {
			if data, err := parseUpstreamNewAPIUsageResponse(usage.body); err == nil {
				snapshot.Balance = fresh(data, UpstreamBalanceSourceNewAPITokenUsage, usage.statusCode)
				readable()
				return
			}
		}
		if upstreamProbeRejectedKey(usage) {
			snapshot.Balance = upstreamBalanceSnapshotFromResult(usage, previousBalance, intervalMinutes, now)
			return
		}
		if custom, ok := s.customUsageBalanceSnapshot(ctx, account, intervalMinutes, now); ok {
			snapshot.Balance = custom
			readable()
			return
		}
		// Identified, but no balance endpoint answered with its schema. Show the
		// spend the upstream did report instead of a fabricated limit, and keep
		// probing on the ten-minute cadence rather than the old multi-hour delay.
		data := map[string]any{"is_valid": true, "unit": "USD", "balance_unavailable": true}
		if used, ok := upstreamLegacyBillingUsedUSD(legacyUsageBody); ok {
			data["quota_used"] = used
		}
		snapshot.Balance = &UpstreamBalanceSnapshot{
			Status:        UpstreamBillingProbeStatusUnsupported,
			Data:          data,
			LastAttemptAt: now,
			HTTPStatus:    usage.statusCode,
			LastError:     "unsupported",
		}
		snapshot.NextProbeAt = now.Add(unsupportedProbeDelay(retryAfter(usage.header, now)))
	}

	// checkNewAPIStatus is the last fingerprint look before a billing payload
	// decides. /api/status needs no key, so it cannot cost a rejected-key
	// penalty, and it is only asked when no cheaper signal answered.
	checkNewAPIStatus := func() bool {
		if newAPISignal != "" {
			return true
		}
		result := s.fetchUpstreamProbeBody(ctx, account, target,
			upstreamSiteRootURL(baseURL, upstreamNewAPIStatusPath), upstreamKindProbeMaxBodyBytes)
		noteResponse(result)
		if newAPISignal == "" && upstreamProbeAnswered(result) {
			if signal, ok := upstreamNewAPIStatusEvidence(result.body); ok {
				newAPISignal = signal
			}
		}
		return newAPISignal != ""
	}

	// readRecognisedBalance reads the balance of an upstream that was already
	// identified as Sub2API / New API. Those two never report a read failure to
	// the account list: a background round retries the read (1s/3s/9s), the last
	// successful value stays on screen, and the next scheduled round keeps
	// retrying until the upstream answers.
	readRecognisedBalance := func(kind, source string, fetch func() upstreamBillingProbeHTTPResult, parse func([]byte) (map[string]any, error)) {
		attempts := 1
		if upstreamBalanceRetryAllowed(ctx) {
			attempts = upstreamBalanceRetryAttempts
		}
		result, data, ok := upstreamBalanceRead(ctx, attempts, fetch, parse)
		if ok {
			snapshot.UpstreamKind = kind
			snapshot.Balance = fresh(data, source, result.statusCode)
			readable()
			return
		}
		snapshot.UpstreamKind = kind
		reason := upstreamBalanceReadReason(result)
		snapshot.Balance = upstreamBalanceRetryPendingSnapshot(previousBalance, now, result.statusCode, reason)
		if (reason == "unsupported" || reason == "invalid_response") &&
			(previousBalance == nil || previousBalance.Status != UpstreamBillingProbeStatusOK) {
			// There is no earlier successful balance to keep and the endpoint is
			// not merely flaky: report "not provided" now (the spend carried in
			// the data stays visible) instead of a read that never ends.
			snapshot.Balance.Status = UpstreamBillingProbeStatusUnsupported
		}
		switch reason {
		case "unsupported", "invalid_response":
			// The endpoint no longer serves its own schema: the software behind
			// the domain may have changed. Drop the kind so the next round
			// identifies the upstream again (the failure is still not shown).
			snapshot.UpstreamKind = ""
		}
		// A missing or unparseable endpoint is not a transient read failure, so
		// it retries on the ten-minute cadence instead of the 1s/3s/9s recovery
		// loop. That loop is for transport errors and rejected keys.
		delay := upstreamBalanceRetryNextDelay(previousBalance, upstreamProbeRejectedKey(result))
		switch reason {
		case "unsupported", "invalid_response":
			delay = unsupportedProbeDelay(retryAfter(result.header, now))
		}
		if retryAt := now.Add(delay); snapshot.NextProbeAt.IsZero() || retryAt.Before(snapshot.NextProbeAt) {
			snapshot.NextProbeAt = retryAt
		}
	}
	trySub2API := func() (done bool) {
		fetchBalance := func() upstreamBillingProbeHTTPResult {
			return s.fetchUpstreamProbeBody(ctx, account, target, upstreamBalanceProbeURL(baseURL, now), upstreamBalanceProbeMaxBodyBytes)
		}
		if snapshot.UpstreamKind == UpstreamKindSub2API {
			readRecognisedBalance(UpstreamKindSub2API, UpstreamBalanceSourceSub2APIUsage, fetchBalance, parseUpstreamBalanceResponse)
			return true
		}
		result := fetchBalance()
		noteResponse(result)
		lastStatusCode = result.statusCode
		if upstreamProbeAnswered(result) {
			if data, err := parseUpstreamBalanceResponse(result.body); err == nil {
				snapshot.UpstreamKind = UpstreamKindSub2API
				snapshot.Balance = fresh(data, UpstreamBalanceSourceSub2APIUsage, result.statusCode)
				readable()
				return true
			}
		}
		if upstreamProbeRejectedKey(result) {
			// The key was rejected before the upstream was identified: record this
			// attempt and stop asking more paths, as before.
			snapshot.Balance = upstreamBalanceSnapshotFromResult(result, previousBalance, intervalMinutes, now)
			return true
		}
		return false
	}
	tryNewAPI := func() (done bool) {
		// Site root, not the model base path: see upstreamSiteRootURL.
		fetchBalance := fetchNewAPIUsage
		if snapshot.UpstreamKind == UpstreamKindNewAPI {
			readRecognisedBalance(UpstreamKindNewAPI, UpstreamBalanceSourceNewAPITokenUsage, fetchBalance, parseUpstreamNewAPIUsageResponse)
			return true
		}
		result := fetchBalance()
		if upstreamProbeAnswered(result) {
			if data, err := parseUpstreamNewAPIUsageResponse(result.body); err == nil {
				snapshot.UpstreamKind = UpstreamKindNewAPI
				snapshot.Balance = fresh(data, UpstreamBalanceSourceNewAPITokenUsage, result.statusCode)
				readable()
				return true
			}
		}
		if upstreamProbeRejectedKey(result) {
			snapshot.Balance = upstreamBalanceSnapshotFromResult(result, previousBalance, intervalMinutes, now)
			return true
		}
		return false
	}
	tryLegacy := func() (done bool) {
		subscription := fetch(upstreamLegacySubscriptionPath, upstreamKindProbeMaxBodyBytes)
		noteResponse(subscription)
		if upstreamProbeRejectedKey(subscription) {
			// The key was rejected: record the attempt and stop asking. A kind
			// is not inferred from an answer the upstream refused to give.
			snapshot.Balance = upstreamBalanceSnapshotFromResult(subscription, previousBalance, intervalMinutes, now)
			return true
		}
		// new-api's 1e8 unlimited sentinel is a fingerprint in its own right: it
		// identifies the software even when /api/status is 404 and the responses
		// carry no new-api header (production account #34747). The payload is not
		// an OpenAI balance, so it must outrank the billing fallback.
		if newAPISignal == "" && upstreamProbeAnswered(subscription) && upstreamLegacyBillingIsNewAPISentinel(subscription.body) {
			newAPISignal = "billing:newapi_unlimited_sentinel"
		}
		// new-api answers these billing endpoints for compatibility. Its
		// fingerprint outranks the payload: adopt New API instead of reading a
		// billing snapshot that was never OpenAI's. The status document is the
		// last signal consulted, and only when no cheaper one matched.
		if newAPISignal != "" || checkNewAPIStatus() {
			// The legacy usage endpoint reports the real spend in cents, which
			// stays meaningful even though the subscription limit does not.
			var legacyUsageBody []byte
			if upstreamProbeAnswered(subscription) {
				if usage := fetch(upstreamLegacyUsagePath, upstreamKindProbeMaxBodyBytes); upstreamProbeAnswered(usage) {
					legacyUsageBody = usage.body
				}
			}
			adoptNewAPIFromEvidence(legacyUsageBody)
			return true
		}
		if upstreamProbeAnswered(subscription) {
			// hard_limit_usd is the granted total, so the usage endpoint is needed
			// for what is left. It is only requested once the subscription parsed.
			if _, err := parseUpstreamLegacySubscriptionResponse(subscription.body, nil); err == nil {
				var usageBody []byte
				if usage := fetch(upstreamLegacyUsagePath, upstreamKindProbeMaxBodyBytes); upstreamProbeAnswered(usage) {
					usageBody = usage.body
				}
				if data, err := parseUpstreamLegacySubscriptionResponse(subscription.body, usageBody); err == nil {
					snapshot.UpstreamKind = UpstreamKindOpenAICompatible
					snapshot.Balance = fresh(data, UpstreamBalanceSourceLegacySubscription, subscription.statusCode)
					readable()
					return true
				}
			}
		}
		return false
	}

	// A recognised upstream is asked only about its own endpoint. If that
	// endpoint no longer parses, the kind is dropped and identification restarts
	// on the next probe rather than in this one, to keep one probe bounded.
	//
	// UpstreamKindOpenAICompatible is deliberately absent: that verdict was
	// reachable while a billing answer alone decided, so it is re-identified
	// from the top on every probe. A misclassified account is corrected on its
	// next probe instead of being trusted because a kind is already stored.
	switch snapshot.UpstreamKind {
	case UpstreamKindSub2API:
		trySub2API()
		if snapshot.Balance != nil && snapshot.Balance.Status == UpstreamBillingProbeStatusPending && snapshot.UpstreamKind != "" {
			// Identified upstream whose read is still being retried: keep the kind
			// so the next round asks the same endpoint instead of re-identifying.
			return
		}
		if snapshot.Balance == nil || snapshot.Balance.Status != UpstreamBillingProbeStatusOK {
			if snapshot.UpstreamKind != "" && snapshot.Status != UpstreamBillingProbeStatusOK && (snapshot.Balance == nil || snapshot.Balance.LastError == "unsupported" || snapshot.Balance.LastError == "invalid_response") {
				snapshot.UpstreamKind = ""
			}
		}
		return
	case UpstreamKindNewAPI:
		tryNewAPI()
		if snapshot.Balance != nil && snapshot.Balance.Status == UpstreamBillingProbeStatusPending {
			return
		}
		if snapshot.Balance == nil || (snapshot.Balance.Status != UpstreamBillingProbeStatusOK &&
			(snapshot.Balance.LastError == "unsupported" || snapshot.Balance.LastError == "invalid_response")) {
			snapshot.UpstreamKind = ""
		}
		return
	}

	if trySub2API() || tryNewAPI() {
		return
	}
	if newAPISignal != "" {
		// A fingerprint proved new-api before the billing endpoints were even
		// asked: read the balance from new-api, never from the billing payload.
		// No subscription was fetched on this path, so there is no spend to keep.
		adoptNewAPIFromEvidence(nil)
		return
	}
	if tryLegacy() {
		return
	}
	// Reached the upstream, the key was not rejected, and no known endpoint
	// answered with its schema. HTTPStatus is what the first balance endpoint
	// answered, which is what an operator looking at "unsupported" wants to see.
	snapshot.UpstreamKind = UpstreamKindCustom
	snapshot.Balance = &UpstreamBalanceSnapshot{
		Status:        UpstreamBillingProbeStatusUnsupported,
		LastAttemptAt: now,
		HTTPStatus:    lastStatusCode,
		LastError:     "unsupported",
	}
}

// upstreamBillingProbeTarget is the credential and route one probe uses for
// both of its upstream requests.
type upstreamBillingProbeTarget struct {
	apiKey   string
	proxyURL string
}

// upstreamBillingProbeHTTPResult is one upstream GET. failure is set when no
// complete body was read; statusCode and header are kept whenever the
// upstream answered at all.
type upstreamBillingProbeHTTPResult struct {
	statusCode int
	header     http.Header
	body       []byte
	failure    string
}

func (s *UpstreamBillingProbeService) fetchUpstreamProbeBody(
	ctx context.Context,
	account *Account,
	target upstreamBillingProbeTarget,
	requestURL string,
	maxBodyBytes int,
) upstreamBillingProbeHTTPResult {
	probeCtx, cancel := context.WithTimeout(ctx, upstreamBillingProbeRequestTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(probeCtx, http.MethodGet, requestURL, bytes.NewReader(nil))
	if err != nil {
		return upstreamBillingProbeHTTPResult{failure: "request_build_failed"}
	}
	// OpenAI 账号保持官方 openai 传输画像；其他平台探测走默认画像。
	profile := HTTPUpstreamProfileDefault
	if account.Platform == PlatformOpenAI {
		profile = HTTPUpstreamProfileOpenAI
	}
	reqCtx := WithHTTPUpstreamProfile(req.Context(), profile)
	req = req.WithContext(WithHTTPUpstreamRedirectsDisabled(reqCtx))
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Authorization", "Bearer "+target.apiKey)
	account.ApplyHeaderOverrides(req.Header)
	var tlsProfile *tlsfingerprint.Profile
	if s.accountTestService.tlsFPProfileService != nil {
		tlsProfile = s.accountTestService.tlsFPProfileService.ResolveTLSProfile(account)
	}
	resp, err := s.accountTestService.httpUpstream.DoWithTLS(req, target.proxyURL, account.ID, account.Concurrency, tlsProfile)
	if err != nil {
		return upstreamBillingProbeHTTPResult{failure: "request_failed"}
	}
	if resp == nil || resp.Body == nil {
		return upstreamBillingProbeHTTPResult{failure: "empty_response"}
	}
	defer func() { _ = resp.Body.Close() }()
	result := upstreamBillingProbeHTTPResult{statusCode: resp.StatusCode, header: resp.Header}
	body, readErr := io.ReadAll(io.LimitReader(resp.Body, int64(maxBodyBytes)+1))
	if readErr != nil {
		result.failure = "response_read_failed"
		return result
	}
	if len(body) > maxBodyBytes {
		result.failure = "response_too_large"
		return result
	}
	result.body = body
	return result
}

// fetchUpstreamProbeBodyFollowingRedirects asks one probe URL and follows a
// bounded number of same-origin redirects. It exists for new-api's
// /api/usage/token/ endpoint, which some deployments answer with a 302 instead
// of the document (production account #34760).
//
// Only same-origin hops are followed, so the account key is never carried to a
// different host, and a redirect that lands on a login/sign-in page is treated
// as "not available" rather than followed. Anything else keeps the redirect
// response, which the callers already read as "no schema answered".
func (s *UpstreamBillingProbeService) fetchUpstreamProbeBodyFollowingRedirects(
	ctx context.Context,
	account *Account,
	target upstreamBillingProbeTarget,
	requestURL string,
	maxBodyBytes int,
) upstreamBillingProbeHTTPResult {
	current, err := url.Parse(requestURL)
	if err != nil {
		return s.fetchUpstreamProbeBody(ctx, account, target, requestURL, maxBodyBytes)
	}
	var result upstreamBillingProbeHTTPResult
	for hop := 0; hop <= upstreamProbeMaxRedirects; hop++ {
		result = s.fetchUpstreamProbeBody(ctx, account, target, current.String(), maxBodyBytes)
		next, ok := upstreamProbeRedirectTarget(current, result)
		if !ok {
			return result
		}
		current = next
	}
	return result
}

// upstreamProbeRedirectTarget resolves one redirect hop. It refuses cross-origin
// targets and login pages, because neither can answer with a key-scoped balance
// document and the first would leak the credential-bearing request.
func upstreamProbeRedirectTarget(current *url.URL, result upstreamBillingProbeHTTPResult) (*url.URL, bool) {
	if current == nil || result.failure != "" || result.statusCode < 300 || result.statusCode >= 400 {
		return nil, false
	}
	location := strings.TrimSpace(result.header.Get("Location"))
	if location == "" {
		return nil, false
	}
	next, err := current.Parse(location)
	if err != nil || next.Host == "" {
		return nil, false
	}
	if !strings.EqualFold(next.Scheme, current.Scheme) || !strings.EqualFold(next.Host, current.Host) {
		return nil, false
	}
	path := strings.ToLower(next.Path)
	if strings.Contains(path, "login") || strings.Contains(path, "signin") || strings.Contains(path, "sign-in") || strings.Contains(path, "sign_in") {
		return nil, false
	}
	return next, true
}

// customUsageBalanceSnapshot reads the operator-configured balance extractor
// when the probe's own new-api endpoint could not answer. It is the same source
// the account page's custom usage column reads, and it is only consulted after
// /api/usage/token/ failed, so a configured extractor never overrides a balance
// the upstream reported through its own schema.
func (s *UpstreamBillingProbeService) customUsageBalanceSnapshot(
	ctx context.Context,
	account *Account,
	intervalMinutes int,
	now time.Time,
) (*UpstreamBalanceSnapshot, bool) {
	if s == nil || s.accountOps == nil || account == nil || !opsConfiguredCustomUsage(account) {
		return nil, false
	}
	if !customUsageExplicitlyConfigured(account) {
		// The default prefill points at /v1/usage, which the probe already asked
		// as the Sub2API endpoint. Only an operator-saved extractor can point at
		// a different source, so only that one is worth a second request.
		return nil, false
	}
	if !upstreamBalanceRetryAllowed(ctx) {
		// A manual probe runs on the admin request path: record the state now and
		// let the next scheduled round fill the extractor balance. Waiting for a
		// second upstream here would block the request for its whole timeout.
		return nil, false
	}
	usage := s.accountOps.CustomUsageService()
	if usage == nil {
		return nil, false
	}
	result, err := usage.Query(ctx, account.ID, false, nil)
	if err != nil || !result.Configured || !result.Enabled || result.Error != "" {
		return nil, false
	}
	data, ok := customUsageBalanceData(result)
	if !ok {
		return nil, false
	}
	received := now
	if result.UpdatedAt != nil && !result.UpdatedAt.IsZero() {
		received = result.UpdatedAt.UTC()
	}
	return &UpstreamBalanceSnapshot{
		Status:        UpstreamBillingProbeStatusOK,
		Data:          data,
		ReceivedAt:    probeTimePtr(received),
		FreshUntil:    probeTimePtr(received.Add(2 * time.Duration(intervalMinutes) * time.Minute)),
		LastAttemptAt: now,
		Source:        UpstreamBalanceSourceCustomUsage,
	}, true
}

// customUsageBalanceData maps an extractor result into the normalised balance
// fields. Remaining is the only field that can be the headline; used/total and
// the unit are carried along for the details panel without inventing a value.
func customUsageBalanceData(result CustomUsageResult) (map[string]any, bool) {
	data := map[string]any{"is_valid": true}
	usable := false
	if remaining, ok := finiteUpstreamBalanceNumber(result.Remaining); ok {
		data["remaining"] = remaining
		usable = true
	}
	if used, ok := finiteUpstreamBalanceNumber(result.Used); ok {
		data["quota_used"] = used
	}
	if total, ok := finiteUpstreamBalanceNumber(result.Total); ok {
		data["quota_limit"] = total
	}
	if unit := strings.TrimSpace(result.Unit); unit != "" {
		data["unit"] = unit
	}
	if plan := strings.TrimSpace(result.PlanName); plan != "" {
		data["plan_name"] = plan
	}
	return data, usable
}

// upstreamBillingRateSnapshotFromResult turns one /v1/sub2api/billing response
// into the snapshot to store, plus the rate to write back when the account
// opted into rate sync. The previous balance is carried over; the caller
// replaces it when the same probe also reads the balance.
func upstreamBillingRateSnapshotFromResult(
	account *Account,
	intervalMinutes int,
	now time.Time,
	result upstreamBillingProbeHTTPResult,
	previousRate float64,
) (*UpstreamBillingProbeSnapshot, *float64) {
	reason := result.failure
	if reason == "" {
		switch {
		case result.statusCode == http.StatusNotFound || result.statusCode == http.StatusMethodNotAllowed:
			reason = "unsupported"
		case result.statusCode < 200 || result.statusCode >= 300:
			reason = "http_error"
		}
	}
	var data map[string]any
	if reason == "" {
		var err error
		if data, err = parseUpstreamBillingProbeResponse(result.body); err != nil {
			reason = "invalid_response"
		}
	}
	if reason != "" {
		return upstreamBillingProbeFailureSnapshot(account, intervalMinutes, now, result.statusCode, reason, retryAfter(result.header, now)), nil
	}
	snapshot := &UpstreamBillingProbeSnapshot{
		Status:        UpstreamBillingProbeStatusOK,
		Data:          data,
		ReceivedAt:    probeTimePtr(now),
		FreshUntil:    probeTimePtr(now.Add(2 * time.Duration(intervalMinutes) * time.Minute)),
		LastAttemptAt: now,
		NextProbeAt:   now.Add(nextProbeDelay(intervalMinutes, 0)),
		HTTPStatus:    result.statusCode,
	}
	if previous := decodeUpstreamBillingProbeSnapshot(account.Extra); previous != nil {
		snapshot.Balance = previous.Balance
	}
	// 账号级值域与精度只在真要写回时才有影响：只观察上游声明、未开启同步的
	// 账号不因声明值不适配 accounts.rate_multiplier 而被记成探测失败并进入
	// 指数退避——探测本身成功了，原始声明照常存进快照供展示。
	var syncRate *float64
	if upstreamBillingRateSyncEnabled(account) {
		if value, valid := upstreamBillingProbeSyncRate(data); valid {
			syncRate = &value
			snapshot.SyncedRateMultiplier = &value
		} else {
			declared, _ := resolveAccountExtraNumber(data, "resolved_rate_multiplier")
			slog.Warn("upstream_billing_rate_sync_rejected",
				"source", "upstream_billing_probe",
				"account_id", account.ID,
				"declared_resolved_rate_multiplier", declared,
				"max_rate_multiplier", upstreamBillingRateSyncMaxMultiplier,
				"current_rate_multiplier", previousRate,
			)
		}
	}
	return snapshot, syncRate
}

func (s *UpstreamBillingProbeService) persistProbeFailure(
	ctx context.Context,
	account *Account,
	intervalMinutes int,
	now time.Time,
	statusCode int,
	reason string,
	retryAfterDuration time.Duration,
) (*UpstreamBillingProbeSnapshot, error) {
	snapshot := upstreamBillingProbeFailureSnapshot(account, intervalMinutes, now, statusCode, reason, retryAfterDuration)
	if err := s.updateSnapshot(ctx, account, snapshot, nil); err != nil {
		return nil, err
	}
	return snapshot, nil
}

func upstreamBillingProbeFailureSnapshot(
	account *Account,
	intervalMinutes int,
	now time.Time,
	statusCode int,
	reason string,
	retryAfterDuration time.Duration,
) *UpstreamBillingProbeSnapshot {
	previous := decodeUpstreamBillingProbeSnapshot(account.Extra)
	failureCount := 1
	if previous != nil {
		failureCount = previous.FailureCount + 1
	}
	status := UpstreamBillingProbeStatusFailed
	delay := nextProbeDelay(intervalMinutes, retryAfterDuration)
	if reason == "unsupported" {
		status = UpstreamBillingProbeStatusUnsupported
		delay = unsupportedProbeDelay(retryAfterDuration)
	}
	snapshot := &UpstreamBillingProbeSnapshot{
		Status:        status,
		LastAttemptAt: now,
		NextProbeAt:   now.Add(delay),
		FailureCount:  failureCount,
		HTTPStatus:    statusCode,
		LastError:     reason,
	}
	if previous != nil {
		snapshot.Data = previous.Data
		snapshot.ReceivedAt = previous.ReceivedAt
		snapshot.FreshUntil = previous.FreshUntil
		if snapshot.FreshUntil == nil && previous.Status == UpstreamBillingProbeStatusOK && previous.ReceivedAt != nil {
			snapshot.FreshUntil = probeTimePtr(previous.ReceivedAt.Add(2 * time.Duration(intervalMinutes) * time.Minute))
		}
		snapshot.Balance = previous.Balance
		snapshot.UpstreamKind = normalizeUpstreamKind(previous.UpstreamKind)
	}
	return snapshot
}

func (s *UpstreamBillingProbeService) updateSnapshot(
	ctx context.Context,
	account *Account,
	snapshot *UpstreamBillingProbeSnapshot,
	rateMultiplier *float64,
) error {
	writer, ok := s.accountRepo.(upstreamBillingProbeSnapshotWriter)
	if !ok {
		return ErrUpstreamBillingProbeUnavailable
	}
	return writer.UpdateUpstreamBillingProbeSnapshot(ctx, account, snapshot, rateMultiplier)
}

func parseUpstreamBillingProbeResponse(body []byte) (map[string]any, error) {
	var response upstreamBillingProbeResponse
	if err := json.Unmarshal(body, &response); err != nil {
		return nil, err
	}
	if response.Object != "sub2api.key_billing" || response.SchemaVersion != 1 || response.BillingScope != "token" {
		return nil, fmt.Errorf("unexpected billing response schema")
	}
	if response.GroupRateMultiplier == nil || response.ResolvedRateMultiplier == nil ||
		response.PeakRateEnabled == nil || response.EffectiveRateMultiplier == nil {
		return nil, fmt.Errorf("incomplete billing response")
	}
	for _, value := range []float64{
		*response.GroupRateMultiplier,
		*response.ResolvedRateMultiplier,
		*response.EffectiveRateMultiplier,
	} {
		if value < 0 || math.IsNaN(value) || math.IsInf(value, 0) {
			return nil, fmt.Errorf("invalid billing multiplier")
		}
	}
	if response.UserRateMultiplier != nil && (*response.UserRateMultiplier < 0 || math.IsNaN(*response.UserRateMultiplier) || math.IsInf(*response.UserRateMultiplier, 0)) {
		return nil, fmt.Errorf("invalid user billing multiplier")
	}
	expectedResolved := *response.GroupRateMultiplier
	if response.UserRateMultiplier != nil {
		expectedResolved = *response.UserRateMultiplier
	}
	if !equalBillingMultiplier(*response.ResolvedRateMultiplier, expectedResolved) {
		return nil, fmt.Errorf("inconsistent resolved billing multiplier")
	}
	observedAt, err := time.Parse(time.RFC3339Nano, response.ObservedAt)
	if err != nil || observedAt.IsZero() {
		return nil, fmt.Errorf("invalid observed_at")
	}
	data := map[string]any{
		"object":                    response.Object,
		"schema_version":            response.SchemaVersion,
		"billing_scope":             response.BillingScope,
		"group_rate_multiplier":     *response.GroupRateMultiplier,
		"resolved_rate_multiplier":  *response.ResolvedRateMultiplier,
		"peak_rate_enabled":         *response.PeakRateEnabled,
		"effective_rate_multiplier": *response.EffectiveRateMultiplier,
		"observed_at":               observedAt.UTC().Format(time.RFC3339Nano),
	}
	if response.UserRateMultiplier != nil {
		data["user_rate_multiplier"] = *response.UserRateMultiplier
	}
	if *response.PeakRateEnabled {
		if response.PeakStart == nil || response.PeakEnd == nil || response.Timezone == nil ||
			response.PeakRateMultiplier == nil || response.AppliedPeakMultiplier == nil ||
			*response.PeakStart == "" || *response.PeakEnd == "" || *response.Timezone == "" ||
			*response.PeakRateMultiplier < 0 || *response.AppliedPeakMultiplier < 0 ||
			math.IsNaN(*response.PeakRateMultiplier) || math.IsInf(*response.PeakRateMultiplier, 0) ||
			math.IsNaN(*response.AppliedPeakMultiplier) || math.IsInf(*response.AppliedPeakMultiplier, 0) {
			return nil, fmt.Errorf("incomplete peak billing response")
		}
		data["peak_start"] = *response.PeakStart
		data["peak_end"] = *response.PeakEnd
		data["peak_rate_multiplier"] = *response.PeakRateMultiplier
		data["applied_peak_multiplier"] = *response.AppliedPeakMultiplier
		data["timezone"] = *response.Timezone
	}
	appliedPeak, ok := upstreamBillingPeakMultiplierAt(data, observedAt)
	if !ok {
		return nil, fmt.Errorf("invalid peak billing response")
	}
	if response.PeakRateEnabled != nil && *response.PeakRateEnabled {
		if !equalBillingMultiplier(*response.AppliedPeakMultiplier, appliedPeak) {
			return nil, fmt.Errorf("inconsistent applied peak multiplier")
		}
	} else if response.AppliedPeakMultiplier != nil && !equalBillingMultiplier(*response.AppliedPeakMultiplier, 1) {
		return nil, fmt.Errorf("inconsistent applied peak multiplier")
	}
	if !equalBillingMultiplier(*response.EffectiveRateMultiplier, *response.ResolvedRateMultiplier*appliedPeak) {
		return nil, fmt.Errorf("inconsistent effective billing multiplier")
	}
	return data, nil
}

func upstreamBillingRateAt(data map[string]any, now time.Time) (float64, bool) {
	if scope, _ := data["billing_scope"].(string); scope != "token" {
		return 0, false
	}
	base, ok := resolveAccountExtraNumber(data, "resolved_rate_multiplier")
	if !ok || base < 0 || math.IsNaN(base) || math.IsInf(base, 0) {
		return 0, false
	}
	appliedPeak, ok := upstreamBillingPeakMultiplierAt(data, now)
	if !ok {
		return 0, false
	}
	base *= appliedPeak
	if math.IsNaN(base) || math.IsInf(base, 0) {
		return 0, false
	}
	return base, true
}

// upstreamBillingProbeSyncRate converts the declared multiplier into the value
// the automatic write-back may store in accounts.rate_multiplier, at the
// precision that column supports (DECIMAL(10,4)).
//
// It reads resolved_rate_multiplier, not effective_rate_multiplier: the
// effective value folds in the peak coefficient that happened to apply at the
// instant of the probe, so writing it would freeze one probe cycle's peak (or
// off-peak) factor into a static column, while display and scheduling
// recompute the peak factor for the current time through upstreamBillingRateAt.
//
// The accepted range is deliberately narrower than the column:
//   - 0 is rejected. accountCost multiplies the request cost by this value, so
//     an upstream-declared 0 would stop quota_used from ever growing and every
//     admin-configured account quota and cost alert would silently stop
//     working. Admins may still set 0 by hand; only the automatic path refuses.
//   - anything above upstreamBillingRateSyncMaxMultiplier is rejected.
//
// A rejected declaration leaves the current multiplier untouched; the probe
// still records an OK snapshot carrying the raw declaration for display.
func upstreamBillingProbeSyncRate(data map[string]any) (float64, bool) {
	value, ok := resolveAccountExtraNumber(data, "resolved_rate_multiplier")
	if !ok || math.IsNaN(value) || math.IsInf(value, 0) {
		return 0, false
	}
	rounded := math.Round(value*upstreamBillingProbeAccountRateScale) / upstreamBillingProbeAccountRateScale
	if rounded <= 0 || rounded > upstreamBillingRateSyncMaxMultiplier {
		return 0, false
	}
	return rounded, true
}

func upstreamBillingPeakMultiplierAt(data map[string]any, now time.Time) (float64, bool) {
	peakEnabled, ok := data["peak_rate_enabled"].(bool)
	if !ok {
		return 0, false
	}
	if !peakEnabled {
		return 1, true
	}

	start, startOK := data["peak_start"].(string)
	end, endOK := data["peak_end"].(string)
	timezoneName, timezoneOK := data["timezone"].(string)
	peakMultiplier, multiplierOK := resolveAccountExtraNumber(data, "peak_rate_multiplier")
	startMinute, validStart := parseMinutes(start)
	endMinute, validEnd := parseMinutes(end)
	if !startOK || !endOK || !timezoneOK || !multiplierOK || !validStart || !validEnd ||
		startMinute >= endMinute || peakMultiplier < 0 || math.IsNaN(peakMultiplier) || math.IsInf(peakMultiplier, 0) {
		return 0, false
	}
	location, err := time.LoadLocation(timezoneName)
	if err != nil {
		return 0, false
	}

	local := now.In(location)
	minute := local.Hour()*60 + local.Minute()
	if minute >= startMinute && minute < endMinute {
		return peakMultiplier, true
	}
	return 1, true
}

func equalBillingMultiplier(left, right float64) bool {
	if math.IsNaN(left) || math.IsNaN(right) || math.IsInf(left, 0) || math.IsInf(right, 0) {
		return false
	}
	scale := math.Max(1, math.Max(math.Abs(left), math.Abs(right)))
	return math.Abs(left-right) <= 1e-9*scale
}

func decodeUpstreamBillingProbeSnapshot(extra map[string]any) *UpstreamBillingProbeSnapshot {
	if extra == nil {
		return nil
	}
	value, ok := extra[UpstreamBillingProbeExtraKey]
	if !ok {
		return nil
	}
	raw, err := json.Marshal(value)
	if err != nil {
		return nil
	}
	var snapshot UpstreamBillingProbeSnapshot
	if err := json.Unmarshal(raw, &snapshot); err != nil || snapshot.Status == "" {
		return nil
	}
	if snapshot.Status != UpstreamBillingProbeStatusOK &&
		snapshot.Status != UpstreamBillingProbeStatusUnsupported &&
		snapshot.Status != UpstreamBillingProbeStatusFailed &&
		snapshot.Status != UpstreamBillingProbeStatusPending {
		return nil
	}
	if snapshot.Balance != nil && snapshot.Balance.Status != UpstreamBillingProbeStatusOK &&
		snapshot.Balance.Status != UpstreamBillingProbeStatusUnsupported &&
		snapshot.Balance.Status != UpstreamBillingProbeStatusFailed &&
		snapshot.Balance.Status != UpstreamBillingProbeStatusPending {
		snapshot.Balance = nil
	}
	return &snapshot
}

// IsUpstreamBillingProbeIdentity reports whether an account identity may opt
// in to the upstream billing probe. `/v1/sub2api/billing` is a key-scoped
// sub2api convention shared by the supported API-key platforms (including the
// CN providers, whose official-domain accounts are short-circuited to
// "unsupported" by upstreamBillingProbeTargetIsOfficialAPI).
// Non-sub2api upstreams return 404 and the snapshot records "unsupported".
// Only AccountTypeAPIKey is in scope. OAuth/Bedrock hold no static API key to
// present at all; AccountTypeUpstream (antigravity relay accounts) does carry
// a base_url plus a static api_key, but it is deliberately left out of the
// current supported set. New antigravity relay accounts are created with
// type=apikey by the admin form, so only pre-existing type=upstream rows
// cannot turn the probe on.
func IsUpstreamBillingProbeIdentity(platform, accountType string) bool {
	if accountType != AccountTypeAPIKey {
		return false
	}
	switch platform {
	case PlatformOpenAI, PlatformAnthropic, PlatformGemini, PlatformAntigravity, PlatformGrok,
		PlatformKimi, PlatformZhipu, PlatformDeepseek, PlatformMiniMax:
		return true
	default:
		return false
	}
}

func isUpstreamBillingProbeAccount(account *Account) bool {
	return account != nil && IsUpstreamBillingProbeIdentity(account.Platform, account.Type)
}

// upstreamBillingProbeOfficialAPIDomains lists the root domains of official
// provider APIs. The create form fills empty base_url values with official
// defaults (and offers official regional presets like us-east-1.api.x.ai),
// so probing them would send the account key to an official API path that
// cannot exist. Matching is by registrable root domain — exact host or any
// subdomain, after stripping the port and a trailing DNS dot — because no
// third-party sub2api relay can live under these domains, while custom
// relays (the only targets that can answer /v1/sub2api/billing) always do
// probe. OpenAI-platform accounts never reach this check: they keep the
// upstream-official behavior of probing api.openai.com.
// ollama.com is a first-class configuration here (Ollama Cloud accounts are
// platform openai/anthropic with base_url https://ollama.com/v1), and it is
// an official provider API just like the rest, so it belongs on this list.
// CN provider domains (moonshot.cn / kimi.com / bigmodel.cn / deepseek.com)
// serve the same role: official APIs that can never host /v1/sub2api/billing,
// so their accounts short-circuit to "unsupported" without a request.
var upstreamBillingProbeOfficialAPIDomains = []string{
	"anthropic.com",
	"googleapis.com",
	"x.ai",
	"grok.com",
	"openai.com",
	"ollama.com",
	"moonshot.cn",
	"kimi.com",
	"bigmodel.cn",
	"deepseek.com",
}

func upstreamBillingProbeTargetIsOfficialAPI(baseURL string) bool {
	baseURL = strings.TrimSpace(baseURL)
	if baseURL == "" {
		return true
	}
	parsed, err := url.Parse(baseURL)
	if err != nil {
		return false
	}
	host := strings.TrimSuffix(strings.ToLower(parsed.Hostname()), ".")
	if host == "" {
		return true
	}
	for _, domain := range upstreamBillingProbeOfficialAPIDomains {
		if host == domain || strings.HasSuffix(host, "."+domain) {
			return true
		}
	}
	return false
}

func upstreamBillingProbeEnabled(account *Account) bool {
	if account == nil || account.Extra == nil {
		return false
	}
	enabled, ok := account.Extra[UpstreamBillingProbeEnabledExtraKey].(bool)
	return ok && enabled
}

// upstreamBillingProbeAutoEligible reports whether the scheduled runner may
// probe an account nobody configured. The enabled flag has three states: true
// (on), false (an admin turned it off) and absent (never decided). Only the
// absent state is auto-eligible, so an explicit "off" is never overridden and
// existing rows need no backfill.
//
// Auto-eligible probes only identify the upstream and read its balance. They
// never write a rate back: rate sync still requires both switches to be true.
func upstreamBillingProbeAutoEligible(account *Account) bool {
	if !isUpstreamBillingProbeAccount(account) {
		return false
	}
	if account.Extra != nil {
		if _, decided := account.Extra[UpstreamBillingProbeEnabledExtraKey]; decided {
			return false
		}
	}
	return true
}

// upstreamBillingProbeScheduled is what the runner checks: explicitly enabled,
// or never decided.
func upstreamBillingProbeScheduled(account *Account) bool {
	return upstreamBillingProbeEnabled(account) || upstreamBillingProbeAutoEligible(account)
}

// upstreamBillingRateSyncEnabled is the probe-side pre-filter deciding whether
// a rate is even proposed for write-back. It is a necessary condition, not the
// authority: the repository CAS re-checks both switches against the row it
// updates, so a switch flipped between load and write can never sneak a rate in.
func upstreamBillingRateSyncEnabled(account *Account) bool {
	if account == nil || account.Extra == nil {
		return false
	}
	enabled, ok := account.Extra[UpstreamBillingRateSyncEnabledExtraKey].(bool)
	return ok && enabled && upstreamBillingProbeEnabled(account)
}

func (s *UpstreamBillingProbeService) currentTime() time.Time {
	if s != nil && s.now != nil {
		return s.now()
	}
	return time.Now()
}

func nextProbeDelay(intervalMinutes int, retryAfterDuration time.Duration) time.Duration {
	interval := time.Duration(intervalMinutes) * time.Minute
	if interval < upstreamBillingProbeMinIntervalMinutes*time.Minute {
		interval = upstreamBillingProbeMinIntervalMinutes * time.Minute
	}
	if interval > upstreamBillingProbeMaxDelay {
		interval = upstreamBillingProbeMaxDelay
	}
	jitterRange := interval / 5
	if jitterRange > 5*time.Minute {
		jitterRange = 5 * time.Minute
	}
	if jitterRange > 0 {
		interval += time.Duration(rand.Int64N(int64(jitterRange)*2+1)) - jitterRange
	}
	if retryAfterDuration > interval {
		// Retry-After is an explicit upstream instruction; do not shorten it
		// with the local maximum delay.
		return retryAfterDuration
	}
	if interval > upstreamBillingProbeMaxDelay {
		return upstreamBillingProbeMaxDelay
	}
	return interval
}

// unsupportedProbeDelay keeps an unsupported (or identified-but-balanceless)
// account on a ten-minute retry cadence. The operator asked for "recognise it,
// fill it in automatically, refresh every ten minutes"; the previous
// interval×8 stretch put the next attempt hours away and the account list showed
// "old data · 2 hours ago". A longer Retry-After from the upstream is still
// honoured, because that is an explicit instruction to back off.
func unsupportedProbeDelay(retryAfterDuration time.Duration) time.Duration {
	if retryAfterDuration > upstreamBillingProbeUnsupportedRetryInterval {
		return retryAfterDuration
	}
	return upstreamBillingProbeUnsupportedRetryInterval
}

func retryAfter(header http.Header, now time.Time) time.Duration {
	value := strings.TrimSpace(header.Get("Retry-After"))
	if value == "" {
		return 0
	}
	if seconds, err := strconv.Atoi(value); err == nil && seconds > 0 {
		return time.Duration(seconds) * time.Second
	}
	if at, err := http.ParseTime(value); err == nil {
		if delay := at.Sub(now); delay > 0 {
			return delay
		}
	}
	return 0
}

func probeTimePtr(value time.Time) *time.Time {
	return &value
}

func safeProbeError(err error) string {
	if err == nil {
		return ""
	}
	if errors.Is(err, ErrUpstreamBillingProbeAccountInvalid) {
		return ErrUpstreamBillingProbeAccountInvalid.Error()
	}
	if errors.Is(err, ErrUpstreamBillingProbeUnavailable) {
		return ErrUpstreamBillingProbeUnavailable.Error()
	}
	return "probe_failed"
}
