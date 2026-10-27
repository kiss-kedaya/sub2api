package service

import (
	"strconv"
	"time"
)

// This is a cache-only ordering hint inside an existing eligible group and
// protocol/priority stratum. API keys remain fallback candidates; sticky and
// previous-response ownership are resolved before this optional stage.
func (s *OpsAutomationService) PreferQualifiedOAuth(group int64, model string, accounts []*Account, loads map[int64]*AccountLoadInfo, stratum func(*Account) string) []*Account {
	if s == nil || len(accounts) < 2 {
		return accounts
	}
	cfg, configured := s.config.Load().(OpsAutomationConfig)
	if !configured {
		return accounts
	}
	v := cfg.OAuthFallback
	if !v.Enabled || !containsAutomationID(v.GroupIDs, group) {
		return accounts
	}
	key := automationRankKey(group, model)
	s.mu.Lock()
	entry, ok := s.ranks[key]
	if !ok || time.Now().After(entry.expires) {
		if !s.rankPending[key] && len(s.rankPending) < 32 {
			ids := []int64{}
			for _, a := range accounts {
				if a != nil {
					ids = append(ids, a.ID)
				}
			}
			select {
			case s.rankRequests <- AutomationRankQuery{Revision: cfg.Revision, GroupID: group, Model: model, AccountIDs: ids, Since: time.Now().Add(-time.Duration(cfg.Ranking.WindowMinutes) * time.Minute)}:
				s.rankPending[key] = true
			default:
			}
		}
		s.mu.Unlock()
		return accounts
	}
	s.mu.Unlock()
	pools := map[string][]*Account{}
	slots := map[string][]int{}
	for i, a := range accounts {
		if a == nil || a.Type != AccountTypeAPIKey && a.Type != AccountTypeOAuth {
			continue
		}
		k := stratum(a)
		pools[k] = append(pools[k], a)
		slots[k] = append(slots[k], i)
	}
	out := append([]*Account(nil), accounts...)
	now := time.Now()
	for k, pool := range pools {
		preferred := []*Account{}
		apiFallback := []*Account{}
		fallback := []*Account{}
		for _, a := range pool {
			signal, known := entry.signals[a.ID]
			load := loads[a.ID]
			_, quotaKnown := resolveAccountExtraNumber(a.Extra, "codex_primary_used_percent", "codex_7d_used_percent")
			qualified := a.Type == AccountTypeOAuth && a.IsOpenAI() && a.IsSchedulable() && known && signal.Quality != nil && signal.QualitySamples > 0 && *signal.Quality >= v.MinimumQuality && quotaKnown && !openAIQuotaHeadroomSnapshotStale(a.Extra, now) && openAIQuotaHeadroomFactor(a, now)*100 >= v.MinimumQuotaPercent && load != nil && load.LoadRate < v.MaximumLoadPercent && load.CurrentConcurrency < a.Concurrency
			if qualified {
				preferred = append(preferred, a)
			} else if a.Type == AccountTypeAPIKey {
				apiFallback = append(apiFallback, a)
			} else {
				fallback = append(fallback, a)
			}
		}
		ordered := append(append(preferred, apiFallback...), fallback...)
		for i, a := range ordered {
			out[slots[k][i]] = a
		}
	}
	return out
}
func oauthFallbackStratum(a *Account, compact, native bool) string {
	k := a.Platform + ":" + a.GetAPIProtocol() + ":" + strconv.Itoa(a.Priority)
	if compact {
		k += ":c" + strconv.Itoa(openAICompactSupportTier(a))
	}
	if native {
		k += ":n" + strconv.FormatBool(openAIAccountServesInboundResponsesNatively(a))
	}
	return k
}
