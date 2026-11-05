package service

import (
	"context"
	"database/sql"
	"fmt"
	"log/slog"
	"math"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
)

// Upstream monitor is a light read model over the upstream billing probe that
// accounts already collect. It never calls an upstream by itself: the existing
// probe owns every network request, and this service only aggregates the stored
// snapshots plus a bounded history for the operations center page.
const (
	upstreamMonitorSampleInterval   = 5 * time.Minute
	upstreamMonitorSampleMaxAge     = 30 * time.Minute
	upstreamMonitorSampleRetention  = 14 * 24 * time.Hour
	upstreamMonitorRateRetention    = 90 * 24 * time.Hour
	upstreamMonitorLeaderLockKey    = "upstream:monitor:sampler:leader"
	upstreamMonitorLeaderLockTTL    = 4 * time.Minute
	upstreamMonitorDefaultHistory   = 7 * 24 * time.Hour
	upstreamMonitorMaxHistory       = 30 * 24 * time.Hour
	upstreamMonitorMaxHistoryPoints = 240
	upstreamMonitorMaxPageSize      = 200

	// Second batch: spend rollups and alerts. All windows stay bounded so the
	// page never reads the full sample history.
	upstreamMonitorDailyRetention  = 120 * 24 * time.Hour
	upstreamMonitorAlertRetention  = 90 * 24 * time.Hour
	upstreamMonitorAlertCooldown   = 6 * time.Hour
	upstreamMonitorRateAlertMinPct = 10.0
	upstreamMonitorBeijingOffset   = 8 * time.Hour
	upstreamMonitorExportMaxRows   = 5000
)

// UpstreamMonitorBeijingZone is the fixed +08:00 day boundary used for "today"
// spend and the daily rollup. A fixed offset keeps the rollup stable on servers
// that run in UTC and do not ship a tz database.
var UpstreamMonitorBeijingZone = time.FixedZone("CST", int(upstreamMonitorBeijingOffset/time.Second))

const (
	UpstreamBalanceStatusOK          = "ok"
	UpstreamBalanceStatusStale       = "stale"
	UpstreamBalanceStatusFailed      = "failed"
	UpstreamBalanceStatusUnsupported = "unsupported"
	UpstreamBalanceStatusUnknown     = "unknown"
)

// UpstreamMonitorFilter is the account list query. Empty strings mean "all".
type UpstreamMonitorFilter struct {
	Platform      string
	AccountType   string
	AccountStatus string
	Search        string
	BalanceStatus string
	Sort          string
	Page          int
	PageSize      int
}

// UpstreamMonitorAccount is one upstream account row with the balance and rate
// captured by the billing probe. All optional fields are nil until the probe
// has produced a value, so the UI does not have to guess a zero balance.
type UpstreamMonitorAccount struct {
	AccountID       int64      `json:"account_id"`
	Name            string     `json:"name"`
	Notes           string     `json:"notes,omitempty"`
	Platform        string     `json:"platform"`
	Type            string     `json:"type"`
	Status          string     `json:"status"`
	Balance         *float64   `json:"balance"`
	Unlimited       bool       `json:"unlimited"`
	Unit            string     `json:"unit"`
	BalanceStatus   string     `json:"balance_status"`
	BalanceReadAt   *time.Time `json:"balance_read_at"`
	BalanceFreshTo  *time.Time `json:"balance_fresh_until"`
	BalanceError    string     `json:"balance_error,omitempty"`
	Rate            *float64   `json:"rate"`
	RateStatus      string     `json:"rate_status,omitempty"`
	RateSynced      *float64   `json:"rate_synced,omitempty"`
	RateReadAt      *time.Time `json:"rate_read_at"`
	RateError       string     `json:"rate_error,omitempty"`
	RateSyncEnabled bool       `json:"rate_sync_enabled"`
	LastSampledAt   *time.Time `json:"last_sampled_at,omitempty"`
	AlertThreshold  *float64   `json:"alert_threshold,omitempty"`
	AlertEnabled    bool       `json:"alert_enabled"`
}

