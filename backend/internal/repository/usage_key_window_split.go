package repository

import (
	"sort"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/usagestats"
)

// keyWindowSplit says where to cut one API key's multi-day window [start, end) into the days that are over and the
// day still being written: the start of today in the window's own time zone (end's location), and whether to cut at
// all. It is the local midnight, not end-24h: the day of a DST change has 23 or 25 hours.
//
// Only the class that keyAggregateTTL gives the long TTL is cut (one key, window over 25 hours). A window with no
// closed day (today starts at or before start) or without today (today starts at or after end) is answered whole, as
// before, and so is every window when there is no cache: two reads would only cost more.
//
// Where the cut falls changes nothing in the answer: the parts cover [start, end) exactly once. The cut only decides
// what can be kept.
func (r *usageLogRepository) keyWindowSplit(apiKeyID int64, start, end time.Time) (time.Time, bool) {
	if r.statsCache == nil || keyAggregateTTL(apiKeyID, start, end) == 0 {
		return time.Time{}, false
	}
	now := time.Now()
	if r.clock != nil {
		now = r.clock()
	}
	loc := end.Location()
	today := now.In(loc)
	splitAt := time.Date(today.Year(), today.Month(), today.Day(), 0, 0, 0, 0, loc)
	if !splitAt.After(start) || !splitAt.Before(end) {
		return time.Time{}, false
	}
	return splitAt, true
}

// keyWindowPart is one read of one API key's window: the range, how long the answer is kept (usageStatsCacheKey.TTL)
// and whether it is the window that was asked for, whole.
type keyWindowPart struct {
	start, end time.Time
	ttl        time.Duration
	whole      bool
}

// keyWindowParts says how to read one API key's window [start, end) when it is not read whole, oldest first, and nil
// when it is read whole (see keyWindowSplit; a window with no whole local day over is read whole too). The parts are:
//
//   - the head [start, first): the part of the first day before the first local midnight. It is left out when the
//     window starts at a midnight, which is what the usage pages ask for. It moves with start, so it is kept for the
//     lifetime of any multi-day answer (usageStatsKeyWindowTTL) and no longer;
//   - the closed days [first, splitAt): whole local days only, kept for a day (usageStatsKeyClosedDaysTTL). This is the
//     part that costs, and its question is the same for every caller of the day;
//   - today [splitAt, end): read again every cache TTL (ttl 0), a fraction of the rows.
//
// The head is why the closed days are cut at midnights at both ends. The default window of /v1/usage is 30 days
// that move in 15 minute steps, so closed days cut at its start would ask a new question at every step and nothing
// would ever be kept.
func (r *usageLogRepository) keyWindowParts(apiKeyID int64, start, end time.Time) []keyWindowPart {
	splitAt, ok := r.keyWindowSplit(apiKeyID, start, end)
	if !ok {
		return nil
	}
	first := localMidnightAtOrAfter(start, end.Location())
	if first.Before(start) || !first.Before(splitAt) {
		return nil
	}
	parts := make([]keyWindowPart, 0, 3)
	if first.After(start) {
		parts = append(parts, keyWindowPart{start: start, end: first, ttl: usageStatsKeyWindowTTL})
	}
	return append(parts,
		keyWindowPart{start: first, end: splitAt, ttl: usageStatsKeyClosedDaysTTL},
		keyWindowPart{start: splitAt, end: end},
	)
}

// localMidnightAtOrAfter is the first local midnight in loc that is not before at.
func localMidnightAtOrAfter(at time.Time, loc *time.Location) time.Time {
	local := at.In(loc)
	midnight := time.Date(local.Year(), local.Month(), local.Day(), 0, 0, 0, 0, loc)
	if midnight.Before(at) {
		midnight = time.Date(local.Year(), local.Month(), local.Day()+1, 0, 0, 0, 0, loc)
	}
	return midnight
}

