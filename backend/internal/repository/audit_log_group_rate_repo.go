package repository

import (
	"context"
	"strconv"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
)

// ListGroupRateUpdateAudits returns the admin group-update audit entries for one
// group since `since`, oldest first. The caller reconstructs rate changes from
// consecutive request bodies; nothing else about the request is exposed.
//
// Filtering uses the existing audit_logs action index plus the recorded route
// params (`extra -> 'params' ->> 'id'`), so no new table or index is required.
func (r *auditLogRepository) ListGroupRateUpdateAudits(ctx context.Context, groupID int64, since time.Time, limit int) ([]service.GroupRateAuditRow, error) {
	if r == nil || r.db == nil {
		return []service.GroupRateAuditRow{}, nil
	}
	if groupID <= 0 {
		return []service.GroupRateAuditRow{}, nil
	}
	if limit <= 0 || limit > 500 {
		limit = 200
	}

	rows, err := r.db.QueryContext(ctx, `
		SELECT created_at, actor_user_id, actor_email, request_body
		FROM audit_logs
		WHERE action = 'admin.groups.update'
			AND created_at >= $1
			AND (extra -> 'params' ->> 'id') = $2
			AND status_code >= 200 AND status_code < 400
			AND request_body <> ''
		ORDER BY created_at ASC, id ASC
		LIMIT $3
	`, since.UTC(), strconv.FormatInt(groupID, 10), limit)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	out := make([]service.GroupRateAuditRow, 0, limit)
	for rows.Next() {
		var row service.GroupRateAuditRow
		var actorUserID *int64
		if err := rows.Scan(&row.CreatedAt, &actorUserID, &row.ActorEmail, &row.RequestBody); err != nil {
			return nil, err
		}
		row.ActorUserID = actorUserID
		out = append(out, row)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return out, nil
}