// UpstreamCurrencyTotal is the balance sum of one currency unit.
type UpstreamCurrencyTotal struct {
	Unit     string  `json:"unit"`
	Total    float64 `json:"total"`
	Accounts int64   `json:"accounts"`
}

// UpstreamMonitorOverview is the header payload of the page. It is intentionally
// small and derives every number from the accounts table plus the two bounded
// history tables; no usage_logs scan is involved.
type UpstreamMonitorOverview struct {
	GeneratedAt    time.Time                `json:"generated_at"`
	TotalAccounts  int64                    `json:"total_accounts"`
	WithBalance    int64                    `json:"with_balance"`
	Healthy        int64                    `json:"healthy"`
	Stale          int64                    `json:"stale"`
	Failed         int64                    `json:"failed"`
	Unsupported    int64                    `json:"unsupported"`
	RateKnown      int64                    `json:"rate_known"`
	RateChanges24h int64                    `json:"rate_changes_24h"`
	Currencies     []UpstreamCurrencyTotal  `json:"currencies"`
	Attention      []UpstreamMonitorAccount `json:"attention"`
	LastSampleAt   *time.Time               `json:"last_sample_at,omitempty"`
	Spend          []UpstreamSpendTotal     `json:"spend"`
	OpenAlerts     int64                    `json:"open_alerts"`
}

// UpstreamSpendTotal is balance-derived spend for one currency. "Spend" means
// the sampled balance dropped between two consecutive samples; top-ups are not
// counted, so the page labels the number as an estimate instead of pretending
// it is an invoice.
type UpstreamSpendTotal struct {
	Unit       string  `json:"unit"`
	Today      float64 `json:"today"`
	Last7Days  float64 `json:"last_7_days"`
	Last30Days float64 `json:"last_30_days"`
}

// UpstreamTrendPoint is one day of the daily rollup for one currency.
type UpstreamTrendPoint struct {
	Day          string   `json:"day"`
	Unit         string   `json:"unit"`
	Spend        float64  `json:"spend"`
	BalanceClose *float64 `json:"balance_close,omitempty"`
	Accounts     int64    `json:"accounts"`
}

// UpstreamAlertRule is the per-account alert configuration. No row means the
// account is not alerting at all.
type UpstreamAlertRule struct {
	AccountID  int64     `json:"account_id"`
	Threshold  float64   `json:"threshold"`
	Unit       string    `json:"unit"`
	RateMinPct float64   `json:"rate_min_pct"`
	Enabled    bool      `json:"enabled"`
	UpdatedAt  time.Time `json:"updated_at"`
}

// UpstreamAlert is one recorded alert.
type UpstreamAlert struct {
	ID        int64      `json:"id"`
	AccountID int64      `json:"account_id"`
	Name      string     `json:"name"`
	Kind      string     `json:"kind"`
	Level     string     `json:"level"`
	Balance   *float64   `json:"balance,omitempty"`
	Threshold *float64   `json:"threshold,omitempty"`
	Unit      string     `json:"unit"`
	OldRate   *float64   `json:"old_rate,omitempty"`
	NewRate   *float64   `json:"new_rate,omitempty"`
	Message   string     `json:"message"`
	CreatedAt time.Time  `json:"created_at"`
	AckedAt   *time.Time `json:"acked_at,omitempty"`
}

// UpstreamAlertFilter queries recorded alerts. Acked is "", "open" or "acked".
type UpstreamAlertFilter struct {
	AccountID int64
	Kind      string
	Acked     string
	From      time.Time
	Page      int
	PageSize  int
}

// UpstreamMonitorPoint is one sampled balance value.
type UpstreamMonitorPoint struct {
	Balance   float64   `json:"balance"`
	Unit      string    `json:"unit"`
	SampledAt time.Time `json:"sampled_at"`
}

