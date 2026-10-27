package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"time"
)

const OpsAutomationSettingsKey = "ops_automation_v1"

type OAuthDefaults struct {
	Enabled     bool    `json:"enabled"`
	Platform    string  `json:"platform"`
	Concurrency int     `json:"concurrency"`
	Priority    int     `json:"priority"`
	LoadFactor  int     `json:"load_factor"`
	GroupIDs    []int64 `json:"group_ids"`
}
type ConcurrencyRampConfig struct {
	Enabled          bool    `json:"enabled"`
	GroupIDs         []int64 `json:"group_ids"`
	SuccessesPerStep int     `json:"successes_per_step"`
	Step             int     `json:"step"`
	Maximum          int     `json:"maximum"`
	CooldownSeconds  int     `json:"cooldown_seconds"`
}
type WithinGroupRankingConfig struct {
	Enabled        bool    `json:"enabled"`
	GroupIDs       []int64 `json:"group_ids"`
	Mode           string  `json:"mode"`
	WindowMinutes  int     `json:"window_minutes"`
	MinimumSamples int     `json:"minimum_samples"`
	QualityWeight  float64 `json:"quality_weight"`
	LatencyWeight  float64 `json:"latency_weight"`
	CostWeight     float64 `json:"cost_weight"`
}
type OpsAutomationConfig struct {
	OAuthFallback OAuthFallbackConfig      `json:"oauth_fallback"`
	Admission     OAuthAdmissionConfig     `json:"admission"`
	RampSuspended bool                     `json:"ramp_suspended,omitempty"`
	Revision      string                   `json:"revision"`
	UpdatedAt     time.Time                `json:"updated_at"`
	OAuth         []OAuthDefaults          `json:"oauth"`
	Ramp          ConcurrencyRampConfig    `json:"ramp"`
	Ranking       WithinGroupRankingConfig `json:"ranking"`
}

