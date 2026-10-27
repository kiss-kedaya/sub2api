package service

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"time"
)

func opsFlag(v *bool) bool { return v == nil || *v }
func opsBool(v bool) *bool { return &v }
func normalizeOpsFlags(c *AccountOpsConfig, old AccountOpsConfig) {
	for i := range c.BalanceThresholds {
		r := &c.BalanceThresholds[i]
		for _, prev := range old.BalanceThresholds {
			if prev.AccountID == r.AccountID {
				if r.NotifyAlert == nil {
					r.NotifyAlert = prev.NotifyAlert
				}
				if r.NotifyRecovery == nil {
					r.NotifyRecovery = prev.NotifyRecovery
				}
			}
		}
		if r.NotifyAlert == nil {
			r.NotifyAlert = opsBool(true)
		}
		if r.NotifyRecovery == nil {
			r.NotifyRecovery = opsBool(true)
		}
	}
	for i := range c.QuotaThresholds {
		r := &c.QuotaThresholds[i]
		for _, prev := range old.QuotaThresholds {
			if prev.AccountID == r.AccountID {
				if r.NotifyAlert == nil {
					r.NotifyAlert = prev.NotifyAlert
				}
				if r.NotifyRecovery == nil {
					r.NotifyRecovery = prev.NotifyRecovery
				}
			}
		}
		if r.NotifyAlert == nil {
			r.NotifyAlert = opsBool(true)
		}
		if r.NotifyRecovery == nil {
			r.NotifyRecovery = opsBool(true)
		}
	}
}

// Criteria intentionally exclude notification switches: editing delivery preferences
// cannot create another adverse episode.
func opsCriteria(c AccountOpsConfig, id int64, kind string) (string, bool, bool, bool) {
	var raw []byte
	var enabled, alert, recovery bool
	if kind == "balance_threshold" {
		for _, r := range c.BalanceThresholds {
			if r.AccountID == id {
				raw, _ = json.Marshal([]any{r.Threshold, r.Unit})
				enabled = r.Enabled
				alert = opsFlag(r.NotifyAlert)
				recovery = opsFlag(r.NotifyRecovery)
				break
			}
		}
	} else {
		for _, r := range c.QuotaThresholds {
			if r.AccountID == id {
				window := r.Window
				if window == "" {
					window = "any"
				}
				raw, _ = json.Marshal([]any{r.ThresholdPercent, window})
				enabled = r.Enabled
				alert = opsFlag(r.NotifyAlert)
				recovery = opsFlag(r.NotifyRecovery)
				break
			}
		}
	}
	if raw == nil {
		return "", false, false, false
	}
	h := sha256.Sum256(raw)
	return hex.EncodeToString(h[:]), enabled, alert, recovery
}

type accountOpsThresholdRepository interface {
	ObserveThreshold(context.Context, AccountOpsEvent, string, time.Time, bool, bool) error
	ThresholdEventEligible(context.Context, *AccountOpsEvent) (bool, error)
}

func (s *AccountOpsService) observeThreshold(ctx context.Context, a *Account, id int64, kind string, c AccountOpsConfig, now time.Time, invalid bool) error {
	repo, ok := s.repo.(accountOpsThresholdRepository)
	if !ok {
		return nil
	}
	criteria, enabled, alert, recovery := opsCriteria(c, id, kind)
	e := AccountOpsEvent{AccountID: id, Kind: kind, Criteria: criteria}
	state := "unknown"
	if invalid || !enabled {
		state = "invalid"
	} else if a != nil {
		var assessed *AccountOpsEvent
		assessed, state = s.assessThreshold(ctx, a, kind, c, now)
		e.Identity = opsAccountIdentity(a)
		e.AccountName = a.Name
		if assessed != nil {
			e = *assessed
			e.Criteria = criteria
		}
		if state == "suppressed" {
			state = "unknown"
		}
		if (kind == "balance_threshold" && a.Type != AccountTypeAPIKey) || (kind == "quota_threshold" && a.Type != AccountTypeOAuth) {
			state = "invalid"
		}
	}
	return repo.ObserveThreshold(ctx, e, state, now, alert, recovery)
}

// ThresholdCriteria exposes only an opaque criteria fingerprint for repository
// invalidation; raw account credentials are never part of settings or public DTOs.
func (c AccountOpsConfig) ThresholdCriteria(id int64, kind string) (string, bool, bool, bool) {
	return opsCriteria(c, id, kind)
}

// ThresholdGroupCriteria matches the fingerprint used by grouped observations.
func (c AccountOpsConfig) ThresholdGroupCriteria(groupID, kind string) (string, bool, bool, bool) {
	members := make([]accountOpsThresholdMember, 0)
	for _, id := range thresholdRuleIDs(c, kind) {
		if thresholdAccountGroup(nil, id, c).ID == groupID {
			members = append(members, accountOpsThresholdMember{id: id})
		}
	}
	return accountOpsGroupCriteria(c, kind, members)
}