// UpstreamRateChange is one observed upstream billing-rate change.
type UpstreamRateChange struct {
	ID        int64     `json:"id"`
	AccountID int64     `json:"account_id"`
	Name      string    `json:"name"`
	OldRate   float64   `json:"old_rate"`
	NewRate   float64   `json:"new_rate"`
	ChangePct float64   `json:"change_pct"`
	ChangedAt time.Time `json:"changed_at"`
}

// UpstreamRateFilter queries the rate change history.
type UpstreamRateFilter struct {
	AccountID int64
	From      time.Time
	// MinPct drops moves smaller than this absolute percentage. Zero means all.
	MinPct   float64
	Page     int
	PageSize int
}

// UpstreamProbeSnapshot is the raw text projection read from accounts.extra.
type UpstreamProbeSnapshot struct {
	AccountID   int64
	BalanceText string
	Unit        string
	RateText    string
}

// UpstreamMonitorState is the last value observed by the sampler. It keeps
// change detection to one indexed row per account instead of a lateral scan.
type UpstreamMonitorState struct {
	LastBalance  *float64
	LastUnit     string
	LastRate     *float64
	LastSampleAt *time.Time
}

// UpstreamSampleWrite is one change the sampler applies in a transaction.
type UpstreamSampleWrite struct {
	AccountID    int64
	HasBalance   bool
	Balance      float64
	Unit         string
	SampledAt    *time.Time
	HasRate      bool
	Rate         float64
	PreviousRate *float64
	RateChanged  bool
	// Spend is the balance drop since the previous sample, in the sample unit.
	// A top-up or a currency change leaves it at zero.
	Spend float64
}

// UpstreamMonitorSampleResult reports what one sampling cycle did.
type UpstreamMonitorSampleResult struct {
	Scanned     int `json:"scanned"`
	Sampled     int `json:"sampled"`
	RateChanges int `json:"rate_changes"`
	Alerts      int `json:"alerts"`
}

// UpstreamMonitorRepository is the read/write boundary.
type UpstreamMonitorRepository interface {
	ListAccounts(ctx context.Context, filter UpstreamMonitorFilter) ([]UpstreamMonitorAccount, int64, error)
	Overview(ctx context.Context, now time.Time) (UpstreamMonitorOverview, error)
	BalanceHistory(ctx context.Context, accountID int64, from time.Time) ([]UpstreamMonitorPoint, error)
	RateChanges(ctx context.Context, filter UpstreamRateFilter) ([]UpstreamRateChange, int64, error)
	ListProbeSnapshots(ctx context.Context) ([]UpstreamProbeSnapshot, error)
	LoadSampleStates(ctx context.Context) (map[int64]UpstreamMonitorState, error)
	ApplySamples(ctx context.Context, writes []UpstreamSampleWrite, pruneSamplesBefore, pruneRatesBefore, pruneDailyBefore time.Time) error

	// Second batch: spend rollups and alerts.
	SpendTotals(ctx context.Context, today time.Time) ([]UpstreamSpendTotal, error)
	OpenAlertCount(ctx context.Context, since time.Time) (int64, error)
	Trend(ctx context.Context, from time.Time) ([]UpstreamTrendPoint, error)
	LoadAlertRules(ctx context.Context) (map[int64]UpstreamAlertRule, error)
	UpsertAlertRule(ctx context.Context, rule UpstreamAlertRule) (UpstreamAlertRule, error)
	DeleteAlertRule(ctx context.Context, accountID int64) error
	RecordAlerts(ctx context.Context, alerts []UpstreamAlert, cooldownSince time.Time) (int, error)
	Alerts(ctx context.Context, filter UpstreamAlertFilter) ([]UpstreamAlert, int64, error)
	AckAlert(ctx context.Context, id int64) error
	ListAccountsForExport(ctx context.Context, filter UpstreamMonitorFilter, limit int) ([]UpstreamMonitorAccount, error)
}

// UpstreamMonitorService serves the operations center page and runs the sampler.
type UpstreamMonitorService struct {
	repo       UpstreamMonitorRepository
	lockCache  LeaderLockCache
	db         *sql.DB
	instanceID string
}