func DefaultOpsAutomationConfig() OpsAutomationConfig {
	c := OpsAutomationConfig{OAuth: []OAuthDefaults{}, Ramp: ConcurrencyRampConfig{GroupIDs: []int64{}, SuccessesPerStep: 20, Step: 1, Maximum: 100, CooldownSeconds: 60}, Ranking: WithinGroupRankingConfig{GroupIDs: []int64{}, Mode: "balanced", WindowMinutes: 60, MinimumSamples: 10, QualityWeight: 30, LatencyWeight: 45, CostWeight: 25}}
	c.OAuthFallback = OAuthFallbackConfig{GroupIDs: []int64{}, MinimumQuality: 1, MinimumQuotaPercent: 10, MaximumLoadPercent: 90}
	c.Admission = OAuthAdmissionConfig{Platforms: []string{}}
	for _, p := range []string{PlatformOpenAI, PlatformAnthropic, PlatformGemini, PlatformAntigravity, PlatformGrok} {
		c.OAuth = append(c.OAuth, OAuthDefaults{Platform: p, Concurrency: 3, Priority: 50, LoadFactor: 1, GroupIDs: []int64{}})
	}
	return c
}
func validateAutomationGroups(ids []int64, enabled bool) error {
	if len(ids) > 100 || enabled && len(ids) == 0 {
		return errors.New("select between 1 and 100 groups before enabling")
	}
	seen := map[int64]bool{}
	for _, id := range ids {
		if id <= 0 || seen[id] {
			return errors.New("group IDs must be positive and unique")
		}
		seen[id] = true
	}
	return nil
}
func ValidateOpsAutomationConfig(c OpsAutomationConfig) error {
	if err := validateOAuthOperations(c); err != nil {
		return err
	}
	if len(c.OAuth) > 5 {
		return errors.New("at most one template per OAuth platform")
	}
	seen := map[string]bool{}
	for _, v := range c.OAuth {
		if !slices.Contains([]string{PlatformOpenAI, PlatformAnthropic, PlatformGemini, PlatformAntigravity, PlatformGrok}, v.Platform) || seen[v.Platform] {
			return errors.New("invalid or duplicate OAuth platform")
		}
		seen[v.Platform] = true
		if v.Concurrency < 1 || v.Concurrency > 10000 || v.Priority < 0 || v.Priority > 10000 || v.LoadFactor < 1 || v.LoadFactor > 10000 {
			return errors.New("invalid OAuth initial fields")
		}
		if err := validateAutomationGroups(v.GroupIDs, v.Enabled); err != nil {
			return err
		}
	}
	if err := validateAutomationGroups(c.Ramp.GroupIDs, c.Ramp.Enabled); err != nil {
		return err
	}
	if c.Ramp.SuccessesPerStep < 1 || c.Ramp.SuccessesPerStep > 100000 || c.Ramp.Step < 1 || c.Ramp.Step > 1000 || c.Ramp.Maximum < 1 || c.Ramp.Maximum > 10000 || c.Ramp.CooldownSeconds < 1 || c.Ramp.CooldownSeconds > 86400 {
		return errors.New("invalid concurrency ramp limits")
	}
	if err := validateAutomationGroups(c.Ranking.GroupIDs, c.Ranking.Enabled); err != nil {
		return err
	}
	if !slices.Contains([]string{"experience", "balanced", "cost", "custom"}, c.Ranking.Mode) || c.Ranking.WindowMinutes < 5 || c.Ranking.WindowMinutes > 1440 || c.Ranking.MinimumSamples < 1 || c.Ranking.MinimumSamples > 100000 {
		return errors.New("invalid ranking window or mode")
	}
	sum := 0.0
	for _, w := range []float64{c.Ranking.QualityWeight, c.Ranking.LatencyWeight, c.Ranking.CostWeight} {
		if !(w >= 0 && w <= 100) {
			return errors.New("ranking weights must be finite and between 0 and 100")
		}
		sum += w
	}
	if sum <= 0 {
		return errors.New("at least one ranking weight must be positive")
	}
	return nil
}
func loadOpsAutomation(ctx context.Context, repo SettingRepository) (OpsAutomationConfig, error) {
	c := DefaultOpsAutomationConfig()
	if repo == nil {
		return c, nil
	}
	raw, err := repo.GetValue(ctx, OpsAutomationSettingsKey)
	if errors.Is(err, ErrSettingNotFound) {
		return c, nil
	}
	if err != nil {
		return c, err
	}
	if raw != "" {
		if err = json.Unmarshal([]byte(raw), &c); err != nil {
			return c, err
		}
	}
	return c, ValidateOpsAutomationConfig(c)
}

// ApplyOAuthDefaults changes only explicitly enabled initial fields. It never
// creates aliases, modifies prices or rewrites credentials supplied by the admin.
func (s *adminServiceImpl) ApplyOAuthDefaults(ctx context.Context, input *CreateAccountInput) error {
	if input == nil {
		return ErrAccountNilInput
	}
	if input.Type != AccountTypeOAuth || s.settingService == nil {
		return nil
	}
	if adminID, _, scoped := AccountOwnerScopeDetail(ctx); scoped && adminID != 1 {
		return nil
	}
	c, err := loadOpsAutomation(ctx, s.settingService.settingRepo)
	if err != nil {
		return fmt.Errorf("read OAuth template: %w", err)
	}
	for _, v := range c.OAuth {
		if v.Enabled && v.Platform == input.Platform {
			for _, id := range v.GroupIDs {
				g, err := s.groupRepo.GetByID(ctx, id)
				if err != nil {
					return err
				}
				if g.Status != StatusActive || g.Platform != v.Platform {
					return errors.New("OAuth template group is unavailable")
				}
			}
			input.Concurrency = v.Concurrency
			input.Priority = v.Priority
			lf := v.LoadFactor
			input.LoadFactor = &lf
			input.GroupIDs = append([]int64(nil), v.GroupIDs...)
			return nil
		}
	}
	return nil
}
