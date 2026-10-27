package service

import "strconv"

// Reorder only existing slots in the same scheduling stratum. No new account
// enters this pool and no qualification, sticky binding or profit gate moves.
func (s *OpenAIGatewayService) rankAutomationPool(groupID *int64, model string, accounts []*Account, compact, native, mapping bool) []*Account {
	if s == nil || s.opsAutomation == nil || groupID == nil || len(accounts) < 2 {
		return accounts
	}
	// Prime the cache with the complete eligible pool before partitioning. The
	// result is intentionally ignored: later calls reorder only compatible slots.
	_ = s.opsAutomation.RankAccounts(*groupID, model, accounts)
	slots := map[string][]int{}
	pools := map[string][]*Account{}
	for i, a := range accounts {
		if a == nil {
			return accounts
		}
		key := a.Platform + ":" + a.Type + ":" + a.GetAPIProtocol() + ":" + strconv.Itoa(a.Priority)
		if compact {
			key += ":c" + strconv.Itoa(openAICompactSupportTier(a))
		}
		if native {
			key += ":n" + strconv.FormatBool(openAIAccountServesInboundResponsesNatively(a))
		}
		if mapping {
			key += ":m" + strconv.FormatBool(len(a.GetModelMapping()) > 0)
		}
		slots[key] = append(slots[key], i)
		pools[key] = append(pools[key], a)
	}
	out := append([]*Account(nil), accounts...)
	for key, pool := range pools {
		ranked := s.opsAutomation.RankAccounts(*groupID, model, pool)
		for i, a := range ranked {
			out[slots[key][i]] = a
		}
	}
	return out
}
func (s *defaultOpenAIAccountScheduler) rankAutomationCandidates(req OpenAIAccountScheduleRequest, items []openAIAccountCandidateScore) []openAIAccountCandidateScore {
	if s == nil || s.service == nil || s.service.opsAutomation == nil || req.StickyAccountID > 0 || req.StickyPreviousAccountID > 0 || req.PreviousResponseID != "" {
		return items
	}
	accounts := make([]*Account, 0, len(items))
	byID := make(map[int64]openAIAccountCandidateScore, len(items))
	for _, item := range items {
		accounts = append(accounts, item.account)
		byID[item.account.ID] = item
	}
	ordered := s.service.rankAutomationPool(req.GroupID, req.RequestedModel, accounts, req.RequireCompact, req.PreferNativeResponses, false)
	out := make([]openAIAccountCandidateScore, 0, len(items))
	for _, a := range ordered {
		out = append(out, byID[a.ID])
	}
	return out
}
func (s *OpenAIGatewayService) rankAutomationLoaded(group *int64, model string, items []accountWithLoad, compact, native bool) []accountWithLoad {
	accounts := make([]*Account, 0, len(items))
	byID := make(map[int64]accountWithLoad, len(items))
	for _, item := range items {
		accounts = append(accounts, item.account)
		byID[item.account.ID] = item
	}
	ordered := s.rankAutomationPool(group, model, accounts, compact, native, false)
	out := make([]accountWithLoad, 0, len(items))
	for _, a := range ordered {
		out = append(out, byID[a.ID])
	}
	return out
}