func NewUpstreamMonitorService(repo UpstreamMonitorRepository) *UpstreamMonitorService {
	return &UpstreamMonitorService{repo: repo, instanceID: uuid.NewString()}
}

// SetLeaderLock injects cross-instance coordination. Without it the sampler
// still runs, which keeps local development and single-instance setups working.
func (s *UpstreamMonitorService) SetLeaderLock(lockCache LeaderLockCache, db *sql.DB) {
	if s == nil {
		return
	}
	s.lockCache = lockCache
	s.db = db
}

func (s *UpstreamMonitorService) List(ctx context.Context, filter UpstreamMonitorFilter) ([]UpstreamMonitorAccount, int64, error) {
	filter = normalizeUpstreamMonitorFilter(filter)
	rows, total, err := s.repo.ListAccounts(ctx, filter)
	if err != nil {
		return nil, 0, err
	}
	now := time.Now().UTC()
	for i := range rows {
		rows[i].finalize(now)
	}
	return rows, total, nil
}

func (s *UpstreamMonitorService) Overview(ctx context.Context) (UpstreamMonitorOverview, error) {
	overview, err := s.repo.Overview(ctx, time.Now().UTC())
	if err != nil {
		return UpstreamMonitorOverview{}, err
	}
	now := time.Now().UTC()
	for i := range overview.Attention {
		overview.Attention[i].finalize(now)
	}
	if overview.Currencies == nil {
		overview.Currencies = []UpstreamCurrencyTotal{}
	}
	if overview.Attention == nil {
		overview.Attention = []UpstreamMonitorAccount{}
	}
	spend, err := s.repo.SpendTotals(ctx, now)
	if err != nil {
		return UpstreamMonitorOverview{}, err
	}
	if spend == nil {
		spend = []UpstreamSpendTotal{}
	}
	overview.Spend = spend
	openAlerts, err := s.repo.OpenAlertCount(ctx, now.Add(-upstreamMonitorAlertRetention))
	if err != nil {
		return UpstreamMonitorOverview{}, err
	}
	overview.OpenAlerts = openAlerts
	return overview, nil
}

func (s *UpstreamMonitorService) History(ctx context.Context, accountID int64, from time.Time) ([]UpstreamMonitorPoint, error) {
	if accountID <= 0 {
		return []UpstreamMonitorPoint{}, nil
	}
	now := time.Now().UTC()
	if from.IsZero() {
		from = now.Add(-upstreamMonitorDefaultHistory)
	}
	if from.Before(now.Add(-upstreamMonitorMaxHistory)) {
		from = now.Add(-upstreamMonitorMaxHistory)
	}
	points, err := s.repo.BalanceHistory(ctx, accountID, from)
	if err != nil {
		return nil, err
	}
	return downsampleUpstreamPoints(points, upstreamMonitorMaxHistoryPoints), nil
}

func (s *UpstreamMonitorService) RateChanges(ctx context.Context, filter UpstreamRateFilter) ([]UpstreamRateChange, int64, error) {
	now := time.Now().UTC()
	if filter.From.IsZero() || filter.From.Before(now.Add(-upstreamMonitorRateRetention)) {
		filter.From = now.Add(-7 * 24 * time.Hour)
	}
	if filter.Page < 1 {
		filter.Page = 1
	}
	if filter.PageSize < 1 {
		filter.PageSize = 20
	}
	if filter.PageSize > upstreamMonitorMaxPageSize {
		filter.PageSize = upstreamMonitorMaxPageSize
	}
	if filter.MinPct < 0 {
		filter.MinPct = 0
	}
	if filter.MinPct > 100 {
		filter.MinPct = 100
	}
	return s.repo.RateChanges(ctx, filter)
}

