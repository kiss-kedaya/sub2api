package repository

import (
	"context"
	"time"

	"github.com/lib/pq"
)

// GetGroupActiveUserCounts returns COUNT(DISTINCT user_id) per group for every
// request logged at or after `since`.
//
// This is the honest replacement for sessions_used on groups whose accounts do
// not enable max_sessions: session counters only exist for limited accounts, so
// a zero there does not mean "no users". The query is bounded by
// idx_usage_logs_group_created_at_not_null (group_id, created_at) and only
// aggregates rows inside the short window the caller passes (5 minutes), so it
// never scans the whole table.
func (r *usageLogRepository) GetGroupActiveUserCounts(
	ctx context.Context,
	groupIDs []int64,
	since time.Time,
) (map[int64]int, error) {
	counts := make(map[int64]int, len(groupIDs))
	if len(groupIDs) == 0 {
		return counts, nil
	}

	const query = `
		SELECT group_id, COUNT(DISTINCT user_id) AS active_users
		FROM usage_logs
		WHERE created_at >= $1
			AND group_id = ANY($2)
			AND user_id > 0
		GROUP BY group_id
	`

	rows, err := r.sql.QueryContext(ctx, query, since, pq.Array(groupIDs))
	if err != nil {
		return nil, err
	}
	defer func() {
		_ = rows.Close()
	}()

	for rows.Next() {
		var groupID int64
		var activeUsers int64
		if err := rows.Scan(&groupID, &activeUsers); err != nil {
			return nil, err
		}
		counts[groupID] = int(activeUsers)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return counts, nil
}