// readKeyWindow answers one API key's aggregate over [start, end) with read: in one read of the whole window (kept
// for wholeTTL) or, when keyWindowParts cuts it, in one read per part, joined by merge. Either part failing fails the
// call: half a report would be taken for the whole of it.
func readKeyWindow[T any](
	r *usageLogRepository, apiKeyID int64, start, end time.Time, wholeTTL time.Duration,
	read func(keyWindowPart) (T, error), merge func(...T) T,
) (T, error) {
	parts := r.keyWindowParts(apiKeyID, start, end)
	if parts == nil {
		return read(keyWindowPart{start: start, end: end, ttl: wholeTTL, whole: true})
	}
	answers := make([]T, 0, len(parts))
	for _, part := range parts {
		answer, err := read(part)
		if err != nil {
			var zero T
			return zero, err
		}
		answers = append(answers, answer)
	}
	return merge(answers...), nil
}

// mergeModelStats adds the parts of one model report up by model name, ordered like the query's ORDER BY total_tokens
// DESC, with the model name breaking a tie (the query leaves ties to the database; the rollup reader breaks them the
// same way). Every figure of a ModelStat is a sum, so a model found in both parts is the sum of the two rows, and a
// model found in one is that row unchanged. The result is never nil, as the query's is not.
func mergeModelStats(parts ...[]ModelStat) []ModelStat {
	byModel := make(map[string]ModelStat)
	for _, part := range parts {
		for _, m := range part {
			row := byModel[m.Model]
			row.Model = m.Model
			row.Requests += m.Requests
			row.InputTokens += m.InputTokens
			row.OutputTokens += m.OutputTokens
			row.CacheCreationTokens += m.CacheCreationTokens
			row.CacheReadTokens += m.CacheReadTokens
			row.TotalTokens += m.TotalTokens
			row.Cost += m.Cost
			row.ActualCost += m.ActualCost
			row.AccountCost += m.AccountCost
			byModel[m.Model] = row
		}
	}
	merged := make([]ModelStat, 0, len(byModel))
	for _, m := range byModel {
		merged = append(merged, m)
	}
	sort.Slice(merged, func(i, j int) bool {
		if merged[i].TotalTokens != merged[j].TotalTokens {
			return merged[i].TotalTokens > merged[j].TotalTokens
		}
		return merged[i].Model < merged[j].Model
	})
	return merged
}

// mergeAPIKeyStats adds the parts of one key's summary (getAPIKeyStatsAggregatedUncached) up. Every figure is a sum
// except the average duration, which is rebuilt from the summed numerator and denominator: this query averages
// COALESCE(duration_ms, 0), so a part's average is its durations over its requests, average times requests is the
// part's durations, and their sum over all the requests is the average of the whole. (That is not true of an
// AVG(duration_ms) that leaves out the NULLs: the rollup reader's mergeUsageStats cannot merge it for that
// reason, and is not used here.) The query fills nothing else: no account cost, no endpoint lists.
func mergeAPIKeyStats(parts ...*usagestats.UsageStats) *usagestats.UsageStats {
	merged := &usagestats.UsageStats{}
	var durationMs float64
	for _, p := range parts {
		if p == nil {
			continue
		}
		merged.TotalRequests += p.TotalRequests
		merged.TotalInputTokens += p.TotalInputTokens
		merged.TotalOutputTokens += p.TotalOutputTokens
		merged.TotalCacheTokens += p.TotalCacheTokens
		merged.TotalCacheCreationTokens += p.TotalCacheCreationTokens
		merged.TotalCacheReadTokens += p.TotalCacheReadTokens
		merged.TotalTokens += p.TotalTokens
		merged.TotalCost += p.TotalCost
		merged.TotalActualCost += p.TotalActualCost
		durationMs += p.AverageDurationMs * float64(p.TotalRequests)
	}
	if merged.TotalRequests > 0 {
		merged.AverageDurationMs = durationMs / float64(merged.TotalRequests)
	}
	return merged
}