// SampleNow collects one bounded snapshot pass. It reads only accounts.extra,
// so it adds no upstream requests and no usage_logs work.
func (s *UpstreamMonitorService) SampleNow(ctx context.Context) (UpstreamMonitorSampleResult, error) {
	var result UpstreamMonitorSampleResult
	snapshots, err := s.repo.ListProbeSnapshots(ctx)
	if err != nil {
		return result, err
	}
	result.Scanned = len(snapshots)
	if len(snapshots) == 0 {
		return result, nil
	}
	states, err := s.repo.LoadSampleStates(ctx)
	if err != nil {
		return result, err
	}
	now := time.Now().UTC()
	writes := buildUpstreamSampleWrites(snapshots, states, now)
	if len(writes) == 0 {
		return result, nil
	}
	for _, write := range writes {
		if write.SampledAt != nil {
			result.Sampled++
		}
		if write.RateChanged {
			result.RateChanges++
		}
	}
	rules, err := s.repo.LoadAlertRules(ctx)
	if err != nil {
		return result, err
	}
	alerts := buildUpstreamAlerts(writes, rules)
	if len(alerts) > 0 {
		recorded, recordErr := s.repo.RecordAlerts(ctx, alerts, now.Add(-upstreamMonitorAlertCooldown))
		if recordErr != nil {
			return result, recordErr
		}
		result.Alerts = recorded
	}
	err = s.repo.ApplySamples(
		ctx,
		writes,
		now.Add(-upstreamMonitorSampleRetention),
		now.Add(-upstreamMonitorRateRetention),
		now.Add(-upstreamMonitorDailyRetention),
	)
	return result, err
}

// Start runs the sampler until ctx is cancelled. Leadership is re-contested on
// every cycle so several gateway instances can share one database safely.
func (s *UpstreamMonitorService) Start(ctx context.Context) {
	if s == nil || s.repo == nil {
		return
	}
	go func() {
		timer := time.NewTimer(30 * time.Second)
		defer timer.Stop()
		select {
		case <-ctx.Done():
			return
		case <-timer.C:
			s.runOnce(ctx)
		}
		ticker := time.NewTicker(upstreamMonitorSampleInterval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				s.runOnce(ctx)
			}
		}
	}()
}

func (s *UpstreamMonitorService) runOnce(ctx context.Context) {
	release, ok := tryAcquireSingletonLeaderLock(ctx, s.lockCache, s.db, upstreamMonitorLeaderLockKey, s.instanceID, upstreamMonitorLeaderLockTTL)
	if !ok {
		return
	}
	defer release()

	runCtx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()
	result, err := s.SampleNow(runCtx)
	if err != nil {
		slog.Warn("upstream_monitor_sample_failed", "error", err)
		return
	}
	if result.Sampled > 0 || result.RateChanges > 0 {
		slog.Info("upstream_monitor_sampled", "scanned", result.Scanned, "samples", result.Sampled, "rate_changes", result.RateChanges)
	}
}

// Trend returns the daily rollup for the last N days (7, 14 or 30).
func (s *UpstreamMonitorService) Trend(ctx context.Context, days int) ([]UpstreamTrendPoint, error) {
	if days <= 0 {
		days = 7
	}
	if days > 30 {
		days = 30
	}
	from := beginOfUpstreamBeijingDay(time.Now().UTC()).AddDate(0, 0, -(days - 1))
	points, err := s.repo.Trend(ctx, from)
	if err != nil {
		return nil, err
	}
	if points == nil {
		points = []UpstreamTrendPoint{}
	}
	return points, nil
}

// AlertRules returns every per-account rule. The page merges it with the account
// list client-side; the sampler uses the same map.
func (s *UpstreamMonitorService) AlertRules(ctx context.Context) (map[int64]UpstreamAlertRule, error) {
	return s.repo.LoadAlertRules(ctx)
}

// SaveAlertRule creates or updates one account rule.
func (s *UpstreamMonitorService) SaveAlertRule(ctx context.Context, rule UpstreamAlertRule) (UpstreamAlertRule, error) {
	if rule.AccountID <= 0 {
		return UpstreamAlertRule{}, fmt.Errorf("invalid account id")
	}
	if rule.Threshold < 0 {
		rule.Threshold = 0
	}
	if rule.RateMinPct < 0 {
		rule.RateMinPct = 0
	}
	if rule.RateMinPct > 100 {
		rule.RateMinPct = 100
	}
	rule.Unit = strings.TrimSpace(rule.Unit)
	if len(rule.Unit) > 16 {
		rule.Unit = rule.Unit[:16]
	}
	return s.repo.UpsertAlertRule(ctx, rule)
}

