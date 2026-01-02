package service

import (
	"context"
	"encoding/json"
	"errors"
	"log"
	"strings"
	"sync"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/pkg/usagestats"
)

type geminiModelClass string

const (
	geminiModelPro   geminiModelClass = "pro"
	geminiModelFlash geminiModelClass = "flash"
)

type GeminiDailyQuota struct {
	ProRPD   int64
	FlashRPD int64
}

type GeminiTierPolicy struct {
	Quota    GeminiDailyQuota
	Cooldown time.Duration
}

type GeminiQuotaPolicy struct {
	tiers map[string]GeminiTierPolicy
}

type GeminiUsageTotals struct {
	ProRequests   int64
	FlashRequests int64
	ProTokens     int64
	FlashTokens   int64
	ProCost       float64
	FlashCost     float64
}

const geminiQuotaCacheTTL = time.Minute

type geminiQuotaOverrides struct {
	Tiers map[string]config.GeminiTierQuotaConfig `json:"tiers"`
}

type GeminiQuotaService struct {
	cfg         *config.Config
	settingRepo SettingRepository
	mu          sync.Mutex
	cachedAt    time.Time
	policy      *GeminiQuotaPolicy
}

func NewGeminiQuotaService(cfg *config.Config, settingRepo SettingRepository) *GeminiQuotaService {
	return &GeminiQuotaService{
		cfg:         cfg,
		settingRepo: settingRepo,
	}
}

func (s *GeminiQuotaService) Policy(ctx context.Context) *GeminiQuotaPolicy {
	if s == nil {
		return newGeminiQuotaPolicy()
	}

	now := time.Now()
	s.mu.Lock()
	if s.policy != nil && now.Sub(s.cachedAt) < geminiQuotaCacheTTL {
		policy := s.policy
		s.mu.Unlock()
		return policy
	}
	s.mu.Unlock()

	policy := newGeminiQuotaPolicy()
	if s.cfg != nil {
		policy.ApplyOverrides(s.cfg.Gemini.Quota.Tiers)
		if strings.TrimSpace(s.cfg.Gemini.Quota.Policy) != "" {
			var overrides geminiQuotaOverrides
			if err := json.Unmarshal([]byte(s.cfg.Gemini.Quota.Policy), &overrides); err != nil {
				log.Printf("gemini quota: parse config policy failed: %v", err)
			} else {
				policy.ApplyOverrides(overrides.Tiers)
			}
		}
	}

	if s.settingRepo != nil {
		value, err := s.settingRepo.GetValue(ctx, SettingKeyGeminiQuotaPolicy)
		if err != nil && !errors.Is(err, ErrSettingNotFound) {
			log.Printf("gemini quota: load setting failed: %v", err)
		} else if strings.TrimSpace(value) != "" {
			var overrides geminiQuotaOverrides
			if err := json.Unmarshal([]byte(value), &overrides); err != nil {
				log.Printf("gemini quota: parse setting failed: %v", err)
			} else {
				policy.ApplyOverrides(overrides.Tiers)
			}
		}
	}

	s.mu.Lock()
	s.policy = policy
	s.cachedAt = now
	s.mu.Unlock()

	return policy
}

func (s *GeminiQuotaService) QuotaForAccount(ctx context.Context, account *Account) (GeminiDailyQuota, bool) {
	if account == nil || !account.IsGeminiCodeAssist() {
		return GeminiDailyQuota{}, false
	}
	policy := s.Policy(ctx)
	return policy.QuotaForTier(account.GeminiTierID())
}

func (s *GeminiQuotaService) CooldownForTier(ctx context.Context, tierID string) time.Duration {
	policy := s.Policy(ctx)
	return policy.CooldownForTier(tierID)
}

func newGeminiQuotaPolicy() *GeminiQuotaPolicy {
	return &GeminiQuotaPolicy{
		tiers: map[string]GeminiTierPolicy{
			"LEGACY": {Quota: GeminiDailyQuota{ProRPD: 50, FlashRPD: 1500}, Cooldown: 30 * time.Minute},
			"PRO":    {Quota: GeminiDailyQuota{ProRPD: 1500, FlashRPD: 4000}, Cooldown: 5 * time.Minute},
			"ULTRA":  {Quota: GeminiDailyQuota{ProRPD: 2000, FlashRPD: 0}, Cooldown: 5 * time.Minute},
		},
	}
}

