package service

import (
	"context"
	"sort"
	"time"
)

// thresholdBalanceMemberIDs includes every declared wallet member even when a
// rule was saved on only one key. Membership, not the rate-probe switch, is the
// operator's declaration that another key may supply the same wallet balance.
func thresholdBalanceMemberIDs(c AccountOpsConfig) []int64 {
	ids := thresholdRuleIDs(c, "balance_threshold")
	seen := make(map[int64]bool, len(ids))
	for _, id := range ids {
		seen[id] = true
	}
	for _, group := range c.Groups {
		matched := false
		for _, id := range group.AccountIDs {
			matched = matched || seen[id]
		}
		if !matched {
			continue
		}
		for _, id := range group.AccountIDs {
			if !seen[id] {
				ids = append(ids, id)
				seen[id] = true
			}
		}
	}
	return ids
}

// opsRefreshNativeBalances runs only in the minute scanner, never on page reads.
// An enabled rule is itself an opt-in, independently of the rate-probe switch.
func (s *AccountOpsService) opsRefreshNativeBalances(ctx context.Context, members map[int64]accountOpsThresholdMember, c AccountOpsConfig) {
	if s.balanceProbe == nil {
		return
	}
	// A slow upstream must not hold the shared notification worker indefinitely.
	ctx, cancel := context.WithTimeout(ctx, accountOpsCustomUsageTimeout)
	defer cancel()
	attempts := 0
	enabledGroups := make(map[string]bool)
	for _, rule := range c.BalanceThresholds {
		if rule.Enabled {
			enabledGroups[thresholdAccountGroup(members[rule.AccountID].account, rule.AccountID, c).ID] = true
		}
	}
	ids := thresholdBalanceMemberIDs(c)
	// Oldest attempts go first so a failing key cannot starve its wallet peers.
	sort.SliceStable(ids, func(i, j int) bool {
		attempt := func(id int64) time.Time {
			if a := members[id].account; a != nil {
				if snap := decodeUpstreamBillingProbeSnapshot(a.Extra); snap != nil && snap.Balance != nil {
					return snap.Balance.LastAttemptAt
				}
			}
			return time.Time{}
		}
		return attempt(ids[i]).Before(attempt(ids[j]))
	})
	seen := make(map[string]bool)
	for _, id := range ids {
		if ctx.Err() != nil || attempts >= upstreamBillingProbeMaxPerCycle {
			break
		}
		member := members[id]
		a := member.account
		if a == nil || member.lookupErr != nil || a.Type != AccountTypeAPIKey || !a.IsActive() || opsConfiguredCustomUsage(a) {
			continue
		}
		group := accountOpsGroupAssignment(a, c)
		if seen[group.ID] || !enabledGroups[group.ID] {
			continue
		}
		now := s.balanceProbe.currentTime()
		item := opsBalanceAccount(a, now)
		if item.BalanceStatus == "ok" || item.BalanceStatus == "unlimited" || item.BalanceStatus == "forbidden" {
			if snap := decodeUpstreamBillingProbeSnapshot(a.Extra); snap != nil && snap.NextProbeAt.After(now) {
				seen[group.ID] = true
				continue
			}
		}
		if snap := decodeUpstreamBillingProbeSnapshot(a.Extra); snap != nil {
			if snap.Balance != nil && (now.Sub(snap.Balance.LastAttemptAt) < time.Minute || snap.Balance.LastError == "invalid_key" || snap.Balance.LastError == "ip_forbidden") {
				continue
			}
			if snap.NextProbeAt.After(now) && snap.Balance != nil && snap.Balance.LastError != "" {
				continue
			}
		}
		seen[group.ID] = true
		attempts++
		query, cancel := context.WithTimeout(ctx, accountOpsCustomUsageTimeout)
		snapshot, err := s.balanceProbe.ProbeAccount(query, id)
		cancel()
		if err != nil || snapshot == nil {
			continue
		}
		// The repository returns detached values. Evaluate the new snapshot in
		// this scan rather than waiting for the next database read.
		copyAccount := *a
		copyAccount.Extra = make(map[string]any, len(a.Extra)+1)
		for key, value := range a.Extra {
			copyAccount.Extra[key] = value
		}
		copyAccount.Extra[UpstreamBillingProbeExtraKey] = snapshot
		member.account = &copyAccount
		members[id] = member
	}
}
