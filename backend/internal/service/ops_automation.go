package service

import (
	"context"
	"errors"
	"sort"
	"strconv"
	"sync"
	"sync/atomic"
	"time"

	"github.com/google/uuid"
)

type AutomationRankQuery struct {
	Revision   string
	GroupID    int64
	Model      string
	AccountIDs []int64
	Since      time.Time
}
type AutomationRankSignal struct {
	Samples int
	P90MS   float64
	Quality *float64
}
type LocalReasoningPlan struct {
	ID              int64     `json:"id"`
	AccountID       int64     `json:"account_id"`
	Model           string    `json:"model"`
	Enabled         bool      `json:"enabled"`
	IntervalMinutes int       `json:"interval_minutes"`
	Action          string    `json:"action"`
	AutoRestore     bool      `json:"auto_restore"`
	Revision        int64     `json:"revision"`
	LastOutcome     string    `json:"last_outcome"`
	NextRunAt       time.Time `json:"next_run_at"`
	Lease           string    `json:"-"`
	JudgeKind       string    `json:"judge_kind"`
	JudgeGroupID    int64     `json:"judge_group_id"`
	JudgeModel      string    `json:"judge_model"`
}
type LocalReasoningResult struct {
	QuestionID string `json:"question_id"`
	Family     string `json:"family"`
	Outcome    string `json:"outcome"`
	DurationMS int64  `json:"duration_ms"`
}
type LocalReasoningOwnership struct {
	PlanID     int64  `json:"plan_id"`
	AccountID  int64  `json:"account_id"`
	Owned      bool   `json:"owned"`
	Fence      string `json:"fence"`
	CanRestore bool   `json:"can_restore"`
}
type OpsAutomationRepository interface {
	SaveConfig(context.Context, OpsAutomationConfig, string) error
	RecordOutcome(context.Context, AutomationOutcome, OpsAutomationConfig) error
	RankingSignals(context.Context, AutomationRankQuery) (map[int64]AutomationRankSignal, error)
	ListLocalPlans(context.Context) ([]LocalReasoningPlan, error)
	SaveLocalPlan(context.Context, LocalReasoningPlan) (*LocalReasoningPlan, error)
	ClaimLocalPlan(context.Context, int64) (*LocalReasoningPlan, error)
	CompleteLocalPlan(context.Context, *LocalReasoningPlan, LocalReasoningResult) error
	LocalOwnership(context.Context, int64, string, bool, bool) (*LocalReasoningOwnership, error)
}
type automationRankEntry struct {
	signals map[int64]AutomationRankSignal
	expires time.Time
}
type OpsAutomationService struct {
	settings     SettingRepository
	groups       GroupRepository
	repo         OpsAutomationRepository
	tester       *AccountTestService
	judge        *LocalQualityJudge
	config       atomic.Value
	blocked      atomic.Bool
	outcomes     chan AutomationOutcome
	rankRequests chan AutomationRankQuery
	mu           sync.Mutex
	ranks        map[string]automationRankEntry
	rankPending  map[string]bool
	cancel       context.CancelFunc
	wg           sync.WaitGroup
}

