package service

import (
	"context"
	"errors"
	"slices"
	"strings"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/localreasoning"
	"github.com/google/uuid"
)

type OAuthFallbackConfig struct {
	Enabled             bool    `json:"enabled"`
	GroupIDs            []int64 `json:"group_ids"`
	MinimumQuality      float64 `json:"minimum_quality"`
	MinimumQuotaPercent float64 `json:"minimum_quota_percent"`
	MaximumLoadPercent  int     `json:"maximum_load_percent"`
}
type OAuthAdmissionConfig struct {
	Enabled   bool     `json:"enabled"`
	Platforms []string `json:"platforms"`
	Model     string   `json:"model"`
}

func validateOAuthOperations(c OpsAutomationConfig) error {
	if e := validateAutomationGroups(c.OAuthFallback.GroupIDs, c.OAuthFallback.Enabled); e != nil {
		return e
	}
	v := c.OAuthFallback
	if !(v.MinimumQuality >= 0 && v.MinimumQuality <= 1) || !(v.MinimumQuotaPercent >= 0 && v.MinimumQuotaPercent <= 100) || v.MaximumLoadPercent < 0 || v.MaximumLoadPercent > 100 {
		return errors.New("invalid OAuth fallback thresholds")
	}
	if c.Admission.Enabled && (len(c.Admission.Platforms) == 0 || strings.TrimSpace(c.Admission.Model) == "" || len(c.Admission.Model) > 200 || strings.ContainsAny(c.Admission.Model, "\r\n\t")) {
		return errors.New("admission requires a model and platforms")
	}
	seen := map[string]bool{}
	for _, p := range c.Admission.Platforms {
		if !slices.Contains([]string{PlatformOpenAI, PlatformAnthropic, PlatformGemini, PlatformGrok}, p) || seen[p] {
			return errors.New("invalid admission platform")
		}
		seen[p] = true
	}
	return nil
}
func (s *adminServiceImpl) PrepareOAuthAdmission(ctx context.Context, a *Account) error {
	if a == nil {
		return ErrAccountNilInput
	}
	if a.Extra == nil {
		a.Extra = map[string]any{}
	}
	delete(a.Extra, OAuthAdmissionPauseKey)
	delete(a.Extra, LocalQualityModelPausesKey)
	delete(a.Extra, "local_reasoning_quality_pauses")
	if a.Type != AccountTypeOAuth || !a.Schedulable || s.settingService == nil {
		return nil
	}
	c, e := loadOpsAutomation(ctx, s.settingService.settingRepo)
	if e != nil {
		return e
	}
	if !c.Admission.Enabled || !slices.Contains(c.Admission.Platforms, a.Platform) {
		return nil
	}
	a.Extra[OAuthAdmissionPauseKey] = map[string]any{"token": uuid.NewString(), "model": c.Admission.Model}
	a.Schedulable = false
	return nil
}

type OAuthAdmissionJob struct {
	AccountID           int64
	Token, Lease, Model string
}
type oauthAdmissionRepository interface {
	ClaimOAuthAdmission(context.Context) (*OAuthAdmissionJob, error)
	CompleteOAuthAdmission(context.Context, *OAuthAdmissionJob, string) error
}

func (s *OpsAutomationService) runOAuthAdmission(ctx context.Context) {
	c := s.config.Load().(OpsAutomationConfig)
	if !c.Admission.Enabled || s.tester == nil {
		return
	}
	repo, ok := s.repo.(oauthAdmissionRepository)
	if !ok {
		return
	}
	query, stop := context.WithTimeout(ctx, 3*time.Second)
	job, e := repo.ClaimOAuthAdmission(query)
	stop()
	if e != nil || job == nil {
		return
	}
	outcome := "inconclusive"
	q, e := localreasoning.Generate(nil)
	if e == nil {
		sampleCtx, done := context.WithTimeout(ctx, 90*time.Second)
		sample, err := runLocalQualitySample(sampleCtx, s.tester, job.AccountID, job.Model, q.Prompt)
		cancelled := sampleCtx.Err() != nil
		done()
		if err == nil && !cancelled && sample != nil && sample.Status == "success" {
			outcome = "failed"
			if localreasoning.Matches(q, sample.ResponseText) {
				outcome = "passed"
			}
		}
	}
	save, done := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer done()
	_ = repo.CompleteOAuthAdmission(save, job, outcome)
}