func (s *UpstreamMonitorService) DeleteAlertRule(ctx context.Context, accountID int64) error {
	if accountID <= 0 {
		return fmt.Errorf("invalid account id")
	}
	return s.repo.DeleteAlertRule(ctx, accountID)
}

func (s *UpstreamMonitorService) Alerts(ctx context.Context, filter UpstreamAlertFilter) ([]UpstreamAlert, int64, error) {
	filter = normalizeUpstreamAlertFilter(filter)
	return s.repo.Alerts(ctx, filter)
}

func (s *UpstreamMonitorService) AckAlert(ctx context.Context, id int64) error {
	if id <= 0 {
		return fmt.Errorf("invalid alert id")
	}
	return s.repo.AckAlert(ctx, id)
}

// ExportAccounts returns the filtered rows for CSV export. The limit keeps one
// click from turning into an unbounded query.
func (s *UpstreamMonitorService) ExportAccounts(ctx context.Context, filter UpstreamMonitorFilter, limit int) ([]UpstreamMonitorAccount, error) {
	filter = normalizeUpstreamMonitorFilter(filter)
	if limit <= 0 || limit > upstreamMonitorExportMaxRows {
		limit = upstreamMonitorExportMaxRows
	}
	rows, err := s.repo.ListAccountsForExport(ctx, filter, limit)
	if err != nil {
		return nil, err
	}
	now := time.Now().UTC()
	for i := range rows {
		rows[i].finalize(now)
	}
	return rows, nil
}

func normalizeUpstreamMonitorFilter(filter UpstreamMonitorFilter) UpstreamMonitorFilter {
	filter.Platform = strings.TrimSpace(filter.Platform)
	filter.AccountType = strings.TrimSpace(filter.AccountType)
	filter.AccountStatus = strings.TrimSpace(filter.AccountStatus)
	filter.Search = strings.TrimSpace(filter.Search)
	if len(filter.Search) > 100 {
		filter.Search = filter.Search[:100]
	}
	switch strings.TrimSpace(filter.BalanceStatus) {
	case "", "all":
		filter.BalanceStatus = ""
	case UpstreamBalanceStatusOK, UpstreamBalanceStatusStale, UpstreamBalanceStatusFailed, UpstreamBalanceStatusUnsupported, UpstreamBalanceStatusUnknown:
		filter.BalanceStatus = strings.TrimSpace(filter.BalanceStatus)
	default:
		filter.BalanceStatus = ""
	}
	switch strings.TrimSpace(filter.Sort) {
	case "balance_asc", "balance_desc", "name":
		filter.Sort = strings.TrimSpace(filter.Sort)
	default:
		filter.Sort = "name"
	}
	if filter.Page < 1 {
		filter.Page = 1
	}
	if filter.PageSize < 1 {
		filter.PageSize = 20
	}
	if filter.PageSize > upstreamMonitorMaxPageSize {
		filter.PageSize = upstreamMonitorMaxPageSize
	}
	return filter
}

// finalize converts the stored "ok" into "stale" once its freshness window has
// elapsed, so the page shows the same status the account list shows.
func (a *UpstreamMonitorAccount) finalize(now time.Time) {
	if a.BalanceStatus == "" {
		a.BalanceStatus = UpstreamBalanceStatusUnknown
	}
	if a.BalanceStatus == UpstreamBalanceStatusOK && a.BalanceFreshTo != nil && now.After(*a.BalanceFreshTo) {
		a.BalanceStatus = UpstreamBalanceStatusStale
	}
}