func NewOpsAutomationService(settings SettingRepository, groups GroupRepository, repo OpsAutomationRepository, tester *AccountTestService) *OpsAutomationService {
	s := &OpsAutomationService{settings: settings, groups: groups, repo: repo, tester: tester, outcomes: make(chan AutomationOutcome, 512), rankRequests: make(chan AutomationRankQuery, 32), ranks: map[string]automationRankEntry{}, rankPending: map[string]bool{}}
	s.config.Store(DefaultOpsAutomationConfig())
	return s
}
func (s *OpsAutomationService) GetConfig(ctx context.Context) (OpsAutomationConfig, error) {
	c, err := loadOpsAutomation(ctx, s.settings)
	c.RampSuspended = s.blocked.Load()
	return c, err
}
func (s *OpsAutomationService) SaveConfig(ctx context.Context, c OpsAutomationConfig) (OpsAutomationConfig, error) {
	if err := ValidateOpsAutomationConfig(c); err != nil {
		return c, err
	}
	for _, v := range c.OAuth {
		for _, id := range v.GroupIDs {
			g, err := s.groups.GetByID(ctx, id)
			if err != nil {
				return c, err
			}
			if g.Status != StatusActive || g.Platform != v.Platform {
				return c, errors.New("OAuth template requires active same-platform groups")
			}
		}
	}
	for _, ids := range [][]int64{c.Ramp.GroupIDs, c.Ranking.GroupIDs} {
		for _, id := range ids {
			g, err := s.groups.GetByID(ctx, id)
			if err != nil {
				return c, err
			}
			if g.Status != StatusActive {
				return c, errors.New("select active groups")
			}
		}
	}
	expected := c.Revision
	c.RampSuspended = false
	c.Revision = uuid.NewString()
	c.UpdatedAt = time.Now().UTC()
	if err := s.repo.SaveConfig(ctx, c, expected); err != nil {
		return c, err
	}
	s.config.Store(c)
	s.blocked.Store(false)
	s.mu.Lock()
	s.ranks = map[string]automationRankEntry{}
	s.mu.Unlock()
	return c, nil
}
func (s *OpsAutomationService) ObserveOutcome(e AutomationOutcome) {
	if s == nil || s.blocked.Load() || e.AccountID <= 0 {
		return
	}
	c := s.config.Load().(OpsAutomationConfig)
	if !c.Ramp.Enabled {
		return
	}
	if e.ID == "" {
		e.ID = uuid.NewString()
	}
	select {
	case s.outcomes <- e:
	default:
		s.blocked.Store(true)
	}
}
func (s *OpsAutomationService) Start()        { s.start(true) }
func (s *OpsAutomationService) StartGateway() { s.start(false) }
func (s *OpsAutomationService) start(shared bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.cancel != nil {
		return
	}
	ctx, cancel := context.WithCancel(context.Background())
	s.cancel = cancel
	s.wg.Add(1)
	go func() {
		defer s.wg.Done()
		s.refresh(ctx)
		seen := map[int64]bool{}
		tick := time.NewTicker(15 * time.Second)
		defer tick.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case e := <-s.outcomes:
				if !s.blocked.Load() {
					if !seen[e.AccountID] {
						e.Reset = true
					}
					c := s.config.Load().(OpsAutomationConfig)
					q, stop := context.WithTimeout(ctx, 3*time.Second)
					err := s.repo.RecordOutcome(q, e, c)
					stop()
					if err != nil {
						s.blocked.Store(true)
					} else {
						if len(seen) >= 10000 {
							seen = map[int64]bool{}
						}
						seen[e.AccountID] = true
					}
				}
			case q := <-s.rankRequests:
				s.refreshRank(ctx, q)
			case <-tick.C:
				s.refresh(ctx)
			}
		}
	}()
	if shared {
		s.wg.Add(1)
		go func() {
			defer s.wg.Done()
			tick := time.NewTicker(30 * time.Second)
			defer tick.Stop()
			for {
				select {
				case <-ctx.Done():
					return
				case <-tick.C:
					if cleaner, ok := s.repo.(interface{ PruneAutomationHistory(context.Context) error }); ok {
						q, stop := context.WithTimeout(ctx, 3*time.Second)
						_ = cleaner.PruneAutomationHistory(q)
						stop()
					}
					s.runDueLocal(ctx)
				}
			}
		}()
	}
}
func (s *OpsAutomationService) Stop() {
	s.mu.Lock()
	cancel := s.cancel
	s.mu.Unlock()
	if cancel != nil {
		cancel()
		s.wg.Wait()
	}
}
func (s *OpsAutomationService) refresh(ctx context.Context) {
	q, stop := context.WithTimeout(ctx, 3*time.Second)
	defer stop()
	c, err := s.GetConfig(q)
	if err != nil {
		disabled := DefaultOpsAutomationConfig()
		disabled.Revision = s.config.Load().(OpsAutomationConfig).Revision
		s.config.Store(disabled)
		return
	}
	old := s.config.Load().(OpsAutomationConfig)
	if old.Revision != c.Revision {
		s.blocked.Store(false)
	}
	s.config.Store(c)
}
func automationRankKey(group int64, model string) string {
	return strconv.FormatInt(group, 10) + ":" + model
}
func (s *OpsAutomationService) refreshRank(ctx context.Context, q AutomationRankQuery) {
	key := automationRankKey(q.GroupID, q.Model)
	defer func() { s.mu.Lock(); delete(s.rankPending, key); s.mu.Unlock() }()
	c := s.config.Load().(OpsAutomationConfig)
	if !c.Ranking.Enabled || c.Revision != q.Revision {
		return
	}
	query, stop := context.WithTimeout(ctx, 5*time.Second)
	defer stop()
	signals, err := s.repo.RankingSignals(query, q)
	if err != nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.config.Load().(OpsAutomationConfig).Revision != q.Revision {
		return
	}
	if len(s.ranks) >= 32 {
		for k, v := range s.ranks {
			if time.Now().After(v.expires) {
				delete(s.ranks, k)
			}
		}
		if len(s.ranks) >= 32 {
			return
		}
	}
	s.ranks[key] = automationRankEntry{signals: signals, expires: time.Now().Add(45 * time.Second)}
}