func (p *GeminiQuotaPolicy) ApplyOverrides(tiers map[string]config.GeminiTierQuotaConfig) {
	if p == nil || len(tiers) == 0 {
		return
	}
	for rawID, override := range tiers {
		tierID := normalizeGeminiTierID(rawID)
		if tierID == "" {
			continue
		}
		policy, ok := p.tiers[tierID]
		if !ok {
			policy = GeminiTierPolicy{Cooldown: 5 * time.Minute}
		}
		if override.ProRPD != nil {
			policy.Quota.ProRPD = clampGeminiQuotaInt64(*override.ProRPD)
		}
		if override.FlashRPD != nil {
			policy.Quota.FlashRPD = clampGeminiQuotaInt64(*override.FlashRPD)
		}
		if override.CooldownMinutes != nil {
			minutes := clampGeminiQuotaInt(*override.CooldownMinutes)
			policy.Cooldown = time.Duration(minutes) * time.Minute
		}
		p.tiers[tierID] = policy
	}
}

func (p *GeminiQuotaPolicy) QuotaForTier(tierID string) (GeminiDailyQuota, bool) {
	policy, ok := p.policyForTier(tierID)
	if !ok {
		return GeminiDailyQuota{}, false
	}
	return policy.Quota, true
}

func (p *GeminiQuotaPolicy) CooldownForTier(tierID string) time.Duration {
	policy, ok := p.policyForTier(tierID)
	if ok && policy.Cooldown > 0 {
		return policy.Cooldown
	}
	return 5 * time.Minute
}

func (p *GeminiQuotaPolicy) policyForTier(tierID string) (GeminiTierPolicy, bool) {
	if p == nil {
		return GeminiTierPolicy{}, false
	}
	normalized := normalizeGeminiTierID(tierID)
	if normalized == "" {
		normalized = "LEGACY"
	}
	if policy, ok := p.tiers[normalized]; ok {
		return policy, true
	}
	policy, ok := p.tiers["LEGACY"]
	return policy, ok
}

func normalizeGeminiTierID(tierID string) string {
	return strings.ToUpper(strings.TrimSpace(tierID))
}

func clampGeminiQuotaInt64(value int64) int64 {
	if value < 0 {
		return 0
	}
	return value
}

func clampGeminiQuotaInt(value int) int {
	if value < 0 {
		return 0
	}
	return value
}

func geminiCooldownForTier(tierID string) time.Duration {
	policy := newGeminiQuotaPolicy()
	return policy.CooldownForTier(tierID)
}

func geminiModelClassFromName(model string) geminiModelClass {
	name := strings.ToLower(strings.TrimSpace(model))
	if strings.Contains(name, "flash") || strings.Contains(name, "lite") {
		return geminiModelFlash
	}
	return geminiModelPro
}

func geminiAggregateUsage(stats []usagestats.ModelStat) GeminiUsageTotals {
	var totals GeminiUsageTotals
	for _, stat := range stats {
		switch geminiModelClassFromName(stat.Model) {
		case geminiModelFlash:
			totals.FlashRequests += stat.Requests
			totals.FlashTokens += stat.TotalTokens
			totals.FlashCost += stat.ActualCost
		default:
			totals.ProRequests += stat.Requests
			totals.ProTokens += stat.TotalTokens
			totals.ProCost += stat.ActualCost
		}
	}
	return totals
}

func geminiQuotaLocation() *time.Location {
	loc, err := time.LoadLocation("America/Los_Angeles")
	if err != nil {
		return time.FixedZone("PST", -8*3600)
	}
	return loc
}

func geminiDailyWindowStart(now time.Time) time.Time {
	loc := geminiQuotaLocation()
	localNow := now.In(loc)
	return time.Date(localNow.Year(), localNow.Month(), localNow.Day(), 0, 0, 0, 0, loc)
}

func geminiDailyResetTime(now time.Time) time.Time {
	loc := geminiQuotaLocation()
	localNow := now.In(loc)
	start := time.Date(localNow.Year(), localNow.Month(), localNow.Day(), 0, 0, 0, 0, loc)
	reset := start.Add(24 * time.Hour)
	if !reset.After(localNow) {
		reset = reset.Add(24 * time.Hour)
	}
	return reset
}
