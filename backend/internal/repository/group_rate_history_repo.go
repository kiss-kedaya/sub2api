package repository

import (
	"context"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/lib/pq"
)

// groupRateHistoryRowCap bounds one lookup. Rows come back oldest first, so a
// runaway edit loop can only drop the newest rows of the last group; the
// service then sees a stale "latest" row and withholds the hint.
const groupRateHistoryRowCap = 5000

// ListGroupRateChangesSince returns the recorded base rate changes of the given
// groups with changed_at >= since, ordered by group, time and id. It reads only
// group_rate_history through its (group_id, changed_at) index.
func (r *groupRepository) ListGroupRateChangesSince(ctx context.Context, groupIDs []int64, since time.Time) ([]service.GroupRateChange, error) {
	if r == nil || r.sql == nil || len(groupIDs) == 0 {
		return []service.GroupRateChange{}, nil
	}
	rows, err := r.sql.QueryContext(ctx, `
		SELECT id, group_id, old_rate, new_rate, changed_at
		FROM group_rate_history
		WHERE group_id = ANY($1) AND changed_at >= $2
		ORDER BY group_id, changed_at, id
		LIMIT $3`, pq.Array(groupIDs), since.UTC(), groupRateHistoryRowCap)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	out := make([]service.GroupRateChange, 0, len(groupIDs))
	for rows.Next() {
		var row service.GroupRateChange
		if err := rows.Scan(&row.ID, &row.GroupID, &row.OldRate, &row.NewRate, &row.ChangedAt); err != nil {
			return nil, err
		}
		out = append(out, row)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return out, nil
}