// buildUpstreamSampleWrites is pure so the change detection can be unit tested
// without a database. A sample is written when the balance changed, when the
// currency changed, or when the previous sample is older than the refresh
// window; a rate row is written only for a real value change.
func buildUpstreamSampleWrites(snapshots []UpstreamProbeSnapshot, states map[int64]UpstreamMonitorState, now time.Time) []UpstreamSampleWrite {
	writes := make([]UpstreamSampleWrite, 0, len(snapshots))
	for _, snapshot := range snapshots {
		balance, balanceOK := ParseUpstreamMonitorNumber(snapshot.BalanceText)
		rate, rateOK := ParseUpstreamMonitorNumber(snapshot.RateText)
		if !balanceOK && !rateOK {
			continue
		}
		state, hasState := states[snapshot.AccountID]
		unit := strings.TrimSpace(snapshot.Unit)
		write := UpstreamSampleWrite{AccountID: snapshot.AccountID}

		if balanceOK {
			write.HasBalance = true
			write.Balance = balance
			write.Unit = unit
			needsSample := !hasState ||
				state.LastBalance == nil ||
				state.LastUnit != unit ||
				!upstreamMonitorSameNumber(*state.LastBalance, balance) ||
				state.LastSampleAt == nil ||
				now.Sub(*state.LastSampleAt) >= upstreamMonitorSampleMaxAge
			if needsSample {
				sampledAt := now
				write.SampledAt = &sampledAt
				if hasState && state.LastBalance != nil && state.LastUnit == unit && *state.LastBalance > balance {
					write.Spend = *state.LastBalance - balance
				}
			}
		}

		if rateOK {
			write.HasRate = true
			write.Rate = rate
			if hasState && state.LastRate != nil && !upstreamMonitorSameNumber(*state.LastRate, rate) {
				previous := *state.LastRate
				write.PreviousRate = &previous
				write.RateChanged = true
			}
		}

		if write.SampledAt != nil || write.RateChanged || (write.HasRate && !hasState) {
			writes = append(writes, write)
		}
	}
	return writes
}

func ParseUpstreamMonitorNumber(raw string) (float64, bool) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return 0, false
	}
	value, err := strconv.ParseFloat(raw, 64)
	if err != nil || math.IsNaN(value) || math.IsInf(value, 0) {
		return 0, false
	}
	return value, true
}

func upstreamMonitorSameNumber(a, b float64) bool {
	return math.Abs(a-b) < 0.0000005
}

func ParseUpstreamSnapshotTime(raw string) *time.Time {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil
	}
	if parsed, err := time.Parse(time.RFC3339Nano, raw); err == nil {
		value := parsed.UTC()
		return &value
	}
	if parsed, err := time.Parse(time.RFC3339, raw); err == nil {
		value := parsed.UTC()
		return &value
	}
	return nil
}

func downsampleUpstreamPoints(points []UpstreamMonitorPoint, limit int) []UpstreamMonitorPoint {
	if limit <= 2 || len(points) <= limit {
		return points
	}
	out := make([]UpstreamMonitorPoint, 0, limit)
	step := float64(len(points)) / float64(limit)
	for i := 0; i < limit; i++ {
		index := int(float64(i) * step)
		if index >= len(points) {
			index = len(points) - 1
		}
		out = append(out, points[index])
	}
	last := points[len(points)-1]
	if out[len(out)-1].SampledAt != last.SampledAt {
		out = append(out, last)
	}
	return out
}

func normalizeUpstreamAlertFilter(filter UpstreamAlertFilter) UpstreamAlertFilter {
	switch strings.TrimSpace(filter.Acked) {
	case "open", "acked":
		filter.Acked = strings.TrimSpace(filter.Acked)
	default:
		filter.Acked = ""
	}
	switch strings.TrimSpace(filter.Kind) {
	case "balance_low", "balance_depleted", "rate_jump":
		filter.Kind = strings.TrimSpace(filter.Kind)
	default:
		filter.Kind = ""
	}
	if filter.From.IsZero() || filter.From.Before(time.Now().UTC().Add(-upstreamMonitorAlertRetention)) {
		filter.From = time.Now().UTC().Add(-7 * 24 * time.Hour)
	}
	if filter.Page < 1 {
		filter.Page = 1
	}
	if filter.PageSize < 1 {
		filter.PageSize = 20
	}
	if filter.PageSize > upstreamMonitorMaxPageSize {
		filter.PageSize = upstreamMonitorMaxPageSize
	}
	return filter
}

