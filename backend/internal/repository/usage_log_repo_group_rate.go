package repository

import (
	"context"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/usagestats"
	"github.com/lib/pq"
)

// GetGroupRateBuckets aggregates usage_logs into (group_id, rate_multiplier)
// buckets for a single user inside [startTime, endTime).
//
// It backs the channel-status "rate changed in the last 24h" hint and is
// intentionally narrow:
//   - one bounded read on idx_usage_logs_user_created (user_id, created_at),
//     then a hash aggregate per group; the group list is capped by the caller;
//   - only counters and timestamps leave the database, never usage rows.
//
// rate_multiplier is the multiplier actually applied when the request was
// billed (group default or the user's custom override), so a distinct value in
// this window means the user-visible rate really changed.
func (r *usageLogRepository) GetGroupRateBuckets(
	ctx context.Context,
	userID int64,
	groupIDs []int64,
	startTime, endTime time.Time,
) ([]usagestats.GroupRateBucket, error) {
	if len(groupIDs) == 0 {
		return []usagestats.GroupRateBucket{}, nil
	}

	const query = `
		SELECT
			group_id,
			rate_multiplier,
			COUNT(*) AS requests,
			MIN(created_at) AS first_seen_at,
			MAX(created_at) AS last_seen_at
		FROM usage_logs
		WHERE user_id = $1
			AND group_id = ANY($2)
			AND created_at >= $3
			AND created_at < $4
			AND rate_multiplier IS NOT NULL
			AND rate_multiplier > 0
		GROUP BY group_id, rate_multiplier
		ORDER BY group_id ASC, last_seen_at DESC
	`

	rows, err := r.sql.QueryContext(ctx, query, userID, pq.Array(groupIDs), startTime, endTime)
	if err != nil {
		return nil, err
	}
	defer func() {
		_ = rows.Close()
	}()

	buckets := make([]usagestats.GroupRateBucket, 0, len(groupIDs))
	for rows.Next() {
		var bucket usagestats.GroupRateBucket
		if err := rows.Scan(
			&bucket.GroupID,
			&bucket.RateMultiplier,
			&bucket.Requests,
			&bucket.FirstSeenAt,
			&bucket.LastSeenAt,
		); err != nil {
			return nil, err
		}
		buckets = append(buckets, bucket)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return buckets, nil
}