// RankAccounts is cache-only. Call only inside an already eligible priority /
// protocol stratum, after sticky-owner resolution and existing profit gates.
func (s *OpsAutomationService) RankAccounts(groupID int64, model string, accounts []*Account) []*Account {
	if s == nil || len(accounts) < 2 || len(accounts) > 2000 {
		return accounts
	}
	c := s.config.Load().(OpsAutomationConfig)
	if !c.Ranking.Enabled || !containsAutomationID(c.Ranking.GroupIDs, groupID) || len(model) > 200 {
		return accounts
	}
	for _, a := range accounts {
		if a == nil {
			return accounts
		}
	}
	key := automationRankKey(groupID, model)
	s.mu.Lock()
	entry, ok := s.ranks[key]
	if !ok || time.Now().After(entry.expires) {
		if !s.rankPending[key] && len(s.rankPending) < 32 {
			ids := make([]int64, 0, len(accounts))
			for _, a := range accounts {
				ids = append(ids, a.ID)
			}
			q := AutomationRankQuery{Revision: c.Revision, GroupID: groupID, Model: model, AccountIDs: ids, Since: time.Now().Add(-time.Duration(c.Ranking.WindowMinutes) * time.Minute)}
			select {
			case s.rankRequests <- q:
				s.rankPending[key] = true
			default:
			}
		}
		s.mu.Unlock()
		return accounts
	}
	s.mu.Unlock()
	weights := [3]float64{c.Ranking.QualityWeight, c.Ranking.LatencyWeight, c.Ranking.CostWeight}
	switch c.Ranking.Mode {
	case "experience":
		weights = [3]float64{40, 55, 5}
	case "balanced":
		weights = [3]float64{30, 45, 25}
	case "cost":
		weights = [3]float64{20, 20, 60}
	}
	type ranked struct {
		a     *Account
		score float64
	}
	known := map[int][]ranked{}
	slots := map[int][]int{}
	for i, a := range accounts {
		v, found := entry.signals[a.ID]
		if !found || v.Samples < c.Ranking.MinimumSamples {
			continue
		}
		quality := 0.5
		if v.Quality != nil {
			quality = *v.Quality
		}
		latency := 1 / (1 + v.P90MS/15000)
		cost := 1 / (1 + a.BillingRateMultiplier())
		known[a.Priority] = append(known[a.Priority], ranked{a, weights[0]*quality + weights[1]*latency + weights[2]*cost})
		slots[a.Priority] = append(slots[a.Priority], i)
	}
	out := append([]*Account(nil), accounts...)
	for priority, values := range known {
		sort.SliceStable(values, func(i, j int) bool { return values[i].score > values[j].score })
		for i, v := range values {
			out[slots[priority][i]] = v.a
		}
	}
	return out
}
func containsAutomationID(ids []int64, id int64) bool {
	for _, v := range ids {
		if v == id {
			return true
		}
	}
	return false
}