// buildUpstreamAlerts is pure so the low-balance and rate-jump rules can be
// unit tested without a database. Cooldown and de-duplication live in the
// repository, which keeps one alert per account and kind per cooling window.
func buildUpstreamAlerts(writes []UpstreamSampleWrite, rules map[int64]UpstreamAlertRule) []UpstreamAlert {
	alerts := make([]UpstreamAlert, 0, 4)
	now := time.Now().UTC()
	for _, write := range writes {
		rule, hasRule := rules[write.AccountID]
		if write.SampledAt != nil && write.HasBalance && hasRule && rule.Enabled {
			if rule.Unit == "" || rule.Unit == write.Unit {
				balance := write.Balance
				threshold := rule.Threshold
				switch {
				case balance <= 0:
					alerts = append(alerts, UpstreamAlert{
						AccountID: write.AccountID,
						Kind:      "balance_depleted",
						Level:     "critical",
						Balance:   &balance,
						Threshold: &threshold,
						Unit:      write.Unit,
						Message:   fmt.Sprintf("余额已归零（%s）", formatUpstreamAlertMoney(balance, write.Unit)),
						CreatedAt: now,
					})
				case balance < threshold:
					alerts = append(alerts, UpstreamAlert{
						AccountID: write.AccountID,
						Kind:      "balance_low",
						Level:     "warning",
						Balance:   &balance,
						Threshold: &threshold,
						Unit:      write.Unit,
						Message: fmt.Sprintf("余额 %s 低于阈值 %s",
							formatUpstreamAlertMoney(balance, write.Unit),
							formatUpstreamAlertMoney(threshold, rule.Unit)),
						CreatedAt: now,
					})
				}
			}
		}
		if write.RateChanged && write.PreviousRate != nil && *write.PreviousRate > 0 {
			minPct := upstreamMonitorRateAlertMinPct
			if hasRule && rule.RateMinPct > 0 {
				minPct = rule.RateMinPct
			}
			pct := UpstreamRateChangePct(*write.PreviousRate, write.Rate)
			if math.Abs(pct) >= minPct {
				oldRate := *write.PreviousRate
				newRate := write.Rate
				alerts = append(alerts, UpstreamAlert{
					AccountID: write.AccountID,
					Kind:      "rate_jump",
					Level:     "warning",
					OldRate:   &oldRate,
					NewRate:   &newRate,
					Message: fmt.Sprintf("上游倍率 ×%s → ×%s（%+.1f%%）",
						strconv.FormatFloat(oldRate, 'f', 4, 64),
						strconv.FormatFloat(newRate, 'f', 4, 64),
						pct),
					CreatedAt: now,
				})
			}
		}
	}
	return alerts
}

// UpstreamRateChangePct reports the relative move between two rates.
func UpstreamRateChangePct(oldRate, newRate float64) float64 {
	if oldRate == 0 {
		return 0
	}
	return (newRate - oldRate) / oldRate * 100
}

func formatUpstreamAlertMoney(value float64, unit string) string {
	text := strconv.FormatFloat(math.Round(value*10000)/10000, 'f', -1, 64)
	if unit == "" {
		return text
	}
	return text + " " + unit
}

// beginOfUpstreamBeijingDay returns local midnight in the fixed +08:00 zone.
func beginOfUpstreamBeijingDay(t time.Time) time.Time {
	local := t.In(UpstreamMonitorBeijingZone)
	return time.Date(local.Year(), local.Month(), local.Day(), 0, 0, 0, 0, UpstreamMonitorBeijingZone)
}

// UpstreamBeijingDay formats a sample time as the rollup day key.
func UpstreamBeijingDay(t time.Time) string {
	return t.In(UpstreamMonitorBeijingZone).Format("2006-01-02")
}
